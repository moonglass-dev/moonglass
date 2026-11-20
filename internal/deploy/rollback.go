package deploy

import (
	"context"
	"fmt"

	"moonglass.dev/moonglass/internal/agent/state"
	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
	"moonglass.dev/moonglass/internal/release"
)

// previousGone is the answer when agent state names a previous release whose
// container is no longer there, so the release cannot serve again.
const previousGone = "the previous release's container is gone; deploy again"

// Rollback runs the health gate on the previous release when it is stopped,
// pushes the complete proxy config with it routed in place of the stored current
// release, and commits the rollback in one state transaction
// (docs/specs/release.md, "Rollback").
func (x *Executor) Rollback(ctx context.Context, requestID string, r *agentv1.Rollback) *agentv1.CommandResult {
	return x.once(ctx, requestID, state.CommandRollback, r.GetAppId(), func(cmd state.Command) (*agentv1.CommandResult, bool) {
		return x.rollback(ctx, cmd, r)
	})
}

// rollback is Rollback's body; written reports a committed result.
func (x *Executor) rollback(ctx context.Context, cmd state.Command, r *agentv1.Rollback) (*agentv1.CommandResult, bool) {
	appID := r.GetAppId()
	defer x.lockApp(appID)()

	b, st, known, err := x.db.App(ctx, appID)
	if err != nil {
		return result(agentv1.CommandOutcome_FAILED, fmt.Sprintf("read app %s: %v", appID, err), release.State{}), false
	}
	if !known {
		return result(agentv1.CommandOutcome_REJECTED,
			fmt.Sprintf("app %s has no release on this server; deploy it first", appID), st), false
	}
	next, err := st.Rollback()
	if err != nil {
		return result(agentv1.CommandOutcome_REJECTED, err.Error(), st), false
	}

	rec, found, err := x.db.Release(ctx, appID, st.Previous.ID)
	if err != nil {
		return result(agentv1.CommandOutcome_FAILED, err.Error(), st), false
	}
	if !found {
		return result(agentv1.CommandOutcome_FAILED,
			fmt.Sprintf("release %s of app %s has no record on this server; deploy again", st.Previous.ID, appID), st), false
	}
	if rec.ContainerID == "" {
		return result(agentv1.CommandOutcome_FAILED, previousGone, st), false
	}

	rs, err := x.engine.runState(ctx, rec.ContainerID)
	if err != nil {
		if isNotFound(err) {
			return result(agentv1.CommandOutcome_FAILED, previousGone, st), false
		}
		return result(agentv1.CommandOutcome_FAILED,
			fmt.Sprintf("release %s of app %s: container %s: %v", rec.ReleaseID, appID, rec.ContainerID, err), st), false
	}
	if !rs.Running {
		if startErr := x.engine.start(ctx, rec.ContainerID); startErr != nil {
			return result(agentv1.CommandOutcome_FAILED, startErr.Error(), st), false
		}
		if gateErr := x.healthGate(ctx, b.Project, rec); gateErr != nil {
			return result(agentv1.CommandOutcome_FAILED, gateErr.Error(), st), false
		}
	}

	res := result(agentv1.CommandOutcome_SUCCEEDED, "", next)
	cmd.Result = res
	routed := state.CurrentRelease{Binding: b, Record: rec}
	if err := x.proxy.PushRouted(ctx, routed, func(ctx context.Context) error {
		return x.db.RecordRollback(ctx, cmd, appID, next, x.now())
	}); err != nil {
		return result(outcome(err), err.Error(), st), false
	}
	return res, true
}
