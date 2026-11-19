---
verdict: ready
reason: ""
---

# Plan for W2-11b

The control plane needs a `releases` table and store methods for the deploy queue: one `running` and one `queued` release per service, with a supersede on enqueue. Both are already specified in `docs/specs/storage.md` (W2-11, owner decisions D6, D18) and `docs/specs/registry.md` "One deploy per service". The work needs no contract change beyond the spec, no ADR and no new module (`encoding/json` is stdlib). About 360 lines count against the 400-line cap, because tests and docs are not counted: the two migrations (~75), `store.go` (~75) and `releases.go` (~210).

## Files to change

- `internal/store/migrations/sqlite/0004_releases.sql` and `internal/store/migrations/pg/0004_releases.sql` hold the same `CREATE TABLE releases`, with the columns in the same order:
  ```sql
  CREATE TABLE releases (
  	id TEXT NOT NULL PRIMARY KEY,
  	org_id TEXT NOT NULL REFERENCES orgs (id),
  	service_id TEXT NOT NULL REFERENCES services (id),
  	image_ref TEXT NOT NULL DEFAULT '',
  	git_repository TEXT NOT NULL DEFAULT '',
  	git_commit TEXT NOT NULL DEFAULT '',
  	port INTEGER NOT NULL,
  	health_path TEXT NOT NULL DEFAULT '',
  	domains TEXT NOT NULL,
  	pre_deploy TEXT NOT NULL DEFAULT '',
  	pre_deploy_reversible BOOLEAN NOT NULL,
  	status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'rejected', 'superseded')),
  	step TEXT NOT NULL DEFAULT '' CHECK (step IN ('', 'build', 'deploy', 'pre_deploy', 'switch')),
  	request_id TEXT NOT NULL DEFAULT '',
  	image_id TEXT NOT NULL DEFAULT '',
  	repo_digest TEXT NOT NULL DEFAULT '',
  	detail TEXT NOT NULL DEFAULT '',
  	created_at TEXT NOT NULL,
  	finished_at TEXT NOT NULL DEFAULT ''
  );
  CREATE UNIQUE INDEX releases_one_queued ON releases (org_id, service_id) WHERE status = 'queued';
  CREATE UNIQUE INDEX releases_one_running ON releases (org_id, service_id) WHERE status = 'running';
  ```
  - The Postgres file also gets `ALTER TABLE releases ENABLE ROW LEVEL SECURITY;` and `CREATE POLICY org_isolation ON releases USING (...) WITH CHECK (...)`, with the expression copied from `0003_projects.sql`.
  - Neither file has grants, and `LookupTables` is unchanged.
  - `pre_deploy_reversible` is a Go `bool` on both backends. SQLite `BOOLEAN` has numeric affinity: modernc binds a Go `bool` as 0 or 1, and `database/sql` scans 0 or 1 into `*bool`.

- `internal/store/store.go`:
  - `type ReleaseStatus string` with `ReleaseQueued`, `ReleaseRunning`, `ReleaseSucceeded`, `ReleaseFailed`, `ReleaseRejected` and `ReleaseSuperseded`.
  - `type ReleaseStep string` with `StepBuild` ("build"), `StepDeploy` ("deploy"), `StepPreDeploy` ("pre_deploy") and `StepSwitch` ("switch"). It has `func (s ReleaseStep) Valid() bool`, which accepts the four steps and refuses the empty step, like `Role.Valid`.
  - `ErrReleaseState = errors.New("store: release is not in a status that allows this")`, added to the error block.
  - `type Release struct { OrgID, ID, ServiceID, ImageRef, GitRepository, GitCommit string; Port int; HealthPath string; Domains, PreDeploy []string; PreDeployReversible bool; Status ReleaseStatus; Step ReleaseStep; RequestID, ImageID, RepoDigest, Detail, CreatedAt, FinishedAt string }`, in the ticket's field order.
  - Eight `Store` methods with the ticket's signatures. Each doc comment names the method's errors.

