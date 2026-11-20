package deploy

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"moonglass.dev/moonglass/internal/agent/state"
	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
	"moonglass.dev/moonglass/internal/release"
)

// deployGate imports an image whose ENTRYPOINT is entrypoint, deploys it as app
// at rel with port and healthPath, and returns the record its health gate reads.
func deployGate(t *testing.T, e *Engine, x *Executor, p *project, app, rel string, entrypoint []string, port uint32, healthPath string) state.Record {
	t.Helper()
	ref := "moonglass.local/" + p.name + "/web:" + rel
	p.registerImage(ref)
	importImage(t, e, ref, entrypoint)
	msg := deployMsg(p.name, app, rel, ref)
	msg.Port = port
	msg.HealthPath = healthPath
	res := x.Deploy(context.Background(), requestID(t), msg)
	if got := res.GetOutcome(); got != agentv1.CommandOutcome_SUCCEEDED {
		t.Fatalf("Deploy(%s/%s) outcome = %v with detail %q, want SUCCEEDED", app, rel, got, res.GetDetail())
	}
	rec, found, err := x.db.Release(context.Background(), app, rel)
	if err != nil {
		t.Fatalf("Release(%q, %q) error = %v, want nil", app, rel, err)
	}
	if !found {
		t.Fatalf("Release(%q, %q) found = false, want the deployed record", app, rel)
	}
	return rec
}

// switchMsg is one SwitchRelease the executor accepts: the app and release ids
// only, so the probe target can never come from the message.
func switchMsg(app, rel string) *agentv1.SwitchRelease {
	return &agentv1.SwitchRelease{AppId: app, ReleaseId: rel}
}

// newSwitchExecutor returns an executor whose one Proxy pushes to a fake admin
// socket, that executor's data directory, and the fake. The proxy is set on the
// executor newExecutor builds, so the existing helper keeps its signature.
func newSwitchExecutor(t *testing.T, e *Engine) (*Executor, string, *fakeAdmin) {
	t.Helper()
	x, dir := newExecutor(t, e)
	fake := newFakeAdmin(t)
	x.proxy = NewProxy(e, x.db, ProxyConfig{Socket: fake.path, Issuer: "internal"})
	return x, dir, fake
}

// releaseID is r's id, or empty when r is nil.
func releaseID(r *release.Release) string {
	if r == nil {
		return ""
	}
	return r.ID
}

// requireAppState fails when the CommandResult's app state differs from want.
func requireAppState(t *testing.T, field string, got *agentv1.AppState, want release.State) {
	t.Helper()
	requireEqual(t, field+".current_release_id", got.GetCurrentReleaseId(), releaseID(want.Current))
	requireEqual(t, field+".previous_release_id", got.GetPreviousReleaseId(), releaseID(want.Previous))
	requireEqual(t, field+".candidate_release_id", got.GetCandidateReleaseId(), releaseID(want.Candidate))
	requireEqual(t, field+".phase", got.GetPhase(), phase(want.Phase))
}

// requireStoredState fails when the stored state of app differs from want.
func requireStoredState(t *testing.T, dataDir, app string, want release.State) {
	t.Helper()
	_, st, known := storedApp(t, dataDir, app)
	if !known {
		t.Fatalf("stored app %s known = false, want the app in agent state", app)
	}
	requireEqual(t, "stored current_release_id", releaseID(st.Current), releaseID(want.Current))
	requireEqual(t, "stored previous_release_id", releaseID(st.Previous), releaseID(want.Previous))
	requireEqual(t, "stored candidate_release_id", releaseID(st.Candidate), releaseID(want.Candidate))
	requireEqual(t, "stored phase", st.Phase, want.Phase)
}

// currentReleaseID reads app's stored current release id without failing the
// test, so it is safe inside the fake admin's /load handler goroutine.
func currentReleaseID(dataDir, app string) (string, error) {
	ctx := context.Background()
	db, err := state.Open(ctx, dataDir)
	if err != nil {
		return "", err
	}
	defer func() { _ = db.Close() }()
	_, st, known, err := db.App(ctx, app)
	if err != nil {
		return "", err
	}
	if !known {
		return "", fmt.Errorf("app %s is not in agent state", app)
	}
	return releaseID(st.Current), nil
}

