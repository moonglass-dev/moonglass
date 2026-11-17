package registry_test

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"moonglass.dev/moonglass/internal/registry"
	"moonglass.dev/moonglass/internal/store"
)

func TestOwnerTokenScopeIsWildcard(t *testing.T) {
	tests := []struct {
		name    string
		scopes  []registry.Scope
		expires time.Duration
		wantErr bool
	}{
		{"single wildcard row", []registry.Scope{wildcard}, time.Hour, false},
		{"specific project", []registry.Scope{{Project: "p1", Environment: "*", Operation: "*"}}, time.Hour, true},
		{"specific environment", []registry.Scope{{Project: "*", Environment: "e1", Operation: "*"}}, time.Hour, true},
		{"specific operation", []registry.Scope{{Project: "*", Environment: "*", Operation: "servers.list"}}, time.Hour, true},
		{"two rows", []registry.Scope{wildcard, wildcard}, time.Hour, true},
		{"zero rows", nil, time.Hour, true},
		{"expiry in the past", []registry.Scope{wildcard}, -time.Hour, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			ctx := context.Background()
			text, err := registry.MintToken(ctx, e.st, e.orgID, e.userID, "ci", tt.scopes, e.now.Add(tt.expires), e.now)
			if (err != nil) != tt.wantErr {
				t.Fatalf("MintToken(%v) error = %v, wantErr %v", tt.scopes, err, tt.wantErr)
			}
			_, authErr := e.reg.Authenticate(ctx, text)
			if tt.wantErr {
				if text != "" {
					t.Errorf("MintToken returned plaintext %q on error, want empty", text)
				}
				return
			}
			if authErr != nil {
				t.Fatalf("Authenticate(minted token) error = %v, want nil", authErr)
			}
			p, _ := e.reg.Authenticate(ctx, text)
			want := []registry.Scope{{Project: "*", Environment: "*", Operation: "*"}}
			if fmt.Sprint(p.Scopes) != fmt.Sprint(want) {
				t.Errorf("stored scopes = %v, want exactly %v", p.Scopes, want)
			}
		})
	}
	t.Run("rejected mint stores nothing", func(t *testing.T) {
		e := newEnv(t)
		_, _ = registry.MintToken(context.Background(), e.st, e.orgID, e.userID, "ci", []registry.Scope{{Project: "p1", Environment: "*", Operation: "*"}}, e.now.Add(time.Hour), e.now)
		for _, table := range []string{"tokens", "token_scopes", "token_hashes"} {
			var n int
			if err := e.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil || n != 0 {
				t.Errorf("rows in %s = %d (err %v), want 0", table, n, err)
			}
		}
	})
}

func TestTokenStoredHashedOnly(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	text, err := registry.MintToken(ctx, e.st, e.orgID, e.userID, "laptop", []registry.Scope{wildcard}, e.now.Add(time.Hour), e.now)
	if err != nil {
		t.Fatalf("MintToken error = %v, want nil", err)
	}
	if !regexp.MustCompile(`^mg_[a-z2-7]{52}$`).MatchString(text) {
		t.Fatalf("token = %q, want match for ^mg_[a-z2-7]{52}$", text)
	}
	other, err := registry.NewToken()
	if err != nil || other == text {
		t.Fatalf("NewToken = %q (err %v), want a fresh token different from %q", other, err, text)
	}

	var hash string
	if err := e.db.QueryRow("SELECT hash FROM token_hashes").Scan(&hash); err != nil {
		t.Fatalf("read token_hashes: %v", err)
	}
	if want := store.HashToken(text); hash != want {
		t.Errorf("stored hash = %q, want store.HashToken(token) = %q", hash, want)
	}

	p, err := e.reg.Authenticate(ctx, text)
	if err != nil {
		t.Fatalf("Authenticate(token) error = %v, want nil", err)
	}
	if p.OrgID != e.orgID || p.UserID != e.userID || p.Role != store.RoleOwner {
		t.Errorf("principal = %+v, want org %s user %s role owner", p, e.orgID, e.userID)
	}
	for name, bad := range map[string]string{"different": other, "truncated": text[:len(text)-1], "empty": "", "prefix only": "mg_"} {
		if _, err := e.reg.Authenticate(ctx, bad); err == nil {
			t.Errorf("Authenticate(%s token %q) error = nil, want ErrUnauthenticated", name, bad)
		}
	}

	var name, last4 string
	if err := e.db.QueryRow("SELECT name, last4 FROM tokens").Scan(&name, &last4); err != nil {
		t.Fatalf("read tokens: %v", err)
	}
	if name != "laptop" || last4 != text[len(text)-4:] {
		t.Errorf("stored name/last4 = %q/%q, want %q/%q", name, last4, "laptop", text[len(text)-4:])
	}

	// A call so the audit payloads exist too, then dump everything.
	if _, err := registry.Invoke(ctx, e.reg, mustServersList(t, e), p, registry.Target{}, empty{}); err != nil {
		t.Logf("Invoke: %v", err)
	}
	rows, err := e.db.Query("SELECT name FROM sqlite_master WHERE type = 'table'")
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	var tables []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		tables = append(tables, n)
	}
	_ = rows.Close()
	secret := strings.TrimPrefix(text, "mg_")
	for _, table := range tables {
		r, err := e.db.Query(`SELECT * FROM "` + table + `"`)
		if err != nil {
			t.Fatalf("dump %s: %v", table, err)
		}
		cols, _ := r.Columns()
		for r.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := r.Scan(ptrs...); err != nil {
				t.Fatalf("scan %s: %v", table, err)
			}
			if row := fmt.Sprint(vals...); strings.Contains(row, text) || strings.Contains(row, secret) {
				t.Errorf("table %s holds the plaintext token in row %v, want only its hash", table, vals)
			}
		}
		_ = r.Close()
	}

	t.Run("expired and revoked fail", func(t *testing.T) {
		e.now = e.now.Add(2 * time.Hour)
		if _, err := e.reg.Authenticate(ctx, text); err == nil {
			t.Errorf("Authenticate(expired token) error = nil, want ErrUnauthenticated")
		}
		e.now = t0
		if _, err := e.db.Exec("UPDATE token_hashes SET revoked_at = ?", t0.Format(time.RFC3339)); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		if _, err := e.reg.Authenticate(ctx, text); err == nil {
			t.Errorf("Authenticate(revoked token) error = nil, want ErrUnauthenticated")
		}
	})
}

// mustServersList registers a trivial viewer operation on e.reg.
func mustServersList(t *testing.T, e *env) *registry.Operation[empty, empty] {
	t.Helper()
	op, err := registry.Register(e.reg, registry.Spec{Name: "t.list", Method: "GET", Path: "/v1/t", MinRole: store.RoleViewer, Destructive: registry.No},
		func(context.Context, registry.Call, empty) (empty, error) { return empty{}, nil })
	if err != nil {
		t.Fatalf("Register error = %v, want nil", err)
	}
	return op
}
