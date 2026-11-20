// Package state is the agent's on-server database: the release and command
// records of the apps here and nothing else (docs/specs/agent-state.md).
package state

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite"

	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
	"moonglass.dev/moonglass/internal/release"
	"moonglass.dev/moonglass/internal/store"
)

const dbName = "agent.db"

const (
	statusCandidate = "candidate"
	statusCurrent   = "current"
	statusPrevious  = "previous"
	statusRetired   = "retired"
	statusFailed    = "failed"
	statusAborted   = "aborted"

	// The command types the agent records; W3-4 adds build, and W5-5b adds
	// stop_app and remove_app.
	CommandDeploy        = "deploy"
	CommandSwitchRelease = "switch_release"
	CommandRollback      = "rollback"
	CommandRunPreDeploy  = "run_pre_deploy"
)

const (
	// previousStopAfter is how long the release a switch replaced keeps running,
	// unrouted, so a rollback is only a route push (docs/specs/release.md).
	previousStopAfter = 10 * time.Minute
	// retiredStopAfter is how long the release a switch retired keeps running
	// after the switch, so its last requests drain.
	retiredStopAfter = 30 * time.Second
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// DB is the agent's open database.
type DB struct {
	db   *sql.DB
	path string
}

// Binding is the project, environment and service an app is bound to. It is
// written by the app's first Deploy and never changes.
type Binding struct {
	Project     string
	Environment string
	Service     string
}

// Record is one release row. A value that a failed step never reached is empty.
type Record struct {
	AppID        string
	ReleaseID    string
	ImageRef     string
	ImageID      string
	RepoDigest   string
	HealthPath   string
	ContainerID  string
	Domains      []string
	Port         uint32
	HasPreDeploy bool
}

// Command is one recorded command and the result it produced.
type Command struct {
	Received               time.Time
	Result                 *agentv1.CommandResult
	RequestID, Type, AppID string
}

// Open opens <dataDir>/agent.db, applies the embedded migrations and returns
// the database. The file and its WAL files are mode 0600. It never creates
// dataDir: a missing directory is an error that names the database path.
func Open(ctx context.Context, dataDir string) (*DB, error) {
	path := filepath.Join(dataDir, dbName)
	if err := createFile(path); err != nil {
		return nil, fmt.Errorf("open agent state %s: %w (create the data directory first)", path, err)
	}
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("open agent state %s: %w", path, err)
	}
	// One connection serialises writers, so a transaction never meets
	// SQLITE_BUSY from itself and every connection carries the pragmas.
	db.SetMaxOpenConns(1)
	if err = db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open agent state %s: %w", path, err)
	}
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open agent state %s: read embedded migrations: %w", path, err)
	}
	if err = store.Migrate(ctx, db, sub); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate agent state %s: %w", path, err)
	}
	return &DB{db: db, path: path}, nil
}

