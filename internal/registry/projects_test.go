package registry_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"moonglass.dev/moonglass/internal/registry"
	"moonglass.dev/moonglass/internal/store"
)

// refusalKinds is every kind a handler refusal can have.
var refusalKinds = []error{registry.ErrInvalidInput, registry.ErrNotFound, registry.ErrConflict}

// createServer registers one server of orgID directly through the store.
func createServer(t *testing.T, e *env, orgID string) store.Server {
	t.Helper()
	pub, err := store.NewID()
	if err != nil {
		t.Fatalf("store.NewID error = %v, want nil", err)
	}
	srv, err := e.st.AddServer(context.Background(), orgID, "pk-"+pub, "age1"+pub)
	if err != nil {
		t.Fatalf("AddServer in org %s error = %v, want nil", orgID, err)
	}
	return srv
}

// createService writes one service of the test org directly through the store.
func createService(t *testing.T, e *env, environmentID, name, serverID string) store.Service {
	t.Helper()
	svc, err := e.st.CreateService(context.Background(), e.orgID, environmentID, name, serverID)
	if err != nil {
		t.Fatalf("CreateService(%q, %q) error = %v, want nil", environmentID, name, err)
	}
	return svc
}

// createErrorStore fails the one create method named by method with err; every
// other call goes to the embedded store.
type createErrorStore struct {
	store.Store
	method string
	err    error
}

func (s *createErrorStore) CreateProject(ctx context.Context, orgID, name string) (store.Project, error) {
	if s.method == "project" {
		return store.Project{}, s.err
	}
	return s.Store.CreateProject(ctx, orgID, name)
}

func (s *createErrorStore) CreateEnvironment(ctx context.Context, orgID, projectID, name string) (store.Environment, error) {
	if s.method == "environment" {
		return store.Environment{}, s.err
	}
	return s.Store.CreateEnvironment(ctx, orgID, projectID, name)
}

func (s *createErrorStore) CreateService(ctx context.Context, orgID, environmentID, name, serverID string) (store.Service, error) {
	if s.method == "service" {
		return store.Service{}, s.err
	}
	return s.Store.CreateService(ctx, orgID, environmentID, name, serverID)
}

// handlerCountStore counts the four store calls a denied target must never
// reach.
type handlerCountStore struct {
	store.Store
	createEnvironment int
	environments      int
	createService     int
	services          int
}

func (s *handlerCountStore) CreateEnvironment(ctx context.Context, orgID, projectID, name string) (store.Environment, error) {
	s.createEnvironment++
	return s.Store.CreateEnvironment(ctx, orgID, projectID, name)
}

func (s *handlerCountStore) Environments(ctx context.Context, orgID, projectID string) ([]store.Environment, error) {
	s.environments++
	return s.Store.Environments(ctx, orgID, projectID)
}

func (s *handlerCountStore) CreateService(ctx context.Context, orgID, environmentID, name, serverID string) (store.Service, error) {
	s.createService++
	return s.Store.CreateService(ctx, orgID, environmentID, name, serverID)
}

func (s *handlerCountStore) Services(ctx context.Context, orgID, environmentID string) ([]store.Service, error) {
	s.services++
	return s.Store.Services(ctx, orgID, environmentID)
}

// invokeProjectsCreate invokes projects.create for name.
func invokeProjectsCreate(reg *registry.Registry, p registry.Principal, name string) error {
	_, err := registry.Invoke(context.Background(), reg, reg.ProjectsCreate, p, registry.Target{},
		registry.ProjectsCreateIn{Name: name})
	return err
}

// invokeEnvironmentsCreate invokes environments.create for project and name.
func invokeEnvironmentsCreate(reg *registry.Registry, p registry.Principal, project, name string) error {
	_, err := registry.Invoke(context.Background(), reg, reg.EnvironmentsCreate, p, registry.Target{Project: project},
		registry.EnvironmentsCreateIn{Project: project, Name: name})
	return err
}

