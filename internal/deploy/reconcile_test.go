package deploy

import (
	"context"
	"testing"

	"moonglass.dev/moonglass/internal/policy"
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
