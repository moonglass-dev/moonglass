package api_test

import (
	"bytes"
	"flag"
	"os"
	"testing"

	"moonglass.dev/moonglass/internal/api"
)

var update = flag.Bool("update", false, "rewrite the checked-in generated files")

func TestGeneratedOpenAPIIsCurrent(t *testing.T) {
	const path = "openapi.json"
	got, err := api.OpenAPI()
	if err != nil {
		t.Fatalf("api.OpenAPI error = %v, want nil", err)
	}
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil { //nolint:gosec // checked-in generated file
			t.Fatalf("write %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v; run make generate", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is stale: generated %d bytes, checked in %d bytes; run make generate", path, len(got), len(want))
	}
}
