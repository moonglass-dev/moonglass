package store_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"moonglass.dev/moonglass/internal/store"
)

func TestSessionsConformanceSQLite(t *testing.T) {
	s, _ := newSQLite(t)
	runSessionsConformance(t, s)
}

func TestSessionsConformancePostgres(t *testing.T) {
	env := newPGEnv(t, true)
	runSessionsConformance(t, env.openApp(t))
}

func runSessionsConformance(t *testing.T, s store.Store) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	exp := now.Add(time.Hour)
	wantExpiry := exp.Format(time.RFC3339)

	newUser := func(t *testing.T) string {
		t.Helper()
		userID, err := s.UserForIdentity(ctx, "sess:"+randHex(t, 8))
		if err != nil {
			t.Fatalf("UserForIdentity error = %v, want nil", err)
		}
		return userID
	}
	newHash := func(t *testing.T) string {
		t.Helper()
		return store.HashToken("mg_" + randHex(t, 16))
	}
	mustAdd := func(t *testing.T, userID, hash string) store.Session {
		t.Helper()
		sess, err := s.AddSession(ctx, userID, hash, exp)
		if err != nil {
			t.Fatalf("AddSession error = %v, want nil", err)
		}
		return sess
	}

	t.Run("add and look up", func(t *testing.T) {
		userID := newUser(t)
		hash := newHash(t)
		sess, err := s.AddSession(ctx, userID, hash, exp)
		if err != nil {
			t.Fatalf("AddSession error = %v, want nil", err)
		}
		if !isHex32(sess.ID) {
			t.Errorf("AddSession ID = %q, want 32 lowercase hex characters", sess.ID)
		}
		if sess.UserID != userID {
			t.Errorf("AddSession UserID = %q, want %q", sess.UserID, userID)
		}
		if sess.ExpiresAt != wantExpiry {
			t.Errorf("AddSession ExpiresAt = %q, want %q", sess.ExpiresAt, wantExpiry)
		}
		if !strings.HasSuffix(sess.ExpiresAt, "Z") {
			t.Errorf("AddSession ExpiresAt = %q, want UTC RFC3339 text ending in Z", sess.ExpiresAt)
		}
		got, err := s.SessionByHash(ctx, hash, now)
		if err != nil {
			t.Fatalf("SessionByHash error = %v, want nil", err)
		}
		if got != sess {
			t.Errorf("SessionByHash = %+v, want %+v", got, sess)
		}
	})

	t.Run("unknown hash", func(t *testing.T) {
		if _, err := s.SessionByHash(ctx, newHash(t), now); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("SessionByHash(unknown) error = %v, want %v", err, store.ErrNotFound)
		}
	})

	t.Run("expiry", func(t *testing.T) {
		userID := newUser(t)
		hash := newHash(t)
		sess := mustAdd(t, userID, hash)
		for _, tc := range []struct {
			name    string
			at      time.Time
			wantErr bool
		}{
			{"before expiry", exp.Add(-time.Second), false},
			{"at expiry", exp, true},
			{"after expiry", exp.Add(time.Second), true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got, err := s.SessionByHash(ctx, hash, tc.at)
				if tc.wantErr {
					if !errors.Is(err, store.ErrNotFound) {
						t.Errorf("SessionByHash(at %s) error = %v, want %v", tc.at.Format(time.RFC3339), err, store.ErrNotFound)
					}
					return
				}
				if err != nil || got.ID != sess.ID {
					t.Errorf("SessionByHash(at %s) = %+v, %v, want session %q, nil", tc.at.Format(time.RFC3339), got, err, sess.ID)
				}
			})
		}
	})

	t.Run("revoke by another user", func(t *testing.T) {
		userID := newUser(t)
		otherID := newUser(t)
		hash := newHash(t)
		sess := mustAdd(t, userID, hash)
		if err := s.RevokeSession(ctx, otherID, sess.ID, now); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("RevokeSession by another user error = %v, want %v", err, store.ErrNotFound)
		}
		got, err := s.SessionByHash(ctx, hash, now)
		if err != nil || got.ID != sess.ID {
			t.Errorf("SessionByHash after a foreign revoke = %+v, %v, want session %q still live, nil", got, err, sess.ID)
		}
	})

	t.Run("revoke unknown id", func(t *testing.T) {
		userID := newUser(t)
		unknown := randHex(t, 16)
		if err := s.RevokeSession(ctx, userID, unknown, now); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("RevokeSession(unknown id %q) error = %v, want %v", unknown, err, store.ErrNotFound)
		}
	})

	t.Run("revoke", func(t *testing.T) {
		userID := newUser(t)
		hash := newHash(t)
		sess := mustAdd(t, userID, hash)
		if err := s.RevokeSession(ctx, userID, sess.ID, now); err != nil {
			t.Fatalf("RevokeSession error = %v, want nil", err)
		}
		if _, err := s.SessionByHash(ctx, hash, now); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("SessionByHash after revoke error = %v, want %v", err, store.ErrNotFound)
		}
		if err := s.RevokeSession(ctx, userID, sess.ID, now.Add(time.Minute)); err != nil {
			t.Errorf("second RevokeSession error = %v, want nil", err)
		}
	})

	t.Run("duplicate hash", func(t *testing.T) {
		userID := newUser(t)
		hash := newHash(t)
		mustAdd(t, userID, hash)
		if _, err := s.AddSession(ctx, userID, hash, exp); !errors.Is(err, store.ErrDuplicate) {
			t.Errorf("AddSession with a repeated hash error = %v, want %v", err, store.ErrDuplicate)
		}
		if got, err := s.SessionByHash(ctx, hash, now); err != nil || len(got.ID) != 32 {
			t.Errorf("SessionByHash after a duplicate add = %+v, %v, want the first session still live, nil", got, err)
		}
	})

	t.Run("independent sessions", func(t *testing.T) {
		userID := newUser(t)
		hashA, hashB := newHash(t), newHash(t)
		sessA := mustAdd(t, userID, hashA)
		sessB := mustAdd(t, userID, hashB)
		if sessA.ID == sessB.ID {
			t.Fatalf("two AddSession calls returned the same ID %q, want distinct ids", sessA.ID)
		}
		if err := s.RevokeSession(ctx, userID, sessA.ID, now); err != nil {
			t.Fatalf("RevokeSession error = %v, want nil", err)
		}
		got, err := s.SessionByHash(ctx, hashB, now)
		if err != nil || got.ID != sessB.ID {
			t.Errorf("SessionByHash(other session) = %+v, %v, want session %q still live, nil", got, err, sessB.ID)
		}
		if _, err := s.SessionByHash(ctx, hashA, now); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("SessionByHash(revoked session) error = %v, want %v", err, store.ErrNotFound)
		}
	})
}

