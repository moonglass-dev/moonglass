package deploy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"

	"moonglass.dev/moonglass/internal/agent/state"
	"moonglass.dev/moonglass/internal/policy"
	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
	"moonglass.dev/moonglass/internal/release"
)

// testRoleLabel is the extra agent label a pre-deploy step carries, repeated
// here so the tests assert the contract rather than the package's constant.
const testRoleLabel = "dev.moonglass.role"

// testPreDeployName is the name the executor gives the pre-deploy container of
// app at rel.
func testPreDeployName(app, rel string) string {
	return "mg-" + app + "-" + rel + "-pre"
}

// preDeployMsg is one RunPreDeploy the protocol boundary accepts. args is an
// exec argv, never a shell string.
func preDeployMsg(app, rel string, reversible bool, args ...string) *agentv1.RunPreDeploy {
	return &agentv1.RunPreDeploy{AppId: app, ReleaseId: rel, Args: args, Reversible: reversible}
}

// deployPending deploys an imported image with has_pre_deploy and requires a
// SUCCEEDED answer, leaving a created but unstarted candidate.
func deployPending(t *testing.T, e *Engine, x *Executor, p *project, app, rel string) {
	t.Helper()
	ref := "moonglass.local/" + p.name + "/web:" + rel
	p.registerImage(ref)
	importImage(t, e, ref, []string{"/app"})
	msg := deployMsg(p.name, app, rel, ref)
	msg.HasPreDeploy = true
	res := x.Deploy(context.Background(), requestID(t), msg)
	if got, want := res.GetOutcome(), agentv1.CommandOutcome_SUCCEEDED; got != want {
		t.Fatalf("Deploy(%s/%s) with has_pre_deploy outcome = %v with detail %q, want %v",
			app, rel, got, res.GetDetail(), want)
	}
	if got, want := res.GetApp().GetPhase(), agentv1.ReleasePhase_PENDING; got != want {
		t.Fatalf("Deploy(%s/%s) with has_pre_deploy phase = %v, want %v", app, rel, got, want)
	}
}

// storedReversible reads the pre_deploy_reversible column of rel directly from
// the agent database. It is invalid when the column is NULL or the row is
// missing.
func storedReversible(t *testing.T, dataDir, app, rel string) sql.NullBool {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "agent.db"))
	if err != nil {
		t.Fatalf("sql.Open(agent.db) error = %v, want nil", err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	var v sql.NullBool
	err = db.QueryRowContext(context.Background(),
		`SELECT pre_deploy_reversible FROM releases WHERE app_id = ? AND release_id = ?`,
		app, rel).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return sql.NullBool{}
	}
	if err != nil {
		t.Fatalf("read pre_deploy_reversible of %s/%s: %v", app, rel, err)
	}
	return v
}

// releaseRowText renders every column of the releases row of rel as one string.
func releaseRowText(t *testing.T, dataDir, app, rel string) string {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "agent.db"))
	if err != nil {
		t.Fatalf("sql.Open(agent.db) error = %v, want nil", err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	rows, err := db.QueryContext(context.Background(),
		`SELECT * FROM releases WHERE app_id = ? AND release_id = ?`, app, rel)
	if err != nil {
		t.Fatalf("read releases row of %s/%s: %v", app, rel, err)
	}
	defer func() { _ = rows.Close() }()
	names, err := rows.Columns()
	if err != nil {
		t.Fatalf("columns of releases: %v", err)
	}
	var text strings.Builder
	for rows.Next() {
		values := make([]any, len(names))
		ptrs := make([]any, len(names))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("scan releases row of %s/%s: %v", app, rel, err)
		}
		for _, v := range values {
			switch x := v.(type) {
			case nil:
				text.WriteString("<null>")
			case []byte:
				text.WriteString(string(x))
			default:
				fmt.Fprint(&text, x)
			}
			text.WriteString("|")
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read releases row of %s/%s: %v", app, rel, err)
	}
	return text.String()
}

// containerLogs reads the whole log stream (stdout and stderr) of id.
func containerLogs(t *testing.T, e *Engine, id string) string {
	t.Helper()
	res, err := e.cli.ContainerLogs(context.Background(), id, client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
	})
	if err != nil {
		t.Fatalf("ContainerLogs(%s) error = %v, want nil", id, err)
	}
	defer func() { _ = res.Close() }()
	data, err := io.ReadAll(res)
	if err != nil {
		t.Fatalf("read ContainerLogs(%s): %v", id, err)
	}
	return string(data)
}