// invokeServicesCreate invokes services.create for the given inputs.
func invokeServicesCreate(reg *registry.Registry, p registry.Principal, project, environment, name, server string) error {
	_, err := registry.Invoke(context.Background(), reg, reg.ServicesCreate, p, registry.Target{Project: project, Environment: environment},
		registry.ServicesCreateIn{Project: project, Environment: environment, Name: name, Server: server})
	return err
}

// countProjects is the number of project rows of the test org.
func countProjects(t *testing.T, e *env) int {
	t.Helper()
	rows, err := e.st.Projects(context.Background(), e.orgID)
	if err != nil {
		t.Fatalf("Projects error = %v, want nil", err)
	}
	return len(rows)
}

// countEnvironments is the number of environment rows of a project.
func countEnvironments(t *testing.T, e *env, projectID string) int {
	t.Helper()
	rows, err := e.st.Environments(context.Background(), e.orgID, projectID)
	if err != nil {
		t.Fatalf("Environments(%q) error = %v, want nil", projectID, err)
	}
	return len(rows)
}

// countServices is the number of service rows of an environment.
func countServices(t *testing.T, e *env, environmentID string) int {
	t.Helper()
	rows, err := e.st.Services(context.Background(), e.orgID, environmentID)
	if err != nil {
		t.Fatalf("Services(%q) error = %v, want nil", environmentID, err)
	}
	return len(rows)
}

// TestProjectEnvironmentServiceSpecs checks that the six operations are
// declared exactly as docs/specs/registry.md gives them, each with its In type
// and its input fields, all required strings.
func TestProjectEnvironmentServiceSpecs(t *testing.T) {
	tests := []struct {
		name   string
		want   registry.Spec
		in     reflect.Type
		fields []string
	}{
		{
			name: "projects.create",
			want: registry.Spec{
				Name: "projects.create", Summary: "Create a project", Method: "POST", Path: "/v1/projects",
				MinRole: store.RoleMember, Destructive: registry.No,
			},
			in:     reflect.TypeFor[registry.ProjectsCreateIn](),
			fields: []string{"name"},
		},
		{
			name: "projects.list",
			want: registry.Spec{
				Name: "projects.list", Summary: "List projects", Method: "GET", Path: "/v1/projects",
				MinRole: store.RoleViewer, Destructive: registry.No,
			},
			in:     reflect.TypeFor[registry.ProjectsListIn](),
			fields: nil,
		},
		{
			name: "environments.create",
			want: registry.Spec{
				Name: "environments.create", Summary: "Create an environment", Method: "POST", Path: "/v1/environments",
				MinRole: store.RoleMember, Destructive: registry.No,
			},
			in:     reflect.TypeFor[registry.EnvironmentsCreateIn](),
			fields: []string{"project", "name"},
		},
		{
			name: "environments.list",
			want: registry.Spec{
				Name: "environments.list", Summary: "List environments", Method: "GET", Path: "/v1/environments",
				MinRole: store.RoleViewer, Destructive: registry.No,
			},
			in:     reflect.TypeFor[registry.EnvironmentsListIn](),
			fields: []string{"project"},
		},
		{
			name: "services.create",
			want: registry.Spec{
				Name: "services.create", Summary: "Create a service", Method: "POST", Path: "/v1/services",
				MinRole: store.RoleMember, Destructive: registry.No,
			},
			in:     reflect.TypeFor[registry.ServicesCreateIn](),
			fields: []string{"project", "environment", "name", "server"},
		},
		{
			name: "services.list",
			want: registry.Spec{
				Name: "services.list", Summary: "List services", Method: "GET", Path: "/v1/services",
				MinRole: store.RoleViewer, Destructive: registry.No,
			},
			in:     reflect.TypeFor[registry.ServicesListIn](),
			fields: []string{"project", "environment"},
		},
	}
	ops := registry.Operations()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var found *registry.Declaration
			for i := range ops {
				if ops[i].Name == tt.name {
					found = &ops[i]
					break
				}
			}
			if found == nil {
				t.Fatalf("registry.Operations() does not declare %q, want it with In %v", tt.name, tt.in)
			}
			if found.Spec != tt.want {
				t.Errorf("%s Spec = %+v, want %+v", tt.name, found.Spec, tt.want)
			}
			if found.In != tt.in {
				t.Errorf("%s In = %v, want %v", tt.name, found.In, tt.in)
			}
			if found.In == nil || found.In.Kind() != reflect.Struct {
				t.Fatalf("%s In = %v, want a struct type", tt.name, found.In)
			}
			if got := found.In.NumField(); got != len(tt.fields) {
				t.Fatalf("%s In has %d fields, want %d (%v)", tt.name, got, len(tt.fields), tt.fields)
			}
			for i, want := range tt.fields {
				f := found.In.Field(i)
				name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
				if name != want {
					t.Errorf("%s field %d json name = %q, want %q", tt.name, i, name, want)
				}
				if strings.Contains(opts, "omitempty") {
					t.Errorf("%s field %s json tag %q has omitempty, want a required input", tt.name, f.Name, f.Tag.Get("json"))
				}
				if f.Type.Kind() != reflect.String {
					t.Errorf("%s field %s is %s, want a string", tt.name, f.Name, f.Type)
				}
				if f.Tag.Get("doc") == "" {
					t.Errorf("%s field %s has no doc tag, want one", tt.name, f.Name)
				}
			}
		})
	}
}

