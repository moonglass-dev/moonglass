package deploy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/client"

	"moonglass.dev/moonglass/internal/agent/state"
)

const (
	// stopWait is how many seconds a stopped container has between SIGTERM and
	// SIGKILL.
	stopWait = 30
	// stopInterval is how often Run carries out the stops that have come due.
	stopInterval = time.Second
	// retainInterval is how often Run applies retention. It is an Executor
	// field, so tests can shorten it.
	retainInterval = time.Hour
	// statusRetired is the release row a stop removes with its container.
	statusRetired = "retired"
)

// lockApp holds the app's lock until the returned function runs, so a stop and
// a switch or rollback of one app never run at the same time.
func (x *Executor) lockApp(appID string) func() {
	x.locksMu.Lock()
	mu, known := x.locks[appID]
	if !known {
		mu = &sync.Mutex{}
		x.locks[appID] = mu
	}
	x.locksMu.Unlock()

	mu.Lock()
	return mu.Unlock
}

// stopDue carries out every stop due at now and joins the failures, so one bad
// row never blocks the others.
func (x *Executor) stopDue(ctx context.Context, now time.Time) error {
	due, err := x.db.DueStops(ctx, now)
	if err != nil {
		return err
	}
	var errs []error
	for _, stop := range due {
		if err := x.stopOne(ctx, stop.AppID, stop.ReleaseID, now); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// stopOne stops one due release under its app's lock. A previous container is
// stopped and kept, so a rollback in its window only starts it. A retired
// container is stopped and removed, and its project network stays. The row is
// re-read under the lock, so a rollback or switch that changed it in between
// means no stop happens. Every stop or removal follows a managed-label check
// made right before the call (docs/threat-model.md §3 B5).
func (x *Executor) stopOne(ctx context.Context, appID, releaseID string, now time.Time) error {
	defer x.lockApp(appID)()

	due, err := x.db.DueStops(ctx, now)
	if err != nil {
		return fmt.Errorf("stop release %s of app %s: %w", releaseID, appID, err)
	}
	stop, listed := dueStop(due, appID, releaseID)
	if !listed {
		return nil
	}

	if stop.ContainerID != "" {
		ours, found, ownErr := x.engine.owned(ctx, stop.ContainerID, appID, releaseID)
		if ownErr != nil {
			return fmt.Errorf("stop release %s of app %s: %w", releaseID, appID, ownErr)
		}
		if found && !ours {
			return fmt.Errorf("container %s is not a Moonglass container of app %s release %s; it was not stopped or removed",
				stop.ContainerID, appID, releaseID)
		}
		if ours {
			if err := x.engine.stop(ctx, stop.ContainerID); err != nil {
				return fmt.Errorf("stop release %s of app %s: %w", releaseID, appID, err)
			}
			if stop.Status == statusRetired {
				if err := x.engine.remove(ctx, stop.ContainerID); err != nil {
					return fmt.Errorf("stop release %s of app %s: %w", releaseID, appID, err)
				}
			}
		}
	}

	if stop.Status == statusRetired {
		if err := x.engine.clearName(ctx, preDeployName(appID, releaseID), appID, releaseID); err != nil {
			return fmt.Errorf("stop release %s of app %s: %w", releaseID, appID, err)
		}
	}

	if err := x.db.RecordStopped(ctx, appID, releaseID, now); err != nil {
		return fmt.Errorf("stop release %s of app %s: %w", releaseID, appID, err)
	}
	return nil
}

// dueStop returns the entry of appID and releaseID in due.
func dueStop(due []state.DueStop, appID, releaseID string) (state.DueStop, bool) {
	for _, stop := range due {
		if stop.AppID == appID && stop.ReleaseID == releaseID {
			return stop, true
		}
	}
	return state.DueStop{}, false
}

// retain applies retention and then removes the images it freed, by ID and
// without force. An image the Engine still uses or no longer has is left for
// the disk guard, which is not an error.
func (x *Executor) retain(ctx context.Context, now time.Time) error {
	images, err := x.db.Retain(ctx, now)
	if err != nil {
		return err
	}
	var errs []error
	for _, image := range images {
		if err := x.engine.removeImage(ctx, image); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Run reconciles and applies retention at start, then carries out the stops due
// every second and applies retention every retainEvery, until ctx ends. A step
// that fails goes to onError and is retried at its next turn; it never stops
// Run (docs/specs/release.md, "Run").
func (x *Executor) Run(ctx context.Context, onError func(error)) {
	x.report(ctx, onError, x.Reconcile(ctx))
	x.report(ctx, onError, x.retain(ctx, x.now()))

	stops := time.NewTicker(stopInterval)
	defer stops.Stop()
	retention := time.NewTicker(x.retainEvery)
	defer retention.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stops.C:
			x.report(ctx, onError, x.stopDue(ctx, x.now()))
		case <-retention.C:
			x.report(ctx, onError, x.retain(ctx, x.now()))
		}
	}
}

// report passes a failed step to onError, unless ctx has ended: a step that
// stopped because of cancellation is not a failure.
func (x *Executor) report(ctx context.Context, onError func(error), err error) {
	if err == nil || onError == nil || ctx.Err() != nil {
		return
	}
	onError(err)
}

// owned reports whether the container the Engine has under id carries the
// managed labels of that app and release. found is false when the Engine no
// longer has the container, which a stop treats as already stopped and removed.
func (e *Engine) owned(ctx context.Context, id, appID, releaseID string) (ours, found bool, err error) {
	ctx, cancel := call(ctx)
	defer cancel()
	res, err := e.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		if isNotFound(err) {
			return false, false, nil
		}
		return false, false, fmt.Errorf("inspect container %s: %w", id, err)
	}
	labels := map[string]string{}
	if res.Container.Config != nil {
		labels = res.Container.Config.Labels
	}
	return ownedBy(labels, appID, releaseID), true, nil
}

// ownedBy is the one ownership check of every stop, removal and leftover
// container: the managed label, the app and the release.
func ownedBy(labels map[string]string, appID, releaseID string) bool {
	return labels[labelManaged] == "true" && labels[labelApp] == appID && labels[labelRelease] == releaseID
}

// stop sends SIGTERM and waits stopWait seconds before SIGKILL. A container the
// Engine no longer has is not an error.
func (e *Engine) stop(ctx context.Context, id string) error {
	ctx, cancel := call(ctx)
	defer cancel()
	wait := stopWait
	if _, err := e.cli.ContainerStop(ctx, id, client.ContainerStopOptions{Signal: "SIGTERM", Timeout: &wait}); err != nil {
		if isNotFound(err) {
			return nil
		}
		return fmt.Errorf("stop container %s: %w", id, err)
	}
	return nil
}

// removeImage removes the image with id, never with force. An image the Engine
// still uses is a conflict, and one it no longer has is not found: both are
// left for the disk guard, so neither is an error.
func (e *Engine) removeImage(ctx context.Context, id string) error {
	ctx, cancel := call(ctx)
	defer cancel()
	if _, err := e.cli.ImageRemove(ctx, id, client.ImageRemoveOptions{PruneChildren: true}); err != nil {
		if isNotFound(err) || isConflict(err) {
			return nil
		}
		return fmt.Errorf("remove image %s: %w", id, err)
	}
	return nil
}

// isConflict reports whether the Engine refused a call because another object
// still uses what it names, as image removal does for an image in use.
func isConflict(err error) bool {
	return strings.Contains(err.Error(), "conflict")
}
