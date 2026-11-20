package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"moonglass.dev/moonglass/internal/registry"
	"moonglass.dev/moonglass/internal/store"
	"moonglass.dev/moonglass/internal/store/sqlite"
)

const (
	testOwnerTokenName = "test"

	wantUnauthenticatedDetail = "the token is missing, unknown, expired or revoked: mint a new one"
	wantInternalDetail        = "the control plane could not complete the call; see its logs and retry"

	// sentinel is a value that must never come back in an error response.
	sentinel = "sentinel-7f3a"
)

// getIn is a GET operation's input: one field of each kind the REST surface
// serves, all named by their json tags.
type getIn struct {
	Name   string   `json:"name" doc:"Name of the thing"`
	Flag   bool     `json:"flag,omitempty" doc:"Whether the thing is on"`
	Count  int64    `json:"count,omitempty" doc:"How many of the thing there are"`
	Domain []string `json:"domain,omitempty" doc:"Domains the thing answers on"`
}

// bodyIn is a POST or PUT operation's input.
type bodyIn struct {
	Name   string   `json:"name" doc:"Name of the thing"`
	Count  int64    `json:"count,omitempty" doc:"How many of the thing there are"`
	Ready  bool     `json:"ready,omitempty" doc:"Whether the thing is ready"`
	Domain []string `json:"domain,omitempty" doc:"Domains the thing answers on"`
}

// targetIn names a project and an environment, so internal/api fills a
// registry.Target from its inputs.
type targetIn struct {
	Project     string `json:"project" doc:"Project name"`
	Environment string `json:"environment" doc:"Environment name"`
}

// testOut is what every test operation returns.
type testOut struct {
	OK bool `json:"ok" doc:"Whether the test operation ran"`
}

// problemBody is the application/problem+json body an error is written as. The
// errors entries are maps so a test can check for a value key.
type problemBody struct {
	Title  string           `json:"title"`
	Status int              `json:"status"`
	Detail string           `json:"detail"`
	Errors []map[string]any `json:"errors"`
}

// apiEnv is one REST API over a fresh SQLite store with an org, an owner user
// and an owner token.
type apiEnv struct {
	st      store.Store
	reg     *registry.Registry
	humaAPI huma.API
	mux     *http.ServeMux
	orgID   string
	userID  string
	token   string
}

func newAPIEnv(t *testing.T) *apiEnv {
	t.Helper()
	ctx := context.Background()
	st, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatalf("sqlite.Open error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	orgID, err := st.CreateOrg(ctx)
	if err != nil {
		t.Fatalf("CreateOrg error = %v, want nil", err)
	}
	userID, err := st.UserForIdentity(ctx, "owner@example.com")
	if err != nil {
		t.Fatalf("UserForIdentity error = %v, want nil", err)
	}
	if err := st.AddMembership(ctx, orgID, userID, store.RoleOwner); err != nil {
		t.Fatalf("AddMembership error = %v, want nil", err)
	}
	now := time.Now()
	token, err := registry.MintToken(ctx, st, orgID, userID, testOwnerTokenName,
		[]registry.Scope{{Project: "*", Environment: "*", Operation: "*"}}, now.Add(time.Hour), now)
	if err != nil {
		t.Fatalf("MintToken error = %v, want nil", err)
	}
	mux := http.NewServeMux()
	return &apiEnv{
		st:      st,
		reg:     registry.New(st, func() time.Time { return now }),
		humaAPI: humago.New(mux, config()),
		mux:     mux,
		orgID:   orgID,
		userID:  userID,
		token:   token,
	}
}

// mountTest registers op in the env's registry and mounts it through the one
// generic path the server uses.
func mountTest[In, Out any](t *testing.T, e *apiEnv, spec registry.Spec, h func(context.Context, registry.Call, In) (Out, error)) {
	t.Helper()
	op, err := registry.Register(e.reg, spec, h)
	if err != nil {
		t.Fatalf("registry.Register(%q) error = %v, want nil", spec.Name, err)
	}
	mount(e.humaAPI, e.reg, op)
}

func (e *apiEnv) bearer() string { return "Bearer " + e.token }

// do sends one request. An empty authorization sends no Authorization header.
func (e *apiEnv) do(t *testing.T, method, target, body, authorization string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, r)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

func (e *apiEnv) auditEvents(t *testing.T) []store.AuditEvent {
	t.Helper()
	evs, err := e.st.AuditEvents(context.Background(), e.orgID)
	if err != nil {
		t.Fatalf("AuditEvents error = %v, want nil", err)
	}
	return evs
}

func problemOf(t *testing.T, rec *httptest.ResponseRecorder) problemBody {
	t.Helper()
	var p problemBody
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode response body %q as problem+json: %v", rec.Body.String(), err)
	}
	return p
}

