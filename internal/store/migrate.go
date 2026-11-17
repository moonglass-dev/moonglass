package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"time"
)

var migrationName = regexp.MustCompile(`^[0-9]{4}_[a-z0-9_]+\.sql$`)

type migrationFile struct {
	version  string
	checksum string
	sql      string
}

// Migrate applies the numbered SQL files in files that are not yet recorded in
// schema_migrations. Each file runs in its own transaction. A recorded file
// that changed or is missing is an error: never edit an applied migration,
// add a new numbered file instead.
func Migrate(ctx context.Context, db *sql.DB, files fs.FS) error {
	local, err := readMigrations(files)
	if err != nil {
		return err
	}
	if _, err = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT NOT NULL PRIMARY KEY,
		checksum TEXT NOT NULL,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w (migrations must run as the table-owning role)", err)
	}
	applied, err := appliedMigrations(ctx, db)
	if err != nil {
		return err
	}
	byVersion := make(map[string]migrationFile, len(local))
	for _, m := range local {
		byVersion[m.version] = m
	}
	for version, sum := range applied {
		m, ok := byVersion[version]
		if !ok {
			return fmt.Errorf("applied migration %s is missing from the migration files: restore it, never delete an applied migration", version)
		}
		if m.checksum != sum {
			return fmt.Errorf("applied migration %s changed (checksum %s, recorded %s): never edit an applied migration, add a new numbered file", version, m.checksum, sum)
		}
	}
	for _, m := range local {
		if _, done := applied[m.version]; done {
			continue
		}
		if err := applyMigration(ctx, db, m); err != nil {
			return err
		}
	}
	return nil
}

func readMigrations(files fs.FS) ([]migrationFile, error) {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return nil, fmt.Errorf("read migration files: %w", err)
	}
	var out []migrationFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !migrationName.MatchString(name) {
			return nil, fmt.Errorf("migration file %q does not match NNNN_name.sql: rename it", name)
		}
		data, err := fs.ReadFile(files, name)
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", name, err)
		}
		sum := sha256.Sum256(data)
		out = append(out, migrationFile{version: name, checksum: hex.EncodeToString(sum[:]), sql: string(data)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

func appliedMigrations(ctx context.Context, db *sql.DB) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, "SELECT version, checksum FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	applied := map[string]string{}
	for rows.Next() {
		var version, sum string
		if err := rows.Scan(&version, &sum); err != nil {
			return nil, fmt.Errorf("scan schema_migrations: %w", err)
		}
		applied[version] = sum
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	return applied, nil
}

// applyMigration runs one file and records it. The recorded values are a
// validated file name, a hex digest and a timestamp, so they are written as
// literals and the same statement serves both backends.
func applyMigration(ctx context.Context, db *sql.DB, m migrationFile) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", m.version, err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return fmt.Errorf("apply migration %s: %w", m.version, err)
	}
	//nolint:gosec // version is regex-validated, checksum is hex, time is formatted by us; no user input
	record := fmt.Sprintf("INSERT INTO schema_migrations (version, checksum, applied_at) VALUES ('%s', '%s', '%s')",
		m.version, m.checksum, formatTime(time.Now()))
	if _, err := tx.ExecContext(ctx, record); err != nil {
		return fmt.Errorf("record migration %s: %w", m.version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", m.version, err)
	}
	return nil
}
