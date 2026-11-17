package store_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"moonglass.dev/moonglass/internal/store"
	"moonglass.dev/moonglass/internal/store/pg"
)

// orgIDsUnder runs "SELECT org_id FROM <table>" (no WHERE) as the application
// role inside a transaction; when setting is non-nil it sets app.org_id first.
func orgIDsUnder(t *testing.T, db *sql.DB, setting *string, table string) []string {
	t.Helper()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if setting != nil {
		if _, err := tx.ExecContext(ctx, "SELECT set_config('app.org_id', $1, true)", *setting); err != nil {
			t.Fatalf("set_config: %v", err)
		}
	}
	rows, err := tx.QueryContext(ctx, "SELECT org_id FROM "+table)
	if err != nil {
		t.Fatalf("SELECT org_id FROM %s: %v", table, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, id)
	}
	return out
}

func TestPostgresMissingOrgFilterReturnsNothing(t *testing.T) {
	env := newPGEnv(t, true)
	ctx := context.Background()
	s := env.openApp(t)

	var orgs [2]string
	for i := range orgs {
		id, err := s.CreateOrg(ctx)
		if err != nil {
			t.Fatalf("CreateOrg error = %v, want nil", err)
		}
		orgs[i] = id
		u, err := s.UserForIdentity(ctx, "rls:"+randHex(t, 8))
		if err != nil {
			t.Fatalf("UserForIdentity error = %v, want nil", err)
		}
		if err := s.AddMembership(ctx, id, u, store.RoleOwner); err != nil {
			t.Fatalf("AddMembership error = %v, want nil", err)
		}
		if _, err := s.AddServer(ctx, id, "pub-"+randHex(t, 16)); err != nil {
			t.Fatalf("AddServer error = %v, want nil", err)
		}
	}
	a, b := orgs[0], orgs[1]

	app := env.rawDB(t, env.appDSN)
	empty, nonexistent := "", randHex(t, 16)
	tables := []string{"orgs", "memberships", "servers"}

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
		got := orgIDsUnder(t, app, &a, tbl)
		if len(got) == 0 {
			t.Errorf("app.org_id=A: SELECT FROM %s returned no rows, want A's rows", tbl)
		}
		for _, id := range got {
			if id != a {
				t.Errorf("app.org_id=A: SELECT org_id FROM %s (no WHERE) returned org %q, want only %q (B is %q)", tbl, id, a, b)
			}
		}
	}

	// WITH CHECK: under A, a row for B is rejected; a row for A is accepted.
	for _, tc := range []struct {
		org     string
		wantErr bool
	}{{b, true}, {a, false}} {
		tx, err := app.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		if _, err := tx.ExecContext(ctx, "SELECT set_config('app.org_id', $1, true)", a); err != nil {
			t.Fatalf("set_config: %v", err)
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO servers (id, org_id, created_at) VALUES ($1, $2, $3)",
			store.HashToken(randHex(t, 8))[:32], tc.org, time.Now().UTC().Format(time.RFC3339))
		if gotErr := err != nil; gotErr != tc.wantErr {
			t.Errorf("INSERT servers org_id=%s under app.org_id=A: error = %v, want error=%v", tc.org, err, tc.wantErr)
		}
		_ = tx.Rollback()
	}
}

func TestAppRoleCannotBypassRLS(t *testing.T) {
	env := newPGEnv(t, true)
	ctx := context.Background()
	super := env.rawDB(t, env.superDSN)

	var bypass, superuser bool
	err := super.QueryRowContext(ctx, "SELECT rolbypassrls, rolsuper FROM pg_roles WHERE rolname = $1", env.appRole).Scan(&bypass, &superuser)
	if err != nil {
		t.Fatalf("query pg_roles: %v", err)
	}
	if bypass || superuser {
		t.Errorf("app role bypassrls = %v, superuser = %v, want false, false", bypass, superuser)
	}

	rows, err := super.QueryContext(ctx, "SELECT tablename, tableowner, rowsecurity FROM pg_tables WHERE schemaname = 'public'")
	if err != nil {
		t.Fatalf("query pg_tables: %v", err)
	}
	defer rows.Close()
	rls := map[string]bool{}
	for rows.Next() {
		var name, owner string
		var sec bool
		if err := rows.Scan(&name, &owner, &sec); err != nil {
			t.Fatalf("scan: %v", err)
		}
		rls[name] = sec
		if owner != env.ownerRole {
			t.Errorf("table %q owner = %q, want migration role %q (not app role %q)", name, owner, env.ownerRole, env.appRole)
		}
	}
	for _, tbl := range []string{"orgs", "memberships", "servers"} {
		if !rls[tbl] {
			t.Errorf("table %q row level security = %v, want true", tbl, rls[tbl])
		}
	}

	app := env.rawDB(t, env.appDSN)
	for _, stmt := range []string{
		"ALTER TABLE servers ADD COLUMN extra TEXT",
		"ALTER TABLE servers DISABLE ROW LEVEL SECURITY",
		"DROP TABLE servers",
	} {
		if _, err := app.ExecContext(ctx, stmt); err == nil {
			t.Errorf("app role %q error = nil, want permission error", stmt)
		}
	}

	// Migrating with the app DSN must fail and apply nothing.
	fresh := newPGEnv(t, false)
	if err := pg.Migrate(ctx, fresh.appDSN, fresh.appRole); err == nil {
		t.Error("pg.Migrate with the application DSN error = nil, want an error")
	}
	var n int
	if err := fresh.rawDB(t, fresh.superDSN).QueryRowContext(ctx, "SELECT count(*) FROM pg_tables WHERE schemaname = 'public'").Scan(&n); err != nil {
		t.Fatalf("count tables: %v", err)
	}
	if n != 0 {
		t.Errorf("tables after failed app-role migrate = %d, want 0", n)
	}

	// The product itself must refuse a role that RLS does not constrain.
	for name, dsn := range map[string]string{"owner": env.ownerDSN, "superuser": env.superDSN} {
		if s, err := pg.Open(ctx, dsn); err == nil {
			_ = s.Close()
			t.Errorf("pg.Open as the %s role error = nil, want an error", name)
		} else if !strings.Contains(err.Error(), "role") {
			t.Errorf("pg.Open as the %s role error = %q, want it to name the role problem", name, err)
		}
	}
	if err := pg.Migrate(ctx, env.ownerDSN, env.ownerRole); err == nil {
		t.Error("pg.Migrate with the owner as appRole error = nil, want an error")
	}
	byp := "mg_bypass_" + randHex(t, 6)
	if _, err := super.ExecContext(ctx, "CREATE ROLE "+byp+" NOSUPERUSER BYPASSRLS"); err != nil {
		t.Fatalf("create bypassrls role: %v", err)
	}
	t.Cleanup(func() { _, _ = super.Exec("DROP ROLE IF EXISTS " + byp) })
	if err := pg.Migrate(ctx, env.ownerDSN, byp); err == nil {
		t.Error("pg.Migrate with a BYPASSRLS appRole error = nil, want an error")
	}
}
