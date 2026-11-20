package deploy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
)

// commandID is one command's request id: c followed by 32 lowercase hex, as the
// control plane mints them (docs/specs/connection.md).
func commandID(t *testing.T) string {
	t.Helper()
	return "c" + randHex(t, 16)
}

// storedCommand is the part of a commands row these tests assert.
type storedCommand struct {
	Type       string
	AppID      string
	Result     []byte
	ReceivedAt string
	FinishedAt string
}

// storedCommandRow reads the commands row of requestID directly from the agent
// database the executor wrote. found is false when there is no such row.
func storedCommandRow(t *testing.T, dataDir, requestID string) (storedCommand, bool) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "agent.db"))
	if err != nil {
		t.Fatalf("sql.Open(agent.db) error = %v, want nil", err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	var row storedCommand
	err = db.QueryRowContext(context.Background(),
		`SELECT type, app_id, result, received_at, finished_at FROM commands WHERE request_id = ?`, requestID).
		Scan(&row.Type, &row.AppID, &row.Result, &row.ReceivedAt, &row.FinishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return storedCommand{}, false
	}
	if err != nil {
		t.Fatalf("read commands row of request id %q: %v", requestID, err)
	}
	return row, true
}

// tableText renders every row a query returns, with the column names, so two
// snapshots can be compared byte for byte.
func tableText(t *testing.T, dataDir, query string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "agent.db"))
	if err != nil {
		t.Fatalf("sql.Open(agent.db) error = %v, want nil", err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	rows, err := db.QueryContext(context.Background(), query)
	if err != nil {
		t.Fatalf("query agent.db with %q: %v", query, err)
	}
	defer func() { _ = rows.Close() }()
	names, err := rows.Columns()
	if err != nil {
		t.Fatalf("columns of %q: %v", query, err)
	}
	out := []string{}
	for rows.Next() {
		values := make([]any, len(names))
		ptrs := make([]any, len(names))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("scan a row of %q: %v", query, err)
		}
		cells := make([]string, len(values))
		for i, v := range values {
			switch x := v.(type) {
			case nil:
				cells[i] = names[i] + "=<null>"
			case []byte:
				cells[i] = fmt.Sprintf("%s=%q", names[i], string(x))
			case string:
				cells[i] = fmt.Sprintf("%s=%q", names[i], x)
			default:
				cells[i] = fmt.Sprintf("%s=%v", names[i], x)
			}
		}
		out = append(out, strings.Join(cells, " "))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read the rows of %q: %v", query, err)
	}
	return out
}

// commandSnapshot renders every apps, releases and commands row of the agent
// database, commands.result included as bytes.
func commandSnapshot(t *testing.T, dataDir string) []string {
	t.Helper()
	out := []string{}
	for _, query := range []string{
		`SELECT app_id, project, environment, service, current_release_id, previous_release_id,
			candidate_release_id, phase, created_at FROM apps ORDER BY app_id`,
		`SELECT * FROM releases ORDER BY app_id, release_id`,
		`SELECT request_id, type, app_id, result, received_at, finished_at FROM commands ORDER BY request_id`,
	} {
		out = append(out, tableText(t, dataDir, query)...)
	}
	return out
}

