package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"moonglass.dev/moonglass/internal/keys"
	"moonglass.dev/moonglass/internal/registry"
	"moonglass.dev/moonglass/internal/store"
	"moonglass.dev/moonglass/internal/store/sqlite"
)

// writeControlPlaneKey puts a fresh control-plane key in dir and returns the
// fingerprint moonglass server must print for it.
func writeControlPlaneKey(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create data dir %s: unexpected error %v", dir, err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: unexpected error %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, controlPlaneKeyFile), priv, 0o600); err != nil {
		t.Fatalf("write control-plane key: unexpected error %v", err)
	}
	fp, err := keys.Fingerprint(pub)
	if err != nil {
		t.Fatalf("Fingerprint: unexpected error %v", err)
	}
	return fp
}

// readControlPlaneKey returns the public half of dir/control-plane.key.
func readControlPlaneKey(t *testing.T, dir string) ed25519.PublicKey {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, controlPlaneKeyFile))
	if err != nil {
		t.Fatalf("read %s: unexpected error %v", controlPlaneKeyFile, err)
	}
	return ed25519.PrivateKey(raw).Public().(ed25519.PublicKey)
}

func TestRunServesListenOnlyOverTLS(t *testing.T) {
	dir := t.TempDir()
	wantFP := writeControlPlaneKey(t, dir)
	wantKey := readControlPlaneKey(t, dir)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for the API: unexpected error %v", err)
	}
	agentLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = ln.Close()
		t.Fatalf("listen for agents: unexpected error %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, dir, ln, agentLn, false, "", io.Discard) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("serve: expected nil after ctx cancel, got %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Errorf("serve: expected it to return after ctx cancel, still running after 10s")
		}
	})
	addr := ln.Addr().String()

	// Plain HTTP on the --listen address must not receive an HTTP response.
	plain := &http.Client{Timeout: 10 * time.Second}
	res, err := plain.Post("http://"+addr+"/v1/enroll", "application/octet-stream", strings.NewReader("not tls"))
	switch {
	case err == nil:
		_ = res.Body.Close()
		t.Errorf("plain HTTP POST to %s: expected a transport error and no HTTP response, got status %d", addr, res.StatusCode)
	case res != nil:
		_ = res.Body.Close()
		t.Errorf("plain HTTP POST to %s: expected a nil response with the transport error, got status %d", addr, res.StatusCode)
	}

	transport := &http.Transport{TLSClientConfig: keys.EnrolClientTLSConfig(wantFP)}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Timeout: 10 * time.Second, Transport: transport}

	tests := []struct {
		name       string
		method     string
		path       string
		body       io.Reader
		wantStatus int
	}{
		{"enrolment with an empty body", http.MethodPost, "/v1/enroll", http.NoBody, http.StatusBadRequest},
		{"a registry route without a token", http.MethodGet, "/v1/servers", http.NoBody, http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(tt.method, "https://"+addr+tt.path, tt.body)
			if err != nil {
				t.Fatalf("http.NewRequest: unexpected error %v", err)
			}
			res, err := client.Do(req)
			if err != nil {
				t.Fatalf("%s %s over TLS 1.3: unexpected error %v", tt.method, tt.path, err)
			}
			defer func() { _ = res.Body.Close() }()
			if res.StatusCode != tt.wantStatus {
				t.Errorf("%s %s status: expected %d, got %d", tt.method, tt.path, tt.wantStatus, res.StatusCode)
			}
			if res.TLS == nil {
				t.Fatalf("%s %s: expected the connection to be TLS, got a plain response", tt.method, tt.path)
			}
			if res.TLS.Version != tls.VersionTLS13 {
				t.Errorf("%s %s negotiated version: expected %#x (TLS 1.3), got %#x", tt.method, tt.path, tls.VersionTLS13, res.TLS.Version)
			}
			if n := len(res.TLS.PeerCertificates); n != 1 {
				t.Fatalf("%s %s peer certificates: expected exactly 1, got %d", tt.method, tt.path, n)
			}
			leaf, err := keys.PeerPublicKey([][]byte{res.TLS.PeerCertificates[0].Raw})
			if err != nil {
				t.Fatalf("%s %s leaf key: unexpected error %v", tt.method, tt.path, err)
			}
			if !leaf.Equal(wantKey) {
				t.Errorf("%s %s leaf key: expected the control-plane key %x (fingerprint %s), got %x", tt.method, tt.path, []byte(wantKey), wantFP, []byte(leaf))
			}
		})
	}
}