func TestAuditSessionColumnsConformanceSQLite(t *testing.T) {
	s, _ := newSQLite(t)
	runAuditSessionColumnsConformance(t, s)
}

func TestAuditSessionColumnsConformancePostgres(t *testing.T) {
	env := newPGEnv(t, true)
	runAuditSessionColumnsConformance(t, env.openApp(t))
}

func runAuditSessionColumnsConformance(t *testing.T, s store.Store) {
	ctx := context.Background()
	orgID, err := s.CreateOrg(ctx)
	if err != nil {
		t.Fatalf("CreateOrg error = %v, want nil", err)
	}
	sessionID := "s" + randHex(t, 8)
	stepUpID := "u" + randHex(t, 8)
	wantSession := []string{sessionID, "", ""}
	wantStepUp := []string{stepUpID, "", ""}

	for i := range wantSession {
		session, stepUp := wantSession[i], wantStepUp[i]
		err := s.AppendAudit(ctx, orgID, func(prevHash string, seq int64) store.AuditEvent {
			return store.AuditEvent{
				OrgID:         orgID,
				Seq:           seq,
				ID:            "e" + randHex(t, 8),
				At:            time.Now().UTC().Format(time.RFC3339),
				PrincipalKind: "human",
				Operation:     "sessions.conformance",
				Outcome:       "allowed",
				PrevHash:      prevHash,
				Hash:          "h" + randHex(t, 8),
				SessionID:     session,
				StepUpID:      stepUp,
			}
		})
		if err != nil {
			t.Fatalf("AppendAudit(event %d) error = %v, want nil", i+1, err)
		}
	}

	evs, err := s.AuditEvents(ctx, orgID)
	if err != nil {
		t.Fatalf("AuditEvents error = %v, want nil", err)
	}
	if len(evs) != 3 {
		t.Fatalf("AuditEvents len = %d, want 3", len(evs))
	}
	for i, ev := range evs {
		if ev.SessionID != wantSession[i] || ev.StepUpID != wantStepUp[i] {
			t.Errorf("event %d session/step-up ids = %q/%q, want %q/%q",
				i+1, ev.SessionID, ev.StepUpID, wantSession[i], wantStepUp[i])
		}
		if ev.Seq != int64(i+1) {
			t.Errorf("event %d Seq = %d, want %d", i+1, ev.Seq, i+1)
		}
	}
	if evs[0].PrevHash != "" {
		t.Errorf("event 1 PrevHash = %q, want empty", evs[0].PrevHash)
	}
	for i := 1; i < len(evs); i++ {
		if evs[i].PrevHash != evs[i-1].Hash {
			t.Errorf("event %d PrevHash = %q, want the previous event's hash %q", i+1, evs[i].PrevHash, evs[i-1].Hash)
		}
	}
}