// tableCount is SELECT COUNT(*) over table.
func tableCount(t *testing.T, dataDir, table string) int {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "agent.db"))
	if err != nil {
		t.Fatalf("sql.Open(agent.db) error = %v, want nil", err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	var count int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil {
		t.Fatalf("count %s rows: %v", table, err)
	}
	return count
}

// TestEveryCommandResultIsRecorded covers "Executor API (W2-10a)" in
// docs/specs/agent-state.md against a real Engine: every result Deploy,
// RunPreDeploy, SwitchRelease and Rollback returns is stored under the envelope's
// request id, with its type and app id, and a SUCCEEDED result's stored app state
// is the state change the same transaction committed.
func TestEveryCommandResultIsRecorded(t *testing.T) {
	ctx := context.Background()
	e := testEngine(t)

	importRef := func(t *testing.T, p *project, rel string, entrypoint []string) string {
		t.Helper()
		ref := "moonglass.local/" + p.name + "/web:" + rel
		p.registerImage(ref)
		importImage(t, e, ref, entrypoint)
		return ref
	}
	requireDeployed := func(t *testing.T, x *Executor, msg *agentv1.Deploy) {
		t.Helper()
		if res := x.Deploy(ctx, commandID(t), msg); res.GetOutcome() != agentv1.CommandOutcome_SUCCEEDED {
			t.Fatalf("Deploy(%s/%s) outcome = %v with detail %q, want SUCCEEDED",
				msg.GetAppId(), msg.GetReleaseId(), res.GetOutcome(), res.GetDetail())
		}
	}
	requireSwitched := func(t *testing.T, x *Executor, app, rel string) {
		t.Helper()
		if res := x.SwitchRelease(ctx, commandID(t), switchMsg(app, rel)); res.GetOutcome() != agentv1.CommandOutcome_SUCCEEDED {
			t.Fatalf("SwitchRelease(%s/%s) outcome = %v with detail %q, want SUCCEEDED",
				app, rel, res.GetOutcome(), res.GetDetail())
		}
	}

	cases := []struct {
		run         func(t *testing.T, x *Executor, p *project, app, rid string) *agentv1.CommandResult
		name        string
		wantType    string
		healthLimit time.Duration
	}{
		{
			name:     "Deploy without a pre-deploy step",
			wantType: "deploy",
			run: func(t *testing.T, x *Executor, p *project, app, rid string) *agentv1.CommandResult {
				ref := importRef(t, p, "r1", []string{"/app"})
				return x.Deploy(ctx, rid, deployMsg(p.name, app, "r1", ref))
			},
		},
		{
			name:     "Deploy with a pre-deploy step",
			wantType: "deploy",
			run: func(t *testing.T, x *Executor, p *project, app, rid string) *agentv1.CommandResult {
				ref := importRef(t, p, "r1", []string{"/app"})
				msg := deployMsg(p.name, app, "r1", ref)
				msg.HasPreDeploy = true
				return x.Deploy(ctx, rid, msg)
			},
		},
		{
			name:     "RunPreDeploy with a step that succeeds",
			wantType: "run_pre_deploy",
			run: func(t *testing.T, x *Executor, p *project, app, rid string) *agentv1.CommandResult {
				ref := importRef(t, p, "r1", []string{"/app"})
				msg := deployMsg(p.name, app, "r1", ref)
				msg.HasPreDeploy = true
				requireDeployed(t, x, msg)
				return x.RunPreDeploy(ctx, rid, preDeployMsg(app, "r1", true, "exit", "0"))
			},
		},
		{
			name:     "RunPreDeploy with a step that exits non-zero",
			wantType: "run_pre_deploy",
			run: func(t *testing.T, x *Executor, p *project, app, rid string) *agentv1.CommandResult {
				ref := importRef(t, p, "r1", []string{"/app"})
				msg := deployMsg(p.name, app, "r1", ref)
				msg.HasPreDeploy = true
				requireDeployed(t, x, msg)
				return x.RunPreDeploy(ctx, rid, preDeployMsg(app, "r1", true, "exit", "3"))
			},
		},
		{
			name:     "SwitchRelease success",
			wantType: "switch_release",
			run: func(t *testing.T, x *Executor, p *project, app, rid string) *agentv1.CommandResult {
				ref := importRef(t, p, "r1", []string{"/app"})
				msg := deployMsg(p.name, app, "r1", ref)
				msg.Port = 8080
				requireDeployed(t, x, msg)
				return x.SwitchRelease(ctx, rid, switchMsg(app, "r1"))
			},
		},
		{
			name:        "SwitchRelease with a failed health gate",
			wantType:    "switch_release",
			healthLimit: 6 * time.Second,
			run: func(t *testing.T, x *Executor, p *project, app, rid string) *agentv1.CommandResult {
				ref := importRef(t, p, "r1", []string{"/app", "health", "/healthz", "500"})
				msg := deployMsg(p.name, app, "r1", ref)
				msg.Port = 8080
				msg.HealthPath = "/healthz"
				requireDeployed(t, x, msg)
				return x.SwitchRelease(ctx, rid, switchMsg(app, "r1"))
			},
		},
		{
			name:     "Rollback success",
			wantType: "rollback",
			run: func(t *testing.T, x *Executor, p *project, app, rid string) *agentv1.CommandResult {
				for _, rel := range []string{"r1", "r2"} {
					ref := importRef(t, p, rel, []string{"/app"})
					msg := deployMsg(p.name, app, rel, ref)
					msg.Port = 8080
					requireDeployed(t, x, msg)
					requireSwitched(t, x, app, rel)
				}
				return x.Rollback(ctx, rid, rollbackMsg(app))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := testProject(t, e)
			x, dir := newExecutor(t, e)
			if tc.healthLimit != 0 {
				x.healthLimit = tc.healthLimit
			}
			app := testAppID(p, "one")
			rid := commandID(t)

			res := tc.run(t, x, p, app, rid)

			row, found := storedCommandRow(t, dir, rid)
			if !found {
				t.Fatalf("commands row for request id %q = missing, want the result the command returned", rid)
			}
			requireEqual(t, "commands.type", row.Type, tc.wantType)
			requireEqual(t, "commands.app_id", row.AppID, app)

			var stored agentv1.CommandResult
			if err := proto.Unmarshal(row.Result, &stored); err != nil {
				t.Fatalf("decode commands.result of request id %q: %v", rid, err)
			}
			if !proto.Equal(&stored, res) {
				t.Errorf("stored result = %v, want the returned result %v", &stored, res)
			}

			if res.GetOutcome() != agentv1.CommandOutcome_SUCCEEDED {
				return
			}
			_, st, known := storedApp(t, dir, app)
			if !known {
				t.Fatalf("stored app %s known = false after a SUCCEEDED command, want its state", app)
			}
			if !proto.Equal(appState(st), res.GetApp()) {
				t.Errorf("stored app state = %v, want the result's app state %v", appState(st), res.GetApp())
			}
		})
	}
}

// TestCommandWithNothingElseWrittenIsRecordedAlone covers the executor rule that
// a rejection written before any state change still stores its result, alone:
// the apps and releases rows are untouched and only the command's own row is new.
func TestCommandWithNothingElseWrittenIsRecordedAlone(t *testing.T) {
	ctx := context.Background()
	e := testEngine(t)

	importRef := func(t *testing.T, p *project, rel string, entrypoint []string) string {
		t.Helper()
		ref := "moonglass.local/" + p.name + "/web:" + rel
		p.registerImage(ref)
		importImage(t, e, ref, entrypoint)
		return ref
	}
	requireDeployed := func(t *testing.T, x *Executor, msg *agentv1.Deploy) {
		t.Helper()
		if res := x.Deploy(ctx, commandID(t), msg); res.GetOutcome() != agentv1.CommandOutcome_SUCCEEDED {
			t.Fatalf("Deploy(%s/%s) outcome = %v with detail %q, want SUCCEEDED",
				msg.GetAppId(), msg.GetReleaseId(), res.GetOutcome(), res.GetDetail())
		}
	}
	stateRows := func(t *testing.T, dir string) []string {
		t.Helper()
		out := tableText(t, dir, `SELECT app_id, project, environment, service, current_release_id,
			previous_release_id, candidate_release_id, phase, created_at FROM apps ORDER BY app_id`)
		return append(out, tableText(t, dir, `SELECT * FROM releases ORDER BY app_id, release_id`)...)
	}

	cases := []struct {
		prepare    func(t *testing.T, x *Executor, p *project, app, rid, dir string) func() *agentv1.CommandResult
		name       string
		wantType   string
		wantDetail string
	}{
		{
			name:       "Deploy with another app's binding",
			wantType:   "deploy",
			wantDetail: "is bound to project",
			prepare: func(t *testing.T, x *Executor, p *project, app, rid, _ string) func() *agentv1.CommandResult {
				ref := importRef(t, p, "r1", []string{"/app"})
				requireDeployed(t, x, deployMsg(p.name, app, "r1", ref))
				other := deployMsg("other-"+p.name, app, "r2", ref)
				return func() *agentv1.CommandResult { return x.Deploy(ctx, rid, other) }
			},
		},
		{
			name:       "SwitchRelease of a candidate still pending its pre-deploy",
			wantType:   "switch_release",
			wantDetail: "wait for pre-deploy",
			prepare: func(t *testing.T, x *Executor, p *project, app, rid, _ string) func() *agentv1.CommandResult {
				ref := importRef(t, p, "r1", []string{"/app"})
				msg := deployMsg(p.name, app, "r1", ref)
				msg.HasPreDeploy = true
				requireDeployed(t, x, msg)
				return func() *agentv1.CommandResult { return x.SwitchRelease(ctx, rid, switchMsg(app, "r1")) }
			},
		},
		{
			name:       "Rollback of an unknown app",
			wantType:   "rollback",
			wantDetail: "has no release on this server",
			prepare: func(_ *testing.T, x *Executor, _ *project, app, rid, _ string) func() *agentv1.CommandResult {
				return func() *agentv1.CommandResult { return x.Rollback(ctx, rid, rollbackMsg(app)) }
			},
		},
		{
			name:       "RunPreDeploy refused by the container policy",
			wantType:   "run_pre_deploy",
			wantDetail: "container policy rejected",
			prepare: func(t *testing.T, x *Executor, p *project, app, rid, dir string) func() *agentv1.CommandResult {
				ref := importRef(t, p, "r1", []string{"/app"})
				msg := deployMsg(p.name, app, "r1", ref)
				msg.HasPreDeploy = true
				requireDeployed(t, x, msg)
				// A project that is not a path element is refused by policy.Check
				// before the step container is created.
				execAgentSQL(t, dir, `UPDATE apps SET project = ? WHERE app_id = ?`, "bad/project", app)
				return func() *agentv1.CommandResult {
					return x.RunPreDeploy(ctx, rid, preDeployMsg(app, "r1", true, "exit", "0"))
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := testProject(t, e)
			x, dir := newExecutor(t, e)
			app := testAppID(p, "one")
			rid := commandID(t)
			call := tc.prepare(t, x, p, app, rid, dir)

			before := stateRows(t, dir)
			beforeCommands := tableCount(t, dir, "commands")
			containers := projectContainerIDs(t, e, p)

			res := call()

			if got, want := res.GetOutcome(), agentv1.CommandOutcome_REJECTED; got != want {
				t.Fatalf("%s outcome = %v with detail %q, want %v", tc.name, got, res.GetDetail(), want)
			}
			if !strings.Contains(res.GetDetail(), tc.wantDetail) {
				t.Errorf("%s detail = %q, want it to contain %q", tc.name, res.GetDetail(), tc.wantDetail)
			}

			row, found := storedCommandRow(t, dir, rid)
			if !found {
				t.Fatalf("commands row for request id %q = missing, want the rejected result", rid)
			}
			requireEqual(t, "commands.type", row.Type, tc.wantType)
			requireEqual(t, "commands.app_id", row.AppID, app)
			var stored agentv1.CommandResult
			if err := proto.Unmarshal(row.Result, &stored); err != nil {
				t.Fatalf("decode commands.result of request id %q: %v", rid, err)
			}
			if !proto.Equal(&stored, res) {
				t.Errorf("stored result = %v, want the returned result %v", &stored, res)
			}

			if after := stateRows(t, dir); !reflect.DeepEqual(before, after) {
				t.Errorf("the rejection changed the apps and releases rows:\n before = %v\n after  = %v", before, after)
			}
			if got, want := tableCount(t, dir, "commands"), beforeCommands+1; got != want {
				t.Errorf("commands rows after the rejection = %d, want %d, the one row of the command", got, want)
			}
			requireEqual(t, "project containers after the rejection", projectContainerIDs(t, e, p), containers)
		})
	}
}

// TestStoredRequestIDIsNotRunAgain covers "Each request id runs once" in
// docs/specs/connection.md: a request id with a stored result answers with that
// result whatever message arrives under it, creates no container and changes no
// row.
func TestStoredRequestIDIsNotRunAgain(t *testing.T) {
	ctx := context.Background()
	e := testEngine(t)
	p := testProject(t, e)
	x, dir := newExecutor(t, e)
	app := testAppID(p, "stored")

	ref1 := "moonglass.local/" + p.name + "/web:r1"
	p.registerImage(ref1)
	importImage(t, e, ref1, []string{"/app"})
	first := deployMsg(p.name, app, "r1", ref1)
	rid := commandID(t)
	want := x.Deploy(ctx, rid, first)
	if got := want.GetOutcome(); got != agentv1.CommandOutcome_SUCCEEDED {
		t.Fatalf("the first Deploy outcome = %v with detail %q, want SUCCEEDED", got, want.GetDetail())
	}

	ref2 := "moonglass.local/" + p.name + "/web:r2"
	p.registerImage(ref2)
	importImage(t, e, ref2, []string{"/app"})

	cases := []struct {
		run  func() *agentv1.CommandResult
		name string
	}{
		{
			name: "the same Deploy again",
			run:  func() *agentv1.CommandResult { return x.Deploy(ctx, rid, first) },
		},
		{
			name: "another release's Deploy",
			run: func() *agentv1.CommandResult {
				return x.Deploy(ctx, rid, deployMsg(p.name, app, "r2", ref2))
			},
		},
		{
			name: "a SwitchRelease",
			run:  func() *agentv1.CommandResult { return x.SwitchRelease(ctx, rid, switchMsg(app, "r1")) },
		},
	}

	containers := projectContainerIDs(t, e, p)
	before := commandSnapshot(t, dir)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := tc.run()

			if !proto.Equal(res, want) {
				t.Errorf("%s under a stored request id = %v, want the stored result %v", tc.name, res, want)
			}
			requireEqual(t, "project containers after the re-send", projectContainerIDs(t, e, p), containers)
			requireNoContainerNamed(t, e, testContainerName(app, "r2"))
			if after := commandSnapshot(t, dir); !reflect.DeepEqual(before, after) {
				t.Errorf("the re-send changed the rows:\n before = %v\n after  = %v", before, after)
			}
		})
	}
}

