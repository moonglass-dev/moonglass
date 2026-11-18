package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCIPostgresURLMatchesWorkflow(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatalf("read ci.yml: %v", err)
	}
	text := string(b)
	if !strings.Contains(text, "image: "+ciPostgresImage) {
		t.Errorf("ci.yml does not name image %q", ciPostgresImage)
	}
	if !strings.Contains(text, "MOONGLASS_TEST_POSTGRES: "+ciPostgresURL) {
		t.Errorf("ci.yml does not set MOONGLASS_TEST_POSTGRES to %q", ciPostgresURL)
	}
}

func TestEnsureTestPostgresKeepsExistingURL(t *testing.T) {
	const already = "postgres://keep.example/db"
	t.Setenv("MOONGLASS_TEST_POSTGRES", already)
	if err := ensureTestPostgres(); err != nil {
		t.Fatalf("ensureTestPostgres: %v", err)
	}
	if got := os.Getenv("MOONGLASS_TEST_POSTGRES"); got != already {
		t.Errorf("MOONGLASS_TEST_POSTGRES = %q, want the URL already set %q", got, already)
	}
}

func TestEnsureTestPostgresSetsCIURLWhenUnset(t *testing.T) {
	t.Setenv("MOONGLASS_TEST_POSTGRES", "")
	if err := ensureTestPostgres(); err != nil {
		t.Fatalf("ensureTestPostgres: %v", err)
	}
	if got := os.Getenv("MOONGLASS_TEST_POSTGRES"); got != ciPostgresURL {
		t.Errorf("MOONGLASS_TEST_POSTGRES = %q, want %q", got, ciPostgresURL)
	}
}
