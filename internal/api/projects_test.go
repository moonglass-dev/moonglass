package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"moonglass.dev/moonglass/internal/store"
)

// projectBody is one project row as the REST surface answers it.
type projectBody struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

// environmentBody is one environment row as the REST surface answers it.
type environmentBody struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

// serviceBody is one service row as the REST surface answers it.
type serviceBody struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Server    string `json:"server"`
	CreatedAt string `json:"created_at"`
}

// projectsAnswer is the body of projects.list.
type projectsAnswer struct {
	Projects []projectBody `json:"projects"`
}

// environmentsAnswer is the body of environments.list.
type environmentsAnswer struct {
	Environments []environmentBody `json:"environments"`
}

// servicesAnswer is the body of services.list.
type servicesAnswer struct {
	Services []serviceBody `json:"services"`
}

// TestProjectEnvironmentServiceRoutes checks the six operations over REST: the
// creates answer 200 with the created rows, the lists answer 200 with them, a
// repeated project name is 409, an unknown project on environments.list is 404
// and a missing server on services.create is 400 at body.
func TestProjectEnvironmentServiceRoutes(t *testing.T) {
	ctx := context.Background()
	e := newAPIEnv(t)
	build(e.humaAPI, e.reg)

	pub, err := store.NewID()
	if err != nil {
		t.Fatalf("store.NewID error = %v, want nil", err)
	}
	srv, err := e.st.AddServer(ctx, e.orgID, "pk-"+pub, "age1"+pub)
	if err != nil {
		t.Fatalf("AddServer error = %v, want nil", err)
	}

	doJSON := func(method, path, body string, want int, out any) {
		t.Helper()
		rec := e.do(t, method, path, body, e.bearer())
		if rec.Code != want {
			t.Fatalf("%s %s with body %q: status = %d, want %d (body %q)", method, path, body, rec.Code, want, rec.Body.String())
		}
		if out == nil {
			return
		}
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("%s %s: decode body %q: %v", method, path, rec.Body.String(), err)
		}
	}

	var web projectBody
	doJSON("POST", "/v1/projects", `{"name":"web"}`, http.StatusOK, &web)
	if web.ID == "" || web.Name != "web" || web.CreatedAt == "" {
		t.Errorf("POST /v1/projects = %+v, want an id, the name %q and created_at", web, "web")
	}

	var prod environmentBody
	doJSON("POST", "/v1/environments", `{"project":"web","name":"prod"}`, http.StatusOK, &prod)
	if prod.ID == "" || prod.Name != "prod" || prod.CreatedAt == "" {
		t.Errorf("POST /v1/environments = %+v, want an id, the name %q and created_at", prod, "prod")
	}

	var app serviceBody
	doJSON("POST", "/v1/services",
		fmt.Sprintf(`{"project":"web","environment":"prod","name":"app","server":%q}`, srv.ID), http.StatusOK, &app)
	if app.ID == "" || app.Name != "app" || app.CreatedAt == "" {
		t.Errorf("POST /v1/services = %+v, want an id, the name %q and created_at", app, "app")
	}
	if app.Server != srv.ID {
		t.Errorf("POST /v1/services server = %q, want %q", app.Server, srv.ID)
	}

	var projects projectsAnswer
	doJSON("GET", "/v1/projects", "", http.StatusOK, &projects)
	if len(projects.Projects) != 1 || projects.Projects[0] != web {
		t.Errorf("GET /v1/projects = %+v, want [%+v]", projects.Projects, web)
	}

	var environments environmentsAnswer
	doJSON("GET", "/v1/environments?project=web", "", http.StatusOK, &environments)
	if len(environments.Environments) != 1 || environments.Environments[0] != prod {
		t.Errorf("GET /v1/environments?project=web = %+v, want [%+v]", environments.Environments, prod)
	}

	var services servicesAnswer
	doJSON("GET", "/v1/services?project=web&environment=prod", "", http.StatusOK, &services)
	if len(services.Services) != 1 || services.Services[0] != app {
		t.Errorf("GET /v1/services?project=web&environment=prod = %+v, want [%+v]", services.Services, app)
	}

	t.Run("a repeated project name is 409", func(t *testing.T) {
		rec := e.do(t, "POST", "/v1/projects", `{"name":"web"}`, e.bearer())
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want %d (body %q)", rec.Code, http.StatusConflict, rec.Body.String())
		}
		wantProblemJSON(t, rec)
		if got := problemOf(t, rec).Detail; got != `project "web" already exists` {
			t.Errorf("detail = %q, want %q", got, `project "web" already exists`)
		}
	})

	t.Run("an unknown project on environments.list is 404", func(t *testing.T) {
		rec := e.do(t, "GET", "/v1/environments?project=ghost", "", e.bearer())
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d (body %q)", rec.Code, http.StatusNotFound, rec.Body.String())
		}
		wantProblemJSON(t, rec)
		if got := problemOf(t, rec).Detail; got != `project "ghost" not found` {
			t.Errorf("detail = %q, want %q", got, `project "ghost" not found`)
		}
	})

	t.Run("a missing server on services.create is 400 at body", func(t *testing.T) {
		// The name is fresh, but the request never reaches the handler. The
		// decoder records a missing required property at the object's own path,
		// so the location is body and the message names the property.
		rec := e.do(t, "POST", "/v1/services", `{"project":"web","environment":"prod","name":"broker"}`, e.bearer())
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d (body %q)", rec.Code, http.StatusBadRequest, rec.Body.String())
		}
		wantProblemJSON(t, rec)
		p := problemOf(t, rec)
		if got := errorLocations(p.Errors); !contains(got, "body") {
			t.Errorf("error locations = %v, want one at %q", got, "body")
		}
		if !errorMessagesContain(p.Errors, "server") {
			t.Errorf("error messages = %v, want one containing %q", errorMessages(p.Errors), "server")
		}
	})
}
