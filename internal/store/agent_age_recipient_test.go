package store_test

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"

	"moonglass.dev/moonglass/internal/store"
	"moonglass.dev/moonglass/internal/store/migrations"
	"moonglass.dev/moonglass/internal/store/sqlite"
)

// testRecipient returns a value in the age recipient shape for the store's
// text column. The store copies the string and does not parse it.
func testRecipient(t *testing.T) string {
	t.Helper()
	return "age1" + randHex(t, 16)
}

func TestServerAgeRecipientConformanceSQLite(t *testing.T) {
	s, _ := newSQLite(t)
	runServerAgeRecipientConformance(t, s)
}

func TestServerAgeRecipientConformancePostgres(t *testing.T) {
	env := newPGEnv(t, true)
	runServerAgeRecipientConformance(t, env.openApp(t))
}

// runServerAgeRecipientConformance covers storing, refusing and reading back
// the agent's age recipient on servers, on both backends.
func runServerAgeRecipientConformance(t *testing.T, s store.Store) {
	ctx := context.Background()
	newOrg := func(t *testing.T) string {
		t.Helper()
		id, err := s.CreateOrg(ctx)
		if err != nil {
			t.Fatalf("CreateOrg error = %v, want nil", err)
		}
		return id
	}

	t.Run("round trip", func(t *testing.T) {
		org, pub, rec := newOrg(t), "pk-"+randHex(t, 16), testRecipient(t)

		srv, err := s.AddServer(ctx, org, pub, rec)
		if err != nil {
			t.Fatalf("AddServer error = %v, want nil", err)
		}
		if srv.AgeRecipient != rec {
			t.Errorf("AddServer AgeRecipient = %q, want %q", srv.AgeRecipient, rec)
		}

		got, err := s.ServerByPublicKey(ctx, pub)
		if err != nil {
			t.Fatalf("ServerByPublicKey(%q) error = %v, want nil", pub, err)
		}
		if got.AgeRecipient != rec {
			t.Errorf("ServerByPublicKey(%q) AgeRecipient = %q, want %q", pub, got.AgeRecipient, rec)
		}

		list, err := s.Servers(ctx, org)
		if err != nil {
			t.Fatalf("Servers error = %v, want nil", err)
		}
		if len(list) != 1 || list[0].AgeRecipient != rec {
			t.Errorf("Servers = %+v, want exactly one server with AgeRecipient %q", list, rec)
		}
	})

	t.Run("two servers keep their own", func(t *testing.T) {
		org := newOrg(t)
		pub1, pub2 := "pk-"+randHex(t, 16), "pk-"+randHex(t, 16)
		rec1, rec2 := testRecipient(t), testRecipient(t)

		srv1, err := s.AddServer(ctx, org, pub1, rec1)
		if err != nil {
			t.Fatalf("AddServer(%q) error = %v, want nil", pub1, err)
		}
		srv2, err := s.AddServer(ctx, org, pub2, rec2)
		if err != nil {
			t.Fatalf("AddServer(%q) error = %v, want nil", pub2, err)
		}

		list, err := s.Servers(ctx, org)
		if err != nil {
			t.Fatalf("Servers error = %v, want nil", err)
		}
		if len(list) != 2 {
			t.Fatalf("Servers len = %d, want 2: %+v", len(list), list)
		}
		wantByID := map[string]string{srv1.ID: rec1, srv2.ID: rec2}
		for _, srv := range list {
			want, ok := wantByID[srv.ID]
			if !ok {
				t.Errorf("Servers entry id %q is not one of %q or %q", srv.ID, srv1.ID, srv2.ID)
				continue
			}
			if srv.AgeRecipient != want {
				t.Errorf("Servers entry %s AgeRecipient = %q, want %q", srv.ID, srv.AgeRecipient, want)
			}
		}

		for _, tc := range []struct {
			pub  string
			want string
		}{{pub1, rec1}, {pub2, rec2}} {
			got, err := s.ServerByPublicKey(ctx, tc.pub)
			if err != nil {
				t.Fatalf("ServerByPublicKey(%q) error = %v, want nil", tc.pub, err)
			}
			if got.AgeRecipient != tc.want {
				t.Errorf("ServerByPublicKey(%q) AgeRecipient = %q, want %q", tc.pub, got.AgeRecipient, tc.want)
			}
		}
	})

	t.Run("empty refused", func(t *testing.T) {
		org := newOrg(t)
		first, err := s.AddServer(ctx, org, "pk-"+randHex(t, 16), testRecipient(t))
		if err != nil {
			t.Fatalf("AddServer error = %v, want nil", err)
		}

		before, err := s.Servers(ctx, org)
		if err != nil {
			t.Fatalf("Servers error = %v, want nil", err)
		}
		if len(before) != 1 || before[0].ID != first.ID {
			t.Fatalf("Servers before the refused call = %+v, want exactly server %q", before, first.ID)
		}

		pub := "pk-" + randHex(t, 16)
		if _, err := s.AddServer(ctx, org, pub, ""); err == nil {
			t.Errorf("AddServer with an empty age recipient error = nil, want an error")
		}

		after, err := s.Servers(ctx, org)
		if err != nil {
			t.Fatalf("Servers after the refused call error = %v, want nil", err)
		}
		if len(after) != len(before) {
			t.Fatalf("Servers after the refused call len = %d, want %d: %+v", len(after), len(before), after)
		}
		for i := range before {
			if after[i].ID != before[i].ID || after[i].AgeRecipient != before[i].AgeRecipient {
				t.Errorf("Servers after the refused call entry %d = id %q recipient %q, want id %q recipient %q",
					i, after[i].ID, after[i].AgeRecipient, before[i].ID, before[i].AgeRecipient)
			}
		}

		if _, err := s.ServerByPublicKey(ctx, pub); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("ServerByPublicKey(%q) after the refused call error = %v, want %v", pub, err, store.ErrNotFound)
		}
	})

	t.Run("org isolation", func(t *testing.T) {
		a, b := newOrg(t), newOrg(t)
		recA, recB := testRecipient(t), testRecipient(t)

		srvA, err := s.AddServer(ctx, a, "pk-"+randHex(t, 16), recA)
		if err != nil {
			t.Fatalf("AddServer in org %s error = %v, want nil", a, err)
		}
		srvB, err := s.AddServer(ctx, b, "pk-"+randHex(t, 16), recB)
		if err != nil {
			t.Fatalf("AddServer in org %s error = %v, want nil", b, err)
		}

		list, err := s.Servers(ctx, b)
		if err != nil {
			t.Fatalf("Servers(org %s) error = %v, want nil", b, err)
		}
		if len(list) != 1 || list[0].ID != srvB.ID {
			t.Errorf("Servers(org %s) = %+v, want exactly server %q", b, list, srvB.ID)
		}
		for _, srv := range list {
			if srv.ID == srvA.ID {
				t.Errorf("Servers(org %s) holds org %s's server %q", b, a, srvA.ID)
			}
			if srv.AgeRecipient == recA {
				t.Errorf("Servers(org %s) holds org %s's recipient %q", b, a, recA)
			}
		}
	})
}

