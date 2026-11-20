package deploy

import (
	"context"
	"strings"
	"testing"
	"time"

	"moonglass.dev/moonglass/internal/agent/state"
	"moonglass.dev/moonglass/internal/policy"
	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
)

// TestExecutorRunStopsAndRetainsUntilCancelled covers Executor.Run: it reconciles
// and retains at start, carries out a stop within two seconds of its stop_after,
// runs retention again at its interval, reports a failing step through onError
// while it keeps running, and returns only when ctx ends.
func TestExecutorRunStopsAndRetainsUntilCancelled(t *testing.T) {
	ctx := context.Background()
	e := testEngine(t)
	p := testProject(t, e)
	x, dir := newExecutor(t, e)
	x.healthLimit = 20 * time.Second
	x.scope = []string{testLabelProject + "=" + p.name}
	x.retainEvery = time.Second
	app := testAppID(p, "run")
	appB := testAppID(p, "runb")

	// r1 and r2 are switched now; r3 is left ready and switched just before Run
	// starts, so r1's retired stop is due about two seconds later.
	requireDeployOutcome(t, deployDomains(t, e, x, p, app, "r1", []string{"run.example.com"}),
		agentv1.CommandOutcome_SUCCEEDED, "")
	stopRequireSwitch(t, x, app, "r1")
	requireDeployOutcome(t, deployDomains(t, e, x, p, app, "r2", []string{"run.example.com"}),
		agentv1.CommandOutcome_SUCCEEDED, "")
	stopRequireSwitch(t, x, app, "r2")
	requireDeployOutcome(t, deployDomains(t, e, x, p, app, "r3", []string{"run.example.com"}),
		agentv1.CommandOutcome_SUCCEEDED, "")

	// App B's previous row is due now, and it names an unmanaged container, so
	// its stop fails on every turn.
	requireDeployOutcome(t, deployDomains(t, e, x, p, appB, "b1", []string{"runb.example.com"}),
		agentv1.CommandOutcome_SUCCEEDED, "")
	stopRequireSwitch(t, x, appB, "b1")
	stopSetClock(x, time.Now().Add(-11*time.Minute))
	requireDeployOutcome(t, deployDomains(t, e, x, p, appB, "b2", []string{"runb.example.com"}),
		agentv1.CommandOutcome_SUCCEEDED, "")
	stopRequireSwitch(t, x, appB, "b2")
	x.now = time.Now

	ghostName := "mg-" + p.name + "-ghost"
	createFixture(t, e, emptyPolicy(t, policy.Env{}),
		fixtureSpec(p, appB, "b0", "moonglass.local/"+p.name+"/web:b1", true), ghostName)

	oldFailed := state.Record{AppID: app, ReleaseID: "r-failed-old"}
	if err := x.db.RecordFailed(ctx, oldFailed, "pull failed", time.Now().Add(-8*24*time.Hour)); err != nil {
		t.Fatalf("RecordFailed(%q) error = %v, want nil", oldFailed.ReleaseID, err)
	}

	looseName := "mg-" + p.name + "-loose"
	loose := createFixture(t, e, emptyPolicy(t, policy.Env{}),
		fixtureSpec(p, appB, "b1", "moonglass.local/"+p.name+"/web:b1", false), looseName)
	execAgentSQL(t, dir,
		`UPDATE releases SET container_id = ? WHERE app_id = ? AND release_id = ?`, loose, appB, "b1")

	// The clock moves with the switch, so the stop is due two seconds after the
	// commit, whatever the health gate took.
	x.now = func() time.Time { return time.Now().Add(-28 * time.Second) }
	stopRequireSwitch(t, x, app, "r3")
	x.now = time.Now

	r1Stop := storedTimes(t, dir, app, "r1").stopAfter
	if !r1Stop.Valid {
		t.Fatalf("r1 stop_after = NULL after the r3 switch, want the 30-second drain")
	}
	stopAfter := parseStoredTime(t, "r1 stop_after", r1Stop.String)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	reported := make(chan error, 128)
	done := make(chan struct{})
	go func() {
		defer close(done)
		x.Run(runCtx, func(err error) { reported <- err })
	}()

	waitFor(t, "the unreferenced container to be removed", 10*time.Second, func() bool {
		return len(containerIDsNamed(t, e, ghostName)) == 0
	})
	waitFor(t, "the old failed row to be deleted", 10*time.Second, func() bool {
		_, found := storedRow(t, dir, app, "r-failed-old")
		return !found
	})

	waitFor(t, "r1's stop within two seconds of its stop_after", time.Until(stopAfter)+2*time.Second, func() bool {
		if ids := containerIDsNamed(t, e, testContainerName(app, "r1")); len(ids) != 0 {
			return false
		}
		row, found := storedRow(t, dir, app, "r1")
		return found && row.Status == "retired" && row.ContainerID == "" &&
			!storedTimes(t, dir, app, "r1").stopAfter.Valid
	})

	select {
	case err := <-reported:
		if err == nil {
			t.Fatalf("Run reported a nil error, want the unmanaged container's failure")
		}
		for _, want := range []string{loose, appB, "b1"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("Run's reported error = %q, want it to name %q", err, want)
			}
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("Run reported no failing step within 10s, want the unmanaged container's failure")
	}

	select {
	case <-done:
		t.Fatalf("Run returned after a failing step, want it to keep running")
	default:
	}

	newFailed := state.Record{AppID: app, ReleaseID: "r-failed-new"}
	if err := x.db.RecordFailed(ctx, newFailed, "pull failed", time.Now().Add(-8*24*time.Hour)); err != nil {
		t.Fatalf("RecordFailed(%q) error = %v, want nil", newFailed.ReleaseID, err)
	}
	waitFor(t, "the second old failed row to be deleted by retention", 3*time.Second, func() bool {
		_, found := storedRow(t, dir, app, "r-failed-new")
		return !found
	})

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("Run did not return within 2s after ctx ended")
	}
}
