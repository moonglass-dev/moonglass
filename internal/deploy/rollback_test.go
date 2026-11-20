package deploy

import (
	"context"
	"database/sql"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"

	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
	"moonglass.dev/moonglass/internal/release"
)

// switchTwice deploys and switches r1 then r2 for app, so r1 is the app's
// previous release and keeps running, and both releases route domain.
func switchTwice(t *testing.T, e *Engine, x *Executor, p *project, app, domain string) {
	t.Helper()
	for _, rel := range []string{"r1", "r2"} {
		requireDeployOutcome(t, deployDomains(t, e, x, p, app, rel, []string{domain}),
			agentv1.CommandOutcome_SUCCEEDED, "")
		res := x.SwitchRelease(context.Background(), switchMsg(app, rel))
		if got, want := res.GetOutcome(), agentv1.CommandOutcome_SUCCEEDED; got != want {
			t.Fatalf("SwitchRelease(%s) outcome = %v with detail %q, want %v", rel, got, res.GetDetail(), want)
		}
	}
}

// rollbackMsg is one Rollback the executor accepts: the app and nothing else, so
// the release it restores can only come from agent state.
func rollbackMsg(app string) *agentv1.Rollback {
	return &agentv1.Rollback{AppId: app}
}

// execAgentSQL runs one statement against the agent database and requires it to
// change exactly one row. It is how a test reaches a stored value no executor
// command sets.
func execAgentSQL(t *testing.T, dataDir, query string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "agent.db"))
	if err != nil {
		t.Fatalf("sql.Open(agent.db) error = %v, want nil", err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	res, err := db.ExecContext(context.Background(), query, args...)
	if err != nil {
		t.Fatalf("exec %q on agent.db: %v", query, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		t.Fatalf("count the rows changed by %q: %v", query, err)
	}
	if affected != 1 {
		t.Fatalf("rows changed by %q = %d, want 1", query, affected)
	}
}

// TestRollbackRunningPreviousRoutesAndCommits covers the rollback of an app whose
// previous container is still running: the restored release is routed in place
// of the stored current one, the commit writes the rollback rows, and the whole
// answer takes under 5 seconds (docs/specs/release.md, "Rollback").
func TestRollbackRunningPreviousRoutesAndCommits(t *testing.T) {
	e := testEngine(t)
	p := testProject(t, e)
	x, dir, fake := newSwitchExecutor(t, e)
	x.healthLimit = 20 * time.Second
	app := testAppID(p, "roll")
	const domain = "rollback.example.com"

	switchTwice(t, e, x, p, app, domain)
	requireRunning(t, e, testContainerName(app, "r1"))

	loads := fake.loadCount()
	containers := projectContainerIDs(t, e, p)
	before := time.Now()
	res := x.Rollback(context.Background(), rollbackMsg(app))
	after := time.Now()

	if got, want := res.GetOutcome(), agentv1.CommandOutcome_SUCCEEDED; got != want {
		t.Fatalf("Rollback() outcome = %v with detail %q, want %v", got, res.GetDetail(), want)
	}
	if elapsed := after.Sub(before); elapsed > 5*time.Second {
		t.Errorf("Rollback() took %s, want under 5s while the previous container is running", elapsed)
	}

	want := release.State{Current: &release.Release{ID: "r1"}}
	requireAppState(t, "result app", res.GetApp(), want)
	requireStoredState(t, dir, app, want)

	if got, want := fake.loadCount(), loads+1; got != want {
		t.Fatalf("POST /load count after the rollback = %d, want %d", got, want)
	}
	routes := pushedRoutes(t, fake.loadAt(loads))
	if len(routes) != 1 {
		t.Fatalf("routes in the pushed document = %d, want 1", len(routes))
	}
	dial := net.JoinHostPort(containerIPOn(t, e, p, app, "r1"), "8080")
	requireJSONEqual(t, "pushed route", routes[0], normalizedJSON(t, expectedRoute([]string{domain}, "r1", dial)))

	row := storedTimes(t, dir, app, "r1")
	if row.status != "current" {
		t.Errorf("r1 status after the rollback = %q, want %q", row.status, "current")
	}
	if !row.switchedAt.Valid {
		t.Fatalf("r1 switched_at = NULL, want the rollback time")
	}
	switchedAt := parseStoredTime(t, "r1 switched_at", row.switchedAt.String)
	if switchedAt.Before(before.Truncate(time.Second)) {
		t.Errorf("r1 switched_at = %s, want no earlier than %s", switchedAt, before.Truncate(time.Second))
	}
	if switchedAt.After(after) {
		t.Errorf("r1 switched_at = %s, want no later than %s", switchedAt, after)
	}
	if row.stopAfter.Valid {
		t.Errorf("r1 stop_after = %q, want NULL for the current release", row.stopAfter.String)
	}

	rolledFrom := storedTimes(t, dir, app, "r2")
	if rolledFrom.status != "retired" {
		t.Errorf("r2 status after the rollback = %q, want %q", rolledFrom.status, "retired")
	}
	if !rolledFrom.stopAfter.Valid {
		t.Fatalf("r2 stop_after = NULL, want the rollback time plus 30s")
	}
	requireEqual(t, "r2 stop_after", parseStoredTime(t, "r2 stop_after", rolledFrom.stopAfter.String),
		switchedAt.Add(30*time.Second))

	requireRunning(t, e, testContainerName(app, "r1"))
	requireRunning(t, e, testContainerName(app, "r2"))
	requireEqual(t, "project containers after the rollback", projectContainerIDs(t, e, p), containers)
}

// TestRollbackStoppedPreviousStartsAndGates covers the stopped previous
// container: the rollback starts it, runs the health gate, then pushes and
// commits; a failed gate answers FAILED and leaves the state and the container
// exactly as they were.
func TestRollbackStoppedPreviousStartsAndGates(t *testing.T) {
	e := testEngine(t)

	t.Run("a stopped previous container is started and gated", func(t *testing.T) {
		p := testProject(t, e)
		x, dir, fake := newSwitchExecutor(t, e)
		x.healthLimit = 20 * time.Second
		app := testAppID(p, "rbstart")
		const domain = "rbstart.example.com"

		switchTwice(t, e, x, p, app, domain)

		id := singleContainerID(t, e, testContainerName(app, "r1"))
		if _, err := e.cli.ContainerStop(context.Background(), id, client.ContainerStopOptions{}); err != nil {
			t.Fatalf("ContainerStop(%s) error = %v, want nil", id, err)
		}

		loads := fake.loadCount()
		res := x.Rollback(context.Background(), rollbackMsg(app))

		if got, want := res.GetOutcome(), agentv1.CommandOutcome_SUCCEEDED; got != want {
			t.Fatalf("Rollback() outcome = %v with detail %q, want %v", got, res.GetDetail(), want)
		}
		requireRunning(t, e, testContainerName(app, "r1"))

		want := release.State{Current: &release.Release{ID: "r1"}}
		requireAppState(t, "result app", res.GetApp(), want)
		requireStoredState(t, dir, app, want)

		if got, want := fake.loadCount(), loads+1; got != want {
			t.Fatalf("POST /load count after the rollback = %d, want %d", got, want)
		}
		routes := pushedRoutes(t, fake.loadAt(loads))
		if len(routes) != 1 {
			t.Fatalf("routes in the pushed document = %d, want 1", len(routes))
		}
		dial := net.JoinHostPort(containerIPOn(t, e, p, app, "r1"), "8080")
		requireJSONEqual(t, "pushed route", routes[0], normalizedJSON(t, expectedRoute([]string{domain}, "r1", dial)))
	})

	t.Run("a failed gate leaves the state and the container in place", func(t *testing.T) {
		p := testProject(t, e)
		x, dir, fake := newSwitchExecutor(t, e)
		x.healthLimit = 6 * time.Second
		app := testAppID(p, "rbfail")

		// Both releases are deployed with no health path, so their TCP gate
		// passes and each switch succeeds. The stored health path of r1 is set
		// afterwards, so only the rollback's restart meets the failing status.
		r1 := deployGate(t, e, x, p, app, "r1", []string{"/app", "health", "/healthz", "500"}, 8080, "")
		if res := x.SwitchRelease(context.Background(), switchMsg(app, r1.ReleaseID)); res.GetOutcome() != agentv1.CommandOutcome_SUCCEEDED {
			t.Fatalf("SwitchRelease(r1) outcome = %v with detail %q, want SUCCEEDED", res.GetOutcome(), res.GetDetail())
		}
		r2 := deployGate(t, e, x, p, app, "r2", []string{"/app"}, 8080, "")
		if res := x.SwitchRelease(context.Background(), switchMsg(app, r2.ReleaseID)); res.GetOutcome() != agentv1.CommandOutcome_SUCCEEDED {
			t.Fatalf("SwitchRelease(r2) outcome = %v with detail %q, want SUCCEEDED", res.GetOutcome(), res.GetDetail())
		}

		id := singleContainerID(t, e, testContainerName(app, "r1"))
		if _, err := e.cli.ContainerStop(context.Background(), id, client.ContainerStopOptions{}); err != nil {
			t.Fatalf("ContainerStop(%s) error = %v, want nil", id, err)
		}
		execAgentSQL(t, dir,
			`UPDATE releases SET health_path = ? WHERE app_id = ? AND release_id = ?`, "/healthz", app, r1.ReleaseID)

		loads := fake.loadCount()
		rows := stateText(t, dir)

		res := x.Rollback(context.Background(), rollbackMsg(app))

		if got, want := res.GetOutcome(), agentv1.CommandOutcome_FAILED; got != want {
			t.Fatalf("Rollback() with a failing gate outcome = %v with detail %q, want %v", got, res.GetDetail(), want)
		}
		detail := res.GetDetail()
		if detail == "" {
			t.Errorf("Rollback() detail = empty, want the health gate failure")
		}
		if strings.Contains(detail, "health-body-must-not-leak") {
			t.Errorf("Rollback() detail = %q, want no probe response body", detail)
		}
		requireEqual(t, "POST /load count after the failed gate", fake.loadCount(), loads)
		requireEqual(t, "agent state rows after the failed gate", stateText(t, dir), rows)
		requireStoredState(t, dir, app, release.State{
			Current:  &release.Release{ID: "r2"},
			Previous: &release.Release{ID: "r1"},
		})
		singleContainerID(t, e, testContainerName(app, "r1"))
	})
}

// TestRollbackPreviousContainerGone covers a previous release whose container is
// gone, both when the Engine no longer has it and when agent state has no
// container_id: the answer is FAILED with the exact message, nothing is pushed
// and the stored state is unchanged.
func TestRollbackPreviousContainerGone(t *testing.T) {
	e := testEngine(t)

	cases := []struct {
		name string
		gone func(t *testing.T, e *Engine, dir, app string)
	}{
		{
			name: "the container was removed from the Engine",
			gone: func(t *testing.T, e *Engine, _ string, app string) {
				id := singleContainerID(t, e, testContainerName(app, "r1"))
				if _, err := e.cli.ContainerRemove(context.Background(), id, client.ContainerRemoveOptions{Force: true}); err != nil {
					t.Fatalf("ContainerRemove(%s) error = %v, want nil", id, err)
				}
			},
		},
		{
			name: "the stored container_id is empty",
			gone: func(t *testing.T, _ *Engine, dir, app string) {
				execAgentSQL(t, dir,
					`UPDATE releases SET container_id = '' WHERE app_id = ? AND release_id = ?`, app, "r1")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := testProject(t, e)
			x, dir, fake := newSwitchExecutor(t, e)
			x.healthLimit = 20 * time.Second
			app := testAppID(p, "rbgone")
			const domain = "rbgone.example.com"

			switchTwice(t, e, x, p, app, domain)
			tc.gone(t, e, dir, app)

			loads := fake.loadCount()
			rows := stateText(t, dir)

			res := x.Rollback(context.Background(), rollbackMsg(app))

			const goneDetail = "the previous release's container is gone; deploy again"
			if got, want := res.GetOutcome(), agentv1.CommandOutcome_FAILED; got != want {
				t.Fatalf("Rollback() outcome = %v with detail %q, want %v", got, res.GetDetail(), want)
			}
			requireEqual(t, "Rollback() detail", res.GetDetail(), goneDetail)
			requireEqual(t, "POST /load count after the rollback", fake.loadCount(), loads)
			requireEqual(t, "agent state rows after the rollback", stateText(t, dir), rows)
			requireStoredState(t, dir, app, release.State{
				Current:  &release.Release{ID: "r2"},
				Previous: &release.Release{ID: "r1"},
			})
		})
	}
}

// TestRollbackRefusalsAndPushFailure covers each refusal, which reads nothing,
// pushes nothing and writes nothing, and a failed /load, which leaves the state
// unchanged so a re-sent Rollback succeeds.
func TestRollbackRefusalsAndPushFailure(t *testing.T) {
	e := testEngine(t)
	ctx := context.Background()

	refusals := []struct {
		name    string
		prepare func(t *testing.T, e *Engine, x *Executor, p *project, app string)
	}{
		{
			name:    "an unknown app",
			prepare: func(*testing.T, *Engine, *Executor, *project, string) {},
		},
		{
			name: "an app with only one release",
			prepare: func(t *testing.T, e *Engine, x *Executor, p *project, app string) {
				requireDeployOutcome(t, deployDomains(t, e, x, p, app, "r1", nil),
					agentv1.CommandOutcome_SUCCEEDED, "")
				if res := x.SwitchRelease(ctx, switchMsg(app, "r1")); res.GetOutcome() != agentv1.CommandOutcome_SUCCEEDED {
					t.Fatalf("SwitchRelease(r1) outcome = %v with detail %q, want SUCCEEDED", res.GetOutcome(), res.GetDetail())
				}
			},
		},
		{
			name: "an app right after a rollback",
			prepare: func(t *testing.T, e *Engine, x *Executor, p *project, app string) {
				switchTwice(t, e, x, p, app, "refuse.example.com")
				if res := x.Rollback(ctx, rollbackMsg(app)); res.GetOutcome() != agentv1.CommandOutcome_SUCCEEDED {
					t.Fatalf("the first Rollback() outcome = %v with detail %q, want SUCCEEDED", res.GetOutcome(), res.GetDetail())
				}
			},
		},
	}

	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			p := testProject(t, e)
			x, dir, fake := newSwitchExecutor(t, e)
			x.healthLimit = 20 * time.Second
			app := testAppID(p, "rbrefuse")

			tc.prepare(t, e, x, p, app)
			loads := fake.loadCount()
			rows := stateText(t, dir)
			containers := projectContainerIDs(t, e, p)

			res := x.Rollback(ctx, rollbackMsg(app))

			if got, want := res.GetOutcome(), agentv1.CommandOutcome_REJECTED; got != want {
				t.Fatalf("Rollback() outcome = %v with detail %q, want %v", got, res.GetDetail(), want)
			}
			if res.GetDetail() == "" {
				t.Errorf("Rollback() detail = empty, want the refusal")
			}
			_, stored, _ := storedApp(t, dir, app)
			requireAppState(t, "result app", res.GetApp(), stored)
			requireEqual(t, "POST /load count after the refusal", fake.loadCount(), loads)
			requireEqual(t, "agent state rows after the refusal", stateText(t, dir), rows)
			requireEqual(t, "project containers after the refusal", projectContainerIDs(t, e, p), containers)
		})
	}

	t.Run("a failed push leaves the state unchanged and a re-sent rollback succeeds", func(t *testing.T) {
		p := testProject(t, e)
		x, dir, fake := newSwitchExecutor(t, e)
		x.healthLimit = 20 * time.Second
		app := testAppID(p, "rbpush")
		const domain = "rbpush.example.com"

		switchTwice(t, e, x, p, app, domain)

		fake.setLoadResponse(http.StatusInternalServerError, "nope")
		loads := fake.loadCount()
		rows := stateText(t, dir)
		before := release.State{
			Current:  &release.Release{ID: "r2"},
			Previous: &release.Release{ID: "r1"},
		}

		res := x.Rollback(ctx, rollbackMsg(app))

		if got, want := res.GetOutcome(), agentv1.CommandOutcome_FAILED; got != want {
			t.Fatalf("Rollback() with /load answering 500 outcome = %v with detail %q, want %v", got, res.GetDetail(), want)
		}
		if detail := res.GetDetail(); !strings.Contains(detail, "500") {
			t.Errorf("Rollback() detail = %q, want it to contain the status 500", detail)
		}
		requireAppState(t, "result app", res.GetApp(), before)
		requireStoredState(t, dir, app, before)
		requireEqual(t, "agent state rows after the failed push", stateText(t, dir), rows)
		requireEqual(t, "POST /load count after the failed push", fake.loadCount(), loads+1)

		fake.setLoadResponse(http.StatusOK, "")
		res = x.Rollback(ctx, rollbackMsg(app))
		if got, want := res.GetOutcome(), agentv1.CommandOutcome_SUCCEEDED; got != want {
			t.Fatalf("the re-sent Rollback() outcome = %v with detail %q, want %v", got, res.GetDetail(), want)
		}
		requireStoredState(t, dir, app, release.State{Current: &release.Release{ID: "r1"}})
	})
}

