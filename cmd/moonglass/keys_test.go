package main

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"moonglass.dev/moonglass/internal/keys"
)

// recoveryWarning is the ADR-0003 sentence keys init must print.
const recoveryWarning = "If every server in an environment is gone and no recovery key exists, nobody can decrypt its secrets or backups, including us."

func TestKeysInitWritesRecoveryKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recovery.txt")
	var stdout, stderr bytes.Buffer

	code := run([]string{"keys", "init", "--out", path}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit status: expected 0, got %d (stderr %q)", code, stderr.String())
	}
	if got := stderr.String(); got != "" {
		t.Errorf("stderr: expected it empty, got %q", got)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat recovery key file: unexpected error %v", err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o600); got != want {
		t.Errorf("recovery key file mode: expected %v, got %v", want, got)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read recovery key file: unexpected error %v", err)
	}
	if got := strings.Count(string(raw), "\n"); got != 1 || !strings.HasSuffix(string(raw), "\n") {
		t.Errorf("recovery key file: expected exactly one line, got %d newlines", got)
	}
	text := strings.TrimSuffix(string(raw), "\n")
	identity, err := keys.ParseIdentity(text)
	if err != nil {
		t.Fatalf("keys.ParseIdentity of the recovery key file: unexpected error %v", err)
	}

	lines := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
	if len(lines) == 0 || lines[0] != identity.Recipient().String() {
		t.Errorf("stdout first line: expected the recipient %q, got %q", identity.Recipient().String(), lines[0])
	}
	if got := stdout.String(); !strings.Contains(got, recoveryWarning) {
		t.Errorf("stdout: expected the ADR-0003 warning %q, got %q", recoveryWarning, got)
	}
	if got := stdout.String(); strings.Contains(got, text) {
		t.Errorf("stdout: expected it to never contain the identity, but it does")
	}
	if got := stderr.String(); strings.Contains(got, text) {
		t.Errorf("stderr: expected it to never contain the identity, but it does")
	}

	agePath, err := exec.LookPath("age")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("stock age is not on PATH and CI is set: install it (ADR-0015 decision 4)")
		}
		t.Log("stock age is not on PATH: skipping the stock age decrypt check")
		return
	}

	encrypt := exec.Command(agePath, "-r", identity.Recipient().String())
	encrypt.Stdin = strings.NewReader("probe")
	ciphertext, err := encrypt.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			t.Fatalf("age -e -r: %v (stderr %q)", err, exit.Stderr)
		}
		t.Fatalf("age -e -r: %v", err)
	}
	decrypt := exec.Command(agePath, "-d", "-i", path)
	decrypt.Stdin = bytes.NewReader(ciphertext)
	got, err := decrypt.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			t.Fatalf("age -d -i: %v (stderr %q)", err, exit.Stderr)
		}
		t.Fatalf("age -d -i: %v", err)
	}
	if string(got) != "probe" {
		t.Errorf("age plaintext: expected %q, got %q", "probe", got)
	}
}

func TestKeysInitFailures(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.txt")
	if err := os.WriteFile(existing, []byte("keep\n"), 0o644); err != nil {
		t.Fatalf("write existing file: unexpected error %v", err)
	}
	before, err := os.Stat(existing)
	if err != nil {
		t.Fatalf("stat existing file: unexpected error %v", err)
	}
	extra := filepath.Join(dir, "extra.txt")
	missingDir := filepath.Join(dir, "missing")
	missingPath := filepath.Join(missingDir, "recovery.txt")

	tests := []struct {
		name      string
		args      []string
		wantCode  int
		wantUsage bool
		check     func(t *testing.T, stderr string)
	}{
		{
			name:     "existing file",
			args:     []string{"keys", "init", "--out", existing},
			wantCode: 1,
			check: func(t *testing.T, stderr string) {
				t.Helper()
				if !strings.Contains(stderr, existing) {
					t.Errorf("stderr: expected it to name %q, got %q", existing, stderr)
				}
				raw, err := os.ReadFile(existing)
				if err != nil {
					t.Fatalf("read existing file: unexpected error %v", err)
				}
				if got, want := string(raw), "keep\n"; got != want {
					t.Errorf("existing file contents: expected %q, got %q", want, got)
				}
				after, err := os.Stat(existing)
				if err != nil {
					t.Fatalf("stat existing file: unexpected error %v", err)
				}
				if got, want := after.Mode(), before.Mode(); got != want {
					t.Errorf("existing file mode: expected %v, got %v", want, got)
				}
			},
		},
		{
			name:      "missing --out",
			args:      []string{"keys", "init"},
			wantCode:  2,
			wantUsage: true,
		},
		{
			name:      "extra argument",
			args:      []string{"keys", "init", "--out", extra, "extra"},
			wantCode:  2,
			wantUsage: true,
		},
		{
			name:     "missing directory",
			args:     []string{"keys", "init", "--out", missingPath},
			wantCode: 1,
			check: func(t *testing.T, stderr string) {
				t.Helper()
				if !strings.Contains(stderr, missingPath) {
					t.Errorf("stderr: expected it to name %q, got %q", missingPath, stderr)
				}
				if _, err := os.Stat(missingDir); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("directory %q: expected it still missing, got error %v", missingDir, err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := run(tt.args, &stdout, &stderr)

			if code != tt.wantCode {
				t.Errorf("exit status: expected %d, got %d (stderr %q)", tt.wantCode, code, stderr.String())
			}
			if got := stdout.String(); got != "" {
				t.Errorf("stdout: expected it empty, got %q", got)
			}
			if tt.wantUsage {
				if got := stderr.String(); got != usage {
					t.Errorf("stderr: expected usage %q, got %q", usage, got)
				}
			} else if stderr.String() == "" {
				t.Errorf("stderr: expected an error message, got nothing")
			}
			if tt.check != nil {
				tt.check(t, stderr.String())
			}
		})
	}
	if _, err := os.Stat(extra); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("file %q: expected it not created, got error %v", extra, err)
	}
}
