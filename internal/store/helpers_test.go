package store_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"moonglass.dev/moonglass/internal/store"
	"moonglass.dev/moonglass/internal/store/pg"
	"moonglass.dev/moonglass/internal/store/sqlite"
)

// col is one (table, column, nullability) triple of a described schema.
type col struct {
	Table    string
	Column   string
	Nullable bool
}

func (c col) String() string {
	return fmt.Sprintf("%s.%s nullable=%v", c.Table, c.Column, c.Nullable)
}

func randHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return hex.EncodeToString(b)
}

// sqlitePath returns a fresh database path in a temp dir.
func sqlitePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "mg.db")
}

// newSQLite opens a migrated SQLite store and a raw handle to the same file.
func newSQLite(t *testing.T) (store.Store, *sql.DB) {
	t.Helper()
	path := sqlitePath(t)
	s, err := sqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("sqlite.Open(%q) error = %v, want nil", path, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, rawSQLite(t, path)
}

func rawSQLite(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open(sqlite, %q) error = %v, want nil", path, err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// pgEnv is a disposable Postgres database with a separate owner and
// application role, created from the CI superuser URL.
type pgEnv struct {
	superDSN  string // superuser, connected to the disposable database
	ownerDSN  string
	appDSN    string
	ownerRole string
	appRole   string
}

// newPGEnv skips when MOONGLASS_TEST_POSTGRES is empty, except under CI where
// it fails. When migrate is true the schema is applied through pg.Migrate.
func newPGEnv(t *testing.T, migrate bool) pgEnv {
	t.Helper()
	base := os.Getenv("MOONGLASS_TEST_POSTGRES")
	if base == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("MOONGLASS_TEST_POSTGRES is empty but CI is set: CI must provide the postgres service")
		}
		t.Skip("MOONGLASS_TEST_POSTGRES is empty")
	}
	ctx := context.Background()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse MOONGLASS_TEST_POSTGRES: %v", err)
	}
	sfx := randHex(t, 6)
	dbName, owner, app := "mg_test_"+sfx, "mg_owner_"+sfx, "mg_app_"+sfx
	ownerPW, appPW := "pw"+randHex(t, 8), "pw"+randHex(t, 8)

	super, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatalf("sql.Open(pgx): %v", err)
	}
	t.Cleanup(func() {
		_, _ = super.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", dbName))
		_, _ = super.Exec("DROP ROLE IF EXISTS " + app)
		_, _ = super.Exec("DROP ROLE IF EXISTS " + owner)
		_ = super.Close()
	})
	for _, stmt := range []string{
		fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD '%s' NOSUPERUSER NOBYPASSRLS", owner, ownerPW),
		fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD '%s' NOSUPERUSER NOBYPASSRLS", app, appPW),
		fmt.Sprintf("CREATE DATABASE %s OWNER %s", dbName, owner),
	} {
		if _, err := super.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("setup %q: %v", stmt, err)
		}
	}
	dsn := func(user *url.Userinfo) string {
		c := *u
		c.Path = "/" + dbName
		if user != nil {
			c.User = user
		}
		return c.String()
	}
	env := pgEnv{
		superDSN:  dsn(nil),
		ownerDSN:  dsn(url.UserPassword(owner, ownerPW)),
		appDSN:    dsn(url.UserPassword(app, appPW)),
		ownerRole: owner,
		appRole:   app,
	}
	if migrate {
		if err := pg.Migrate(ctx, env.ownerDSN, env.appRole); err != nil {
			t.Fatalf("pg.Migrate error = %v, want nil", err)
		}
	}
	return env
}

func (e pgEnv) rawDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("sql.Open(pgx): %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func (e pgEnv) openApp(t *testing.T) store.Store {
	t.Helper()
	s, err := pg.Open(context.Background(), e.appDSN)
	if err != nil {
		t.Fatalf("pg.Open(app) error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func sqliteSchema(t *testing.T, db *sql.DB) []col {
	t.Helper()
	return queryCols(t, db, `SELECT m.name, p.name, p."notnull" = 0
		FROM sqlite_master m JOIN pragma_table_info(m.name) p
		WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite_%'`)
}

func pgSchema(t *testing.T, db *sql.DB) []col {
	t.Helper()
	return queryCols(t, db, `SELECT table_name, column_name, is_nullable = 'YES'
		FROM information_schema.columns WHERE table_schema = 'public'`)
}

func queryCols(t *testing.T, db *sql.DB, q string) []col {
	t.Helper()
	rows, err := db.Query(q)
	if err != nil {
		t.Fatalf("schema query: %v", err)
	}
	defer rows.Close()
	var out []col
	for rows.Next() {
		var c col
		var nullable any
		if err := rows.Scan(&c.Table, &c.Column, &nullable); err != nil {
			t.Fatalf("scan: %v", err)
		}
		switch v := nullable.(type) {
		case bool:
			c.Nullable = v
		case int64:
			c.Nullable = v != 0
		default:
			t.Fatalf("unexpected nullable type %T", nullable)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

func tableNames(cols []col) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range cols {
		if !seen[c.Table] {
			seen[c.Table] = true
			out = append(out, c.Table)
		}
	}
	sort.Strings(out)
	return out
}

// dumpAll returns every row of every table as sorted strings, keyed by table.
func dumpAll(t *testing.T, db *sql.DB, cols []col) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, table := range tableNames(cols) {
		rows, err := db.Query(`SELECT * FROM "` + table + `"`)
		if err != nil {
			t.Fatalf("select * from %s: %v", table, err)
		}
		names, _ := rows.Columns()
		out[table] = []string{}
		for rows.Next() {
			vals := make([]any, len(names))
			ptrs := make([]any, len(names))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatalf("scan %s: %v", table, err)
			}
			cells := make([]string, len(vals))
			for i, v := range vals {
				if b, ok := v.([]byte); ok {
					v = string(b)
				}
				cells[i] = fmt.Sprint(v)
			}
			out[table] = append(out[table], strings.Join(cells, "|"))
		}
		_ = rows.Close()
		sort.Strings(out[table])
	}
	return out
}

func diffCols(a, b []col, an, bn string) string {
	in := func(set []col) map[string]bool {
		m := map[string]bool{}
		for _, c := range set {
			m[c.String()] = true
		}
		return m
	}
	am, bm := in(a), in(b)
	var sb strings.Builder
	for _, c := range a {
		if !bm[c.String()] {
			fmt.Fprintf(&sb, "\n  only in %s: %s", an, c)
		}
	}
	for _, c := range b {
		if !am[c.String()] {
			fmt.Fprintf(&sb, "\n  only in %s: %s", bn, c)
		}
	}
	return sb.String()
}

func sameStrings(got, want []string) bool {
	g := append([]string(nil), got...)
	w := append([]string(nil), want...)
	sort.Strings(g)
	sort.Strings(w)
	return strings.Join(g, ",") == strings.Join(w, ",")
}