// createFile creates the database file with mode 0600 when it is missing and
// brings an existing file to 0600 before SQLite opens it.
func createFile(path string) error {
	//nolint:gosec // the path is <data-dir>/agent.db, built by the agent and never request input
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func dsn(path string) string {
	return "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)" +
		"&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=cache_size(-2000)"
}

// Close closes the database.
func (d *DB) Close() error {
	if err := d.db.Close(); err != nil {
		return fmt.Errorf("close agent state %s: %w", d.path, err)
	}
	return nil
}

const selectApp = `SELECT a.project, a.environment, a.service, a.phase,
	a.current_release_id, c.image_ref,
	a.previous_release_id, p.image_ref,
	a.candidate_release_id, n.image_ref
	FROM apps a
	LEFT JOIN releases c ON c.app_id = a.app_id AND c.release_id = a.current_release_id
	LEFT JOIN releases p ON p.app_id = a.app_id AND p.release_id = a.previous_release_id
	LEFT JOIN releases n ON n.app_id = a.app_id AND n.release_id = a.candidate_release_id
	WHERE a.app_id = ?`

// App returns the stored binding and release state of appID. known is false
// when the app has never been deployed here, and the other values are zero.
func (d *DB) App(ctx context.Context, appID string) (Binding, release.State, bool, error) {
	binding, st, known, err := readState(ctx, d.db, appID)
	if err != nil {
		return Binding{}, release.State{}, false, fmt.Errorf("read app %s from agent state %s: %w", appID, d.path, err)
	}
	return binding, st, known, nil
}

// readState reads one app's binding and release state through q, which is
// either the database or one of its transactions.
func readState(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, appID string,
) (Binding, release.State, bool, error) {
	var (
		binding      Binding
		phase        string
		currentID    sql.NullString
		currentImage sql.NullString
		previousID   sql.NullString
		prevImage    sql.NullString
		candidateID  sql.NullString
		candImage    sql.NullString
	)
	err := q.QueryRowContext(ctx, selectApp, appID).Scan(
		&binding.Project, &binding.Environment, &binding.Service, &phase,
		&currentID, &currentImage,
		&previousID, &prevImage,
		&candidateID, &candImage,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Binding{}, release.State{}, false, nil
	}
	if err != nil {
		return Binding{}, release.State{}, false, err
	}
	parsed, err := parsePhase(phase)
	if err != nil {
		return Binding{}, release.State{}, false, err
	}
	return binding, release.State{
		Current:   storedRelease(currentID, currentImage),
		Previous:  storedRelease(previousID, prevImage),
		Candidate: storedRelease(candidateID, candImage),
		Phase:     parsed,
	}, true, nil
}

const selectReleaseRecord = `SELECT image_ref, image_id, repo_digest, port, health_path, domains, has_pre_deploy, container_id
	FROM releases WHERE app_id = ? AND release_id = ?`

// Release returns the stored record of one release of one app. found is false
// when there is no such row, and the other values are zero.
func (d *DB) Release(ctx context.Context, appID, releaseID string) (Record, bool, error) {
	var (
		rec          Record
		domains      string
		hasPreDeploy int64
	)
	err := d.db.QueryRowContext(ctx, selectReleaseRecord, appID, releaseID).Scan(
		&rec.ImageRef, &rec.ImageID, &rec.RepoDigest, &rec.Port, &rec.HealthPath, &domains, &hasPreDeploy, &rec.ContainerID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, fmt.Errorf("read release %s of app %s from agent state %s: %w", releaseID, appID, d.path, err)
	}
	if err := json.Unmarshal([]byte(domains), &rec.Domains); err != nil {
		return Record{}, false, fmt.Errorf("read release %s of app %s from agent state %s: decode domains: %w", releaseID, appID, d.path, err)
	}
	rec.AppID = appID
	rec.ReleaseID = releaseID
	rec.HasPreDeploy = hasPreDeploy != 0
	return rec, true, nil
}

// CurrentRelease is an app's binding and the record of the release in its
// current_release_id.
type CurrentRelease struct {
	Binding Binding
	Record  Record
}

const selectCurrentReleases = `SELECT a.app_id, a.project, a.environment, a.service,
	r.release_id, r.image_ref, r.image_id, r.repo_digest, r.port, r.health_path,
	r.domains, r.has_pre_deploy, r.container_id
	FROM apps a
	JOIN releases r ON r.app_id = a.app_id AND r.release_id = a.current_release_id
	ORDER BY a.app_id`

// CurrentReleases returns every app that has a current release, ordered by
// app_id, each with its binding and its current release's record. An app with
// only a candidate, or with no releases, is absent.
func (d *DB) CurrentReleases(ctx context.Context) ([]CurrentRelease, error) {
	rows, err := d.db.QueryContext(ctx, selectCurrentReleases)
	if err != nil {
		return nil, fmt.Errorf("read current releases from agent state %s: %w", d.path, err)
	}
	defer func() { _ = rows.Close() }()

	var out []CurrentRelease
	for rows.Next() {
		var (
			cr           CurrentRelease
			domains      string
			hasPreDeploy int64
		)
		err := rows.Scan(
			&cr.Record.AppID, &cr.Binding.Project, &cr.Binding.Environment, &cr.Binding.Service,
			&cr.Record.ReleaseID, &cr.Record.ImageRef, &cr.Record.ImageID, &cr.Record.RepoDigest,
			&cr.Record.Port, &cr.Record.HealthPath, &domains, &hasPreDeploy, &cr.Record.ContainerID,
		)
		if err != nil {
			return nil, fmt.Errorf("read current releases from agent state %s: %w", d.path, err)
		}
		if err := json.Unmarshal([]byte(domains), &cr.Record.Domains); err != nil {
			return nil, fmt.Errorf("read current releases from agent state %s: decode domains of app %s release %s: %w",
				d.path, cr.Record.AppID, cr.Record.ReleaseID, err)
		}
		cr.Record.HasPreDeploy = hasPreDeploy != 0
		out = append(out, cr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read current releases from agent state %s: %w", d.path, err)
	}
	return out, nil
}

func storedRelease(id, image sql.NullString) *release.Release {
	if !id.Valid {
		return nil
	}
	return &release.Release{ID: id.String, Image: image.String}
}

func parsePhase(text string) (release.Phase, error) {
	switch text {
	case release.PhaseNone.String():
		return release.PhaseNone, nil
	case release.PhasePending.String():
		return release.PhasePending, nil
	case release.PhaseReady.String():
		return release.PhaseReady, nil
	default:
		return release.PhaseNone, fmt.Errorf("phase %q is not one of none, pending or ready", text)
	}
}

// RecordCandidate records the candidate release of state in one transaction:
// the binding on the app's first deploy, the release row and the app's release
// state. It writes nothing and returns an error when the app is bound to
// another binding, when state's candidate is not r, or when the release is
// already recorded.
func (d *DB) RecordCandidate(ctx context.Context, cmd Command, b Binding, r Record, state release.State, now time.Time) error {
	if state.Candidate == nil {
		return fmt.Errorf("record candidate %s of app %s: state has no candidate; record the state's candidate", r.ReleaseID, r.AppID)
	}
	if state.Candidate.ID != r.ReleaseID || state.Candidate.Image != r.ImageRef {
		return fmt.Errorf("record candidate %s of app %s: state candidate is %s with image %s; record the state's candidate",
			r.ReleaseID, r.AppID, state.Candidate.ID, state.Candidate.Image)
	}

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("record candidate %s of app %s: %w", r.ReleaseID, r.AppID, err)
	}
	defer func() { _ = tx.Rollback() }()

	var stored Binding
	err = tx.QueryRowContext(ctx,
		`SELECT project, environment, service FROM apps WHERE app_id = ?`, r.AppID).
		Scan(&stored.Project, &stored.Environment, &stored.Service)
	known := true
	switch {
	case errors.Is(err, sql.ErrNoRows):
		known = false
	case err != nil:
		return fmt.Errorf("record candidate %s of app %s: read binding: %w", r.ReleaseID, r.AppID, err)
	case stored != b:
		return fmt.Errorf("app %s is bound to project %q, environment %q, service %q; deploy it with that binding or use a new app id",
			r.AppID, stored.Project, stored.Environment, stored.Service)
	}

	if err = refuseRecorded(ctx, tx, r.AppID, r.ReleaseID); err != nil {
		return err
	}
	if err = insertRelease(ctx, tx, r, statusCandidate, "", now); err != nil {
		return err
	}

	if known {
		_, err = tx.ExecContext(ctx,
			`UPDATE apps SET current_release_id = ?, previous_release_id = ?, candidate_release_id = ?, phase = ?
			WHERE app_id = ?`,
			nullID(state.Current), nullID(state.Previous), nullID(state.Candidate), state.Phase.String(), r.AppID)
	} else {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO apps (app_id, project, environment, service,
				current_release_id, previous_release_id, candidate_release_id, phase, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.AppID, b.Project, b.Environment, b.Service,
			nullID(state.Current), nullID(state.Previous), nullID(state.Candidate),
			state.Phase.String(), formatTime(now))
	}
	if err != nil {
		return fmt.Errorf("record candidate %s of app %s: write apps row: %w", r.ReleaseID, r.AppID, err)
	}
	if err = insertCommand(ctx, tx, cmd, now); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("record candidate %s of app %s: %w", r.ReleaseID, r.AppID, err)
	}
	return nil
}

