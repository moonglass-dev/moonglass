package state

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"moonglass.dev/moonglass/internal/release"
)

// switchRow is the part of a releases row a switch writes.
type switchRow struct {
	status     string
	switchedAt sql.NullString
	stopAfter  sql.NullString
}

// readSwitchRow reads status, switched_at and stop_after directly from the
// agent database.
func readSwitchRow(t *testing.T, d *DB, appID, releaseID string) switchRow {
	t.Helper()
	var row switchRow
	err := d.db.QueryRowContext(context.Background(),
		`SELECT status, switched_at, stop_after FROM releases WHERE app_id = ? AND release_id = ?`,
		appID, releaseID).
		Scan(&row.status, &row.switchedAt, &row.stopAfter)
	if err != nil {
		t.Fatalf("read switch row for app %q and release %q: %v", appID, releaseID, err)
	}
	return row
}

// requireStoredState fails when the app's stored state differs from want by
// release id or phase.
func requireStoredState(t *testing.T, d *DB, appID string, want release.State) {
	t.Helper()
	_, got, known, err := d.App(context.Background(), appID)
	if err != nil {
		t.Fatalf("App(%q) error = %v, want nil", appID, err)
	}
	if !known {
		t.Fatalf("App(%q) known = false, want the stored app", appID)
	}
	if !sameState(got, want) {
		t.Errorf("stored state of %s = %+v, want %+v", appID, got, want)
	}
}

// requireTime fails when text is not timeText in RFC 3339 UTC.
func requireTime(t *testing.T, field, text string, want time.Time) {
	t.Helper()
	if got, wantText := text, testTimeText(want); got != wantText {
		t.Errorf("%s = %q, want %q", field, got, wantText)
	}
}

