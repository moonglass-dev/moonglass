package agent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"moonglass.dev/moonglass/internal/keys"
	"moonglass.dev/moonglass/internal/protocol"
	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
)

// freshFingerprint returns the fingerprint of a key that no test server presents.
func freshFingerprint(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: unexpected error %v", err)
	}
	fp, err := keys.Fingerprint(pub)
	if err != nil {
		t.Fatalf("Fingerprint: unexpected error %v", err)
	}
	return fp
}

func TestEnrollPinsFingerprintVerifiedKey(t *testing.T) {
	e := newEnv(t)
	tok := e.issue(t)
	dir := t.TempDir()

	fp, err := Enroll(context.Background(), dir, e.ts.URL, e.fp, tok)
	if err != nil {
		t.Fatalf("Enroll over TLS with the server's fingerprint: unexpected error %v", err)
	}
	if fp == "" {
		t.Errorf("returned fingerprint: expected the agent fingerprint, got %q", fp)
	}

	pin := readFile(t, dir, pinFile)

	conn, err := tls.Dial("tcp", e.ts.Listener.Addr().String(), keys.EnrolClientTLSConfig(e.fp))
	if err != nil {
		t.Fatalf("tls.Dial with the server's fingerprint: unexpected error %v", err)
	}
	defer func() { _ = conn.Close() }()
	state := conn.ConnectionState()
	if n := len(state.PeerCertificates); n != 1 {
		t.Fatalf("peer certificates: expected exactly 1, got %d", n)
	}
	leaf, err := keys.PeerPublicKey([][]byte{state.PeerCertificates[0].Raw})
	if err != nil {
		t.Fatalf("PeerPublicKey: unexpected error %v", err)
	}
	if !bytes.Equal(pin, leaf) {
		t.Errorf("%s: expected the TLS leaf key %x, got %x", pinFile, []byte(leaf), pin)
	}
	leafFP, err := keys.Fingerprint(leaf)
	if err != nil {
		t.Fatalf("Fingerprint(leaf): unexpected error %v", err)
	}
	if leafFP != e.fp {
		t.Errorf("TLS leaf fingerprint: expected the server fingerprint %q, got %q", e.fp, leafFP)
	}
}

func TestEnrollFingerprintMismatchSendsNoToken(t *testing.T) {
	var requests atomic.Int64
	e := newEnvWith(t, func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			h.ServeHTTP(w, r)
		})
	})
	tok := e.issue(t)
	dir := t.TempDir()
	wantFP := freshFingerprint(t)

	fp, err := Enroll(context.Background(), dir, e.ts.URL, wantFP, tok)
	if err == nil {
		t.Fatalf("Enroll with a wrong fingerprint: expected an error, got fingerprint %q", fp)
	}
	if got := requests.Load(); got != 0 {
		t.Errorf("requests the control plane handled: expected 0 (the handshake fails first), got %d", got)
	}
	if snap := snapshot(t, dir); len(snap) != 0 {
		t.Errorf("data dir after a refused enrol: expected no files, got %v", snap)
	}
	if strings.Contains(err.Error(), tok) {
		t.Errorf("error leaks the token: %q", err)
	}

	// The token is still unused: the same token enrols with the right fingerprint.
	if _, err := Enroll(context.Background(), t.TempDir(), e.ts.URL, e.fp, tok); err != nil {
		t.Errorf("Enroll with the right fingerprint after the mismatch: expected success, got %v", err)
	}
}

func TestEnrollRefusesPlainHTTP(t *testing.T) {
	var requests atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
	}))
	t.Cleanup(ts.Close)
	base, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("url.Parse(%q): unexpected error %v", ts.URL, err)
	}
	port := base.Port()
	tok := "mg_" + strings.Repeat("a", 52)
	fp := freshFingerprint(t)

	tests := []struct {
		name string
		url  string
	}{
		{"http on localhost", "http://localhost:" + port},
		{"http on a loopback IP", "http://127.0.0.1:" + port},
		{"http on a non-loopback host", "http://example.invalid"},
		{"uppercase scheme", "HTTP://127.0.0.1:" + port},
		{"no scheme", "127.0.0.1:" + port},
		{"wss scheme", "wss://127.0.0.1:" + port},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()

			gotFP, err := Enroll(context.Background(), dir, tt.url, fp, tok)
			if err == nil {
				t.Fatalf("Enroll against %q: expected an error, got fingerprint %q", tt.url, gotFP)
			}
			if !strings.Contains(err.Error(), "https://") {
				t.Errorf("Enroll against %q error: expected it to name https://, got %q", tt.url, err)
			}
			if strings.Contains(err.Error(), tok) {
				t.Errorf("Enroll against %q error leaks the token: %q", tt.url, err)
			}
			if got := requests.Load(); got != 0 {
				t.Errorf("requests the plain-HTTP listener handled for %q: expected 0, got %d", tt.url, got)
			}
			if snap := snapshot(t, dir); len(snap) != 0 {
				t.Errorf("data dir after refusing %q: expected no files, got %v", tt.url, snap)
			}
		})
	}
}

func TestEnrollRejectsResponseKeyOtherThanTLSKey(t *testing.T) {
	pubA, privA, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey(server): unexpected error %v", err)
	}
	pubB, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey(other): unexpected error %v", err)
	}
	fpA, err := keys.Fingerprint(pubA)
	if err != nil {
		t.Fatalf("Fingerprint: unexpected error %v", err)
	}
	serverCfg, err := keys.ListenerTLSConfig(privA)
	if err != nil {
		t.Fatalf("ListenerTLSConfig: unexpected error %v", err)
	}

	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		envelope, err := protocol.Wrap(agentv1.MessageType_ENROLL_RESPONSE, "enroll", &agentv1.EnrollResponse{
			Result:                agentv1.EnrollResult_ACCEPTED,
			ControlPlanePublicKey: pubB,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		body, err := protocol.Marshal(envelope)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(body)
	}))
	ts.TLS = serverCfg
	ts.StartTLS()
	t.Cleanup(ts.Close)

	dir := t.TempDir()
	tok := "mg_" + strings.Repeat("b", 52)
	fp, err := Enroll(context.Background(), dir, ts.URL, fpA, tok)
	if err == nil {
		t.Fatalf("Enroll from a server that proved key A but answered key B: expected an error, got fingerprint %q", fp)
	}
	if strings.Contains(err.Error(), tok) {
		t.Errorf("Enroll from a server that proved key A but answered key B: expected an error without the token, got %q", err)
	}
	for _, name := range []string{pinFile, fingerFile} {
		if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s after a response key mismatch: expected it absent, got error %v", name, err)
		}
	}
}
