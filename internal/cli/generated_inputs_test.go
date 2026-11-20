package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"moonglass.dev/moonglass/internal/cli"
	"moonglass.dev/moonglass/internal/registry"
	"moonglass.dev/moonglass/internal/store"
)

// testIn is the input of the test operations: one field of each kind, in field
// order, with two required and five optional.
type testIn struct {
	Project             string   `json:"project" doc:"Project name"`
	Environment         string   `json:"environment" doc:"Environment name"`
	HealthPath          string   `json:"health_path,omitempty" doc:"Path the health check calls"`
	PreDeployReversible bool     `json:"pre_deploy_reversible,omitempty" doc:"Whether the pre-deploy step can be undone"`
	Domain              []string `json:"domain,omitempty" doc:"Public domain, repeatable"`
	Port                int64    `json:"port,omitempty" doc:"Port the service listens on"`
	Name                string   `json:"name,omitempty" doc:"Name of the test"`
}

// testOut is the empty output of the test operations.
type testOut struct{}

// testDeclarations registers test.get, test.post and test.put and pairs each
// registered Spec with testIn, the way registry.Operations() lists servers.list.
func testDeclarations(t *testing.T) []registry.Declaration {
	t.Helper()
	r := registry.New(nil, time.Now)
	specs := []registry.Spec{
		{Name: "test.get", Summary: "Get a test", Method: "GET", Path: "/v1/test", MinRole: store.RoleViewer, Destructive: registry.No},
		{Name: "test.post", Summary: "Create a test", Method: "POST", Path: "/v1/test", MinRole: store.RoleViewer, Destructive: registry.No},
		{Name: "test.put", Summary: "Replace a test", Method: "PUT", Path: "/v1/test", MinRole: store.RoleViewer, Destructive: registry.No},
	}
	decls := make([]registry.Declaration, 0, len(specs))
	for _, spec := range specs {
		op, err := registry.Register(r, spec, func(context.Context, registry.Call, testIn) (testOut, error) {
			return testOut{}, nil
		})
		if err != nil {
			t.Fatalf("registry.Register(%q) error = %v, want nil", spec.Name, err)
		}
		decls = append(decls, registry.Declaration{Spec: op.Spec, In: reflect.TypeFor[testIn]()})
	}
	return decls
}

// testSpec returns the declaration of a test operation with in as its input.
func testSpec(name, method string, in reflect.Type) registry.Declaration {
	return registry.Declaration{
		Spec: registry.Spec{Name: name, Summary: "A test", Method: method, Path: "/v1/test", MinRole: store.RoleViewer, Destructive: registry.No},
		In:   in,
	}
}

// capturedRequest is one request the test server received.
type capturedRequest struct {
	method      string
	path        string
	rawQuery    string
	contentType string
	body        string
}

// captureServer records every request and replies with the status and body set
// by reply.
type captureServer struct {
	*httptest.Server
	mu     sync.Mutex
	status int
	body   string
	reqs   []capturedRequest
}

func newCaptureServer(t *testing.T) *captureServer {
	t.Helper()
	cs := &captureServer{status: http.StatusOK, body: `{}`}
	cs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read the request body: %v", err)
			return
		}
		cs.mu.Lock()
		cs.reqs = append(cs.reqs, capturedRequest{
			method:      r.Method,
			path:        r.URL.Path,
			rawQuery:    r.URL.RawQuery,
			contentType: r.Header.Get("Content-Type"),
			body:        string(body),
		})
		status, reply := cs.status, cs.body
		cs.mu.Unlock()
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(cs.Close)
	return cs
}

// reply sets the status and body the next requests are answered with.
func (cs *captureServer) reply(status int, body string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.status, cs.body = status, body
}

// requests returns the requests received so far.
func (cs *captureServer) requests() []capturedRequest {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return append([]capturedRequest(nil), cs.reqs...)
}

