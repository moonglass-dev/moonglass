package deploy

import (
	"context"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"moonglass.dev/moonglass/internal/agent/state"
	"moonglass.dev/moonglass/internal/policy"
	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
	"moonglass.dev/moonglass/internal/release"
)

func TestDeployImportedImageStartsCandidate(t *testing.T) {
	ctx := context.Background()
	e := testEngine(t)
	p := testProject(t, e)
	x, dir := newExecutor(t, e)

	app := testAppID(p, "one")
	ref := "moonglass.local/" + p.name + "/web:r1"
	p.registerImage(ref)
	imageID := importImage(t, e, ref, []string{"/app"})
	name := testContainerName(app, "r1")

	msg := deployMsg(p.name, app, "r1", ref)
	msg.Port = 8080

	res := x.Deploy(ctx, msg)

	if got, want := res.GetOutcome(), agentv1.CommandOutcome_SUCCEEDED; got != want {
		t.Fatalf("Deploy outcome = %v with detail %q, want %v", got, res.GetDetail(), want)
	}
	requireEqual(t, "detail", res.GetDetail(), "")
	requireEqual(t, "image_id", res.GetImageId(), imageID)
	requireEqual(t, "repo_digest", res.GetRepoDigest(), "")
	requireEqual(t, "app.current_release_id", res.GetApp().GetCurrentReleaseId(), "")
	requireEqual(t, "app.previous_release_id", res.GetApp().GetPreviousReleaseId(), "")
	requireEqual(t, "app.candidate_release_id", res.GetApp().GetCandidateReleaseId(), "r1")
	requireEqual(t, "app.phase", res.GetApp().GetPhase(), agentv1.ReleasePhase_READY)

	net := requireNetwork(t, e, "mg-"+p.name)
	requireEqual(t, "network name", net.Name, "mg-"+p.name)
	requireEqual(t, "network driver", net.Driver, "bridge")
	requireEqual(t, "network labels", net.Labels, map[string]string{
		testLabelManaged: "true",
		testLabelProject: p.name,
	})

	c := requireContainer(t, e, name)
	requireEqual(t, "container name", c.Name, "/"+name)
	requireEqual(t, "container image", c.Image, imageID)
	if c.State == nil || !c.State.Running {
		t.Fatalf("container %s state = %+v, want running", name, c.State)
	}
	if c.Config == nil {
		t.Fatalf("container %s Config = nil, want the created config", name)
	}
	if c.HostConfig == nil {
		t.Fatalf("container %s HostConfig = nil, want the created host config", name)
	}
	requireEqual(t, "container labels", c.Config.Labels, map[string]string{
		testLabelManaged:     "true",
		testLabelProject:     p.name,
		testLabelEnvironment: testEnvironment,
		testLabelService:     testService,
		testLabelApp:         app,
		testLabelRelease:     "r1",
	})
	requireEqual(t, "restart policy", c.HostConfig.RestartPolicy,
		container.RestartPolicy{Name: container.RestartPolicyUnlessStopped})
	requireEqual(t, "log driver", c.HostConfig.LogConfig.Type, "local")
	requireEqual(t, "log options", c.HostConfig.LogConfig.Config,
		map[string]string{"max-size": "10m", "max-file": "3"})
	if c.NetworkSettings == nil {
		t.Fatalf("container %s NetworkSettings = nil, want its networks", name)
	}
	requireEqual(t, "container networks", mapKeys(c.NetworkSettings.Networks), []string{"mg-" + p.name})
	requireEqual(t, "len(Mounts)", len(c.Mounts), 0)
	requireEqual(t, "len(PortBindings)", len(c.HostConfig.PortBindings), 0)
	requireEqual(t, "len(CapAdd)", len(c.HostConfig.CapAdd), 0)
	requireEqual(t, "Privileged", c.HostConfig.Privileged, false)

	binding, st, known := storedApp(t, dir, app)
	if !known {
		t.Fatalf("App(%s) known = false, want true after a successful deploy", app)
	}
	requireEqual(t, "stored binding", binding, state.Binding{
		Project:     p.name,
		Environment: testEnvironment,
		Service:     testService,
	})
	requireEqual(t, "stored release state", st, release.State{
		Candidate: &release.Release{ID: "r1", Image: ref},
		Phase:     release.PhaseReady,
	})

	row, found := storedRow(t, dir, app, "r1")
	if !found {
		t.Fatalf("releases row for %s/r1 = missing, want status candidate", app)
	}
	requireEqual(t, "row status", row.Status, "candidate")
	requireEqual(t, "row image_id", row.ImageID, imageID)
	requireEqual(t, "row repo_digest", row.RepoDigest, "")
	requireEqual(t, "row container_id", row.ContainerID, c.ID)
}