func wantProblemJSON(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/problem+json") {
		t.Errorf("Content-Type = %q, want application/problem+json", got)
	}
}

// wantRefusedWithoutHandler checks the shape every refused request has: the
// status, the problem content type, no handler run and no audit event.
func wantRefusedWithoutHandler(t *testing.T, e *apiEnv, rec *httptest.ResponseRecorder, ran *int, want int) problemBody {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d (body %q)", rec.Code, want, rec.Body.String())
	}
	wantProblemJSON(t, rec)
	if *ran != 0 {
		t.Errorf("handler runs = %d, want 0", *ran)
	}
	if n := len(e.auditEvents(t)); n != 0 {
		t.Errorf("audit events = %d, want 0", n)
	}
	return problemOf(t, rec)
}

func TestQueryInputsDecoded(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  getIn
	}{
		{
			name:  "every input present",
			query: "?name=n&flag=true&count=-42&domain=a&domain=b,c",
			want:  getIn{Name: "n", Flag: true, Count: -42, Domain: []string{"a", "b,c"}},
		},
		{
			name:  "false is a boolean",
			query: "?name=n&flag=false",
			want:  getIn{Name: "n"},
		},
		{
			name:  "absent optional inputs take their zero value",
			query: "?name=n",
			want:  getIn{Name: "n"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newAPIEnv(t)
			var got getIn
			ran := 0
			mountTest(t, e, registry.Spec{
				Name: "test.query", Summary: "Test query inputs", Method: "GET", Path: "/v1/test/query",
				MinRole: store.RoleViewer, Destructive: registry.No,
			}, func(_ context.Context, _ registry.Call, in getIn) (testOut, error) {
				ran++
				got = in
				return testOut{OK: true}, nil
			})

			rec := e.do(t, "GET", "/v1/test/query"+tt.query, "", e.bearer())
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s with the owner token: status = %d, want %d (body %q)", tt.query, rec.Code, http.StatusOK, rec.Body.String())
			}
			if ran != 1 {
				t.Fatalf("handler runs = %d, want 1", ran)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("handler input = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestBodyInputsDecoded(t *testing.T) {
	const every = `{"name":"n","count":7,"ready":true,"domain":["x","y,z"]}`
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		want   bodyIn
	}{
		{
			name:   "POST carries every field",
			method: "POST",
			path:   "/v1/test/post",
			body:   every,
			want:   bodyIn{Name: "n", Count: 7, Ready: true, Domain: []string{"x", "y,z"}},
		},
		{
			name:   "PUT carries every field",
			method: "PUT",
			path:   "/v1/test/put",
			body:   every,
			want:   bodyIn{Name: "n", Count: 7, Ready: true, Domain: []string{"x", "y,z"}},
		},
		{
			name:   "absent optional fields take their zero value",
			method: "POST",
			path:   "/v1/test/post",
			body:   `{"name":"n"}`,
			want:   bodyIn{Name: "n"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newAPIEnv(t)
			var got bodyIn
			ran := 0
			mountTest(t, e, registry.Spec{
				Name: "test.body", Summary: "Test body inputs", Method: tt.method, Path: tt.path,
				MinRole: store.RoleViewer, Destructive: registry.No,
			}, func(_ context.Context, _ registry.Call, in bodyIn) (testOut, error) {
				ran++
				got = in
				return testOut{OK: true}, nil
			})

			rec := e.do(t, tt.method, tt.path, tt.body, e.bearer())
			if rec.Code != http.StatusOK {
				t.Fatalf("%s %s: status = %d, want %d (body %q)", tt.method, tt.path, rec.Code, http.StatusOK, rec.Body.String())
			}
			if ran != 1 {
				t.Fatalf("handler runs = %d, want 1", ran)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("handler input = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestUnknownInputsRefused(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		path     string
		target   string
		body     string
		location string
		opName   string
	}{
		{
			name: "unknown query parameter", method: "GET", path: "/v1/test/query",
			target: "/v1/test/query?name=n&bogus=1", location: "query.bogus", opName: "test.query",
		},
		{
			name: "unknown body field", method: "POST", path: "/v1/test/body",
			target: "/v1/test/body", body: `{"name":"n","bogus":1}`, location: "body.bogus", opName: "test.body",
		},
		{
			name: "query parameter on a POST", method: "POST", path: "/v1/test/body",
			target: "/v1/test/body?bogus=1", body: `{"name":"n"}`, location: "query.bogus", opName: "test.body",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newAPIEnv(t)
			ran := 0
			mountSchemas(t, e, tt.method, tt.path, tt.opName, &ran)

			rec := e.do(t, tt.method, tt.target, tt.body, e.bearer())
			p := wantRefusedWithoutHandler(t, e, rec, &ran, http.StatusBadRequest)
			if want := "invalid input for " + tt.opName; p.Detail != want {
				t.Errorf("detail = %q, want %q", p.Detail, want)
			}
			if len(p.Errors) == 0 {
				t.Fatalf("errors = %v, want at least one entry", p.Errors)
			}
			if got := errorLocations(p.Errors); !contains(got, tt.location) {
				t.Errorf("error locations = %v, want one at %q", got, tt.location)
			}
		})
	}
}

func TestSchemaFailuresAre400(t *testing.T) {
	tests := []struct {
		name     string
		opName   string
		method   string
		path     string
		target   string
		body     string
		location string
		message  string
	}{
		{
			name: "missing required query input", opName: "test.query", method: "GET", path: "/v1/test/query",
			target: "/v1/test/query", location: "query.name",
		},
		{
			name: "query integer is not decimal", opName: "test.query", method: "GET", path: "/v1/test/query",
			target: "/v1/test/query?count=" + sentinel, location: "query.count",
		},
		{
			name: "query boolean is not true or false", opName: "test.query", method: "GET", path: "/v1/test/query",
			target: "/v1/test/query?flag=" + sentinel, location: "query.flag",
		},
		{
			name: "repeated query parameter for a scalar", opName: "test.query", method: "GET", path: "/v1/test/query",
			target: "/v1/test/query?name=a&name=b", location: "query.name", message: "one value",
		},
		{
			name: "missing required body field", opName: "test.body", method: "POST", path: "/v1/test/body",
			target: "/v1/test/body", body: `{}`, location: "body", message: "name",
		},
		{
			name: "body is not an object", opName: "test.body", method: "POST", path: "/v1/test/body",
			target: "/v1/test/body", body: `[]`, location: "body",
		},
		{
			name: "body field has the wrong type", opName: "test.body", method: "POST", path: "/v1/test/body",
			target: "/v1/test/body", body: `{"name":"n","count":"` + sentinel + `"}`, location: "body.count",
		},
		{
			name: "body is not JSON", opName: "test.body", method: "POST", path: "/v1/test/body",
			target: "/v1/test/body", body: "not json " + sentinel, location: "body",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, authorization := range []string{"owner token", "no token"} {
				t.Run(authorization, func(t *testing.T) {
					e := newAPIEnv(t)
					ran := 0
					mountSchemas(t, e, tt.method, tt.path, tt.opName, &ran)

					header := e.bearer()
					if authorization == "no token" {
						header = ""
					}
					rec := e.do(t, tt.method, tt.target, tt.body, header)

					p := wantRefusedWithoutHandler(t, e, rec, &ran, http.StatusBadRequest)
					if want := "invalid input for " + tt.opName; p.Detail != want {
						t.Errorf("detail = %q, want %q", p.Detail, want)
					}
					if rec.Code == http.StatusUnprocessableEntity {
						t.Errorf("status = %d, want 400 and never 422", rec.Code)
					}
					if len(p.Errors) == 0 {
						t.Fatalf("errors = %v, want at least one entry", p.Errors)
					}
					for i, entry := range p.Errors {
						if loc, _ := entry["location"].(string); loc == "" {
							t.Errorf("errors[%d] = %v, want a non-empty location", i, entry)
						}
						if msg, _ := entry["message"].(string); msg == "" {
							t.Errorf("errors[%d] = %v, want a non-empty message", i, entry)
						}
						if _, ok := entry["value"]; ok {
							t.Errorf("errors[%d] = %v, want no value key", i, entry)
						}
					}
					if got := errorLocations(p.Errors); !contains(got, tt.location) {
						t.Errorf("error locations = %v, want one at %q", got, tt.location)
					}
					if tt.message != "" && !errorMessagesContain(p.Errors, tt.message) {
						t.Errorf("error messages = %v, want one containing %q", errorMessages(p.Errors), tt.message)
					}
					if strings.Contains(rec.Body.String(), sentinel) {
						t.Errorf("response body = %q, want it not to hold the sent value %q", rec.Body.String(), sentinel)
					}
				})
			}
		})
	}
}

// mountSchemas mounts the one test operation of the case's method and path.
func mountSchemas(t *testing.T, e *apiEnv, method, path, name string, ran *int) {
	t.Helper()
	spec := registry.Spec{
		Name: name, Summary: "Test schema failures", Method: method, Path: path,
		MinRole: store.RoleViewer, Destructive: registry.No,
	}
	if method == "GET" {
		mountTest(t, e, spec, func(context.Context, registry.Call, getIn) (testOut, error) {
			*ran++
			return testOut{OK: true}, nil
		})
		return
	}
	mountTest(t, e, spec, func(context.Context, registry.Call, bodyIn) (testOut, error) {
		*ran++
		return testOut{OK: true}, nil
	})
}

func TestRefusalStatuses(t *testing.T) {
	tests := []struct {
		name string
		kind error
		want int
	}{
		{"invalid input", registry.ErrInvalidInput, http.StatusBadRequest},
		{"not found", registry.ErrNotFound, http.StatusNotFound},
		{"conflict", registry.ErrConflict, http.StatusConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newAPIEnv(t)
			message := fmt.Sprintf("thing %q cannot be used: %s", "x", tt.name)
			ran := 0
			mountTest(t, e, registry.Spec{
				Name: "test.refuse", Summary: "Test refusals", Method: "POST", Path: "/v1/test/refuse",
				MinRole: store.RoleViewer, Destructive: registry.No,
			}, func(context.Context, registry.Call, bodyIn) (testOut, error) {
				ran++
				return testOut{}, registry.Refuse(tt.kind, message)
			})

			rec := e.do(t, "POST", "/v1/test/refuse", `{"name":"n"}`, e.bearer())
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tt.want, rec.Body.String())
			}
			wantProblemJSON(t, rec)
			if ran != 1 {
				t.Errorf("handler runs = %d, want 1", ran)
			}
			if p := problemOf(t, rec); p.Detail != message {
				t.Errorf("detail = %q, want the refusal message %q", p.Detail, message)
			}
		})
	}
}

func TestUnmappedHandlerErrorIs500(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		secret string
	}{
		{"plain wrapped error", fmt.Errorf("query rows: %w", errors.New("secret-text")), "secret-text"},
		{"unmapped store sentinel", fmt.Errorf("create: %w", store.ErrDuplicate), store.ErrDuplicate.Error()},
		{"wrapped refusal", fmt.Errorf("wrap: %w", registry.Refuse(registry.ErrConflict, "inner-text")), "inner-text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newAPIEnv(t)
			ran := 0
			mountTest(t, e, registry.Spec{
				Name: "test.error", Summary: "Test handler errors", Method: "POST", Path: "/v1/test/error",
				MinRole: store.RoleViewer, Destructive: registry.No,
			}, func(context.Context, registry.Call, bodyIn) (testOut, error) {
				ran++
				return testOut{}, tt.err
			})

			rec := e.do(t, "POST", "/v1/test/error", `{"name":"n"}`, e.bearer())
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, http.StatusInternalServerError, rec.Body.String())
			}
			wantProblemJSON(t, rec)
			if ran != 1 {
				t.Errorf("handler runs = %d, want 1", ran)
			}
			p := problemOf(t, rec)
			if p.Detail != wantInternalDetail {
				t.Errorf("detail = %q, want %q", p.Detail, wantInternalDetail)
			}
			if strings.Contains(rec.Body.String(), tt.secret) {
				t.Errorf("response body = %q, want it not to hold %q", rec.Body.String(), tt.secret)
			}
			if strings.Contains(rec.Body.String(), tt.err.Error()) {
				t.Errorf("response body = %q, want it not to hold the error text %q", rec.Body.String(), tt.err.Error())
			}
			if strings.Contains(rec.Body.String(), e.token) {
				t.Errorf("response body = %q, want it not to hold the bearer token", rec.Body.String())
			}
		})
	}
}