// env returns the environment the runner reads its URL and token from.
func (cs *captureServer) env(key string) string {
	return map[string]string{"MOONGLASS_URL": cs.URL, "MOONGLASS_TOKEN": "mg_test"}[key]
}

// runTest runs one test subcommand with args and returns its exit code, stdout
// and stderr.
func runTest(t *testing.T, srv *captureServer, command, args []string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	argv := append(append([]string{}, command...), args...)
	code, err := cli.RunDeclared(testDeclarations(t), argv, srv.env, &stdout, &stderr)
	if err != nil {
		t.Fatalf("cli.RunDeclared(%q) error = %v, want a generated subcommand to run", argv, err)
	}
	return code, stdout.String(), stderr.String()
}

// TestGeneratedFlagNames checks that each input has exactly one flag, named from
// its json tag, and that no alias or short flag exists.
func TestGeneratedFlagNames(t *testing.T) {
	srv := newCaptureServer(t)
	code, _, stderr := runTest(t, srv, []string{"test", "get"}, []string{"--project", "p", "--env", "e", "--health-path", "/h", "--pre-deploy-reversible"})
	if code != 0 {
		t.Fatalf("test get with the flags of every input exit = %d, stderr = %q, want 0", code, stderr)
	}
	reqs := srv.requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	got, err := url.ParseQuery(reqs[0].rawQuery)
	if err != nil {
		t.Fatalf("parse query %q: %v", reqs[0].rawQuery, err)
	}
	want := url.Values{
		"project":               {"p"},
		"environment":           {"e"},
		"health_path":           {"/h"},
		"pre_deploy_reversible": {"true"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("query = %v, want %v", got, want)
	}

	for _, tt := range []struct {
		name string
		args []string
	}{
		{"json name instead of env", []string{"--environment", "e"}},
		{"json name instead of hyphens", []string{"--health_path", "/h"}},
		{"short flag", []string{"-p", "p"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := newCaptureServer(t)
			code, _, stderr := runTest(t, srv, []string{"test", "get"}, tt.args)
			if code != 2 {
				t.Errorf("exit = %d, stderr = %q, want 2", code, stderr)
			}
			if !strings.Contains(stderr, "Usage: moonglass test get") {
				t.Errorf("stderr = %q, want the usage of test get", stderr)
			}
			if got := len(srv.requests()); got != 0 {
				t.Errorf("requests = %d, want 0: the call is refused before anything is sent", got)
			}
		})
	}
}

// TestGeneratedFlagsSentAsQueryOrBody checks that a GET sends the given flags as
// query parameters and POST and PUT as one JSON body, each with only the flags
// given.
func TestGeneratedFlagsSentAsQueryOrBody(t *testing.T) {
	args := []string{"--project", "p", "--env", "e", "--name", "n"}
	tests := []struct {
		command string
		method  string
	}{
		{"test get", "GET"},
		{"test post", "POST"},
		{"test put", "PUT"},
	}
	for _, tt := range tests {
		t.Run(tt.command+" "+tt.method, func(t *testing.T) {
			srv := newCaptureServer(t)
			code, _, stderr := runTest(t, srv, strings.Fields(tt.command), args)
			if code != 0 {
				t.Fatalf("exit = %d, stderr = %q, want 0", code, stderr)
			}
			reqs := srv.requests()
			if len(reqs) != 1 {
				t.Fatalf("requests = %d, want 1", len(reqs))
			}
			got := reqs[0]
			if got.method != tt.method || got.path != "/v1/test" {
				t.Errorf("request = %s %s, want %s /v1/test", got.method, got.path, tt.method)
			}

			if tt.method == "GET" {
				query, err := url.ParseQuery(got.rawQuery)
				if err != nil {
					t.Fatalf("parse query %q: %v", got.rawQuery, err)
				}
				wantQuery := url.Values{"project": {"p"}, "environment": {"e"}, "name": {"n"}}
				if !reflect.DeepEqual(query, wantQuery) {
					t.Errorf("query = %v, want %v", query, wantQuery)
				}
				if got.body != "" {
					t.Errorf("body = %q, want an empty body", got.body)
				}
				return
			}

			if got.rawQuery != "" {
				t.Errorf("query = %q, want no query string", got.rawQuery)
			}
			if got.contentType != "application/json" {
				t.Errorf("Content-Type = %q, want %q", got.contentType, "application/json")
			}
			var body map[string]any
			if err := json.Unmarshal([]byte(got.body), &body); err != nil {
				t.Fatalf("body %q is not one JSON object: %v", got.body, err)
			}
			wantBody := map[string]any{"project": "p", "environment": "e", "name": "n"}
			if !reflect.DeepEqual(body, wantBody) {
				t.Errorf("body = %v, want %v", body, wantBody)
			}
		})
	}
}

// TestGeneratedRepeatedBooleanAndIntegerFlags checks lists, booleans and
// integers, and that a non-decimal integer is refused before anything is sent.
func TestGeneratedRepeatedBooleanAndIntegerFlags(t *testing.T) {
	tests := []struct {
		name      string
		command   []string
		args      []string
		wantQuery url.Values
		wantBody  map[string]any
		wantCode  int
		wantErr   string
	}{
		{
			name:      "list repeats in order for GET",
			command:   []string{"test", "get"},
			args:      []string{"--project", "p", "--env", "e", "--domain", "a.example", "--domain", "b.example"},
			wantQuery: url.Values{"project": {"p"}, "environment": {"e"}, "domain": {"a.example", "b.example"}},
		},
		{
			name:     "list repeats in order for POST",
			command:  []string{"test", "post"},
			args:     []string{"--project", "p", "--env", "e", "--domain", "a.example", "--domain", "b.example"},
			wantBody: map[string]any{"project": "p", "environment": "e", "domain": []any{"a.example", "b.example"}},
		},
		{
			name:     "boolean flag sends true",
			command:  []string{"test", "post"},
			args:     []string{"--project", "p", "--env", "e", "--pre-deploy-reversible"},
			wantBody: map[string]any{"project": "p", "environment": "e", "pre_deploy_reversible": true},
		},
		{
			name:     "boolean flag sends false",
			command:  []string{"test", "post"},
			args:     []string{"--project", "p", "--env", "e", "--pre-deploy-reversible=false"},
			wantBody: map[string]any{"project": "p", "environment": "e", "pre_deploy_reversible": false},
		},
		{
			name:     "integer flag sends a JSON number",
			command:  []string{"test", "post"},
			args:     []string{"--project", "p", "--env", "e", "--port", "8080"},
			wantBody: map[string]any{"project": "p", "environment": "e", "port": float64(8080)},
		},
		{
			name:      "integer flag sends a decimal query parameter",
			command:   []string{"test", "get"},
			args:      []string{"--project", "p", "--env", "e", "--port", "8080"},
			wantQuery: url.Values{"project": {"p"}, "environment": {"e"}, "port": {"8080"}},
		},
		{
			name:     "value that starts with a hyphen",
			command:  []string{"test", "post"},
			args:     []string{"--project", "p", "--env", "e", "--name=-v"},
			wantBody: map[string]any{"project": "p", "environment": "e", "name": "-v"},
		},
		{
			name:     "non-decimal integer is refused",
			command:  []string{"test", "get"},
			args:     []string{"--project", "p", "--env", "e", "--port", "8x"},
			wantCode: 2,
			wantErr:  "--port must be an integer",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newCaptureServer(t)
			code, _, stderr := runTest(t, srv, tt.command, tt.args)
			if code != tt.wantCode {
				t.Fatalf("exit = %d, stderr = %q, want %d", code, stderr, tt.wantCode)
			}
			if tt.wantCode != 0 {
				if !strings.Contains(stderr, tt.wantErr) {
					t.Errorf("stderr = %q, want %q in it", stderr, tt.wantErr)
				}
				if got := len(srv.requests()); got != 0 {
					t.Errorf("requests = %d, want 0: the call is refused before anything is sent", got)
				}
				return
			}
			reqs := srv.requests()
			if len(reqs) != 1 {
				t.Fatalf("requests = %d, want 1", len(reqs))
			}
			got := reqs[0]
			if tt.wantQuery != nil {
				query, err := url.ParseQuery(got.rawQuery)
				if err != nil {
					t.Fatalf("parse query %q: %v", got.rawQuery, err)
				}
				if !reflect.DeepEqual(query, tt.wantQuery) {
					t.Errorf("query = %v, want %v", query, tt.wantQuery)
				}
				return
			}
			var body map[string]any
			if err := json.Unmarshal([]byte(got.body), &body); err != nil {
				t.Fatalf("body %q is not one JSON object: %v", got.body, err)
			}
			if !reflect.DeepEqual(body, tt.wantBody) {
				t.Errorf("body = %v, want %v", body, tt.wantBody)
			}
		})
	}
}