- `internal/store/releases.go` (new file):
  - **`const enqueueRetries = 10`** bounds `EnqueueRelease`'s retries.
    - A call can lose only to another enqueue that committed during its attempt. With N concurrent callers, each loses at most N−1 times.
    - 10 covers the test's 8 callers with room to spare.
  - **`const releaseColumns`** is `"r.id, r.org_id, r.service_id, r.image_ref, r.git_repository, r.git_commit, r.port, r.health_path, r.domains, r.pre_deploy, r.pre_deploy_reversible, r.status, r.step, r.request_id, r.image_id, r.repo_digest, r.detail, r.created_at, r.finished_at"`. Every read uses `FROM releases r`, so the same list also works in the join.
  - **`scanRelease(row interface{ Scan(...any) error }) (Release, error)`**:
    - It decodes `domains` with `json.Unmarshal`.
    - It decodes `pre_deploy` only when the text is not empty; empty text becomes a nil argv.
    - It converts `status` and `step` to their types and wraps every error, for example "decode release %s domains: %w".
  - **`encodeArgs(domains, preDeploy []string) (string, string, error)`**:
    - `domains` goes through `json.Marshal`, and nil is written as `[]`.
    - `preDeploy` is empty text when its length is 0, otherwise `json.Marshal`.
  - **`EnqueueRelease`**:
    - It encodes the arguments, calls `NewID()` once, and builds the returned `Release` from the input's source fields only. `OrgID` is orgID, `Status` is queued and `CreatedAt` is `formatTime(now)`. `Step`, `RequestID`, `ImageID`, `RepoDigest`, `Detail` and `FinishedAt` stay empty.
    - It calls `enqueueReleaseOnce` up to `enqueueRetries` times and stops at the first result that is not `ErrDuplicate`.
    - When every attempt fails it returns `fmt.Errorf("enqueue release: the queued slot of service %s kept changing after %d attempts, retry the deploy: %w", ...)`.
  - **`enqueueReleaseOnce`** runs in `inOrg`:
    1. `SELECT id FROM services WHERE id = $1 AND org_id = $2`. No row returns `ErrNotFound` ("enqueue release: service %s is not in org %s: %w").
    2. `UPDATE releases SET status = 'superseded', finished_at = $1 WHERE org_id = $2 AND service_id = $3 AND status = 'queued'`.
    3. `INSERT INTO releases` with all 19 columns (`$1` to `$19`). The error goes through `s.dupOr(err, "insert release")`, so losing the race for `releases_one_queued` comes back as `ErrDuplicate` and is retried in a fresh transaction. Postgres aborts the failed transaction.

    How a race resolves:
    - On Postgres (READ COMMITTED), a caller blocked on the same queued row re-checks `status = 'queued'` after the winner commits and skips the row. Its insert then collides with the winner's new row, and its retry supersedes that row.
    - SQLite serialises writers on its single connection.
  - **`updateRelease(ctx, orgID, releaseID, what, query string, args ...any) error`** runs one `ExecContext(s.q(query), args...)` in `inOrg`:
    - A duplicate can only come from `StartRelease` hitting `releases_one_running`. It returns `fmt.Errorf("%s: release %s: another release of its service is running: %w", what, releaseID, ErrReleaseState)`.
    - Any other error is wrapped with context.
    - When `RowsAffected()` is 0, it runs `SELECT status FROM releases WHERE id = $1 AND org_id = $2`. No row returns `fmt.Errorf("%s: release %s is not in org %s: %w", …, ErrNotFound)`. A row returns `fmt.Errorf("%s: release %s is %s: %w", …, ErrReleaseState)`.
    - Each change is a single statement, so neither error case writes anything.
  - **`StartRelease`**: `UPDATE releases SET status = 'running' WHERE id = $1 AND org_id = $2 AND status = 'queued'`.
  - **`SetReleaseStep`**:
    - An invalid step returns `fmt.Errorf("set release step: step %q is not one of build, deploy, pre_deploy, switch", step)` before any SQL runs.
    - Otherwise: `UPDATE releases SET step = $1, request_id = $2 WHERE id = $3 AND org_id = $4 AND status = 'running'`.
  - **`SetReleaseImage`**: `UPDATE releases SET image_id = $1, repo_digest = $2 WHERE id = $3 AND org_id = $4 AND status = 'running'`.
  - **`FinishRelease`**:
    - A status other than `succeeded`, `failed` or `rejected` returns `fmt.Errorf("finish release: status %q is not one of succeeded, failed, rejected", status)` before any SQL runs.
    - Otherwise: `UPDATE releases SET status = $1, detail = $2, finished_at = $3 WHERE id = $4 AND org_id = $5 AND status IN ('queued', 'running')`.
    - `step` keeps the last step that ran.
  - **`Release`**: `SELECT releaseColumns FROM releases r WHERE r.id = $1 AND r.org_id = $2`, with `sql.ErrNoRows` mapped to `ErrNotFound`.
  - **`Releases`**: `… FROM releases r WHERE r.org_id = $1 AND r.service_id = $2 ORDER BY r.created_at DESC, r.id DESC LIMIT 50`. The cap of 50 is written once, in this query.
  - **`InFlightReleases`**:
    - `… FROM releases r JOIN services v ON v.id = r.service_id WHERE r.org_id = $1 AND v.org_id = $2 AND v.server_id = $3 AND r.status IN ('queued', 'running') ORDER BY r.created_at, r.id`.
    - The arguments are `orgID, orgID, serverID`, because `RebindQuestion` needs each placeholder used once and in order.
  - **`queryReleases(ctx, orgID, query string, args ...any) ([]Release, error)`** runs a list query in `inOrg` and scans each row with `scanRelease`. `Releases` and `InFlightReleases` both use it.