// openRunStore opens the store Run left in dir/server.db for post-run reads.
func openRunStore(t *testing.T, dir string) store.Store {
	t.Helper()
	st, err := sqlite.Open(context.Background(), filepath.Join(dir, "server.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: unexpected error %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// storedEnrolmentToken reports whether token's hash is an enrolment token row.
func storedEnrolmentToken(t *testing.T, dir, token string) bool {
	t.Helper()
	var n int
	if err := openServerDB(t, dir).QueryRow("SELECT COUNT(*) FROM enrolment_token_hashes WHERE token_hash = ?", store.HashToken(token)).Scan(&n); err != nil {
		t.Fatalf("count enrolment tokens: unexpected error %v", err)
	}
	return n == 1
}

func TestRunPrintsControlPlaneFingerprintFirst(t *testing.T) {
	dir := t.TempDir()
	wantFP := writeControlPlaneKey(t, dir)
	if len(wantFP) != 64 {
		t.Fatalf("control-plane fingerprint: expected 64 characters, got %d (%q)", len(wantFP), wantFP)
	}
	if _, err := hex.DecodeString(wantFP); err != nil {
		t.Errorf("control-plane fingerprint %q: expected lowercase hex, got error %v", wantFP, err)
	}
	if strings.ToLower(wantFP) != wantFP {
		t.Errorf("control-plane fingerprint %q: expected lowercase, got uppercase characters", wantFP)
	}

	tests := []struct {
		name        string
		printEnrol  bool
		ownerName   string
		wantLines   int
		wantEnrol   bool
		wantOwnerAt int
	}{
		{"neither token", false, "", 1, false, -1},
		{"enrolment token", true, "", 2, true, -1},
		{"owner token", false, "ci", 2, false, 1},
		{"both tokens", true, "ci", 3, true, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := runServerOutput(t, dir, tt.printEnrol, tt.ownerName)
			if len(lines) != tt.wantLines {
				t.Fatalf("stdout lines: expected %d, got %d (%q)", tt.wantLines, len(lines), lines)
			}
			if lines[0] != wantFP {
				t.Errorf("first stdout line: expected the control-plane fingerprint %q and nothing else, got %q", wantFP, lines[0])
			}

			ctx := context.Background()
			st := openRunStore(t, dir)
			reg := registry.New(st, time.Now)
			if tt.wantEnrol {
				tok := lines[1]
				if !storedEnrolmentToken(t, dir, tok) {
					t.Errorf("second stdout line %q: expected the enrolment token to be stored, got no enrolment_token_hashes row for its hash", tok)
				}
				if _, err := reg.Authenticate(ctx, tok); !errors.Is(err, registry.ErrUnauthenticated) {
					t.Errorf("Authenticate(%q): expected %v (an enrolment token is not an owner token), got %v", tok, registry.ErrUnauthenticated, err)
				}
			}
			if tt.wantOwnerAt >= 0 {
				tok := lines[tt.wantOwnerAt]
				p, err := reg.Authenticate(ctx, tok)
				if err != nil {
					t.Errorf("Authenticate(line %d %q): expected the owner token, got error %v", tt.wantOwnerAt+1, tok, err)
				} else if p.Role != store.RoleOwner {
					t.Errorf("line %d principal role: expected %q, got %q", tt.wantOwnerAt+1, store.RoleOwner, p.Role)
				}
			}
		})
	}

	second := runServerOutput(t, dir, false, "")
	if len(second) != 1 {
		t.Fatalf("second start stdout lines: expected 1, got %d (%q)", len(second), second)
	}
	if second[0] != wantFP {
		t.Errorf("second start first stdout line: expected %q (the same control-plane fingerprint), got %q", wantFP, second[0])
	}
}