// deploySnapshot is the stored state a REJECTED command must leave untouched.
type deploySnapshot struct {
	binding    state.Binding
	state      release.State
	known      bool
	row        releaseRow
	rowFound   bool
	reversible sql.NullBool
}

func takeDeploySnapshot(t *testing.T, dataDir, app, rel string) deploySnapshot {
	t.Helper()
	binding, st, known := storedApp(t, dataDir, app)
	row, found := storedRow(t, dataDir, app, rel)
	return deploySnapshot{
		binding:    binding,
		state:      st,
		known:      known,
		row:        row,
		rowFound:   found,
		reversible: storedReversible(t, dataDir, app, rel),
	}
}

func TestPreDeploySpecDerivesFromCandidate(t *testing.T) {
	b := state.Binding{Project: "proj", Environment: testEnvironment, Service: testService}
	rec := state.Record{
		AppID:        "app-1",
		ReleaseID:    "r1",
		ImageRef:     "ghcr.io/acme/web:r1",
		ImageID:      "sha256:" + strings.Repeat("b", 64),
		RepoDigest:   "sha256:" + strings.Repeat("a", 64),
		HasPreDeploy: true,
	}
	args := []string{"/bin/migrate", "--to", "latest"}

	got := preDeploySpec(b, rec, args)

	candidate := appSpec(&agentv1.Deploy{
		AppId:       rec.AppID,
		ReleaseId:   rec.ReleaseID,
		Project:     b.Project,
		Environment: b.Environment,
		Service:     b.Service,
	}, resolved{ID: rec.ImageID})
	want := candidate
	want.Kind = "pre_deploy"
	want.Cmd = []string{"/bin/migrate", "--to", "latest"}
	want.RestartPolicy = "no"
	want.AgentLabels = make(map[string]string, len(candidate.AgentLabels)+1)
	maps.Copy(want.AgentLabels, candidate.AgentLabels)
	want.AgentLabels[testRoleLabel] = "pre_deploy"

	requireEqual(t, "pre-deploy spec", got, want)
	if got.Image != rec.ImageID {
		t.Errorf("pre-deploy spec Image = %q, want the candidate's image ID %q", got.Image, rec.ImageID)
	}
	if got.Image == rec.RepoDigest {
		t.Errorf("pre-deploy spec Image = %q, the repository digest; want the image ID %q", got.Image, rec.ImageID)
	}

	// Every field outside the pass-through set must stay zero, which covers
	// Env, Labels and Mounts.
	permitted := map[string]bool{
		"Kind": true, "Project": true, "Environment": true, "Service": true,
		"Image": true, "Cmd": true, "AgentLabels": true, "Networks": true,
		"RestartPolicy": true, "LogConfig": true,
	}
	v := reflect.ValueOf(got)
	for i := range v.NumField() {
		field := v.Type().Field(i)
		if permitted[field.Name] {
			continue
		}
		if !v.Field(i).IsZero() {
			t.Errorf("pre-deploy spec field %s = %#v, want the zero value", field.Name, v.Field(i).Interface())
		}
	}

	mutable := []string{"/bin/one", "/bin/two"}
	spec := preDeploySpec(b, rec, mutable)
	mutable[0] = "/bin/changed"
	requireEqual(t, "Cmd after the caller changed args", spec.Cmd, []string{"/bin/one", "/bin/two"})
}