// releaseTimes is the part of a releases row a switch writes.
type releaseTimes struct {
	status     string
	switchedAt sql.NullString
	stopAfter  sql.NullString
}

// storedTimes reads status, switched_at and stop_after directly from the agent
// database the executor wrote.
func storedTimes(t *testing.T, dataDir, app, rel string) releaseTimes {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "agent.db"))
	if err != nil {
		t.Fatalf("sql.Open(agent.db) error = %v, want nil", err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	var row releaseTimes
	err = db.QueryRowContext(context.Background(),
		`SELECT status, switched_at, stop_after FROM releases WHERE app_id = ? AND release_id = ?`,
		app, rel).Scan(&row.status, &row.switchedAt, &row.stopAfter)
	if err != nil {
		t.Fatalf("read releases row of %s/%s: %v", app, rel, err)
	}
	return row
}

// parseStoredTime parses a stored timestamp, requiring RFC 3339 UTC with a Z.
func parseStoredTime(t *testing.T, field, text string) time.Time {
	t.Helper()
	if !strings.HasSuffix(text, "Z") {
		t.Errorf("%s = %q, want RFC 3339 UTC with a Z", field, text)
	}
	parsed, err := time.Parse(time.RFC3339, text)
	if err != nil {
		t.Fatalf("parse %s %q: %v", field, text, err)
	}
	return parsed
}

// stateText renders every apps and releases row of the agent database, so a
// refusal can be shown to write nothing.
func stateText(t *testing.T, dataDir string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "agent.db"))
	if err != nil {
		t.Fatalf("sql.Open(agent.db) error = %v, want nil", err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	queries := []string{
		`SELECT app_id, release_id, status, container_id, error, created_at, switched_at, stop_after
		FROM releases ORDER BY app_id, release_id`,
		`SELECT app_id, project, environment, service, current_release_id, previous_release_id,
		candidate_release_id, phase, created_at FROM apps ORDER BY app_id`,
	}
	out := []string{}
	for _, query := range queries {
		rows, err := db.QueryContext(context.Background(), query)
		if err != nil {
			t.Fatalf("query agent state: %v", err)
		}
		names, err := rows.Columns()
		if err != nil {
			t.Fatalf("columns of an agent state query: %v", err)
		}
		for rows.Next() {
			vals := make([]sql.NullString, len(names))
			ptrs := make([]any, len(vals))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatalf("scan an agent state row: %v", err)
			}
			cells := make([]string, len(vals))
			for i, v := range vals {
				if v.Valid {
					cells[i] = names[i] + "=" + v.String
				} else {
					cells[i] = names[i] + "=<null>"
				}
			}
			out = append(out, strings.Join(cells, " "))
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("read agent state: %v", err)
		}
		_ = rows.Close()
	}
	return out
}

// pushedRoutes returns the routes of the last pushed document's :443 server.
func pushedRoutes(t *testing.T, doc []byte) []any {
	t.Helper()
	root, ok := decodeAny(t, doc).(map[string]any)
	if !ok {
		t.Fatalf("pushed config = %q, want a JSON object", doc)
	}
	apps, ok := root["apps"].(map[string]any)
	if !ok {
		t.Fatalf("pushed config apps = %#v, want an object", root["apps"])
	}
	httpApp, ok := apps["http"].(map[string]any)
	if !ok {
		t.Fatalf("pushed config apps.http = %#v, want an object", apps["http"])
	}
	servers, ok := httpApp["servers"].(map[string]any)
	if !ok {
		t.Fatalf("pushed config apps.http.servers = %#v, want an object", httpApp["servers"])
	}
	server, ok := servers["moonglass"].(map[string]any)
	if !ok {
		t.Fatalf("pushed config apps.http.servers.moonglass = %#v, want an object", servers["moonglass"])
	}
	routes, ok := server["routes"].([]any)
	if !ok {
		t.Fatalf("pushed config routes = %#v, want an array", server["routes"])
	}
	return routes
}

