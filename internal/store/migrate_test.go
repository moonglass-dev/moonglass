package store_test

import (
	"context"
	"os"
	"reflect"
	"testing"
	"testing/fstest"

	"moonglass.dev/moonglass/internal/store"
	"moonglass.dev/moonglass/internal/store/pg"
)

type snapshot struct {
	Cols []col
	Rows map[string][]string
}

func snap(t *testing.T, cols func() []col, raw func(c []col) map[string][]string) snapshot {
	t.Helper()
	c := cols()
	return snapshot{Cols: c, Rows: raw(c)}
}

func TestMigrateIdempotent(t *testing.T) {
	ctx := context.Background()

	t.Run("sqlite", func(t *testing.T) {
		db := rawSQLite(t, sqlitePath(t))
		files := os.DirFS("migrations/sqlite")
		if err := store.Migrate(ctx, db, files); err != nil {
			t.Fatalf("first Migrate error = %v, want nil", err)
		}
		take := func() snapshot {
			return snap(t, func() []col { return sqliteSchema(t, db) }, func(c []col) map[string][]string { return dumpAll(t, db, c) })
		}
		before := take()
		if len(before.Rows["schema_migrations"]) == 0 {
			t.Fatalf("schema_migrations rows = %v, want at least one applied migration", before.Rows["schema_migrations"])
		}
		if err := store.Migrate(ctx, db, files); err != nil {
			t.Fatalf("second Migrate error = %v, want nil", err)
		}
		if after := take(); !reflect.DeepEqual(before, after) {
			t.Errorf("second Migrate changed state:\n before = %+v\n after  = %+v", before, after)
		}
	})

	t.Run("postgres", func(t *testing.T) {
		env := newPGEnv(t, true)
		super := env.rawDB(t, env.superDSN)
		take := func() snapshot {
			return snap(t, func() []col { return pgSchema(t, super) }, func(c []col) map[string][]string { return dumpAll(t, super, c) })
		}
		before := take()
		if len(before.Rows["schema_migrations"]) == 0 {
			t.Fatalf("schema_migrations rows = %v, want at least one applied migration", before.Rows["schema_migrations"])
		}
		if err := pg.Migrate(ctx, env.ownerDSN, env.appRole); err != nil {
			t.Fatalf("second pg.Migrate error = %v, want nil", err)
		}
		if after := take(); !reflect.DeepEqual(before, after) {
			t.Errorf("second pg.Migrate changed state:\n before = %+v\n after  = %+v", before, after)
		}
	})

	t.Run("changed or missing file errors", func(t *testing.T) {
		db := rawSQLite(t, sqlitePath(t))
		orig := fstest.MapFS{"0001_init.sql": {Data: []byte("CREATE TABLE t1 (a TEXT NOT NULL);")}}
		if err := store.Migrate(ctx, db, orig); err != nil {
			t.Fatalf("Migrate error = %v, want nil", err)
		}
		changed := fstest.MapFS{"0001_init.sql": {Data: []byte("CREATE TABLE t1 (a TEXT NOT NULL, b TEXT);")}}
		if err := store.Migrate(ctx, db, changed); err == nil {
			t.Error("Migrate with a changed applied file error = nil, want a checksum error")
		}
		missing := fstest.MapFS{"0002_next.sql": {Data: []byte("CREATE TABLE t2 (a TEXT NOT NULL);")}}
		if err := store.Migrate(ctx, db, missing); err == nil {
			t.Error("Migrate with an applied file missing error = nil, want an error")
		}
	})
}