func TestRunPreDeploySuccessStartsCandidate(t *testing.T) {
	ctx := context.Background()
	e := testEngine(t)

	for _, reversible := range []bool{true, false} {
		t.Run(fmt.Sprintf("reversible=%v", reversible), func(t *testing.T) {
			p := testProject(t, e)
			x, dir := newExecutor(t, e)
			app := testAppID(p, "one")
			rel := "r1"
			ref := "moonglass.local/" + p.name + "/web:" + rel
			candidateName := testContainerName(app, rel)

			deployPending(t, e, x, p, app, rel)
			if c := requireContainer(t, e, candidateName); c.State == nil || c.State.Running {
				t.Fatalf("candidate container state before RunPreDeploy = %+v, want created and not running", c.State)
			}

			row, found := storedRow(t, dir, app, rel)
			if !found {
				t.Fatalf("releases row for %s/%s = missing, want the pending candidate", app, rel)
			}

			res := x.RunPreDeploy(ctx, requestID(t), preDeployMsg(app, rel, reversible, "exit", "0"))

			if got, want := res.GetOutcome(), agentv1.CommandOutcome_SUCCEEDED; got != want {
				t.Fatalf("RunPreDeploy outcome = %v with detail %q, want %v", got, res.GetDetail(), want)
			}
			requireEqual(t, "detail", res.GetDetail(), "")
			requireEqual(t, "app.candidate_release_id", res.GetApp().GetCandidateReleaseId(), rel)
			requireEqual(t, "app.current_release_id", res.GetApp().GetCurrentReleaseId(), "")
			requireEqual(t, "app.previous_release_id", res.GetApp().GetPreviousReleaseId(), "")
			requireEqual(t, "app.phase", res.GetApp().GetPhase(), agentv1.ReleasePhase_READY)

			requireRunning(t, e, candidateName)

			preName := testPreDeployName(app, rel)
			pre := requireContainer(t, e, preName)
			if pre.State == nil {
				t.Fatalf("container %s State = nil, want the step's exit status", preName)
			}
			if pre.Config == nil {
				t.Fatalf("container %s Config = nil, want the created config", preName)
			}
			if pre.HostConfig == nil {
				t.Fatalf("container %s HostConfig = nil, want the created host config", preName)
			}
			if pre.NetworkSettings == nil {
				t.Fatalf("container %s NetworkSettings = nil, want its networks", preName)
			}

			requireEqual(t, "step running", pre.State.Running, false)
			requireEqual(t, "step exit code", pre.State.ExitCode, 0)
			requireEqual(t, "step image", pre.Config.Image, row.ImageID)
			requireEqual(t, "step Cmd", pre.Config.Cmd, []string{"exit", "0"})
			requireEqual(t, "step restart policy", pre.HostConfig.RestartPolicy,
				container.RestartPolicy{Name: container.RestartPolicyDisabled})
			requireEqual(t, "step labels", pre.Config.Labels, map[string]string{
				testLabelManaged:     "true",
				testLabelProject:     p.name,
				testLabelEnvironment: testEnvironment,
				testLabelService:     testService,
				testLabelApp:         app,
				testLabelRelease:     rel,
				testRoleLabel:        "pre_deploy",
			})
			requireEqual(t, "step networks", mapKeys(pre.NetworkSettings.Networks), []string{"mg-" + p.name})

			image := inspectImage(t, e, ref)
			if image.Config == nil {
				t.Fatalf("ImageInspect(%q) Config = nil, want the image's own config", ref)
			}
			wantEnv := image.Config.Env
			if got := pre.Config.Env; len(got) != len(wantEnv) || (len(wantEnv) > 0 && !reflect.DeepEqual(got, wantEnv)) {
				t.Errorf("step Env = %q, want the image's own environment %q", got, wantEnv)
			}

			after, found := storedRow(t, dir, app, rel)
			if !found {
				t.Fatalf("releases row for %s/%s = missing after the step, want it kept", app, rel)
			}
			requireEqual(t, "row status", after.Status, "candidate")
			if got := storedReversible(t, dir, app, rel); !got.Valid || got.Bool != reversible {
				t.Errorf("pre_deploy_reversible = %+v, want a valid %v", got, reversible)
			}
		})
	}
}

