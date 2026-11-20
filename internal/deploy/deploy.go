package deploy

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"moonglass.dev/moonglass/internal/agent/state"
	"moonglass.dev/moonglass/internal/policy"
	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
	"moonglass.dev/moonglass/internal/release"
)

const (
	labelManaged     = "dev.moonglass.managed"
	labelProject     = "dev.moonglass.project"
	labelEnvironment = "dev.moonglass.environment"
	labelService     = "dev.moonglass.service"
	labelApp         = "dev.moonglass.app"
	labelRelease     = "dev.moonglass.release"
)

type Executor struct {
	engine *Engine
	policy *policy.Policy
	db     *state.DB
	// proxy is the agent's one pusher: Run and SwitchRelease share it.
	proxy          *Proxy
	preDeployLimit time.Duration
	healthLimit    time.Duration
}

// New returns the executor over the agent's one Proxy, so Run and
// SwitchRelease push through the same pusher (docs/specs/release.md, "Proxy
// config").
func New(engine *Engine, p *policy.Policy, db *state.DB, proxy *Proxy) *Executor {
	return &Executor{
		engine: engine, policy: p, db: db, proxy: proxy,
		preDeployLimit: preDeployTimeout, healthLimit: healthTimeout,
	}
}

type rejectedError struct{ error }

func (e rejectedError) Unwrap() error { return e.error }
func rejected(err error) error        { return rejectedError{err} }
func (x *Executor) Deploy(ctx context.Context, d *agentv1.Deploy) *agentv1.CommandResult {
	appID := d.GetAppId()
	stored, st, known, err := x.db.App(ctx, appID)
	if err != nil {
		return result(agentv1.CommandOutcome_FAILED, fmt.Sprintf("read app %s: %v", appID, err), release.State{})
	}
	binding := state.Binding{Project: d.GetProject(), Environment: d.GetEnvironment(), Service: d.GetService()}
	if known && stored != binding {
		detail := fmt.Sprintf("app %s is bound to project %q, environment %q, service %q; deploy it with that binding or use a new app id", appID, stored.Project, stored.Environment, stored.Service)
		return result(agentv1.CommandOutcome_REJECTED, detail, st)
	}
	next, err := st.Deploy(d.GetReleaseId(), d.GetImage(), d.GetHasPreDeploy())
	if err != nil {
		return result(agentv1.CommandOutcome_REJECTED, err.Error(), st)
	}
	rec := state.Record{
		AppID: appID, ReleaseID: d.GetReleaseId(), ImageRef: d.GetImage(), Port: d.GetPort(),
		HealthPath: d.GetHealthPath(), Domains: d.GetDomains(), HasPreDeploy: d.GetHasPreDeploy(),
	}
	if domErr := x.checkDomains(ctx, appID, d.GetDomains()); domErr != nil {
		return x.fail(ctx, rec, st, domErr)
	}
	img, err := x.prepare(ctx, d, &rec)
	if err != nil {
		return x.fail(ctx, rec, st, err)
	}
	if err := x.db.RecordCandidate(ctx, binding, rec, next, time.Now()); err != nil {
		if rmErr := x.engine.remove(ctx, rec.ContainerID); rmErr != nil {
			err = fmt.Errorf("%w; removing the container also failed: %w", err, rmErr)
		}
		rec.ContainerID = ""
		return x.fail(ctx, rec, st, err)
	}
	res := result(agentv1.CommandOutcome_SUCCEEDED, "", next)
	res.ImageId = img.ID
	res.RepoDigest = img.RepoDigest
	return res
}

// checkDomains refuses a domain that another app's current release routes. The
// first conflict in the order the Deploy sent them is named. A candidate of
// another app holds no domain, and the app's own releases never conflict.
func (x *Executor) checkDomains(ctx context.Context, appID string, domains []string) error {
	if len(domains) == 0 {
		return nil
	}
	current, err := x.db.CurrentReleases(ctx)
	if err != nil {
		return err
	}
	owner := make(map[string]string)
	for _, cr := range current {
		if cr.Record.AppID == appID {
			continue
		}
		for _, domain := range cr.Record.Domains {
			if _, taken := owner[domain]; !taken {
				owner[domain] = cr.Record.AppID
			}
		}
	}
	for _, domain := range domains {
		if other, taken := owner[domain]; taken {
			return rejected(fmt.Errorf("domain %s is already routed to app %s", domain, other))
		}
	}
	return nil
}

