package store_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"moonglass.dev/moonglass/internal/store"
)

func TestStepUpsConformanceSQLite(t *testing.T) {
	s, raw := newSQLite(t)
	runStepUpsConformance(t, s, raw)
}

func TestStepUpsConformancePostgres(t *testing.T) {
	env := newPGEnv(t, true)
	runStepUpsConformance(t, env.openApp(t), env.rawDB(t, env.superDSN))
}

// countStepUps returns the database's row count for step_ups, so a refused
// create is proven to write no row.
func countStepUps(t *testing.T, raw *sql.DB) int {
	t.Helper()
	var n int
	if err := raw.QueryRow("SELECT count(*) FROM step_ups").Scan(&n); err != nil {
		t.Fatalf("count step_ups: %v", err)
	}
	return n
}

// runStepUpsConformance covers creating a step-up bound to one user, one
// credential, one operation, one target and one input digest, reading it back
// inside its org, and refusing a row that breaks a binding rule.
func runStepUpsConformance(t *testing.T, s store.Store, raw *sql.DB) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	exp := now.Add(10 * time.Minute)

	newOrg := func(t *testing.T) string {
		t.Helper()
		id, err := s.CreateOrg(ctx)
		if err != nil {
			t.Fatalf("CreateOrg error = %v, want nil", err)
		}
		return id
	}
	newUser := func(t *testing.T) string {
		t.Helper()
		userID, err := s.UserForIdentity(ctx, "stepup:"+randHex(t, 8))
		if err != nil {
			t.Fatalf("UserForIdentity error = %v, want nil", err)
		}
		return userID
	}

	orgA, orgB := newOrg(t), newOrg(t)
	userID := newUser(t)

	tokenStepUp := func(t *testing.T) store.StepUp {
		t.Helper()
		return store.StepUp{
			UserID:        userID,
			TokenID:       randHex(t, 16),
			Operation:     "services.delete",
			ProjectID:     randHex(t, 16),
			EnvironmentID: randHex(t, 16),
			InputDigest:   randHex(t, 32),
		}
	}

	t.Run("token credential with target", func(t *testing.T) {
		in := tokenStepUp(t)
		got, err := s.CreateStepUp(ctx, orgA, in, now, exp)
		if err != nil {
			t.Fatalf("CreateStepUp error = %v, want nil", err)
		}
		if !isHex32(got.ID) {
			t.Errorf("CreateStepUp ID = %q, want 32 lowercase hex characters", got.ID)
		}
		want := in
		want.OrgID, want.ID = orgA, got.ID
		want.CreatedAt, want.ExpiresAt = now.Format(time.RFC3339), exp.Format(time.RFC3339)
		if got != want {
			t.Errorf("CreateStepUp = %+v, want %+v", got, want)
		}
		if !strings.HasSuffix(got.CreatedAt, "Z") || !strings.HasSuffix(got.ExpiresAt, "Z") {
			t.Errorf("CreateStepUp times = %q/%q, want UTC RFC3339 text ending in Z", got.CreatedAt, got.ExpiresAt)
		}
		if got.SignedInAt != "" || got.ApprovedAt != "" || got.DeclinedAt != "" || got.UsedAt != "" {
			t.Errorf("CreateStepUp markers = %q/%q/%q/%q, want all empty",
				got.SignedInAt, got.ApprovedAt, got.DeclinedAt, got.UsedAt)
		}

		read, err := s.StepUp(ctx, orgA, got.ID)
		if err != nil || read != got {
			t.Errorf("StepUp(%q) = %+v, %v, want %+v, nil", got.ID, read, err, got)
		}
	})

	t.Run("session credential, no target", func(t *testing.T) {
		in := store.StepUp{
			UserID:      userID,
			SessionID:   randHex(t, 16),
			Operation:   "services.delete",
			InputDigest: randHex(t, 32),
		}
		got, err := s.CreateStepUp(ctx, orgA, in, now, exp)
		if err != nil {
			t.Fatalf("CreateStepUp error = %v, want nil", err)
		}
		if got.TokenID != "" || got.ProjectID != "" || got.EnvironmentID != "" {
			t.Errorf("CreateStepUp with a session credential and no target = token %q project %q environment %q, want all empty",
				got.TokenID, got.ProjectID, got.EnvironmentID)
		}
		if got.SessionID != in.SessionID {
			t.Errorf("CreateStepUp SessionID = %q, want %q", got.SessionID, in.SessionID)
		}

		read, err := s.StepUp(ctx, orgA, got.ID)
		if err != nil || read != got {
			t.Errorf("StepUp(%q) = %+v, %v, want %+v, nil", got.ID, read, err, got)
		}
	})

	t.Run("caller fields ignored", func(t *testing.T) {
		const junk = "2000-01-01T00:00:00Z"
		in := tokenStepUp(t)
		in.ID, in.OrgID = "x", orgB
		in.CreatedAt, in.ExpiresAt = junk, junk
		in.SignedInAt, in.ApprovedAt, in.DeclinedAt, in.UsedAt = junk, junk, junk, junk

		got, err := s.CreateStepUp(ctx, orgA, in, now, exp)
		if err != nil {
			t.Fatalf("CreateStepUp error = %v, want nil", err)
		}
		if !isHex32(got.ID) {
			t.Errorf("CreateStepUp ID = %q, want a fresh 32 lowercase hex id", got.ID)
		}
		if got.ID == in.ID {
			t.Errorf("CreateStepUp ID = %q, want a fresh id, not the caller's %q", got.ID, in.ID)
		}
		if got.OrgID != orgA {
			t.Errorf("CreateStepUp OrgID = %q, want %q (not the caller's %q)", got.OrgID, orgA, in.OrgID)
		}
		want := in
		want.OrgID, want.ID = orgA, got.ID
		want.CreatedAt, want.ExpiresAt = now.Format(time.RFC3339), exp.Format(time.RFC3339)
		want.SignedInAt, want.ApprovedAt, want.DeclinedAt, want.UsedAt = "", "", "", ""
		if got != want {
			t.Errorf("CreateStepUp = %+v, want %+v", got, want)
		}

		read, err := s.StepUp(ctx, orgA, got.ID)
		if err != nil || read != got {
			t.Errorf("StepUp(%q) = %+v, %v, want %+v, nil", got.ID, read, err, got)
		}
		if _, err := s.StepUp(ctx, orgB, got.ID); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("StepUp in org %s of org %s's step-up error = %v, want %v", orgB, orgA, err, store.ErrNotFound)
		}
	})

	t.Run("refused", func(t *testing.T) {
		before := countStepUps(t, raw)
		for _, tc := range []struct {
			name   string
			mutate func(t *testing.T, su *store.StepUp)
			want   string
		}{
			{
				name: "both credentials",
				mutate: func(t *testing.T, su *store.StepUp) {
					t.Helper()
					su.SessionID = randHex(t, 16)
				},
				want: "exactly one of token id and session id",
			},
			{
				name: "neither credential",
				mutate: func(t *testing.T, su *store.StepUp) {
					t.Helper()
					su.TokenID = ""
				},
				want: "exactly one of token id and session id",
			},
			{
				name: "empty user",
				mutate: func(t *testing.T, su *store.StepUp) {
					t.Helper()
					su.UserID = ""
				},
				want: "user id is empty",
			},
			{
				name: "empty operation",
				mutate: func(t *testing.T, su *store.StepUp) {
					t.Helper()
					su.Operation = ""
				},
				want: "operation is empty",
			},
			{
				name: "empty input digest",
				mutate: func(t *testing.T, su *store.StepUp) {
					t.Helper()
					su.InputDigest = ""
				},
				want: "input digest is empty",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				su := tokenStepUp(t)
				tc.mutate(t, &su)
				if _, err := s.CreateStepUp(ctx, orgA, su, now, exp); err == nil {
					t.Fatalf("CreateStepUp(%s) error = nil, want an error naming %q", tc.name, tc.want)
				} else if !strings.Contains(err.Error(), tc.want) {
					t.Errorf("CreateStepUp(%s) error = %q, want it to name %q", tc.name, err, tc.want)
				} else if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrDuplicate) {
					t.Errorf("CreateStepUp(%s) error = %v, want an error that is neither ErrNotFound nor ErrDuplicate", tc.name, err)
				}
			})
		}
		if after := countStepUps(t, raw); after != before {
			t.Errorf("step_ups rows after the refused creates = %d, want %d unchanged", after, before)
		}
	})

	t.Run("unknown id", func(t *testing.T) {
		unknown := randHex(t, 16)
		if _, err := s.StepUp(ctx, orgA, unknown); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("StepUp(unknown id %q) error = %v, want %v", unknown, err, store.ErrNotFound)
		}
	})

	t.Run("another org", func(t *testing.T) {
		in := tokenStepUp(t)
		got, err := s.CreateStepUp(ctx, orgA, in, now, exp)
		if err != nil {
			t.Fatalf("CreateStepUp error = %v, want nil", err)
		}
		if _, err := s.StepUp(ctx, orgB, got.ID); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("StepUp(org %s, org %s's step-up) error = %v, want %v", orgB, orgA, err, store.ErrNotFound)
		}
		read, err := s.StepUp(ctx, orgA, got.ID)
		if err != nil || read != got {
			t.Errorf("StepUp(org %s) after org %s's read = %+v, %v, want %+v, nil", orgA, orgB, read, err, got)
		}
	})
}

