package deploy

import (
	"bytes"
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"moonglass.dev/moonglass/internal/agent/state"
	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
	"moonglass.dev/moonglass/internal/release"
)

// TestSwitchReleaseRejectsDomainRoutedByAnotherApp covers two apps whose
// candidates hold one domain: Deploy allows both, the first switch succeeds, and
// the second is refused inside the push lock without posting, committing or
// touching the candidate (docs/specs/release.md, "SwitchRelease").
func TestSwitchReleaseRejectsDomainRoutedByAnotherApp(t *testing.T) {
	e := testEngine(t)
	p := testProject(t, e)
	x, dir, fake := newSwitchExecutor(t, e)
	x.healthLimit = 20 * time.Second
	appA := testAppID(p, "a")
	appB := testAppID(p, "b")
	const domain = "x.example"

	requireDeployOutcome(t, deployDomains(t, e, x, p, appA, "r1", []string{domain}),
		agentv1.CommandOutcome_SUCCEEDED, "")
	requireDeployOutcome(t, deployDomains(t, e, x, p, appB, "r1", []string{domain}),
		agentv1.CommandOutcome_SUCCEEDED, "")

	if res := x.SwitchRelease(context.Background(), switchMsg(appA, "r1")); res.GetOutcome() != agentv1.CommandOutcome_SUCCEEDED {
		t.Fatalf("SwitchRelease(%s/r1) outcome = %v with detail %q, want SUCCEEDED",
			appA, res.GetOutcome(), res.GetDetail())
	}

	_, before, known := storedApp(t, dir, appB)
	if !known {
		t.Fatalf("stored app %s known = false, want it after its deploy", appB)
	}
	requireEqual(t, "stored candidate before the refusal", releaseID(before.Candidate), "r1")
	requireEqual(t, "stored phase before the refusal", before.Phase, release.PhaseReady)
	if before.Current != nil {
		t.Fatalf("stored current before the refusal = %q, want no current release", before.Current.ID)
	}

	loads := fake.loadCount()
	if loads == 0 {
		t.Fatalf("pushed documents before the second switch = 0, want the first switch's push")
	}

	res := x.SwitchRelease(context.Background(), switchMsg(appB, "r1"))

	wantDetail := "domain " + domain + " is already routed to app " + appA
	if got, want := res.GetOutcome(), agentv1.CommandOutcome_REJECTED; got != want {
		t.Fatalf("SwitchRelease(%s/r1) outcome = %v with detail %q, want %v", appB, got, res.GetDetail(), want)
	}
	requireEqual(t, "SwitchRelease refusal detail", res.GetDetail(), wantDetail)
	requireEqual(t, "POST /load count after the refusal", fake.loadCount(), loads)
	requireStoredState(t, dir, appB, before)
	requireAppState(t, "result app", res.GetApp(), before)

	row, found := storedRow(t, dir, appB, "r1")
	if !found {
		t.Fatalf("releases row for %s/r1 = missing, want the untouched candidate row", appB)
	}
	requireEqual(t, "B r1 status after the refusal", row.Status, "candidate")
	requireRunning(t, e, testContainerName(appB, "r1"))

	wantRoutes := []any{normalizedJSON(t, expectedRoute([]string{domain}, "r1",
		net.JoinHostPort(containerIPOn(t, e, p, appA, "r1"), "8080")))}
	requireJSONEqual(t, "routes in the last pushed document", pushedRoutes(t, fake.loadAt(loads-1)), wantRoutes)
}

// TestRollbackRejectsDomainRoutedByAnotherApp covers a previous release whose
// domain another app's current release now routes: the rollback is refused
// inside the push lock with the stored state, the rows and the container
// unchanged (docs/specs/release.md, "Rollback").
func TestRollbackRejectsDomainRoutedByAnotherApp(t *testing.T) {
	e := testEngine(t)
	p := testProject(t, e)
	x, dir, fake := newSwitchExecutor(t, e)
	x.healthLimit = 20 * time.Second
	appA := testAppID(p, "a")
	appB := testAppID(p, "b")
	const (
		domainX = "x.example"
		domainY = "y.example"
	)

	steps := []struct{ app, rel, domain string }{
		{appA, "r1", domainX},
		{appA, "r2", domainY},
		{appB, "r1", domainX},
	}
	for _, step := range steps {
		requireDeployOutcome(t, deployDomains(t, e, x, p, step.app, step.rel, []string{step.domain}),
			agentv1.CommandOutcome_SUCCEEDED, "")
		if res := x.SwitchRelease(context.Background(), switchMsg(step.app, step.rel)); res.GetOutcome() != agentv1.CommandOutcome_SUCCEEDED {
			t.Fatalf("SwitchRelease(%s/%s) outcome = %v with detail %q, want SUCCEEDED",
				step.app, step.rel, res.GetOutcome(), res.GetDetail())
		}
	}

	loads := fake.loadCount()
	res := x.Rollback(context.Background(), rollbackMsg(appA))

	wantDetail := "domain " + domainX + " is already routed to app " + appB
	if got, want := res.GetOutcome(), agentv1.CommandOutcome_REJECTED; got != want {
		t.Fatalf("Rollback(%s) outcome = %v with detail %q, want %v", appA, got, res.GetDetail(), want)
	}
	requireEqual(t, "Rollback refusal detail", res.GetDetail(), wantDetail)
	requireEqual(t, "POST /load count after the refusal", fake.loadCount(), loads)

	want := release.State{Current: &release.Release{ID: "r2"}, Previous: &release.Release{ID: "r1"}}
	requireAppState(t, "result app", res.GetApp(), want)
	requireStoredState(t, dir, appA, want)
	requireEqual(t, "A r1 status after the refusal", storedTimes(t, dir, appA, "r1").status, "previous")
	requireEqual(t, "A r2 status after the refusal", storedTimes(t, dir, appA, "r2").status, "current")
	singleContainerID(t, e, testContainerName(appA, "r1"))
}

