---
verdict: ready
reason: ""
---

# Plan for W2-11d

Six operations over existing store methods. No contract change: the six rows are already in `docs/specs/registry.md`'s table, the store methods exist (W2-11a/h), REST mounting and target filling exist (W2-11f: `mount` fills `registry.Target` from string inputs named `project` and `environment`), CLI generation exists (W2-11g: `environment` becomes `--env`). No new module.

## Files to change

- `internal/registry/operations.go`
  - Specs, as the spec table gives them, all `Destructive: No`:
    - `projectsCreateSpec` `{Name: "projects.create", Summary: "Create a project", Method: "POST", Path: "/v1/projects", MinRole: store.RoleMember}`
    - `projectsListSpec` `{"projects.list", "List projects", "GET", "/v1/projects", RoleViewer}`
    - `environmentsCreateSpec` `{"environments.create", "Create an environment", "POST", "/v1/environments", RoleMember}`
    - `environmentsListSpec` `{"environments.list", "List environments", "GET", "/v1/environments", RoleViewer}`
    - `servicesCreateSpec` `{"services.create", "Create a service", "POST", "/v1/services", RoleMember}`
    - `servicesListSpec` `{"services.list", "List services", "GET", "/v1/services", RoleViewer}`
  - Inputs, every field `string` with `json` (no `omitempty`) and `doc` tags:
    - `ProjectsCreateIn{Name}`; `ProjectsListIn struct{}`
    - `EnvironmentsCreateIn{Project, Name}`; `EnvironmentsListIn{Project}`
    - `ServicesCreateIn{Project, Environment, Name, Server}`; `ServicesListIn{Project, Environment}`
  - Outputs:
    - `ProjectInfo{ID "id", Name "name", CreatedAt "created_at"}`, `EnvironmentInfo` (same fields), `ServiceInfo{ID, Name, Server "server", CreatedAt}`.
    - Creates return the row type directly (`*Operation[ProjectsCreateIn, ProjectInfo]` and so on).
    - `ProjectsListOut{Projects []ProjectInfo "projects"}`, `EnvironmentsListOut{Environments "environments"}`, `ServicesListOut{Services "services"}`.
  - `Operations()`: append the six with `reflect.TypeFor[...In]()`, after `servers.list`, in the spec table's order.
- `internal/registry/registry.go`
  - Six `Registry` fields, each with a doc comment: `ProjectsCreate`, `ProjectsList`, `EnvironmentsCreate`, `EnvironmentsList`, `ServicesCreate`, `ServicesList`, each registered in `New` with `registerShipped`, in the same style as `ServersList`.
  - Handlers use only `c` and `st`:
    - `ProjectsCreate`: `st.CreateProject(ctx, c.OrgID, in.Name)`, error through `createRefusal`.
    - `ProjectsList`: `st.Projects(ctx, c.OrgID)`. `EnvironmentsCreate`: `st.CreateEnvironment(ctx, c.OrgID, c.ProjectID, in.Name)`. `EnvironmentsList`: `st.Environments(ctx, c.OrgID, c.ProjectID)`. `ServicesCreate`: `st.CreateService(ctx, c.OrgID, c.EnvironmentID, in.Name, in.Server)`. `ServicesList`: `st.Services(ctx, c.OrgID, c.EnvironmentID)`.
    - Each list builds its slice with `make([]T, 0, len(rows))`, so an empty list is `[]`, never null, and keeps the store's name order.
  - One private helper does the store-error mapping for the three creates:
    `func createRefusal(err error, kind, name, duplicate, notFound string) error`
    - `errors.Is(err, store.ErrInvalidName)` → `Refuse(ErrInvalidInput, fmt.Sprintf("%s name %q must be 1 to 63 bytes of [a-z0-9-], starting and ending with a letter or digit", kind, name))`
    - `store.ErrDuplicate` → `Refuse(ErrConflict, duplicate)`
    - `store.ErrNotFound` with `notFound != ""` → `Refuse(ErrNotFound, notFound)`
    - otherwise → `fmt.Errorf("create %s: %w", kind, err)`, which is unmapped and becomes a 500.
    - The callers pass the fixed messages, built from inputs only:
      - project: `project %q already exists`, no not-found message.
      - environment: `environment %q already exists in project %q`; not found: `project %q not found`.
      - service: `service %q already exists in environment %q of project %q`; not found: `server %q not found`.
  - List errors are wrapped: `fmt.Errorf("list projects: %w", err)` and the same for environments and services.
