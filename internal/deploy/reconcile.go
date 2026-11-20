package deploy

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/moby/moby/client"

	"moonglass.dev/moonglass/internal/release"
)

// ScopeReconcile narrows Reconcile, and so Run's start-up reconcile, to managed
// containers that also carry every given key=value label; another run's
// containers on the same Engine are left alone. It is called before Run, and
// production never calls it: every managed container on the server is the
// agent's own (docs/specs/release.md, "Run").
func (x *Executor) ScopeReconcile(labels ...string) {
	x.scope = slices.Clone(labels)
}

// Reconcile removes the managed containers no release references and then
// carries out the stops due at the time it runs (docs/specs/release.md,
// "Failure").
func (x *Executor) Reconcile(ctx context.Context) error {
	return errors.Join(x.reconcile(ctx, x.scope...), x.stopDue(ctx, x.now()))
}

func (x *Executor) reconcile(ctx context.Context, scope ...string) error {
	listCtx, cancel := call(ctx)
	defer cancel()
	filters := make(client.Filters).Add("label", labelManaged+"=true")
	if len(scope) > 0 {
		filters = filters.Add("label", scope...)
	}
	res, err := x.engine.cli.ContainerList(listCtx, client.ContainerListOptions{All: true, Filters: filters})
	if err != nil {
		return fmt.Errorf("list managed containers: %w", err)
	}
	var errs []error
	for _, c := range res.Items {
		app := c.Labels[labelApp]
		st := release.State{}
		if app != "" {
			_, read, _, readErr := x.db.App(ctx, app)
			if readErr != nil {
				errs = append(errs, fmt.Errorf("container %s: read app %s: %w", c.ID, app, readErr))
				continue
			}
			st = read
		}
		if keepContainer(c.Labels, st) {
			continue
		}
		if err := x.engine.remove(ctx, c.ID); err != nil {
			errs = append(errs, fmt.Errorf("container %s: %w", c.ID, err))
		}
	}
	return errors.Join(errs...)
}

func keepContainer(labels map[string]string, s release.State) bool {
	rel := labels[labelRelease]
	if labels[labelApp] == "" || rel == "" {
		return false
	}
	return same(s.Current, rel) || same(s.Previous, rel) || same(s.Candidate, rel)
}
func same(r *release.Release, id string) bool { return r != nil && r.ID == id }