// RecordPreDeploy records a pre-deploy outcome in one transaction: the release
// row's pre_deploy_reversible, and on abort its status, error and empty
// container_id, plus the app's new release state. It writes nothing and returns
// an error when next does not follow from the stored state of that release, so
// only the pending candidate's outcome is ever recorded. detail is stored as
// given and never logged.
func (d *DB) RecordPreDeploy(ctx context.Context, cmd Command, appID, releaseID string, next release.State, reversible bool, detail string, now time.Time) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("record pre-deploy outcome of release %s of app %s: %w", releaseID, appID, err)
	}
	defer func() { _ = tx.Rollback() }()

	_, stored, known, err := readState(ctx, tx, appID)
	if err != nil {
		return fmt.Errorf("record pre-deploy outcome of release %s of app %s: %w", releaseID, appID, err)
	}
	if !known {
		return fmt.Errorf("record pre-deploy outcome of release %s: app %s is not in agent state; deploy it first", releaseID, appID)
	}
	want, err := stored.PreDeployDone(releaseID, next.Candidate != nil)
	if err != nil {
		return fmt.Errorf("record pre-deploy outcome of release %s of app %s: %w", releaseID, appID, err)
	}
	if !sameState(want, next) {
		return fmt.Errorf("record pre-deploy outcome of release %s of app %s: the given state does not follow from the stored one; record the state PreDeployDone returned", releaseID, appID)
	}

	reversibleValue := 0
	if reversible {
		reversibleValue = 1
	}
	if next.Candidate != nil {
		err = updateOne(ctx, tx,
			`UPDATE releases SET pre_deploy_reversible = ? WHERE app_id = ? AND release_id = ? AND status = ?`,
			reversibleValue, appID, releaseID, statusCandidate)
	} else {
		err = updateOne(ctx, tx,
			`UPDATE releases SET pre_deploy_reversible = ?, status = ?, error = ?, container_id = ''
			WHERE app_id = ? AND release_id = ? AND status = ?`,
			reversibleValue, statusAborted, detail, appID, releaseID, statusCandidate)
	}
	if err != nil {
		return fmt.Errorf("record pre-deploy outcome of release %s of app %s: %w", releaseID, appID, err)
	}

	if err = setAppState(ctx, tx, appID, next); err != nil {
		return fmt.Errorf("record pre-deploy outcome of release %s of app %s: %w", releaseID, appID, err)
	}
	if err = insertCommand(ctx, tx, cmd, now); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("record pre-deploy outcome of release %s of app %s: %w", releaseID, appID, err)
	}
	return nil
}

