package main

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"moonglass.dev/moonglass/internal/keys"
	"moonglass.dev/moonglass/internal/server"
	"moonglass.dev/moonglass/internal/store/sqlite"
)

func newTestServer(t *testing.T) (url, fingerprint, token string) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	st, err := sqlite.Open(ctx, filepath.Join(dir, "server.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: unexpected error %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	org, err := st.CreateOrg(ctx)
	if err != nil {
		t.Fatalf("CreateOrg: unexpected error %v", err)
	}
	s, err := server.New(st, dir, time.Now)
	if err != nil {
		t.Fatalf("server.New: unexpected error %v", err)
	}
	ts := httptest.NewUnstartedServer(s.Handler())
	cfg, err := s.TLSConfig()
	if err != nil {
		t.Fatalf("TLSConfig: unexpected error %v", err)
	}
	ts.TLS = cfg
	ts.StartTLS()
	t.Cleanup(ts.Close)
	token, err = s.IssueEnrolToken(ctx, org)
	if err != nil {
		t.Fatalf("IssueEnrolToken: unexpected error %v", err)
	}
	return ts.URL, s.Fingerprint(), token
}

func TestAgentEnrollPrintsFingerprint(t *testing.T) {
	url, fingerprint, token := newTestServer(t)
	dir := t.TempDir()

	var stdout, stderr bytes.Buffer
	code := run([]string{"agent", "enroll", "--data-dir", dir, "--url", url, "--server-fingerprint", fingerprint, token}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit status: expected 0, got %d (stderr %q)", code, stderr.String())
	}
	raw, err := os.ReadFile(filepath.Join(dir, "agent-signing.key"))
	if err != nil {
		t.Fatalf("read signing key: unexpected error %v", err)
	}
	if len(raw) != 64 {
		t.Fatalf("signing key length: expected 64, got %d", len(raw))
	}
	want, err := keys.Fingerprint(raw[32:])
	if err != nil {
		t.Fatalf("Fingerprint: unexpected error %v", err)
	}
	if got := stdout.String(); got != want+"\n" {
		t.Errorf("stdout: expected %q, got %q", want+"\n", got)
	}
	if len(want) != 64 {
		t.Errorf("fingerprint length: expected 64, got %d", len(want))
	}
	if strings.Contains(stdout.String(), token) || strings.Contains(stderr.String(), token) {
		t.Errorf("token leaked: stdout %q, stderr %q", stdout.String(), stderr.String())
	}
}

func TestAgentEnrollFailures(t *testing.T) {
	url, fingerprint, token := newTestServer(t)
	first := t.TempDir()
	var out, errb bytes.Buffer
	if code := run([]string{"agent", "enroll", "--data-dir", first, "--url", url, "--server-fingerprint", fingerprint, token}, &out, &errb); code != 0 {
		t.Fatalf("first enroll exit status: expected 0, got %d (stderr %q)", code, errb.String())
	}

	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantUse  bool
	}{
		{"missing token", []string{"agent", "enroll", "--data-dir", t.TempDir(), "--url", url, "--server-fingerprint", fingerprint}, 2, true},
		{"replayed token", []string{"agent", "enroll", "--data-dir", t.TempDir(), "--url", url, "--server-fingerprint", fingerprint, token}, 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := run(tt.args, &stdout, &stderr)

			if code != tt.wantCode {
				t.Errorf("exit status: expected %d, got %d", tt.wantCode, code)
			}
			if stdout.String() != "" {
				t.Errorf("stdout: expected it empty, got %q", stdout.String())
			}
			if tt.wantUse && stderr.String() != usage {
				t.Errorf("stderr: expected usage %q, got %q", usage, stderr.String())
			}
			if !tt.wantUse && stderr.String() == "" {
				t.Errorf("stderr: expected an error message, got nothing")
			}
			if strings.Contains(stderr.String(), token) {
				t.Errorf("stderr leaks the token: %q", stderr.String())
			}
		})
	}
}