func TestRunPreDeployFailureAbortsDeploy(t *testing.T) {
	ctx := context.Background()
	e := testEngine(t)
	p := testProject(t, e)
	x, dir := newExecutor(t, e)

	app := testAppID(p, "one")
	rel := "r1"
	candidateName := testContainerName(app, rel)
	preName := testPreDeployName(app, rel)

	deployPending(t, e, x, p, app, rel)
	bindingBefore, stateBefore, knownBefore := storedApp(t, dir, app)

	res := x.RunPreDeploy(ctx, requestID(t), preDeployMsg(app, rel, true, "exit", "3"))

	wantDetail := fmt.Sprintf("pre-deploy step exited with code 3; its output is in the logs of container %s", preName)
	if got, want := res.GetOutcome(), agentv1.CommandOutcome_FAILED; got != want {
		t.Fatalf("RunPreDeploy(exit 3) outcome = %v with detail %q, want %v", got, res.GetDetail(), want)
	}
	requireEqual(t, "detail", res.GetDetail(), wantDetail)

	requireEqual(t, "app.current_release_id", res.GetApp().GetCurrentReleaseId(), "")
	requireEqual(t, "app.previous_release_id", res.GetApp().GetPreviousReleaseId(), "")
	requireEqual(t, "app.candidate_release_id", res.GetApp().GetCandidateReleaseId(), "")
	requireEqual(t, "app.phase", res.GetApp().GetPhase(), agentv1.ReleasePhase_NONE)

	binding, st, known := storedApp(t, dir, app)
	requireEqual(t, "binding after an aborted pre-deploy", binding, bindingBefore)
	requireEqual(t, "known after an aborted pre-deploy", known, knownBefore)
	requireEqual(t, "state.Current after an aborted pre-deploy", st.Current, stateBefore.Current)
	requireEqual(t, "state.Previous after an aborted pre-deploy", st.Previous, stateBefore.Previous)
	if st.Candidate != nil {
		t.Errorf("state.Candidate after an aborted pre-deploy = %+v, want none", *st.Candidate)
	}
	requireEqual(t, "state.Phase after an aborted pre-deploy", st.Phase, release.PhaseNone)

	row, found := storedRow(t, dir, app, rel)
	if !found {
		t.Fatalf("releases row for %s/%s = missing after an aborted pre-deploy, want an aborted row", app, rel)
	}
	requireEqual(t, "row status", row.Status, "aborted")
	requireEqual(t, "row error", row.Err, wantDetail)
	requireEqual(t, "row container_id", row.ContainerID, "")

	requireNoContainerNamed(t, e, candidateName)

	pre := requireContainer(t, e, preName)
	if pre.State == nil {
		t.Fatalf("container %s State = nil, want its exit status", preName)
	}
	requireEqual(t, "step running", pre.State.Running, false)
	requireEqual(t, "step exit code", pre.State.ExitCode, 3)

	deployImported(t, e, x, p, app, "r2")
}

func TestRunPreDeployOutputStaysOutOfResult(t *testing.T) {
	ctx := context.Background()
	e := testEngine(t)
	p := testProject(t, e)
	x, dir := newExecutor(t, e)

	app := testAppID(p, "one")
	rel := "r1"
	marker := "pre-deploy-marker-" + randHex(t, 16)

	deployPending(t, e, x, p, app, rel)

	res := x.RunPreDeploy(ctx, requestID(t), preDeployMsg(app, rel, true, "print", marker, "1"))

	if got, want := res.GetOutcome(), agentv1.CommandOutcome_FAILED; got != want {
		t.Fatalf("RunPreDeploy(print, exit 1) outcome = %v with detail %q, want %v", got, res.GetDetail(), want)
	}

	preName := testPreDeployName(app, rel)
	logs := containerLogs(t, e, singleContainerID(t, e, preName))
	if !strings.Contains(logs, marker) {
		t.Errorf("logs of %s = %q, want them to contain the marker %q", preName, logs, marker)
	}

	if detail := res.GetDetail(); strings.Contains(detail, marker) {
		t.Errorf("detail = %q, want it not to contain the marker %q", detail, marker)
	}
	if text := prototext.Format(res); strings.Contains(text, marker) {
		t.Errorf("prototext.Format(result) = %q, want it not to contain the marker %q", text, marker)
	}
	encoded, err := proto.Marshal(res)
	if err != nil {
		t.Fatalf("proto.Marshal(result) error = %v, want nil", err)
	}
	if strings.Contains(string(encoded), marker) {
		t.Errorf("the encoded result contains the marker %q, want the step's output only in its logs", marker)
	}
	if text := releaseRowText(t, dir, app, rel); strings.Contains(text, marker) {
		t.Errorf("releases row = %q, want it not to contain the marker %q", text, marker)
	}
}

