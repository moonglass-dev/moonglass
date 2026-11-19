package store_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"moonglass.dev/moonglass/internal/store"
)

func TestRollbacksConformanceSQLite(t *testing.T) {
	s, _ := newSQLite(t)
	runRollbacksConformance(t, s)
}

func TestRollbacksConformancePostgres(t *testing.T) {
	runRollbacksConformance(t, newPGEnv(t, true).openApp(t))
}

// mustStartRollback starts a rollback at at(i) and fails the test on error.
func mustStartRollback(t *testing.T, s store.Store, orgID, serviceID, requestID string, i int) store.Rollback {
	t.Helper()
	rb, err := s.StartRollback(context.Background(), orgID, serviceID, requestID, at(i))
	if err != nil {
		t.Fatalf("StartRollback(service %s, at %d) in org %s error = %v, want nil", serviceID, i, orgID, err)
	}
	return rb
}

func rollbackIDs(rs []store.Rollback) []string {
	out := make([]string, len(rs))
	for i, rb := range rs {
		out[i] = rb.ID
	}
	return out
}

// runRollbacksConformance covers starting, finishing and reading rollbacks,
// including the org and server isolation of the in-flight read.
func runRollbacksConformance(t *testing.T, s store.Store) {
	ctx := context.Background()

	t.Run("start", func(t *testing.T) {
		tgt := seedTarget(t, s)
		svc := seedService(t, s, tgt, tgt.server, "web")
		reqID := "c" + randHex(t, 16)
		rb := mustStartRollback(t, s, tgt.orgID, svc.ID, reqID, 0)

		want := store.Rollback{
			OrgID: tgt.orgID, ID: rb.ID, ServiceID: svc.ID, RequestID: reqID,
			Status: store.ReleaseRunning, Detail: "",
			CreatedAt: at(0).Format(time.RFC3339), FinishedAt: "",
		}
		if !releaseIDPattern.MatchString(rb.ID) {
			t.Errorf("StartRollback id = %q, want 32 lowercase hex characters", rb.ID)
		}
		if !reflect.DeepEqual(rb, want) {
			t.Errorf("StartRollback = %+v, want %+v", rb, want)
		}

		list, err := s.InFlightRollbacks(ctx, tgt.orgID, tgt.server)
		if err != nil {
			t.Fatalf("InFlightRollbacks error = %v, want nil", err)
		}
		if len(list) != 1 || !reflect.DeepEqual(list[0], rb) {
			t.Errorf("InFlightRollbacks = %+v, want exactly the started rollback %+v", list, rb)
		}
	})

	t.Run("start refused", func(t *testing.T) {
		a, b := seedTarget(t, s), seedTarget(t, s)
		bSvc := seedService(t, s, b, b.server, "web")
		for _, svcID := range []string{randHex(t, 16), bSvc.ID} {
			if _, err := s.StartRollback(ctx, a.orgID, svcID, "c"+randHex(t, 16), at(0)); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("StartRollback(service %s) in org %s error = %v, want ErrNotFound", svcID, a.orgID, err)
			}
		}
		for _, org := range []orgTarget{a, b} {
			list, err := s.InFlightRollbacks(ctx, org.orgID, org.server)
			if err != nil || len(list) != 0 {
				t.Errorf("InFlightRollbacks(org %s, server %s) after the refused starts = %+v, %v, want no rows, nil",
					org.orgID, org.server, list, err)
			}
		}
	})

	t.Run("finish", func(t *testing.T) {
		tgt := seedTarget(t, s)
		for _, st := range []store.ReleaseStatus{store.ReleaseSucceeded, store.ReleaseFailed, store.ReleaseRejected} {
			svc := seedService(t, s, tgt, tgt.server, "fin-"+string(st))
			rb := mustStartRollback(t, s, tgt.orgID, svc.ID, "c"+randHex(t, 16), 0)

			detail := "detail " + string(st)
			got, err := s.FinishRollback(ctx, tgt.orgID, rb.ID, st, detail, at(5))
			if err != nil {
				t.Fatalf("FinishRollback(%s) error = %v, want nil", st, err)
			}
			want := rb
			want.Status, want.Detail, want.FinishedAt = st, detail, at(5).Format(time.RFC3339)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("FinishRollback(%s) = %+v, want %+v", st, got, want)
			}

			if _, err := s.FinishRollback(ctx, tgt.orgID, rb.ID, store.ReleaseFailed, "again", at(6)); !errors.Is(err, store.ErrRollbackState) {
				t.Errorf("second FinishRollback(%s) error = %v, want ErrRollbackState", st, err)
			}
			list, err := s.InFlightRollbacks(ctx, tgt.orgID, tgt.server)
			if err != nil {
				t.Fatalf("InFlightRollbacks after finishing (%s) error = %v, want nil", st, err)
			}
			if slices.Contains(rollbackIDs(list), rb.ID) {
				t.Errorf("InFlightRollbacks after finishing (%s) = %v, want the finished rollback %s left out", st, rollbackIDs(list), rb.ID)
			}
		}
	})

	t.Run("finish non-final status", func(t *testing.T) {
		tgt := seedTarget(t, s)
		for _, st := range []store.ReleaseStatus{store.ReleaseQueued, store.ReleaseRunning, store.ReleaseSuperseded} {
			svc := seedService(t, s, tgt, tgt.server, "hold-"+string(st))
			rb := mustStartRollback(t, s, tgt.orgID, svc.ID, "c"+randHex(t, 16), 0)

			_, err := s.FinishRollback(ctx, tgt.orgID, rb.ID, st, "nope", at(1))
			checkNames(t, "FinishRollback with status "+string(st), err, "succeeded", "failed", "rejected")
			if errors.Is(err, store.ErrRollbackState) {
				t.Errorf("FinishRollback with status %q error = %v, want an error that is not ErrRollbackState", st, err)
			}

			list, err := s.InFlightRollbacks(ctx, tgt.orgID, tgt.server)
			if err != nil {
				t.Fatalf("InFlightRollbacks after the refused finish (%s) error = %v, want nil", st, err)
			}
			if !slices.Contains(rollbackIDs(list), rb.ID) {
				t.Errorf("InFlightRollbacks after the refused finish (%s) = %v, want the rollback %s still running", st, rollbackIDs(list), rb.ID)
			}
		}
	})

	t.Run("in flight", func(t *testing.T) {
		tgt := seedTarget(t, s)
		srv2, err := s.AddServer(ctx, tgt.orgID, "pub-"+randHex(t, 16))
		if err != nil {
			t.Fatalf("AddServer error = %v, want nil", err)
		}
		a1 := seedService(t, s, tgt, tgt.server, "a1")
		a2 := seedService(t, s, tgt, tgt.server, "a2")
		a3 := seedService(t, s, tgt, tgt.server, "a3")
		c := seedService(t, s, tgt, srv2.ID, "c")

		r3 := mustStartRollback(t, s, tgt.orgID, a1.ID, "c"+randHex(t, 16), 3)
		r1 := mustStartRollback(t, s, tgt.orgID, a2.ID, "c"+randHex(t, 16), 1)
		r2 := mustStartRollback(t, s, tgt.orgID, a3.ID, "c"+randHex(t, 16), 2)
		rc := mustStartRollback(t, s, tgt.orgID, c.ID, "c"+randHex(t, 16), 0)
		finished := mustStartRollback(t, s, tgt.orgID, a1.ID, "c"+randHex(t, 16), 4)
		if _, err := s.FinishRollback(ctx, tgt.orgID, finished.ID, store.ReleaseSucceeded, "ok", at(5)); err != nil {
			t.Fatalf("FinishRollback error = %v, want nil", err)
		}

		got, err := s.InFlightRollbacks(ctx, tgt.orgID, tgt.server)
		want := []string{r1.ID, r2.ID, r3.ID}
		if err != nil || !slices.Equal(rollbackIDs(got), want) {
			t.Errorf("InFlightRollbacks(server 1) = %v, %v, want %v, nil (oldest first; server 2's %s and the finished %s left out)",
				rollbackIDs(got), err, want, rc.ID, finished.ID)
		}

		gotC, err := s.InFlightRollbacks(ctx, tgt.orgID, srv2.ID)
		if wantC := []string{rc.ID}; err != nil || !slices.Equal(rollbackIDs(gotC), wantC) {
			t.Errorf("InFlightRollbacks(server 2) = %v, %v, want %v, nil", rollbackIDs(gotC), err, wantC)
		}

		none, err := s.InFlightRollbacks(ctx, tgt.orgID, randHex(t, 16))
		if err != nil || len(none) != 0 {
			t.Errorf("InFlightRollbacks(unknown server) = %+v, %v, want no rows, nil", none, err)
		}
	})

	t.Run("second org", func(t *testing.T) {
		a, b := seedTarget(t, s), seedTarget(t, s)
		svc := seedService(t, s, a, a.server, "web")
		rb := mustStartRollback(t, s, a.orgID, svc.ID, "c"+randHex(t, 16), 0)
		mustEnqueue(t, s, a.orgID, sampleRelease(svc.ID), 0)

		if _, err := s.FinishRollback(ctx, b.orgID, rb.ID, store.ReleaseFailed, "x", at(5)); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("FinishRollback by org %s on org %s's rollback error = %v, want ErrNotFound", b.orgID, a.orgID, err)
		}
		list, err := s.InFlightRollbacks(ctx, a.orgID, a.server)
		if err != nil || !slices.Equal(rollbackIDs(list), []string{rb.ID}) {
			t.Errorf("InFlightRollbacks(org %s) after org %s's finish = %v, %v, want the running rollback %s", a.orgID, b.orgID, rollbackIDs(list), err, rb.ID)
		}

		none, err := s.InFlightRollbacks(ctx, b.orgID, a.server)
		if err != nil || len(none) != 0 {
			t.Errorf("InFlightRollbacks(org %s, org %s's server) = %+v, %v, want no rows, nil", b.orgID, a.orgID, none, err)
		}
		releases, err := s.InFlightReleases(ctx, b.orgID, a.server)
		if err != nil || len(releases) != 0 {
			t.Errorf("InFlightReleases(org %s, org %s's server) = %+v, %v, want no rows, nil", b.orgID, a.orgID, releases, err)
		}
	})
}