// RecordSwitch records a successful switch in one transaction: the candidate
// row becomes current with switched_at = now, the old current becomes previous
// due to stop at now + 10 minutes, the old previous becomes retired due to stop
// at now + 30 seconds, and the apps row becomes next. It writes nothing and
// returns an error when next does not follow from the stored state of that
// release.
func (d *DB) RecordSwitch(ctx context.Context, cmd Command, appID, releaseID string, next release.State, now time.Time) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("record switch of release %s of app %s: %w", releaseID, appID, err)
	}
	defer func() { _ = tx.Rollback() }()

	_, stored, known, err := readState(ctx, tx, appID)
	if err != nil {
		return fmt.Errorf("record switch of release %s of app %s: %w", releaseID, appID, err)
	}
	if !known {
		return fmt.Errorf("record switch of release %s: app %s is not in agent state; deploy it first", releaseID, appID)
	}
	want, err := stored.Switch(releaseID)
	if err != nil {
		return fmt.Errorf("record switch of release %s of app %s: %w", releaseID, appID, err)
	}
	if !sameState(want, next) {
		return fmt.Errorf("record switch of release %s of app %s: the given state does not follow from the stored one; record the state Switch returned", releaseID, appID)
	}

	if err = updateOne(ctx, tx,
		`UPDATE releases SET status = ?, switched_at = ? WHERE app_id = ? AND release_id = ? AND status = ?`,
		statusCurrent, formatTime(now), appID, releaseID, statusCandidate); err != nil {
		return fmt.Errorf("record switch of release %s of app %s: %w", releaseID, appID, err)
	}
	if stored.Current != nil {
		if err = updateOne(ctx, tx,
			`UPDATE releases SET status = ?, stop_after = ? WHERE app_id = ? AND release_id = ? AND status = ?`,
			statusPrevious, formatTime(now.Add(previousStopAfter)), appID, stored.Current.ID, statusCurrent); err != nil {
			return fmt.Errorf("record switch of release %s of app %s: %w", releaseID, appID, err)
		}
	}
	if stored.Previous != nil {
		if err = updateOne(ctx, tx,
			`UPDATE releases SET status = ?, stop_after = ? WHERE app_id = ? AND release_id = ? AND status = ?`,
			statusRetired, formatTime(now.Add(retiredStopAfter)), appID, stored.Previous.ID, statusPrevious); err != nil {
			return fmt.Errorf("record switch of release %s of app %s: %w", releaseID, appID, err)
		}
	}
	if err = setAppState(ctx, tx, appID, next); err != nil {
		return fmt.Errorf("record switch of release %s of app %s: %w", releaseID, appID, err)
	}
	if err = insertCommand(ctx, tx, cmd, now); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("record switch of release %s of app %s: %w", releaseID, appID, err)
	}
	return nil
}

