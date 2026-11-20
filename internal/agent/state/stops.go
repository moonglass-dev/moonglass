package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"
)

const (
	// retentionRetired is how many retired releases per app retention keeps, the
	// current and previous rows aside, so an app keeps five
	// (docs/specs/release.md, "Keep five").
	retentionRetired = 3
	// retentionFailedAge is how old a failed or aborted row must be to be
	// deleted.
	retentionFailedAge = 7 * 24 * time.Hour
	// retentionCommandAge is how old a commands row must be to be deleted.
	retentionCommandAge = 7 * 24 * time.Hour
)

// DueStop is a previous or retired release whose stop_after has come.
type DueStop struct {
	StopAfter   time.Time
	AppID       string
	ReleaseID   string
	Status      string
	ContainerID string
}

const selectDueStops = `SELECT app_id, release_id, status, container_id, stop_after
	FROM releases
	WHERE status IN (?, ?) AND stop_after IS NOT NULL AND stop_after <= ?
	ORDER BY stop_after, app_id, release_id`

// DueStops returns every previous or retired release whose stop_after is at or
// before now, ordered by stop_after, then app_id, then release_id. A current,
// candidate, failed or aborted row is never listed, and neither is a row whose
// stop is not yet due.
func (d *DB) DueStops(ctx context.Context, now time.Time) ([]DueStop, error) {
	rows, err := d.db.QueryContext(ctx, selectDueStops, statusPrevious, statusRetired, formatTime(now))
	if err != nil {
		return nil, fmt.Errorf("read due stops from agent state %s: %w", d.path, err)
	}
	defer func() { _ = rows.Close() }()

	var out []DueStop
	for rows.Next() {
		var (
			stop     DueStop
			stopText string
		)
		if err = rows.Scan(&stop.AppID, &stop.ReleaseID, &stop.Status, &stop.ContainerID, &stopText); err != nil {
			return nil, fmt.Errorf("read due stops from agent state %s: %w", d.path, err)
		}
		stop.StopAfter, err = time.Parse(time.RFC3339, stopText)
		if err != nil {
			return nil, fmt.Errorf("read due stops from agent state %s: parse stop_after %q of release %s of app %s: %w",
				d.path, stopText, stop.ReleaseID, stop.AppID, err)
		}
		out = append(out, stop)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("read due stops from agent state %s: %w", d.path, err)
	}
	return out, nil
}

const selectStopRow = `SELECT status, stop_after FROM releases WHERE app_id = ? AND release_id = ?`

const (
	clearStopAfter           = `UPDATE releases SET stop_after = NULL WHERE app_id = ? AND release_id = ? AND status = ?`
	clearStopAfterAndOwnerID = `UPDATE releases SET stop_after = NULL, container_id = '' WHERE app_id = ? AND release_id = ? AND status = ?`
)

// RecordStopped records a carried-out stop in one transaction. A previous row
// keeps its container_id and loses stop_after, so a rollback inside its window
// only starts it; a retired row loses both, because its container was removed.
// It writes nothing and returns an error when the row is unknown, is no longer
// previous or retired, is already recorded as stopped, or its stop is not yet
// due.
func (d *DB) RecordStopped(ctx context.Context, appID, releaseID string, now time.Time) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("record stop of release %s of app %s: %w", releaseID, appID, err)
	}
	defer func() { _ = tx.Rollback() }()

	var (
		status    string
		stopAfter sql.NullString
	)
	err = tx.QueryRowContext(ctx, selectStopRow, appID, releaseID).Scan(&status, &stopAfter)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("record stop of release %s of app %s: no such release in agent state %s; deploy it first",
			releaseID, appID, d.path)
	}
	if err != nil {
		return fmt.Errorf("record stop of release %s of app %s: read its row: %w", releaseID, appID, err)
	}
	if status != statusPrevious && status != statusRetired {
		return fmt.Errorf("record stop of release %s of app %s: its status is %q, not previous or retired; it is never stopped",
			releaseID, appID, status)
	}
	if !stopAfter.Valid {
		return fmt.Errorf("record stop of release %s of app %s: already recorded as stopped", releaseID, appID)
	}
	due, err := time.Parse(time.RFC3339, stopAfter.String)
	if err != nil {
		return fmt.Errorf("record stop of release %s of app %s: parse stop_after %q: %w", releaseID, appID, stopAfter.String, err)
	}
	if due.After(now) {
		return fmt.Errorf("record stop of release %s of app %s: not due until %s", releaseID, appID, stopAfter.String)
	}

	query := clearStopAfter
	if status == statusRetired {
		query = clearStopAfterAndOwnerID
	}
	if err = updateOne(ctx, tx, query, appID, releaseID, status); err != nil {
		return fmt.Errorf("record stop of release %s of app %s: %w", releaseID, appID, err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("record stop of release %s of app %s: %w", releaseID, appID, err)
	}
	return nil
}