func TestTargetFilledFromInputs(t *testing.T) {
	ctx := context.Background()
	e := newAPIEnv(t)
	web, err := e.st.CreateProject(ctx, e.orgID, "web")
	if err != nil {
		t.Fatalf("CreateProject(%q) error = %v, want nil", "web", err)
	}
	prod, err := e.st.CreateEnvironment(ctx, e.orgID, web.ID, "prod")
	if err != nil {
		t.Fatalf("CreateEnvironment(%q) error = %v, want nil", "prod", err)
	}

	var targetCall registry.Call
	targetRan := 0
	mountTest(t, e, registry.Spec{
		Name: "test.target", Summary: "Test targets", Method: "GET", Path: "/v1/test/target",
		MinRole: store.RoleViewer, Destructive: registry.No,
	}, func(_ context.Context, c registry.Call, _ targetIn) (testOut, error) {
		targetRan++
		targetCall = c
		return testOut{OK: true}, nil
	})

	var queryCall registry.Call
	queryRan := 0
	mountTest(t, e, registry.Spec{
		Name: "test.query", Summary: "Test query targets", Method: "GET", Path: "/v1/test/query",
		MinRole: store.RoleViewer, Destructive: registry.No,
	}, func(_ context.Context, c registry.Call, _ getIn) (testOut, error) {
		queryRan++
		queryCall = c
		return testOut{OK: true}, nil
	})

	targets := []struct {
		name   string
		target string
		want   int
		detail string
	}{
		{
			name: "unknown project", target: `/v1/test/target?project=nope&environment=prod`,
			want: http.StatusNotFound, detail: `project "nope" not found`,
		},
		{
			name: "unknown environment in a known project", target: `/v1/test/target?project=web&environment=nope`,
			want: http.StatusNotFound, detail: `environment "nope" not found in project "web"`,
		},
		{
			name: "known pair reaches the handler", target: `/v1/test/target?project=web&environment=prod`,
			want: http.StatusOK,
		},
	}
	for _, tt := range targets {
		t.Run(tt.name, func(t *testing.T) {
			targetRan, targetCall = 0, registry.Call{}
			rec := e.do(t, "GET", tt.target, "", e.bearer())
			if rec.Code != tt.want {
				t.Fatalf("GET %s: status = %d, want %d (body %q)", tt.target, rec.Code, tt.want, rec.Body.String())
			}
			if tt.detail != "" {
				wantProblemJSON(t, rec)
				if p := problemOf(t, rec); p.Detail != tt.detail {
					t.Errorf("detail = %q, want %q", p.Detail, tt.detail)
				}
				if targetRan != 0 {
					t.Errorf("handler runs = %d, want 0", targetRan)
				}
				return
			}
			if targetRan != 1 {
				t.Fatalf("handler runs = %d, want 1", targetRan)
			}
			if targetCall.ProjectID != web.ID || targetCall.EnvironmentID != prod.ID {
				t.Errorf("handler Call ids = project %q, environment %q; want %q and %q",
					targetCall.ProjectID, targetCall.EnvironmentID, web.ID, prod.ID)
			}
		})
	}

	t.Run("operation without target fields gets an empty target", func(t *testing.T) {
		rec := e.do(t, "GET", "/v1/test/query?name=n", "", e.bearer())
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d (body %q)", rec.Code, http.StatusOK, rec.Body.String())
		}
		if queryRan != 1 {
			t.Fatalf("handler runs = %d, want 1", queryRan)
		}
		if queryCall.ProjectID != "" || queryCall.EnvironmentID != "" {
			t.Errorf("handler Call ids = project %q, environment %q; want both empty",
				queryCall.ProjectID, queryCall.EnvironmentID)
		}
		var found bool
		for _, ev := range e.auditEvents(t) {
			if ev.Operation != "test.query" {
				continue
			}
			found = true
			if ev.Outcome != "allowed" {
				t.Errorf("audit outcome = %q, want %q", ev.Outcome, "allowed")
			}
		}
		if !found {
			t.Errorf("audit events = %v, want one for test.query", e.auditEvents(t))
		}
	})
}

