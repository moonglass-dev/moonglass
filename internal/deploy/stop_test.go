package deploy

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"moonglass.dev/moonglass/internal/agent/state"
	"moonglass.dev/moonglass/internal/policy"
	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
)

// stopSetClock fixes the executor's clock at t until the test sets another.
func stopSetClock(x *Executor, t time.Time) {
	x.now = func() time.Time { return t }
}

// stopRequireSwitch switches app to rel and requires SUCCEEDED.
func stopRequireSwitch(t *testing.T, x *Executor, app, rel string) {
	t.Helper()
	res := x.SwitchRelease(context.Background(), switchMsg(app, rel))
	if got, want := res.GetOutcome(), agentv1.CommandOutcome_SUCCEEDED; got != want {
		t.Fatalf("SwitchRelease(%s) outcome = %v with detail %q, want %v", rel, got, res.GetDetail(), want)
	}
}

// stopDeployPending imports an image and deploys it with a pre-deploy step and
// port 8080, leaving the pending candidate its step then starts.
func stopDeployPending(t *testing.T, e *Engine, x *Executor, p *project, app, rel string) {
	t.Helper()
	ref := "moonglass.local/" + p.name + "/web:" + rel
	p.registerImage(ref)
	importImage(t, e, ref, []string{"/app"})
	msg := deployMsg(p.name, app, rel, ref)
	msg.Port = 8080
	msg.HasPreDeploy = true
	res := x.Deploy(context.Background(), msg)
	if got, want := res.GetOutcome(), agentv1.CommandOutcome_SUCCEEDED; got != want {
		t.Fatalf("Deploy(%s/%s) with a pre-deploy step outcome = %v with detail %q, want %v",
			app, rel, got, res.GetDetail(), want)
	}
}

// stopDueRow returns the due entry of app at rel, failing when DueStops does
// not list it.
func stopDueRow(t *testing.T, x *Executor, app, rel string, now time.Time) state.DueStop {
	t.Helper()
	due, err := x.db.DueStops(context.Background(), now)
	if err != nil {
		t.Fatalf("DueStops(%s) error = %v, want nil", now, err)
	}
	for _, d := range due {
		if d.AppID == app && d.ReleaseID == rel {
			return d
		}
	}
	t.Fatalf("DueStops(%s) = %+v, want a row for app %s release %s", now, due, app, rel)
	return state.DueStop{}
}

// TestStopPreviousStopsAndKeepsContainer covers the previous release's stop: its
// container is stopped and kept, its row keeps container_id and loses stop_after,
// and a rollback inside the window starts it, gates it and succeeds.
func TestStopPreviousStopsAndKeepsContainer(t *testing.T) {
	ctx := context.Background()
	e := testEngine(t)
	p := testProject(t, e)
	x, dir, _ := newSwitchExecutor(t, e)
	x.healthLimit = 20 * time.Second
	app := testAppID(p, "stopprev")
	const domain = "stopprev.example.com"

	requireDeployOutcome(t, deployDomains(t, e, x, p, app, "r1", []string{domain}),
		agentv1.CommandOutcome_SUCCEEDED, "")
	stopRequireSwitch(t, x, app, "r1")
	id := singleContainerID(t, e, testContainerName(app, "r1"))

	// The r2 switch happened 11 minutes ago, so r1 is a previous release past
	// its ten-minute stop_after.
	stopSetClock(x, time.Now().Add(-11*time.Minute))
	requireDeployOutcome(t, deployDomains(t, e, x, p, app, "r2", []string{domain}),
		agentv1.CommandOutcome_SUCCEEDED, "")
	stopRequireSwitch(t, x, app, "r2")
	x.now = time.Now

	if err := x.stopDue(ctx, time.Now()); err != nil {
		t.Fatalf("stopDue() error = %v, want nil", err)
	}

	c := requireContainer(t, e, testContainerName(app, "r1"))
	if c.State == nil || c.State.Running {
		t.Errorf("container of r1 state = %+v, want it stopped and present", c.State)
	}
	row, found := storedRow(t, dir, app, "r1")
	if !found {
		t.Fatalf("releases row for %s/r1 = missing, want the previous row kept", app)
	}
	requireEqual(t, "r1 status after the stop", row.Status, "previous")
	requireEqual(t, "r1 container_id after the stop", row.ContainerID, id)
	if times := storedTimes(t, dir, app, "r1"); times.stopAfter.Valid {
		t.Errorf("r1 stop_after after the stop = %q, want NULL", times.stopAfter.String)
	}

	res := x.Rollback(ctx, rollbackMsg(app))
	if got, want := res.GetOutcome(), agentv1.CommandOutcome_SUCCEEDED; got != want {
		t.Fatalf("Rollback() after the stop outcome = %v with detail %q, want %v", got, res.GetDetail(), want)
	}
	requireRunning(t, e, testContainerName(app, "r1"))
}

