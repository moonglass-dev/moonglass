package installtest

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestProxyUnitOwnsAdminSocketDir checks the proxy unit runs with the admin
// group, owns the runtime directory, and keeps the guarded socket inside it.
func TestProxyUnitOwnsAdminSocketDir(t *testing.T) {
	_, proxyPath := unitPaths(t)
	u := parseUnit(t, proxyPath)

	if got := u["Service.User"]; len(got) != 1 || got[0] != "moonglass-proxy" {
		t.Errorf("proxy User = %v, want [moonglass-proxy]", got)
	}
	if got := u["Service.Group"]; len(got) != 1 || got[0] != "moonglass-proxy-admin" {
		t.Errorf("proxy Group = %v, want [moonglass-proxy-admin]", got)
	}
	if got := u["Service.RuntimeDirectoryMode"]; len(got) != 1 || got[0] != "0750" {
		t.Errorf("proxy RuntimeDirectoryMode = %v, want [0750]", got)
	}
	if got := u["Service.SupplementaryGroups"]; len(got) != 0 {
		t.Errorf("proxy SupplementaryGroups = %v, want none (the group is the primary one)", got)
	}

	rt := u["Service.RuntimeDirectory"]
	if len(rt) != 1 || rt[0] != "moonglass-proxy" {
		t.Fatalf("proxy RuntimeDirectory = %v, want [moonglass-proxy]", rt)
	}
	want := "/run/" + rt[0] + "/admin.sock"

	exec := fields(u["Service.ExecStart"])
	for i, f := range exec {
		switch {
		case f == "--admin-socket":
			if i+1 >= len(exec) {
				t.Fatalf("proxy ExecStart %v: --admin-socket has no value", exec)
			}
			if got := exec[i+1]; got != want {
				t.Errorf("proxy ExecStart --admin-socket = %q, want %q (the path under the runtime directory)", got, want)
			}
		case strings.HasPrefix(f, "--admin-socket="):
			if got := strings.TrimPrefix(f, "--admin-socket="); got != want {
				t.Errorf("proxy ExecStart --admin-socket = %q, want %q (the path under the runtime directory)", got, want)
			}
		}
	}
}

// TestOnlyAgentJoinsProxyAdminGroup checks that the agent is the only unit that
// holds moonglass-proxy-admin as a supplementary group.
func TestOnlyAgentJoinsProxyAdminGroup(t *testing.T) {
	agentPath, proxyPath := unitPaths(t)

	agent := parseUnit(t, agentPath)
	groups := fields(agent["Service.SupplementaryGroups"])
	for _, want := range []string{"docker", "moonglass-proxy-admin"} {
		if !containsString(groups, want) {
			t.Errorf("agent SupplementaryGroups = %v, want it to contain %s", groups, want)
		}
	}

	proxy := parseUnit(t, proxyPath)
	if got := proxy["Service.Group"]; len(got) != 1 || got[0] != "moonglass-proxy-admin" {
		t.Errorf("proxy Group = %v, want [moonglass-proxy-admin]", got)
	}
	for key, values := range proxy {
		if key == "Service.Group" {
			continue
		}
		for _, v := range values {
			if strings.Contains(v, "moonglass-proxy-admin") {
				t.Errorf("proxy %s = %q, want moonglass-proxy-admin only as Group (no user is a member)", key, v)
			}
		}
	}

	script, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "install"))
	if err != nil {
		t.Fatalf("read install script: %v", err)
	}
	re := regexp.MustCompile(`(?m)^[^#\n]*\b(usermod|gpasswd|adduser|useradd)\b[^\n]*moonglass-proxy-admin`)
	if m := re.FindString(string(script)); m != "" {
		t.Errorf("install script line %q adds a user to moonglass-proxy-admin, want none", m)
	}
}

// TestInstallCreatesProxyAdminGroup checks the host setup creates the admin
// group idempotently, and that a custom-layout install never calls groupadd.
func TestInstallCreatesProxyAdminGroup(t *testing.T) {
	scriptPath := filepath.Join(repoRoot(t), "scripts", "install")
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read install script: %v", err)
	}
	lines := strings.Split(string(data), "\n")

	rootCheck, reload := -1, -1
	for i, line := range lines {
		if rootCheck < 0 && strings.Contains(line, "id -u") && strings.Contains(line, "die") {
			rootCheck = i
		}
		if reload < 0 && strings.Contains(line, "systemctl daemon-reload") {
			reload = i
		}
	}
	if rootCheck < 0 {
		t.Fatalf("scripts/install: no root check line (id -u ... die) found")
	}
	if reload < 0 {
		t.Fatalf("scripts/install: no systemctl daemon-reload line found")
	}
	if rootCheck >= reload {
		t.Fatalf("scripts/install: root check at line %d, want it before systemctl daemon-reload at line %d", rootCheck+1, reload+1)
	}

	groupRE := regexp.MustCompile(`^\s*getent group moonglass-proxy-admin\b.*\|\|\s*groupadd --system moonglass-proxy-admin\s*\|\|\s*die\b`)
	var matches []string
	for _, line := range lines[rootCheck+1 : reload] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if groupRE.MatchString(line) {
			matches = append(matches, trimmed)
		}
	}
	if len(matches) != 1 {
		t.Errorf("host setup group creation between the root check (line %d) and systemctl daemon-reload (line %d): expected exactly one guarded groupadd, got %d: %v", rootCheck+1, reload+1, len(matches), matches)
	}

	keys := t.TempDir()
	priv, pub := newKey(t, keys, "release1")
	e, _ := newEnv(t, pub)
	signAll(t, e, priv, namespace)

	e.shimDir = t.TempDir()
	log := filepath.Join(e.shimDir, "calls.log")
	for _, name := range []string{"groupadd", "getent"} {
		shim := fmt.Sprintf("#!/bin/sh\necho %s \"$@\" >> '%s'\n", name, log)
		if err := os.WriteFile(filepath.Join(e.shimDir, name), []byte(shim), 0o755); err != nil {
			t.Fatalf("write %s shim: %v", name, err)
		}
	}

	stderr, exit := runInstall(t, e)
	if exit != 0 {
		t.Errorf("custom-layout install exit = %d, want 0 without root\nstderr: %s", exit, stderr)
	}
	if _, err := os.Stat(log); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("shim log %s: expected a custom-layout install to call no groupadd or getent, got %v", log, err)
	}
}
