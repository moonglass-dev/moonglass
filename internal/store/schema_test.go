package store_test

import (
	"strings"
	"testing"

	"moonglass.dev/moonglass/internal/store"
)

func TestSchemasMatch(t *testing.T) {
	env := newPGEnv(t, true)
	_, sdb := newSQLite(t)
	pdb := env.rawDB(t, env.superDSN)

	sc, pc := sqliteSchema(t, sdb), pgSchema(t, pdb)
	if len(sc) == 0 || len(pc) == 0 {
		t.Fatalf("empty schema: sqlite columns = %d, postgres columns = %d, want both > 0", len(sc), len(pc))
	}
	if d := diffCols(sc, pc, "sqlite", "postgres"); d != "" {
		t.Errorf("schemas differ (table.column nullable):%s", d)
	}
}

func TestLookupTablesMatchSchema(t *testing.T) {
	want := []string{"agent_keys", "enrolment_token_hashes", "token_hashes", "user_identities", "user_orgs"}
	if !sameStrings(store.LookupTables, want) {
		t.Errorf("store.LookupTables = %v, want %v", store.LookupTables, want)
	}

	env := newPGEnv(t, true)
	_, sdb := newSQLite(t)
	backends := []struct {
		name string
		cols []col
	}{
		{"sqlite", sqliteSchema(t, sdb)},
		{"postgres", pgSchema(t, env.rawDB(t, env.superDSN))},
	}
	lookup := map[string]bool{}
	for _, l := range store.LookupTables {
		lookup[l] = true
	}
	denyExact := map[string]bool{"name": true, "secret": true, "wrapped_key": true, "token": true, "plaintext": true, "scope": true, "scopes": true}
	denySub := []string{"secret", "wrapped", "plaintext", "scope"}

	t.Run("postgres rls", func(t *testing.T) {
		rows, err := env.rawDB(t, env.superDSN).Query(`SELECT c.relname, c.relrowsecurity
			FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p') AND c.relname <> 'schema_migrations'`)
		if err != nil {
			t.Fatalf("query pg_class: %v", err)
		}
		defer rows.Close()
		var bad []string
		for rows.Next() {
			var name string
			var rls bool
			if err := rows.Scan(&name, &rls); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if !rls && !lookup[name] {
				bad = append(bad, name)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("rows: %v", err)
		}
		if len(bad) > 0 {
			t.Errorf("tables with neither row-level security nor a store.LookupTables entry: %v, want none", bad)
		}
	})

	for _, b := range backends {
		t.Run(b.name, func(t *testing.T) {
			hasOrg := map[string]bool{}
			for _, c := range b.cols {
				if c.Column == "org_id" {
					hasOrg[c.Table] = true
				}
			}
			tables := tableNames(b.cols)
			for l := range lookup {
				if !sameStrings(filter(tables, l), []string{l}) {
					t.Errorf("lookup table %q missing from schema; tables = %v", l, tables)
				}
			}
			for _, tbl := range tables {
				if lookup[tbl] || tbl == "schema_migrations" {
					continue
				}
				if !hasOrg[tbl] {
					t.Errorf("non-lookup table %q has no org_id column, want one", tbl)
				}
			}
			for _, c := range b.cols {
				if !lookup[c.Table] {
					continue
				}
				bad := denyExact[c.Column]
				for _, s := range denySub {
					bad = bad || strings.Contains(c.Column, s)
				}
				if bad {
					t.Errorf("lookup table %q has forbidden column %q", c.Table, c.Column)
				}
			}
		})
	}
}

func filter(in []string, v string) []string {
	var out []string
	for _, s := range in {
		if s == v {
			out = append(out, s)
		}
	}
	return out
}