// TestHealthGateHTTPPath covers the HTTP mode of the health gate: a 2xx or 3xx
// passes, redirects are never followed, any other status fails at the limit,
// and the probe uses the stored path on the container's own address.
func TestHealthGateHTTPPath(t *testing.T) {
	t.Parallel()
	e := testEngine(t)
	cases := []struct {
		name       string
		entrypoint []string
		healthPath string
		wantPass   bool
	}{
		{
			name:       "200 passes",
			entrypoint: []string{"/app", "health", "/healthz", "200"},
			healthPath: "/healthz",
			wantPass:   true,
		},
		{
			name:       "302 passes without following the redirect",
			entrypoint: []string{"/app", "health", "/healthz", "302"},
			healthPath: "/healthz",
			wantPass:   true,
		},
		{
			name:       "500 fails at the limit",
			entrypoint: []string{"/app", "health", "/healthz", "500"},
			healthPath: "/healthz",
			wantPass:   false,
		},
		{
			name:       "the stored path is probed, not another one",
			entrypoint: []string{"/app", "health", "/other", "200"},
			healthPath: "/healthz",
			wantPass:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := testProject(t, e)
			x, _ := newExecutor(t, e)
			x.healthLimit = 6 * time.Second
			app := testAppID(p, "gate")
			rec := deployGate(t, e, x, p, app, "r1", tc.entrypoint, 8080, tc.healthPath)

			start := time.Now()
			err := x.healthGate(context.Background(), p.name, rec)
			elapsed := time.Since(start)

			if tc.wantPass {
				if err != nil {
					t.Fatalf("healthGate() error = %v with detail, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("healthGate() error = nil, want a failure within %s", x.healthLimit)
			}
			if elapsed < x.healthLimit {
				t.Errorf("healthGate() failed after %s, want at least the limit %s", elapsed, x.healthLimit)
			}
			if elapsed > x.healthLimit+3*time.Second {
				t.Errorf("healthGate() failed after %s, want under %s", elapsed, x.healthLimit+3*time.Second)
			}
			addr := net.JoinHostPort(containerIPOn(t, e, p, app, "r1"), "8080")
			if want := addr + tc.healthPath; !strings.Contains(err.Error(), want) {
				t.Errorf("healthGate() error = %q, want it to name %q", err, want)
			}
			if strings.Contains(err.Error(), "health-body-must-not-leak") {
				t.Errorf("healthGate() error = %q, want no probe response body", err)
			}
		})
	}
}

// TestHealthGateTCPConnect covers the TCP mode: a container listening on the
// release's port passes, and a port nothing listens on fails at the limit.
func TestHealthGateTCPConnect(t *testing.T) {
	t.Parallel()
	e := testEngine(t)
	p := testProject(t, e)
	x, _ := newExecutor(t, e)
	x.healthLimit = 10 * time.Second

	listening := testAppID(p, "listen")
	rec := deployGate(t, e, x, p, listening, "r1", []string{"/app"}, 8080, "")
	if err := x.healthGate(context.Background(), p.name, rec); err != nil {
		t.Fatalf("healthGate() with a listening port error = %v, want nil", err)
	}

	silent := testAppID(p, "silent")
	rec = deployGate(t, e, x, p, silent, "r1", []string{"/app"}, 9, "")
	start := time.Now()
	err := x.healthGate(context.Background(), p.name, rec)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("healthGate() with nothing listening on port 9 error = nil, want a failure")
	}
	if elapsed < x.healthLimit {
		t.Errorf("healthGate() failed after %s, want at least the limit %s", elapsed, x.healthLimit)
	}
	if elapsed > x.healthLimit+3*time.Second {
		t.Errorf("healthGate() failed after %s, want under %s", elapsed, x.healthLimit+3*time.Second)
	}
}