// TestGeneratedMissingRequiredFlagExits2 checks that the first required flag
// missing in field order is named, the exit is 2 and nothing is sent.
func TestGeneratedMissingRequiredFlagExits2(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"both required flags missing", nil, "moonglass: test get: --project is required"},
		{"second required flag missing", []string{"--project", "p"}, "moonglass: test get: --env is required"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := newCaptureServer(t)
			code, _, stderr := runTest(t, srv, []string{"test", "get"}, tt.args)
			if code != 2 {
				t.Errorf("exit = %d, stderr = %q, want 2", code, stderr)
			}
			if !strings.Contains(stderr, tt.want) {
				t.Errorf("stderr = %q, want %q in it", stderr, tt.want)
			}
			if got := len(srv.requests()); got != 0 {
				t.Errorf("requests = %d, want 0: a missing required flag sends nothing", got)
			}
		})
	}
}

// TestGeneratedRefusesPositionalAndUnknownFlags checks that a positional
// argument and an unknown flag each print the subcommand's usage, listing every
// flag with the required ones first and each doc, and send nothing.
func TestGeneratedRefusesPositionalAndUnknownFlags(t *testing.T) {
	wantFlags := []string{"--project", "--env", "--health-path", "--pre-deploy-reversible", "--domain", "--port", "--name"}
	wantDocs := []string{
		"Project name",
		"Environment name",
		"Path the health check calls",
		"Whether the pre-deploy step can be undone",
		"Public domain, repeatable",
		"Port the service listens on",
		"Name of the test",
	}
	for _, tt := range []struct {
		name string
		args []string
	}{
		{"positional argument", []string{"extra"}},
		{"unknown flag", []string{"--nope"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := newCaptureServer(t)
			code, _, stderr := runTest(t, srv, []string{"test", "get"}, tt.args)
			if code != 2 {
				t.Errorf("exit = %d, stderr = %q, want 2", code, stderr)
			}
			if !strings.Contains(stderr, "Usage: moonglass test get") {
				t.Errorf("stderr = %q, want the usage of test get", stderr)
			}
			last := -1
			for i, flag := range wantFlags {
				at := strings.Index(stderr, flag)
				if at < 0 {
					t.Errorf("%s = %q, want the flag %q listed", tt.name, stderr, flag)
					continue
				}
				if at < last {
					t.Errorf("stderr lists %q at %d, after the next flag at %d: want required flags first, then optional ones in field order", flag, at, last)
				}
				last = at
				if !strings.Contains(stderr, wantDocs[i]) {
					t.Errorf("stderr = %q, want the doc %q of %q", stderr, wantDocs[i], flag)
				}
			}
			if got := len(srv.requests()); got != 0 {
				t.Errorf("requests = %d, want 0: a refused call sends nothing", got)
			}
		})
	}
}