- `docs/specs/storage.md`: in "Projects, environments, services and releases (W2-11)", replace "The release cases come with W2-11b." with a W2-11b paragraph covering:
  - every `releases` column is `NOT NULL` and text columns default to empty;
  - `domains` is a JSON array (`[]` for none) and `pre_deploy` is a JSON argv or empty;
  - the `CHECK`s, and the partial unique indexes `releases_one_queued` and `releases_one_running` as the database's guarantee;
  - enqueue checks the service in the org-bound transaction, then supersedes and inserts in one transaction, retrying a lost race up to 10 times;
  - which statuses each update accepts; `ErrReleaseState` for any other status and `ErrNotFound` for another org's release, with nothing changed in either case;
  - `finished_at` stays empty until the release reaches a final status;
  - the history order (`created_at`, then `id`, descending) and its cap of 50;
  - the in-flight read is per org and server, oldest first;
  - the four new tests by name and what each covers.

  The `services` bullet stays as it is, because the ticket gives that wording to W2-11e.

- `testdata/tests.txt`: regenerate it with `make test-list`.

## Tests to write

Helpers in `internal/store/releases_conformance_test.go` (package `store_test`):
- `seedService`: builds a service on the existing `seedTarget` and `mustCreate*` helpers.
- `sampleRelease(serviceID)`: sets every input field (`ImageRef`, `GitRepository`, `GitCommit`, `Port: 8080`, `HealthPath`, two `Domains`, `PreDeploy: {"migrate","--up"}`, `PreDeployReversible: true`). It also puts junk in every field the store should ignore (`Status: "succeeded"`, `Step`, `RequestID`, `ImageID`, `RepoDigest`, `Detail`, `FinishedAt`).
- `at(i)`: a fixed base time plus i seconds.
- `mustEnqueue` and `statuses`.
- String slices are compared with `slices.Equal`, so nil and empty count as equal. Failure messages print the values with `%+v`.

