package deploy

import (
	"context"
	"errors"
	"fmt"

	"github.com/moby/moby/client"

	"moonglass.dev/moonglass/internal/release"
)

func (x *Executor) Reconcile(ctx context.Context) error { return x.reconcile(ctx) }
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
