---
verdict: ready
reason: ""
---

# Plan for W2-13

No contract change: the schema is the one `docs/specs/agent-state.md` already fixes (this ticket implements it, it does not change it), `modernc.org/sqlite v1.62.0` is named in ADR-0010 and its 2026-11-17 amendment names `internal/agent/state`, and no proto, registry operation or `agent.toml` field is touched.

## Files to change

- `internal/agent/state/state.go` (new, `package state`). Imports `database/sql`, `embed`, `encoding/json`, `errors`, `fmt`, `io/fs`, `os`, `path/filepath`, `time`, `_ "modernc.org/sqlite"`, `moonglass.dev/moonglass/internal/release`, `moonglass.dev/moonglass/internal/store`. Nothing from `internal/keys`, `internal/protocol` or `internal/deploy`.
  - `//go:embed migrations/*.sql` into `var migrationFiles embed.FS`; `fs.Sub(migrationFiles, "migrations")` is passed to `store.Migrate`.
  - `type DB struct{ db *sql.DB; path string }`.
  - `type Binding struct{ Project, Environment, Service string }`.
  - `type Record struct { AppID, ReleaseID, ImageRef, ImageID, RepoDigest, HealthPath, ContainerID string; Domains []string; Port uint32; HasPreDeploy bool }` (`port` is `uint32` in the `Deploy` proto; fields ordered for govet fieldalignment).
  - `func Open(ctx context.Context, dataDir string) (*DB, error)`:
    1. `path := filepath.Join(dataDir, "agent.db")`.
    2. `os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)` then `Close`, then `os.Chmod(path, 0o600)`, so the file is 0600 before SQLite opens it and an existing file is brought to 0600. `OpenFile` never creates `dataDir`, so a missing directory fails here. SQLite's unix VFS gives `-wal` and `-shm` the main file's mode, so they are 0600 too (the test checks it).
    3. `sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=cache_size(-2000)")`, the same DSN form as `internal/store/sqlite`. modernc applies `_pragma` to every new connection. `SetMaxOpenConns(1)`.
    4. `db.PingContext`, then `store.Migrate(ctx, db, sub)`. On error close the db.
    - Every error is `fmt.Errorf("open agent state %s: %w", path, err)` or `fmt.Errorf("migrate agent state %s: %w", path, err)`, so it holds the full path.
  - `func (d *DB) Close() error`.
  - `func (d *DB) App(ctx context.Context, appID string) (Binding, release.State, bool, error)`: one `SELECT` on `apps` with three `LEFT JOIN releases` (current, previous, candidate, joined on `app_id` and the id) for each `image_ref`. `sql.ErrNoRows` returns `false` (unknown) and a nil error. Each non-null id becomes `&release.Release{ID: id, Image: imageRef}`. `phase` text is parsed by `parsePhase(string) (release.Phase, error)`; unknown text is an error.
  - `func (d *DB) RecordCandidate(ctx context.Context, b Binding, r Record, s release.State, now time.Time) error`:
    - Before the transaction: `s.Candidate == nil || s.Candidate.ID != r.ReleaseID || s.Candidate.Image != r.ImageRef` returns `fmt.Errorf("record candidate %s of app %s: state candidate is %s; record the state's candidate", …)`.
    - `BeginTx`; `defer tx.Rollback()`.
    - `SELECT project, environment, service FROM apps WHERE app_id = ?`. Found and different: error `app %s is bound to project %q, environment %q, service %q; deploy it with that binding or use a new app id`.
    - `SELECT EXISTS(SELECT 1 FROM releases WHERE app_id = ? AND release_id = ?)`; true is `release %s of app %s is already recorded; use a new release id`. Checked inside the transaction on the one connection, so it cannot race.
    - `insertRelease(ctx, tx, r, "candidate", "", now)`.
    - First deploy: `INSERT INTO apps (...)` with the binding, ids, `s.Phase.String()` and `created_at`. Otherwise `UPDATE apps SET current_release_id = ?, previous_release_id = ?, candidate_release_id = ?, phase = ? WHERE app_id = ?` (binding and `created_at` never touched).
    - `Commit`. Every error wrapped with the app and release id.
  - `func (d *DB) RecordFailed(ctx context.Context, r Record, detail string, now time.Time) error`: in one transaction, the same existence check, then `insertRelease(ctx, tx, r, "failed", detail, now)`. It never reads or writes `apps`. `detail` is stored as given and is never in an error string or a log.
  - `func insertRelease(ctx context.Context, tx *sql.Tx, r Record, status, errText string, now time.Time) error`: one parameterised `INSERT INTO releases`; `domains` is `json.Marshal` of the slice, with nil marshalled as `[]`; `has_pre_deploy` as 0/1; `pre_deploy_reversible`, `switched_at`, `stop_after` left NULL.
  - `func nullID(*release.Release) sql.NullString` and `func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }` (the `internal/store` helper is unexported and outside the allowed paths).
  - Every statement uses `?` parameters; no `fmt.Sprintf` into SQL.