// TestStopRetiredRemovesContainerAndPreDeployStep covers the retired release's
// stop: its container and its -pre step container are removed, its row loses
// container_id and stop_after, and the other releases and the project network
// are untouched.
func TestStopRetiredRemovesContainerAndPreDeployStep(t *testing.T) {
	ctx := context.Background()
	e := testEngine(t)
	p := testProject(t, e)
	x, dir, _ := newSwitchExecutor(t, e)
	x.healthLimit = 20 * time.Second
	app := testAppID(p, "stopret")
	const domain = "stopret.example.com"

	// r1 is deployed with a pre-deploy step, so its -pre container stays behind.
	stopDeployPending(t, e, x, p, app, "r1")
	if res := x.RunPreDeploy(ctx, preDeployMsg(app, "r1", false, "exit", "0")); res.GetOutcome() != agentv1.CommandOutcome_SUCCEEDED {
		t.Fatalf("RunPreDeploy(r1) outcome = %v with detail %q, want SUCCEEDED", res.GetOutcome(), res.GetDetail())
	}
	stopRequireSwitch(t, x, app, "r1")

	requireDeployOutcome(t, deployDomains(t, e, x, p, app, "r2", []string{domain}),
		agentv1.CommandOutcome_SUCCEEDED, "")
	stopRequireSwitch(t, x, app, "r2")
	requireRunning(t, e, testContainerName(app, "r1"))
	requireRunning(t, e, testContainerName(app, "r2"))

	// The r3 switch happened 31 seconds ago, so r1's retired stop is due.
	stopSetClock(x, time.Now().Add(-31*time.Second))
	requireDeployOutcome(t, deployDomains(t, e, x, p, app, "r3", []string{domain}),
		agentv1.CommandOutcome_SUCCEEDED, "")
	stopRequireSwitch(t, x, app, "r3")
	x.now = time.Now

	r2ID := singleContainerID(t, e, testContainerName(app, "r2"))
	r3ID := singleContainerID(t, e, testContainerName(app, "r3"))
	networkID := requireNetwork(t, e, "mg-"+p.name).ID

	if err := x.stopDue(ctx, time.Now()); err != nil {
		t.Fatalf("stopDue() error = %v, want nil", err)
	}

	requireNoContainerNamed(t, e, testContainerName(app, "r1"))
	requireNoContainerNamed(t, e, testPreDeployName(app, "r1"))
	requireEqual(t, "r2 container after the stop", containerIDsNamed(t, e, testContainerName(app, "r2")), []string{r2ID})
	requireEqual(t, "r3 container after the stop", containerIDsNamed(t, e, testContainerName(app, "r3")), []string{r3ID})
	requireRunning(t, e, testContainerName(app, "r2"))
	requireRunning(t, e, testContainerName(app, "r3"))
	requireEqual(t, "project network after the stop", requireNetwork(t, e, "mg-"+p.name).ID, networkID)

	row, found := storedRow(t, dir, app, "r1")
	if !found {
		t.Fatalf("releases row for %s/r1 = missing, want the retired row kept", app)
	}
	requireEqual(t, "r1 status after the stop", row.Status, "retired")
	requireEqual(t, "r1 container_id after the stop", row.ContainerID, "")
	if times := storedTimes(t, dir, app, "r1"); times.stopAfter.Valid {
		t.Errorf("r1 stop_after after the stop = %q, want NULL", times.stopAfter.String)
	}
}

