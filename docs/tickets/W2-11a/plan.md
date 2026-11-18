---
verdict: ready
reason: ""
---

# Plan for W2-11a

The tables, names and conformance cases are already specified in `docs/specs/storage.md` "Projects, environments, services and releases (W2-11)" (owner decisions D6, D18), so no contract or ADR changes, and no new module. Counted lines (non-test, non-doc): about 280 (two migrations ~70, `store.go` ~45, `names.go` ~20, `projects.go` ~150), under the 400 cap.

## Files to change

- `internal/store/migrations/pg/0003_projects.sql` (new):
  - `projects (id TEXT NOT NULL PRIMARY KEY, org_id TEXT NOT NULL REFERENCES orgs (id), name TEXT NOT NULL, created_at TEXT NOT NULL, UNIQUE (org_id, name))`.
  - `environments (id … PRIMARY KEY, org_id … REFERENCES orgs (id), project_id TEXT NOT NULL REFERENCES projects (id), name, created_at, UNIQUE (org_id, project_id, name))`.
  - `services (id … PRIMARY KEY, org_id … REFERENCES orgs (id), project_id … REFERENCES projects (id), environment_id TEXT NOT NULL REFERENCES environments (id), name, server_id TEXT NOT NULL REFERENCES servers (id), created_at, UNIQUE (org_id, environment_id, name))`.
  - Every column `TEXT NOT NULL`. The unique constraints lead with `org_id`, so they also serve the org-scoped lists; no extra indexes.
  - `ALTER TABLE … ENABLE ROW LEVEL SECURITY` and `CREATE POLICY org_isolation ON …` with the exact `USING`/`WITH CHECK` expression of `0001_init.sql`, for all three tables.
  - No grants in the file: `pg.Migrate` already grants on every table it finds after applying the files.
- `internal/store/migrations/sqlite/0003_projects.sql` (new): the same three `CREATE TABLE` statements, character for character, without the RLS and policy statements. SQLite enforces the foreign keys (`foreign_keys(1)` in `sqlite.Open`).
- `internal/store/store.go`:
  - `ErrInvalidName = errors.New("store: invalid name")` added to the error block.
  - `type Project struct { OrgID, ID, Name, CreatedAt string }`; `type Environment struct { OrgID, ID, ProjectID, Name, CreatedAt string }`; `type Service struct { OrgID, ID, ProjectID, EnvironmentID, Name, ServerID, CreatedAt string }`.
  - `Store` interface gains, with one-line doc comments naming the error sentinels:
    - `CreateProject(ctx context.Context, orgID, name string) (Project, error)`
    - `Projects(ctx context.Context, orgID string) ([]Project, error)`
    - `CreateEnvironment(ctx context.Context, orgID, projectID, name string) (Environment, error)`
    - `Environments(ctx context.Context, orgID, projectID string) ([]Environment, error)`
    - `CreateService(ctx context.Context, orgID, environmentID, name, serverID string) (Service, error)`
    - `Services(ctx context.Context, orgID, environmentID string) ([]Service, error)`
    - `ResolveTarget(ctx context.Context, orgID, project, environment string) (projectID, environmentID string, err error)`
  - Other implementers: only `internal/api/errors_test.go` `failingAudit`, which embeds `store.Store` and keeps compiling.
- `internal/store/names.go` (new):
  - `var namePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)`: 1 to 63 bytes, a letter or digit at both ends.
  - `func checkName(kind, name string) error`: nil on a match, otherwise `fmt.Errorf("%s name %q must be 1 to 63 bytes of [a-z0-9-], starting and ending with a letter or digit: %w", kind, name, ErrInvalidName)`.
