package main

import (
	"bytes"
	"strings"
	"testing"

	"moonglass.dev/moonglass/internal/proxy"
)

func TestProxyAdminSocketDefault(t *testing.T) {
	const want = "/run/moonglass-proxy/admin.sock"
	if got := proxy.DefaultAdminSocket; got != want {
		t.Errorf("proxy.DefaultAdminSocket: expected %q, got %q", want, got)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"proxy", "--help"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("exit status: expected 2, got %d (stdout %q, stderr %q)", code, stdout.String(), stderr.String())
	}

	help := stderr.String()
	if !strings.Contains(help, "-admin-socket string") {
		t.Errorf("flag help: expected it to describe -admin-socket as a string flag, got %q", help)
	}
	if wantDefault := `(default "` + proxy.DefaultAdminSocket + `")`; !strings.Contains(help, wantDefault) {
		t.Errorf("flag help: expected it to show the default %s, got %q", wantDefault, help)
	}
}

func TestProxyRejectsEmptyAdminSocket(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"proxy", "--data-dir", "/tmp/x", "--admin-socket", ""}, &stdout, &stderr)

	if code != 2 {
		t.Errorf("exit status: expected 2, got %d", code)
	}
	if got := stderr.String(); got != usage {
		t.Errorf("stderr: expected %q, got %q", usage, got)
	}
	if got := stdout.String(); got != "" {
		t.Errorf("stdout: expected it empty, got %q", got)
	}
}