// TestStopNeverStopsRestoredRelease covers a rollback that commits after a
// previous row became due: the restored release keeps running and serving, the
// release rolled back from is stopped when its own stop_after comes, and a stop
// and a rollback of the same app never run at the same time.
func TestStopNeverStopsRestoredRelease(t *testing.T) {
	ctx := context.Background()
	e := testEngine(t)
	p := testProject(t, e)
	x, dir, _ := newSwitchExecutor(t, e)
	x.healthLimit = 20 * time.Second
	app := testAppID(p, "stopres")
	const domain = "stopres.example.com"

	requireDeployOutcome(t, deployDomains(t, e, x, p, app, "r1", []string{domain}),
		agentv1.CommandOutcome_SUCCEEDED, "")
	stopRequireSwitch(t, x, app, "r1")

	stopSetClock(x, time.Now().Add(-11*time.Minute))
	requireDeployOutcome(t, deployDomains(t, e, x, p, app, "r2", []string{domain}),
		agentv1.CommandOutcome_SUCCEEDED, "")
	stopRequireSwitch(t, x, app, "r2")
	x.now = time.Now

	now := time.Now()
	stale := stopDueRow(t, x, app, "r1", now)

	res := x.Rollback(ctx, rollbackMsg(app))
	if got, want := res.GetOutcome(), agentv1.CommandOutcome_SUCCEEDED; got != want {
		t.Fatalf("Rollback() outcome = %v with detail %q, want %v", got, res.GetDetail(), want)
	}
	requireRunning(t, e, testContainerName(app, "r1"))
	r1ID := singleContainerID(t, e, testContainerName(app, "r1"))

	if err := x.stopOne(ctx, stale.AppID, stale.ReleaseID, now); err != nil {
		t.Fatalf("stopOne(%s) after the rollback error = %v, want nil", stale.ReleaseID, err)
	}
	requireRunning(t, e, testContainerName(app, "r1"))
	if got := singleContainerID(t, e, testContainerName(app, "r1")); got != r1ID {
		t.Errorf("restored r1 container = %q, want the same container %q", got, r1ID)
	}
	addr := net.JoinHostPort(containerIPOn(t, e, p, app, "r1"), "8080")
	if err := probe(ctx, addr, ""); err != nil {
		t.Errorf("probe(%s) error = %v, want the restored release serving", addr, err)
	}

	// r2 became retired at the rollback and is due 30 seconds later.
	r2Stop := storedTimes(t, dir, app, "r2").stopAfter
	if !r2Stop.Valid {
		t.Fatalf("r2 stop_after = NULL, want the rollback's 30-second drain")
	}
	stopAt := parseStoredTime(t, "r2 stop_after", r2Stop.String).Add(time.Second)
	if err := x.stopDue(ctx, stopAt); err != nil {
		t.Fatalf("stopDue(%s) error = %v, want nil", stopAt, err)
	}
	requireNoContainerNamed(t, e, testContainerName(app, "r2"))
	requireRunning(t, e, testContainerName(app, "r1"))

	// A stop and a Rollback of the same app never run at the same time.
	unlock := x.lockApp(app)
	started := make(chan struct{})
	rollbackDone := make(chan *agentv1.CommandResult, 1)
	go func() {
		close(started)
		rollbackDone <- x.Rollback(ctx, rollbackMsg(app))
	}()
	<-started
	select {
	case res := <-rollbackDone:
		t.Fatalf("Rollback returned (%v) while the app lock was held, want it to wait", res.GetOutcome())
	case <-time.After(300 * time.Millisecond):
	}
	unlock()
	select {
	case <-rollbackDone:
	case <-time.After(10 * time.Second):
		t.Fatalf("Rollback did not return within 10s after the app lock was released")
	}

	// A stop of r2 is no longer due after the stop above, so it returns at once
	// without an Engine call. A stopOne that returned while the lock was held
	// would therefore prove that it never took the lock.
	if times := storedTimes(t, dir, app, "r2"); times.status != "retired" || times.stopAfter.Valid {
		t.Fatalf("r2 row after its stop = %+v, want a retired row with no stop_after", times)
	}
	unlock = x.lockApp(app)
	started = make(chan struct{})
	stopDone := make(chan error, 1)
	go func() {
		close(started)
		stopDone <- x.stopOne(ctx, app, "r2", time.Now())
	}()
	<-started
	select {
	case err := <-stopDone:
		t.Fatalf("stopOne of the stopped r2 returned (%v) while the app lock was held, want it to wait", err)
	case <-time.After(300 * time.Millisecond):
	}
	unlock()
	select {
	case err := <-stopDone:
		if err != nil {
			t.Errorf("stopOne of the stopped r2 after the app lock was released error = %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("stopOne did not return within 10s after the app lock was released")
	}
}

// TestStopRefusesUnmanagedContainer covers a recorded container_id that names a
// container without the managed labels of its app and release: it is neither
// stopped nor removed, the row is unchanged, and the error names the container,
// the app and the release.
func TestStopRefusesUnmanagedContainer(t *testing.T) {
	ctx := context.Background()
	e := testEngine(t)
	p := testProject(t, e)
	x, dir, _ := newSwitchExecutor(t, e)
	x.healthLimit = 20 * time.Second
	app := testAppID(p, "stoploose")
	const domain = "stoploose.example.com"

	requireDeployOutcome(t, deployDomains(t, e, x, p, app, "r1", []string{domain}),
		agentv1.CommandOutcome_SUCCEEDED, "")
	stopRequireSwitch(t, x, app, "r1")

	stopSetClock(x, time.Now().Add(-11*time.Minute))
	requireDeployOutcome(t, deployDomains(t, e, x, p, app, "r2", []string{domain}),
		agentv1.CommandOutcome_SUCCEEDED, "")
	stopRequireSwitch(t, x, app, "r2")
	x.now = time.Now

	image := "moonglass.local/" + p.name + "/web:r1"
	otherApp := testAppID(p, "stopother")
	cases := []struct {
		name   string
		suffix string
		spec   policy.Spec
	}{
		{
			name:   "a container without the Moonglass labels",
			suffix: "loose",
			spec:   fixtureSpec(p, app, "r1", image, false),
		},
		{
			name:   "a managed container of another app",
			suffix: "other-app",
			spec:   fixtureSpec(p, otherApp, "r1", image, true),
		},
		{
			name:   "a managed container of another release",
			suffix: "other-release",
			spec:   fixtureSpec(p, app, "r9", image, true),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			name := "mg-" + p.name + "-" + tc.suffix
			id := createFixture(t, e, emptyPolicy(t, policy.Env{}), tc.spec, name)
			if _, err := e.cli.ContainerStart(ctx, id, client.ContainerStartOptions{}); err != nil {
				t.Fatalf("ContainerStart(%s) error = %v, want nil", id, err)
			}
			execAgentSQL(t, dir,
				`UPDATE releases SET container_id = ? WHERE app_id = ? AND release_id = ?`, id, app, "r1")

			before := stateText(t, dir)
			err := x.stopDue(ctx, time.Now())
			if err == nil {
				t.Fatalf("stopDue() with container %s recorded for %s/r1 error = nil, want an error", id, app)
			}
			for _, want := range []string{id, app, "r1"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("stopDue() error = %q, want it to name %q", err, want)
				}
			}
			requireRunning(t, e, name)
			requireEqual(t, "agent state rows after the refusal", stateText(t, dir), before)
		})
	}
}