// TestProjectsCreateAndList checks that an owner creates projects and lists
// them in name order, and that another org lists nothing.
func TestProjectsCreateAndList(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.principal(store.RoleOwner, wildcard)

	web, err := registry.Invoke(ctx, e.reg, e.reg.ProjectsCreate, p, registry.Target{}, registry.ProjectsCreateIn{Name: "web"})
	if err != nil {
		t.Fatalf("projects.create %q error = %v, want nil", "web", err)
	}
	if web.ID == "" {
		t.Errorf("projects.create %q ID = %q, want a non-empty id", "web", web.ID)
	}
	if web.Name != "web" {
		t.Errorf("projects.create Name = %q, want %q", web.Name, "web")
	}
	if _, err := time.Parse(time.RFC3339, web.CreatedAt); err != nil {
		t.Errorf("projects.create %q CreatedAt = %q, want RFC 3339: %v", "web", web.CreatedAt, err)
	}

	api, err := registry.Invoke(ctx, e.reg, e.reg.ProjectsCreate, p, registry.Target{}, registry.ProjectsCreateIn{Name: "api"})
	if err != nil {
		t.Fatalf("projects.create %q error = %v, want nil", "api", err)
	}
	if api.Name != "api" {
		t.Errorf("projects.create Name = %q, want %q", api.Name, "api")
	}

	listed, err := registry.Invoke(ctx, e.reg, e.reg.ProjectsList, p, registry.Target{}, registry.ProjectsListIn{})
	if err != nil {
		t.Fatalf("projects.list error = %v, want nil", err)
	}
	if len(listed.Projects) != 2 {
		t.Fatalf("projects.list = %+v, want two projects", listed.Projects)
	}
	want := []registry.ProjectInfo{
		{ID: api.ID, Name: "api", CreatedAt: api.CreatedAt},
		{ID: web.ID, Name: "web", CreatedAt: web.CreatedAt},
	}
	for i, w := range want {
		if got := listed.Projects[i]; got != w {
			t.Errorf("projects.list[%d] = %+v, want %+v", i, got, w)
		}
	}

	otherOrg, err := e.st.CreateOrg(ctx)
	if err != nil {
		t.Fatalf("CreateOrg error = %v, want nil", err)
	}
	other := e.principal(store.RoleOwner, wildcard)
	other.OrgID = otherOrg
	empty, err := registry.Invoke(ctx, e.reg, e.reg.ProjectsList, other, registry.Target{}, registry.ProjectsListIn{})
	if err != nil {
		t.Fatalf("projects.list of the second org error = %v, want nil", err)
	}
	if empty.Projects == nil {
		t.Errorf("projects.list of the second org = nil, want an empty non-nil list")
	}
	if len(empty.Projects) != 0 {
		t.Errorf("projects.list of the second org = %+v, want no projects", empty.Projects)
	}
}