// TestHealthGateWithoutPort covers the mode with no port: the container is
// healthy only after 10 consecutive seconds of running.
func TestHealthGateWithoutPort(t *testing.T) {
	t.Parallel()
	e := testEngine(t)
	p := testProject(t, e)
	x, _ := newExecutor(t, e)
	x.healthLimit = 15 * time.Second
	app := testAppID(p, "portless")
	rec := deployGate(t, e, x, p, app, "r1", []string{"/app"}, 0, "")

	start := time.Now()
	err := x.healthGate(context.Background(), p.name, rec)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("healthGate() error = %v, want nil after the container has run 10s", err)
	}
	if elapsed < 10*time.Second {
		t.Errorf("healthGate() passed after %s, want at least 10s of running", elapsed)
	}
}

// TestHealthGateFailsWhenContainerExitsOrRestarts covers the gate's exit and
// restart checks in each mode that applies.
func TestHealthGateFailsWhenContainerExitsOrRestarts(t *testing.T) {
	t.Parallel()
	e := testEngine(t)
	cases := []struct {
		name       string
		port       uint32
		healthPath string
		restarts   bool
	}{
		{"http, exits", 8080, "/healthz", false},
		{"tcp, exits", 8080, "", false},
		{"no port, exits", 0, "", false},
		{"http, restarts", 8080, "/healthz", true},
		{"tcp, restarts", 8080, "", true},
		{"no port, restarts", 0, "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := testProject(t, e)
			x, _ := newExecutor(t, e)
			x.healthLimit = 30 * time.Second
			app := testAppID(p, "die")
			rec := deployGate(t, e, x, p, app, "r1",
				[]string{"/app", "die", "1", "1"}, tc.port, tc.healthPath)

			if !tc.restarts {
				id := singleContainerID(t, e, testContainerName(app, "r1"))
				ctx := context.Background()
				if _, err := e.cli.ContainerUpdate(ctx, id, client.ContainerUpdateOptions{
					RestartPolicy: &container.RestartPolicy{Name: container.RestartPolicyDisabled},
				}); err != nil {
					t.Fatalf("ContainerUpdate(%s, restart no) error = %v, want nil", id, err)
				}
			}

			start := time.Now()
			err := x.healthGate(context.Background(), p.name, rec)
			elapsed := time.Since(start)
			if err == nil {
				t.Fatalf("healthGate() error = nil, want a failure when the container exits or restarts")
			}
			if elapsed > 20*time.Second {
				t.Errorf("healthGate() failed after %s, want well before the %s limit", elapsed, x.healthLimit)
			}
			if tc.restarts {
				if !strings.Contains(err.Error(), "restart") && !strings.Contains(err.Error(), "exited") {
					t.Errorf("healthGate() error = %q, want it to name a restart or an exit", err)
				}
				return
			}
			if !strings.Contains(err.Error(), "exited") {
				t.Errorf("healthGate() error = %q, want it to name the exit", err)
			}
		})
	}
}

