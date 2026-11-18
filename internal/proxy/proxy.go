// Package proxy runs the Moonglass proxy: Caddy embedded as a library, its
// admin API on a Unix socket only, and the per-release request counter.
package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/caddyserver/caddy/v2"

	// Caddy modules the proxy serves with. The internal issuer needs the pki
	// app; the route needs the http app and reverse_proxy.
	_ "github.com/caddyserver/caddy/v2/modules/caddyhttp"
	_ "github.com/caddyserver/caddy/v2/modules/caddyhttp/reverseproxy"
	_ "github.com/caddyserver/caddy/v2/modules/caddypki"
	_ "github.com/caddyserver/caddy/v2/modules/caddytls"
	_ "github.com/caddyserver/caddy/v2/modules/filestorage"
	_ "github.com/caddyserver/caddy/v2/modules/logging"
)

const (
	// DefaultAdminSocket is the agent-facing guarded admin socket. It lives in
	// the proxy unit's RuntimeDirectory, so the agent reaches it through the
	// group moonglass-proxy-admin and no other user can traverse to it.
	DefaultAdminSocket   = "/run/moonglass-proxy/admin.sock"
	caddyAdminSocketName = "proxy-caddy.sock"
	storageDirName       = "caddy"
	autosaveName         = "caddy-autosave.json"
)

func caddyAdminSocketPath(dataDir string) string {
	return filepath.Join(dataDir, caddyAdminSocketName)
}

// AdminListenAddress is the value of "admin.listen" every pushed config must
// carry: Caddy's own admin socket, mode 0600, behind the guard. The proxy
// refuses a pushed config with any other "admin".
func AdminListenAddress(dataDir string) string {
	return "unix/" + caddyAdminSocketPath(dataDir) + "|0600"
}

// StorageDir is where Caddy keeps certificates and their private keys. The
// proxy refuses a pushed config whose "storage" is not a file_system module
// rooted here.
func StorageDir(dataDir string) string {
	return filepath.Join(dataDir, storageDirName)
}

// Run starts Caddy with only its admin API on a Unix socket under dataDir, and
// serves the guarded admin API on adminSocket until ctx is done. The agent then
// pushes a complete JSON config to that API. Caddy's own admin socket stays at
// <dataDir>/proxy-caddy.sock. Caddy is process-global, so one Run at a time.
func Run(ctx context.Context, dataDir, adminSocket string) error {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("create proxy data dir %s: %w; check that the service user owns its parent", dataDir, err)
	}
	// A directory needs the owner execute bit; 0700 is the tightest usable mode.
	if err := os.Chmod(dataDir, 0o700); err != nil { //nolint:gosec // G302 expects file modes; this is a directory.
		return fmt.Errorf("restrict proxy data dir %s to its owner: %w", dataDir, err)
	}
	// Caddy autosaves every config it loads. Keep that file under the data dir
	// instead of the service user's home.
	caddy.ConfigAutosavePath = filepath.Join(dataDir, autosaveName)
	initial, err := json.Marshal(map[string]any{
		"admin":   map[string]any{"listen": AdminListenAddress(dataDir)},
		"storage": map[string]any{"module": "file_system", "root": StorageDir(dataDir)},
		"logging": map[string]any{"logs": map[string]any{"default": map[string]any{"level": "WARN"}}},
	})
	if err != nil {
		return fmt.Errorf("encode initial proxy config: %w", err)
	}
	if err = caddy.Load(initial, true); err != nil {
		return fmt.Errorf("start Caddy with its admin API on %s: %w; check that the data dir is writable and that no other proxy uses it", caddyAdminSocketPath(dataDir), err)
	}
	listener, err := listenAdmin(adminSocket)
	if err != nil {
		_ = caddy.Stop()
		return err
	}
	srv := &http.Server{Handler: guardedAdmin(dataDir), ReadHeaderTimeout: 10 * time.Second}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(listener) }()
	var serveErr error
	select {
	case <-ctx.Done():
	case err := <-served:
		serveErr = fmt.Errorf("serve admin socket %s: %w", adminSocket, err)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	if err := caddy.Stop(); err != nil {
		return fmt.Errorf("stop Caddy: %w", err)
	}
	return serveErr
}