func TestMissingTokenIs401WithSpecDetail(t *testing.T) {
	tests := []struct {
		name          string
		authorization string
	}{
		{"no header", ""},
		{"non-Bearer header", "Basic abc"},
		{"unknown token", "Bearer mg_" + strings.Repeat("a", 52)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newAPIEnv(t)
			ran := 0
			mountTest(t, e, registry.Spec{
				Name: "test.query", Summary: "Test authentication", Method: "GET", Path: "/v1/test/query",
				MinRole: store.RoleViewer, Destructive: registry.No,
			}, func(context.Context, registry.Call, getIn) (testOut, error) {
				ran++
				return testOut{OK: true}, nil
			})

			rec := e.do(t, "GET", "/v1/test/query?name=n", "", tt.authorization)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, http.StatusUnauthorized, rec.Body.String())
			}
			wantProblemJSON(t, rec)
			if ran != 0 {
				t.Errorf("handler runs = %d, want 0", ran)
			}
			if p := problemOf(t, rec); p.Detail != wantUnauthenticatedDetail {
				t.Errorf("detail = %q, want %q", p.Detail, wantUnauthenticatedDetail)
			}
			if token := strings.TrimPrefix(tt.authorization, "Bearer "); token != "" && strings.Contains(rec.Body.String(), token) {
				t.Errorf("response body = %q, want it not to hold the sent token", rec.Body.String())
			}
		})
	}
}

