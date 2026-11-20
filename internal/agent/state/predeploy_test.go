package state

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"moonglass.dev/moonglass/internal/release"
)

// preDeployRow is the part of a releases row a pre-deploy outcome changes.
type preDeployRow struct {
	status      string
	reversible  sql.NullBool
	containerID string
	errText     string
}

func readPreDeployRow(t *testing.T, d *DB, appID, releaseID string) preDeployRow {
	t.Helper()
	var row preDeployRow
	err := d.db.QueryRowContext(context.Background(),
		`SELECT status, pre_deploy_reversible, container_id, error FROM releases WHERE app_id = ? AND release_id = ?`,
		appID, releaseID).
		Scan(&row.status, &row.reversible, &row.containerID, &row.errText)
	if err != nil {
		t.Fatalf("read pre-deploy row for app %q and release %q: %v", appID, releaseID, err)
	}
	return row
}

func mustPreDeployDone(t *testing.T, s release.State, id string, ok bool) release.State {
	t.Helper()
	next, err := s.PreDeployDone(id, ok)
	if err != nil {
		t.Fatalf("State.PreDeployDone(%q, %v) error = %v, want nil", id, ok, err)
	}
	return next
}

// preDeployFixture writes a current and a previous release, then deploys rel-3
// with or without a pre-deploy step, so the stored state has Current and
// Previous set and rel-3 is the pending candidate.
func preDeployFixture(t *testing.T, hasPreDeploy bool) (*DB, Record, release.State) {
	t.Helper()
	d, _ := openTemp(t)
	b := sampleBinding()

	r1 := sampleRecord("app-1", "rel-1")
	s1 := mustDeploy(t, release.State{}, r1)
	mustRecord(t, d, b, r1, s1, testTime())

	r2 := sampleRecord("app-1", "rel-2")
	s2 := mustDeploy(t, mustSwitch(t, s1, r1.ReleaseID), r2)
	mustRecord(t, d, b, r2, s2, testTime().Add(time.Hour))

	r3 := sampleRecord("app-1", "rel-3")
	r3.HasPreDeploy = hasPreDeploy
	s3 := mustDeploy(t, mustSwitch(t, s2, r2.ReleaseID), r3)
	mustRecord(t, d, b, r3, s3, testTime().Add(2*time.Hour))
	return d, r3, s3
}