// TestRollbackLeavesCandidateUntouched covers a rollback while a ready candidate
// is running: the candidate's id, phase, row and container are unchanged, and
// the pushed document routes only the restored release.
func TestRollbackLeavesCandidateUntouched(t *testing.T) {
	e := testEngine(t)
	p := testProject(t, e)
	x, dir, fake := newSwitchExecutor(t, e)
	x.healthLimit = 20 * time.Second
	app := testAppID(p, "rbcand")
	const domain = "rbcand.example.com"
	const candidateDomain = "rbcand-candidate.example.com"

	switchTwice(t, e, x, p, app, domain)
	requireDeployOutcome(t, deployDomains(t, e, x, p, app, "r3", []string{candidateDomain}),
		agentv1.CommandOutcome_SUCCEEDED, "")
	requireRunning(t, e, testContainerName(app, "r3"))

	loads := fake.loadCount()
	res := x.Rollback(context.Background(), rollbackMsg(app))

	if got, want := res.GetOutcome(), agentv1.CommandOutcome_SUCCEEDED; got != want {
		t.Fatalf("Rollback() outcome = %v with detail %q, want %v", got, res.GetDetail(), want)
	}

	want := release.State{
		Current:   &release.Release{ID: "r1"},
		Candidate: &release.Release{ID: "r3"},
		Phase:     release.PhaseReady,
	}
	requireAppState(t, "result app", res.GetApp(), want)
	requireStoredState(t, dir, app, want)

	row, found := storedRow(t, dir, app, "r3")
	if !found {
		t.Fatalf("releases row for %s/r3 = missing, want the candidate row", app)
	}
	requireEqual(t, "r3 status after the rollback", row.Status, "candidate")
	requireRunning(t, e, testContainerName(app, "r3"))

	if got, want := fake.loadCount(), loads+1; got != want {
		t.Fatalf("POST /load count after the rollback = %d, want %d", got, want)
	}
	routes := pushedRoutes(t, fake.loadAt(loads))
	if len(routes) != 1 {
		t.Fatalf("routes in the pushed document = %d, want 1", len(routes))
	}
	dial := net.JoinHostPort(containerIPOn(t, e, p, app, "r1"), "8080")
	requireJSONEqual(t, "pushed route", routes[0], normalizedJSON(t, expectedRoute([]string{domain}, "r1", dial)))

	candidateIP := containerIPOn(t, e, p, app, "r3")
	if body := string(fake.loadAt(loads)); strings.Contains(body, candidateIP) {
		t.Errorf("the pushed document contains the candidate container's address %q, want only the restored release's route", candidateIP)
	}
}
