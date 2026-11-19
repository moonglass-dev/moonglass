package proxy_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"moonglass.dev/moonglass/internal/agent/state"
	"moonglass.dev/moonglass/internal/deploy"
	"moonglass.dev/moonglass/internal/proxy"
)

// testEngine connects to the Docker Engine, skips without one, and fails under
// CI, which must provide Docker (docs/specs/deploy-suite.md).
func testEngine(t *testing.T) *deploy.Engine {
	t.Helper()
	engine, err := deploy.Connect(context.Background())
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("deploy.Connect() error = %v, want a Docker Engine: CI must provide Docker", err)
		}
		t.Skipf("no Docker Engine: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	return engine
}

// adminClient returns a client that dials the admin socket at sock.
func adminClient(sock string) *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sock)
			},
		},
	}
}

// runStoppable runs the proxy with its guarded admin socket at sock and returns
// a function that stops it.
func runStoppable(t *testing.T, dataDir, sock string) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- proxy.Run(ctx, dataDir, sock) }()

	admin := adminClient(sock)
	deadline := time.Now().Add(startTimeout)
	for {
		resp, err := admin.Get("http://localhost/config/")
		if err == nil {
			_ = resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("admin API not reachable on %s within %s: %v", sock, startTimeout, err)
		}
		select {
		case err := <-done:
			t.Fatalf("proxy exited before the admin API came up: %v", err)
		case <-time.After(50 * time.Millisecond):
		}
	}

	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("proxy.Run after stop = %v, want nil", err)
				}
			case <-time.After(10 * time.Second):
				t.Errorf("proxy.Run did not stop within 10 s")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

// readConfig returns the decoded body of GET /config/, or the error when the
// proxy is not reachable yet.
func readConfig(c *http.Client) (any, error) {
	resp, err := c.Get("http://localhost/config/")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /config/: status %d, want 200", resp.StatusCode)
	}
	var v any
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// waitForConfig polls GET /config/ until it decodes equal to want.
func waitForConfig(t *testing.T, c *http.Client, want any, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last any
	var lastErr error
	for {
		last, lastErr = readConfig(c)
		if lastErr == nil && reflect.DeepEqual(last, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET /config/ decoded = %#v (error %v), want %#v within %s", last, lastErr, want, timeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestAgentPushLoadsIntoProxy(t *testing.T) {
	engine := testEngine(t)
	dir := tempProxyDir(t)
	dataDir := dir + "/data"
	sock := dir + "/admin.sock"

	stop := runStoppable(t, dataDir, sock)

	db, err := state.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("state.Open error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	pusher := deploy.NewProxy(engine, db, deploy.ProxyConfig{Socket: sock, Issuer: "internal"})
	if err := pusher.Push(context.Background()); err != nil {
		t.Fatalf("Push() error = %v, want nil", err)
	}

	admin := adminClient(sock)
	resp, err := admin.Get("http://localhost/config/apps/http/grace_period")
	if err != nil {
		t.Fatalf("GET /config/apps/http/grace_period: %v", err)
	}
	var gracePeriod any
	decodeErr := json.NewDecoder(resp.Body).Decode(&gracePeriod)
	_ = resp.Body.Close()
	if decodeErr != nil {
		t.Fatalf("decode /config/apps/http/grace_period: %v", decodeErr)
	}
	if got, want := gracePeriod, "30s"; got != want {
		t.Errorf("GET /config/apps/http/grace_period = %v, want %q", got, want)
	}

	want, err := readConfig(admin)
	if err != nil {
		t.Fatalf("GET /config/ after Push(): %v", err)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	var runErr error
	go func() {
		defer close(runDone)
		runErr = pusher.Run(runCtx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-runDone:
		case <-time.After(5 * time.Second):
			t.Errorf("Proxy.Run did not stop")
		}
	})

	stop()
	runStoppable(t, dataDir, sock)

	waitForConfig(t, adminClient(sock), want, 5*time.Second)

	cancel()
	select {
	case <-runDone:
		if runErr != nil {
			t.Errorf("Proxy.Run after cancel = %v, want nil", runErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Proxy.Run did not return within 2 s of cancel")
	}
}
