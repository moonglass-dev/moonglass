// Package pg is the hosted storage backend (Postgres through pgx).
package pg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver

	"moonglass.dev/moonglass/internal/store"
	"moonglass.dev/moonglass/internal/store/migrations"
)

const uniqueViolation = "23505"

// Open connects as the application role and returns the store. It never
// migrates: the application role does not own the tables.
func Open(ctx context.Context, appDSN string) (store.Store, error) {
	db, err := sql.Open("pgx", appDSN)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	if err = db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect to postgres as the application role: %w", err)
	}
	var unsafe bool
	err = db.QueryRowContext(ctx, `SELECT r.rolsuper OR r.rolbypassrls
			OR EXISTS (SELECT 1 FROM pg_tables t WHERE t.schemaname = 'public' AND t.tableowner = r.rolname)
		FROM pg_roles r WHERE r.rolname = current_user`).Scan(&unsafe)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("check the application role: %w", err)
	}
	if unsafe {
		_ = db.Close()
		return nil, errors.New("the application role must not be a superuser, have BYPASSRLS, or own tables: row level security would not apply to it")
	}
	return store.NewSQLStore(db, store.Dialect{
		SetOrg:      setOrg,
		IsDuplicate: isDuplicate,
	}), nil
}

// Migrate connects as the table-owning role, applies the numbered migrations,
// then grants appRole data access (no DDL) on every table except
// schema_migrations. It is idempotent.
func Migrate(ctx context.Context, ownerDSN, appRole string) error {
	db, err := sql.Open("pgx", ownerDSN)
	if err != nil {
		return fmt.Errorf("open postgres: %w", err)
	}
	defer func() { _ = db.Close() }()
	var unsafe bool
	err = db.QueryRowContext(ctx, `SELECT r.rolsuper OR r.rolbypassrls OR r.rolname = current_user
		FROM pg_roles r WHERE r.rolname = $1`, appRole).Scan(&unsafe)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check the application role: %w", err)
	}
	if unsafe {
		return fmt.Errorf("application role %q must not be the migration role, a superuser, or have BYPASSRLS", appRole)
	}
	if err = store.Migrate(ctx, db, migrations.PG()); err != nil {
		return fmt.Errorf("migrate postgres as the owner role: %w", err)
	}
	rows, err := db.QueryContext(ctx, `SELECT tablename FROM pg_tables
		WHERE schemaname = 'public' AND tablename <> 'schema_migrations' ORDER BY tablename`)
	if err != nil {
		return fmt.Errorf("list tables to grant: %w", err)
	}
	var tables []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan table name: %w", err)
		}
		tables = append(tables, t)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("list tables to grant: %w", err)
	}
	_ = rows.Close()
	role := pgx.Identifier{appRole}.Sanitize()
	for _, t := range tables {
		//nolint:gosec // table and role names are quoted with pgx.Identifier.Sanitize; DDL cannot take placeholders
		stmt := "GRANT SELECT, INSERT, UPDATE, DELETE ON " + pgx.Identifier{t}.Sanitize() + " TO " + role
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("grant data access on %s to %s: %w", t, appRole, err)
		}
	}
	return nil
}

func setOrg(ctx context.Context, tx *sql.Tx, orgID string) error {
	_, err := tx.ExecContext(ctx, "SELECT set_config('app.org_id', $1, true)", orgID)
	return err
}

func isDuplicate(err error) bool {
	var e *pgconn.PgError
	return errors.As(err, &e) && e.Code == uniqueViolation
}
