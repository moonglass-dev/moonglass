package deploy

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"moonglass.dev/moonglass/internal/agent/state"
	"moonglass.dev/moonglass/internal/policy"
	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
	"moonglass.dev/moonglass/internal/release"
)

const (
	labelRole        = "dev.moonglass.role"
	preDeployTimeout = 15 * time.Minute
)

// preDeploySpec is the candidate's spec with the step's differences: the kind,
// the image ID the candidate runs, the argv to exec, no restart and the role
// label. It is built fresh on every call, so no map or slice is shared.
func preDeploySpec(b state.Binding, rec state.Record, args []string) policy.Spec {
	s := appSpec(&agentv1.Deploy{
		AppId:       rec.AppID,
		ReleaseId:   rec.ReleaseID,
		Project:     b.Project,
		Environment: b.Environment,
		Service:     b.Service,
	}, resolved{ID: rec.ImageID})
	s.Kind = "pre_deploy"
	s.Cmd = slices.Clone(args)
	s.RestartPolicy = "no"
	s.AgentLabels[labelRole] = "pre_deploy"
	return s
}

func preDeployName(appID, releaseID string) string {
	return "mg-" + appID + "-" + releaseID + "-pre"
}

// RunPreDeploy runs the pending candidate's one-off step, then starts the
// candidate or aborts the deploy (docs/specs/release.md).
func (x *Executor) RunPreDeploy(ctx context.Context, requestID string, r *agentv1.RunPreDeploy) *agentv1.CommandResult {
	return x.once(ctx, requestID, state.CommandRunPreDeploy, r.GetAppId(), func(cmd state.Command) (*agentv1.CommandResult, bool) {
		return x.runPreDeploy(ctx, cmd, r)
	})
}

// runPreDeploy is RunPreDeploy's body; written reports a committed result.
func (x *Executor) runPreDeploy(ctx context.Context, cmd state.Command, r *agentv1.RunPreDeploy) (*agentv1.CommandResult, bool) {
	appID := r.GetAppId()
	releaseID := r.GetReleaseId()
	defer x.lockApp(appID)()

	b, st, known, err := x.db.App(ctx, appID)
	if err != nil {
		return result(agentv1.CommandOutcome_FAILED, fmt.Sprintf("read app %s: %v", appID, err), release.State{}), false
	}
	if !known {
		return result(agentv1.CommandOutcome_REJECTED,
			fmt.Sprintf("app %s has no release on this server; deploy it first", appID), st), false
	}

	ready, err := st.PreDeployDone(releaseID, true)
	if err != nil {
		return result(agentv1.CommandOutcome_REJECTED, err.Error(), st), false
	}

	rec, found, err := x.db.Release(ctx, appID, releaseID)
	if err != nil {
		return result(agentv1.CommandOutcome_FAILED, err.Error(), st), false
	}
	if !found {
		return result(agentv1.CommandOutcome_FAILED,
			fmt.Sprintf("release %s of app %s has no record on this server; deploy it again", releaseID, appID), st), false
	}

	checked, err := x.policy.Check(preDeploySpec(b, rec, r.GetArgs()))
	if err != nil {
		return result(agentv1.CommandOutcome_REJECTED, err.Error(), st), false
	}

	code, err := x.engine.runStep(ctx, checked, b.Project, rec, x.preDeployLimit)
	if err != nil {
		return x.abortPreDeploy(ctx, cmd, st, rec, r.GetReversible(), err.Error())
	}
	if code != 0 {
		return x.abortPreDeploy(ctx, cmd, st, rec, r.GetReversible(),
			fmt.Sprintf("pre-deploy step exited with code %d; its output is in the logs of container %s",
				code, preDeployName(appID, releaseID)))
	}

	if err := x.engine.start(ctx, rec.ContainerID); err != nil {
		return x.abortPreDeploy(ctx, cmd, st, rec, r.GetReversible(), err.Error())
	}
	res := result(agentv1.CommandOutcome_SUCCEEDED, "", ready)
	cmd.Result = res
	if err := x.db.RecordPreDeploy(ctx, cmd, appID, releaseID, ready, r.GetReversible(), "", x.now()); err != nil {
		return x.abortPreDeploy(ctx, cmd, st, rec, r.GetReversible(), err.Error())
	}
	return res, true
}

