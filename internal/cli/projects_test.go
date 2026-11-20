package cli_test

import (
	"bytes"
	"encoding/json"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"moonglass.dev/moonglass/internal/cli"
	"moonglass.dev/moonglass/internal/registry"
)

// runDeclared runs the generated subcommand the shipped operations declare for
// args and returns its exit code and stderr.
func runDeclared(t *testing.T, srv *captureServer, args []string) (int, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code, err := cli.RunDeclared(registry.Operations(), args, srv.env, &stdout, &stderr)
	if err != nil {
		t.Fatalf("cli.RunDeclared(%q) error = %v, want a generated subcommand", args, err)
	}
	return code, stderr.String()
}

// TestGeneratedProjectEnvironmentServiceCommands checks the six generated
// subcommands: their flags, the request each sends, and that every flag is
// required.
func TestGeneratedProjectEnvironmentServiceCommands(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		method string
		path   string
		query  url.Values
		body   map[string]any
	}{
		{
			name:   "projects create",
			args:   []string{"--name", "web"},
			method: "POST", path: "/v1/projects",
			body: map[string]any{"name": "web"},
		},
		{
			name:   "projects list",
			method: "GET", path: "/v1/projects",
			query: url.Values{},
		},
		{
			name:   "environments create",
			args:   []string{"--project", "web", "--name", "prod"},
			method: "POST", path: "/v1/environments",
			body: map[string]any{"project": "web", "name": "prod"},
		},
		{
			name:   "environments list",
			args:   []string{"--project", "web"},
			method: "GET", path: "/v1/environments",
			query: url.Values{"project": {"web"}},
		},
		{
			name:   "services create",
			args:   []string{"--project", "web", "--env", "prod", "--name", "app", "--server", "srv-1"},
			method: "POST", path: "/v1/services",
			body: map[string]any{"project": "web", "environment": "prod", "name": "app", "server": "srv-1"},
		},
		{
			name:   "services list",
			args:   []string{"--project", "web", "--env", "prod"},
			method: "GET", path: "/v1/services",
			query: url.Values{"project": {"web"}, "environment": {"prod"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newCaptureServer(t)
			args := append(strings.Fields(tt.name), tt.args...)
			code, stderr := runDeclared(t, srv, args)
			if code != 0 {
				t.Fatalf("%s with every flag exit = %d, stderr = %q, want 0", tt.name, code, stderr)
			}
			reqs := srv.requests()
			if len(reqs) != 1 {
				t.Fatalf("%s requests = %d, want 1", tt.name, len(reqs))
			}
			got := reqs[0]
			if got.method != tt.method || got.path != tt.path {
				t.Errorf("%s request = %s %s, want %s %s", tt.name, got.method, got.path, tt.method, tt.path)
			}
			if tt.query != nil {
				query, err := url.ParseQuery(got.rawQuery)
				if err != nil {
					t.Fatalf("%s parse query %q: %v", tt.name, got.rawQuery, err)
				}
				if !reflect.DeepEqual(query, tt.query) {
					t.Errorf("%s query = %v, want %v", tt.name, query, tt.query)
				}
				if got.body != "" {
					t.Errorf("%s body = %q, want an empty body", tt.name, got.body)
				}
				return
			}
			if got.rawQuery != "" {
				t.Errorf("%s query = %q, want no query string", tt.name, got.rawQuery)
			}
			var body map[string]any
			if err := json.Unmarshal([]byte(got.body), &body); err != nil {
				t.Fatalf("%s body %q is not one JSON object: %v", tt.name, got.body, err)
			}
			if !reflect.DeepEqual(body, tt.body) {
				t.Errorf("%s body = %v, want %v", tt.name, body, tt.body)
			}
		})
	}

	for _, tt := range tests {
		for i := 0; i < len(tt.args); i += 2 {
			flag := tt.args[i]
			t.Run(tt.name+"/without "+flag, func(t *testing.T) {
				args := append([]string{}, strings.Fields(tt.name)...)
				args = append(args, tt.args[:i]...)
				args = append(args, tt.args[i+2:]...)
				srv := newCaptureServer(t)
				code, stderr := runDeclared(t, srv, args)
				if code != 2 {
					t.Errorf("%s without %s exit = %d, stderr = %q, want 2", tt.name, flag, code, stderr)
				}
				if want := "moonglass: " + tt.name + ": " + flag + " is required"; !strings.Contains(stderr, want) {
					t.Errorf("%s without %s stderr = %q, want %q in it", tt.name, flag, stderr, want)
				}
				if got := len(srv.requests()); got != 0 {
					t.Errorf("%s without %s requests = %d, want 0: the call is refused before anything is sent", tt.name, flag, got)
				}
			})
		}
	}
}
