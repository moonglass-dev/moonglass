---
verdict: ready
reason: ""
---

# Plan for W2-11e

The spec already defines `services.current_release_id`, `services.previous_release_id` (nullable) and `rollbacks` (`docs/specs/storage.md` W2-11). This ticket builds them, so no contract or ADR change is needed. No new module. `SQLStore` is the only implementation of `store.Store` (no fakes elsewhere), so the new interface methods break nothing outside `internal/store`. Tests do not count toward the 390-line cap (`countsTowardSize`); the estimate for non-test code is about 280 lines.

## Files to change

- `internal/store/migrations/sqlite/0006_rollbacks.sql` (new):
  - `ALTER TABLE services ADD COLUMN current_release_id TEXT NOT NULL DEFAULT '';`
  - `ALTER TABLE services ADD COLUMN previous_release_id TEXT;`
  - `CREATE TABLE rollbacks (id TEXT NOT NULL PRIMARY KEY, org_id TEXT NOT NULL REFERENCES orgs (id), service_id TEXT NOT NULL REFERENCES services (id), request_id TEXT NOT NULL DEFAULT '', status TEXT NOT NULL CHECK (status IN ('running', 'succeeded', 'failed', 'rejected')), detail TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, finished_at TEXT NOT NULL DEFAULT '');`
- `internal/store/migrations/pg/0006_rollbacks.sql` (new): the same three statements, then `ALTER TABLE rollbacks ENABLE ROW LEVEL SECURITY;` and `CREATE POLICY org_isolation ON rollbacks USING (org_id = nullif(current_setting('app.org_id', true), '')) WITH CHECK (<same>);`. No grants (`pg.Migrate` grants on every table it lists). No index, no foreign key on the release-id columns.
- `internal/store/store.go`:
  - `Service` gains `CurrentReleaseID string` and `PreviousReleaseID string`, after `ServerID` (all strings, so fieldalignment is satisfied).
  - `ErrRollbackState = errors.New("store: rollback is not in a status that allows this")` in the error block.
  - `type Rollback struct { OrgID, ID, ServiceID, RequestID string; Status ReleaseStatus; Detail, CreatedAt, FinishedAt string }`, with a doc comment saying that a rollback's status is `running`, `succeeded`, `failed` or `rejected` from `ReleaseStatus`.
  - `Store` interface gains these methods, each with a one-line contract comment:
    - `ServiceByName(ctx context.Context, orgID, environmentID, name string) (Service, error)`
    - `SetServiceReleases(ctx context.Context, orgID, serviceID, currentReleaseID, previousReleaseID string) error`
    - `StartRollback(ctx context.Context, orgID, serviceID, requestID string, now time.Time) (Rollback, error)`
    - `FinishRollback(ctx context.Context, orgID, rollbackID string, status ReleaseStatus, detail string, now time.Time) (Rollback, error)`
    - `InFlightRollbacks(ctx context.Context, orgID, serverID string) ([]Rollback, error)`
- `internal/store/projects.go`:
  - `const serviceColumns = "org_id, id, project_id, environment_id, name, server_id, current_release_id, COALESCE(previous_release_id, ''), created_at"` and `func scanService(row interface{ Scan(...any) error }) (Service, error)`. `COALESCE` maps `NULL` to empty text on both backends, so `Service` stays plain strings.
  - `Services` selects `serviceColumns` and scans with `scanService`. This is the only scan path for services.
  - `CreateService` is unchanged: its INSERT omits the two columns, so they take the defaults (`''` and `NULL`), and the returned `Service` has both fields empty.
  - `ServiceByName`: inside `inOrg`, `SELECT serviceColumns FROM services WHERE org_id = $1 AND environment_id = $2 AND name = $3`. `sql.ErrNoRows` becomes `fmt.Errorf("service by name: service %q not found in environment %s: %w", name, environmentID, ErrNotFound)`, and any other error is wrapped as `select service: %w`.
  - `SetServiceReleases`: inside `inOrg`, `UPDATE services SET current_release_id = $1, previous_release_id = $2 WHERE id = $3 AND org_id = $4`. The previous id is bound as `sql.NullString{String: prev, Valid: prev != ""}`, so an empty previous id is stored as `NULL`. `RowsAffected() == 0` returns `ErrNotFound` naming the service and org. Exec and RowsAffected errors are wrapped with context. The ids are not checked or interpreted.