// RecordRollback records a rollback in one transaction: the restored row becomes
// current with switched_at = now and no stop_after, the row rolled back from
// becomes retired due to stop at now + 30 seconds, and the apps row becomes next.
// The candidate row, the app's candidate and its phase are left as they are. It
// writes nothing and returns an error when next is not the state Rollback
// returned for the stored one.
func (d *DB) RecordRollback(ctx context.Context, cmd Command, appID string, next release.State, now time.Time) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("record rollback of app %s: %w", appID, err)
	}
	defer func() { _ = tx.Rollback() }()

	_, stored, known, err := readState(ctx, tx, appID)
	if err != nil {
		return fmt.Errorf("record rollback of app %s: %w", appID, err)
	}
	if !known {
		return fmt.Errorf("record rollback of app %s: app is not in agent state; deploy it first", appID)
	}
	want, err := stored.Rollback()
	if err != nil {
		return fmt.Errorf("record rollback of app %s: %w", appID, err)
	}
	if !sameState(want, next) {
		return fmt.Errorf("record rollback of app %s: the given state does not follow from the stored one; record the state Rollback returned", appID)
	}

	if err = updateOne(ctx, tx,
		`UPDATE releases SET status = ?, switched_at = ?, stop_after = NULL
		WHERE app_id = ? AND release_id = ? AND status = ?`,
		statusCurrent, formatTime(now), appID, stored.Previous.ID, statusPrevious); err != nil {
		return fmt.Errorf("record rollback of app %s: %w", appID, err)
	}
	if err = updateOne(ctx, tx,
		`UPDATE releases SET status = ?, stop_after = ? WHERE app_id = ? AND release_id = ? AND status = ?`,
		statusRetired, formatTime(now.Add(retiredStopAfter)), appID, stored.Current.ID, statusCurrent); err != nil {
		return fmt.Errorf("record rollback of app %s: %w", appID, err)
	}
	if err = setAppState(ctx, tx, appID, next); err != nil {
		return fmt.Errorf("record rollback of app %s: %w", appID, err)
	}
	if err = insertCommand(ctx, tx, cmd, now); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("record rollback of app %s: %w", appID, err)
	}
	return nil
}