func TestDeployWithPreDeployLeavesContainerCreated(t *testing.T) {
	ctx := context.Background()
	e := testEngine(t)
	p := testProject(t, e)
	x, dir := newExecutor(t, e)

	app := testAppID(p, "one")
	ref := "moonglass.local/" + p.name + "/web:r1"
	p.registerImage(ref)
	importImage(t, e, ref, []string{"/app"})
	name := testContainerName(app, "r1")

	msg := deployMsg(p.name, app, "r1", ref)
	msg.HasPreDeploy = true

	res := x.Deploy(ctx, msg)

	if got, want := res.GetOutcome(), agentv1.CommandOutcome_SUCCEEDED; got != want {
		t.Fatalf("Deploy(has_pre_deploy) outcome = %v with detail %q, want %v", got, res.GetDetail(), want)
	}
	requireEqual(t, "detail", res.GetDetail(), "")
	requireEqual(t, "app.candidate_release_id", res.GetApp().GetCandidateReleaseId(), "r1")
	requireEqual(t, "app.phase", res.GetApp().GetPhase(), agentv1.ReleasePhase_PENDING)

	c := requireContainer(t, e, name)
	if c.State == nil {
		t.Fatalf("container %s State = nil, want status created", name)
	}
	requireEqual(t, "container status", c.State.Status, container.ContainerState("created"))
	requireEqual(t, "container running", c.State.Running, false)

	_, st, known := storedApp(t, dir, app)
	if !known {
		t.Fatalf("App(%s) known = false, want true after a successful deploy", app)
	}
	requireEqual(t, "stored phase", st.Phase, release.PhasePending)
	requireEqual(t, "stored candidate", st.Candidate, &release.Release{ID: "r1", Image: ref})

	row, found := storedRow(t, dir, app, "r1")
	if !found {
		t.Fatalf("releases row for %s/r1 = missing, want status candidate", app)
	}
	requireEqual(t, "row status", row.Status, "candidate")
}

