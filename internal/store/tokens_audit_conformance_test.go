package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"moonglass.dev/moonglass/internal/store"
)

func TestTokensAuditConformanceSQLite(t *testing.T) {
	s, _ := newSQLite(t)
	runTokensAuditConformance(t, s)
}

func TestTokensAuditConformancePostgres(t *testing.T) {
	env := newPGEnv(t, true)
	runTokensAuditConformance(t, env.openApp(t))
}

func runTokensAuditConformance(t *testing.T, s store.Store) {
	ctx := context.Background()
	seed := func(t *testing.T) (orgID, userID string) {
		t.Helper()
		orgID, err := s.CreateOrg(ctx)
		if err != nil {
			t.Fatalf("CreateOrg error = %v, want nil", err)
		}
		userID, err = s.UserForIdentity(ctx, "ta:"+randHex(t, 8))
		if err != nil {
			t.Fatalf("UserForIdentity error = %v, want nil", err)
		}
		if err := s.AddMembership(ctx, orgID, userID, store.RoleAdmin); err != nil {
			t.Fatalf("AddMembership error = %v, want nil", err)
		}
		return orgID, userID
	}

	t.Run("token round trip", func(t *testing.T) {
		orgID, userID := seed(t)
		secret := "mg_" + randHex(t, 8)
		id := "t" + randHex(t, 6)
		tok := store.Token{
			ID: id, OrgID: orgID, UserID: userID, Name: "ci", Last4: "abcd",
			ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			Scopes:    []store.Scope{{Project: "*", Environment: "*", Operation: "*"}},
		}
		if err := s.AddToken(ctx, orgID, tok, store.HashToken(secret)); err != nil {
			t.Fatalf("AddToken error = %v, want nil", err)
		}
		got, role, err := s.TokenByHash(ctx, store.HashToken(secret))
		if err != nil {
			t.Fatalf("TokenByHash error = %v, want nil", err)
		}
		if got.ID != id || got.OrgID != orgID || got.Name != "ci" || got.Last4 != "abcd" || len(got.Scopes) != 1 || role != store.RoleAdmin {
			t.Errorf("TokenByHash = %+v role %s, want token %s in org %s with one scope and role admin", got, role, id, orgID)
		}
		if _, _, err := s.TokenByHash(ctx, store.HashToken("mg_other"+randHex(t, 4))); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("TokenByHash(unknown) error = %v, want ErrNotFound", err)
		}
	})

	t.Run("audit chain per org", func(t *testing.T) {
		a, _ := seed(t)
		b, _ := seed(t)
		appendN := func(org string, n int) {
			for i := 0; i < n; i++ {
				err := s.AppendAudit(ctx, org, func(prev string, seq int64) store.AuditEvent {
					return store.AuditEvent{
						OrgID: org, Seq: seq, ID: "e" + randHex(t, 4), PrevHash: prev,
						Hash: "h" + org + string(rune('0'+seq)), Operation: "x", Outcome: "allowed", PrincipalKind: "human",
					}
				})
				if err != nil {
					t.Fatalf("AppendAudit error = %v, want nil", err)
				}
			}
		}
		appendN(a, 2)
		appendN(b, 1)
		evs, err := s.AuditEvents(ctx, a)
		if err != nil || len(evs) != 2 {
			t.Fatalf("AuditEvents(a) = %d events, err %v, want 2", len(evs), err)
		}
		if evs[0].Seq != 1 || evs[0].PrevHash != "" || evs[1].Seq != 2 || evs[1].PrevHash != evs[0].Hash {
			t.Errorf("events = %+v, want seq 1 with empty prev, then seq 2 chained to seq 1", evs)
		}
		evs, err = s.AuditEvents(ctx, b)
		if err != nil || len(evs) != 1 || evs[0].OrgID != b || evs[0].Seq != 1 {
			t.Errorf("AuditEvents(b) = %+v, err %v, want only org b's seq 1", evs, err)
		}
	})
}

func TestPostgresRLSOnTokenAndAuditTables(t *testing.T) {
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
		u, err := s.UserForIdentity(ctx, "rls2:"+randHex(t, 8))
		if err != nil {
			t.Fatalf("UserForIdentity error = %v, want nil", err)
		}
		if err := s.AddMembership(ctx, id, u, store.RoleAdmin); err != nil {
			t.Fatalf("AddMembership error = %v, want nil", err)
		}
		tok := store.Token{
			ID: "t" + randHex(t, 6), OrgID: id, UserID: u, Name: "n", Last4: "abcd",
			ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			Scopes:    []store.Scope{{Project: "*", Environment: "*", Operation: "*"}},
		}
		if err := s.AddToken(ctx, id, tok, store.HashToken("mg_"+randHex(t, 8))); err != nil {
			t.Fatalf("AddToken error = %v, want nil", err)
		}
		err = s.AppendAudit(ctx, id, func(prev string, seq int64) store.AuditEvent {
			return store.AuditEvent{
				OrgID: id, Seq: seq, ID: "e" + randHex(t, 4), PrevHash: prev, Hash: "h" + randHex(t, 4),
				Operation: "x", Outcome: "allowed", PrincipalKind: "human",
			}
		})
		if err != nil {
			t.Fatalf("AppendAudit error = %v, want nil", err)
		}
	}
	a, b := orgs[0], orgs[1]

	app := env.rawDB(t, env.appDSN)
	empty, nonexistent := "", randHex(t, 16)
	tables := []string{"tokens", "token_scopes", "audit_events"}
	for _, setting := range []*string{nil, &empty, &nonexistent} {
		for _, tbl := range tables {
			if got := orgIDsUnder(t, app, setting, tbl); len(got) != 0 {
				t.Errorf("unscoped SELECT org_id FROM %s = %v, want no rows", tbl, got)
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
				t.Errorf("app.org_id=A: SELECT org_id FROM %s returned org %q, want only %q (B is %q)", tbl, id, a, b)
			}
		}
	}

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
		_, err = tx.ExecContext(ctx, `INSERT INTO audit_events (org_id, seq, id, at, principal_kind, user_id, token_id, operation, outcome, reason, prev_hash, hash)
			VALUES ($1, 100, 'x', 'now', 'human', '', '', 'x', 'allowed', '', '', 'h')`, tc.org)
		if gotErr := err != nil; gotErr != tc.wantErr {
			t.Errorf("INSERT audit_events org_id=%s under app.org_id=A: error = %v, want error=%v", tc.org, err, tc.wantErr)
		}
		_ = tx.Rollback()
	}
}

func TestTokenAuditTablesHaveRLS(t *testing.T) {
	env := newPGEnv(t, true)
	ctx := context.Background()
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
	for _, tbl := range []string{"tokens", "token_scopes", "audit_events"} {
		if !rls[tbl] {
			t.Errorf("table %q row level security = %v, want true", tbl, rls[tbl])
		}
	}
}