- `internal/store/rollbacks.go` (new):
  - `const rollbackColumns = "b.id, b.org_id, b.service_id, b.request_id, b.status, b.detail, b.created_at, b.finished_at"` and `scanRollback(row) (Rollback, error)`, which converts `status` to `ReleaseStatus`.
  - `StartRollback`: `NewID()`, then inside `inOrg`, `SELECT id FROM services WHERE id = $1 AND org_id = $2`. `ErrNoRows` returns `fmt.Errorf("start rollback: service %s is not in org %s: %w", …, ErrNotFound)`. Then `INSERT INTO rollbacks (id, org_id, service_id, request_id, status, detail, created_at, finished_at) VALUES ($1…$8)` with `'running'`, `""`, `formatTime(now)`, `""`. It returns the built `Rollback`, which is exactly the stored row.
  - `FinishRollback`: before any SQL, a status other than `ReleaseSucceeded`, `ReleaseFailed` or `ReleaseRejected` returns `fmt.Errorf("finish rollback: status %q is not one of succeeded, failed, rejected", status)`. Inside `inOrg`, `UPDATE rollbacks SET status = $1, detail = $2, finished_at = $3 WHERE id = $4 AND org_id = $5 AND status = 'running' RETURNING id, org_id, service_id, request_id, status, detail, created_at, finished_at` is scanned with `scanRollback`. `RETURNING` already exists in `UseEnrolmentToken` and works on both backends. On `ErrNoRows`, `SELECT status FROM rollbacks WHERE id = $1 AND org_id = $2` in the same transaction decides the error: no row returns `ErrNotFound`, and otherwise the error is `rollback %s is %s: %w` with `ErrRollbackState`. The UPDATE did not match, so nothing changed.
  - `InFlightRollbacks`: inside `inOrg`, `SELECT rollbackColumns FROM rollbacks b JOIN services v ON v.id = b.service_id WHERE b.org_id = $1 AND v.org_id = $2 AND v.server_id = $3 AND b.status = 'running' ORDER BY b.created_at, b.id`, with args `orgID, orgID, serverID`. Each placeholder is used once and in order, for `RebindQuestion`. Errors are wrapped as `select rollbacks` and `read rollbacks`.
- `internal/store/lookup.go`: unchanged.
- `docs/specs/storage.md`, W2-11 section:
  - The `services` bullet's last sentence becomes "…W2-11e adds both with `rollbacks`."
  - A new W2-11e paragraph after the W2-11b ones states these rules:
    - `current_release_id` is `NOT NULL DEFAULT ''`; `previous_release_id` is nullable and `NULL` for none; neither has a foreign key.
    - Every `rollbacks` column is `NOT NULL`; `request_id`, `detail` and `finished_at` default to empty text; `status` is checked against the four statuses.
    - `ServiceByName` returns `ErrNotFound` outside the environment or the org.
    - `SetServiceReleases` copies a `CommandResult`'s `app.current_release_id` and `app.previous_release_id` in one UPDATE, stores an empty previous id as `NULL`, returns `ErrNotFound` for another org, and does not interpret the ids.
    - `StartRollback` checks the service inside the org-bound transaction.
    - `FinishRollback` accepts only `running` and ends a rollback as `succeeded`, `failed` or `rejected`. Otherwise it returns `ErrRollbackState`, or `ErrNotFound` for another org. A non-final status is refused before SQL runs. `finished_at` stays empty until the rollback is final.
    - The in-flight read returns the `running` rollbacks per org and server, oldest first.
    - `rollbacks` is not a lookup table, `LookupTables` is unchanged, and on Postgres it has RLS with `org_isolation`.
  - One more paragraph names `runServiceReleaseStateConformance`, `runRollbacksConformance` and their four tests, `TestPostgresRLSOnRollbacks` and `TestRollbackConstraintsEnforced`, with what each covers.