func TestDeployRejectsBeforeActing(t *testing.T) {
	ctx := context.Background()
	e := testEngine(t)

	type testCase struct {
		run     func(t *testing.T, x *Executor, p, other *project, app string) *agentv1.Deploy
		detail  func(p, other *project) []string
		name    string
		wantRow bool
	}

	cases := []testCase{
		{
			name: "bound to another project",
			run: func(t *testing.T, x *Executor, p, other *project, app string) *agentv1.Deploy {
				deployImported(t, e, x, p, app, "r1")
				return deployMsg(other.name, app, "r2", "moonglass.local/"+other.name+"/web:r2")
			},
			detail: func(p, _ *project) []string {
				return []string{`project "` + p.name + `"`, `environment "` + testEnvironment + `"`, `service "` + testService + `"`}
			},
		},
		{
			name: "bound to another environment",
			run: func(t *testing.T, x *Executor, p, _ *project, app string) *agentv1.Deploy {
				deployImported(t, e, x, p, app, "r1")
				msg := deployMsg(p.name, app, "r2", "moonglass.local/"+p.name+"/web:r2")
				msg.Environment = "staging"
				return msg
			},
			detail: func(p, _ *project) []string {
				return []string{`project "` + p.name + `"`, `environment "` + testEnvironment + `"`, `service "` + testService + `"`}
			},
		},
		{
			name: "bound to another service",
			run: func(t *testing.T, x *Executor, p, _ *project, app string) *agentv1.Deploy {
				deployImported(t, e, x, p, app, "r1")
				msg := deployMsg(p.name, app, "r2", "moonglass.local/"+p.name+"/web:r2")
				msg.Service = "worker"
				return msg
			},
			detail: func(p, _ *project) []string {
				return []string{`project "` + p.name + `"`, `environment "` + testEnvironment + `"`, `service "` + testService + `"`}
			},
		},
		{
			name: "candidate already exists",
			run: func(t *testing.T, x *Executor, p, _ *project, app string) *agentv1.Deploy {
				deployImported(t, e, x, p, app, "r1")
				return deployMsg(p.name, app, "r2", "moonglass.local/"+p.name+"/web:r2")
			},
			detail: func(_, _ *project) []string {
				return []string{`candidate "r1"`, `"r2"`}
			},
		},
		{
			name: "another project's build",
			run: func(t *testing.T, _ *Executor, p, other *project, app string) *agentv1.Deploy {
				return deployMsg(p.name, app, "r1", "moonglass.local/"+other.name+"/web:r1")
			},
			detail: func(_, other *project) []string {
				return []string{"moonglass.local/" + other.name + "/web:r1", "belongs to project"}
			},
			wantRow: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := testProject(t, e)
			other := testProject(t, e)
			x, dir := newExecutor(t, e)
			app := testAppID(p, "one")

			msg := tc.run(t, x, p, other, app)
			beforeContainers := projectContainerIDs(t, e, p)
			beforeNetworks := projectNetworkNames(t, e, p)
			beforeBinding, beforeState, beforeKnown := storedApp(t, dir, app)

			res := x.Deploy(ctx, msg)

			if got, want := res.GetOutcome(), agentv1.CommandOutcome_REJECTED; got != want {
				t.Fatalf("Deploy outcome = %v with detail %q, want %v", got, res.GetDetail(), want)
			}
			for _, want := range tc.detail(p, other) {
				if !strings.Contains(res.GetDetail(), want) {
					t.Errorf("detail = %q, want it to contain %q", res.GetDetail(), want)
				}
			}
			requireEqual(t, "containers of project "+p.name, projectContainerIDs(t, e, p), beforeContainers)
			requireEqual(t, "networks of project "+p.name, projectNetworkNames(t, e, p), beforeNetworks)
			requireNoContainerNamed(t, e, testContainerName(msg.GetAppId(), msg.GetReleaseId()))
			if msg.GetProject() != p.name {
				requireEqual(t, "containers of project "+other.name, projectContainerIDs(t, e, other), []string{})
				requireEqual(t, "networks of project "+other.name, projectNetworkNames(t, e, other), []string{})
			}

			binding, st, known := storedApp(t, dir, app)
			requireEqual(t, "binding after a rejection", binding, beforeBinding)
			requireEqual(t, "release state after a rejection", st, beforeState)
			requireEqual(t, "known after a rejection", known, beforeKnown)

			row, found := storedRow(t, dir, app, msg.GetReleaseId())
			if found != tc.wantRow {
				t.Fatalf("releases row for %s/%s found = %v, want %v; row = %+v",
					app, msg.GetReleaseId(), found, tc.wantRow, row)
			}
			if tc.wantRow {
				requireEqual(t, "row status", row.Status, "failed")
				requireEqual(t, "row error", row.Err, res.GetDetail())
			}
		})
	}
}