func TestPostgresRLSOnRollbacks(t *testing.T) {
	env := newPGEnv(t, true)
	ctx := context.Background()
	s := env.openApp(t)

	var targets [2]orgTarget
	var rollbacks [2]store.Rollback
	for i := range targets {
		targets[i] = seedTarget(t, s)
		svc := seedService(t, s, targets[i], targets[i].server, "web")
		rollbacks[i] = mustStartRollback(t, s, targets[i].orgID, svc.ID, "c"+randHex(t, 16), i)
	}
	a, b := targets[0], targets[1]
	ra, rb := rollbacks[0], rollbacks[1]

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
		if got := orgIDsUnder(t, app, tc.setting, "rollbacks"); len(got) != 0 {
			t.Errorf("app.org_id %s: SELECT org_id FROM rollbacks (no WHERE) = %v, want no rows", tc.name, got)
		}
	}

	got := orgIDsUnder(t, app, &a.orgID, "rollbacks")
	if len(got) == 0 {
		t.Errorf("app.org_id=A: SELECT FROM rollbacks returned no rows, want A's rows")
	}
	for _, id := range got {
		if id != a.orgID {
			t.Errorf("app.org_id=A: SELECT org_id FROM rollbacks (no WHERE) returned org %q, want only %q (B is %q)", id, a.orgID, b.orgID)
		}
	}

	const insert = "INSERT INTO rollbacks (id, org_id, service_id, status, created_at) VALUES ($1, $2, $3, $4, $5)"
	now := at(0).Format(time.RFC3339)
	for _, tc := range []struct {
		name    string
		org     string
		service string
		wantErr bool
	}{
		{"another org's row", b.orgID, rb.ServiceID, true},
		{"own row", a.orgID, ra.ServiceID, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := app.BeginTx(ctx, nil)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer func() { _ = tx.Rollback() }()
			if _, err := tx.ExecContext(ctx, "SELECT set_config('app.org_id', $1, true)", a.orgID); err != nil {
				t.Fatalf("set_config: %v", err)
			}
			_, err = tx.ExecContext(ctx, insert, randHex(t, 16), tc.org, tc.service, string(store.ReleaseRunning), now)

			if !tc.wantErr {
				if err != nil {
					t.Errorf("control INSERT INTO rollbacks for org %s under app.org_id=%s error = %v, want nil", tc.org, a.orgID, err)
				}
				return
			}
			var pgErr pgError
			if !errors.As(err, &pgErr) || pgErr.SQLState() != "42501" {
				t.Errorf("INSERT INTO rollbacks for org %s under app.org_id=%s error = %v, want SQLSTATE %q", tc.org, a.orgID, err, "42501")
			}
		})
	}

	super := env.rawDB(t, env.superDSN)
	var rls bool
	if err := super.QueryRowContext(ctx, "SELECT rowsecurity FROM pg_tables WHERE schemaname = 'public' AND tablename = 'rollbacks'").Scan(&rls); err != nil {
		t.Fatalf("query pg_tables: %v", err)
	}
	if !rls {
		t.Errorf("table %q row level security = %v, want true", "rollbacks", rls)
	}
}
