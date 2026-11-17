package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"reflect"
)

// maxConfigBytes bounds a pushed config.
const maxConfigBytes = 16 << 20

// guardedAdmin serves the admin socket the agent talks to. It forwards reads
// to Caddy's own admin API (on a second socket) and lets exactly one mutation
// through: POST /load of a JSON config that keeps the admin API on its socket
// and the certificate keys under the data dir. Everything else is refused, so
// no pushed config or config edit can move the admin API to TCP or the keys
// elsewhere.
func guardedAdmin(dataDir string) http.Handler {
	inner := caddyAdminSocketPath(dataDir)
	forward := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = "localhost"
			pr.Out.Host = pr.In.Host
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", inner)
			},
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet || r.Method == http.MethodHead:
			forward.ServeHTTP(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/load":
			raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxConfigBytes))
			if err != nil {
				http.Error(w, fmt.Sprintf("read config: %v", err), http.StatusBadRequest)
				return
			}
			if err := checkConfig(dataDir, raw); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(raw))
			r.ContentLength = int64(len(raw))
			forward.ServeHTTP(w, r)
		default:
			http.Error(w, fmt.Sprintf("%s %s is not allowed on the proxy admin socket; push a complete config with POST /load, or read with GET", r.Method, r.URL.Path), http.StatusForbidden)
		}
	})
}

// checkConfig refuses a config whose admin API or storage is not the one the
// proxy requires.
func checkConfig(dataDir string, raw []byte) error {
	var cfg struct {
		Admin   map[string]any `json:"admin"`
		Storage map[string]any `json:"storage"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("config is not a JSON object: %w; push a complete JSON config", err)
	}
	wantAdmin := map[string]any{"listen": AdminListenAddress(dataDir)}
	if !reflect.DeepEqual(cfg.Admin, wantAdmin) {
		return fmt.Errorf("config \"admin\" must be exactly %s; the admin API stays on the Unix socket", mustJSON(wantAdmin))
	}
	wantStorage := map[string]any{"module": "file_system", "root": StorageDir(dataDir)}
	if !reflect.DeepEqual(cfg.Storage, wantStorage) {
		return fmt.Errorf("config \"storage\" must be exactly %s; certificate keys stay under the data dir", mustJSON(wantStorage))
	}
	return nil
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

// listenAdmin opens the guarded admin socket, mode 0600.
func listenAdmin(dataDir string) (net.Listener, error) {
	path := AdminSocketPath(dataDir)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("remove stale admin socket %s: %w", path, err)
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen on admin socket %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = l.Close()
		return nil, fmt.Errorf("restrict admin socket %s to its owner: %w", path, err)
	}
	return l, nil
}