func TestDeployFailuresLeaveStateUnchanged(t *testing.T) {
	ctx := context.Background()
	e := testEngine(t)

	type testCase struct {
		run         func(t *testing.T, x *Executor, p *project, app string) (*agentv1.Deploy, string)
		name        string
		leftInPlace bool
	}

	cases := []testCase{
		{
			name: "missing imported image",
			run: func(t *testing.T, _ *Executor, p *project, app string) (*agentv1.Deploy, string) {
				ref := "moonglass.local/" + p.name + "/web:r1"
				return deployMsg(p.name, app, "r1", ref), "image " + ref + " is not on this server; build it again"
			},
		},
		{
			name: "network not managed by Moonglass",
			run: func(t *testing.T, _ *Executor, p *project, app string) (*agentv1.Deploy, string) {
				networkName := "mg-" + p.name
				p.registerNetwork(networkName)
				if _, err := e.cli.NetworkCreate(ctx, networkName, client.NetworkCreateOptions{Driver: "bridge"}); err != nil {
					t.Fatalf("NetworkCreate(%q) error = %v, want nil", networkName, err)
				}
				ref := "moonglass.local/" + p.name + "/web:r1"
				p.registerImage(ref)
				importImage(t, e, ref, []string{"/app"})
				return deployMsg(p.name, app, "r1", ref),
					"network " + networkName + " exists and is not managed by Moonglass"
			},
		},
		{
			name: "start fails and the container is removed",
			run: func(t *testing.T, _ *Executor, p *project, app string) (*agentv1.Deploy, string) {
				ref := "moonglass.local/" + p.name + "/web:r1"
				p.registerImage(ref)
				importImage(t, e, ref, []string{"/missing"})
				return deployMsg(p.name, app, "r1", ref), ""
			},
		},
		{
			name: "same name container without the managed labels",
			run: func(t *testing.T, _ *Executor, p *project, app string) (*agentv1.Deploy, string) {
				ref := "moonglass.local/" + p.name + "/web:r1"
				p.registerImage(ref)
				importImage(t, e, ref, []string{"/app"})
				name := testContainerName(app, "r1")
				createFixture(t, e, emptyPolicy(t, policy.Env{}), fixtureSpec(p, app, "r1", ref, false), name)
				return deployMsg(p.name, app, "r1", ref), ""
			},
			leftInPlace: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := testProject(t, e)
			x, dir := newExecutor(t, e)
			app := testAppID(p, "one")

			msg, wantDetail := tc.run(t, x, p, app)

			res := x.Deploy(ctx, msg)

			if got, want := res.GetOutcome(), agentv1.CommandOutcome_FAILED; got != want {
				t.Fatalf("Deploy outcome = %v with detail %q, want %v", got, res.GetDetail(), want)
			}
			detail := res.GetDetail()
			if detail == "" {
				t.Fatalf("detail after FAILED = empty, want the reason")
			}
			if wantDetail != "" {
				requireEqual(t, "detail", detail, wantDetail)
			}

			binding, st, known := storedApp(t, dir, app)
			requireEqual(t, "binding after a failed deploy", binding, state.Binding{})
			requireEqual(t, "release state after a failed deploy", st, release.State{})
			requireEqual(t, "known after a failed deploy", known, false)

			row, found := storedRow(t, dir, app, msg.GetReleaseId())
			if !found {
				t.Fatalf("releases row for %s/%s = missing, want a failed row", app, msg.GetReleaseId())
			}
			requireEqual(t, "row status", row.Status, "failed")
			requireEqual(t, "row error", row.Err, detail)

			name := testContainerName(msg.GetAppId(), msg.GetReleaseId())
			if tc.leftInPlace {
				if ids := containerIDsNamed(t, e, name); len(ids) != 1 {
					t.Errorf("containers named %q = %v, want the pre-existing one left in place", name, ids)
				}
				if !strings.Contains(detail, name) {
					t.Errorf("detail = %q, want it to name the container %q", detail, name)
				}
				return
			}
			requireNoContainerNamed(t, e, name)
		})
	}
}

func TestDeployReplacesLeftoverContainer(t *testing.T) {
	ctx := context.Background()
	e := testEngine(t)
	p := testProject(t, e)
	x, dir := newExecutor(t, e)

	app := testAppID(p, "one")
	ref := "moonglass.local/" + p.name + "/web:r1"
	p.registerImage(ref)
	importImage(t, e, ref, []string{"/app"})

	name := testContainerName(app, "r1")
	leftover := createFixture(t, e, emptyPolicy(t, policy.Env{}), fixtureSpec(p, app, "r1", ref, true), name)

	res := x.Deploy(ctx, deployMsg(p.name, app, "r1", ref))

	if got, want := res.GetOutcome(), agentv1.CommandOutcome_SUCCEEDED; got != want {
		t.Fatalf("Deploy over a leftover container outcome = %v with detail %q, want %v",
			got, res.GetDetail(), want)
	}
	replacement := singleContainerID(t, e, name)
	if replacement == leftover {
		t.Errorf("container %s ID = %s, want a new container after the leftover was replaced", name, replacement)
	}
	if _, err := e.cli.ContainerInspect(ctx, leftover, client.ContainerInspectOptions{}); err == nil {
		t.Errorf("leftover container %s still exists after the deploy, want it removed", leftover)
	}
	requireRunning(t, e, name)

	row, found := storedRow(t, dir, app, "r1")
	if !found {
		t.Fatalf("releases row for %s/r1 = missing, want status candidate", app)
	}
	requireEqual(t, "row container_id", row.ContainerID, replacement)
}

// TestNewTakesTheAgentsProxy covers the constructor handing the executor the
// agent's one Proxy, so a switch pushes through the pusher Run uses.
func TestNewTakesTheAgentsProxy(t *testing.T) {
	t.Parallel()
	db := newProxyDB(t)
	pusher := NewProxy(nil, db, ProxyConfig{Socket: "unused", Issuer: "internal"})
	if got := New(nil, emptyPolicy(t, policy.Env{}), db, pusher); got.proxy != pusher {
		t.Errorf("New(...).proxy = %p, want the given Proxy %p", got.proxy, pusher)
	}
}
