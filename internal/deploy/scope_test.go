package deploy

import (
	"context"
	"testing"

	"moonglass.dev/moonglass/internal/policy"
)

// TestScopeReconcileRemovesOnlyMatchingContainers proves that ScopeReconcile
// narrows the reconcile to the containers carrying every given label, so a
// fresh agent database never removes another run's containers on the same
// Engine (docs/specs/release.md, "Run"; docs/specs/deploy-suite.md).
func TestScopeReconcileRemovesOnlyMatchingContainers(t *testing.T) {
	ctx := context.Background()
	e := testEngine(t)
	p1 := testProject(t, e)
	p2 := testProject(t, e)
	pol := emptyPolicy(t, policy.Env{})

	image := "moonglass.local/" + p1.name + "/web:r1"
	p1.registerImage(image)
	importImage(t, e, image, []string{"/app"})

	for _, p := range []*project{p1, p2} {
		if err := e.ensureNetwork(ctx, p.name); err != nil {
			t.Fatalf("ensureNetwork(%q) error = %v, want nil", p.name, err)
		}
	}

	app1 := testAppID(p1, "ghost")
	name1 := testContainerName(app1, "r1")
	createFixture(t, e, pol, fixtureSpec(p1, app1, "r1", image, true), name1)

	app2 := testAppID(p2, "ghost")
	name2 := testContainerName(app2, "r1")
	kept := createFixture(t, e, pol, fixtureSpec(p2, app2, "r1", image, true), name2)

	x, _ := newExecutor(t, e)
	x.ScopeReconcile(testLabelProject + "=" + p1.name)
	if err := x.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile() after ScopeReconcile(project %q) error = %v, want nil", p1.name, err)
	}

	requireNoContainerNamed(t, e, name1)
	requireEqual(t, "the second project's container after Reconcile",
		containerIDsNamed(t, e, name2), []string{kept})
}
