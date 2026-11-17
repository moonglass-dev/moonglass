package api_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"moonglass.dev/moonglass/internal/server"
	"moonglass.dev/moonglass/internal/store"
	"moonglass.dev/moonglass/internal/store/sqlite"
)

// failingAudit is a store whose audit append always fails.
type failingAudit struct{ store.Store }

func (failingAudit) AppendAudit(context.Context, string, func(string, int64) store.AuditEvent) error {
	return errors.New("disk full")
}

func TestServerErrorIsLoggedWithoutTheToken(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	real, err := sqlite.Open(ctx, filepath.Join(dir, "server.db"))
	if err != nil {
		t.Fatalf("sqlite.Open error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = real.Close() })
	now := time.Now()
	srv, err := server.New(failingAudit{real}, dir, func() time.Time { return now })
	if err != nil {
		t.Fatalf("server.New error = %v, want nil", err)
	}
	orgID, err := real.CreateOrg(ctx)
	if err != nil {
		t.Fatalf("CreateOrg error = %v, want nil", err)
	}
	userID, err := real.UserForIdentity(ctx, "owner@example.com")
	if err != nil {
		t.Fatalf("UserForIdentity error = %v, want nil", err)
	}
	if err := real.AddMembership(ctx, orgID, userID, store.RoleOwner); err != nil {
		t.Fatalf("AddMembership error = %v, want nil", err)
	}
	token, err := srv.MintOwnerToken(ctx, orgID, userID, "test", now.Add(time.Hour))
	if err != nil {
		t.Fatalf("MintOwnerToken error = %v, want nil", err)
	}

	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(io.Discard) })

	req := httptest.NewRequest("GET", "/v1/servers", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if !strings.Contains(logs.String(), "write audit event for servers.list") {
		t.Errorf("log = %q, want it to contain the wrapped error %q", logs.String(), "write audit event for servers.list")
	}
	if strings.Contains(logs.String(), token) {
		t.Errorf("log contains the bearer token: %q", logs.String())
	}
}