// TestReconcileCarriesOutDueStops covers the start-up reconcile: it removes
// managed containers no release references, as before, carries out every stop
// due at the time it runs, and leaves a stop that is not yet due pending.
func TestReconcileCarriesOutDueStops(t *testing.T) {
	ctx := context.Background()
	e := testEngine(t)
	p := testProject(t, e)
	x, dir, _ := newSwitchExecutor(t, e)
	x.healthLimit = 20 * time.Second
	x.scope = []string{testLabelProject + "=" + p.name}
	appA := testAppID(p, "reca")
	appB := testAppID(p, "recb")
	now := time.Now()

	// App A: r2 is previous and due, r1 is retired and due, r3 is current.
	stopSetClock(x, now.Add(-40*time.Minute))
	requireDeployOutcome(t, deployDomains(t, e, x, p, appA, "r1", []string{"reca.example.com"}),
		agentv1.CommandOutcome_SUCCEEDED, "")
	stopRequireSwitch(t, x, appA, "r1")
	stopSetClock(x, now.Add(-30*time.Minute))
	requireDeployOutcome(t, deployDomains(t, e, x, p, appA, "r2", []string{"reca.example.com"}),
		agentv1.CommandOutcome_SUCCEEDED, "")
	stopRequireSwitch(t, x, appA, "r2")
	stopSetClock(x, now.Add(-20*time.Minute))
	requireDeployOutcome(t, deployDomains(t, e, x, p, appA, "r3", []string{"reca.example.com"}),
		agentv1.CommandOutcome_SUCCEEDED, "")
	stopRequireSwitch(t, x, appA, "r3")

	// App B: its previous row is not due yet.
	stopSetClock(x, now.Add(-time.Minute))
	requireDeployOutcome(t, deployDomains(t, e, x, p, appB, "b1", []string{"recb.example.com"}),
		agentv1.CommandOutcome_SUCCEEDED, "")
	stopRequireSwitch(t, x, appB, "b1")
	requireDeployOutcome(t, deployDomains(t, e, x, p, appB, "b2", []string{"recb.example.com"}),
		agentv1.CommandOutcome_SUCCEEDED, "")
	stopRequireSwitch(t, x, appB, "b2")
	x.now = time.Now

	r2ID := singleContainerID(t, e, testContainerName(appA, "r2"))
	ghostName := testContainerName(appA, "r0")
	ghost := createFixture(t, e, emptyPolicy(t, policy.Env{}),
		fixtureSpec(p, appA, "r0", "moonglass.local/"+p.name+"/web:r3", true), ghostName)

	if err := x.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile() error = %v, want nil", err)
	}

	if ids := containerIDsNamed(t, e, ghostName); len(ids) != 0 {
		t.Errorf("unreferenced container %s (%s) is still present, want it removed", ghostName, ghost)
	}

	requireNoContainerNamed(t, e, testContainerName(appA, "r1"))
	prevContainer := requireContainer(t, e, testContainerName(appA, "r2"))
	requireEqual(t, "A's previous container ID after the reconcile", prevContainer.ID, r2ID)
	if prevContainer.State == nil || prevContainer.State.Running {
		t.Errorf("A's previous container %s state after the reconcile = %+v, want it stopped and present", r2ID, prevContainer.State)
	}
	requireRunning(t, e, testContainerName(appA, "r3"))
	requireRunning(t, e, testContainerName(appB, "b1"))
	requireRunning(t, e, testContainerName(appB, "b2"))

	retired, found := storedRow(t, dir, appA, "r1")
	if !found {
		t.Fatalf("releases row for %s/r1 = missing, want the retired row kept", appA)
	}
	requireEqual(t, "A's r1 status after the reconcile", retired.Status, "retired")
	requireEqual(t, "A's r1 container_id after the reconcile", retired.ContainerID, "")
	if times := storedTimes(t, dir, appA, "r1"); times.stopAfter.Valid {
		t.Errorf("A's r1 stop_after after the reconcile = %q, want NULL", times.stopAfter.String)
	}

	previous, found := storedRow(t, dir, appA, "r2")
	if !found {
		t.Fatalf("releases row for %s/r2 = missing, want the previous row kept", appA)
	}
	requireEqual(t, "A's r2 status after the reconcile", previous.Status, "previous")
	requireEqual(t, "A's r2 container_id after the reconcile", previous.ContainerID, r2ID)
	if times := storedTimes(t, dir, appA, "r2"); times.stopAfter.Valid {
		t.Errorf("A's r2 stop_after after the reconcile = %q, want NULL", times.stopAfter.String)
	}

	if times := storedTimes(t, dir, appB, "b1"); !times.stopAfter.Valid {
		t.Errorf("B's b1 stop_after after the reconcile = NULL, want the pending stop kept")
	}
}