func TestSessionStoredHashedOnly(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		s, raw := newSQLite(t)
		checkSessionHashedOnly(t, s, raw, sqliteSchema(t, raw))
	})
	t.Run("postgres", func(t *testing.T) {
		env := newPGEnv(t, true)
		raw := env.rawDB(t, env.superDSN)
		checkSessionHashedOnly(t, env.openApp(t), raw, pgSchema(t, raw))
	})
}

func checkSessionHashedOnly(t *testing.T, s store.Store, raw *sql.DB, cols []col) {
	t.Helper()
	ctx := context.Background()
	plain := "mg_" + strings.Repeat("c", 52)
	other := "mg_" + strings.Repeat("d", 52)
	sum := sha256.Sum256([]byte(plain))
	wantHash := hex.EncodeToString(sum[:])

	if got := store.HashToken(plain); got != wantHash {
		t.Fatalf("HashToken(%q) = %q, want hex SHA-256 %q", plain, got, wantHash)
	}
	userID, err := s.UserForIdentity(ctx, "sess:"+randHex(t, 8))
	if err != nil {
		t.Fatalf("UserForIdentity error = %v, want nil", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	sess, err := s.AddSession(ctx, userID, store.HashToken(plain), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("AddSession error = %v, want nil", err)
	}

	if _, err := s.SessionByHash(ctx, store.HashToken(other), now); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("SessionByHash with a different bearer error = %v, want %v", err, store.ErrNotFound)
	}

	foundHash := false
	for table, rows := range dumpAll(t, raw, cols) {
		for _, row := range rows {
			for _, cell := range strings.Split(row, "|") {
				if cell == wantHash {
					foundHash = true
				}
			}
			if strings.Contains(row, plain) || strings.Contains(row, "mg_") {
				t.Errorf("table %q row %q contains the plaintext session or an mg_ value, want only the hash", table, row)
			}
		}
	}
	if !foundHash {
		t.Errorf("no stored value equals the SHA-256 hash %q", wantHash)
	}

	got, err := s.SessionByHash(ctx, store.HashToken(plain), now)
	if err != nil || got != sess {
		t.Errorf("SessionByHash with the right bearer = %+v, %v, want %+v, nil", got, err, sess)
	}
}

// isHex32 reports whether s is exactly 32 lowercase hexadecimal characters.
func isHex32(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