const deleteOldFailures = `DELETE FROM releases WHERE status IN (?, ?) AND created_at < ?`

const deleteOldCommands = `DELETE FROM commands WHERE finished_at < ?`

// selectPrunableRetired lists the retired rows outside the newest
// retentionRetired of their app whose stop has run. Newest means created_at,
// then insertion order: the later row wins a tie.
const selectPrunableRetired = `SELECT app_id, release_id, image_id FROM (
	SELECT app_id, release_id, image_id, stop_after, container_id,
		ROW_NUMBER() OVER (PARTITION BY app_id ORDER BY created_at DESC, rowid DESC) AS n
	FROM releases WHERE status = ?
) WHERE n > ? AND stop_after IS NULL AND container_id = ''`

const deleteRetiredRow = `DELETE FROM releases WHERE app_id = ? AND release_id = ? AND status = ?`

const selectImageKept = `SELECT EXISTS(SELECT 1 FROM releases WHERE image_id = ? AND status IN (?, ?, ?, ?))`

// prunedRetired is one retired row retention deletes.
type prunedRetired struct {
	appID     string
	releaseID string
	imageID   string
}

// Retain applies the retention rules in one transaction: failed and aborted
// rows older than seven days go, and per app the retired rows outside the three
// newest are deleted once their stop has run. It returns the image IDs of the
// deleted rows that no remaining candidate, current, previous or retired row of
// any app names, sorted, so the caller removes exactly those images. It never
// writes an apps row. Command rows finished more than seven days ago are deleted
// too.
func (d *DB) Retain(ctx context.Context, now time.Time) ([]string, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("retain agent state %s: %w", d.path, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err = tx.ExecContext(ctx, deleteOldFailures, statusFailed, statusAborted, formatTime(now.Add(-retentionFailedAge))); err != nil {
		return nil, fmt.Errorf("retain agent state %s: delete failed and aborted rows: %w", d.path, err)
	}
	if _, err = tx.ExecContext(ctx, deleteOldCommands, formatTime(now.Add(-retentionCommandAge))); err != nil {
		return nil, fmt.Errorf("retain agent state %s: delete old command rows: %w", d.path, err)
	}

	prunable, err := prunableRetired(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("retain agent state %s: %w", d.path, err)
	}
	for _, row := range prunable {
		if _, err = tx.ExecContext(ctx, deleteRetiredRow, row.appID, row.releaseID, statusRetired); err != nil {
			return nil, fmt.Errorf("retain agent state %s: delete retired release %s of app %s: %w", d.path, row.releaseID, row.appID, err)
		}
	}

	var (
		images []string
		seen   = map[string]bool{}
	)
	for _, row := range prunable {
		if row.imageID == "" || seen[row.imageID] {
			continue
		}
		seen[row.imageID] = true
		var kept bool
		if err = tx.QueryRowContext(ctx, selectImageKept,
			row.imageID, statusCandidate, statusCurrent, statusPrevious, statusRetired).Scan(&kept); err != nil {
			return nil, fmt.Errorf("retain agent state %s: read the rows naming image %s: %w", d.path, row.imageID, err)
		}
		if !kept {
			images = append(images, row.imageID)
		}
	}

	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("retain agent state %s: %w", d.path, err)
	}
	slices.Sort(images)
	return images, nil
}

// prunableRetired reads the retired rows retention may delete.
func prunableRetired(ctx context.Context, tx *sql.Tx) ([]prunedRetired, error) {
	rows, err := tx.QueryContext(ctx, selectPrunableRetired, statusRetired, retentionRetired)
	if err != nil {
		return nil, fmt.Errorf("read the retired rows outside the kept ones: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []prunedRetired
	for rows.Next() {
		var row prunedRetired
		if err = rows.Scan(&row.appID, &row.releaseID, &row.imageID); err != nil {
			return nil, fmt.Errorf("read the retired rows outside the kept ones: %w", err)
		}
		out = append(out, row)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("read the retired rows outside the kept ones: %w", err)
	}
	return out, nil
}