// TestGeneratedPrintsErrorDetail checks the output of a success, of a problem
// body with errors, of a detail-only body and of a bare status.
func TestGeneratedPrintsErrorDetail(t *testing.T) {
	const (
		body400 = `{"title":"Unprocessable Entity","status":400,"detail":"invalid input for test.get",` +
			`"errors":[{"location":"body.name","message":"expected string"},` +
			`{"location":"query.project","message":"required query parameter is missing"}]}`
		stderr400 = "moonglass: test get: invalid input for test.get\n" +
			"  body.name: expected string\n" +
			"  query.project: required query parameter is missing\n"
		body404   = `{"title":"Not Found","status":404,"detail":"project \"web\" not found"}`
		stderr404 = "moonglass: test get: project \"web\" not found\n"
	)
	tests := []struct {
		name       string
		status     int
		body       string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{"problem body with errors", http.StatusBadRequest, body400, 1, "", stderr400},
		{"detail only", http.StatusNotFound, body404, 1, "", stderr404},
		{"success prints the body", http.StatusOK, `{"servers":[]}`, 0, `{"servers":[]}`, ""},
		{"empty error body uses the status", http.StatusInternalServerError, "", 1, "", "moonglass: test get: 500 Internal Server Error\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newCaptureServer(t)
			srv.reply(tt.status, tt.body)
			code, stdout, stderr := runTest(t, srv, []string{"test", "get"}, []string{"--project", "p", "--env", "e"})
			if code != tt.wantCode {
				t.Errorf("exit = %d, want %d; stderr = %q", code, tt.wantCode, stderr)
			}
			if stdout != tt.wantStdout {
				t.Errorf("stdout = %q, want %q", stdout, tt.wantStdout)
			}
			if stderr != tt.wantStderr {
				t.Errorf("stderr = %q, want %q", stderr, tt.wantStderr)
			}
			if got := len(srv.requests()); got != 1 {
				t.Errorf("requests = %d, want 1", got)
			}
		})
	}
}

