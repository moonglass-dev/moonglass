package store_test

import (
	"database/sql"
	"strings"
	"testing"
	"time"
)

func TestRollbackConstraintsEnforced(t *testing.T) {
	s, db := newSQLite(t)
	tgt := seedTarget(t, s)
	const insert = `INSERT INTO rollbacks (id, org_id, service_id, status, created_at)
		VALUES (?, ?, ?, ?, ?)`

	for _, tc := range []struct {
		name   string
		status string
		refuse string
	}{
		{name: "unknown status", status: "done", refuse: "CHECK constraint failed"},
		{name: "queued", status: "queued", refuse: "CHECK constraint failed"},
		{name: "superseded", status: "superseded", refuse: "CHECK constraint failed"},
		{name: "running", status: "running"},
		{name: "succeeded", status: "succeeded"},
		{name: "failed", status: "failed"},
		{name: "rejected", status: "rejected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := seedService(t, s, tgt, tgt.server, "r-"+randHex(t, 4))
			_, err := db.Exec(insert, randHex(t, 16), tgt.orgID, svc.ID, tc.status, at(0).Format(time.RFC3339))
			switch {
			case tc.refuse == "" && err != nil:
				t.Errorf("insert of rollback status %q error = %v, want nil", tc.status, err)
			case tc.refuse != "" && (err == nil || !strings.Contains(err.Error(), tc.refuse)):
				t.Errorf("insert of rollback status %q error = %v, want an error containing %q", tc.status, err, tc.refuse)
			}
		})
	}

	t.Run("service without release columns", func(t *testing.T) {
		id := randHex(t, 16)
		if _, err := db.Exec(`INSERT INTO services (id, org_id, project_id, environment_id, name, server_id, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id, tgt.orgID, tgt.project.ID, tgt.env.ID, "raw-"+randHex(t, 4), tgt.server, at(0).Format(time.RFC3339)); err != nil {
			t.Fatalf("insert of a service without the release columns error = %v, want nil", err)
		}

		var current string
		var previous sql.NullString
		if err := db.QueryRow(`SELECT current_release_id, previous_release_id FROM services WHERE id = ?`, id).
			Scan(&current, &previous); err != nil {
			t.Fatalf("select the service release columns error = %v, want nil", err)
		}
		if current != "" {
			t.Errorf("current_release_id of a service inserted without it = %q, want empty text", current)
		}
		if previous.Valid {
			t.Errorf("previous_release_id of a service inserted without it = %q (valid), want NULL", previous.String)
		}
	})
}