func TestRunPreDeployTimeoutKillsStep(t *testing.T) {
	ctx := context.Background()
	e := testEngine(t)
	p := testProject(t, e)
	x, dir := newExecutor(t, e)

	if got, want := x.preDeployLimit, 15*time.Minute; got != want {
		t.Fatalf("New executor pre-deploy limit = %v, want %v", got, want)
	}
	x.preDeployLimit = 3 * time.Second

	app := testAppID(p, "one")
	rel := "r1"
	preName := testPreDeployName(app, rel)

	deployPending(t, e, x, p, app, rel)

	start := time.Now()
	res := x.RunPreDeploy(ctx, requestID(t), preDeployMsg(app, rel, true, "sleep", "120"))
	elapsed := time.Since(start)

	if got, want := res.GetOutcome(), agentv1.CommandOutcome_FAILED; got != want {
		t.Fatalf("RunPreDeploy(sleep 120) outcome = %v with detail %q, want %v", got, res.GetDetail(), want)
	}

	pre := requireContainer(t, e, preName)
	if pre.State == nil {
		t.Fatalf("container %s State = nil, want its exit status", preName)
	}
	requireEqual(t, "step running", pre.State.Running, false)
	if pre.State.ExitCode == 0 {
		t.Errorf("step exit code = 0, want the killed container's non-zero code")
	}
	wantDetail := fmt.Sprintf("pre-deploy step exited with code %d; its output is in the logs of container %s",
		pre.State.ExitCode, preName)
	requireEqual(t, "detail", res.GetDetail(), wantDetail)

	if elapsed >= 3*time.Second+time.Minute {
		t.Errorf("RunPreDeploy took %v, want it returned after the %v limit, not after the step's sleep", elapsed, 3*time.Second)
	}

	row, found := storedRow(t, dir, app, rel)
	if !found {
		t.Fatalf("releases row for %s/%s = missing after a timed-out step, want an aborted row", app, rel)
	}
	requireEqual(t, "row status", row.Status, "aborted")
	requireEqual(t, "row error", row.Err, wantDetail)
	requireEqual(t, "row container_id", row.ContainerID, "")

	_, st, _ := storedApp(t, dir, app)
	if st.Candidate != nil {
		t.Errorf("state.Candidate after a timed-out step = %+v, want none", *st.Candidate)
	}
	requireNoContainerNamed(t, e, testContainerName(app, rel))
}

func TestRunPreDeployEngineErrorAborts(t *testing.T) {
	ctx := context.Background()
	e := testEngine(t)
	p := testProject(t, e)
	x, dir := newExecutor(t, e)

	app := testAppID(p, "one")
	rel := "r1"
	candidateName := testContainerName(app, rel)

	deployPending(t, e, x, p, app, rel)
	candidateID := singleContainerID(t, e, candidateName)
	if _, err := e.cli.ContainerRemove(ctx, candidateID, client.ContainerRemoveOptions{Force: true}); err != nil {
		t.Fatalf("ContainerRemove(%s) error = %v, want nil", candidateID, err)
	}

	res := x.RunPreDeploy(ctx, requestID(t), preDeployMsg(app, rel, true, "exit", "0"))

	if got, want := res.GetOutcome(), agentv1.CommandOutcome_FAILED; got != want {
		t.Fatalf("RunPreDeploy without a candidate container outcome = %v with detail %q, want %v",
			got, res.GetDetail(), want)
	}
	detail := res.GetDetail()
	if !strings.Contains(detail, "start container") {
		t.Errorf("detail = %q, want it to name the failed start of the candidate container", detail)
	}
	if !strings.Contains(detail, candidateID) {
		t.Errorf("detail = %q, want it to name the candidate container %s", detail, candidateID)
	}
	requireEqual(t, "app.candidate_release_id", res.GetApp().GetCandidateReleaseId(), "")
	requireEqual(t, "app.phase", res.GetApp().GetPhase(), agentv1.ReleasePhase_NONE)

	row, found := storedRow(t, dir, app, rel)
	if !found {
		t.Fatalf("releases row for %s/%s = missing after a failed start, want an aborted row", app, rel)
	}
	requireEqual(t, "row status", row.Status, "aborted")
	requireEqual(t, "row container_id", row.ContainerID, "")
	if row.Err == "" {
		t.Errorf("row error = empty, want the detail of the failed start")
	}

	_, st, _ := storedApp(t, dir, app)
	if st.Candidate != nil {
		t.Errorf("state.Candidate after a failed start = %+v, want none", *st.Candidate)
	}
}