func (x *Executor) prepare(ctx context.Context, d *agentv1.Deploy, rec *state.Record) (resolved, error) {
	img, err := x.engine.resolve(ctx, d.GetImage(), d.GetProject())
	if err != nil {
		return resolved{}, err
	}
	rec.ImageID = img.ID
	rec.RepoDigest = img.RepoDigest
	checked, checkErr := x.policy.Check(appSpec(d, img))
	if checkErr != nil {
		return resolved{}, rejected(checkErr)
	}
	if netErr := x.engine.ensureNetwork(ctx, d.GetProject()); netErr != nil {
		return resolved{}, netErr
	}
	name := "mg-" + rec.AppID + "-" + rec.ReleaseID
	if nameErr := x.engine.clearName(ctx, name, rec.AppID, rec.ReleaseID); nameErr != nil {
		return resolved{}, nameErr
	}
	if rec.ContainerID, err = x.engine.create(ctx, checked, name); err != nil {
		return resolved{}, err
	}
	if rec.HasPreDeploy {
		return img, nil
	}
	if startErr := x.engine.start(ctx, rec.ContainerID); startErr != nil {
		if rmErr := x.engine.remove(ctx, rec.ContainerID); rmErr != nil {
			return resolved{}, fmt.Errorf("%w; removing the container also failed: %w", startErr, rmErr)
		}
		return resolved{}, startErr
	}
	return img, nil
}

func (x *Executor) fail(ctx context.Context, rec state.Record, st release.State, cause error) *agentv1.CommandResult {
	rec.ContainerID = ""
	detail := truncate(cause.Error())
	if err := x.db.RecordFailed(ctx, rec, detail, time.Now()); err != nil {
		detail += "; recording the failure also failed: " + err.Error()
	}
	o := agentv1.CommandOutcome_FAILED
	var refusal rejectedError
	if errors.As(cause, &refusal) {
		o = agentv1.CommandOutcome_REJECTED
	}
	return result(o, detail, st)
}

func appSpec(d *agentv1.Deploy, img resolved) policy.Spec {
	image := cmp.Or(img.RepoDigest, img.ID)
	return policy.Spec{
		Kind: "app", Project: d.GetProject(), Environment: d.GetEnvironment(), Service: d.GetService(), Image: image,
		AgentLabels: map[string]string{
			labelManaged: "true", labelProject: d.GetProject(), labelEnvironment: d.GetEnvironment(),
			labelService: d.GetService(), labelApp: d.GetAppId(), labelRelease: d.GetReleaseId(),
		},
		Networks: []string{"mg-" + d.GetProject()}, RestartPolicy: "unless-stopped",
		LogConfig: policy.LogConfig{Driver: "local", Options: map[string]string{"max-size": "10m", "max-file": "3"}},
	}
}

func result(o agentv1.CommandOutcome, detail string, s release.State) *agentv1.CommandResult {
	return &agentv1.CommandResult{Outcome: o, Detail: truncate(detail), App: appState(s)}
}

func truncate(detail string) string {
	if len(detail) > 4096 {
		detail = detail[:4096]
		for !utf8.ValidString(detail) {
			detail = detail[:len(detail)-1]
		}
	}
	return detail
}

func appState(s release.State) *agentv1.AppState {
	id := func(r *release.Release) string {
		if r == nil {
			return ""
		}
		return r.ID
	}
	return &agentv1.AppState{
		CurrentReleaseId: id(s.Current), PreviousReleaseId: id(s.Previous),
		CandidateReleaseId: id(s.Candidate), Phase: phase(s.Phase),
	}
}

func phase(p release.Phase) agentv1.ReleasePhase {
	if p == release.PhaseNone || p == release.PhasePending || p == release.PhaseReady {
		return agentv1.ReleasePhase(p + 1)
	}
	return agentv1.ReleasePhase_RELEASE_PHASE_UNSPECIFIED
}