// TestRollbackStoppedPreviousRejectedForDomain covers the same refusal when the
// previous container is stopped first: the rollback starts and gates it, then
// is refused, and the container is left in place as the app's previous release
// (docs/specs/release.md, "Rollback" step 3 and step 4).
func TestRollbackStoppedPreviousRejectedForDomain(t *testing.T) {
	e := testEngine(t)
	p := testProject(t, e)
	x, dir, fake := newSwitchExecutor(t, e)
	x.healthLimit = 20 * time.Second
	appA := testAppID(p, "a")
	appB := testAppID(p, "b")
	const (
		domainX = "x.example"
		domainY = "y.example"
	)

	steps := []struct{ app, rel, domain string }{
		{appA, "r1", domainX},
		{appA, "r2", domainY},
		{appB, "r1", domainX},
	}
	for _, step := range steps {
		requireDeployOutcome(t, deployDomains(t, e, x, p, step.app, step.rel, []string{step.domain}),
			agentv1.CommandOutcome_SUCCEEDED, "")
		if res := x.SwitchRelease(context.Background(), switchMsg(step.app, step.rel)); res.GetOutcome() != agentv1.CommandOutcome_SUCCEEDED {
			t.Fatalf("SwitchRelease(%s/%s) outcome = %v with detail %q, want SUCCEEDED",
				step.app, step.rel, res.GetOutcome(), res.GetDetail())
		}
	}

	id := singleContainerID(t, e, testContainerName(appA, "r1"))
	if _, err := e.cli.ContainerStop(context.Background(), id, client.ContainerStopOptions{}); err != nil {
		t.Fatalf("ContainerStop(%s) error = %v, want nil", id, err)
	}

	loads := fake.loadCount()
	rows := stateText(t, dir)

	res := x.Rollback(context.Background(), rollbackMsg(appA))

	wantDetail := "domain " + domainX + " is already routed to app " + appB
	if got, want := res.GetOutcome(), agentv1.CommandOutcome_REJECTED; got != want {
		t.Fatalf("Rollback(%s) outcome = %v with detail %q, want %v", appA, got, res.GetDetail(), want)
	}
	requireEqual(t, "Rollback refusal detail", res.GetDetail(), wantDetail)
	requireEqual(t, "POST /load count after the refusal", fake.loadCount(), loads)
	requireEqual(t, "agent state rows after the refusal", stateText(t, dir), rows)

	want := release.State{Current: &release.Release{ID: "r2"}, Previous: &release.Release{ID: "r1"}}
	requireAppState(t, "result app", res.GetApp(), want)
	requireStoredState(t, dir, appA, want)
	singleContainerID(t, e, testContainerName(appA, "r1"))
}

