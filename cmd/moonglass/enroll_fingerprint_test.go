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

// agentEnrollUsageLine returns the line of usage that holds the agent enroll synopsis.
func agentEnrollUsageLine(text string) (string, bool) {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "agent enroll ") {
			return strings.TrimSpace(line), true
		}
	}
	return "", false
}

func TestAgentEnrollNeedsServerFingerprint(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "agent")
	token := "mg_" + strings.Repeat("a", 52)

	var stdout, stderr bytes.Buffer
	code := run([]string{"agent", "enroll", "--data-dir", dir, "--url", "https://127.0.0.1:1", token}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit status: expected 2, got %d (stderr %q)", code, stderr.String())
	}
	if got := stderr.String(); got != usage {
		t.Errorf("stderr: expected the usage text %q, got %q", usage, got)
	}
	if got := stdout.String(); got != "" {
		t.Errorf("stdout: expected it empty, got %q", got)
	}
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("agent data dir %s: expected it not to exist, got error %v", dir, err)
	}
	if strings.Contains(stderr.String(), token) {
		t.Errorf("stderr leaks the token: %q", stderr.String())
	}

	line, ok := agentEnrollUsageLine(usage)
	if !ok {
		t.Fatalf("usage: expected an agent enroll line, got:\n%s", usage)
	}
	if !strings.Contains(line, "--server-fingerprint FP") {
		t.Errorf("agent enroll usage line: expected it to contain %q so the missing flag is what exits 2, got %q",
			"--server-fingerprint FP", line)
	}
}
