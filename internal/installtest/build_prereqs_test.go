package installtest

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAgentUnitMemoryMaxFitsBuilds checks the agent unit's memory cap covers the
// git and docker buildx children a build runs (owner decision D19), while the
// proxy keeps its own smaller cap.
func TestAgentUnitMemoryMaxFitsBuilds(t *testing.T) {
	agentPath, proxyPath := unitPaths(t)
	tests := []struct {
		name string
		path string
		want string
	}{
		{"agent", agentPath, "512M"},
		{"proxy", proxyPath, "64M"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseUnit(t, tt.path)["Service.MemoryMax"]
			if len(got) != 1 || got[0] != tt.want {
				t.Errorf("%s Service.MemoryMax = %v, want exactly [%s]", tt.name, got, tt.want)
			}
		})
	}
}

// Shim bodies run as `#!/bin/sh` scripts from a PATH directory the install
// script searches first.
const (
	gitOK   = "echo 'git version 2.40.0'\n"
	exit127 = "exit 127\n"

	// dockerBuildxOK succeeds only for `buildx version`.
	dockerBuildxOK = "if [ \"$1\" = buildx ] && [ \"$2\" = version ]; then exit 0; fi\nexit 1\n"

	// dockerBuildxFails reports the missing plugin and exits 1.
	dockerBuildxFails = "echo 'docker: buildx is not a docker command' >&2\nexit 1\n"
)

// writeShim writes an executable sh script called name into dir.
func writeShim(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatalf("write %s shim: %v", name, err)
	}
}

// installWithPrereqs runs a full custom-layout install whose PATH finds only
// the given git and docker shims, and returns the install's stderr. It fails
// unless the install exits 0 and installs the binary and both units.
func installWithPrereqs(t *testing.T, gitBody, dockerBody string) string {
	t.Helper()
	keys := t.TempDir()
	priv, pub := newKey(t, keys, "release1")
	e, binary := newEnv(t, pub)
	signAll(t, e, priv, namespace)
	e.shimDir = t.TempDir()
	writeShim(t, e.shimDir, "git", gitBody)
	writeShim(t, e.shimDir, "docker", dockerBody)

	stderr, exit := runInstall(t, e)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0 (a missing prerequisite must never fail the install)\nstderr: %s", exit, stderr)
	}
	got, err := os.ReadFile(e.dest())
	if err != nil || !bytes.Equal(got, binary) {
		t.Errorf("installed binary = %d bytes (err %v), want the %d-byte fixture", len(got), err, len(binary))
	}
	for _, name := range unitNames {
		if _, err := os.Stat(filepath.Join(e.unitDir, name)); err != nil {
			t.Errorf("unit %s not installed in %s: %v", name, e.unitDir, err)
		}
	}
	return stderr
}

// linesWith returns the stderr lines that contain every substring in subs.
func linesWith(stderr string, subs ...string) []string {
	var out []string
	for _, line := range strings.Split(stderr, "\n") {
		if line == "" {
			continue
		}
		matched := true
		for _, sub := range subs {
			if !strings.Contains(line, sub) {
				matched = false
				break
			}
		}
		if matched {
			out = append(out, line)
		}
	}
	return out
}

// TestInstallWarnsWhenGitMissing checks a missing git produces one warning and
// still installs everything.
func TestInstallWarnsWhenGitMissing(t *testing.T) {
	stderr := installWithPrereqs(t, exit127, dockerBuildxOK)

	if got := linesWith(stderr, "git 2.30", "git builds"); len(got) != 1 {
		t.Errorf("stderr lines naming git 2.30 and git builds = %d (%v), want exactly 1\nstderr: %s", len(got), got, stderr)
	}
	if got := linesWith(stderr, "buildx"); len(got) != 0 {
		t.Errorf("stderr lines naming buildx = %v, want none when only git is missing\nstderr: %s", got, stderr)
	}
}