// TestSwitchReleaseKeepsOwnDomains covers the switch that reuses the app's own
// domain: an app's own current release never conflicts with its candidate, and
// the other app's route is preserved in app_id order (docs/specs/release.md,
// "SwitchRelease").
func TestSwitchReleaseKeepsOwnDomains(t *testing.T) {
	e := testEngine(t)
	p := testProject(t, e)
	x, dir, fake := newSwitchExecutor(t, e)
	x.healthLimit = 20 * time.Second
	appA := testAppID(p, "a")
	appB := testAppID(p, "b")
	const (
		domainX  = "x.example"
		domainY  = "z.example"
		domainZ2 = "z2.example"
	)

	requireDeployOutcome(t, deployDomains(t, e, x, p, appA, "r1", []string{domainX}),
		agentv1.CommandOutcome_SUCCEEDED, "")
	if res := x.SwitchRelease(context.Background(), switchMsg(appA, "r1")); res.GetOutcome() != agentv1.CommandOutcome_SUCCEEDED {
		t.Fatalf("SwitchRelease(%s/r1) outcome = %v with detail %q, want SUCCEEDED",
			appA, res.GetOutcome(), res.GetDetail())
	}
	requireDeployOutcome(t, deployDomains(t, e, x, p, appB, "r1", []string{domainY}),
		agentv1.CommandOutcome_SUCCEEDED, "")
	if res := x.SwitchRelease(context.Background(), switchMsg(appB, "r1")); res.GetOutcome() != agentv1.CommandOutcome_SUCCEEDED {
		t.Fatalf("SwitchRelease(%s/r1) outcome = %v with detail %q, want SUCCEEDED",
			appB, res.GetOutcome(), res.GetDetail())
	}
	requireDeployOutcome(t, deployDomains(t, e, x, p, appA, "r2", []string{domainX, domainZ2}),
		agentv1.CommandOutcome_SUCCEEDED, "")

	loads := fake.loadCount()
	if loads == 0 {
		t.Fatalf("pushed documents before the switch = 0, want the earlier switches' pushes")
	}

	res := x.SwitchRelease(context.Background(), switchMsg(appA, "r2"))

	if got, want := res.GetOutcome(), agentv1.CommandOutcome_SUCCEEDED; got != want {
		t.Fatalf("SwitchRelease(%s/r2) outcome = %v with detail %q, want %v", appA, got, res.GetDetail(), want)
	}
	requireEqual(t, "POST /load count after the switch", fake.loadCount(), loads+1)

	wantRoutes := []any{
		normalizedJSON(t, expectedRoute([]string{domainX, domainZ2}, "r2",
			net.JoinHostPort(containerIPOn(t, e, p, appA, "r2"), "8080"))),
		normalizedJSON(t, expectedRoute([]string{domainY}, "r1",
			net.JoinHostPort(containerIPOn(t, e, p, appB, "r1"), "8080"))),
	}
	requireJSONEqual(t, "routes in the pushed document", pushedRoutes(t, fake.loadAt(loads)), wantRoutes)

	want := release.State{Current: &release.Release{ID: "r2"}, Previous: &release.Release{ID: "r1"}}
	requireAppState(t, "result app", res.GetApp(), want)
	requireStoredState(t, dir, appA, want)
}

// TestProxyPushRoutedRejectsDomainBeforePosting covers the check inside the
// push lock: a routed release whose domain another app's current release routes
// is refused before any route is built, no POST /load is sent, the commit never
// runs and the last pushed document stays as it was (docs/specs/release.md,
// "Proxy config", "Pushing").
func TestProxyPushRoutedRejectsDomainBeforePosting(t *testing.T) {
	ctx := context.Background()
	e := testEngine(t)
	p := testProject(t, e)
	x, dir, fake := newSwitchExecutor(t, e)
	appA := testAppID(p, "a")
	appB := testAppID(p, "b")
	const domain = "x.example"

	requireDeployOutcome(t, deployDomains(t, e, x, p, appA, "r1", []string{domain}),
		agentv1.CommandOutcome_SUCCEEDED, "")
	makeCurrent(t, dir, appA)

	if err := x.proxy.Push(ctx); err != nil {
		t.Fatalf("Push() error = %v, want nil", err)
	}
	loads := fake.loadCount()
	last := x.proxy.last.Load()
	if last == nil {
		t.Fatalf("last pushed document = nil, want the document Push() posted")
	}
	snapshot := bytes.Clone(*last)

	routed := state.CurrentRelease{
		Binding: state.Binding{Project: p.name, Environment: testEnvironment, Service: testService},
		Record: state.Record{
			AppID: appB, ReleaseID: "r1", Port: 8080, Domains: []string{domain},
		},
	}
	committed := false
	err := x.proxy.PushRouted(ctx, routed, func(context.Context) error {
		committed = true
		return nil
	})

	if err == nil {
		t.Fatalf("PushRouted(%s/r1) error = nil, want the domain refusal", appB)
	}
	wantDetail := "domain " + domain + " is already routed to app " + appA
	requireEqual(t, "PushRouted() error", err.Error(), wantDetail)

	var refusal rejectedError
	if !errors.As(err, &refusal) {
		t.Errorf("PushRouted() error = %T %q, want errors.As to find a rejectedError", err, err)
	}
	requireEqual(t, "commit called", committed, false)
	requireEqual(t, "POST /load count after the refusal", fake.loadCount(), loads)
	got := x.proxy.last.Load()
	if got == nil {
		t.Errorf("last pushed document = nil, want the document from before the call unchanged")
	} else if !bytes.Equal(*got, snapshot) {
		t.Errorf("last pushed document = %s, want %s", *got, snapshot)
	}
}