func TestRecordPreDeployOutcome(t *testing.T) {
	ctx := context.Background()
	const abortDetail = "pre-deploy step exited with code 3; its output is in the logs of container mg-app-1-rel-3-pre"

	type testCase struct {
		next         func(t *testing.T, stored release.State) release.State
		name         string
		releaseID    string
		detail       string
		hasPreDeploy bool
		empty        bool
		reversible   bool
		wantErr      bool
	}

	cases := []testCase{
		{
			name:         "success with reversible true",
			hasPreDeploy: true,
			releaseID:    "rel-3",
			next: func(t *testing.T, stored release.State) release.State {
				return mustPreDeployDone(t, stored, "rel-3", true)
			},
			reversible: true,
		},
		{
			name:         "success with reversible false",
			hasPreDeploy: true,
			releaseID:    "rel-3",
			next: func(t *testing.T, stored release.State) release.State {
				return mustPreDeployDone(t, stored, "rel-3", true)
			},
			reversible: false,
		},
		{
			name:         "abort with reversible true",
			hasPreDeploy: true,
			releaseID:    "rel-3",
			next: func(t *testing.T, stored release.State) release.State {
				return mustPreDeployDone(t, stored, "rel-3", false)
			},
			reversible: true,
			detail:     abortDetail,
		},
		{
			name:         "abort with reversible false",
			hasPreDeploy: true,
			releaseID:    "rel-3",
			next: func(t *testing.T, stored release.State) release.State {
				return mustPreDeployDone(t, stored, "rel-3", false)
			},
			reversible: false,
			detail:     abortDetail,
		},
		{
			name:      "unknown app",
			empty:     true,
			releaseID: "rel-3",
			next: func(_ *testing.T, _ release.State) release.State {
				return release.State{Candidate: &release.Release{ID: "rel-3"}, Phase: release.PhaseReady}
			},
			wantErr: true,
		},
		{
			name:         "release is not the candidate",
			hasPreDeploy: true,
			releaseID:    "rel-other",
			next: func(_ *testing.T, stored release.State) release.State {
				return release.State{
					Current:   stored.Current,
					Previous:  stored.Previous,
					Candidate: &release.Release{ID: "rel-other"},
					Phase:     release.PhaseReady,
				}
			},
			wantErr: true,
		},
		{
			name:         "candidate was deployed without a pre-deploy step",
			hasPreDeploy: false,
			releaseID:    "rel-3",
			next: func(_ *testing.T, stored release.State) release.State {
				return stored
			},
			wantErr: true,
		},
		{
			name:         "next state does not follow from the stored one",
			hasPreDeploy: true,
			releaseID:    "rel-3",
			next: func(_ *testing.T, stored release.State) release.State {
				return release.State{Current: stored.Current, Previous: stored.Previous, Phase: release.PhaseReady}
			},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var (
				d      *DB
				rec    Record
				stored release.State
			)
			if tc.empty {
				d, _ = openTemp(t)
			} else {
				d, rec, stored = preDeployFixture(t, tc.hasPreDeploy)
			}
			next := tc.next(t, stored)
			before := snapshot(t, d)

			err := d.RecordPreDeploy(ctx, testCommand(t, "app-1"), "app-1", tc.releaseID, next, tc.reversible, tc.detail, testTime())

			if tc.wantErr {
				if err == nil {
					t.Fatalf("RecordPreDeploy(%s) error = nil, want an error", tc.name)
				}
				if after := snapshot(t, d); !reflect.DeepEqual(before, after) {
					t.Errorf("a refused RecordPreDeploy changed the rows:\n before = %v\n after  = %v", before, after)
				}
				return
			}
			if err != nil {
				t.Fatalf("RecordPreDeploy(app %q, release %q) error = %v, want nil", "app-1", tc.releaseID, err)
			}

			row := readPreDeployRow(t, d, "app-1", tc.releaseID)
			app := readApp(t, d, "app-1")

			if next.Candidate != nil {
				if row.status != "candidate" {
					t.Errorf("releases.status after a successful step = %q, want %q", row.status, "candidate")
				}
				if !row.reversible.Valid || row.reversible.Bool != tc.reversible {
					t.Errorf("releases.pre_deploy_reversible = %+v, want a valid %v", row.reversible, tc.reversible)
				}
				if row.containerID != rec.ContainerID {
					t.Errorf("releases.container_id after a successful step = %q, want %q", row.containerID, rec.ContainerID)
				}
				if app.Phase != release.PhaseReady.String() {
					t.Errorf("apps.phase after a successful step = %q, want %q", app.Phase, release.PhaseReady.String())
				}
				if !app.Candidate.Valid || app.Candidate.String != tc.releaseID {
					t.Errorf("apps.candidate_release_id after a successful step = %+v, want %q", app.Candidate, tc.releaseID)
				}
				if app.Current.String != stored.Current.ID {
					t.Errorf("apps.current_release_id after a successful step = %+v, want %q", app.Current, stored.Current.ID)
				}
				if app.Previous.String != stored.Previous.ID {
					t.Errorf("apps.previous_release_id after a successful step = %+v, want %q", app.Previous, stored.Previous.ID)
				}

				got, found, err := d.Release(ctx, "app-1", tc.releaseID)
				if err != nil {
					t.Fatalf("Release(%q, %q) error = %v, want nil", "app-1", tc.releaseID, err)
				}
				if !found {
					t.Fatalf("Release(%q, %q) found = false, want the stored record", "app-1", tc.releaseID)
				}
				if !reflect.DeepEqual(got, rec) {
					t.Errorf("Release(%q, %q) = %+v, want %+v", "app-1", tc.releaseID, got, rec)
				}
				return
			}

			if row.status != "aborted" {
				t.Errorf("releases.status after a failed step = %q, want %q", row.status, "aborted")
			}
			if !row.reversible.Valid || row.reversible.Bool != tc.reversible {
				t.Errorf("releases.pre_deploy_reversible = %+v, want a valid %v", row.reversible, tc.reversible)
			}
			if row.errText != tc.detail {
				t.Errorf("releases.error = %q, want %q", row.errText, tc.detail)
			}
			if row.containerID != "" {
				t.Errorf("releases.container_id after a failed step = %q, want it empty", row.containerID)
			}
			if app.Candidate.Valid {
				t.Errorf("apps.candidate_release_id after a failed step = %q, want NULL", app.Candidate.String)
			}
			if app.Phase != release.PhaseNone.String() {
				t.Errorf("apps.phase after a failed step = %q, want %q", app.Phase, release.PhaseNone.String())
			}
			if app.Current.String != stored.Current.ID {
				t.Errorf("apps.current_release_id after a failed step = %+v, want %q", app.Current, stored.Current.ID)
			}
			if app.Previous.String != stored.Previous.ID {
				t.Errorf("apps.previous_release_id after a failed step = %+v, want %q", app.Previous, stored.Previous.ID)
			}
		})
	}

	t.Run("release of an unknown pair is not found", func(t *testing.T) {
		d, _, _ := preDeployFixture(t, true)
		rec, found, err := d.Release(ctx, "app-1", "rel-missing")
		if err != nil {
			t.Fatalf("Release(%q, %q) error = %v, want nil", "app-1", "rel-missing", err)
		}
		if found {
			t.Errorf("Release(%q, %q) found = true, want false", "app-1", "rel-missing")
		}
		if !reflect.DeepEqual(rec, Record{}) {
			t.Errorf("Release(%q, %q) record = %+v, want the zero record", "app-1", "rel-missing", rec)
		}
	})
}