// TestRecordSwitchWritesStatusesAndStopAfter covers the switch commit of
// docs/specs/agent-state.md: three consecutive switches write the statuses,
// switched_at and stop_after of every affected row, and a refused switch writes
// nothing.
func TestRecordSwitchWritesStatusesAndStopAfter(t *testing.T) {
	ctx := context.Background()

	t.Run("three consecutive switches", func(t *testing.T) {
		d, _ := openTemp(t)
		b := sampleBinding()
		now1 := testTime()
		now2 := now1.Add(time.Hour)
		now3 := now2.Add(time.Hour)

		r1 := sampleRecord("app-1", "rel-1")
		r2 := sampleRecord("app-1", "rel-2")
		r3 := sampleRecord("app-1", "rel-3")

		s1 := mustDeploy(t, release.State{}, r1)
		mustRecord(t, d, b, r1, s1, now1)
		next1 := mustSwitch(t, s1, r1.ReleaseID)
		if err := d.RecordSwitch(ctx, testCommand(t, "app-1"), "app-1", r1.ReleaseID, next1, now1); err != nil {
			t.Fatalf("RecordSwitch(rel-1) error = %v, want nil", err)
		}
		row := readSwitchRow(t, d, "app-1", "rel-1")
		if row.status != "current" {
			t.Errorf("rel-1 status after the first switch = %q, want %q", row.status, "current")
		}
		requireTime(t, "rel-1 switched_at after the first switch", row.switchedAt.String, now1)
		if row.stopAfter.Valid {
			t.Errorf("rel-1 stop_after after the first switch = %q, want NULL", row.stopAfter.String)
		}
		requireStoredState(t, d, "app-1", next1)

		s2 := mustDeploy(t, next1, r2)
		mustRecord(t, d, b, r2, s2, now2)
		next2 := mustSwitch(t, s2, r2.ReleaseID)
		if err := d.RecordSwitch(ctx, testCommand(t, "app-1"), "app-1", r2.ReleaseID, next2, now2); err != nil {
			t.Fatalf("RecordSwitch(rel-2) error = %v, want nil", err)
		}
		row = readSwitchRow(t, d, "app-1", "rel-2")
		if row.status != "current" {
			t.Errorf("rel-2 status after the second switch = %q, want %q", row.status, "current")
		}
		requireTime(t, "rel-2 switched_at after the second switch", row.switchedAt.String, now2)
		if row.stopAfter.Valid {
			t.Errorf("rel-2 stop_after after the second switch = %q, want NULL", row.stopAfter.String)
		}
		row = readSwitchRow(t, d, "app-1", "rel-1")
		if row.status != "previous" {
			t.Errorf("rel-1 status after the second switch = %q, want %q", row.status, "previous")
		}
		requireTime(t, "rel-1 switched_at after the second switch", row.switchedAt.String, now1)
		requireTime(t, "rel-1 stop_after after the second switch", row.stopAfter.String, now2.Add(10*time.Minute))
		requireStoredState(t, d, "app-1", next2)

		s3 := mustDeploy(t, next2, r3)
		mustRecord(t, d, b, r3, s3, now3)
		next3 := mustSwitch(t, s3, r3.ReleaseID)
		if err := d.RecordSwitch(ctx, testCommand(t, "app-1"), "app-1", r3.ReleaseID, next3, now3); err != nil {
			t.Fatalf("RecordSwitch(rel-3) error = %v, want nil", err)
		}
		row = readSwitchRow(t, d, "app-1", "rel-3")
		if row.status != "current" {
			t.Errorf("rel-3 status after the third switch = %q, want %q", row.status, "current")
		}
		requireTime(t, "rel-3 switched_at after the third switch", row.switchedAt.String, now3)
		if row.stopAfter.Valid {
			t.Errorf("rel-3 stop_after after the third switch = %q, want NULL", row.stopAfter.String)
		}
		row = readSwitchRow(t, d, "app-1", "rel-2")
		if row.status != "previous" {
			t.Errorf("rel-2 status after the third switch = %q, want %q", row.status, "previous")
		}
		requireTime(t, "rel-2 switched_at after the third switch", row.switchedAt.String, now2)
		requireTime(t, "rel-2 stop_after after the third switch", row.stopAfter.String, now3.Add(10*time.Minute))
		row = readSwitchRow(t, d, "app-1", "rel-1")
		if row.status != "retired" {
			t.Errorf("rel-1 status after the third switch = %q, want %q", row.status, "retired")
		}
		requireTime(t, "rel-1 switched_at after the third switch", row.switchedAt.String, now1)
		requireTime(t, "rel-1 stop_after after the third switch", row.stopAfter.String, now3.Add(30*time.Second))
		requireStoredState(t, d, "app-1", next3)
	})

	t.Run("a refused switch writes nothing", func(t *testing.T) {
		cases := []struct {
			name   string
			setup  func(t *testing.T) (*DB, string, release.State)
			reason string
		}{
			{
				name: "next state does not follow from the stored one",
				setup: func(t *testing.T) (*DB, string, release.State) {
					d, _ := openTemp(t)
					r1 := sampleRecord("app-1", "rel-1")
					s1 := mustDeploy(t, release.State{}, r1)
					mustRecord(t, d, sampleBinding(), r1, s1, testTime())
					want := mustSwitch(t, s1, r1.ReleaseID)
					wrong := want
					wrong.Previous = &release.Release{ID: "rel-0"}
					return d, r1.ReleaseID, wrong
				},
				reason: "a next state with the wrong Previous",
			},
			{
				name: "a different release id",
				setup: func(t *testing.T) (*DB, string, release.State) {
					d, _ := openTemp(t)
					r1 := sampleRecord("app-1", "rel-1")
					s1 := mustDeploy(t, release.State{}, r1)
					mustRecord(t, d, sampleBinding(), r1, s1, testTime())
					return d, "rel-9", s1
				},
				reason: "an id that is not the stored candidate",
			},
			{
				name: "a pending candidate",
				setup: func(t *testing.T) (*DB, string, release.State) {
					d, _ := openTemp(t)
					r1 := sampleRecord("app-1", "rel-1")
					r1.HasPreDeploy = true
					s1 := mustDeploy(t, release.State{}, r1)
					mustRecord(t, d, sampleBinding(), r1, s1, testTime())
					return d, r1.ReleaseID, s1
				},
				reason: "a candidate that has not passed its pre-deploy step",
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				d, releaseID, next := tc.setup(t)
				before := snapshot(t, d)

				err := d.RecordSwitch(ctx, testCommand(t, "app-1"), "app-1", releaseID, next, testTime().Add(time.Hour))

				if err == nil {
					t.Fatalf("RecordSwitch on %s error = nil, want an error", tc.reason)
				}
				if after := snapshot(t, d); !reflect.DeepEqual(before, after) {
					t.Errorf("a refused RecordSwitch changed the rows:\n before = %v\n after  = %v", before, after)
				}
			})
		}
	})
}

// abortRow is the part of a releases row an abort writes.
type abortRow struct {
	status      string
	containerID string
	errText     string
}

