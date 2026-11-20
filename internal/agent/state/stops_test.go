package state

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"moonglass.dev/moonglass/internal/release"
)

// switchApp records rel as a candidate at created and switches it at switched,
// returning the state after the switch. The row carries the container
// sampleRecord gives it, so a stop can name it.
func switchApp(t *testing.T, d *DB, appID, releaseID string, created, switched time.Time, st release.State) release.State {
	t.Helper()
	r := sampleRecord(appID, releaseID)
	next := mustDeploy(t, st, r)
	mustRecord(t, d, sampleBinding(), r, next, created)
	after := mustSwitch(t, next, releaseID)
	if err := d.RecordSwitch(context.Background(), appID, releaseID, after, switched); err != nil {
		t.Fatalf("RecordSwitch(app %q, release %q) error = %v, want nil", appID, releaseID, err)
	}
	return after
}

// dueStopsApps records two apps whose previous and retired rows fall due at
// different times, plus a current row restored by a rollback, a candidate, an
// aborted row and a failed row that no stop may name.
//
// App B is recorded first, so every app-b row is inserted before every app-a
// row. Its b-r1 is retired and due before every app-a row, and its b-r2 falls
// due at the same instant as a-r1 but sorts after it by app and release. App A's
// a-r3 is the one due row that sorts last.
func dueStopsApps(t *testing.T) (*DB, time.Time) {
	t.Helper()
	ctx := context.Background()
	d, _ := openTemp(t)
	t0 := testTime()

	stB := switchApp(t, d, "app-b", "b-r1", t0, t0.Add(-30*time.Minute), release.State{})
	stB = switchApp(t, d, "app-b", "b-r2", t0, t0.Add(-20*time.Minute), stB)
	// The third switch retires b-r1 due at t0-8m and makes b-r2 a previous row
	// due ten minutes after the switch, at t0+90s.
	switchApp(t, d, "app-b", "b-r3", t0, t0.Add(-8*time.Minute-30*time.Second), stB)

	st := switchApp(t, d, "app-a", "a-r1", t0, t0.Add(-20*time.Minute), release.State{})
	st = switchApp(t, d, "app-a", "a-r2", t0, t0.Add(-10*time.Minute), st)
	// a-r1 becomes retired due at t0+90s, the same instant as b-r2, while a-r2
	// becomes previous. The rollback then restores a-r2 and clears its
	// stop_after, and retires a-r3 due at t0+2m30s.
	st = switchApp(t, d, "app-a", "a-r3", t0, t0.Add(60*time.Second), st)

	rolledBack := mustRollback(t, st)
	if err := d.RecordRollback(ctx, "app-a", rolledBack, t0.Add(2*time.Minute)); err != nil {
		t.Fatalf("RecordRollback(app-a) error = %v, want nil", err)
	}

	r4 := sampleRecord("app-a", "a-r4")
	withCandidate := mustDeploy(t, rolledBack, r4)
	mustRecord(t, d, sampleBinding(), r4, withCandidate, t0.Add(2*time.Minute))
	aborted, err := withCandidate.Abort("a-r4")
	if err != nil {
		t.Fatalf("State.Abort(%q) error = %v, want nil", "a-r4", err)
	}
	if err := d.RecordAbort(ctx, "app-a", "a-r4", aborted, "health gate failed"); err != nil {
		t.Fatalf("RecordAbort(app-a) error = %v, want nil", err)
	}

	r5 := sampleRecord("app-a", "a-r5")
	ready := mustDeploy(t, aborted, r5)
	mustRecord(t, d, sampleBinding(), r5, ready, t0.Add(3*time.Minute))

	if err := d.RecordFailed(ctx, sampleRecord("app-a", "a-failed"), "pull failed", t0.Add(3*time.Minute)); err != nil {
		t.Fatalf("RecordFailed(app-a) error = %v, want nil", err)
	}

	return d, t0
}

