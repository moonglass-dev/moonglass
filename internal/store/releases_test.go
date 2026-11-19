package store_test

import (
	"strings"
	"testing"
)

func TestReleaseConstraintsEnforced(t *testing.T) {
	s, db := newSQLite(t)
	tgt := seedTarget(t, s)
	const insert = `INSERT INTO releases (id, org_id, service_id, port, domains, pre_deploy_reversible, status, step, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

	// row is one releases row; service picks the case's first or second service.
	type row struct {
		service int
		status  string
		step    string
	}
	for _, tc := range []struct {
		name   string
		setup  []row
		check  row
		refuse string
	}{
		{name: "unknown status", check: row{0, "done", ""}, refuse: "CHECK constraint failed"},
		{name: "unknown step", check: row{0, "queued", "test"}, refuse: "CHECK constraint failed"},
		{name: "second queued", setup: []row{{0, "queued", ""}}, check: row{0, "queued", ""}, refuse: "UNIQUE constraint failed"},
		{name: "second running", setup: []row{{0, "running", ""}}, check: row{0, "running", ""}, refuse: "UNIQUE constraint failed"},
		{name: "several finished", setup: []row{{0, "succeeded", ""}, {0, "failed", ""}}, check: row{0, "rejected", ""}},
		{name: "queued and running on different services", setup: []row{{0, "running", ""}}, check: row{1, "queued", ""}},
		{name: "queued and running on one service", setup: []row{{0, "queued", ""}}, check: row{0, "running", ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			services := [2]string{
				seedService(t, s, tgt, tgt.server, "c-"+randHex(t, 4)).ID,
				seedService(t, s, tgt, tgt.server, "c-"+randHex(t, 4)).ID,
			}
			insertRow := func(r row) error {
				_, err := db.Exec(insert, randHex(t, 16), tgt.orgID, services[r.service], 8080, "[]", false, r.status, r.step, "2026-11-18T12:00:00Z")
				return err
			}
			for _, r := range tc.setup {
				if err := insertRow(r); err != nil {
					t.Fatalf("setup insert %+v error = %v, want nil", r, err)
				}
			}
			err := insertRow(tc.check)
			switch {
			case tc.refuse == "" && err != nil:
				t.Errorf("insert %+v error = %v, want nil", tc.check, err)
			case tc.refuse != "" && (err == nil || !strings.Contains(err.Error(), tc.refuse)):
				t.Errorf("insert %+v error = %v, want an error containing %q", tc.check, err, tc.refuse)
			}
		})
	}
}
