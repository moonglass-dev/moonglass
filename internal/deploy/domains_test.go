package deploy

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"moonglass.dev/moonglass/internal/agent/state"
	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
	"moonglass.dev/moonglass/internal/release"
)

// deployDomains imports an image and deploys app at rel with port 8080 and the
// given domains. The caller asserts the outcome.
func deployDomains(t *testing.T, e *Engine, x *Executor, p *project, app, rel string, domains []string) *agentv1.CommandResult {
	t.Helper()
	return deployDomainsPort(t, e, x, p, app, rel, 8080, domains)
}

// deployDomainsPort imports an image and deploys app at rel with the given port
// and domains, so a config built from the release's own port is distinguishable
// from one that always names the default. The caller asserts the outcome.
func deployDomainsPort(t *testing.T, e *Engine, x *Executor, p *project, app, rel string, port uint32, domains []string) *agentv1.CommandResult {
	t.Helper()
	ref := "moonglass.local/" + p.name + "/web:" + rel
	p.registerImage(ref)
	importImage(t, e, ref, []string{"/app"})
	msg := deployMsg(p.name, app, rel, ref)
	msg.Port = port
	msg.Domains = domains
	return x.Deploy(context.Background(), msg)
}

// requireDeployOutcome fails with the detail when Deploy's outcome is not want;
// a non-empty detail must match exactly.
func requireDeployOutcome(t *testing.T, res *agentv1.CommandResult, want agentv1.CommandOutcome, detail string) {
	t.Helper()
	if got := res.GetOutcome(); got != want {
		t.Fatalf("Deploy outcome = %v with detail %q, want %v", got, res.GetDetail(), want)
	}
	if detail != "" && res.GetDetail() != detail {
		t.Errorf("Deploy detail = %q, want %q", res.GetDetail(), detail)
	}
}

// makeCurrent moves app's candidate release to current with the direct update
// W2-4b's switch will make, so this ticket's tests do not depend on it.
func makeCurrent(t *testing.T, dataDir, app string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "agent.db"))
	if err != nil {
		t.Fatalf("sql.Open(agent.db) error = %v, want nil", err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	res, err := db.ExecContext(context.Background(),
		`UPDATE apps SET current_release_id = candidate_release_id, candidate_release_id = NULL, phase = 'none'
		WHERE app_id = ?`, app)
	if err != nil {
		t.Fatalf("make %s's candidate current: %v", app, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		t.Fatalf("count the apps rows updated for %s: %v", app, err)
	}
	if affected != 1 {
		t.Fatalf("apps rows updated for %s = %d, want 1", app, affected)
	}
}

func TestDeployRejectsDomainRoutedByAnotherApp(t *testing.T) {
	e := testEngine(t)

	cases := []struct {
		name string
		run  func(t *testing.T, e *Engine, p *project, x *Executor, dir, app string)
	}{
		{
			name: "another app's current release routes the domain",
			run: func(t *testing.T, e *Engine, p *project, x *Executor, dir, app string) {
				other := testAppID(p, "other")
				requireDeployOutcome(t,
					deployDomains(t, e, x, p, other, "r1", []string{"x.example.com"}),
					agentv1.CommandOutcome_SUCCEEDED, "")
				makeCurrent(t, dir, other)

				res := deployDomains(t, e, x, p, app, "r2", []string{"y.example.com", "x.example.com"})

				wantDetail := "domain x.example.com is already routed to app " + other
				requireDeployOutcome(t, res, agentv1.CommandOutcome_REJECTED, wantDetail)

				row, found := storedRow(t, dir, app, "r2")
				if !found {
					t.Fatalf("releases row for %s/r2 = missing, want a failed row with the domain detail", app)
				}
				requireEqual(t, "row status", row.Status, "failed")
				requireEqual(t, "row error", row.Err, wantDetail)

				binding, st, known := storedApp(t, dir, app)
				requireEqual(t, "binding after the rejection", binding, state.Binding{})
				requireEqual(t, "release state after the rejection", st, release.State{})
				requireEqual(t, "known after the rejection", known, false)
				requireNoContainerNamed(t, e, testContainerName(app, "r2"))
			},
		},
		{
			name: "another app's candidate does not hold its domains",
			run: func(t *testing.T, e *Engine, p *project, x *Executor, _ string, app string) {
				other := testAppID(p, "other")
				requireDeployOutcome(t,
					deployDomains(t, e, x, p, other, "r1", []string{"x.example.com"}),
					agentv1.CommandOutcome_SUCCEEDED, "")

				res := deployDomains(t, e, x, p, app, "r2", []string{"x.example.com"})

				requireDeployOutcome(t, res, agentv1.CommandOutcome_SUCCEEDED, "")
				requireRunning(t, e, testContainerName(app, "r2"))
			},
		},
		{
			name: "the app's own current release never conflicts",
			run: func(t *testing.T, e *Engine, p *project, x *Executor, dir, app string) {
				requireDeployOutcome(t,
					deployDomains(t, e, x, p, app, "r1", []string{"x.example.com"}),
					agentv1.CommandOutcome_SUCCEEDED, "")
				makeCurrent(t, dir, app)

				res := deployDomains(t, e, x, p, app, "r2", []string{"x.example.com"})

				requireDeployOutcome(t, res, agentv1.CommandOutcome_SUCCEEDED, "")
				requireRunning(t, e, testContainerName(app, "r2"))
			},
		},
		{
			name: "an illegal transition wins over the domain check",
			run: func(t *testing.T, e *Engine, p *project, x *Executor, dir, app string) {
				other := testAppID(p, "other")
				requireDeployOutcome(t,
					deployDomains(t, e, x, p, other, "r1", []string{"x.example.com"}),
					agentv1.CommandOutcome_SUCCEEDED, "")
				makeCurrent(t, dir, other)

				requireDeployOutcome(t,
					deployDomains(t, e, x, p, app, "r1", nil),
					agentv1.CommandOutcome_SUCCEEDED, "")

				res := deployDomains(t, e, x, p, app, "r2", []string{"x.example.com"})

				if got, want := res.GetOutcome(), agentv1.CommandOutcome_REJECTED; got != want {
					t.Fatalf("Deploy outcome = %v with detail %q, want %v", got, res.GetDetail(), want)
				}
				if !strings.Contains(res.GetDetail(), "illegal") {
					t.Errorf("Deploy detail = %q, want the illegal transition error", res.GetDetail())
				}
				if strings.Contains(res.GetDetail(), "already routed") {
					t.Errorf("Deploy detail = %q, want the transition error, not the domain error", res.GetDetail())
				}
				if _, found := storedRow(t, dir, app, "r2"); found {
					t.Errorf("releases row for %s/r2 exists, want no row for a refused transition", app)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := testProject(t, e)
			x, dir := newExecutor(t, e)
			tc.run(t, e, p, x, dir, testAppID(p, "one"))
		})
	}
}