// TestSwitchReleaseRoutesCandidateAndCommits covers a successful switch: the
// candidate is routed in the pushed document before the commit, and the commit
// writes the statuses and times of three consecutive switches.
func TestSwitchReleaseRoutesCandidateAndCommits(t *testing.T) {
	e := testEngine(t)
	p := testProject(t, e)
	x, dir, fake := newSwitchExecutor(t, e)
	x.healthLimit = 20 * time.Second
	app := testAppID(p, "switch")
	const domain = "sw.example.com"

	var mu sync.Mutex
	var seen []string
	fake.onLoad = func() {
		id, err := currentReleaseID(dir, app)
		if err != nil {
			id = "<" + err.Error() + ">"
		}
		mu.Lock()
		seen = append(seen, id)
		mu.Unlock()
	}

	oldCurrent := ""
	for _, rel := range []string{"r1", "r2", "r3"} {
		requireDeployOutcome(t, deployDomains(t, e, x, p, app, rel, []string{domain}),
			agentv1.CommandOutcome_SUCCEEDED, "")
		_, st, known := storedApp(t, dir, app)
		if !known {
			t.Fatalf("stored app %s known = false, want it after a deploy", app)
		}
		next, err := st.Switch(rel)
		if err != nil {
			t.Fatalf("State.Switch(%q) error = %v, want nil", rel, err)
		}

		loads := fake.loadCount()
		seenBefore := len(seen)
		before := time.Now()
		res := x.SwitchRelease(context.Background(), requestID(t), switchMsg(app, rel))
		after := time.Now()

		if got, want := res.GetOutcome(), agentv1.CommandOutcome_SUCCEEDED; got != want {
			t.Fatalf("SwitchRelease(%s) outcome = %v with detail %q, want %v", rel, got, res.GetDetail(), want)
		}
		mu.Lock()
		observed := append([]string(nil), seen...)
		mu.Unlock()
		if len(observed) != seenBefore+1 {
			t.Fatalf("pushes during SwitchRelease(%s) = %d, want 1", rel, len(observed)-seenBefore)
		}
		if got := observed[len(observed)-1]; got != oldCurrent {
			t.Errorf("current release in agent state during the push = %q, want %q (the push must precede the commit)", got, oldCurrent)
		}

		if got, want := fake.loadCount(), loads+1; got != want {
			t.Fatalf("POST /load count after SwitchRelease(%s) = %d, want %d", rel, got, want)
		}
		routes := pushedRoutes(t, fake.loadAt(loads))
		if len(routes) != 1 {
			t.Fatalf("routes in the pushed document = %d, want 1", len(routes))
		}
		dial := net.JoinHostPort(containerIPOn(t, e, p, app, rel), "8080")
		requireJSONEqual(t, "pushed route", routes[0],
			normalizedJSON(t, expectedRoute([]string{domain}, rel, dial)))

		requireAppState(t, "result app", res.GetApp(), next)
		requireStoredState(t, dir, app, next)

		row := storedTimes(t, dir, app, rel)
		if row.status != "current" {
			t.Errorf("%s status after the switch = %q, want %q", rel, row.status, "current")
		}
		if !row.switchedAt.Valid {
			t.Fatalf("%s switched_at = NULL, want the switch time", rel)
		}
		switchedAt := parseStoredTime(t, rel+" switched_at", row.switchedAt.String)
		if switchedAt.Before(before.Truncate(time.Second)) {
			t.Errorf("%s switched_at = %s, want no earlier than %s", rel, switchedAt, before.Truncate(time.Second))
		}
		if switchedAt.After(after) {
			t.Errorf("%s switched_at = %s, want no later than %s", rel, switchedAt, after)
		}
		if row.stopAfter.Valid {
			t.Errorf("%s stop_after = %q, want NULL for the current release", rel, row.stopAfter.String)
		}
		oldCurrent = rel
	}

	current := storedTimes(t, dir, app, "r3")
	switchedAt := parseStoredTime(t, "r3 switched_at", current.switchedAt.String)

	previous := storedTimes(t, dir, app, "r2")
	if previous.status != "previous" {
		t.Errorf("r2 status after the third switch = %q, want %q", previous.status, "previous")
	}
	if !previous.stopAfter.Valid {
		t.Fatalf("r2 stop_after = NULL, want the switch time plus 10m")
	}
	requireEqual(t, "r2 stop_after", parseStoredTime(t, "r2 stop_after", previous.stopAfter.String),
		switchedAt.Add(10*time.Minute))

	retired := storedTimes(t, dir, app, "r1")
	if retired.status != "retired" {
		t.Errorf("r1 status after the third switch = %q, want %q", retired.status, "retired")
	}
	if !retired.stopAfter.Valid {
		t.Fatalf("r1 stop_after = NULL, want the switch time plus 30s")
	}
	requireEqual(t, "r1 stop_after", parseStoredTime(t, "r1 stop_after", retired.stopAfter.String),
		switchedAt.Add(30*time.Second))

	requireRunning(t, e, testContainerName(app, "r1"))
	requireRunning(t, e, testContainerName(app, "r2"))
}