- `testdata/tests.txt`: regenerated with `make test-list`.

## Tests to write

The tests go in new files only. Existing test files are not edited, because `tests-kept` forbids removing lines from them. They reuse the `store_test` helpers already present: `newSQLite`, `newPGEnv`, `seedTarget`, `seedOrg`, `seedService`, `mustCreateEnvironment`, `mustEnqueue`, `at`, `releaseIDPattern`, `checkNames`, `orgIDsUnder` and `pgError`.

- `internal/store/service_releases_conformance_test.go`:
  - `./internal/store TestServiceReleaseStateConformanceSQLite` runs `runServiceReleaseStateConformance(t, s)` on `newSQLite`.
  - `./internal/store TestServiceReleaseStateConformancePostgres` runs it on `newPGEnv(t, true).openApp(t)`.
  - `runServiceReleaseStateConformance` has these subtests:
    - "new service has no releases": the `CreateService` return, the `Services` entry and `ServiceByName` all have empty `CurrentReleaseID` and `PreviousReleaseID`.
    - "by name": `ServiceByName` equals the `CreateService` result field by field (`reflect.DeepEqual` on `Service`): id, org, project, environment, name, server and created_at.
    - "by name not found": each case returns `errors.Is(err, store.ErrNotFound)`. The cases are an unknown name, the name asked in a second environment of the same org (created with `mustCreateEnvironment`), and the name and environment asked by a second org.
    - "set and read back": `SetServiceReleases(svc, "r2", "r1")` is read back by both `ServiceByName` and `Services` as `r2`/`r1`. Then `SetServiceReleases(svc, "r3", "")` is read back as `r3`/empty.
    - "set refused": an unknown service id and another org's service id each return `ErrNotFound`. The first org's service keeps its ids, checked with `ServiceByName` in the first org.
- `internal/store/rollbacks_conformance_test.go`:
  - `./internal/store TestRollbacksConformanceSQLite` and `./internal/store TestRollbacksConformancePostgres` run `runRollbacksConformance`.
  - `runRollbacksConformance` has these subtests:
    - "start": the id matches `releaseIDPattern`; the status is `ReleaseRunning`; `RequestID` is the given one; `CreatedAt` is `at(0).Format(time.RFC3339)`; `Detail` and `FinishedAt` are empty; `OrgID` and `ServiceID` are set. `InFlightRollbacks` returns the same row.
    - "start refused": an unknown service and another org's service each return `ErrNotFound`. `InFlightRollbacks` of both orgs on their servers is empty.
    - "finish": a table over `succeeded`, `failed` and `rejected`, each with a fresh running rollback. The returned row has the status, the `detail` and `FinishedAt == at(5)`, and keeps its other fields. A second `FinishRollback` returns `ErrRollbackState`.
    - "finish non-final status": a table over `queued`, `running` and `superseded`. `checkNames(err, "succeeded", "failed", "rejected")` is called, the error is not `ErrRollbackState`, and the rollback is still in `InFlightRollbacks`.
    - "in flight": the target has two services on server A and one on a second server B (`AddServer` in the same org). Rollbacks are started at `at(3)`, `at(1)` and `at(2)` on A's services, one on B, and one on A that is then finished. `InFlightRollbacks(A)` returns the three running ids in created order; B's and the finished one are left out.
    - "second org": org 1 has a running rollback and a queued release (`mustEnqueue`) on its server. Org 2's `FinishRollback` on org 1's rollback returns `ErrNotFound`, and the rollback stays running in org 1. Org 2's `InFlightRollbacks` and `InFlightReleases` with org 1's server id both return no rows.
  - `./internal/store TestPostgresRLSOnRollbacks` follows the shape of `TestPostgresRLSOnReleases`:
    - Two targets, each with a started rollback.
    - With `app.org_id` unset, empty or naming a nonexistent org, `orgIDsUnder(..., "rollbacks")` returns no rows. Under A it returns only A's rows, and at least one.
    - An `INSERT INTO rollbacks (id, org_id, service_id, status, created_at)` of B's row under `app.org_id` A fails with SQLSTATE `42501`. The same insert for A's own row succeeds as the control.
    - `pg_tables.rowsecurity` is true for `rollbacks`.