func TestRunPreDeployRejectsIllegalTransition(t *testing.T) {
	ctx := context.Background()
	e := testEngine(t)

	type testCase struct {
		prepare          func(t *testing.T, x *Executor, p *project, app string) *agentv1.RunPreDeploy
		name             string
		candidateRunning bool
	}

	cases := []testCase{
		{
			name: "unknown app",
			prepare: func(_ *testing.T, _ *Executor, _ *project, app string) *agentv1.RunPreDeploy {
				return preDeployMsg(app, "r1", true, "exit", "0")
			},
		},
		{
			name: "release is not the candidate",
			prepare: func(t *testing.T, x *Executor, p *project, app string) *agentv1.RunPreDeploy {
				deployPending(t, e, x, p, app, "r1")
				return preDeployMsg(app, "other", true, "exit", "0")
			},
		},
		{
			name: "ready candidate deployed without a pre-deploy step",
			prepare: func(t *testing.T, x *Executor, p *project, app string) *agentv1.RunPreDeploy {
				deployImported(t, e, x, p, app, "r1")
				return preDeployMsg(app, "r1", true, "exit", "0")
			},
			candidateRunning: true,
		},
		{
			name: "second run after a successful one",
			prepare: func(t *testing.T, x *Executor, p *project, app string) *agentv1.RunPreDeploy {
				deployPending(t, e, x, p, app, "r1")
				res := x.RunPreDeploy(ctx, requestID(t), preDeployMsg(app, "r1", true, "exit", "0"))
				if got, want := res.GetOutcome(), agentv1.CommandOutcome_SUCCEEDED; got != want {
					t.Fatalf("first RunPreDeploy outcome = %v with detail %q, want %v", got, res.GetDetail(), want)
				}
				return preDeployMsg(app, "r1", true, "exit", "0")
			},
			candidateRunning: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := testProject(t, e)
			x, dir := newExecutor(t, e)
			app := testAppID(p, "one")
			msg := tc.prepare(t, x, p, app)

			rel := msg.GetReleaseId()
			preName := testPreDeployName(app, rel)
			beforePre := containerIDsNamed(t, e, preName)
			before := takeDeploySnapshot(t, dir, app, rel)

			var candidateID, candidateStartedAt string
			if tc.candidateRunning {
				candidateID = singleContainerID(t, e, testContainerName(app, rel))
				c := requireContainer(t, e, testContainerName(app, rel))
				if c.State == nil || !c.State.Running {
					t.Fatalf("candidate container %s state = %+v, want running before the rejected run",
						candidateID, c.State)
				}
				candidateStartedAt = c.State.StartedAt
			}

			res := x.RunPreDeploy(ctx, requestID(t), msg)

			if got, want := res.GetOutcome(), agentv1.CommandOutcome_REJECTED; got != want {
				t.Fatalf("RunPreDeploy(%s) outcome = %v with detail %q, want %v", tc.name, got, res.GetDetail(), want)
			}
			requireEqual(t, "containers named "+preName, containerIDsNamed(t, e, preName), beforePre)
			requireEqual(t, "stored state after a rejection", takeDeploySnapshot(t, dir, app, rel), before)

			if tc.candidateRunning {
				c := requireContainer(t, e, testContainerName(app, rel))
				requireEqual(t, "candidate container ID", c.ID, candidateID)
				if c.State == nil {
					t.Fatalf("candidate container %s State = nil after a rejection, want it running", candidateID)
				}
				requireEqual(t, "candidate StartedAt", c.State.StartedAt, candidateStartedAt)
				requireEqual(t, "candidate running", c.State.Running, true)
			}
		})
	}
}