- `internal/store/projects.go` (new), the `SQLStore` methods. Every query has an `org_id = $n` predicate, runs inside `s.inOrg(ctx, orgID, …)`, and uses each `$n` once and in order (`RebindQuestion`):
  - `CreateProject`: `checkName("project", name)` first, then `NewID`, `formatTime(time.Now())`, `INSERT INTO projects (id, org_id, name, created_at)`; insert error through `s.dupOr(err, "insert project")`.
  - `CreateEnvironment`: `checkName("environment", name)`; in the transaction `SELECT id FROM projects WHERE id = $1 AND org_id = $2`; `sql.ErrNoRows` becomes `fmt.Errorf("create environment: project %s is not in org %s: %w", projectID, orgID, ErrNotFound)`; then the insert through `dupOr`.
  - `CreateService`: `checkName("service", name)`; in the transaction `SELECT project_id FROM environments WHERE id = $1 AND org_id = $2` (not found → `ErrNotFound` naming the environment), then `SELECT id FROM servers WHERE id = $1 AND org_id = $2` (not found → `ErrNotFound` naming the server, with the same message whether the id is unknown or another org's), then `INSERT INTO services (id, org_id, project_id, environment_id, name, server_id, created_at)` with the `project_id` read from the environment row, through `dupOr`. These explicit reads are what refuses cross-org parents: Postgres referential checks ignore RLS.
  - `Projects`: `SELECT org_id, id, name, created_at FROM projects WHERE org_id = $1 ORDER BY name`.
  - `Environments`: `SELECT org_id, id, project_id, name, created_at FROM environments WHERE org_id = $1 AND project_id = $2 ORDER BY name`.
  - `Services`: `SELECT org_id, id, project_id, environment_id, name, server_id, created_at FROM services WHERE org_id = $1 AND environment_id = $2 ORDER BY name`.
  - Lists follow the `Memberships` pattern (rows loop, `rows.Err()`), and wrap select and scan errors (`"select projects: %w"`, `"scan project: %w"`). An unknown parent id gives an empty list, not an error.
  - `ResolveTarget`: in one transaction `SELECT id FROM projects WHERE org_id = $1 AND name = $2` (no rows → `fmt.Errorf("resolve target: project %q not found: %w", project, ErrNotFound)`); when `environment != ""`, `SELECT id FROM environments WHERE org_id = $1 AND project_id = $2 AND name = $3` (no rows → `ErrNotFound` naming project and environment). Empty `environment` returns `(projectID, "", nil)`. Names are not format-checked here: a name that cannot be stored is simply not found.
- `docs/specs/storage.md`, in the W2-11 section:
  - under `services`: "W2-11a creates `services` without `current_release_id` and `previous_release_id`; W2-11b adds both with `releases`."
  - after the conformance sentence: W2-11a adds `runProjectsConformance` (`TestProjectsConformanceSQLite`, `TestProjectsConformancePostgres`), `TestPostgresRLSOnProjectTables` and `TestProjectNamesValidated`, and lists the cases they cover; the release cases come with W2-11b.
- `testdata/tests.txt`: regenerated with `make test-list`.

## Tests to write

All in package `store_test`, using the existing `newSQLite`, `newPGEnv`, `openApp`, `rawDB`, `orgIDsUnder` and `randHex` from `helpers_test.go`/`pg_rls_test.go`.

- `internal/store/projects_conformance_test.go`:
  - `./internal/store TestProjectsConformanceSQLite`: `s, _ := newSQLite(t); runProjectsConformance(t, s)`.
  - `./internal/store TestProjectsConformancePostgres`: `runProjectsConformance(t, newPGEnv(t, true).openApp(t))`.
  - `runProjectsConformance(t, s store.Store)`. A `seed` helper creates a fresh org and one server (`AddServer` with a random public key) so subtests are independent. Subtests:
    - `create and list`: project `web`, environment `prod` in it, service `api` on the org's server; `Projects`, `Environments`, `Services` each return exactly that one row with the returned id, `OrgID`, name, parent ids (`ProjectID`, `EnvironmentID`), `ServerID` and a non-empty `CreatedAt`; a second project `alpha` lists before `web` (order by name). Failures print got and want.
    - `cross-org parents`: orgs A and B, each seeded with project, environment, server. `CreateService(A, envA, "x", serverB)`, `CreateService(A, envA, "x", randHex id)`, `CreateService(A, envB, "x", serverA)` and `CreateEnvironment(A, projectB, "x")` each return `errors.Is(err, store.ErrNotFound)`; afterwards `Services(A, envA)` and `Environments(A, projectA)` still hold only the seeded rows, and B's lists are unchanged.
    - `duplicates`: second `CreateProject(A, "web")`, second `CreateEnvironment(A, p, "prod")`, second `CreateService(A, e, "api", srv)` return `ErrDuplicate`; `CreateEnvironment(A, p2, "prod")`, `CreateService(A, e2, "api", srv)` (e2 in another project or environment) and `CreateProject(B, "web")` succeed.
    - `resolve target`: `ResolveTarget(A, "web", "prod")` returns the project and environment ids; `ResolveTarget(A, "web", "")` returns the project id and `""`; unknown project, unknown environment, and a project name that exists only in B each return `ErrNotFound`.
    - `second org sees nothing`: with A populated, `Projects(B)` is empty, `Environments(B, projectA)` is empty, `Services(B, envA)` is empty.
  - `./internal/store TestPostgresRLSOnProjectTables`: two orgs, each with server, project, environment and service created through the store. On `env.rawDB(t, env.appDSN)`:
    - for `app.org_id` unset, empty and a random nonexistent id, `orgIDsUnder` on `projects`, `environments`, `services` returns no rows;
    - for `app.org_id` A it returns rows, all equal to A;
    - for each table, under `app.org_id` A, an `INSERT` of a row for org B whose parent ids are B's real rows (so foreign keys pass and only `WITH CHECK` can refuse) fails with `pgconn.PgError` code `42501`; the same insert for org A with A's parent ids succeeds (control), each in its own rolled-back transaction;
    - `pg_tables.rowsecurity` is true for the three tables (superuser handle).
- `internal/store/names_test.go`:
  - `./internal/store TestProjectNamesValidated`: table-driven on SQLite. One org with a server, a seeded project `p` and environment `e`.
    - Accepted: `a`, `a-1`, `strings.Repeat("a", 63)`: for each, `CreateProject(org, n)`, `CreateEnvironment(org, thatProject, n)` and `CreateService(org, thatEnv, n, srv)` succeed.
    - Rejected: `""`, `strings.Repeat("a", 64)`, `"Web"`, `"a_b"`, `"a.b"`, `"a b"`, `"-a"`, `"a-"`: `CreateProject(org, n)`, `CreateEnvironment(org, p, n)` and `CreateService(org, e, n, srv)` each return `errors.Is(err, store.ErrInvalidName)` and an error text containing the quoted name; `SELECT count(*)` of `projects`, `environments`, `services` on the raw handle is the same before and after.

## Edge cases

- Invalid name: checked before any SQL, so no transaction and no row (`TestProjectNamesValidated` row counts).
- Cross-org server, unknown server, cross-org environment, cross-org project: explicit org-scoped reads give `ErrNotFound` with one message for unknown and foreign ids, so another org's id is not revealed (`cross-org parents`).
- Duplicate names: unique constraints mapped by `dupOr` on both backends (`duplicates`); the transaction is rolled back by `inOrg`.
- Same name under a different parent or org: allowed by the constraint shape (`duplicates`).
- Empty environment in `ResolveTarget`: project only (`resolve target`); empty or unknown project: `ErrNotFound`.
- A list with another org's parent id: empty, through the `org_id` predicate and on Postgres also RLS (`second org sees nothing`).
- Missing `WHERE org_id` on Postgres: RLS returns nothing or only the bound org's rows; an insert for another org fails `WITH CHECK` (`TestPostgresRLSOnProjectTables`).
- Hostile names (`..`, `/`, upper case, whitespace, leading `-`) are outside `[a-z0-9-]` or the end rule and are refused, so a stored name is always a valid `agent.toml` path element and Docker name.

## Risks

- `TestSchemasMatch`: the two migration files must declare identical columns and nullability; both are written from one text, differing only in the Postgres RLS statements.
- `TestLookupTablesMatchSchema`: the new tables have `org_id` and RLS and are not added to `LookupTables`, so it passes unchanged.
- `RebindQuestion` needs each `$n` once and in order; every query above uses them that way.
- The RLS insert test could pass for the wrong reason (a foreign-key error); using the other org's real parent ids, checking code `42501`, and the succeeding org-A control rule that out.
- Postgres tests skip locally without `MOONGLASS_TEST_POSTGRES` but run in CI; run them locally against a `postgres:18` container before `scripts/verify`.
- Diff size: tests and docs are not counted; product lines are about 280, under 400.
- Banned words: none of TODO, FIXME, XXX, HACK, fallback, legacy in added lines.
- No new dependency: `pgconn` in the test is already in `go.mod` (ADR-0010).

## Verification

- `make test-one PKG=./internal/store NAME='TestProjectsConformanceSQLite|TestProjectNamesValidated'`, then with `MOONGLASS_TEST_POSTGRES` set: `TestProjectsConformancePostgres`, `TestPostgresRLSOnProjectTables`, `TestSchemasMatch`, `TestLookupTablesMatchSchema`.
- `make test-list`, `make ci`, `scripts/verify check W2-11a`.
