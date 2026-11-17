// Package migrations embeds the numbered SQL migration files of each backend.
package migrations

import (
	"embed"
	"io/fs"
)

//go:embed pg/*.sql sqlite/*.sql
var files embed.FS

// PG returns the Postgres migration files.
func PG() fs.FS { return sub("pg") }

// SQLite returns the SQLite migration files.
func SQLite() fs.FS { return sub("sqlite") }

func sub(dir string) fs.FS {
	f, err := fs.Sub(files, dir)
	if err != nil {
		panic("migrations: embedded directory " + dir + " is missing: " + err.Error())
	}
	return f
}