// TestSwitchReleaseHealthFailureAborts covers a failed gate: the candidate is
// removed and recorded aborted, the old release keeps serving, nothing is
// pushed and a new deploy is accepted.
func TestSwitchReleaseHealthFailureAborts(t *testing.T) {
	e := testEngine(t)
	p := testProject(t, e)
	x, dir, fake := newSwitchExecutor(t, e)
	x.healthLimit = 10 * time.Second
	app := testAppID(p, "abort")

	requireDeployOutcome(t, deployDomains(t, e, x, p, app, "r1", nil),
		agentv1.CommandOutcome_SUCCEEDED, "")
	res := x.SwitchRelease(context.Background(), requestID(t), switchMsg(app, "r1"))
	if got, want := res.GetOutcome(), agentv1.CommandOutcome_SUCCEEDED; got != want {
		t.Fatalf("SwitchRelease(r1) outcome = %v with detail %q, want %v", got, res.GetDetail(), want)
	}
	loads := fake.loadCount()

	deployGate(t, e, x, p, app, "r2", []string{"/app", "health", "/healthz", "500"}, 8080, "/healthz")

	res = x.SwitchRelease(context.Background(), requestID(t), switchMsg(app, "r2"))

	if got, want := res.GetOutcome(), agentv1.CommandOutcome_FAILED; got != want {
		t.Fatalf("SwitchRelease(r2) outcome = %v with detail %q, want %v", got, res.GetDetail(), want)
	}
	detail := res.GetDetail()
	if detail == "" {
		t.Errorf("SwitchRelease(r2) detail = empty, want the health gate failure")
	}
	if strings.Contains(detail, "health-body-must-not-leak") {
		t.Errorf("SwitchRelease(r2) detail = %q, want no probe response body", detail)
	}
	requireAppState(t, "result app", res.GetApp(), release.State{
		Current: &release.Release{ID: "r1"},
		Phase:   release.PhaseNone,
	})
	requireStoredState(t, dir, app, release.State{
		Current: &release.Release{ID: "r1"},
		Phase:   release.PhaseNone,
	})

	requireNoContainerNamed(t, e, testContainerName(app, "r2"))
	row, found := storedRow(t, dir, app, "r2")
	if !found {
		t.Fatalf("releases row for %s/r2 = missing, want the aborted row", app)
	}
	requireEqual(t, "r2 status", row.Status, "aborted")
	requireEqual(t, "r2 error", row.Err, detail)
	requireEqual(t, "r2 container_id", row.ContainerID, "")
	requireEqual(t, "POST /load count after the abort", fake.loadCount(), loads)

	requireDeployOutcome(t, deployDomains(t, e, x, p, app, "r3", nil),
		agentv1.CommandOutcome_SUCCEEDED, "")
	requireRunning(t, e, testContainerName(app, "r3"))
}