// TestEnvironmentsCreateAndList checks that environments.create uses the
// resolved project and that environments.list returns them in name order.
func TestEnvironmentsCreateAndList(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	web := createProject(t, e, "web")
	p := e.principal(store.RoleOwner, wildcard)

	prod, err := registry.Invoke(ctx, e.reg, e.reg.EnvironmentsCreate, p, registry.Target{Project: "web"},
		registry.EnvironmentsCreateIn{Project: "web", Name: "prod"})
	if err != nil {
		t.Fatalf("environments.create %q error = %v, want nil", "prod", err)
	}
	if prod.ID == "" || prod.Name != "prod" {
		t.Errorf("environments.create = %+v, want an id and the name %q", prod, "prod")
	}
	if _, err := time.Parse(time.RFC3339, prod.CreatedAt); err != nil {
		t.Errorf("environments.create %q CreatedAt = %q, want RFC 3339: %v", "prod", prod.CreatedAt, err)
	}

	staging, err := registry.Invoke(ctx, e.reg, e.reg.EnvironmentsCreate, p, registry.Target{Project: "web"},
		registry.EnvironmentsCreateIn{Project: "web", Name: "staging"})
	if err != nil {
		t.Fatalf("environments.create %q error = %v, want nil", "staging", err)
	}

	stored, err := e.st.Environments(ctx, e.orgID, web.ID)
	if err != nil {
		t.Fatalf("Environments(%q) error = %v, want nil", web.ID, err)
	}
	if len(stored) != 2 {
		t.Fatalf("Environments(%q) = %d rows, want 2: the handler used the resolved project id", web.ID, len(stored))
	}

	listed, err := registry.Invoke(ctx, e.reg, e.reg.EnvironmentsList, p, registry.Target{Project: "web"},
		registry.EnvironmentsListIn{Project: "web"})
	if err != nil {
		t.Fatalf("environments.list error = %v, want nil", err)
	}
	want := []registry.EnvironmentInfo{
		{ID: prod.ID, Name: "prod", CreatedAt: prod.CreatedAt},
		{ID: staging.ID, Name: "staging", CreatedAt: staging.CreatedAt},
	}
	if len(listed.Environments) != len(want) {
		t.Fatalf("environments.list = %+v, want %+v", listed.Environments, want)
	}
	for i, w := range want {
		if got := listed.Environments[i]; got != w {
			t.Errorf("environments.list[%d] = %+v, want %+v", i, got, w)
		}
	}
}

// TestServicesCreateAndList checks that services.create uses the resolved
// environment and the given server, and that services.list returns them in name
// order.
func TestServicesCreateAndList(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	web := createProject(t, e, "web")
	prod := createEnvironment(t, e, web.ID, "prod")
	srv := createServer(t, e, e.orgID)
	p := e.principal(store.RoleOwner, wildcard)

	api, err := registry.Invoke(ctx, e.reg, e.reg.ServicesCreate, p, registry.Target{Project: "web", Environment: "prod"},
		registry.ServicesCreateIn{Project: "web", Environment: "prod", Name: "api", Server: srv.ID})
	if err != nil {
		t.Fatalf("services.create %q error = %v, want nil", "api", err)
	}
	if api.ID == "" || api.Name != "api" {
		t.Errorf("services.create = %+v, want an id and the name %q", api, "api")
	}
	if api.Server != srv.ID {
		t.Errorf("services.create server = %q, want %q", api.Server, srv.ID)
	}
	if _, err := time.Parse(time.RFC3339, api.CreatedAt); err != nil {
		t.Errorf("services.create %q CreatedAt = %q, want RFC 3339: %v", "api", api.CreatedAt, err)
	}

	worker, err := registry.Invoke(ctx, e.reg, e.reg.ServicesCreate, p, registry.Target{Project: "web", Environment: "prod"},
		registry.ServicesCreateIn{Project: "web", Environment: "prod", Name: "worker", Server: srv.ID})
	if err != nil {
		t.Fatalf("services.create %q error = %v, want nil", "worker", err)
	}

	stored, err := e.st.Services(ctx, e.orgID, prod.ID)
	if err != nil {
		t.Fatalf("Services(%q) error = %v, want nil", prod.ID, err)
	}
	if len(stored) != 2 {
		t.Fatalf("Services(%q) = %d rows, want 2: the handler used the resolved environment id", prod.ID, len(stored))
	}

	listed, err := registry.Invoke(ctx, e.reg, e.reg.ServicesList, p, registry.Target{Project: "web", Environment: "prod"},
		registry.ServicesListIn{Project: "web", Environment: "prod"})
	if err != nil {
		t.Fatalf("services.list error = %v, want nil", err)
	}
	want := []registry.ServiceInfo{
		{ID: api.ID, Name: "api", Server: srv.ID, CreatedAt: api.CreatedAt},
		{ID: worker.ID, Name: "worker", Server: srv.ID, CreatedAt: worker.CreatedAt},
	}
	if len(listed.Services) != len(want) {
		t.Fatalf("services.list = %+v, want %+v", listed.Services, want)
	}
	for i, w := range want {
		if got := listed.Services[i]; got != w {
			t.Errorf("services.list[%d] = %+v, want %+v", i, got, w)
		}
	}
}

