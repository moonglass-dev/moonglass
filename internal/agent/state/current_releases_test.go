package state

import (
	"context"
	"reflect"
	"testing"

	"moonglass.dev/moonglass/internal/release"
)

// makeCurrentInState moves app's candidate release to current with the direct
// update W2-4b's switch will make, so this test does not depend on it.
func makeCurrentInState(t *testing.T, d *DB, appID string) {
	t.Helper()
	res, err := d.db.ExecContext(context.Background(),
		`UPDATE apps SET current_release_id = candidate_release_id, candidate_release_id = NULL, phase = 'none'
		WHERE app_id = ?`, appID)
	if err != nil {
		t.Fatalf("make %s's candidate current: %v", appID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		t.Fatalf("count the apps rows updated for %s: %v", appID, err)
	}
	if affected != 1 {
		t.Fatalf("apps rows updated for %s = %d, want 1", appID, affected)
	}
}

func TestCurrentReleasesListsEveryAppsCurrentRelease(t *testing.T) {
	ctx := context.Background()
	d, _ := openTemp(t)
	b := sampleBinding()
	now := testTime()

	recA := sampleRecord("app-a", "r1")
	mustRecord(t, d, b, recA, mustDeploy(t, release.State{}, recA), now)

	recB := sampleRecord("app-b", "r1")
	recB.Domains = nil
	recB.Port = 0
	mustRecord(t, d, b, recB, mustDeploy(t, release.State{}, recB), now)

	recC := sampleRecord("app-c", "r1")
	mustRecord(t, d, b, recC, mustDeploy(t, release.State{}, recC), now)

	recD := sampleRecord("app-d", "r1")
	if err := d.RecordFailed(ctx, recD, "pull failed: manifest unknown", now); err != nil {
		t.Fatalf("RecordFailed(app %q, release %q) error = %v, want nil", recD.AppID, recD.ReleaseID, err)
	}

	makeCurrentInState(t, d, recA.AppID)
	makeCurrentInState(t, d, recB.AppID)

	got, err := d.CurrentReleases(ctx)
	if err != nil {
		t.Fatalf("CurrentReleases() error = %v, want nil", err)
	}

	// A nil domains slice is stored as the JSON array [] and read back as an
	// empty slice, not nil.
	wantB := recB
	wantB.Domains = []string{}
	want := []CurrentRelease{
		{Binding: b, Record: recA},
		{Binding: b, Record: wantB},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CurrentReleases() = %#v, want %#v; app-c (candidate only) and app-d (failed) must be absent",
			got, want)
	}

	t.Run("fresh state", func(t *testing.T) {
		fresh, _ := openTemp(t)
		got, err := fresh.CurrentReleases(ctx)
		if err != nil {
			t.Fatalf("CurrentReleases() on a fresh state error = %v, want nil", err)
		}
		if len(got) != 0 {
			t.Errorf("CurrentReleases() on a fresh state = %#v, want an empty result", got)
		}
	})
}