Tests:
- `./internal/store TestReleasesConformanceSQLite`: `s, _ := newSQLite(t); runReleasesConformance(t, s)`.
- `./internal/store TestReleasesConformancePostgres`: `runReleasesConformance(t, newPGEnv(t, true).openApp(t))`.
- `runReleasesConformance` has these subtests, each on fresh orgs:
  - **enqueue and read**
    - The id matches `^[0-9a-f]{32}$`, the status is `queued`, `CreatedAt` is `at(0)` formatted and `FinishedAt` is empty.
    - The ignored fields come back empty, and every input field reads back unchanged from both `Release` and `Releases`.
    - An enqueue with nil `PreDeploy` and nil `Domains` reads back with no argv, no domains and `PreDeployReversible` false.
  - **enqueue refused**
    - An unknown service id returns `ErrNotFound`, and so does another org's service.
    - `Releases` is empty for both orgs.
  - **queue**
    - R1 is enqueued and started, so it is `running`.
    - R2 queues beside it.
    - R3, enqueued at `at(3)`, marks R2 `superseded` with `FinishedAt` equal to `at(3)`, leaving exactly one `queued` release.
    - `StartRelease(R3)` while R1 runs returns `ErrReleaseState`, and R3 is still queued.
    - `StartRelease(R2)` returns `ErrReleaseState`.
  - **steps and image**
    - On the running release, `SetReleaseStep(StepDeploy, "c"+32 hex)` and `SetReleaseImage` both read back.
    - On a queued release both return `ErrReleaseState`, and so on a finished one.
    - Step `"bogus"` returns an error that is neither sentinel, and the release is unchanged.
  - **finish**
    - A table over `succeeded`, `failed` and `rejected`: each starts a release and finishes it, and the status, `Detail` and `FinishedAt` read back.
    - After the finish, the queued release on that service starts.
    - Finishing a finished release returns `ErrReleaseState`.
    - A table over `queued`, `running` and `superseded` as the finish status: each returns an error and leaves the release unchanged.
    - Finishing a queued release as `rejected` is accepted.
  - **history**: 51 enqueues at `at(0)` to `at(50)` leave `Releases` with 50 entries, which are enqueues 50 down to 1.
  - **in flight**
    - Setup: servers S1 and S2; services A and B on S1, C on S2. A has a running and a queued release, B has a finished one, C has a queued one.
    - `InFlightReleases(S1)` returns A's running release then A's queued one, and nothing from B or C.
  - **concurrent enqueue**
    - 8 goroutines run, with `sync.WaitGroup` and their errors collected in a `[8]error`.
    - Every call returns nil, and the service ends with 8 rows: 1 `queued` and 7 `superseded`.
  - **second org**
    - Org B gets `ErrNotFound` from `Release`, `StartRelease`, `SetReleaseStep`, `SetReleaseImage` and `FinishRelease` on org A's queued and running releases.
    - A's releases are `reflect.DeepEqual` before and after.
    - `Releases(B, A's service)` and `InFlightReleases(B, A's server)` are empty.
- `./internal/store TestPostgresRLSOnReleases` follows the same pattern as `TestPostgresRLSOnProjectTables`:
  - It seeds a release in org A and one in org B through the store.
  - `orgIDsUnder` on `releases` returns nothing with `app.org_id` unset, empty or naming a nonexistent org.
  - Under A it returns only A's rows, and at least one.
  - It inserts org B's row with B's real service id under A: `INSERT INTO releases (id, org_id, service_id, port, domains, pre_deploy_reversible, status, created_at)`. The insert fails with `pgconn.PgError` code `42501`.
  - A control row for A with status `failed` (so it cannot clash with a partial index) succeeds.
  - `pg_tables.rowsecurity` is true for `releases`.
