package state

import (
	"context"
	"reflect"
	"testing"
	"time"

	"moonglass.dev/moonglass/internal/release"
)

// recordSwitchTwice deploys and switches rel-1 then rel-2 for app-1, so rel-1 is
// the app's previous release and rel-2 its current one. It returns the stored
// state after the second switch.
func recordSwitchTwice(t *testing.T, d *DB, now1, now2 time.Time) release.State {
	t.Helper()
	b := sampleBinding()

	r1 := sampleRecord("app-1", "rel-1")
	s1 := mustDeploy(t, release.State{}, r1)
	mustRecord(t, d, b, r1, s1, now1)
	next1 := mustSwitch(t, s1, r1.ReleaseID)
	if err := d.RecordSwitch(context.Background(), testCommand(t, "app-1"), "app-1", r1.ReleaseID, next1, now1); err != nil {
		t.Fatalf("RecordSwitch(rel-1) error = %v, want nil", err)
	}

	r2 := sampleRecord("app-1", "rel-2")
	s2 := mustDeploy(t, next1, r2)
	mustRecord(t, d, b, r2, s2, now2)
	next2 := mustSwitch(t, s2, r2.ReleaseID)
	if err := d.RecordSwitch(context.Background(), testCommand(t, "app-1"), "app-1", r2.ReleaseID, next2, now2); err != nil {
		t.Fatalf("RecordSwitch(rel-2) error = %v, want nil", err)
	}
	return next2
}

// mustRollback returns the state Rollback produces from s.
func mustRollback(t *testing.T, s release.State) release.State {
	t.Helper()
	next, err := s.Rollback()
	if err != nil {
		t.Fatalf("State.Rollback() error = %v, want nil", err)
	}
	return next
}