func TestPostgresRLSOnStepUps(t *testing.T) {
	env := newPGEnv(t, true)
	ctx := context.Background()
	s := env.openApp(t)

	var orgs [2]string
	var users [2]string
	for i := range orgs {
		orgID, err := s.CreateOrg(ctx)
		if err != nil {
			t.Fatalf("CreateOrg error = %v, want nil", err)
		}
		orgs[i] = orgID
		userID, err := s.UserForIdentity(ctx, "stepup-rls:"+randHex(t, 8))
		if err != nil {
			t.Fatalf("UserForIdentity error = %v, want nil", err)
		}
		users[i] = userID
		in := store.StepUp{
			UserID:      userID,
			TokenID:     randHex(t, 16),
			Operation:   "services.delete",
			InputDigest: randHex(t, 32),
		}
		if _, err := s.CreateStepUp(ctx, orgID, in, at(0), at(600)); err != nil {
			t.Fatalf("CreateStepUp in org %s error = %v, want nil", orgID, err)
		}
	}
	a, b := orgs[0], orgs[1]

	app := env.rawDB(t, env.appDSN)
	empty, nonexistent := "", randHex(t, 16)
	for _, tc := range []struct {
		name    string
		setting *string
	}{
		{"unset", nil},
		{"empty", &empty},
		{"nonexistent org", &nonexistent},
	} {
		if got := orgIDsUnder(t, app, tc.setting, "step_ups"); len(got) != 0 {
			t.Errorf("app.org_id %s: SELECT org_id FROM step_ups (no WHERE) = %v, want no rows", tc.name, got)
		}
	}

	got := orgIDsUnder(t, app, &a, "step_ups")
	if len(got) == 0 {
		t.Errorf("app.org_id=A: SELECT FROM step_ups returned no rows, want A's rows")
	}
	for _, id := range got {
		if id != a {
			t.Errorf("app.org_id=A: SELECT org_id FROM step_ups (no WHERE) returned org %q, want only %q (B is %q)", id, a, b)
		}
	}

	const insert = `INSERT INTO step_ups (org_id, id, user_id, token_id, operation, input_digest, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	now := at(0).Format(time.RFC3339)
	for _, tc := range []struct {
		name    string
		org     string
		wantErr bool
	}{
		{"another org's row", b, true},
		{"own row", a, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := app.BeginTx(ctx, nil)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer func() { _ = tx.Rollback() }()
			if _, err := tx.ExecContext(ctx, "SELECT set_config('app.org_id', $1, true)", a); err != nil {
				t.Fatalf("set_config: %v", err)
			}
			_, err = tx.ExecContext(ctx, insert, tc.org, randHex(t, 16), users[0], randHex(t, 16),
				"services.delete", randHex(t, 32), now, now)

			if !tc.wantErr {
				if err != nil {
					t.Errorf("control INSERT INTO step_ups for org %s under app.org_id=%s error = %v, want nil", tc.org, a, err)
				}
				return
			}
			var pgErr pgError
			if !errors.As(err, &pgErr) || pgErr.SQLState() != "42501" {
				t.Errorf("INSERT INTO step_ups for org %s under app.org_id=%s error = %v, want SQLSTATE %q", tc.org, a, err, "42501")
			}
		})
	}

	super := env.rawDB(t, env.superDSN)
	var rls bool
	if err := super.QueryRowContext(ctx, "SELECT rowsecurity FROM pg_tables WHERE schemaname = 'public' AND tablename = 'step_ups'").Scan(&rls); err != nil {
		t.Fatalf("query pg_tables: %v", err)
	}
	if !rls {
		t.Errorf("table %q row level security = %v, want true", "step_ups", rls)
	}
}
