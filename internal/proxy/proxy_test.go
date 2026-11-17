package proxy_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"moonglass.dev/moonglass/internal/proxy"
)

const startTimeout = 15 * time.Second

// startProxy runs the proxy in a short temp directory (unix socket paths are
// limited to about 100 bytes) and returns the data dir and an admin client.
func startProxy(t *testing.T) (string, *http.Client) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "mgproxy")
	if err != nil {
		t.Fatalf("create data dir: %v", err)
	}
	dataDir := dir + "/data"

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var runErr error
	go func() {
		defer close(done)
		runErr = proxy.Run(ctx, dataDir)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		if runErr != nil {
			t.Errorf("proxy.Run: expected nil after cancel, got %v", runErr)
		}
		_ = os.RemoveAll(dir)
	})

	admin := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", proxy.AdminSocketPath(dataDir))
			},
		},
	}
	deadline := time.Now().Add(startTimeout)
	for {
		resp, err := admin.Get("http://localhost/config/")
		if err == nil {
			_ = resp.Body.Close()
			return dataDir, admin
		}
		if time.Now().After(deadline) {
			t.Fatalf("admin API not reachable on %s within %s: %v", proxy.AdminSocketPath(dataDir), startTimeout, err)
		}
		select {
		case <-done:
			t.Fatalf("proxy exited before the admin API came up: %v", runErr)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find a free port: %v", err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

// routeConfig is a complete JSON config: one HTTPS route for localhost that
// counts requests for release and proxies them to upstream, with certificates
// from Caddy's internal issuer.
func routeConfig(t *testing.T, dataDir string, port int, upstream, release string) []byte {
	t.Helper()
	cfg := map[string]any{
		"admin":   map[string]any{"listen": proxy.AdminListenAddress(dataDir)},
		"storage": map[string]any{"module": "file_system", "root": proxy.StorageDir(dataDir)},
		"logging": map[string]any{"logs": map[string]any{"default": map[string]any{"level": "WARN"}}},
		"apps": map[string]any{
			"pki": map[string]any{"certificate_authorities": map[string]any{
				"local": map[string]any{"install_trust": false},
			}},
			"tls": map[string]any{"automation": map[string]any{"policies": []any{
				map[string]any{
					"subjects": []string{"localhost"},
					"issuers":  []any{map[string]any{"module": "internal"}},
				},
			}}},
			"http": map[string]any{
				"https_port": port,
				"servers": map[string]any{"main": map[string]any{
					"listen":          []string{fmt.Sprintf("127.0.0.1:%d", port)},
					"automatic_https": map[string]any{"disable_redirects": true},
					"routes": []any{map[string]any{
						"match": []any{map[string]any{"host": []string{"localhost"}}},
						"handle": []any{
							map[string]any{"handler": "moonglass_counter", "release": release},
							map[string]any{
								"handler":   "reverse_proxy",
								"upstreams": []any{map[string]any{"dial": upstream}},
							},
						},
					}},
				}},
			},
		},
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return raw
}

func push(t *testing.T, admin *http.Client, cfg []byte) {
	t.Helper()
	resp, err := admin.Post("http://localhost/load", "application/json", bytes.NewReader(cfg))
	if err != nil {
		t.Fatalf("push config: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("push config: expected status 200, got %d: %s", resp.StatusCode, body)
	}
}

// httpsClient trusts only the root of Caddy's local CA, fetched over the admin
// socket.
func httpsClient(t *testing.T, admin *http.Client) *http.Client {
	t.Helper()
	resp, err := admin.Get("http://localhost/pki/ca/local")
	if err != nil {
		t.Fatalf("fetch local CA: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var ca struct {
		RootCertificate string `json:"root_certificate"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ca); err != nil {
		t.Fatalf("decode local CA: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(ca.RootCertificate)) {
		t.Fatalf("local CA root certificate is not valid PEM: %q", ca.RootCertificate)
	}
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			RootCAs:    pool,
			ServerName: "localhost",
			MinVersion: tls.VersionTLS12,
		}},
	}
}

func get(t *testing.T, c *http.Client, url string) string {
	t.Helper()
	deadline := time.Now().Add(startTimeout)
	for {
		resp, err := c.Get(url)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET %s: expected status 200, got %d: %s", url, resp.StatusCode, body)
			}
			return string(body)
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET %s: %v", url, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestAdminAPIOnUnixSocket(t *testing.T) {
	dataDir, admin := startProxy(t)

	sock := proxy.AdminSocketPath(dataDir)
	info, err := os.Stat(sock)
	if err != nil {
		t.Fatalf("stat %s: %v", sock, err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		t.Errorf("%s: expected a unix socket, got mode %v", sock, info.Mode())
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("%s: expected mode 0600 or tighter, got %04o", sock, perm)
	}
	dirInfo, err := os.Stat(dataDir)
	if err != nil {
		t.Fatalf("stat %s: %v", dataDir, err)
	}
	if perm := dirInfo.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("%s: expected mode 0700 or tighter, got %04o", dataDir, perm)
	}
	resp, err := admin.Get("http://localhost/config/")
	if err != nil {
		t.Fatalf("GET /config/ over the socket: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /config/: expected status 200, got %d", resp.StatusCode)
	}
}

func TestNoTCPAdminPort(t *testing.T) {
	dataDir, admin := startProxy(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer upstream.Close()
	// A pushed config that carries the admin address must not open a TCP port.
	push(t, admin, routeConfig(t, dataDir, freePort(t), upstream.Listener.Addr().String(), "rel-no-tcp"))

	for _, addr := range []string{"127.0.0.1:2019", "[::1]:2019"} {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			_ = conn.Close()
			t.Errorf("dial %s: expected connection refused, but something is listening (Caddy's default admin port?)", addr)
		}
	}
}

func TestPushedConfigCannotOpenTCPAdmin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	dataDir, admin := startProxy(t)

	fileStorage := map[string]any{"module": "file_system", "root": proxy.StorageDir(dataDir)}
	adminSock := map[string]any{"listen": proxy.AdminListenAddress(dataDir)}
	route := map[string]any{"http": map[string]any{"servers": map[string]any{}}}
	cases := map[string]map[string]any{
		"no admin key":     {"storage": fileStorage, "apps": route},
		"tcp admin listen": {"admin": map[string]any{"listen": "127.0.0.1:2019"}, "storage": fileStorage, "apps": route},
		"admin remote": {
			"admin":   map[string]any{"listen": proxy.AdminListenAddress(dataDir), "remote": map[string]any{"listen": "127.0.0.1:2021"}},
			"storage": fileStorage, "apps": route,
		},
		"no storage": {"admin": adminSock, "apps": route},
		"other storage": {
			"admin":   adminSock,
			"storage": map[string]any{"module": "file_system", "root": home},
			"apps":    route,
		},
	}
	for name, cfg := range cases {
		raw, err := json.Marshal(cfg)
		if err != nil {
			t.Fatalf("%s: marshal config: %v", name, err)
		}
		resp, err := admin.Post("http://localhost/load", "application/json", bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("%s: push config: %v", name, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Errorf("%s: expected the proxy to refuse the config, got status 200", name)
		}
		if len(body) == 0 {
			t.Errorf("%s: expected an error message in the response body, got none", name)
		}
	}

	for _, addr := range []string{"127.0.0.1:2019", "[::1]:2019", "127.0.0.1:2021"} {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			_ = conn.Close()
			t.Errorf("dial %s: expected connection refused, but something is listening", addr)
		}
	}
	resp, err := admin.Get("http://localhost/config/")
	if err != nil {
		t.Fatalf("GET /config/ over the socket after refused configs: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /config/: expected status 200, got %d", resp.StatusCode)
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatalf("read %s: %v", home, err)
	}
	if len(entries) != 0 {
		t.Errorf("expected nothing written under HOME, found %d entries (first: %s)", len(entries), entries[0].Name())
	}
}

func TestAdminSocketRefusesOtherMutations(t *testing.T) {
	_, admin := startProxy(t)

	for _, req := range []struct{ method, path string }{
		{http.MethodPost, "/stop"},
		{http.MethodDelete, "/config/admin"},
		{http.MethodPut, "/config/storage"},
	} {
		r, err := http.NewRequest(req.method, "http://localhost"+req.path, bytes.NewReader([]byte("{}")))
		if err != nil {
			t.Fatalf("build %s %s: %v", req.method, req.path, err)
		}
		resp, err := admin.Do(r)
		if err != nil {
			t.Fatalf("%s %s: %v", req.method, req.path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode < 400 {
			t.Errorf("%s %s: expected the proxy to refuse it, got status %d", req.method, req.path, resp.StatusCode)
		}
	}
	resp, err := admin.Get("http://localhost/config/")
	if err != nil {
		t.Fatalf("GET /config/ after refused mutations: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /config/: expected status 200, got %d", resp.StatusCode)
	}
}

func TestOneHTTPSRouteInternalIssuer(t *testing.T) {
	dataDir, admin := startProxy(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "hello from upstream")
	}))
	defer upstream.Close()
	port := freePort(t)
	push(t, admin, routeConfig(t, dataDir, port, upstream.Listener.Addr().String(), "rel-https"))

	body := get(t, httpsClient(t, admin), fmt.Sprintf("https://localhost:%d/", port))

	if want := "hello from upstream"; body != want {
		t.Errorf("GET body: expected %q, got %q", want, body)
	}
}

func TestCounterRecordsRequest(t *testing.T) {
	dataDir, admin := startProxy(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()
	port := freePort(t)
	push(t, admin, routeConfig(t, dataDir, port, upstream.Listener.Addr().String(), "rel-counter"))
	get(t, httpsClient(t, admin), fmt.Sprintf("https://localhost:%d/", port))

	resp, err := admin.Get("http://localhost/moonglass/counters")
	if err != nil {
		t.Fatalf("GET /moonglass/counters: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var snap proxy.Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		t.Fatalf("decode counters: %v", err)
	}

	got, ok := snap.Releases["rel-counter"]
	if !ok {
		t.Fatalf("counters: expected release rel-counter, got %+v", snap.Releases)
	}
	if got.Requests != 1 {
		t.Errorf("requests: expected 1, got %d", got.Requests)
	}
	if got.Status["2xx"] != 1 {
		t.Errorf("status 2xx: expected 1, got %d (all: %v)", got.Status["2xx"], got.Status)
	}
	if got.LatencyNanosTotal == 0 {
		t.Errorf("latency_nanos_total: expected a positive value, got 0")
	}
	var inBuckets uint64
	for _, n := range got.LatencyBuckets {
		inBuckets += n
	}
	if inBuckets != 1 {
		t.Errorf("latency buckets: expected 1 request in total, got %d (%v)", inBuckets, got.LatencyBuckets)
	}
}
