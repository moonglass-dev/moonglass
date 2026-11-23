package deploy

import (
	"context"
	"testing"
	"time"

	"moonglass.dev/moonglass/internal/agent/state"
	"moonglass.dev/moonglass/internal/policy"
	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
	"moonglass.dev/moonglass/internal/release"
)

func TestReconcileKeepsReferencedReleases(t *testing.T) {
	current := &release.Release{ID: "r2", Image: "image"}
	previous := &release.Release{ID: "r1", Image: "image"}
	candidate := &release.Release{ID: "r3", Image: "image"}

	cases := []struct {
		labels map[string]string
		name   string
		state  release.State
		keep   bool
	}{
		{
			name:   "current",
			labels: map[string]string{testLabelApp: "app", testLabelRelease: "r2"},
			state:  release.State{Current: current},
			keep:   true,
		},
		{
			name:   "previous",
			labels: map[string]string{testLabelApp: "app", testLabelRelease: "r1"},
			state:  release.State{Current: current, Previous: previous},
			keep:   true,
		},
		{
			name:   "candidate",
			labels: map[string]string{testLabelApp: "app", testLabelRelease: "r3"},
			state:  release.State{Candidate: candidate, Phase: release.PhaseReady},
			keep:   true,
		},
		{
			name:   "unreferenced release",
			labels: map[string]string{testLabelApp: "app", testLabelRelease: "r0"},
			state:  release.State{Current: current, Previous: previous},
		},
		{
			name:   "no state",
			labels: map[string]string{testLabelApp: "app", testLabelRelease: "r2"},
		},
		{
			name:   "missing app label",
			labels: map[string]string{testLabelRelease: "r2"},
			state:  release.State{Current: current},
		},
		{
			name:   "missing release label",
			labels: map[string]string{testLabelApp: "app"},
			state:  release.State{Current: current},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := keepContainer(tc.labels, tc.state); got != tc.keep {
				t.Errorf("keepContainer(%v, %+v) = %v, want %v", tc.labels, tc.state, got, tc.keep)
			}
		})
	}
}

func TestReconcileRemovesUnreferencedContainers(t *testing.T) {
	ctx := context.Background()
	e := testEngine(t)
	p := testProject(t, e)
	x, dir := newExecutor(t, e)
	pol := emptyPolicy(t, policy.Env{})

	app := testAppID(p, "one")
	ghost := testAppID(p, "ghost")
	deployImported(t, e, x, p, app, "r1")
	image := "moonglass.local/" + p.name + "/web:r1"
	kept := singleContainerID(t, e, testContainerName(app, "r1"))

	createFixture(t, e, pol, fixtureSpec(p, app, "r0", image, true), testContainerName(app, "r0"))
	createFixture(t, e, pol, fixtureSpec(p, ghost, "r1", image, true), testContainerName(ghost, "r1"))
	loose := createFixture(t, e, pol, fixtureSpec(p, app, "r1", image, false), "mg-"+p.name+"-loose")

	if err := x.reconcile(ctx, testLabelProject+"="+p.name); err != nil {
		t.Fatalf("reconcile(project %q) error = %v, want nil", p.name, err)
	}

	requireNoContainerNamed(t, e, testContainerName(app, "r0"))
	requireNoContainerNamed(t, e, testContainerName(ghost, "r1"))
	requireEqual(t, "deployed container", containerIDsNamed(t, e, testContainerName(app, "r1")), []string{kept})
	requireEqual(t, "unmanaged container", containerIDsNamed(t, e, "mg-"+p.name+"-loose"), []string{loose})

	_, st, known := storedApp(t, dir, app)
	if !known {
		t.Fatalf("App(%s) known = false after reconcile, want its state kept", app)
	}
	requireEqual(t, "candidate after reconcile", st.Candidate, &release.Release{ID: "r1", Image: image})
	requireEqual(t, "phase after reconcile", st.Phase, release.PhaseReady)
}

// TestReconcileWaitsForTheAppLock covers a deploy that has created its
// candidate container but not yet recorded it. The deploy holds the app's lock
// for its whole run, so the reconcile must wait for it and then keep the
// container it would otherwise have removed as unreferenced. Deploy and
// RunPreDeploy must take that lock too.
func TestReconcileWaitsForTheAppLock(t *testing.T) {
	ctx := context.Background()
	e := testEngine(t)
	p := testProject(t, e)
	x, dir := newExecutor(t, e)
	pol := emptyPolicy(t, policy.Env{})

	image := "moonglass.local/" + p.name + "/web:r1"
	p.registerImage(image)
	imageID := importImage(t, e, image, []string{"/app"})

	// The test stands in for a deploy of app r1 between its container create
	// and RecordCandidate.
	app := testAppID(p, "racing")
	unlock := x.lockApp(app)
	candidate := createFixture(t, e, pol, fixtureSpec(p, app, "r1", image, true), testContainerName(app, "r1"))

	done := make(chan error, 1)
	go func() { done <- x.reconcile(ctx, testLabelProject+"="+p.name) }()
	select {
	case err := <-done:
		unlock()
		t.Fatalf("reconcile returned (%v) while the app lock was held, want it to wait", err)
	case <-time.After(300 * time.Millisecond):
	}
	requireEqual(t, "candidate container while the lock is held", containerIDsNamed(t, e, testContainerName(app, "r1")), []string{candidate})

	next, err := release.State{}.Deploy("r1", image, false)
	if err != nil {
		unlock()
		t.Fatalf("State.Deploy(r1) error = %v, want nil", err)
	}
	rec := state.Record{AppID: app, ReleaseID: "r1", ImageRef: image, ImageID: imageID, Port: 8080, Domains: []string{}, ContainerID: candidate}
	b := state.Binding{Project: p.name, Environment: testEnvironment, Service: testService}
	if err := x.db.RecordCandidate(ctx, seedCommand(t, app), b, rec, next, time.Now()); err != nil {
		unlock()
		t.Fatalf("RecordCandidate(r1) error = %v, want nil", err)
	}
	unlock()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("reconcile error = %v, want nil", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("reconcile did not return within 30s after the app lock was released")
	}
	requireEqual(t, "candidate container after reconcile", containerIDsNamed(t, e, testContainerName(app, "r1")), []string{candidate})
	if _, st, _ := storedApp(t, dir, app); st.Candidate == nil || st.Candidate.ID != "r1" {
		t.Errorf("candidate after reconcile = %+v, want r1", st.Candidate)
	}

	for _, tc := range []struct {
		name string
		run  func() *agentv1.CommandResult
	}{
		{"Deploy", func() *agentv1.CommandResult { return x.Deploy(ctx, requestID(t), deployMsg(p.name, app, "r2", image)) }},
		{"RunPreDeploy", func() *agentv1.CommandResult {
			return x.RunPreDeploy(ctx, requestID(t), &agentv1.RunPreDeploy{AppId: app, ReleaseId: "r1"})
		}},
	} {
		unlock := x.lockApp(app)
		res := make(chan *agentv1.CommandResult, 1)
		go func() { res <- tc.run() }()
		select {
		case r := <-res:
			unlock()
			t.Fatalf("%s returned (%v: %s) while the app lock was held, want it to wait", tc.name, r.GetOutcome(), r.GetDetail())
		case <-time.After(300 * time.Millisecond):
		}
		unlock()
		select {
		case <-res:
		case <-time.After(60 * time.Second):
			t.Fatalf("%s did not return within 60s after the app lock was released", tc.name)
		}
	}
}