- `internal/agent/state/migrations/0001_releases.sql` (new):
  - `releases`: `app_id TEXT NOT NULL`, `release_id TEXT NOT NULL`, `image_ref TEXT NOT NULL`, `image_id TEXT NOT NULL`, `repo_digest TEXT NOT NULL`, `port INTEGER NOT NULL`, `health_path TEXT NOT NULL`, `domains TEXT NOT NULL`, `has_pre_deploy INTEGER NOT NULL CHECK (has_pre_deploy IN (0, 1))`, `pre_deploy_reversible INTEGER CHECK (pre_deploy_reversible IN (0, 1))`, `status TEXT NOT NULL CHECK (status IN ('candidate','current','previous','retired','failed','aborted'))`, `container_id TEXT NOT NULL`, `error TEXT NOT NULL`, `created_at TEXT NOT NULL`, `switched_at TEXT`, `stop_after TEXT`, `PRIMARY KEY (app_id, release_id)`. No foreign key to `apps`: a failed first deploy has a `releases` row and no `apps` row.
  - `apps`: `app_id TEXT NOT NULL PRIMARY KEY`, `project`, `environment`, `service` `TEXT NOT NULL`, `current_release_id`, `previous_release_id`, `candidate_release_id` `TEXT` (nullable), `phase TEXT NOT NULL CHECK (phase IN ('none','pending','ready'))`, `created_at TEXT NOT NULL`, and `FOREIGN KEY (app_id, current_release_id) REFERENCES releases (app_id, release_id)`, likewise for previous and candidate, so the state only names recorded releases. `releases` is created first.
  - No `commands` table.
- `go.mod`: `modernc.org/sqlite v1.62.0` moves from the indirect block to the direct `require` block (`go mod tidy` does this). No other module change; `go.sum` stays as it is.
- `.golangci.yml`:
  - New rule `sqlite-deps`, `list-mode: lax`, with a comment naming ADR-0010 and the age-deps reason. `files: ["**/cmd/**/*.go", "**/internal/agent/*.go", "**/internal/api/**/*.go", "**/internal/cli/**/*.go", "**/internal/installtest/**/*.go", "**/internal/keys/**/*.go", "**/internal/policy/**/*.go", "**/internal/protocol/**/*.go", "**/internal/proxy/**/*.go", "**/internal/registry/**/*.go", "**/internal/release/**/*.go", "**/internal/server/**/*.go", "**/tools/**/*.go"]`, deny `modernc.org/sqlite`, desc `only internal/store/sqlite and internal/agent/state may import modernc sqlite (ADR-0010)`. `**/internal/agent/*.go` matches only the agent's top-level files, because depguard compiles globs with `/` as the separator, so `*` does not match `internal/agent/state/`.
  - Delete the `modernc.org/sqlite` entry from `policy-deps`; `sqlite-deps` now covers `cmd` and `internal/protocol`, and the old description ("only internal/store") would be wrong. One rule per dependency.
- `docs/specs/agent-state.md`: the introduction becomes "W2-13 creates it with the release tables, W2-3 uses it, and W2-10 adds the command records." "Rules" gains: "A `failed` release row never creates or changes an `apps` row: the binding and the release state stay as they were (`docs/specs/release.md`, "Failure")." The `(W2-3)` headings on `apps` and `releases` become `(W2-13)`.
- `testdata/tests.txt`: `make test-list`.

## Tests to write

All in `internal/agent/state/state_test.go`, `package state` (internal, so tests read `d.db` directly). Helpers: `openTemp(t) (*DB, string)` (opens in `t.TempDir()`, registers `Close` with `t.Cleanup`), `sampleRecord(appID, releaseID string) Record` (every field non-empty, two domains), `snapshot(t, d) []string` (every row of `apps` and `releases`, `SELECT *` ordered by primary key, each value rendered with `%q`/`%v` so NULL and text differ), `columns(t, d, table) []string` (`PRAGMA table_info`).

