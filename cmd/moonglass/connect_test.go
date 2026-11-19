package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"moonglass.dev/moonglass/internal/agent/state"
	"moonglass.dev/moonglass/internal/deploy"
)

func TestAgentConnectProxyFlagDefaults(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantSocket string
		wantIssuer string
	}{
		{"neither flag", []string{"--url", "https://x"}, "/run/moonglass-proxy/admin.sock", "acme"},
		{"both flags", []string{"--url", "https://x", "--proxy-socket", "/tmp/a.sock", "--tls-issuer", "internal"}, "/tmp/a.sock", "internal"},
		{"acme staging", []string{"--url", "https://x", "--tls-issuer", "acme-staging"}, "/run/moonglass-proxy/admin.sock", "acme-staging"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseAgentConnect(tt.args)
			if !ok {
				t.Fatalf("parseAgentConnect(%q) ok = false, want true", tt.args)
			}
			if got.proxy.Socket != tt.wantSocket {
				t.Errorf("proxy Socket = %q, want %q", got.proxy.Socket, tt.wantSocket)
			}
			if got.proxy.Issuer != tt.wantIssuer {
				t.Errorf("proxy Issuer = %q, want %q", got.proxy.Issuer, tt.wantIssuer)
			}
		})
	}
}

func TestAgentConnectRejectsBadProxyFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"empty proxy socket", []string{"--proxy-socket", ""}},
		{"unknown tls issuer", []string{"--tls-issuer", "letsencrypt"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			args := append([]string{"agent", "connect", "--data-dir", dir, "--url", "https://127.0.0.1:1"}, tt.args...)

			var stdout, stderr bytes.Buffer
			code := run(args, &stdout, &stderr)

			if code != 2 {
				t.Errorf("exit status: expected 2, got %d (stderr %q)", code, stderr.String())
			}
			if stderr.String() != usage {
				t.Errorf("stderr: expected usage %q, got %q", usage, stderr.String())
			}
			if got := stdout.String(); got != "" {
				t.Errorf("stdout: expected it empty, got %q", got)
			}
			dbPath := filepath.Join(dir, "agent.db")
			if _, err := os.Stat(dbPath); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("stat %q: expected it absent before anything opens, got error %v", dbPath, err)
			}
		})
	}
}

// syncBuffer is a bytes.Buffer that both the command's goroutines and the test
// may write and read under -race.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// requireJSONEqual compares two JSON documents by value.
func requireJSONEqual(t *testing.T, field string, got json.RawMessage, want string) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("decode %s = %s: %v", field, got, err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("decode wanted %s = %s: %v", field, want, err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Errorf("%s = %s, want %s", field, got, want)
	}
}

func TestAgentConnectRunsProxyPush(t *testing.T) {
	engine, err := deploy.Connect(context.Background())
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("deploy.Connect() error = %v, want a Docker Engine: CI must provide Docker", err)
		}
		t.Skipf("no Docker Engine: %v", err)
	}
	if err := engine.Close(); err != nil {
		t.Fatalf("engine.Close() error = %v, want nil", err)
	}

	// Enrol a fresh data directory against a real control plane.
	serverURL, token := newTestServer(t)
	dir := t.TempDir()
	var enrollOut, enrollErr bytes.Buffer
	if code := run([]string{"agent", "enroll", "--data-dir", dir, "--url", serverURL, token}, &enrollOut, &enrollErr); code != 0 {
		t.Fatalf("agent enroll exit status = %d, want 0 (stderr %q)", code, enrollErr.String())
	}

	// A control-plane URL that refuses connections, so the connect loop keeps
	// backing off while the proxy push runs beside it.
	refusing, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for a refusing address error = %v, want nil", err)
	}
	refusingAddr := refusing.Addr().String()
	if err := refusing.Close(); err != nil {
		t.Fatalf("close the refusing listener error = %v, want nil", err)
	}

	// A short directory: macOS limits a Unix socket path to 104 bytes, which a
	// t.TempDir() path can exceed.
	sockDir, err := os.MkdirTemp("", "mg")
	if err != nil {
		t.Fatalf("MkdirTemp error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	sock := filepath.Join(sockDir, "admin.sock")

	loaded := make(chan []byte, 16)
	mux := http.NewServeMux()
	mux.HandleFunc("/config/admin", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"listen":"unix//x"}`)
	})
	mux.HandleFunc("/config/storage", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"module":"file_system","root":"/x"}`)
	})
	mux.HandleFunc("/config/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{}`)
	})
	mux.HandleFunc("/load", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			http.Error(w, readErr.Error(), http.StatusInternalServerError)
			return
		}
		loaded <- body
		w.WriteHeader(http.StatusOK)
	})

	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen on %q error = %v, want nil", sock, err)
	}
	srv := httptest.NewUnstartedServer(mux)
	srv.Listener = listener
	srv.Start()
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stderr syncBuffer
	done := make(chan int, 1)
	go func() {
		done <- agentConnect(ctx, []string{
			"--data-dir", dir,
			"--url", "https://" + refusingAddr,
			"--proxy-socket", sock,
			"--tls-issuer", "internal",
		}, &stderr)
	}()

	var body []byte
	select {
	case body = <-loaded:
	case <-time.After(5 * time.Second):
		t.Fatalf("no POST /load within 5 s, want one complete config (stderr %q)", stderr.String())
	}

	var doc struct {
		Admin   json.RawMessage            `json:"admin"`
		Storage json.RawMessage            `json:"storage"`
		Apps    map[string]json.RawMessage `json:"apps"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode the pushed config %s: %v", body, err)
	}
	requireJSONEqual(t, "admin", doc.Admin, `{"listen":"unix//x"}`)
	requireJSONEqual(t, "storage", doc.Storage, `{"module":"file_system","root":"/x"}`)
	for _, app := range []string{"http", "tls"} {
		if _, ok := doc.Apps[app]; !ok {
			t.Errorf("apps.%s is absent from the pushed config %s, want it present", app, body)
		}
	}

	select {
	case code := <-done:
		t.Fatalf("agent connect returned %d before the context was cancelled, want it still running (stderr %q)", code, stderr.String())
	case <-time.After(1500 * time.Millisecond):
	}

	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit status after cancel = %d, want 0 (stderr %q)", code, stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("agent connect did not return within 10 s of cancel (stderr %q)", stderr.String())
	}

	wal := filepath.Join(dir, "agent.db-wal")
	if _, err := os.Stat(wal); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("stat %q: expected it absent after exit, got error %v: the database must be closed", wal, err)
	}
	db, err := state.Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("state.Open(%q) after exit error = %v, want nil: the database must be closed", dir, err)
	}
	if err := db.Close(); err != nil {
		t.Errorf("db.Close() error = %v, want nil", err)
	}
}