// TestCreateRefusalsFromStoreErrors checks that each of the three creates maps
// the store's sentinels to the fixed refusal text, audits it and writes nothing.
func TestCreateRefusalsFromStoreErrors(t *testing.T) {
	unmapped := errors.New("disk full")
	// otherServerID is filled by the other-org case, so its message can name it.
	var otherServerID string
	tests := []struct {
		name        string
		setup       func(t *testing.T, e *env) (invoke func() error, count func() int)
		wantKind    error
		wantMessage string
		wraps       error
		wantReason  string
	}{
		{
			name: "project invalid name",
			setup: func(t *testing.T, e *env) (func() error, func() int) {
				t.Helper()
				p := e.principal(store.RoleOwner, wildcard)
				return func() error { return invokeProjectsCreate(e.reg, p, "Web!") },
					func() int { return countProjects(t, e) }
			},
			wantKind:    registry.ErrInvalidInput,
			wantMessage: `project name "Web!" must be 1 to 63 bytes of [a-z0-9-], starting and ending with a letter or digit`,
			wantReason:  "invalid input",
		},
		{
			name: "project duplicate name",
			setup: func(t *testing.T, e *env) (func() error, func() int) {
				t.Helper()
				createProject(t, e, "web")
				p := e.principal(store.RoleOwner, wildcard)
				return func() error { return invokeProjectsCreate(e.reg, p, "web") },
					func() int { return countProjects(t, e) }
			},
			wantKind:    registry.ErrConflict,
			wantMessage: `project "web" already exists`,
			wantReason:  "conflict",
		},
		{
			name: "project unmapped store error",
			setup: func(t *testing.T, e *env) (func() error, func() int) {
				t.Helper()
				reg := registry.New(&createErrorStore{Store: e.st, method: "project", err: unmapped}, func() time.Time { return t0 })
				p := e.principal(store.RoleOwner, wildcard)
				return func() error { return invokeProjectsCreate(reg, p, "web") },
					func() int { return countProjects(t, e) }
			},
			wraps:      unmapped,
			wantReason: "the handler returned an error",
		},
		{
			name: "environment invalid name",
			setup: func(t *testing.T, e *env) (func() error, func() int) {
				t.Helper()
				web := createProject(t, e, "web")
				p := e.principal(store.RoleOwner, wildcard)
				return func() error { return invokeEnvironmentsCreate(e.reg, p, "web", "Prod!") },
					func() int { return countEnvironments(t, e, web.ID) }
			},
			wantKind:    registry.ErrInvalidInput,
			wantMessage: `environment name "Prod!" must be 1 to 63 bytes of [a-z0-9-], starting and ending with a letter or digit`,
			wantReason:  "invalid input",
		},
		{
			name: "environment duplicate name",
			setup: func(t *testing.T, e *env) (func() error, func() int) {
				t.Helper()
				web := createProject(t, e, "web")
				createEnvironment(t, e, web.ID, "prod")
				p := e.principal(store.RoleOwner, wildcard)
				return func() error { return invokeEnvironmentsCreate(e.reg, p, "web", "prod") },
					func() int { return countEnvironments(t, e, web.ID) }
			},
			wantKind:    registry.ErrConflict,
			wantMessage: `environment "prod" already exists in project "web"`,
			wantReason:  "conflict",
		},
		{
			name: "environment project not found",
			setup: func(t *testing.T, e *env) (func() error, func() int) {
				t.Helper()
				web := createProject(t, e, "web")
				reg := registry.New(&createErrorStore{Store: e.st, method: "environment", err: store.ErrNotFound}, func() time.Time { return t0 })
				p := e.principal(store.RoleOwner, wildcard)
				return func() error { return invokeEnvironmentsCreate(reg, p, "web", "prod") },
					func() int { return countEnvironments(t, e, web.ID) }
			},
			wantKind:    registry.ErrNotFound,
			wantMessage: `project "web" not found`,
			wantReason:  "not found",
		},
		{
			name: "service invalid name",
			setup: func(t *testing.T, e *env) (func() error, func() int) {
				t.Helper()
				web := createProject(t, e, "web")
				prod := createEnvironment(t, e, web.ID, "prod")
				srv := createServer(t, e, e.orgID)
				p := e.principal(store.RoleOwner, wildcard)
				return func() error { return invokeServicesCreate(e.reg, p, "web", "prod", "Api!", srv.ID) },
					func() int { return countServices(t, e, prod.ID) }
			},
			wantKind:    registry.ErrInvalidInput,
			wantMessage: `service name "Api!" must be 1 to 63 bytes of [a-z0-9-], starting and ending with a letter or digit`,
			wantReason:  "invalid input",
		},
		{
			name: "service duplicate name",
			setup: func(t *testing.T, e *env) (func() error, func() int) {
				t.Helper()
				web := createProject(t, e, "web")
				prod := createEnvironment(t, e, web.ID, "prod")
				srv := createServer(t, e, e.orgID)
				createService(t, e, prod.ID, "app", srv.ID)
				p := e.principal(store.RoleOwner, wildcard)
				return func() error { return invokeServicesCreate(e.reg, p, "web", "prod", "app", srv.ID) },
					func() int { return countServices(t, e, prod.ID) }
			},
			wantKind:    registry.ErrConflict,
			wantMessage: `service "app" already exists in environment "prod" of project "web"`,
			wantReason:  "conflict",
		},
		{
			name: "service unknown server",
			setup: func(t *testing.T, e *env) (func() error, func() int) {
				t.Helper()
				web := createProject(t, e, "web")
				prod := createEnvironment(t, e, web.ID, "prod")
				p := e.principal(store.RoleOwner, wildcard)
				return func() error { return invokeServicesCreate(e.reg, p, "web", "prod", "api", "srv-ghost") },
					func() int { return countServices(t, e, prod.ID) }
			},
			wantKind:    registry.ErrNotFound,
			wantMessage: `server "srv-ghost" not found`,
			wantReason:  "not found",
		},
		{
			name: "service another org's server",
			setup: func(t *testing.T, e *env) (func() error, func() int) {
				t.Helper()
				web := createProject(t, e, "web")
				prod := createEnvironment(t, e, web.ID, "prod")
				otherOrg, err := e.st.CreateOrg(context.Background())
				if err != nil {
					t.Fatalf("CreateOrg error = %v, want nil", err)
				}
				other := createServer(t, e, otherOrg)
				otherServerID = other.ID
				p := e.principal(store.RoleOwner, wildcard)
				return func() error { return invokeServicesCreate(e.reg, p, "web", "prod", "api", other.ID) },
					func() int { return countServices(t, e, prod.ID) }
			},
			wantKind:    registry.ErrNotFound,
			wantMessage: `server "%s" not found`,
			wantReason:  "not found",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			invoke, count := tt.setup(t, e)
			before := count()
			err := invoke()
			if err == nil {
				t.Fatalf("Invoke error = nil, want a refusal")
			}
			if tt.wraps != nil {
				if !errors.Is(err, tt.wraps) {
					t.Errorf("Invoke error = %v, want it to wrap %v", err, tt.wraps)
				}
			} else {
				if !errors.Is(err, tt.wantKind) {
					t.Fatalf("Invoke error = %v, want it to match %v", err, tt.wantKind)
				}
				want := strings.ReplaceAll(tt.wantMessage, "%s", otherServerID)
				if got := err.Error(); got != want {
					t.Errorf("Invoke error = %q, want exactly %q", got, want)
				}
				if unwrapped := errors.Unwrap(err); unwrapped != nil {
					t.Errorf("errors.Unwrap(error) = %v, want nil: a refusal wraps nothing", unwrapped)
				}
			}
			for _, kind := range refusalKinds {
				if tt.wraps != nil || !errors.Is(kind, tt.wantKind) {
					if errors.Is(err, kind) {
						t.Errorf("Invoke error = %v, want it not to match %v", err, kind)
					}
				}
			}
			if strings.Contains(err.Error(), "store:") {
				t.Errorf("Invoke error = %q, want no store error text", err)
			}
			for _, sentinel := range []error{store.ErrNotFound, store.ErrDuplicate, store.ErrInvalidName} {
				if errors.Is(err, sentinel) {
					t.Errorf("Invoke error = %v, want it not to match %v", err, sentinel)
				}
			}
			if after := count(); after != before {
				t.Errorf("rows = %d after the refusal, want %d: nothing is written", after, before)
			}
			ev := onlyEvent(t, e)
			if ev.Outcome != "error" || ev.Reason != tt.wantReason {
				t.Errorf("audit outcome/reason = %q/%q, want %q/%q", ev.Outcome, ev.Reason, "error", tt.wantReason)
			}
		})
	}
}

