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

func TestEnrolmentTokenStoredHashedOnly(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		s, raw := newSQLite(t)
		checkTokenHashedOnly(t, s, raw, sqliteSchema(t, raw))
	})
	t.Run("postgres", func(t *testing.T) {
		env := newPGEnv(t, true)
		raw := env.rawDB(t, env.superDSN)
		checkTokenHashedOnly(t, env.openApp(t), raw, pgSchema(t, raw))
	})
}

func checkTokenHashedOnly(t *testing.T, s store.Store, raw *sql.DB, cols []col) {
	ctx := context.Background()
	plain := "mg_" + strings.Repeat("a", 52)
	other := "mg_" + strings.Repeat("b", 52)
	sum := sha256.Sum256([]byte(plain))
	wantHash := hex.EncodeToString(sum[:])

	if got := store.HashToken(plain); got != wantHash {
		t.Fatalf("HashToken(%q) = %q, want hex SHA-256 %q", plain, got, wantHash)
	}
	org, err := s.CreateOrg(ctx)
	if err != nil {
		t.Fatalf("CreateOrg error = %v, want nil", err)
	}
	now := time.Now().UTC()
	if err := s.AddEnrolmentToken(ctx, org, store.HashToken(plain), now.Add(time.Hour)); err != nil {
		t.Fatalf("AddEnrolmentToken error = %v, want nil", err)
	}

	if _, err := s.UseEnrolmentToken(ctx, store.HashToken(other), now); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("lookup with a different token error = %v, want %v", err, store.ErrNotFound)
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
				t.Errorf("table %q row %q contains the plaintext token or an mg_ value, want only the hash", table, row)
			}
		}
	}
	if !foundHash {
		t.Errorf("no stored value equals the SHA-256 hash %q", wantHash)
	}

	if got, err := s.UseEnrolmentToken(ctx, store.HashToken(plain), now); err != nil || got != org {
		t.Errorf("lookup with the right token = %q, %v, want %q, nil", got, err, org)
	}
}