// abortPreDeploy clears the candidate, removes its container, records the
// aborted row and answers FAILED. Current and Previous never change.
func (x *Executor) abortPreDeploy(ctx context.Context, cmd state.Command, st release.State, rec state.Record, reversible bool, cause string) (*agentv1.CommandResult, bool) {
	aborted, err := st.PreDeployDone(rec.ReleaseID, false)
	if err != nil {
		return result(agentv1.CommandOutcome_FAILED, cause+"; aborting the release also failed: "+err.Error(), st), false
	}
	detail := truncate(cause)
	if rmErr := x.engine.remove(ctx, rec.ContainerID); rmErr != nil && !isNotFound(rmErr) {
		detail += "; removing the candidate container also failed: " + rmErr.Error()
	}
	res := result(agentv1.CommandOutcome_FAILED, detail, aborted)
	cmd.Result = res
	if recErr := x.db.RecordPreDeploy(ctx, cmd, rec.AppID, rec.ReleaseID, aborted, reversible, truncate(detail), x.now()); recErr != nil {
		return result(agentv1.CommandOutcome_FAILED, detail+"; recording the abort also failed: "+recErr.Error(), st), false
	}
	return res, true
}

// runStep creates and starts the step container at its -pre name and waits for
// it to exit. It never removes the container, so its logs stay readable.
func (e *Engine) runStep(ctx context.Context, c policy.Checked, project string, rec state.Record, limit time.Duration) (int64, error) {
	name := preDeployName(rec.AppID, rec.ReleaseID)
	if err := e.ensureNetwork(ctx, project); err != nil {
		return 0, err
	}
	if err := e.clearName(ctx, name, rec.AppID, rec.ReleaseID); err != nil {
		return 0, err
	}
	id, err := e.create(ctx, c, name)
	if err != nil {
		return 0, err
	}
	startCtx, cancel := call(ctx)
	_, startErr := e.cli.ContainerStart(startCtx, id, client.ContainerStartOptions{})
	cancel()
	if startErr != nil {
		return 0, fmt.Errorf("start pre-deploy step %s: %w", id, startErr)
	}
	return e.waitStep(ctx, id, limit)
}

// waitStep waits for the step container to stop. Over the limit, it kills the
// container and returns the exit code of the kill.
func (e *Engine) waitStep(ctx context.Context, id string, limit time.Duration) (int64, error) {
	waitCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	res := e.cli.ContainerWait(waitCtx, id, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	select {
	case out := <-res.Result:
		if out.Error != nil {
			return 0, fmt.Errorf("wait for pre-deploy step %s: %s", id, out.Error.Message)
		}
		return out.StatusCode, nil
	case waitErr := <-res.Error:
		if waitCtx.Err() != nil && ctx.Err() == nil {
			return e.killStep(ctx, id)
		}
		if _, killErr := e.killStep(ctx, id); killErr != nil {
			return 0, fmt.Errorf("wait for pre-deploy step %s: %w; killing it also failed: %w", id, waitErr, killErr)
		}
		return 0, fmt.Errorf("wait for pre-deploy step %s: %w", id, waitErr)
	}
}

// killStep kills the step container and returns the exit code it stopped with.
func (e *Engine) killStep(ctx context.Context, id string) (int64, error) {
	killCtx, cancelKill := call(context.WithoutCancel(ctx))
	_, killErr := e.cli.ContainerKill(killCtx, id, client.ContainerKillOptions{Signal: "SIGKILL"})
	cancelKill()
	if killErr != nil && !isNotFound(killErr) && !isNotRunning(killErr) {
		return 0, fmt.Errorf("kill pre-deploy step %s: %w", id, killErr)
	}

	waitCtx, cancelWait := call(context.WithoutCancel(ctx))
	defer cancelWait()
	res := e.cli.ContainerWait(waitCtx, id, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	select {
	case out := <-res.Result:
		if out.Error != nil {
			return 0, fmt.Errorf("wait for the killed pre-deploy step %s: %s", id, out.Error.Message)
		}
		return out.StatusCode, nil
	case waitErr := <-res.Error:
		return 0, fmt.Errorf("wait for the killed pre-deploy step %s: %w", id, waitErr)
	}
}

// isNotRunning reports whether a kill failed only because the step had already
// stopped, which is what the kill wanted.
func isNotRunning(err error) bool {
	return strings.Contains(err.Error(), "is not running")
}