// TestSwitchReleaseRefusalsAndPushFailure covers each refusal, which writes and
// touches nothing, and a failed /load, which leaves the candidate ready so the
// switch can be sent again.
func TestSwitchReleaseRefusalsAndPushFailure(t *testing.T) {
	e := testEngine(t)
	ctx := context.Background()

	refusals := []struct {
		name      string
		prepare   func(t *testing.T, e *Engine, x *Executor, p *project, app string)
		releaseID string
	}{
		{
			name:      "an unknown app",
			prepare:   func(*testing.T, *Engine, *Executor, *project, string) {},
			releaseID: "r1",
		},
		{
			name: "an app with no candidate",
			prepare: func(t *testing.T, e *Engine, x *Executor, p *project, app string) {
				requireDeployOutcome(t, deployDomains(t, e, x, p, app, "r1", nil),
					agentv1.CommandOutcome_SUCCEEDED, "")
				res := x.SwitchRelease(ctx, requestID(t), switchMsg(app, "r1"))
				if got, want := res.GetOutcome(), agentv1.CommandOutcome_SUCCEEDED; got != want {
					t.Fatalf("SwitchRelease(r1) outcome = %v with detail %q, want %v", got, res.GetDetail(), want)
				}
			},
			releaseID: "r1",
		},
		{
			name: "a different release id",
			prepare: func(t *testing.T, e *Engine, x *Executor, p *project, app string) {
				requireDeployOutcome(t, deployDomains(t, e, x, p, app, "r1", nil),
					agentv1.CommandOutcome_SUCCEEDED, "")
			},
			releaseID: "r9",
		},
		{
			name: "a pending candidate",
			prepare: func(t *testing.T, e *Engine, x *Executor, p *project, app string) {
				ref := "moonglass.local/" + p.name + "/web:r1"
				p.registerImage(ref)
				importImage(t, e, ref, []string{"/app"})
				msg := deployMsg(p.name, app, "r1", ref)
				msg.Port = 8080
				msg.HasPreDeploy = true
				res := x.Deploy(ctx, requestID(t), msg)
				if got, want := res.GetOutcome(), agentv1.CommandOutcome_SUCCEEDED; got != want {
					t.Fatalf("Deploy with a pre-deploy step outcome = %v with detail %q, want %v", got, res.GetDetail(), want)
				}
			},
			releaseID: "r1",
		},
	}

	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			p := testProject(t, e)
			x, dir, fake := newSwitchExecutor(t, e)
			x.healthLimit = 10 * time.Second
			app := testAppID(p, "refuse")

			tc.prepare(t, e, x, p, app)
			loads := fake.loadCount()
			rows := stateText(t, dir)
			containers := projectContainerIDs(t, e, p)

			res := x.SwitchRelease(ctx, requestID(t), switchMsg(app, tc.releaseID))

			if got, want := res.GetOutcome(), agentv1.CommandOutcome_REJECTED; got != want {
				t.Fatalf("SwitchRelease(%s) outcome = %v with detail %q, want %v", tc.releaseID, got, res.GetDetail(), want)
			}
			if res.GetDetail() == "" {
				t.Errorf("SwitchRelease(%s) detail = empty, want the refusal", tc.releaseID)
			}
			_, stored, _ := storedApp(t, dir, app)
			requireAppState(t, "result app", res.GetApp(), stored)
			requireEqual(t, "POST /load count after the refusal", fake.loadCount(), loads)
			requireEqual(t, "agent state rows after the refusal", stateText(t, dir), rows)
			requireEqual(t, "project containers after the refusal", projectContainerIDs(t, e, p), containers)
		})
	}

	t.Run("a failed push leaves the candidate ready", func(t *testing.T) {
		p := testProject(t, e)
		x, dir, fake := newSwitchExecutor(t, e)
		x.healthLimit = 10 * time.Second
		app := testAppID(p, "push")

		requireDeployOutcome(t, deployDomains(t, e, x, p, app, "r1", nil),
			agentv1.CommandOutcome_SUCCEEDED, "")
		_, st, known := storedApp(t, dir, app)
		if !known {
			t.Fatalf("stored app %s known = false, want it after a deploy", app)
		}
		next, err := st.Switch("r1")
		if err != nil {
			t.Fatalf("State.Switch(%q) error = %v, want nil", "r1", err)
		}

		fake.setLoadResponse(http.StatusInternalServerError, "nope")
		res := x.SwitchRelease(ctx, requestID(t), switchMsg(app, "r1"))

		if got, want := res.GetOutcome(), agentv1.CommandOutcome_FAILED; got != want {
			t.Fatalf("SwitchRelease(r1) with /load answering 500 outcome = %v with detail %q, want %v",
				got, res.GetDetail(), want)
		}
		if !strings.Contains(res.GetDetail(), "500") {
			t.Errorf("SwitchRelease(r1) detail = %q, want it to contain the status 500", res.GetDetail())
		}
		requireAppState(t, "result app", res.GetApp(), st)
		requireStoredState(t, dir, app, st)
		row, found := storedRow(t, dir, app, "r1")
		if !found {
			t.Fatalf("releases row for %s/r1 = missing, want the candidate row", app)
		}
		requireEqual(t, "r1 status after the failed push", row.Status, "candidate")
		requireRunning(t, e, testContainerName(app, "r1"))

		fake.setLoadResponse(http.StatusOK, "")
		res = x.SwitchRelease(ctx, requestID(t), switchMsg(app, "r1"))
		if got, want := res.GetOutcome(), agentv1.CommandOutcome_SUCCEEDED; got != want {
			t.Fatalf("the re-sent SwitchRelease(r1) outcome = %v with detail %q, want %v", got, res.GetDetail(), want)
		}
		requireStoredState(t, dir, app, next)
	})
}