// TestListsStayInTheirTarget checks that each list names only its target's rows
// and that an unknown target is the 404 refusal that runs no handler.
func TestListsStayInTheirTarget(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	web := createProject(t, e, "web")
	other := createProject(t, e, "api")
	webProd := createEnvironment(t, e, web.ID, "prod")
	webStaging := createEnvironment(t, e, web.ID, "staging")
	otherProd := createEnvironment(t, e, other.ID, "prod")
	_ = createEnvironment(t, e, other.ID, "staging")
	srv := createServer(t, e, e.orgID)
	inWebProd := createService(t, e, webProd.ID, "app", srv.ID)
	_ = createService(t, e, otherProd.ID, "app", srv.ID)
	_ = createService(t, e, webStaging.ID, "app", srv.ID)
	p := e.principal(store.RoleOwner, wildcard)

	envs, err := registry.Invoke(ctx, e.reg, e.reg.EnvironmentsList, p, registry.Target{Project: "web"},
		registry.EnvironmentsListIn{Project: "web"})
	if err != nil {
		t.Fatalf("environments.list of %q error = %v, want nil", "web", err)
	}
	wantEnvs := []string{webProd.ID, webStaging.ID}
	if len(envs.Environments) != len(wantEnvs) {
		t.Fatalf("environments.list of %q = %+v, want %v", "web", envs.Environments, wantEnvs)
	}
	for i, want := range wantEnvs {
		if got := envs.Environments[i].ID; got != want {
			t.Errorf("environments.list of %q [%d] id = %q, want %q", "web", i, got, want)
		}
	}

	svcs, err := registry.Invoke(ctx, e.reg, e.reg.ServicesList, p, registry.Target{Project: "web", Environment: "prod"},
		registry.ServicesListIn{Project: "web", Environment: "prod"})
	if err != nil {
		t.Fatalf("services.list of %q/%q error = %v, want nil", "web", "prod", err)
	}
	if len(svcs.Services) != 1 {
		t.Fatalf("services.list of %q/%q = %+v, want only the target environment's service", "web", "prod", svcs.Services)
	}
	if got := svcs.Services[0].ID; got != inWebProd.ID {
		t.Errorf("services.list of %q/%q id = %q, want %q", "web", "prod", got, inWebProd.ID)
	}

	cs := &handlerCountStore{Store: e.st}
	reg := registry.New(cs, func() time.Time { return t0 })
	targets := []struct {
		name      string
		operation string
		invoke    func(p registry.Principal) error
		message   string
		count     func() int
	}{
		{
			name:      "environments.list with an unknown project",
			operation: "environments.list",
			invoke: func(p registry.Principal) error {
				_, err := registry.Invoke(ctx, reg, reg.EnvironmentsList, p, registry.Target{Project: "ghost"},
					registry.EnvironmentsListIn{Project: "ghost"})
				return err
			},
			message: `project "ghost" not found`,
			count:   func() int { return countEnvironments(t, e, web.ID) },
		},
		{
			name:      "services.list with an unknown environment",
			operation: "services.list",
			invoke: func(p registry.Principal) error {
				_, err := registry.Invoke(ctx, reg, reg.ServicesList, p, registry.Target{Project: "web", Environment: "ghost"},
					registry.ServicesListIn{Project: "web", Environment: "ghost"})
				return err
			},
			message: `environment "ghost" not found in project "web"`,
			count:   func() int { return countServices(t, e, webProd.ID) },
		},
		{
			name:      "environments.create with an unknown project",
			operation: "environments.create",
			invoke: func(p registry.Principal) error {
				_, err := registry.Invoke(ctx, reg, reg.EnvironmentsCreate, p, registry.Target{Project: "ghost"},
					registry.EnvironmentsCreateIn{Project: "ghost", Name: "new"})
				return err
			},
			message: `project "ghost" not found`,
			count:   func() int { return countEnvironments(t, e, web.ID) },
		},
		{
			name:      "services.create with an unknown environment",
			operation: "services.create",
			invoke: func(p registry.Principal) error {
				_, err := registry.Invoke(ctx, reg, reg.ServicesCreate, p, registry.Target{Project: "web", Environment: "ghost"},
					registry.ServicesCreateIn{Project: "web", Environment: "ghost", Name: "new", Server: srv.ID})
				return err
			},
			message: `environment "ghost" not found in project "web"`,
			count:   func() int { return countServices(t, e, webProd.ID) },
		},
	}
	for _, tt := range targets {
		t.Run(tt.name, func(t *testing.T) {
			before := tt.count()
			err := tt.invoke(p)
			if !errors.Is(err, registry.ErrNotFound) {
				t.Fatalf("%s error = %v, want it to match registry.ErrNotFound", tt.operation, err)
			}
			if err == nil || err.Error() != tt.message {
				t.Errorf("%s error = %q, want %q", tt.operation, err, tt.message)
			}
			if errors.Is(err, store.ErrNotFound) {
				t.Errorf("%s error = %v, want it not to match store.ErrNotFound", tt.operation, err)
			}
			if after := tt.count(); after != before {
				t.Errorf("rows = %d after the refusal, want %d: nothing is written", after, before)
			}
			evs, err := e.st.AuditEvents(ctx, e.orgID)
			if err != nil {
				t.Fatalf("AuditEvents error = %v, want nil", err)
			}
			if len(evs) == 0 {
				t.Fatalf("AuditEvents = no events, want one for %s", tt.operation)
			}
			ev := evs[len(evs)-1]
			if ev.Operation != tt.operation || ev.Outcome != "error" || ev.Reason != "not found" {
				t.Errorf("audit operation/outcome/reason = %q/%q/%q, want %q/%q/%q",
					ev.Operation, ev.Outcome, ev.Reason, tt.operation, "error", "not found")
			}
		})
	}
	if cs.createEnvironment != 0 || cs.environments != 0 || cs.createService != 0 || cs.services != 0 {
		t.Errorf("handler store calls = createEnvironment %d, environments %d, createService %d, services %d; want all 0",
			cs.createEnvironment, cs.environments, cs.createService, cs.services)
	}
}