// TestRecordRollbackRestoresPrevious covers the rollback commit of
// docs/specs/agent-state.md: the restored row becomes current with switched_at
// set to now and no stop_after, the row rolled back from becomes retired due to
// stop at now + 30 seconds, the apps row takes the new state, the candidate row
// is untouched, and a state that does not follow from the stored one writes
// nothing.
func TestRecordRollbackRestoresPrevious(t *testing.T) {
	ctx := context.Background()

	t.Run("two switches", func(t *testing.T) {
		d, _ := openTemp(t)
		now1 := testTime()
		now2 := now1.Add(time.Hour)
		now3 := now2.Add(time.Hour)

		stored := recordSwitchTwice(t, d, now1, now2)
		next := mustRollback(t, stored)

		if err := d.RecordRollback(ctx, testCommand(t, "app-1"), "app-1", next, now3); err != nil {
			t.Fatalf("RecordRollback() error = %v, want nil", err)
		}

		restored := readSwitchRow(t, d, "app-1", "rel-1")
		if restored.status != "current" {
			t.Errorf("rel-1 status after the rollback = %q, want %q", restored.status, "current")
		}
		requireTime(t, "rel-1 switched_at after the rollback", restored.switchedAt.String, now3)
		if restored.stopAfter.Valid {
			t.Errorf("rel-1 stop_after after the rollback = %q, want NULL", restored.stopAfter.String)
		}

		rolledFrom := readSwitchRow(t, d, "app-1", "rel-2")
		if rolledFrom.status != "retired" {
			t.Errorf("rel-2 status after the rollback = %q, want %q", rolledFrom.status, "retired")
		}
		requireTime(t, "rel-2 switched_at after the rollback", rolledFrom.switchedAt.String, now2)
		requireTime(t, "rel-2 stop_after after the rollback", rolledFrom.stopAfter.String, now3.Add(30*time.Second))

		requireStoredState(t, d, "app-1", next)

		app := readApp(t, d, "app-1")
		if !app.Current.Valid || app.Current.String != "rel-1" {
			t.Errorf("apps.current_release_id after the rollback = %+v, want %q", app.Current, "rel-1")
		}
		if app.Previous.Valid {
			t.Errorf("apps.previous_release_id after the rollback = %q, want NULL", app.Previous.String)
		}
		if app.Candidate.Valid {
			t.Errorf("apps.candidate_release_id after the rollback = %q, want NULL", app.Candidate.String)
		}
		if app.Phase != release.PhaseNone.String() {
			t.Errorf("apps.phase after the rollback = %q, want %q", app.Phase, release.PhaseNone.String())
		}
	})

	t.Run("with a candidate", func(t *testing.T) {
		d, _ := openTemp(t)
		now1 := testTime()
		now2 := now1.Add(time.Hour)
		now3 := now2.Add(time.Hour)
		now4 := now3.Add(time.Hour)

		stored := recordSwitchTwice(t, d, now1, now2)

		r3 := sampleRecord("app-1", "rel-3")
		s3 := mustDeploy(t, stored, r3)
		mustRecord(t, d, sampleBinding(), r3, s3, now3)
		candidateBefore := readSwitchRow(t, d, "app-1", "rel-3")

		next := mustRollback(t, s3)
		if err := d.RecordRollback(ctx, testCommand(t, "app-1"), "app-1", next, now4); err != nil {
			t.Fatalf("RecordRollback() with a candidate error = %v, want nil", err)
		}

		candidateAfter := readSwitchRow(t, d, "app-1", "rel-3")
		if candidateAfter.status != "candidate" {
			t.Errorf("rel-3 status after the rollback = %q, want %q", candidateAfter.status, "candidate")
		}
		if !reflect.DeepEqual(candidateBefore, candidateAfter) {
			t.Errorf("the rollback changed the candidate row:\n before = %+v\n after  = %+v", candidateBefore, candidateAfter)
		}

		requireStoredState(t, d, "app-1", next)

		app := readApp(t, d, "app-1")
		if !app.Current.Valid || app.Current.String != "rel-1" {
			t.Errorf("apps.current_release_id after the rollback = %+v, want %q", app.Current, "rel-1")
		}
		if app.Previous.Valid {
			t.Errorf("apps.previous_release_id after the rollback = %q, want NULL", app.Previous.String)
		}
		if !app.Candidate.Valid || app.Candidate.String != "rel-3" {
			t.Errorf("apps.candidate_release_id after the rollback = %+v, want %q", app.Candidate, "rel-3")
		}
		if app.Phase != release.PhaseReady.String() {
			t.Errorf("apps.phase after the rollback = %q, want %q", app.Phase, release.PhaseReady.String())
		}
	})

	t.Run("a refused rollback writes nothing", func(t *testing.T) {
		cases := []struct {
			name   string
			setup  func(t *testing.T) (*DB, string, release.State)
			reason string
		}{
			{
				name: "an unknown app",
				setup: func(t *testing.T) (*DB, string, release.State) {
					d, _ := openTemp(t)
					return d, "app-1", release.State{Current: &release.Release{ID: "rel-1"}}
				},
				reason: "an app that was never deployed here",
			},
			{
				name: "an app with no previous release",
				setup: func(t *testing.T) (*DB, string, release.State) {
					d, _ := openTemp(t)
					now := testTime()
					r1 := sampleRecord("app-1", "rel-1")
					s1 := mustDeploy(t, release.State{}, r1)
					mustRecord(t, d, sampleBinding(), r1, s1, now)
					next := mustSwitch(t, s1, r1.ReleaseID)
					if err := d.RecordSwitch(context.Background(), testCommand(t, "app-1"), "app-1", r1.ReleaseID, next, now); err != nil {
						t.Fatalf("RecordSwitch(rel-1) error = %v, want nil", err)
					}
					return d, "app-1", next
				},
				reason: "a state with no previous release to restore",
			},
			{
				name: "a next that does not follow from the stored one",
				setup: func(t *testing.T) (*DB, string, release.State) {
					d, _ := openTemp(t)
					now1 := testTime()
					stored := recordSwitchTwice(t, d, now1, now1.Add(time.Hour))
					return d, "app-1", stored
				},
				reason: "the state before the rollback instead of the state Rollback returned",
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				d, appID, next := tc.setup(t)
				before := snapshot(t, d)

				err := d.RecordRollback(ctx, testCommand(t, appID), appID, next, testTime().Add(2*time.Hour))

				if err == nil {
					t.Fatalf("RecordRollback on %s error = nil, want an error", tc.reason)
				}
				if after := snapshot(t, d); !reflect.DeepEqual(before, after) {
					t.Errorf("a refused RecordRollback changed the rows:\n before = %v\n after  = %v", before, after)
				}
			})
		}
	})
}