func TestBodyOverLimitIs413(t *testing.T) {
	e := newAPIEnv(t)
	ran := 0
	mountTest(t, e, registry.Spec{
		Name: "test.body", Summary: "Test body limit", Method: "POST", Path: "/v1/test/body",
		MinRole: store.RoleViewer, Destructive: registry.No,
	}, func(context.Context, registry.Call, bodyIn) (testOut, error) {
		ran++
		return testOut{OK: true}, nil
	})

	body := `{"name":"` + strings.Repeat("a", (1<<20)+1) + `"}`
	rec := e.do(t, "POST", "/v1/test/body", body, e.bearer())
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d (body %q)", rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
	}
	wantProblemJSON(t, rec)
	if ran != 0 {
		t.Errorf("handler runs = %d, want 0", ran)
	}
}

// errorLocations returns every errors entry's location.
func errorLocations(entries []map[string]any) []string {
	locations := make([]string, 0, len(entries))
	for _, entry := range entries {
		if loc, ok := entry["location"].(string); ok {
			locations = append(locations, loc)
		}
	}
	return locations
}

// errorMessages returns every errors entry's message.
func errorMessages(entries []map[string]any) []string {
	messages := make([]string, 0, len(entries))
	for _, entry := range entries {
		if msg, ok := entry["message"].(string); ok {
			messages = append(messages, msg)
		}
	}
	return messages
}

func errorMessagesContain(entries []map[string]any, want string) bool {
	for _, msg := range errorMessages(entries) {
		if strings.Contains(msg, want) {
			return true
		}
	}
	return false
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