- `./internal/store TestReleaseConstraintsEnforced` goes in `internal/store/releases_test.go`:
  - It uses SQLite through `newSQLite`: the store seeds the org and services, and the test runs raw SQL on the raw handle.
  - Each case uses a fresh service. Its setup rows must insert cleanly, then one insert is checked.
  - Refused, with the message checked by substring:
    - an unknown status (`done`): `CHECK constraint failed`;
    - an unknown step (`test`): `CHECK constraint failed`;
    - a second `queued` row for the service: `UNIQUE constraint failed`;
    - a second `running` row for the service: `UNIQUE constraint failed`.
  - Accepted:
    - several finished rows on one service;
    - a `queued` row on one service with a `running` row on another;
    - a `queued` and a `running` row on the same service.
- `TestSchemasMatch`, `TestLookupTablesMatchSchema` and `TestMigrateIdempotent` pass without changes.

## Edge cases

| Case | Handling | Test |
|---|---|---|
| Unknown service, or another org's service, on enqueue | An explicit `SELECT … WHERE id = $1 AND org_id = $2` in the org-bound transaction returns `ErrNotFound` before anything is written. Postgres foreign-key checks do not apply RLS. | enqueue refused |
| Two enqueues race for the queued slot | The partial unique index raises `ErrDuplicate`, which is retried in a fresh transaction, at most 10 times, ending in an error that tells the caller to retry. | concurrent enqueue |
| `StartRelease` while another release of the service runs | The `releases_one_running` violation is mapped to `ErrReleaseState`. | queue |
| An update aimed at another org's release | Zero rows change, and the status lookup in the same org-bound transaction finds nothing, so `ErrNotFound`. | second org |
| An update in the wrong status | Zero rows change, so `ErrReleaseState`. | queue, steps and image, finish |
| An invalid step, or a non-final finish status | Refused before any SQL. The error names the allowed values. | steps and image, finish |
| A concurrent change against a release in a final status | Each change is one `UPDATE … AND status IN (…)`, so it cannot move a final release. | (statement shape) |
| Empty `domains` and `pre_deploy` | Written as `[]` and empty text; read back as none. | enqueue and read |
| Status, step, ids, detail and times supplied by the caller | Ignored on enqueue. | enqueue and read |
| Releases with the same `created_at` second | Ties are broken by `id`. | history uses distinct seconds |
| Hostile `detail` or `pre_deploy` text | Passed only through placeholders. The argv is stored as JSON and never interpreted. | — |
| In-flight read crossing orgs | Prevented by the `r.org_id` and `v.org_id` predicates, plus RLS on Postgres. | in flight, second org |

## Risks

- **Line cap.** All updates share `updateRelease` and all list reads share `queryReleases`, with no per-method copies, so the counted lines stay near 360.
- **Retry flakes on Postgres.** 10 attempts is more than the 7 losses possible with 8 callers. There is no deadlock: a blocked caller waits on one row lock only.
- **Schema comparison.** `TestSchemasMatch` compares column names and nullability only. That makes `BOOLEAN`, `INTEGER` and `DEFAULT ''` safe, and both files list the same columns.
- **SQLite booleans.** modernc returns 0 or 1 as `int64`, and `database/sql` converts it to `bool`. The enqueue-and-read case covers this on SQLite.
- **Placeholders.** Every `$n` is used once and in order. `InFlightReleases` passes the org id twice for this.
- **Spec wording.** `storage.md` currently says W2-11b adds the `services` release columns. The ticket gives those columns and that wording to W2-11e, so this change leaves them alone.
- **Banned words.** No TODO, FIXME, XXX, HACK, fallback or legacy in code or comments.
- **Finishing steps.** Run `make fmt` and `make test-list`, then `make ci` and `scripts/verify check W2-11b`.

## Verification

- Run `make test-one PKG=./internal/store NAME=TestReleaseConstraintsEnforced` and `NAME=TestReleasesConformanceSQLite`.
- With `MOONGLASS_TEST_POSTGRES` set, run `TestReleasesConformancePostgres`, `TestPostgresRLSOnReleases`, `TestSchemasMatch`, `TestLookupTablesMatchSchema` and `TestMigrateIdempotent`.
- Run `make ci` and `scripts/verify check W2-11b`.