// badListIn has a list-of-objects field, which has no flag.
type badListIn struct {
	Items []badItem `json:"items" doc:"Items"`
}

type badItem struct {
	A string `json:"a" doc:"A"`
}

// badIntIn has an int field, which has no flag.
type badIntIn struct {
	Count int `json:"count" doc:"Count"`
}

// TestGenerateRefusesInputWithoutFlag checks that generation fails, naming the
// operation and the field, when a field's kind has no flag.
func TestGenerateRefusesInputWithoutFlag(t *testing.T) {
	for _, tt := range []struct {
		name  string
		in    reflect.Type
		field string
	}{
		{"list of objects", reflect.TypeFor[badListIn](), "Items"},
		{"int", reflect.TypeFor[badIntIn](), "Count"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			out, err := cli.Generate([]registry.Declaration{testSpec("test.bad", "POST", tt.in)}, nil)
			if err == nil {
				t.Fatalf("cli.Generate(input field %s) error = nil, want an error naming test.bad and %s; output = %q", tt.field, tt.field, out)
			}
			if len(out) != 0 {
				t.Errorf("output = %q, want none when generation fails", out)
			}
			for _, want := range []string{"test.bad", tt.field} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want %q in it", err, want)
				}
			}
		})
	}
}

// TestGenerateSkipsLocalCommands checks that an operation whose subcommand is a
// local command generates nothing and does not fail on its input kind.
func TestGenerateSkipsLocalCommands(t *testing.T) {
	local := []registry.LocalCommand{{Name: "test bad", Synopsis: "test bad", Summary: "A test", Touches: "nothing"}}
	out, err := cli.Generate([]registry.Declaration{testSpec("test.bad", "POST", reflect.TypeFor[badListIn]())}, local)
	if err != nil {
		t.Fatalf("cli.Generate with test bad on local commands error = %v, want nil", err)
	}
	if bytes.Contains(out, []byte(`"test bad"`)) {
		t.Errorf("generated output = %q, want no command named %q", out, "test bad")
	}
}