- `./internal/agent/state TestOpenCreatesFileMode0600`: after `Open`, `agent.db` is `0600` (`Mode().Perm()`); after a `RecordCandidate`, each of `agent.db-wal` and `agent.db-shm` that exists is `0600`, and at least `-wal` exists (WAL mode). Also a pre-existing `agent.db` created with `0644` is `0600` after `Open`.
- `./internal/agent/state TestOpenSetsPragmas`: table of pragma name to expected value (`journal_mode`=`wal`, `synchronous`=`2`, `busy_timeout`=`5000`, `foreign_keys`=`1`, `cache_size`=`-2000`), checked on the first `Open` and again after `Close` and a second `Open`. Failure shows pragma, want and got.
- `./internal/agent/state TestOpenMigratesSpecSchema`: `sqlite_master` tables (excluding `sqlite_%`) are exactly `apps`, `releases`, `schema_migrations`; `columns` of `apps` and `releases` equal the spec lists exactly, in order; `schema_migrations` has `0001_releases.sql`. A record is written, the DB closed and reopened, `Open` succeeds and the `snapshot` is equal.
- `./internal/agent/state TestOpenErrorNamesPath`: table: (a) `dataDir` is `filepath.Join(t.TempDir(), "missing")`; (b) open, close, `UPDATE schema_migrations SET checksum = 'altered'` through a separate `sql.Open("sqlite", …)`, close. Each: `Open` returns an error that contains `filepath.Join(dataDir, "agent.db")`; case (a) also asserts that `dataDir` still does not exist.
- `./internal/agent/state TestRecordCandidateFirstDeployWritesBinding`: table over `hasPreDeploy` false/true (phase `ready`/`pending`, from `release.State{}.Deploy`). `App` first returns unknown. After `RecordCandidate`, the `releases` row read with SQL equals every sent column (`domains` as `["a.example","b.example"]`, status `candidate`, `error` empty, `created_at` = `now` in RFC 3339 `Z`, nullable columns NULL); `App` returns the same binding and a `release.State` `reflect.DeepEqual` to the one recorded.
- `./internal/agent/state TestRecordCandidateKeepsBinding`: record r1 at `t1`, `Switch` it, record r2 at `t2` with state `switched.Deploy(r2)`. The `apps` binding and `created_at` equal `t1`; `App` returns current r1, previous nil, candidate r2; a third step (switch r2, record r3) shows previous r1, current r2, candidate r3.
- `./internal/agent/state TestRecordCandidateRejectsOtherBinding`: table over a different project, environment, service. Each: error contains the stored project, environment and service; `snapshot` before and after is equal.
- `./internal/agent/state TestRecordCandidateIsAtomic`: table: (a) duplicate `(app_id, release_id)` of an existing candidate on a known app, with a valid next state; (b) a state whose candidate is a different release id; (c) a state with no candidate; (d) duplicate on an unknown app whose release row exists from `RecordFailed`. Each returns an error and `snapshot` is unchanged; (d) also asserts no `apps` row appears.
- `./internal/agent/state TestRecordFailedLeavesStateUnchanged`: table over unknown and known app. `RecordFailed` with detail `pull failed: manifest unknown` writes one `failed` row with `error` equal to the detail; the `apps` rows equal the before snapshot (absent for unknown); `App` returns the same `(binding, state, known)` as before. A second `RecordFailed` for the same release is an error and the snapshot is unchanged.

## Edge cases

- Missing data directory: `os.OpenFile` fails, error names the path, directory not created — `TestOpenErrorNamesPath`.
- Altered applied migration: `store.Migrate` checksum error, wrapped with the path — `TestOpenErrorNamesPath`.
- Existing `agent.db` with a looser mode: `Chmod` to 0600 — `TestOpenCreatesFileMode0600`.
- Different binding for a known `app_id` (each of the three names): error naming the stored triple, nothing written — `TestRecordCandidateRejectsOtherBinding`.
- Duplicate release (known app, unknown app with a failed row), wrong or missing candidate: nothing written — `TestRecordCandidateIsAtomic`; duplicate failed row — `TestRecordFailedLeavesStateUnchanged`.
- A state naming a release that has no row (for example a forged previous id): the composite foreign keys fail the `INSERT`/`UPDATE` and the transaction rolls back, on the same rollback path `TestRecordCandidateIsAtomic` covers.
- Hostile text in ids, image refs, domains or detail: only bound parameters, so it is stored verbatim; `sampleRecord` uses a release id containing `'` and `;` to prove it.
- Nil `Domains`: stored as `[]`, never `null`.
- `detail` never appears in an error: `RecordFailed` errors name only app and release ids.

## Risks

- **depguard glob semantics.** If `**/internal/agent/*.go` matched subdirectories, lint would fail on the package itself. depguard v2 compiles with `/` as separator; the worker confirms with `make lint` and, once, by adding a temporary `modernc.org/sqlite` import to `internal/agent/enroll.go` and `internal/server` to see lint fail, then removing it.
- **WAL/shm mode** depends on SQLite copying the main file's mode; if a platform differed, the test fails loudly rather than passing silently. modernc is transpiled SQLite, so the unix VFS behaviour holds on Linux and macOS.
- **Diff size:** non-test lines are about 200 (Go) + 40 (SQL) + 15 (yml) + 5 (spec) + 2 (go.mod), within 450; tests do not count.
- **fieldalignment** (govet enable-all) on `Record` and `DB`: order fields by size; the worker runs `make lint`.
- **Banned words:** none in added code lines.
- **`go.mod` drift:** use `go mod tidy` and check that the only change is the move of the one line; `go.sum` unchanged. `cmd/moonglass` does not import the package yet, so `make build-all` is unchanged.
- **Scope creep:** no `commands` table, no status transitions beyond `candidate`/`failed`, no retention, no wiring into agent start.