func readAbortRow(t *testing.T, d *DB, appID, releaseID string) abortRow {
	t.Helper()
	var row abortRow
	err := d.db.QueryRowContext(context.Background(),
		`SELECT status, container_id, error FROM releases WHERE app_id = ? AND release_id = ?`,
		appID, releaseID).
		Scan(&row.status, &row.containerID, &row.errText)
	if err != nil {
		t.Fatalf("read abort row for app %q and release %q: %v", appID, releaseID, err)
	}
	return row
}

// TestRecordAbortMarksCandidateAborted covers the aborted row a failed health
// gate writes: status aborted, the detail as error, an empty container_id and
// the app's candidate cleared while Current and Previous stay. A next that is
// not the stored state's Abort writes nothing while the abort is still legal.
func TestRecordAbortMarksCandidateAborted(t *testing.T) {
	ctx := context.Background()
	const detail = "release rel-3 of app app-1 did not pass its health gate within 4s; the old release keeps serving"

	d, _ := openTemp(t)
	b := sampleBinding()
	now := testTime()

	r1 := sampleRecord("app-1", "rel-1")
	r2 := sampleRecord("app-1", "rel-2")
	r3 := sampleRecord("app-1", "rel-3")

	mustRecord(t, d, b, r1, release.State{
		Candidate: &release.Release{ID: r1.ReleaseID, Image: r1.ImageRef},
		Phase:     release.PhaseReady,
	}, now)
	mustRecord(t, d, b, r2, release.State{
		Current:   &release.Release{ID: r1.ReleaseID, Image: r1.ImageRef},
		Candidate: &release.Release{ID: r2.ReleaseID, Image: r2.ImageRef},
		Phase:     release.PhaseReady,
	}, now.Add(time.Hour))
	mustRecord(t, d, b, r3, release.State{
		Current:   &release.Release{ID: r2.ReleaseID, Image: r2.ImageRef},
		Previous:  &release.Release{ID: r1.ReleaseID, Image: r1.ImageRef},
		Candidate: &release.Release{ID: r3.ReleaseID, Image: r3.ImageRef},
		Phase:     release.PhaseReady,
	}, now.Add(2*time.Hour))

	_, stored, known, err := d.App(ctx, "app-1")
	if err != nil {
		t.Fatalf("App(app-1) error = %v, want nil", err)
	}
	if !known {
		t.Fatalf("App(app-1) known = false, want the stored app")
	}
	next, err := stored.Abort(r3.ReleaseID)
	if err != nil {
		t.Fatalf("State.Abort(%q) error = %v, want nil", r3.ReleaseID, err)
	}

	before := snapshot(t, d)
	if err := d.RecordAbort(ctx, testCommand(t, "app-1"), "app-1", r3.ReleaseID, stored, detail, testTime()); err == nil {
		t.Fatalf("RecordAbort with a state that does not follow from the stored one error = nil, want an error")
	}
	if after := snapshot(t, d); !reflect.DeepEqual(before, after) {
		t.Fatalf("snapshot changed after illegal RecordAbort: before = %+v, after = %+v", before, after)
	}

	if err := d.RecordAbort(ctx, testCommand(t, "app-1"), "app-1", r3.ReleaseID, next, detail, testTime()); err != nil {
		t.Fatalf("RecordAbort(rel-3) error = %v, want nil", err)
	}

	row := readAbortRow(t, d, "app-1", "rel-3")
	if row.status != "aborted" {
		t.Errorf("rel-3 status = %q, want %q", row.status, "aborted")
	}
	if row.errText != detail {
		t.Errorf("rel-3 error = %q, want %q", row.errText, detail)
	}
	if row.containerID != "" {
		t.Errorf("rel-3 container_id = %q, want it empty", row.containerID)
	}

	app := readApp(t, d, "app-1")
	if app.Current.String != "rel-2" {
		t.Errorf("apps.current_release_id = %q, want %q", app.Current.String, "rel-2")
	}
	if app.Previous.String != "rel-1" {
		t.Errorf("apps.previous_release_id = %q, want %q", app.Previous.String, "rel-1")
	}
	if app.Candidate.Valid {
		t.Errorf("apps.candidate_release_id = %q, want NULL", app.Candidate.String)
	}
	if app.Phase != release.PhaseNone.String() {
		t.Errorf("apps.phase = %q, want %q", app.Phase, release.PhaseNone.String())
	}
	requireStoredState(t, d, "app-1", next)
}
