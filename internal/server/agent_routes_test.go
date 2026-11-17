package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"moonglass.dev/moonglass/internal/registry"
)

func TestAgentRoutesMatchTheDeclaredList(t *testing.T) {
	f := newFixture(t)
	listed := map[string]bool{}
	for _, r := range registry.NonOperationRoutes {
		listed[r.Method+" "+r.Path] = true
	}
	routes := f.srv.AgentRoutes()
	if len(routes) == 0 {
		t.Fatalf("AgentRoutes() = none, want the agent WebSocket route")
	}
	for _, route := range routes {
		if !listed[route] {
			t.Errorf("agent listener mounts %q, which is not on registry.NonOperationRoutes", route)
		}
	}
}

func TestAgentEndpointRejectsNonGet(t *testing.T) {
	f := newFixture(t)
	rec := httptest.NewRecorder()
	f.srv.AgentHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, agentPath, nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST %s status = %d, want %d", agentPath, rec.Code, http.StatusMethodNotAllowed)
	}
}
