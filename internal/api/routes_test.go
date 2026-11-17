package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"moonglass.dev/moonglass/internal/registry"
	"moonglass.dev/moonglass/internal/server"
	"moonglass.dev/moonglass/internal/store"
	"moonglass.dev/moonglass/internal/store/sqlite"
)

func TestEveryMountedRouteIsOperationOrAllowlisted(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := sqlite.Open(ctx, filepath.Join(dir, "server.db"))
	if err != nil {
		t.Fatalf("sqlite.Open error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Now()
	srv, err := server.New(st, dir, func() time.Time { return now })
	if err != nil {
		t.Fatalf("server.New error = %v, want nil", err)
	}
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
	token, err := srv.MintOwnerToken(ctx, orgID, userID, "test", now.Add(time.Hour))
	if err != nil {
		t.Fatalf("MintOwnerToken error = %v, want nil", err)
	}

	// Every mounted route is an operation or on the fixed list.
	allowed := map[string]string{}
	for _, s := range registry.Operations() {
		allowed[s.Method+" "+s.Path] = "operation " + s.Name
	}
	listed := map[string]registry.Route{}
	for _, r := range registry.NonOperationRoutes {
		listed[r.Method+" "+r.Path] = r
		if r.Authorisation == "" {
			t.Errorf("non-operation route %s %s has an empty Authorisation, want its rule", r.Method, r.Path)
		}
	}
	mounted := map[string]bool{}
	for _, route := range srv.Routes() {
		mounted[route] = true
		_, isOp := allowed[route]
		_, isListed := listed[route]
		if !isOp && !isListed {
			t.Errorf("mounted route %q is neither a registry operation nor on registry.NonOperationRoutes", route)
		}
	}
	for route, what := range allowed {
		if !mounted[route] {
			t.Errorf("%s (%s) is in the registry but not mounted, want it served", route, what)
		}
	}
	if _, ok := listed["GET /v1/agent"]; !ok {
		t.Errorf("registry.NonOperationRoutes lacks GET /v1/agent (the agent WebSocket)")
	}
	if !mounted["POST /v1/enroll"] {
		t.Errorf("POST /v1/enroll is not mounted, want it served and listed")
	}

	// Each declared authorisation is what the route enforces.
	h := srv.Handler()
	do := func(method, path, bearer string) int {
		req := httptest.NewRequest(method, path, strings.NewReader(""))
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	for _, s := range registry.Operations() {
		tests := []struct {
			name   string
			bearer string
			want   int
		}{
			{"no token", "", http.StatusUnauthorized},
			{"bad token", "mg_" + strings.Repeat("a", 52), http.StatusUnauthorized},
			{"owner token", token, http.StatusOK},
		}
		for _, tt := range tests {
			if got := do(s.Method, s.Path, tt.bearer); got != tt.want {
				t.Errorf("%s %s with %s: status = %d, want %d", s.Method, s.Path, tt.name, got, tt.want)
			}
		}
	}
	if got := do("POST", "/v1/enroll", ""); got < 400 || got >= 500 {
		t.Errorf("POST /v1/enroll without an enrolment token: status = %d, want a 4xx rejection", got)
	}
}