// TestDueStopsListsPreviousAndRetiredPastStopAfter covers the due rows of
// docs/specs/agent-state.md: both apps' previous and retired rows whose
// stop_after is at or before now, in stop_after order, then app, then release,
// and no current, candidate, aborted, failed or not-yet-due row.
func TestDueStopsListsPreviousAndRetiredPastStopAfter(t *testing.T) {
	d, t0 := dueStopsApps(t)

	cases := []struct {
		name string
		now  time.Time
		want []DueStop
	}{
		{
			// App B is the later app, yet its b-r1 is due first. a-r1 and b-r2
			// share a stop_after, and app-a sorts before app-b even though every
			// app-b row was inserted first.
			name: "every due row",
			now:  t0.Add(20 * time.Minute),
			want: []DueStop{
				{
					AppID: "app-b", ReleaseID: "b-r1", Status: "retired",
					ContainerID: "container-b-r1", StopAfter: t0.Add(-8 * time.Minute),
				},
				{
					AppID: "app-a", ReleaseID: "a-r1", Status: "retired",
					ContainerID: "container-a-r1", StopAfter: t0.Add(90 * time.Second),
				},
				{
					AppID: "app-b", ReleaseID: "b-r2", Status: "previous",
					ContainerID: "container-b-r2", StopAfter: t0.Add(90 * time.Second),
				},
				{
					AppID: "app-a", ReleaseID: "a-r3", Status: "retired",
					ContainerID: "container-a-r3", StopAfter: t0.Add(150 * time.Second),
				},
			},
		},
		{
			// now equals a-r1's and b-r2's stop_after, so both are due.
			name: "a row whose stop_after is now",
			now:  t0.Add(90 * time.Second),
			want: []DueStop{
				{
					AppID: "app-b", ReleaseID: "b-r1", Status: "retired",
					ContainerID: "container-b-r1", StopAfter: t0.Add(-8 * time.Minute),
				},
				{
					AppID: "app-a", ReleaseID: "a-r1", Status: "retired",
					ContainerID: "container-a-r1", StopAfter: t0.Add(90 * time.Second),
				},
				{
					AppID: "app-b", ReleaseID: "b-r2", Status: "previous",
					ContainerID: "container-b-r2", StopAfter: t0.Add(90 * time.Second),
				},
			},
		},
		{
			name: "only the rows due at now",
			now:  t0.Add(31 * time.Second),
			want: []DueStop{
				{
					AppID: "app-b", ReleaseID: "b-r1", Status: "retired",
					ContainerID: "container-b-r1", StopAfter: t0.Add(-8 * time.Minute),
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := d.DueStops(context.Background(), tc.now)
			if err != nil {
				t.Fatalf("DueStops(%s) error = %v, want nil", tc.now, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("DueStops(%s) = %+v, want %+v", tc.now, got, tc.want)
			}
		})
	}
}

// stopRecordSetup records app-1's r1, r2 and r3 as switched at t0, t0+1m and
// t0+1m, then r4 as a ready candidate at t0+2m. r2 is previous and due at
// t0+11m, r1 is retired and due at t0+1m30s, and r3 is current.
func stopRecordSetup(t *testing.T) (*DB, time.Time) {
	t.Helper()
	d, _ := openTemp(t)
	t0 := testTime()

	st := switchApp(t, d, "app-1", "r1", t0, t0, release.State{})
	st = switchApp(t, d, "app-1", "r2", t0, t0.Add(time.Minute), st)
	st = switchApp(t, d, "app-1", "r3", t0.Add(time.Minute), t0.Add(time.Minute), st)

	r4 := sampleRecord("app-1", "r4")
	ready := mustDeploy(t, st, r4)
	mustRecord(t, d, sampleBinding(), r4, ready, t0.Add(2*time.Minute))
	return d, t0
}

// TestRecordStoppedClearsStopAfter covers the carried-out stop of
// docs/specs/agent-state.md: a due previous row keeps its container and loses
// stop_after, a due retired row loses both, and every refusal writes nothing.
func TestRecordStoppedClearsStopAfter(t *testing.T) {
	ctx := context.Background()

	type stopCase struct {
		name        string
		prepare     func(t *testing.T, d *DB, t0 time.Time)
		releaseID   string
		now         func(t0 time.Time) time.Time
		wantErr     bool
		wantStatus  string
		wantContain string
	}

	cases := []stopCase{
		{
			name:        "a due previous row keeps its container",
			releaseID:   "r2",
			now:         func(t0 time.Time) time.Time { return t0.Add(11 * time.Minute) },
			wantStatus:  "previous",
			wantContain: "container-r2",
		},
		{
			name:        "a due retired row loses its container",
			releaseID:   "r1",
			now:         func(t0 time.Time) time.Time { return t0.Add(2 * time.Minute) },
			wantStatus:  "retired",
			wantContain: "",
		},
		{
			name:      "a current row",
			releaseID: "r3",
			now:       func(t0 time.Time) time.Time { return t0.Add(20 * time.Minute) },
			wantErr:   true,
		},
		{
			name:      "a candidate row",
			releaseID: "r4",
			now:       func(t0 time.Time) time.Time { return t0.Add(20 * time.Minute) },
			wantErr:   true,
		},
		{
			name:      "a row that is not due yet",
			releaseID: "r2",
			now:       func(t0 time.Time) time.Time { return t0.Add(5 * time.Minute) },
			wantErr:   true,
		},
		{
			name: "a row already recorded as stopped",
			prepare: func(t *testing.T, d *DB, t0 time.Time) {
				t.Helper()
				if err := d.RecordStopped(context.Background(), "app-1", "r1", t0.Add(2*time.Minute)); err != nil {
					t.Fatalf("the first RecordStopped(r1) error = %v, want nil", err)
				}
			},
			releaseID: "r1",
			now:       func(t0 time.Time) time.Time { return t0.Add(3 * time.Minute) },
			wantErr:   true,
		},
		{
			name:      "an unknown row",
			releaseID: "r-nope",
			now:       func(t0 time.Time) time.Time { return t0.Add(20 * time.Minute) },
			wantErr:   true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, t0 := stopRecordSetup(t)
			if tc.prepare != nil {
				tc.prepare(t, d, t0)
			}
			before := snapshot(t, d)
			appsBefore := tableRows(t, d, "apps")

			err := d.RecordStopped(ctx, "app-1", tc.releaseID, tc.now(t0))

			if tc.wantErr {
				if err == nil {
					t.Fatalf("RecordStopped(app %q, release %q) error = nil, want an error", "app-1", tc.releaseID)
				}
				for _, want := range []string{"app-1", tc.releaseID} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("RecordStopped error = %q, want it to name %q", err, want)
					}
				}
				if after := snapshot(t, d); !reflect.DeepEqual(before, after) {
					t.Errorf("a refused RecordStopped changed the rows:\n before = %v\n after  = %v", before, after)
				}
				if after := tableRows(t, d, "apps"); !reflect.DeepEqual(appsBefore, after) {
					t.Errorf("a refused RecordStopped changed the apps rows:\n before = %v\n after  = %v", appsBefore, after)
				}
				return
			}

			if err != nil {
				t.Fatalf("RecordStopped(app %q, release %q) error = %v, want nil", "app-1", tc.releaseID, err)
			}
			row := readRelease(t, d, "app-1", tc.releaseID)
			if row.Status != tc.wantStatus {
				t.Errorf("releases.status = %q, want %q", row.Status, tc.wantStatus)
			}
			if row.ContainerID != tc.wantContain {
				t.Errorf("releases.container_id = %q, want %q", row.ContainerID, tc.wantContain)
			}
			if row.StopAfter.Valid {
				t.Errorf("releases.stop_after = %q, want NULL after the stop", row.StopAfter.String)
			}
			if after := tableRows(t, d, "apps"); !reflect.DeepEqual(appsBefore, after) {
				t.Errorf("a carried-out stop changed the apps rows:\n before = %v\n after  = %v", appsBefore, after)
			}
		})
	}
}