// TestEmptyRequestIDRunsNothing covers the executor rule that an empty request id
// is refused before anything runs: every method answers REJECTED with a detail
// that names the request id, creates no container and writes no row.
func TestEmptyRequestIDRunsNothing(t *testing.T) {
	ctx := context.Background()
	e := testEngine(t)

	cases := []struct {
		run  func(x *Executor, p *project, app string) *agentv1.CommandResult
		name string
	}{
		{
			name: "Deploy",
			run: func(x *Executor, p *project, app string) *agentv1.CommandResult {
				return x.Deploy(ctx, "", deployMsg(p.name, app, "r1", "moonglass.local/"+p.name+"/web:r1"))
			},
		},
		{
			name: "SwitchRelease",
			run: func(x *Executor, _ *project, app string) *agentv1.CommandResult {
				return x.SwitchRelease(ctx, "", switchMsg(app, "r1"))
			},
		},
		{
			name: "Rollback",
			run: func(x *Executor, _ *project, app string) *agentv1.CommandResult {
				return x.Rollback(ctx, "", rollbackMsg(app))
			},
		},
		{
			name: "RunPreDeploy",
			run: func(x *Executor, _ *project, app string) *agentv1.CommandResult {
				return x.RunPreDeploy(ctx, "", preDeployMsg(app, "r1", true, "exit", "0"))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := testProject(t, e)
			x, dir := newExecutor(t, e)
			app := testAppID(p, "empty")

			res := tc.run(x, p, app)

			if got, want := res.GetOutcome(), agentv1.CommandOutcome_REJECTED; got != want {
				t.Fatalf("%s with an empty request id outcome = %v with detail %q, want %v",
					tc.name, got, res.GetDetail(), want)
			}
			if !strings.Contains(res.GetDetail(), "request id") {
				t.Errorf("%s detail = %q, want it to name the request id", tc.name, res.GetDetail())
			}
			requireEqual(t, "containers of project "+p.name, projectContainerIDs(t, e, p), []string{})
			for _, table := range []string{"apps", "releases", "commands"} {
				if got := tableCount(t, dir, table); got != 0 {
					t.Errorf("%s rows after an empty request id = %d, want 0", table, got)
				}
			}
		})
	}
}
