package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"moonglass.dev/moonglass/internal/store"
)

func TestProjectsConformanceSQLite(t *testing.T) {
	s, _ := newSQLite(t)
	runProjectsConformance(t, s)
}

func TestProjectsConformancePostgres(t *testing.T) {
	runProjectsConformance(t, newPGEnv(t, true).openApp(t))
}

// seedOrg creates a fresh org with one server in it.
func seedOrg(t *testing.T, s store.Store) (orgID, serverID string) {
	t.Helper()
	ctx := context.Background()
	orgID, err := s.CreateOrg(ctx)
	if err != nil {
		t.Fatalf("CreateOrg error = %v, want nil", err)
	}
	srv, err := s.AddServer(ctx, orgID, "pk-"+randHex(t, 16))
	if err != nil {
		t.Fatalf("AddServer in org %s error = %v, want nil", orgID, err)
	}
	return orgID, srv.ID
}

func mustCreateProject(t *testing.T, s store.Store, orgID, name string) store.Project {
	t.Helper()
	p, err := s.CreateProject(context.Background(), orgID, name)
	if err != nil {
		t.Fatalf("CreateProject(%q) in org %s error = %v, want nil", name, orgID, err)
	}
	return p
}

func mustCreateEnvironment(t *testing.T, s store.Store, orgID, projectID, name string) store.Environment {
	t.Helper()
	e, err := s.CreateEnvironment(context.Background(), orgID, projectID, name)
	if err != nil {
		t.Fatalf("CreateEnvironment(%q) in project %s error = %v, want nil", name, projectID, err)
	}
	return e
}

func mustCreateService(t *testing.T, s store.Store, orgID, environmentID, name, serverID string) store.Service {
	t.Helper()
	svc, err := s.CreateService(context.Background(), orgID, environmentID, name, serverID)
	if err != nil {
		t.Fatalf("CreateService(%q) in environment %s on server %s error = %v, want nil", name, environmentID, serverID, err)
	}
	return svc
}

// orgTarget is one org with a server, a project, an environment and a service.
type orgTarget struct {
	orgID   string
	server  string
	project store.Project
	env     store.Environment
}

// seedTarget creates an org with one server and a project "web", its
// environment "prod" and a service "api" on the org's server.
func seedTarget(t *testing.T, s store.Store) orgTarget {
	t.Helper()
	orgID, serverID := seedOrg(t, s)
	p := mustCreateProject(t, s, orgID, "web")
	e := mustCreateEnvironment(t, s, orgID, p.ID, "prod")
	mustCreateService(t, s, orgID, e.ID, "api", serverID)
	return orgTarget{orgID: orgID, server: serverID, project: p, env: e}
}

// checkOrder compares names in list order.
func checkOrder(t *testing.T, what string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s = %v, want %v", what, got, want)
		return
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s = %v, want %v", what, got, want)
			return
		}
	}
}

func projectNames(ps []store.Project) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Name
	}
	return out
}

func environmentNames(es []store.Environment) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Name
	}
	return out
}

func serviceNames(ss []store.Service) []string {
	out := make([]string, len(ss))
	for i, svc := range ss {
		out[i] = svc.Name
	}
	return out
}

