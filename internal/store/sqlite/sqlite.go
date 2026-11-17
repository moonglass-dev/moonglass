// Package sqlite is the self-hosted storage backend (modernc.org/sqlite, no cgo).
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"modernc.org/sqlite"

	"moonglass.dev/moonglass/internal/store"
	"moonglass.dev/moonglass/internal/store/migrations"
)

// SQLite extended result codes for unique and primary key violations.
const (
	codeConstraintPrimaryKey = 1555
	codeConstraintUnique     = 2067
)

// Open opens the database at path, applies migrations and returns the store.
func Open(ctx context.Context, path string) (store.Store, error) {
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}
	// One connection serialises writers, so a transaction never meets SQLITE_BUSY from itself.
	db.SetMaxOpenConns(1)
	if err := store.Migrate(ctx, db, migrations.SQLite()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate sqlite %q: %w", path, err)
	}
	return store.NewSQLStore(db, store.Dialect{
		IsDuplicate: isDuplicate,
		Rebind:      store.RebindQuestion,
	}), nil
}

func isDuplicate(err error) bool {
	var e *sqlite.Error
	if !errors.As(err, &e) {
		return false
	}
	return e.Code() == codeConstraintPrimaryKey || e.Code() == codeConstraintUnique
}