// RecordAbort records a candidate its health gate gave up on in one transaction:
// the row becomes aborted with detail as error and an empty container_id, and
// the apps row becomes next. Current and Previous are untouched. It writes
// nothing and returns an error when next does not follow from the stored state
// of that release. detail is stored as given and never logged.
func (d *DB) RecordAbort(ctx context.Context, cmd Command, appID, releaseID string, next release.State, detail string, now time.Time) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("record abort of release %s of app %s: %w", releaseID, appID, err)
	}
	defer func() { _ = tx.Rollback() }()

	_, stored, known, err := readState(ctx, tx, appID)
	if err != nil {
		return fmt.Errorf("record abort of release %s of app %s: %w", releaseID, appID, err)
	}
	if !known {
		return fmt.Errorf("record abort of release %s: app %s is not in agent state; deploy it first", releaseID, appID)
	}
	want, err := stored.Abort(releaseID)
	if err != nil {
		return fmt.Errorf("record abort of release %s of app %s: %w", releaseID, appID, err)
	}
	if !sameState(want, next) {
		return fmt.Errorf("record abort of release %s of app %s: the given state does not follow from the stored one; record the state Abort returned", releaseID, appID)
	}

	if err = updateOne(ctx, tx,
		`UPDATE releases SET status = ?, error = ?, container_id = ''
		WHERE app_id = ? AND release_id = ? AND status = ?`,
		statusAborted, detail, appID, releaseID, statusCandidate); err != nil {
		return fmt.Errorf("record abort of release %s of app %s: %w", releaseID, appID, err)
	}
	if err = setAppState(ctx, tx, appID, next); err != nil {
		return fmt.Errorf("record abort of release %s of app %s: %w", releaseID, appID, err)
	}
	if err = insertCommand(ctx, tx, cmd, now); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("record abort of release %s of app %s: %w", releaseID, appID, err)
	}
	return nil
}