// retainRelease is one row the retention test records.
type retainRelease struct {
	appID     string
	releaseID string
	imageID   string
	created   time.Time
	switched  time.Time
}

// recordRetained records rel as a candidate at rel.created and switches it at
// rel.switched, returning the state after the switch.
func recordRetained(t *testing.T, d *DB, rel retainRelease, st release.State) release.State {
	t.Helper()
	r := sampleRecord(rel.appID, rel.releaseID)
	if rel.imageID != "" {
		r.ImageID = rel.imageID
	}
	next := mustDeploy(t, st, r)
	mustRecord(t, d, sampleBinding(), r, next, rel.created)
	after := mustSwitch(t, next, rel.releaseID)
	if err := d.RecordSwitch(context.Background(), rel.appID, rel.releaseID, after, rel.switched); err != nil {
		t.Fatalf("RecordSwitch(app %q, release %q) error = %v, want nil", rel.appID, rel.releaseID, err)
	}
	return after
}

// releaseExists reports whether appID has a releases row for releaseID.
func releaseExists(t *testing.T, d *DB, appID, releaseID string) bool {
	t.Helper()
	var count int
	if err := d.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM releases WHERE app_id = ? AND release_id = ?`, appID, releaseID).
		Scan(&count); err != nil {
		t.Fatalf("count releases rows for app %q and release %q: %v", appID, releaseID, err)
	}
	return count > 0
}

// TestRetainKeepsFiveAndPrunesOldRows covers "Retention" in
// docs/specs/agent-state.md: per app the current, previous and three newest
// retired rows stay, an older retired row whose stop has not run stays, failed
// and aborted rows older than seven days go, and the returned image IDs are
// those of the deleted rows that no kept row of any app still names.
func TestRetainKeepsFiveAndPrunesOldRows(t *testing.T) {
	ctx := context.Background()
	d, _ := openTemp(t)
	base := testTime()
	now := base.Add(24 * time.Hour)

	// App A: current a-r7, previous a-r6, and five retired rows. a-r3 is the
	// oldest by created_at, so it is outside the three newest retired rows even
	// though insertion order and switched_at both make it the third newest. a-r1
	// and a-r2 share a created_at, and insertion order keeps a-r2 for the third
	// kept retired row and deletes a-r1.
	created := map[string]time.Time{
		"a-r1": base.Add(2 * time.Minute),
		"a-r2": base.Add(2 * time.Minute),
		"a-r3": base,
		"a-r4": base.Add(3 * time.Minute),
		"a-r5": base.Add(4 * time.Minute),
		"a-r6": base.Add(5 * time.Minute),
		"a-r7": base.Add(6 * time.Minute),
	}
	st := release.State{}
	for i := 1; i <= 7; i++ {
		rel := fmt.Sprintf("a-r%d", i)
		st = recordRetained(t, d, retainRelease{
			appID: "app-a", releaseID: rel,
			created: created[rel], switched: base.Add(time.Duration(i) * 10 * time.Minute),
		}, st)
	}

	// App B: the same shape, judged on its own. Its current row names the image
	// a-r1 names, so that image is not returned.
	stB := release.State{}
	for i := 1; i <= 7; i++ {
		rel := retainRelease{
			appID: "app-b", releaseID: fmt.Sprintf("b-r%d", i),
			created:  base.Add(time.Duration(20+i) * time.Minute),
			switched: base.Add(time.Duration(100+i) * time.Minute),
		}
		if i == 7 {
			rel.imageID = "sha256:image-a-r1"
		}
		stB = recordRetained(t, d, rel, stB)
	}

	// App C: a retired row outside the five whose stop has not run is kept.
	stC := release.State{}
	for i := 1; i <= 6; i++ {
		stC = recordRetained(t, d, retainRelease{
			appID: "app-c", releaseID: fmt.Sprintf("c-r%d", i),
			created:  base.Add(time.Duration(30+i) * time.Minute),
			switched: base.Add(time.Duration(200+i) * time.Minute),
		}, stC)
	}

	stopped := []struct{ appID, releaseID string }{
		{"app-a", "a-r1"},
		{"app-a", "a-r2"},
		{"app-a", "a-r3"},
		{"app-a", "a-r4"},
		{"app-a", "a-r5"},
		{"app-b", "b-r1"},
		{"app-b", "b-r2"},
		{"app-b", "b-r3"},
		{"app-b", "b-r4"},
		{"app-b", "b-r5"},
		{"app-c", "c-r2"},
		{"app-c", "c-r3"},
		{"app-c", "c-r4"},
	}
	for _, rel := range stopped {
		if err := d.RecordStopped(ctx, rel.appID, rel.releaseID, now); err != nil {
			t.Fatalf("RecordStopped(%s/%s) error = %v, want nil", rel.appID, rel.releaseID, err)
		}
	}

	// Failed and aborted rows on both sides of the seven-day line.
	if err := d.RecordFailed(ctx, sampleRecord("app-e", "e-old"), "pull failed", now.Add(-8*24*time.Hour)); err != nil {
		t.Fatalf("RecordFailed(app-e/e-old) error = %v, want nil", err)
	}
	if err := d.RecordFailed(ctx, sampleRecord("app-e", "e-new"), "pull failed", now.Add(-6*24*time.Hour)); err != nil {
		t.Fatalf("RecordFailed(app-e/e-new) error = %v, want nil", err)
	}

	stD := release.State{}
	for _, tc := range []struct {
		releaseID string
		created   time.Time
	}{
		{"d-old", now.Add(-8 * 24 * time.Hour)},
		{"d-new", now.Add(-6 * 24 * time.Hour)},
	} {
		r := sampleRecord("app-d", tc.releaseID)
		ready := mustDeploy(t, stD, r)
		mustRecord(t, d, sampleBinding(), r, ready, tc.created)
		aborted, err := ready.Abort(tc.releaseID)
		if err != nil {
			t.Fatalf("State.Abort(%q) error = %v, want nil", tc.releaseID, err)
		}
		if err := d.RecordAbort(ctx, "app-d", tc.releaseID, aborted, "health gate failed"); err != nil {
			t.Fatalf("RecordAbort(%s) error = %v, want nil", tc.releaseID, err)
		}
		stD = aborted
	}

	appsBefore := tableRows(t, d, "apps")
	ids, err := d.Retain(ctx, now)
	if err != nil {
		t.Fatalf("Retain(%s) error = %v, want nil", now, err)
	}

	// a-r1's image is named by app B's kept current row, so it is not returned.
	want := []string{"sha256:image-a-r3", "sha256:image-b-r1", "sha256:image-b-r2"}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("Retain(%s) image ids = %v, want %v", now, ids, want)
	}

	gone := []struct{ appID, releaseID string }{
		{"app-a", "a-r1"},
		{"app-a", "a-r3"},
		{"app-b", "b-r1"},
		{"app-b", "b-r2"},
		{"app-d", "d-old"},
		{"app-e", "e-old"},
	}
	for _, rel := range gone {
		if releaseExists(t, d, rel.appID, rel.releaseID) {
			t.Errorf("releases row %s/%s still exists after Retain, want it deleted", rel.appID, rel.releaseID)
		}
	}

	kept := []struct{ appID, releaseID string }{
		{"app-a", "a-r2"},
		{"app-a", "a-r4"},
		{"app-a", "a-r5"},
		{"app-a", "a-r6"},
		{"app-a", "a-r7"},
		{"app-b", "b-r3"},
		{"app-b", "b-r4"},
		{"app-b", "b-r5"},
		{"app-b", "b-r6"},
		{"app-b", "b-r7"},
		{"app-c", "c-r1"},
		{"app-c", "c-r2"},
		{"app-c", "c-r3"},
		{"app-c", "c-r4"},
		{"app-c", "c-r5"},
		{"app-c", "c-r6"},
		{"app-d", "d-new"},
		{"app-e", "e-new"},
	}
	for _, rel := range kept {
		if !releaseExists(t, d, rel.appID, rel.releaseID) {
			t.Errorf("releases row %s/%s is missing after Retain, want it kept", rel.appID, rel.releaseID)
		}
	}

	pending := readRelease(t, d, "app-c", "c-r1")
	if !pending.StopAfter.Valid {
		t.Errorf("c-r1 stop_after = NULL, want the pending stop of a row outside the five kept")
	}

	if after := tableRows(t, d, "apps"); !reflect.DeepEqual(appsBefore, after) {
		t.Errorf("Retain changed the apps rows:\n before = %v\n after  = %v", appsBefore, after)
	}
}