func runProjectsConformance(t *testing.T, s store.Store) {
	ctx := context.Background()

	t.Run("create and list", func(t *testing.T) {
		org, srv := seedOrg(t, s)
		web := mustCreateProject(t, s, org, "web")
		if web.OrgID != org || web.Name != "web" || web.ID == "" || web.CreatedAt == "" {
			t.Errorf("CreateProject = %+v, want org %q, name %q, non-empty id and created_at", web, org, "web")
		}

		prod := mustCreateEnvironment(t, s, org, web.ID, "prod")
		if prod.OrgID != org || prod.ProjectID != web.ID || prod.Name != "prod" || prod.ID == "" || prod.CreatedAt == "" {
			t.Errorf("CreateEnvironment = %+v, want org %q, project %q, name %q, non-empty id and created_at", prod, org, web.ID, "prod")
		}

		api := mustCreateService(t, s, org, prod.ID, "api", srv)
		if api.OrgID != org || api.ProjectID != web.ID || api.EnvironmentID != prod.ID ||
			api.Name != "api" || api.ServerID != srv || api.ID == "" || api.CreatedAt == "" {
			t.Errorf("CreateService = %+v, want org %q, project %q, environment %q, name %q, server %q, non-empty id and created_at",
				api, org, web.ID, prod.ID, "api", srv)
		}

		projects, err := s.Projects(ctx, org)
		if err != nil {
			t.Fatalf("Projects error = %v, want nil", err)
		}
		if len(projects) != 1 || projects[0].ID != web.ID || projects[0].OrgID != org ||
			projects[0].Name != "web" || projects[0].CreatedAt == "" {
			t.Errorf("Projects = %+v, want exactly %+v", projects, web)
		}

		environments, err := s.Environments(ctx, org, web.ID)
		if err != nil {
			t.Fatalf("Environments error = %v, want nil", err)
		}
		if len(environments) != 1 || environments[0].ID != prod.ID || environments[0].OrgID != org ||
			environments[0].ProjectID != web.ID || environments[0].Name != "prod" || environments[0].CreatedAt == "" {
			t.Errorf("Environments = %+v, want exactly %+v", environments, prod)
		}

		services, err := s.Services(ctx, org, prod.ID)
		if err != nil {
			t.Fatalf("Services error = %v, want nil", err)
		}
		if len(services) != 1 || services[0].ID != api.ID || services[0].OrgID != org ||
			services[0].ProjectID != web.ID || services[0].EnvironmentID != prod.ID ||
			services[0].Name != "api" || services[0].ServerID != srv || services[0].CreatedAt == "" {
			t.Errorf("Services = %+v, want exactly %+v", services, api)
		}

		// Lists are ordered by name.
		alpha := mustCreateProject(t, s, org, "alpha")
		staging := mustCreateEnvironment(t, s, org, web.ID, "staging")
		worker := mustCreateService(t, s, org, prod.ID, "worker", srv)

		if alpha.Name != "alpha" || staging.Name != "staging" || worker.Name != "worker" {
			t.Errorf("created names = %q, %q, %q, want %q, %q, %q", alpha.Name, staging.Name, worker.Name, "alpha", "staging", "worker")
		}

		projects, err = s.Projects(ctx, org)
		if err != nil {
			t.Fatalf("Projects error = %v, want nil", err)
		}
		checkOrder(t, "Projects names", projectNames(projects), []string{"alpha", "web"})

		environments, err = s.Environments(ctx, org, web.ID)
		if err != nil {
			t.Fatalf("Environments error = %v, want nil", err)
		}
		checkOrder(t, "Environments names", environmentNames(environments), []string{"prod", "staging"})
		for _, e := range environments {
			if e.ProjectID != web.ID {
				t.Errorf("Environments entry %+v project id = %q, want %q", e, e.ProjectID, web.ID)
			}
		}

		services, err = s.Services(ctx, org, prod.ID)
		if err != nil {
			t.Fatalf("Services error = %v, want nil", err)
		}
		checkOrder(t, "Services names", serviceNames(services), []string{"api", "worker"})
		for _, svc := range services {
			if svc.ProjectID != web.ID || svc.EnvironmentID != prod.ID || svc.ServerID != srv {
				t.Errorf("Services entry %+v ids = project %q, environment %q, server %q; want %q, %q, %q",
					svc, svc.ProjectID, svc.EnvironmentID, svc.ServerID, web.ID, prod.ID, srv)
			}
		}
	})

	t.Run("cross-org parents", func(t *testing.T) {
		a, b := seedTarget(t, s), seedTarget(t, s)

		if _, err := s.CreateService(ctx, a.orgID, a.env.ID, "x", b.server); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("CreateService on another org's server %q error = %v, want ErrNotFound", b.server, err)
		}
		if _, err := s.CreateService(ctx, a.orgID, a.env.ID, "x", randHex(t, 16)); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("CreateService on an unknown server id error = %v, want ErrNotFound", err)
		}
		if _, err := s.CreateService(ctx, a.orgID, b.env.ID, "x", a.server); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("CreateService in another org's environment %q error = %v, want ErrNotFound", b.env.ID, err)
		}
		if _, err := s.CreateEnvironment(ctx, a.orgID, b.project.ID, "x"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("CreateEnvironment in another org's project %q error = %v, want ErrNotFound", b.project.ID, err)
		}

		services, err := s.Services(ctx, a.orgID, a.env.ID)
		if err != nil {
			t.Fatalf("Services(a) error = %v, want nil", err)
		}
		checkOrder(t, "Services(a) names after refused creates", serviceNames(services), []string{"api"})

		environments, err := s.Environments(ctx, a.orgID, a.project.ID)
		if err != nil {
			t.Fatalf("Environments(a) error = %v, want nil", err)
		}
		checkOrder(t, "Environments(a) names after refused creates", environmentNames(environments), []string{"prod"})

		// A refused create must not write a row for A under B's parent id:
		// such a row would carry A's org_id and stay hidden from the lists above.
		strayEnvironments, err := s.Environments(ctx, a.orgID, b.project.ID)
		if err != nil {
			t.Fatalf("Environments(a, b's project) error = %v, want nil", err)
		}
		if len(strayEnvironments) != 0 {
			t.Errorf("Environments(a, b's project %q) = %+v, want no rows", b.project.ID, strayEnvironments)
		}

		strayServices, err := s.Services(ctx, a.orgID, b.env.ID)
		if err != nil {
			t.Fatalf("Services(a, b's environment) error = %v, want nil", err)
		}
		if len(strayServices) != 0 {
			t.Errorf("Services(a, b's environment %q) = %+v, want no rows", b.env.ID, strayServices)
		}

		projects, err := s.Projects(ctx, b.orgID)
		if err != nil {
			t.Fatalf("Projects(b) error = %v, want nil", err)
		}
		checkOrder(t, "Projects(b) names after refused creates", projectNames(projects), []string{"web"})

		environments, err = s.Environments(ctx, b.orgID, b.project.ID)
		if err != nil {
			t.Fatalf("Environments(b) error = %v, want nil", err)
		}
		checkOrder(t, "Environments(b) names after refused creates", environmentNames(environments), []string{"prod"})

		services, err = s.Services(ctx, b.orgID, b.env.ID)
		if err != nil {
			t.Fatalf("Services(b) error = %v, want nil", err)
		}
		checkOrder(t, "Services(b) names after refused creates", serviceNames(services), []string{"api"})
	})

	t.Run("duplicates", func(t *testing.T) {
		a := seedTarget(t, s)
		b, _ := seedOrg(t, s)

		if _, err := s.CreateProject(ctx, a.orgID, "web"); !errors.Is(err, store.ErrDuplicate) {
			t.Errorf("second CreateProject(%q) error = %v, want ErrDuplicate", "web", err)
		}
		if _, err := s.CreateEnvironment(ctx, a.orgID, a.project.ID, "prod"); !errors.Is(err, store.ErrDuplicate) {
			t.Errorf("second CreateEnvironment(%q) in project %q error = %v, want ErrDuplicate", "prod", a.project.ID, err)
		}
		if _, err := s.CreateService(ctx, a.orgID, a.env.ID, "api", a.server); !errors.Is(err, store.ErrDuplicate) {
			t.Errorf("second CreateService(%q) in environment %q error = %v, want ErrDuplicate", "api", a.env.ID, err)
		}

		other := mustCreateProject(t, s, a.orgID, "other")
		if _, err := s.CreateEnvironment(ctx, a.orgID, other.ID, "prod"); err != nil {
			t.Errorf("CreateEnvironment(%q) in another project %q error = %v, want nil", "prod", other.ID, err)
		}
		otherEnv := mustCreateEnvironment(t, s, a.orgID, a.project.ID, "staging")
		if _, err := s.CreateService(ctx, a.orgID, otherEnv.ID, "api", a.server); err != nil {
			t.Errorf("CreateService(%q) in another environment %q error = %v, want nil", "api", otherEnv.ID, err)
		}
		// The same project name in another org is allowed.
		otherOrgProject, err := s.CreateProject(ctx, b, "web")
		if err != nil {
			t.Fatalf("CreateProject(%q) in another org %q error = %v, want nil", "web", b, err)
		}
		if otherOrgProject.OrgID != b || otherOrgProject.Name != "web" || otherOrgProject.ID == "" {
			t.Errorf("CreateProject(%q) in another org = %+v, want org %q, name %q, non-empty id", "web", otherOrgProject, b, "web")
		}
		projects, err := s.Projects(ctx, b)
		if err != nil {
			t.Fatalf("Projects(b) error = %v, want nil", err)
		}
		checkOrder(t, "Projects(b) names after the same project name", projectNames(projects), []string{"web"})
	})

	t.Run("resolve target", func(t *testing.T) {
		a, b := seedTarget(t, s), seedTarget(t, s)
		mustCreateProject(t, s, b.orgID, "b-only")
		mustCreateEnvironment(t, s, b.orgID, b.project.ID, "b-only-env")

		projectID, environmentID, err := s.ResolveTarget(ctx, a.orgID, "web", "prod")
		if err != nil || projectID != a.project.ID || environmentID != a.env.ID {
			t.Errorf("ResolveTarget(web, prod) = (%q, %q, %v), want (%q, %q, nil)",
				projectID, environmentID, err, a.project.ID, a.env.ID)
		}

		projectID, environmentID, err = s.ResolveTarget(ctx, a.orgID, "web", "")
		if err != nil || projectID != a.project.ID || environmentID != "" {
			t.Errorf("ResolveTarget(web, \"\") = (%q, %q, %v), want (%q, \"\", nil)",
				projectID, environmentID, err, a.project.ID)
		}

		if _, _, err := s.ResolveTarget(ctx, a.orgID, "nope", "prod"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("ResolveTarget(unknown project) error = %v, want ErrNotFound", err)
		}
		if _, _, err := s.ResolveTarget(ctx, a.orgID, "web", "nope"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("ResolveTarget(unknown environment) error = %v, want ErrNotFound", err)
		}
		if _, _, err := s.ResolveTarget(ctx, a.orgID, "b-only", ""); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("ResolveTarget(project only in another org) error = %v, want ErrNotFound", err)
		}
		if _, _, err := s.ResolveTarget(ctx, a.orgID, "web", "b-only-env"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("ResolveTarget(environment only in another org) error = %v, want ErrNotFound", err)
		}
	})

	t.Run("second org sees nothing", func(t *testing.T) {
		a := seedTarget(t, s)
		b, _ := seedOrg(t, s)

		projects, err := s.Projects(ctx, b)
		if err != nil {
			t.Fatalf("Projects(b) error = %v, want nil", err)
		}
		if len(projects) != 0 {
			t.Errorf("Projects(b) = %+v, want no rows", projects)
		}

		environments, err := s.Environments(ctx, b, a.project.ID)
		if err != nil {
			t.Fatalf("Environments(b, a's project) error = %v, want nil", err)
		}
		if len(environments) != 0 {
			t.Errorf("Environments(b, a's project) = %+v, want no rows", environments)
		}

		services, err := s.Services(ctx, b, a.env.ID)
		if err != nil {
			t.Fatalf("Services(b, a's environment) error = %v, want nil", err)
		}
		if len(services) != 0 {
			t.Errorf("Services(b, a's environment) = %+v, want no rows", services)
		}

		projects, err = s.Projects(ctx, a.orgID)
		if err != nil {
			t.Fatalf("Projects(a) error = %v, want nil", err)
		}
		checkOrder(t, "Projects(a) names after org b lists", projectNames(projects), []string{"web"})
	})
}

