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

func TestLinkTokensConformanceSQLite(t *testing.T) {
	s, _ := newSQLite(t)
	runLinkTokensConformance(t, s)
}

func TestLinkTokensConformancePostgres(t *testing.T) {
	runLinkTokensConformance(t, newPGEnv(t, true).openApp(t))
}

// runLinkTokensConformance covers adding a link token by the hash of its
// bearer, using it once before expiry, and refusing a repeat, an expired token
// and an unknown id.
func runLinkTokensConformance(t *testing.T, s store.Store) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	exp := now.Add(10 * time.Minute)
	wantExpiry := exp.Format(time.RFC3339)

	newUser := func(t *testing.T) string {
		t.Helper()
		userID, err := s.UserForIdentity(ctx, "link:"+randHex(t, 8))
		if err != nil {
			t.Fatalf("UserForIdentity error = %v, want nil", err)
		}
		return userID
	}
	newHash := func(t *testing.T) string {
		t.Helper()
		return store.HashToken("mg_" + randHex(t, 16))
	}
	mustAdd := func(t *testing.T, userID, hash string) store.LinkToken {
		t.Helper()
		lt, err := s.AddLinkToken(ctx, userID, hash, exp)
		if err != nil {
			t.Fatalf("AddLinkToken error = %v, want nil", err)
		}
		return lt
	}

	t.Run("add", func(t *testing.T) {
		userID := newUser(t)
		lt, err := s.AddLinkToken(ctx, userID, newHash(t), exp)
		if err != nil {
			t.Fatalf("AddLinkToken error = %v, want nil", err)
		}
		if !isHex32(lt.ID) {
			t.Errorf("AddLinkToken ID = %q, want 32 lowercase hex characters", lt.ID)
		}
		if lt.UserID != userID {
			t.Errorf("AddLinkToken UserID = %q, want %q", lt.UserID, userID)
		}
		if lt.ExpiresAt != wantExpiry {
			t.Errorf("AddLinkToken ExpiresAt = %q, want %q", lt.ExpiresAt, wantExpiry)
		}
		if !strings.HasSuffix(lt.ExpiresAt, "Z") {
			t.Errorf("AddLinkToken ExpiresAt = %q, want UTC RFC3339 text ending in Z", lt.ExpiresAt)
		}
	})

	t.Run("use once", func(t *testing.T) {
		lt := mustAdd(t, newUser(t), newHash(t))

		got, err := s.UseLinkToken(ctx, lt.ID, now)
		if err != nil || got != lt {
			t.Fatalf("UseLinkToken before expiry = %+v, %v, want %+v, nil", got, err, lt)
		}
		if _, err := s.UseLinkToken(ctx, lt.ID, now); !errors.Is(err, store.ErrTokenUsed) {
			t.Errorf("second UseLinkToken at %s error = %v, want %v", now.Format(time.RFC3339), err, store.ErrTokenUsed)
		}
		if _, err := s.UseLinkToken(ctx, lt.ID, exp.Add(time.Second)); !errors.Is(err, store.ErrTokenUsed) {
			t.Errorf("UseLinkToken after a used token expired error = %v, want %v", err, store.ErrTokenUsed)
		}
	})

	t.Run("expired", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			at   time.Time
		}{
			{"at expiry", exp},
			{"after expiry", exp.Add(time.Second)},
		} {
			t.Run(tc.name, func(t *testing.T) {
				lt := mustAdd(t, newUser(t), newHash(t))

				if _, err := s.UseLinkToken(ctx, lt.ID, tc.at); !errors.Is(err, store.ErrTokenExpired) {
					t.Errorf("UseLinkToken at %s error = %v, want %v", tc.at.Format(time.RFC3339), err, store.ErrTokenExpired)
				}
				if _, err := s.UseLinkToken(ctx, lt.ID, now); !errors.Is(err, store.ErrTokenExpired) {
					t.Errorf("UseLinkToken at %s after a refused use error = %v, want %v", now.Format(time.RFC3339), err, store.ErrTokenExpired)
				}
			})
		}
	})

	t.Run("unknown id", func(t *testing.T) {
		unknown := randHex(t, 16)
		if _, err := s.UseLinkToken(ctx, unknown, now); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("UseLinkToken(unknown id %q) error = %v, want %v", unknown, err, store.ErrNotFound)
		}
	})

	t.Run("duplicate hash", func(t *testing.T) {
		userID := newUser(t)
		hash := newHash(t)
		first := mustAdd(t, userID, hash)
		if _, err := s.AddLinkToken(ctx, userID, hash, exp); !errors.Is(err, store.ErrDuplicate) {
			t.Errorf("AddLinkToken with a repeated hash error = %v, want %v", err, store.ErrDuplicate)
		}
		got, err := s.UseLinkToken(ctx, first.ID, now)
		if err != nil || got != first {
			t.Errorf("UseLinkToken after a duplicate add = %+v, %v, want the first token %+v, nil", got, err, first)
		}
	})

	t.Run("independent tokens", func(t *testing.T) {
		userID := newUser(t)
		a := mustAdd(t, userID, newHash(t))
		b := mustAdd(t, userID, newHash(t))
		if a.ID == b.ID {
			t.Fatalf("two AddLinkToken calls returned the same ID %q, want distinct ids", a.ID)
		}
		if got, err := s.UseLinkToken(ctx, a.ID, now); err != nil || got != a {
			t.Errorf("UseLinkToken(first token) = %+v, %v, want %+v, nil", got, err, a)
		}
		got, err := s.UseLinkToken(ctx, b.ID, now)
		if err != nil || got != b {
			t.Errorf("UseLinkToken(second token) after using the first = %+v, %v, want %+v, nil", got, err, b)
		}
	})
}

func TestLinkTokenStoredHashedOnly(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		s, raw := newSQLite(t)
		checkLinkTokenHashedOnly(t, s, raw, sqliteSchema(t, raw))
	})
	t.Run("postgres", func(t *testing.T) {
		env := newPGEnv(t, true)
		raw := env.rawDB(t, env.superDSN)
		checkLinkTokenHashedOnly(t, env.openApp(t), raw, pgSchema(t, raw))
	})
}

func checkLinkTokenHashedOnly(t *testing.T, s store.Store, raw *sql.DB, cols []col) {
	t.Helper()
	ctx := context.Background()
	plain := "mg_" + strings.Repeat("e", 52)
	sum := sha256.Sum256([]byte(plain))
	wantHash := hex.EncodeToString(sum[:])

	if got := store.HashToken(plain); got != wantHash {
		t.Fatalf("HashToken(%q) = %q, want hex SHA-256 %q", plain, got, wantHash)
	}
	userID, err := s.UserForIdentity(ctx, "link:"+randHex(t, 8))
	if err != nil {
		t.Fatalf("UserForIdentity error = %v, want nil", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	lt, err := s.AddLinkToken(ctx, userID, store.HashToken(plain), now.Add(10*time.Minute))
	if err != nil {
		t.Fatalf("AddLinkToken error = %v, want nil", err)
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
				t.Errorf("table %q row %q contains the plaintext link token or an mg_ value, want only the hash", table, row)
			}
		}
	}
	if !foundHash {
		t.Errorf("no stored value equals the SHA-256 hash %q", wantHash)
	}

	got, err := s.UseLinkToken(ctx, lt.ID, now)
	if err != nil || got != lt {
		t.Errorf("UseLinkToken with the stored token = %+v, %v, want %+v, nil", got, err, lt)
	}
}
