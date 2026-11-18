package proxy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"moonglass.dev/moonglass/internal/proxy"
)

func TestAdminSocketAtGivenPath(t *testing.T) {
	dataDir, sock, admin := startProxy(t)

	if strings.HasPrefix(sock, dataDir+string(filepath.Separator)) {
		t.Fatalf("admin socket %s: expected it outside the data dir %s", sock, dataDir)
	}
	resp, err := admin.Get("http://localhost/config/")
	if err != nil {
		t.Fatalf("GET /config/ over %s: %v", sock, err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /config/ over %s: expected status 200, got %d", sock, resp.StatusCode)
	}

	old := filepath.Join(dataDir, "proxy-admin.sock")
	if _, err := os.Stat(old); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("stat %s: expected not-exist (the admin socket lives only at the given path), got %v", old, err)
	}
}

func TestAdminSocketModeAndGroup(t *testing.T) {
	dir := tempProxyDir(t)
	// Give the parent another group and set its setgid bit, so a new file
	// inherits that group unless the proxy chowns the socket to its own
	// effective group.
	groups, err := os.Getgroups()
	if err != nil {
		t.Fatalf("read the process groups: %v", err)
	}
	for _, gid := range groups {
		if gid == os.Getegid() {
			continue
		}
		if err := os.Chown(dir, -1, gid); err != nil {
			t.Fatalf("chown %s to gid %d: %v", dir, gid, err)
		}
		if err := os.Chmod(dir, 0o750|os.ModeSetgid); err != nil {
			t.Fatalf("set group inheritance on %s: %v", dir, err)
		}
		break
	}
	dataDir := dir + "/data"
	sock := dir + "/admin.sock"
	runProxy(t, dataDir, sock)

	info, err := os.Stat(sock)
	if err != nil {
		t.Fatalf("stat %s: %v", sock, err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		t.Errorf("%s: expected a unix socket, got mode %v", sock, info.Mode())
	}
	if perm := info.Mode().Perm(); perm != 0o660 {
		t.Errorf("%s: expected mode 0660 exactly, got %04o", sock, perm)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("stat %s: expected a *syscall.Stat_t, got %T", sock, info.Sys())
	}
	if want, got := uint32(os.Getegid()), st.Gid; got != want {
		t.Errorf("%s: expected group %d (the proxy's effective group), got %d", sock, want, got)
	}
}

func TestCaddyAdminSocketStaysPrivate(t *testing.T) {
	dataDir, sock, admin := startProxy(t)

	inner := filepath.Join(dataDir, "proxy-caddy.sock")
	info, err := os.Stat(inner)
	if err != nil {
		t.Fatalf("stat %s: %v", inner, err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		t.Errorf("%s: expected a unix socket, got mode %v", inner, info.Mode())
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("%s: expected mode 0600 or tighter, got %04o", inner, perm)
	}
	dirInfo, err := os.Stat(dataDir)
	if err != nil {
		t.Fatalf("stat %s: %v", dataDir, err)
	}
	if perm := dirInfo.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("%s: expected mode 0700 or tighter, got %04o", dataDir, perm)
	}

	storage := map[string]any{"module": "file_system", "root": proxy.StorageDir(dataDir)}
	for _, listen := range []string{"unix/" + sock, "unix/" + sock + "|0660"} {
		cfg, err := json.Marshal(map[string]any{
			"admin":   map[string]any{"listen": listen},
			"storage": storage,
		})
		if err != nil {
			t.Fatalf("admin.listen %q: marshal config: %v", listen, err)
		}
		resp, err := admin.Post("http://localhost/load", "application/json", bytes.NewReader(cfg))
		if err != nil {
			t.Fatalf("admin.listen %q: push config: %v", listen, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("admin.listen %q: expected status 400 (the agent-facing socket is outside Caddy's reach), got %d: %s", listen, resp.StatusCode, body)
		}
	}

	good, err := json.Marshal(map[string]any{
		"admin":   map[string]any{"listen": proxy.AdminListenAddress(dataDir)},
		"storage": storage,
		"logging": map[string]any{"logs": map[string]any{"default": map[string]any{"level": "WARN"}}},
	})
	if err != nil {
		t.Fatalf("marshal accepted config: %v", err)
	}
	push(t, admin, good)
}

func TestAdminSocketRefusesNonSocketPath(t *testing.T) {
	dir := tempProxyDir(t)
	dataDir := filepath.Join(dir, "data")
	sock := filepath.Join(dir, "admin.sock")
	const content = "not a socket\n"
	if err := os.WriteFile(sock, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", sock, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()
	err := proxy.Run(ctx, dataDir, sock)
	if err == nil {
		t.Fatalf("proxy.Run with a regular file at %s: expected an error, got nil", sock)
	}
	if !strings.Contains(err.Error(), sock) {
		t.Errorf("error %q: expected it to name %s", err, sock)
	}
	if !strings.Contains(err.Error(), "not a socket") {
		t.Errorf("error %q: expected it to say the path is not a socket", err)
	}
	got, readErr := os.ReadFile(sock)
	if readErr != nil {
		t.Fatalf("read %s: expected the file to be left alone, got %v", sock, readErr)
	}
	if string(got) != content {
		t.Errorf("%s: expected content %q, got %q", sock, content, got)
	}
}