func TestPostgresRLSOnProjectTables(t *testing.T) {
	env := newPGEnv(t, true)
	ctx := context.Background()
	s := env.openApp(t)

	var targets [2]orgTarget
	for i := range targets {
		targets[i] = seedTarget(t, s)
	}
	a, b := targets[0], targets[1]

	app := env.rawDB(t, env.appDSN)
	empty, nonexistent := "", randHex(t, 16)
	tables := []string{"projects", "environments", "services"}

	for _, tc := range []struct {
		name    string
		setting *string
	}{
		{"unset", nil},
		{"empty", &empty},
		{"nonexistent org", &nonexistent},
	} {
		for _, tbl := range tables {
			if got := orgIDsUnder(t, app, tc.setting, tbl); len(got) != 0 {
				t.Errorf("app.org_id %s: SELECT org_id FROM %s (no WHERE) = %v, want no rows", tc.name, tbl, got)
			}
		}
	}

	for _, tbl := range tables {
		got := orgIDsUnder(t, app, &a.orgID, tbl)
		if len(got) == 0 {
			t.Errorf("app.org_id=A: SELECT FROM %s returned no rows, want A's rows", tbl)
		}
		for _, id := range got {
			if id != a.orgID {
				t.Errorf("app.org_id=A: SELECT org_id FROM %s (no WHERE) returned org %q, want only %q (B is %q)", tbl, id, a.orgID, b.orgID)
			}
		}
	}

	// WITH CHECK: under A, a row for B with B's real parent ids is rejected
	// (so only the policy can refuse it, not a foreign key); a row for A with
	// A's parent ids succeeds.
	now := time.Now().UTC().Format(time.RFC3339)
	inserts := []struct {
		table string
		stmt  string
	}{
		{"projects", "INSERT INTO projects (id, org_id, name, created_at) VALUES ($1, $2, $3, $4)"},
		{"environments", "INSERT INTO environments (id, org_id, project_id, name, created_at) VALUES ($1, $2, $3, $4, $5)"},
		{"services", "INSERT INTO services (id, org_id, project_id, environment_id, name, server_id, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7)"},
	}
	for _, ins := range inserts {
		for _, tc := range []struct {
			name    string
			org     string
			source  orgTarget
			wantErr bool
		}{
			{"another org's row", b.orgID, b, true},
			{"own row", a.orgID, a, false},
		} {
			t.Run(ins.table+"/"+tc.name, func(t *testing.T) {
				id, name := randHex(t, 16), "rls-"+randHex(t, 8)
				var args []any
				switch ins.table {
				case "projects":
					args = []any{id, tc.org, name, now}
				case "environments":
					args = []any{id, tc.org, tc.source.project.ID, name, now}
				case "services":
					args = []any{id, tc.org, tc.source.project.ID, tc.source.env.ID, name, tc.source.server, now}
				}

				tx, err := app.BeginTx(ctx, nil)
				if err != nil {
					t.Fatalf("begin: %v", err)
				}
				defer func() { _ = tx.Rollback() }()
				if _, err := tx.ExecContext(ctx, "SELECT set_config('app.org_id', $1, true)", a.orgID); err != nil {
					t.Fatalf("set_config: %v", err)
				}
				_, err = tx.ExecContext(ctx, ins.stmt, args...)

				if !tc.wantErr {
					if err != nil {
						t.Errorf("control INSERT INTO %s for org %s under app.org_id=%s error = %v, want nil", ins.table, tc.org, a.orgID, err)
					}
					return
				}
				var sqlState interface{ SQLState() string }
				if !errors.As(err, &sqlState) || sqlState.SQLState() != "42501" {
					t.Errorf("INSERT INTO %s for org %s under app.org_id=%s error = %v, want SQLSTATE %q", ins.table, tc.org, a.orgID, err, "42501")
				}
			})
		}
	}

	super := env.rawDB(t, env.superDSN)
	rows, err := super.QueryContext(ctx, "SELECT tablename, rowsecurity FROM pg_tables WHERE schemaname = 'public'")
	if err != nil {
		t.Fatalf("query pg_tables: %v", err)
	}
	defer rows.Close()
	rls := map[string]bool{}
	for rows.Next() {
		var name string
		var sec bool
		if err := rows.Scan(&name, &sec); err != nil {
			t.Fatalf("scan: %v", err)
		}
		rls[name] = sec
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	for _, tbl := range tables {
		if !rls[tbl] {
			t.Errorf("table %q row level security = %v, want true", tbl, rls[tbl])
		}
	}
}
