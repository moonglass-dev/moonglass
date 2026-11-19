package agent

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestAgentPackageHoldsNoKeyFileIO keeps the agent data-dir file names, their
// formats and their atomic writes inside internal/keys (ticket FR1-3).
func TestAgentPackageHoldsNoKeyFileIO(t *testing.T) {
	needles := []string{"agent-signing.key", "agent-agree.key", "control-plane.pub", `"fingerprint"`}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir: unexpected error %v", err)
	}
	var found []string
	scanned := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: unexpected error %v", name, err)
		}
		scanned++
		for _, needle := range needles {
			if bytes.Contains(src, []byte(needle)) {
				found = append(found, fmt.Sprintf("%s: %s", name, needle))
			}
		}
	}
	if scanned == 0 {
		t.Fatalf("scanned files: expected at least one non-test .go file in internal/agent, got 0 in %q", ".")
	}
	if len(found) != 0 {
		t.Errorf("key file IO in internal/agent: expected 0 matches (internal/keys owns the four files), got %d: %v", len(found), found)
	}
}