// setAppState writes one app's release state. It is the one apps-row update of
// every write that changes the state.
func setAppState(ctx context.Context, tx *sql.Tx, appID string, st release.State) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE apps SET current_release_id = ?, previous_release_id = ?, candidate_release_id = ?, phase = ?
		WHERE app_id = ?`,
		nullID(st.Current), nullID(st.Previous), nullID(st.Candidate), st.Phase.String(), appID)
	if err != nil {
		return fmt.Errorf("write apps row: %w", err)
	}
	return nil
}

// updateOne runs a releases-row update that must change exactly one row, so a
// state write never silently changes none or several.
func updateOne(ctx context.Context, tx *sql.Tx, query string, args ...any) error {
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("write releases row: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("count updated releases rows: %w", err)
	}
	if affected != 1 {
		return fmt.Errorf("the update changed %d releases rows, want 1; nothing was written", affected)
	}
	return nil
}

// sameState reports whether two states name the same releases and phase. Only
// the ids are compared: the machine keeps the stored images.
func sameState(a, b release.State) bool {
	return sameRelease(a.Current, b.Current) && sameRelease(a.Previous, b.Previous) &&
		sameRelease(a.Candidate, b.Candidate) && a.Phase == b.Phase
}

func sameRelease(a, b *release.Release) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.ID == b.ID
}

// RecordFailed records a failed release in one transaction. It never creates or
// changes an apps row, so the binding and the release state stay as they were.
// detail is stored as given and never logged.
func (d *DB) RecordFailed(ctx context.Context, cmd Command, r Record, detail string, now time.Time) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("record failed release %s of app %s: %w", r.ReleaseID, r.AppID, err)
	}
	defer func() { _ = tx.Rollback() }()

	if err = refuseRecorded(ctx, tx, r.AppID, r.ReleaseID); err != nil {
		return err
	}
	if err = insertRelease(ctx, tx, r, statusFailed, detail, now); err != nil {
		return err
	}
	if err = insertCommand(ctx, tx, cmd, now); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("record failed release %s of app %s: %w", r.ReleaseID, r.AppID, err)
	}
	return nil
}

// RecordCommand records one command's result alone, in its own transaction.
func (d *DB) RecordCommand(ctx context.Context, cmd Command, finished time.Time) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("record command %s: %w", cmd.RequestID, err)
	}
	defer func() { _ = tx.Rollback() }()

	if err = insertCommand(ctx, tx, cmd, finished); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("record command %s: %w", cmd.RequestID, err)
	}
	return nil
}

// CommandResult returns the stored result of requestID, if there is one.
func (d *DB) CommandResult(ctx context.Context, requestID string) (*agentv1.CommandResult, bool, error) {
	var encoded []byte
	err := d.db.QueryRowContext(ctx, `SELECT result FROM commands WHERE request_id = ?`, requestID).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read command %s from agent state %s: %w", requestID, d.path, err)
	}
	res := &agentv1.CommandResult{}
	if err = proto.Unmarshal(encoded, res); err != nil {
		return nil, false, fmt.Errorf("read command %s from agent state %s: decode its result: %w", requestID, d.path, err)
	}
	return res, true, nil
}

// insertCommand writes one commands row inside tx, the only encoder of
// commands.result. A plain INSERT fails on an existing request id.
func insertCommand(ctx context.Context, tx *sql.Tx, cmd Command, finished time.Time) error {
	if cmd.RequestID == "" || cmd.Result == nil {
		return fmt.Errorf("write commands row: the command needs a request id and a result; pass the envelope's id and the result it returned")
	}
	encoded, err := proto.Marshal(cmd.Result)
	if err != nil {
		return fmt.Errorf("command %s: encode its result: %w", cmd.RequestID, err)
	}
	if _, err = tx.ExecContext(ctx,
		`INSERT INTO commands (request_id, type, app_id, result, received_at, finished_at) VALUES (?, ?, ?, ?, ?, ?)`,
		cmd.RequestID, cmd.Type, cmd.AppID, encoded, formatTime(cmd.Received), formatTime(finished)); err != nil {
		return fmt.Errorf("command %s: write commands row: %w", cmd.RequestID, err)
	}
	return nil
}

// refuseRecorded is the duplicate check of both writes. It runs inside the
// transaction on the one connection, so no other write can slip in between.
func refuseRecorded(ctx context.Context, tx *sql.Tx, appID, releaseID string) error {
	var recorded bool
	if err := tx.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM releases WHERE app_id = ? AND release_id = ?)`,
		appID, releaseID).Scan(&recorded); err != nil {
		return fmt.Errorf("release %s of app %s: read release row: %w", releaseID, appID, err)
	}
	if recorded {
		return fmt.Errorf("release %s of app %s is already recorded; use a new release id", releaseID, appID)
	}
	return nil
}

func insertRelease(ctx context.Context, tx *sql.Tx, r Record, status, errText string, now time.Time) error {
	domains := r.Domains
	if domains == nil {
		domains = []string{}
	}
	encoded, err := json.Marshal(domains)
	if err != nil {
		return fmt.Errorf("release %s of app %s: encode domains: %w", r.ReleaseID, r.AppID, err)
	}
	hasPreDeploy := 0
	if r.HasPreDeploy {
		hasPreDeploy = 1
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO releases (app_id, release_id, image_ref, image_id, repo_digest, port, health_path,
			domains, has_pre_deploy, status, container_id, error, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.AppID, r.ReleaseID, r.ImageRef, r.ImageID, r.RepoDigest, r.Port, r.HealthPath,
		string(encoded), hasPreDeploy, status, r.ContainerID, errText, formatTime(now))
	if err != nil {
		return fmt.Errorf("release %s of app %s: write releases row: %w", r.ReleaseID, r.AppID, err)
	}
	return nil
}

func nullID(r *release.Release) sql.NullString {
	if r == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: r.ID, Valid: true}
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
