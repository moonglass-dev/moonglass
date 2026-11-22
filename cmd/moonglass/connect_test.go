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
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"moonglass.dev/moonglass/internal/agent/state"
	"moonglass.dev/moonglass/internal/deploy"
	"moonglass.dev/moonglass/internal/policy"
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

	// Executor.Run's start-up reconcile must leave another run's managed
	// containers alone on a shared Engine (docs/specs/deploy-suite.md), so the
	// command's Engine goes through a proxy that reports no containers.
	host := os.Getenv("DOCKER_HOST")
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	realSock, ok := strings.CutPrefix(host, "unix://")
	if !ok {
		t.Fatalf("DOCKER_HOST = %q, want a unix:// socket (ADR-0014)", host)
	}
	engineDir, err := os.MkdirTemp("", "mg")
	if err != nil {
		t.Fatalf("MkdirTemp error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(engineDir) })
	engineSock := filepath.Join(engineDir, "engine.sock")
	engineLn, err := net.Listen("unix", engineSock)
	if err != nil {
		t.Fatalf("listen on %q error = %v, want nil", engineSock, err)
	}
	reverse := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: "docker"})
	reverse.Transport = &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", realSock)
	}}
	engineServer := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/containers/json") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, "[]")
			return
		}
		reverse.ServeHTTP(w, r)
	})}
	go func() { _ = engineServer.Serve(engineLn) }()
	t.Cleanup(func() { _ = engineServer.Close() })
	t.Setenv("DOCKER_HOST", "unix://"+engineSock)

	// Enrol a fresh data directory against a real control plane.
	serverURL, serverFP, token := newTestServer(t)
	dir := t.TempDir()
	var enrollOut, enrollErr bytes.Buffer
	if code := run([]string{"agent", "enroll", "--data-dir", dir, "--url", serverURL, "--server-fingerprint", serverFP, token}, &enrollOut, &enrollErr); code != 0 {
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

func TestAgentConnectIssuerFollowsDeployCheck(t *testing.T) {
	tests := []struct {
		name   string
		issuer string
	}{
		{"acme", "acme"},
		{"acme staging", "acme-staging"},
		{"internal", "internal"},
		{"letsencrypt", "letsencrypt"},
		{"upper case", "ACME"},
		{"leading space", " internal"},
		{"empty", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseAgentConnect([]string{"--url", "https://x", "--tls-issuer", tt.issuer})
			checkErr := deploy.CheckIssuer(tt.issuer)
			wantOK := checkErr == nil
			if ok != wantOK {
				t.Fatalf("parseAgentConnect with --tls-issuer %q ok = %v, want %v (CheckIssuer(%q) = %v)",
					tt.issuer, ok, wantOK, tt.issuer, checkErr)
			}
			if !ok {
				return
			}
			if got.proxy.Issuer != tt.issuer {
				t.Errorf("proxy Issuer = %q, want %q", got.proxy.Issuer, tt.issuer)
			}
		})
	}
}

func TestAgentConnectPolicyFlagDefault(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
		ok   bool
	}{
		{"no flag", []string{"--url", "https://x"}, "/etc/moonglass/agent.toml", true},
		{"given file", []string{"--url", "https://x", "--policy", "/tmp/agent.toml"}, "/tmp/agent.toml", true},
		{"empty file", []string{"--url", "https://x", "--policy", ""}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseAgentConnect(tt.args)
			if ok != tt.ok {
				t.Fatalf("parseAgentConnect(%q) ok = %v, want %v", tt.args, ok, tt.ok)
			}
			if !ok {
				return
			}
			if got.policyFile != tt.want {
				t.Errorf("policyFile = %q, want %q", got.policyFile, tt.want)
			}
		})
	}
}

func TestAgentConnectRefusesPolicyThatDoesNotLoad(t *testing.T) {
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

	// A listener that counts every connection, so a dial is observable.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for the control plane error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	var accepts atomic.Int64
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			accepts.Add(1)
			_ = c.Close()
		}
	}()
	addr := listener.Addr().String()

	files := []struct {
		name string
		body string
		mode os.FileMode
	}{
		{"unsafe mode", "", 0o666},
		{"invalid TOML", "[[[", 0o600},
	}
	for _, tt := range files {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(t.TempDir(), "agent.toml")
			if err := os.WriteFile(file, []byte(tt.body), 0o600); err != nil {
				t.Fatalf("write %s error = %v, want nil", file, err)
			}
			if err := os.Chmod(file, tt.mode); err != nil {
				t.Fatalf("chmod %s error = %v, want nil", file, err)
			}

			wantErr := func() error {
				_, err := policy.LoadFile(file, policy.Env{}, 0)
				return err
			}()
			if wantErr == nil {
				t.Fatalf("policy.LoadFile(%q) error = nil, want the error the command must print", file)
			}

			var stderr syncBuffer
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			code := agentConnect(ctx, []string{
				"--data-dir", dir,
				"--url", "https://" + addr,
				"--policy", file,
				"--proxy-socket", filepath.Join(t.TempDir(), "admin.sock"),
				"--tls-issuer", "internal",
			}, &stderr)

			if code != 1 {
				t.Errorf("exit status = %d, want 1 (stderr %q)", code, stderr.String())
			}
			if got, want := stderr.String(), "moonglass: "+wantErr.Error()+"\n"; got != want {
				t.Errorf("stderr = %q, want %q", got, want)
			}
			dbPath := filepath.Join(dir, "agent.db")
			if _, err := os.Stat(dbPath); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("stat %q: expected it absent, got error %v: the policy is loaded before the database", dbPath, err)
			}
			if got := accepts.Load(); got != 0 {
				t.Errorf("TCP connections to the control plane = %d, want 0", got)
			}
		})
	}
}
