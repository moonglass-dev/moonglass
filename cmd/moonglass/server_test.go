package main

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// serverUsageLine returns the line of usage that holds the server synopsis.
func serverUsageLine(text string) (string, bool) {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "server ") {
			return strings.TrimSpace(line), true
		}
	}
	return "", false
}

func TestServerOwnerTokenFlagNeedsName(t *testing.T) {
	tests := []struct {
		name string
		args func(dir string) []string
	}{
		{"empty string value", func(dir string) []string {
			return []string{"server", "--data-dir", dir, "--print-owner-token", ""}
		}},
		{"equals with no value", func(dir string) []string {
			return []string{"server", "--data-dir", dir, "--print-owner-token="}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "data")
			var stdout, stderr bytes.Buffer

			code := run(tt.args(dir), &stdout, &stderr)

			if code != 2 {
				t.Errorf("exit status: expected 2, got %d", code)
			}
			if got := stderr.String(); got != usage {
				t.Errorf("stderr: expected the usage text %q, got %q", usage, got)
			}
			if got := stdout.String(); got != "" {
				t.Errorf("stdout: expected it empty, got %q", got)
			}
			if _, err := os.Stat(filepath.Join(dir, "server.db")); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("server.db: expected it not to exist, got err %v", err)
			}
			// The server line must advertise the flag: without it the arguments
			// above are rejected as an undefined flag, not for the empty name.
			line, ok := serverUsageLine(usage)
			if !ok {
				t.Fatalf("usage: expected a server line, got:\n%s", usage)
			}
			if !strings.Contains(line, "[--print-owner-token NAME]") {
				t.Errorf("server line: expected it to contain %q so the exit is the empty-name rule, got %q",
					"[--print-owner-token NAME]", line)
			}
		})
	}
}

func TestUsageListsOwnerTokenFlag(t *testing.T) {
	line, ok := serverUsageLine(usage)
	if !ok {
		t.Fatalf("usage: expected a server line, got:\n%s", usage)
	}
	if !strings.Contains(line, "[--print-owner-token NAME]") {
		t.Errorf("server line: expected it to contain %q, got %q\nfull usage:\n%s",
			"[--print-owner-token NAME]", line, usage)
	}
}