- `internal/api/api.go`: `build` gains six `mount(api, reg, reg.X)` lines, in `Operations()` order.
- `internal/api/openapi.json`, `internal/cli/generated.go`: rewritten by `make generate` only.
- `internal/api/routes_test.go` (`TestEveryMountedRouteIsOperationOrAllowlisted`, as the owner allowed):
  - Before the route loop, seed through the store: `CreateProject(orgID, "web")`, `CreateEnvironment(orgID, p.ID, "prod")`, and `AddServer(orgID, <fixed test key>, <fixed age recipient>)`. Copy the key and recipient argument shapes from an existing `AddServer` call in the store conformance tests.
  - A `requests map[string]struct{ query, body string }` keyed by operation name:
    - `projects.create` body `{"name":"routes-p"}`
    - `environments.create` body `{"project":"web","name":"routes-e"}`
    - `services.create` body `{"project":"web","environment":"prod","name":"routes-s","server":"<id>"}`
    - `environments.list` query `project=web`
    - `services.list` query `project=web&environment=prod`
    - `servers.list` and `projects.list` empty.
  - `do` takes the path with the query appended, and the body. The no-token and bad-token cases send the same inputs.
  - The names are fresh, so the owner-token POSTs answer 200 once. Each case runs once per operation, and only the owner case creates.
  - A missing map entry for an operation fails the test with a message. This makes a later operation add its own valid inputs.
  - The other checks are unchanged.
- `docs/specs/registry.md`: under the W2-11 table bullets, one block for the six:
  - the inputs, as above;
  - the outputs: create → row; list → `projects`/`environments`/`services` array in name order, `[]` when empty; project/environment `id, name, created_at`; service `id, name, server, created_at`;
  - the refusal messages and their kinds, and that any other store error is a 500.
  - The existing `services.create` bullet is folded in, so nothing is said twice.
- `testdata/tests.txt`: `make test-list`.

## Tests to write

New file `internal/registry/projects_test.go`, using `newEnv`, `e.principal`, `wildcard`, `onlyEvent`, `createProject` and `createEnvironment`.

- `./internal/registry TestProjectEnvironmentServiceSpecs`: table of the six with the want `Spec` and want input json names.
  - For each, it finds the declaration in `Operations()`, compares `d.Spec == want` and `d.In == reflect.TypeFor[...]()`.
  - It walks `d.In`'s fields and asserts: exactly the want json names in order, each `reflect.String`, no `omitempty`, and a non-empty `doc`.
- `./internal/registry TestProjectsCreateAndList`:
  - The owner principal invokes `reg.ProjectsCreate` with `Target{}` for `web` and then `api`. Each output has a non-empty `ID`, the given `Name`, and a `CreatedAt` that parses as RFC 3339.
  - `ProjectsList` returns `[api, web]` with the same ids.
  - A second org, made with `e.st.CreateOrg`, with the principal's `OrgID` swapped: `ProjectsList` gives `Projects != nil && len == 0`.
- `./internal/registry TestEnvironmentsCreateAndList`:
  - Seeds project `web`, then invokes `EnvironmentsCreate` with `Target{Project: "web"}` and input `{Project: "web", Name: "prod"}`, then `staging`.
  - `e.st.Environments(orgID, web.ID)` holds both. That proves `Call.ProjectID` was used.
  - `EnvironmentsList` returns `[prod, staging]` with id, name and `created_at` equal to the create outputs.
- `./internal/registry TestServicesCreateAndList`:
  - Seeds project, environment and `AddServer`. `ServicesCreate` with `Target{web, prod}` creates `api` and then `worker`.
  - Each output has `Server == server.ID`. `e.st.Services(orgID, env.ID)` holds them.
  - `ServicesList` returns `[api, worker]` with id, name, server and created_at equal to the create outputs.
- `./internal/registry TestCreateRefusalsFromStoreErrors`: a table over the three creates. Cases:
  - project invalid `Web!`; project duplicate `web`;
  - environment invalid; environment duplicate (`environment "prod" already exists in project "web"`);
  - service invalid; service duplicate; service unknown server `srv-ghost`; service with another org's server (`AddServer` in a second org).
  - Each case asserts:
    - `errors.Is(err, wantKind)` and `err.Error() == wantMessage` exactly;
    - `!strings.Contains(err.Error(), "store:")`;
    - `errors.Unwrap(err) == nil`;
    - not `errors.Is(err, store.ErrX)`.
  - The audit event of that call is the last event: outcome `error`, and reason `invalid input`, `conflict` or `not found`.
  - Nothing is written: the row count from the list store method is the same before and after the call.
  - Setup rows are made through the store directly, so the call under test is the only Invoke and `onlyEvent` applies.
- `./internal/registry TestListsStayInTheirTarget`:
  - Seeds projects `web` and `api`, each with environments `prod` and `staging`, and in `web/prod` and `api/prod` a service `app` on one server, plus `web/staging` service `app`.
  - `EnvironmentsList{web}` returns only web's environment ids. `ServicesList{web, prod}` returns only the web/prod service id.
  - Then a table of unknown targets, each giving `ErrNotFound` with the W2-11c message, the audit reason `not found`, and no rows created (checked by store counts):
    - `EnvironmentsList` with project `ghost`;
    - `ServicesList` with `web/ghost`;
    - `EnvironmentsCreate` with `ghost`;
    - `ServicesCreate` with `web/ghost`.
  - To prove no handler runs, wrap the store in a type embedding `store.Store` that counts `CreateEnvironment`, `Environments`, `CreateService` and `Services` calls. The four counts are 0 after these cases.