- `internal/store/rollbacks_test.go`: `./internal/store TestRollbackConstraintsEnforced`. It is table-driven on `newSQLite` with raw SQL.
  - Rollback status cases: `done`, `queued` and `superseded` are refused with `CHECK constraint failed`, and `running`, `succeeded`, `failed` and `rejected` are accepted.
  - A services case: a raw `INSERT INTO services (id, org_id, project_id, environment_id, name, server_id, created_at)` that omits the release columns, using the `seedTarget` ids. Reading it back with `SELECT current_release_id, previous_release_id` returns `""` and a `sql.NullString` with `Valid == false`.

## Edge cases

- **Another org's service.** `StartRollback`, `SetServiceReleases` and `ServiceByName` return `ErrNotFound`, covered by "start refused", "set refused" and "by name not found". `StartRollback` uses an explicit `SELECT … AND org_id` rather than the foreign key alone, because Postgres referential checks bypass RLS.
- **Another org's rollback.** `FinishRollback` returns `ErrNotFound`, not `ErrRollbackState`. The status read is org-predicated and RLS-bound, so it finds nothing and reveals nothing. Covered by "second org".
- **Finish in the wrong status.** A second finish returns `ErrRollbackState` with no change. Covered by "finish".
- **Non-final status.** It is refused before SQL. Covered by "finish non-final status".
- **Empty previous id.** It is stored as `NULL` and read back as empty. Covered by "set and read back", with `NULL` checked at the SQL level by `TestRollbackConstraintsEnforced`.
- **Same ids written twice.** `RowsAffected` counts matched rows on both SQLite and Postgres, so an update with the same values is not reported as `ErrNotFound`. The "set and read back" second call writes new values; the first-call semantics hold in either case.
- **Rollbacks on another server, or finished.** They are left out of the in-flight read. Covered by "in flight".
- **Cross-org server id in an in-flight read.** It returns no rows: both tables carry an `org_id` predicate, and RLS applies on Postgres. Covered by "second org".
- **Agent-reported text.** `detail` and the release ids are passed only as bind parameters, never concatenated into SQL.

## Risks

- **Schema parity.** `TestSchemasMatch` compares column names and nullability: `previous_release_id` must be nullable on both backends, and every other new column `NOT NULL` on both. The two migration files are written from one template.
- **RLS coverage.** `TestLookupTablesMatchSchema` and `TestAppRoleCannotBypassRLS` require RLS on `rollbacks` and ownership by the owner role. The pg migration enables RLS, `pg.Migrate` runs as the owner, and `LookupTables` is untouched.
- **Migration idempotence.** `ALTER TABLE ADD COLUMN … NOT NULL DEFAULT ''` is valid on SQLite and Postgres. `TestMigrateIdempotent` passes because the file is recorded once.
- **Existing suites.** The projects and releases suites compare `Service` values from `CreateService` and `Services`. Both now carry empty release ids, so equality holds.
- **NULL scans.** Scanning a `NULL` `previous_release_id` into a `string` would fail, which `COALESCE` in `serviceColumns` avoids. Every service read uses `scanService`.
- **Placeholders.** Each `$n` is used once and in order, because `RebindQuestion` relies on it; the org id is passed twice in `InFlightRollbacks`.
- **Lint.** govet with enable-all: the new struct fields are strings only, so fieldalignment passes. There is no shadowed `err` in non-test code: the `selErr`/`insErr` names follow the existing style. No banned words appear in added lines.
- **Postgres in CI.** The Postgres tests skip locally without `MOONGLASS_TEST_POSTGRES` but fail under `CI`, so the SQL is kept to portable forms already used in the package: `RETURNING`, `COALESCE`, `JOIN`.
- **Scope.** Nothing is built that belongs to W2-5: no single-running-rollback index, no release-id validation and no deleted-service filtering.