func TestRunPreDeployReplacesLeftoverStepContainer(t *testing.T) {
	ctx := context.Background()
	e := testEngine(t)

	t.Run("managed leftover is replaced", func(t *testing.T) {
		p := testProject(t, e)
		x, dir := newExecutor(t, e)
		app := testAppID(p, "one")
		rel := "r1"
		preName := testPreDeployName(app, rel)

		deployPending(t, e, x, p, app, rel)
		row, found := storedRow(t, dir, app, rel)
		if !found {
			t.Fatalf("releases row for %s/%s = missing, want the pending candidate", app, rel)
		}
		leftover := createFixture(t, e, emptyPolicy(t, policy.Env{}),
			fixtureSpec(p, app, rel, row.ImageID, true), preName)

		res := x.RunPreDeploy(ctx, requestID(t), preDeployMsg(app, rel, true, "exit", "0"))

		if got, want := res.GetOutcome(), agentv1.CommandOutcome_SUCCEEDED; got != want {
			t.Fatalf("RunPreDeploy over a leftover step container outcome = %v with detail %q, want %v",
				got, res.GetDetail(), want)
		}
		replacement := singleContainerID(t, e, preName)
		if replacement == leftover {
			t.Errorf("step container ID = %s, want a new container after the leftover was replaced", replacement)
		}
		if _, err := e.cli.ContainerInspect(ctx, leftover, client.ContainerInspectOptions{}); err == nil {
			t.Errorf("leftover container %s still exists after the step, want it removed", leftover)
		}
	})

	t.Run("foreign container fails the step and is left in place", func(t *testing.T) {
		p := testProject(t, e)
		x, dir := newExecutor(t, e)
		app := testAppID(p, "one")
		rel := "r1"
		preName := testPreDeployName(app, rel)

		deployPending(t, e, x, p, app, rel)
		row, found := storedRow(t, dir, app, rel)
		if !found {
			t.Fatalf("releases row for %s/%s = missing, want the pending candidate", app, rel)
		}
		foreign := createFixture(t, e, emptyPolicy(t, policy.Env{}),
			fixtureSpec(p, app, rel, row.ImageID, false), preName)

		res := x.RunPreDeploy(ctx, requestID(t), preDeployMsg(app, rel, true, "exit", "0"))

		if got, want := res.GetOutcome(), agentv1.CommandOutcome_FAILED; got != want {
			t.Fatalf("RunPreDeploy over a foreign step container outcome = %v with detail %q, want %v",
				got, res.GetDetail(), want)
		}
		if !strings.Contains(res.GetDetail(), preName) {
			t.Errorf("detail = %q, want it to name the container %q", res.GetDetail(), preName)
		}
		requireEqual(t, "containers named "+preName, containerIDsNamed(t, e, preName), []string{foreign})
		requireEqual(t, "app.candidate_release_id", res.GetApp().GetCandidateReleaseId(), "")
		requireEqual(t, "app.phase", res.GetApp().GetPhase(), agentv1.ReleasePhase_NONE)

		after, found := storedRow(t, dir, app, rel)
		if !found {
			t.Fatalf("releases row for %s/%s = missing after the foreign container failed the step, want an aborted row",
				app, rel)
		}
		requireEqual(t, "row status", after.Status, "aborted")
		requireEqual(t, "row container_id", after.ContainerID, "")

		_, st, _ := storedApp(t, dir, app)
		if st.Candidate != nil {
			t.Errorf("state.Candidate after a foreign container failed the step = %+v, want none", *st.Candidate)
		}
	})
}
