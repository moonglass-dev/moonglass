package store_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

func TestStepUpConstraintsEnforced(t *testing.T) {
	s, db := newSQLite(t)
	orgID, err := s.CreateOrg(context.Background())
	if err != nil {
		t.Fatalf("CreateOrg error = %v, want nil", err)
	}
	stamp := at(0).Format(time.RFC3339)
	const insert = `INSERT INTO step_ups (org_id, id, user_id, token_id, session_id, operation, input_digest, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

	for _, tc := range []struct {
		name      string
		tokenID   string
		sessionID string
		refuse    string
	}{
		{name: "both credentials", tokenID: randHex(t, 16), sessionID: randHex(t, 16), refuse: "CHECK constraint failed"},
		{name: "neither credential", refuse: "CHECK constraint failed"},
		{name: "token only", tokenID: randHex(t, 16)},
		{name: "session only", sessionID: randHex(t, 16)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := db.Exec(insert, orgID, randHex(t, 16), randHex(t, 16), tc.tokenID, tc.sessionID,
				"services.delete", randHex(t, 32), stamp, stamp)
			switch {
			case tc.refuse == "" && err != nil:
				t.Errorf("insert with token id %q and session id %q error = %v, want nil", tc.tokenID, tc.sessionID, err)
			case tc.refuse != "" && (err == nil || !strings.Contains(err.Error(), tc.refuse)):
				t.Errorf("insert with token id %q and session id %q error = %v, want an error containing %q",
					tc.tokenID, tc.sessionID, err, tc.refuse)
			}
		})
	}

	t.Run("defaults", func(t *testing.T) {
		id := randHex(t, 16)
		if _, err := db.Exec(`INSERT INTO step_ups (org_id, id, user_id, session_id, operation, input_digest, created_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			orgID, id, randHex(t, 16), randHex(t, 16), "services.delete", randHex(t, 32), stamp, stamp); err != nil {
			t.Fatalf("insert without the optional columns error = %v, want nil", err)
		}

		var tokenID, projectID, environmentID string
		var confirmHash, signedInAt, approvedAt, declinedAt, usedAt sql.NullString
		if err := db.QueryRow(`SELECT token_id, project_id, environment_id, confirm_hash,
			signed_in_at, approved_at, declined_at, used_at FROM step_ups WHERE id = ?`, id).
			Scan(&tokenID, &projectID, &environmentID, &confirmHash, &signedInAt, &approvedAt, &declinedAt, &usedAt); err != nil {
			t.Fatalf("select the step-up defaults error = %v, want nil", err)
		}
		for _, tc := range []struct {
			name string
			got  string
		}{
			{"token_id", tokenID},
			{"project_id", projectID},
			{"environment_id", environmentID},
		} {
			if tc.got != "" {
				t.Errorf("%s of a step-up inserted without it = %q, want empty text", tc.name, tc.got)
			}
		}
		for _, tc := range []struct {
			name string
			got  sql.NullString
		}{
			{"confirm_hash", confirmHash},
			{"signed_in_at", signedInAt},
			{"approved_at", approvedAt},
			{"declined_at", declinedAt},
			{"used_at", usedAt},
		} {
			if tc.got.Valid {
				t.Errorf("%s of a new step-up = %q (valid), want NULL", tc.name, tc.got.String)
			}
		}
	})
}