// TestInstallWarnsWhenGitTooOld checks the version parse and the 2.30 minimum.
func TestInstallWarnsWhenGitTooOld(t *testing.T) {
	tests := []struct {
		output  string
		wantGit bool
	}{
		{"git version 2.29.9", true},
		{"git version 1.9.5", true},
		{"git version garbage", true},
		{"git version 2.30.0", false},
		{"git version 2.39.3 (Apple Git-146)", false},
		{"git version 3.0.1", false},
	}
	for _, tt := range tests {
		t.Run(tt.output, func(t *testing.T) {
			stderr := installWithPrereqs(t, fmt.Sprintf("printf '%%s\\n' '%s'\n", tt.output), dockerBuildxOK)

			want := 0
			if tt.wantGit {
				want = 1
			}
			if got := linesWith(stderr, "git 2.30", "git builds"); len(got) != want {
				t.Errorf("git %q: stderr lines naming git 2.30 and git builds = %d (%v), want %d\nstderr: %s", tt.output, len(got), got, want, stderr)
			}
			if got := linesWith(stderr, "buildx"); len(got) != 0 {
				t.Errorf("git %q: stderr lines naming buildx = %v, want none when buildx works\nstderr: %s", tt.output, got, stderr)
			}
		})
	}
}

// TestInstallWarnsWhenBuildxMissing checks a missing docker or buildx plugin
// produces one warning and still installs everything.
func TestInstallWarnsWhenBuildxMissing(t *testing.T) {
	tests := []struct {
		name       string
		dockerBody string
	}{
		{"buildx plugin missing", dockerBuildxFails},
		{"docker missing", exit127},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stderr := installWithPrereqs(t, gitOK, tt.dockerBody)

			if got := linesWith(stderr, "buildx", "git builds"); len(got) != 1 {
				t.Errorf("stderr lines naming buildx and git builds = %d (%v), want exactly 1\nstderr: %s", len(got), got, stderr)
			}
			if got := linesWith(stderr, "git 2.30"); len(got) != 0 {
				t.Errorf("stderr lines naming git 2.30 = %v, want none when git is new enough\nstderr: %s", got, stderr)
			}
		})
	}
}

// TestInstallQuietWhenBuildPrereqsPresent checks nothing is printed when both
// prerequisites work, that the check only runs `git --version` and
// `docker buildx version`, and that each missing one gets its own line.
func TestInstallQuietWhenBuildPrereqsPresent(t *testing.T) {
	t.Run("present", func(t *testing.T) {
		logDir := t.TempDir()
		log := filepath.Join(logDir, "calls.log")
		gitBody := fmt.Sprintf("echo \"git $*\" >> '%s'\n%s", log, gitOK)
		dockerBody := fmt.Sprintf("echo \"docker $*\" >> '%s'\n%s", log, dockerBuildxOK)

		stderr := installWithPrereqs(t, gitBody, dockerBody)

		if got := linesWith(stderr, "git builds"); len(got) != 0 {
			t.Errorf("stderr lines naming git builds = %v, want none when both prerequisites are present\nstderr: %s", got, stderr)
		}

		data, err := os.ReadFile(log)
		if err != nil {
			t.Fatalf("no prerequisite command ran (log %s): %v", log, err)
		}
		var gitCalls, dockerCalls []string
		for _, line := range strings.Split(string(data), "\n") {
			switch {
			case strings.HasPrefix(line, "git "):
				gitCalls = append(gitCalls, strings.TrimPrefix(line, "git "))
			case strings.HasPrefix(line, "docker "):
				dockerCalls = append(dockerCalls, strings.TrimPrefix(line, "docker "))
			}
		}
		if len(gitCalls) == 0 {
			t.Errorf("git calls = %v, want at least one --version call", gitCalls)
		}
		for _, call := range gitCalls {
			if call != "--version" {
				t.Errorf("git call = %q, want only \"--version\"", call)
			}
		}
		if len(dockerCalls) == 0 {
			t.Errorf("docker calls = %v, want at least one buildx version call", dockerCalls)
		}
		for _, call := range dockerCalls {
			if call != "buildx version" {
				t.Errorf("docker call = %q, want only \"buildx version\"", call)
			}
		}
	})

	t.Run("both missing", func(t *testing.T) {
		stderr := installWithPrereqs(t, exit127, exit127)

		got := linesWith(stderr, "git builds")
		if len(got) != 2 {
			t.Errorf("stderr lines naming git builds = %d (%v), want exactly 2, one per prerequisite\nstderr: %s", len(got), got, stderr)
		}
		if n := len(linesWith(stderr, "git 2.30")); n != 1 {
			t.Errorf("stderr lines naming git 2.30 = %d, want exactly 1\nstderr: %s", n, stderr)
		}
		if n := len(linesWith(stderr, "buildx")); n != 1 {
			t.Errorf("stderr lines naming buildx = %d, want exactly 1\nstderr: %s", n, stderr)
		}
	})
}