- `./internal/api TestProjectEnvironmentServiceRoutes`, new file `internal/api/projects_test.go`, package `api`:
  - Use `newAPIEnv`. Mount the real operations through `build(e.humaAPI, e.reg)`, the server's own path, not `mountTest`. Seed a server with `e.st.AddServer`.
  - Through `e.do`:
    - `POST /v1/projects {"name":"web"}` → 200, with JSON `id`/`name`/`created_at`;
    - `POST /v1/environments {"project":"web","name":"prod"}` → 200;
    - `POST /v1/services {...,"server":id}` → 200, with `server` equal to the id;
    - `GET /v1/projects`, `GET /v1/environments?project=web` and `GET /v1/services?project=web&environment=prod` → 200 with the created rows;
    - a repeated `POST /v1/projects {"name":"web"}` → 409 with detail `project "web" already exists`;
    - `GET /v1/environments?project=ghost` → 404;
    - `POST /v1/services` without `server` → 400, with `errorLocations` containing `body.server`.
- `./internal/cli TestGeneratedProjectEnvironmentServiceCommands`, new file `internal/cli/projects_test.go`:
  - Use `captureServer` with a 200 `{}` reply, and `cli.RunDeclared(registry.Operations(), …)`.
  - A table per command checks:
    - with every flag given, the exit is 0 and the request has the right method and path;
    - `services list --project web --env prod` sends a GET with `url.ParseQuery(rawQuery)` equal to `project=web, environment=prod` and an empty body;
    - `services create` sends one POST with the JSON body `{"project","environment","name","server"}`, decoded and compared as a map;
    - for each flag of each command, leaving it out exits 2 and nothing is sent. This proves every flag is required and that the names are `--name`, `--project`, `--env` and `--server`. A wrong flag name exits 2 as an unknown flag, so the full-flag run proves the names.
  - `projects list` takes no flags and sends a GET with an empty query.
  - For the setup, reuse `newCaptureServer` (or whatever constructor `generated_inputs_test.go` defines) and write a local run helper that calls `RunDeclared(registry.Operations(), ...)`, because `runTest` binds `testDeclarations`.

## Edge cases

- Invalid name, duplicate name, unknown server, other org's server: `TestCreateRefusalsFromStoreErrors`.
- A project removed between resolve and `CreateEnvironment` (store `ErrNotFound`) → `project "<project>" not found`. The mapping is the same `createRefusal` path and the not-found message is in the table for services. The environment not-found message is asserted through a store wrapper case in the same table: a store whose `CreateEnvironment` returns `store.ErrNotFound` is the only way to reach it.
- An unmapped store error is wrapped, so it is not a refusal and becomes a 500. Add one table case using a wrapper store that returns `errors.New("disk full")` from `CreateProject`. It asserts `!errors.Is` of any refusal kind and the audit reason `the handler returned an error`.
- An empty list gives `[]`, not null: the second-org case in `TestProjectsCreateAndList`. JSON `[]` is covered by the GET of `environments.list` before any environment exists, in the API test.
- Unknown target names: W2-11c `Invoke`, asserted in `TestListsStayInTheirTarget` with zero handler calls.
- Hostile names, such as quotes and unicode: messages use `%q`, so they are quoted and escaped, and the store rejects them as an invalid name. The `Web!` case covers this.
- A missing required input → 400 before Invoke: API test, `body.server`.

## Risks

- **Diff cap 380 counted lines.** Tests and generated files are not counted, so the counted lines are the types (~75), the handlers and helper (~110), `build` (6) and the spec (~35), about 230. Keep handlers compact and share `createRefusal`.
- **`routes_test.go` 200 for every operation.** POSTs need fresh names, and a GET needs its required query. If an entry is missing, the test fails with a message instead of a status mismatch. The bad-token cases still answer 401, because `principal` runs after decode and valid inputs pass decode.
- **The order of `decode` and `principal` in `mount`.** Decode runs first, so a no-token request with an empty body would be 400, not 401. This is why the routes test must send valid inputs to the unauthenticated cases too, as the ticket says.
- **OpenAPI built with a nil store.** The handlers close over `st` and call it only when invoked, so `OpenAPI()` and `cli.Generate` work with `registry.New(nil, …)`.
- **Generated files drift.** Run `make generate` after `operations.go` and `api.go` change, then `make test-list` and `make ci`. Never edit either file by hand.
- **Banned words.** None in the new code or comments.
- **Store error text leaking.** Every refusal is built from inputs and fixed text. The tests assert exact text and `errors.Unwrap == nil`.
- **Trusted-core review.** `internal/registry` is risk high. No handler calls `ResolveTarget`, reads `Target`, or writes audit, which keeps review simple.