func TestServerAgeRecipientMigration(t *testing.T) {
	ctx := context.Background()
	path := sqlitePath(t)
	db := rawSQLite(t, path)

	full := migrations.SQLite()
	entries, err := fs.ReadDir(full, ".")
	if err != nil {
		t.Fatalf("read embedded sqlite migrations: %v", err)
	}
	partial := fstest.MapFS{}
	var names []string
	for _, e := range entries {
		if e.IsDir() || e.Name() >= "0007" {
			continue
		}
		data, err := fs.ReadFile(full, e.Name())
		if err != nil {
			t.Fatalf("read migration %s: %v", e.Name(), err)
		}
		partial[e.Name()] = &fstest.MapFile{Data: data}
		names = append(names, e.Name())
	}
	wantFiles := []string{
		"0001_init.sql",
		"0002_tokens_audit.sql",
		"0003_projects.sql",
		"0004_releases.sql",
		"0005_sessions.sql",
		"0006_rollbacks.sql",
	}
	if !sameStrings(names, wantFiles) {
		t.Fatalf("migrations before 0007 = %v, want %v", names, wantFiles)
	}
	if err := store.Migrate(ctx, db, partial); err != nil {
		t.Fatalf("Migrate through 0006 error = %v, want nil", err)
	}

	orgID, srvID, pub := randHex(t, 16), randHex(t, 16), "pk-"+randHex(t, 16)
	now := time.Now().UTC().Format(time.RFC3339)
	for _, stmt := range []struct {
		what  string
		query string
		args  []any
	}{
		{"orgs", `INSERT INTO orgs (id, org_id, created_at) VALUES (?, ?, ?)`, []any{orgID, orgID, now}},
		{"servers", `INSERT INTO servers (id, org_id, created_at) VALUES (?, ?, ?)`, []any{srvID, orgID, now}},
		{"agent_keys", `INSERT INTO agent_keys (public_key, server_id, org_id) VALUES (?, ?, ?)`, []any{pub, srvID, orgID}},
	} {
		if _, err := db.ExecContext(ctx, stmt.query, stmt.args...); err != nil {
			t.Fatalf("insert %s row error = %v, want nil", stmt.what, err)
		}
	}

	if err := store.Migrate(ctx, db, full); err != nil {
		t.Fatalf("Migrate to the full set error = %v, want nil", err)
	}

	var recipient string
	if err := db.QueryRowContext(ctx, "SELECT agent_age_recipient FROM servers WHERE id = ?", srvID).Scan(&recipient); err != nil {
		t.Fatalf("select agent_age_recipient error = %v, want nil", err)
	}
	if recipient != "" {
		t.Errorf("agent_age_recipient of a server from before 0007 = %q, want %q", recipient, "")
	}

	st, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("sqlite.Open(%q) error = %v, want nil", path, err)
	}
	t.Cleanup(func() { _ = st.Close() })
	srv, err := st.ServerByPublicKey(ctx, pub)
	if err != nil {
		t.Fatalf("ServerByPublicKey(%q) error = %v, want nil", pub, err)
	}
	if srv.ID != srvID || srv.AgeRecipient != "" {
		t.Errorf("ServerByPublicKey(%q) = id %q recipient %q, want id %q recipient %q",
			pub, srv.ID, srv.AgeRecipient, srvID, "")
	}

	_, err = db.ExecContext(ctx,
		`INSERT INTO servers (id, org_id, created_at, agent_age_recipient) VALUES (?, ?, ?, NULL)`,
		randHex(t, 16), orgID, now)
	if err == nil {
		t.Errorf("raw INSERT of a NULL agent_age_recipient error = nil, want a NOT NULL constraint error")
	}
}
