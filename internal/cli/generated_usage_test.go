package cli_test

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"moonglass.dev/moonglass/internal/cli"
	"moonglass.dev/moonglass/internal/registry"
)

// orderingIn declares two optional fields before two required ones, so a usage
// that lists the flags in plain field order puts the required ones last.
type orderingIn struct {
	Display string `json:"display,omitempty" doc:"Name the test is shown under"`
	Note    string `json:"note,omitempty" doc:"Note about the test"`
	Project string `json:"project" doc:"Project name"`
	Region  string `json:"region" doc:"Region name"`
}

// TestGeneratedUsageListsRequiredFlagsFirst checks that a subcommand's usage
// lists every required flag before every optional one, in field order within
// each group, even when a required field is declared after an optional one.
func TestGeneratedUsageListsRequiredFlagsFirst(t *testing.T) {
	srv := newCaptureServer(t)
	decls := []registry.Declaration{testSpec("test.order", "GET", reflect.TypeFor[orderingIn]())}
	var stdout, stderr bytes.Buffer
	code, err := cli.RunDeclared(decls, []string{"test", "order", "extra"}, srv.env, &stdout, &stderr)
	if err != nil {
		t.Fatalf("cli.RunDeclared(test order) error = %v, want a generated subcommand", err)
	}
	if code != 2 {
		t.Fatalf("test order with a positional argument exit = %d, stderr = %q, want 2", code, stderr.String())
	}
	usage := stderr.String()
	if !strings.Contains(usage, "Usage: moonglass test order") {
		t.Fatalf("stderr = %q, want the usage of test order", usage)
	}

	wantOrder := []string{"--project", "--region", "--display", "--note"}
	last := -1
	for _, flag := range wantOrder {
		at := strings.Index(usage, flag)
		if at < 0 {
			t.Errorf("stderr = %q, want the flag %q listed", usage, flag)
			continue
		}
		if at < last {
			t.Errorf("stderr lists %q at %d, after the flag at %d: want required flags first, then optional ones, each in field order", flag, at, last)
		}
		last = at
	}
	for _, doc := range []string{"Project name", "Region name", "Name the test is shown under", "Note about the test"} {
		if !strings.Contains(usage, doc) {
			t.Errorf("stderr = %q, want the doc %q", usage, doc)
		}
	}
	if got := len(srv.requests()); got != 0 {
		t.Errorf("requests = %d, want 0: a refused call sends nothing", got)
	}
}
