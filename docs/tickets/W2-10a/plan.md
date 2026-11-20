---
verdict: ready
reason: ""
---

# Plan for W2-10a

Context: every executor result for `Deploy`, `SwitchRelease`, `Rollback` and `RunPreDeploy` must be stored under the envelope's request id: in the same transaction as the state change it reports, or alone. A request id that already has a row returns its stored result and runs nothing (`docs/specs/agent-state.md` "commands", "Executor API (W2-10a)"; `docs/specs/connection.md` "Each request id runs once"). No contract change: the `commands` table and the executor API are already in the spec. The ticket owns the storage migration, `google.golang.org/protobuf` is already a module in `go.mod` (generated `agentv1` uses it), and the owner exception allows the `agentv1` import. Nothing calls the executor outside `internal/deploy` yet (W2-10b wires it), so no other package changes.

## Files to change

- `internal/agent/state/migrations/0002_commands.sql` (new): `CREATE TABLE commands (request_id TEXT NOT NULL PRIMARY KEY, type TEXT NOT NULL, app_id TEXT NOT NULL, result BLOB NOT NULL, received_at TEXT NOT NULL, finished_at TEXT NOT NULL);`. No `CHECK` on `type`. `0001_releases.sql` is not touched.

- `internal/agent/state/state.go`:
  - Imports `google.golang.org/protobuf/proto` and `agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"`.
  - Exported constants `CommandDeploy = "deploy"`, `CommandSwitchRelease = "switch_release"`, `CommandRollback = "rollback"`, `CommandRunPreDeploy = "run_pre_deploy"`.
  - `type Command struct { Received time.Time; Result *agentv1.CommandResult; RequestID, Type, AppID string }`, with fields ordered the way `fieldalignment` wants (check with `make lint`).
  - `insertCommand(ctx, tx *sql.Tx, cmd Command, finished time.Time) error`: refuses an empty `RequestID` or a nil `Result` with an error that says what to pass; `proto.Marshal(cmd.Result)`; `INSERT INTO commands (...) VALUES (?, ?, ?, ?, ?, ?)` with `formatTime(cmd.Received)` and `formatTime(finished)`. Errors are wrapped as `command %s: write commands row: %w`. A plain `INSERT`, never `OR REPLACE`, so an existing request id fails on the primary key and its row is never changed. This is the only place that encodes `commands.result`.
  - The six writes take `cmd Command` as their second argument and call `insertCommand` inside their transaction, just before `tx.Commit`, so any earlier refusal or a primary-key failure rolls back everything:
    - `RecordCandidate(ctx, cmd Command, b Binding, r Record, state release.State, now time.Time) error`
    - `RecordPreDeploy(ctx, cmd Command, appID, releaseID string, next release.State, reversible bool, detail string, now time.Time) error` (new `now`, used only for `finished_at`)
    - `RecordSwitch(ctx, cmd Command, appID, releaseID string, next release.State, now time.Time) error`
    - `RecordRollback(ctx, cmd Command, appID string, next release.State, now time.Time) error`
    - `RecordAbort(ctx, cmd Command, appID, releaseID string, next release.State, detail string, now time.Time) error` (new `now`)
    - `RecordFailed(ctx, cmd Command, r Record, detail string, now time.Time) error`
    `finished_at` is the `now` each write is given. The checks they already make before the transaction write nothing, as before.
  - `RecordCommand(ctx, cmd Command, finished time.Time) error`: begin, `insertCommand`, commit. Errors are wrapped as `record command %s: %w`.
  - `CommandResult(ctx, requestID string) (*agentv1.CommandResult, bool, error)`: `SELECT result FROM commands WHERE request_id = ?`. `sql.ErrNoRows` returns `nil, false, nil`. Otherwise `proto.Unmarshal` into a new `CommandResult`. Errors name the request id and the database path.
  - The package doc comment says "release and command records".

- `internal/agent/state/stops.go`: `retentionCommandAge = 7 * 24 * time.Hour`. In `Retain`, inside the same transaction and after `deleteOldFailures`: `DELETE FROM commands WHERE finished_at < ?` with `formatTime(now.Add(-retentionCommandAge))`. The text comparison is correct because every timestamp is written by `formatTime` (second precision, `Z`). The image list and the rest of `Retain` are unchanged. The doc comment mentions command rows.

- `internal/deploy/deploy.go`:
  - `once(ctx, requestID, typ, appID string, run func(state.Command) (*agentv1.CommandResult, bool)) *agentv1.CommandResult` is the only entry point of all four methods:
    1. `received := x.now()`.
    2. Empty `requestID`: return `result(REJECTED, "the command has no request id; send it in an envelope with its request id", release.State{})`. No read, no write, no Engine call.
    3. `x.db.CommandResult`: when `found`, return the stored result unchanged. When the read fails, the result is `FAILED` with `read command <id>: <err>` and goes to step 5. The `INSERT` cannot overwrite a row, so writing it is safe.
    4. `res, written := run(cmd)`, where `cmd` holds the request id, type, app id and `received`.
    5. When `written` is false: `recordAlone(ctx, cmd, res)`.
  - `recordAlone(ctx, cmd state.Command, res *agentv1.CommandResult) *agentv1.CommandResult`: sets `cmd.Result = res` and calls `x.db.RecordCommand(ctx, cmd, x.now())`. On error it sets `res.Detail = truncate(res.Detail + "; recording the command also failed: " + err.Error())`.
  - `Deploy(ctx, requestID string, d *agentv1.Deploy) *agentv1.CommandResult` returns `x.once(ctx, requestID, state.CommandDeploy, d.GetAppId(), func(cmd) { return x.deploy(ctx, cmd, d) })`. The current body becomes `deploy(ctx, cmd state.Command, d) (*agentv1.CommandResult, bool)`. Early returns (a failed read, identity mismatch, an illegal transition) return `(res, false)`. On success it builds the SUCCEEDED result with `ImageId` and `RepoDigest` first, sets `cmd.Result = res`, then calls `RecordCandidate(ctx, cmd, ...)` and returns `(res, true)`. A failed `RecordCandidate` keeps the current container removal and calls `fail`.
  - `fail(ctx, cmd, rec, st, cause) (*agentv1.CommandResult, bool)` builds `res := result(outcome(cause), detail, st)` and sets `cmd.Result = res`. When `RecordFailed(ctx, cmd, rec, detail, x.now())` succeeds it returns `(res, true)`. When it fails it returns `(result(outcome(cause), detail+"; recording the failure also failed: "+err, st), false)`, which is the same text as today, and that result is recorded alone.

- `internal/deploy/switch.go`: `SwitchRelease(ctx, requestID string, s *agentv1.SwitchRelease)` goes through `once` with `CommandSwitchRelease`, and the body moves to `switchRelease(ctx, cmd, s) (*agentv1.CommandResult, bool)`. `lockApp` stays first in the inner body. On success it builds `res := result(SUCCEEDED, "", next)` and `cmd.Result = res` before `PushRouted`. The commit closure calls `RecordSwitch(ctx, cmd, appID, releaseID, next, x.now())`. A `PushRouted` error means the commit did not succeed (`proxy.go:92`), so it returns `(result(outcome(err), err.Error(), st), false)`. `abortSwitch` takes `cmd`, builds `res := result(FAILED, detail, aborted)` and sets `cmd.Result = res`. When `RecordAbort(ctx, cmd, ..., truncate(detail), x.now())` succeeds it returns `(res, true)`; when it fails, the existing "recording the abort also failed" result with `false`. The early `st.Abort` failure returns `false`.

- `internal/deploy/rollback.go`: `Rollback(ctx, requestID string, r *agentv1.Rollback)` goes through `once` with `CommandRollback`, and the body moves to `rollback(ctx, cmd, r)`. Every early return is `false`. Success follows the same pattern as the switch, with `RecordRollback(ctx, cmd, appID, next, x.now())` in the commit closure.

- `internal/deploy/predeploy.go`: `RunPreDeploy(ctx, requestID string, r *agentv1.RunPreDeploy)` goes through `once` with `CommandRunPreDeploy`, and the body moves to `runPreDeploy(ctx, cmd, r)`. On success it builds `res := result(SUCCEEDED, "", ready)` and `cmd.Result = res`, then calls `RecordPreDeploy(ctx, cmd, ..., "", x.now())`. When that fails, `abortPreDeploy(ctx, cmd, ...)` follows the same pattern as `abortSwitch` (`RecordPreDeploy` with the aborted result and `x.now()`). Rejections before any write return `false`.

- Existing tests are updated only for the new signatures, through the owner-allowed files in `tools/pipeline/verify.go` `ownerTestEdits["W2-10a"]`. In the state tests, a helper `testCommand(t, appID string) Command` (unique request id, a SUCCEEDED result) is passed to each `Record*`. In the deploy tests, each call passes a fresh request id from a helper `requestID(t)` built on `randHex`. In `state_test.go` the only assertion changes are the owner's two: the `forbidden` list lets exactly `internal/protocol/agentv1` through (still forbidding `internal/protocol` itself and every other path under it, `internal/keys` and `internal/deploy`), and `wantTables`/migrations become `[apps commands releases schema_migrations]` and `0001_releases.sql`, `0002_commands.sql`.

- `docs/specs/agent-state.md`: no change expected. The behaviour above is what it already states.

## Tests to write

New files: `internal/agent/state/commands_test.go` and `internal/deploy/commands_test.go`.

- `./internal/agent/state TestCommandsResultIsNotNull`: after `Open`, a raw `INSERT INTO commands` with `result` NULL fails with an error containing `NOT NULL`. The `schema_migrations` versions in order are `0001_releases.sql`, `0002_commands.sql`.
- `./internal/agent/state TestStateChangeWritesCommandRowInOneTransaction`: a table over `RecordCandidate`, `RecordPreDeploy`, `RecordSwitch`, `RecordRollback`, `RecordAbort` and `RecordFailed`. Each case has a setup that brings a fresh DB to the state the write needs, a successful call, and refused calls: a state that does not follow; for candidate and failed, a release already recorded; and for every write, a request id that already has a row (written first with `RecordCommand`). On success, the release or app change is read back, and `CommandResult` is `proto.Equal` to `cmd.Result` with `found` true. On refusal, a snapshot of every `apps`, `releases` and `commands` row (raw columns, `result` as bytes) taken before the call equals the one taken after it. Failure messages show both snapshots.
- `./internal/agent/state TestCommandResultReturnsStoredResult`: `RecordCommand` with a result that has an outcome, a detail, an `App`, an `ImageId` and a `RepoDigest`, and with `Received` and `finished` in a non-UTC zone. `CommandResult` returns a `proto.Equal` result with `found` true. An unknown id returns `nil, false, nil`. The raw `received_at` and `finished_at` match `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$` and equal the UTC times.
- `./internal/agent/state TestRetainPrunesCommandsAfterSevenDays`: two temp DBs get the same release fixture, including retired rows past retention and a failed row older than 7 days. Only the first also gets command rows finished at `now-7d-1s` (deleted) and `now-7d+1s` (kept). After `Retain(now)` on both, the first holds only the kept command, and the release rows and returned images of the two DBs are equal.
- `./internal/deploy TestEveryCommandResultIsRecorded`: against a real Engine, a table of: `Deploy` without pre-deploy; `Deploy` with pre-deploy; `RunPreDeploy` success; `RunPreDeploy` with a step that exits non-zero; `SwitchRelease` success; `SwitchRelease` with a failed health gate (short `healthLimit`, as in `switch_test.go`); `Rollback` success. For each: `CommandResult(requestID)` is `proto.Equal` to the returned result, and the raw `type` and `app_id` match. For every SUCCEEDED case, `appState` of `storedApp` is `proto.Equal` to `result.App`, which shows the row and the state change landed together.
- `./internal/deploy TestCommandWithNothingElseWrittenIsRecordedAlone`: a table with one rejection per method, each before any write. `Deploy` with an identity mismatch (a second binding on a deployed app). `SwitchRelease` with an illegal transition (a candidate still pending its pre-deploy). `Rollback` on an unknown app. `RunPreDeploy` with a policy rejection: the test sets the stored `apps.project` to a value that is not a path element through the raw SQLite file, so `preDeploySpec` fails `policy.Check` with REJECTED. The snapshot is taken after that edit. Each result is stored under its request id, the `apps` and `releases` rows are unchanged, and only the one new `commands` row appears.
- `./internal/deploy TestStoredRequestIDIsNotRunAgain`: a successful `Deploy` with id R. A table re-sends R with the same `Deploy`, with a `Deploy` of another release, and as a `SwitchRelease`. Each returns a result `proto.Equal` to the first one. `projectContainerIDs` is unchanged, and a raw snapshot of every `apps`, `releases` and `commands` row (bytes) is equal before and after.
- `./internal/deploy TestEmptyRequestIDRunsNothing`: on a fresh executor, each of the four methods called with `""` (the message names a deployable app) returns REJECTED with a detail containing `request id`. The project has no containers, and `SELECT COUNT(*)` is 0 for `apps`, `releases` and `commands`.
- Then `make test-list`.

## Edge cases

- Same request id arriving inside a `Record*` transaction (a race W2-10b closes): the primary key fails the transaction, so nothing is committed. Deploy removes its container, the `RecordFailed` path also fails on the release or the primary key, the lone `RecordCommand` fails on the primary key, and the detail names each failure. The first stored row stays as it was. Covered by the "request id already has a row" refusal in `TestStateChangeWritesCommandRowInOneTransaction`.
- A failed lone write: the detail gets `; recording the command also failed: …`, truncated to 4096 bytes. A test that injects a failing `RecordCommand` would need a seam, which is not added. The code path is reviewed, and the `insertCommand` error branches are covered by the state tests.
- A failed `CommandResult` read: FAILED is recorded alone, and `INSERT` never overwrites, so a row that is present but undecodable is never replaced.
- A different message type under a stored id returns the stored result (`TestStoredRequestIDIsNotRunAgain`).
- An empty id writes nothing anywhere (`TestEmptyRequestIDRunsNothing`). `insertCommand` also refuses an empty id or a nil result, so the state API cannot store an unkeyed row.
- The retention boundary is exactly 7 days, kept by the strict `<` (`TestRetainPrunesCommandsAfterSevenDays`, ±1 s).
- Secrets: the only text stored is the returned `CommandResult`, whose detail never carries an environment value (ADR-0004 decision 12). Nothing new is logged.

## Risks

- Diff size (cap 330 counted lines; tests and docs are not counted). The estimate is about 110 lines in `state.go`, 8 in the migration, 6 in `stops.go` and about 100 in `internal/deploy`. Keep the inner bodies as they are and change only the returns and the record calls.
- `tests-kept`: lines may change only in the files listed for W2-10a in `ownerTestEdits`. Never weaken an assertion. New tests go in new files.
- `fieldalignment`/lint on `Command`, and the long new signatures: run `make fmt lint`.
- An empty marshalled result would bind as NULL. It cannot happen, because every executor result has a non-zero outcome and an `App`, so the encoding is never empty. Do not add a special case.
- The `RunPreDeploy` policy-rejection fixture edits the binding through raw SQL, so open the file the way `storedRow` does and close it before calling the executor.
- Docker tests skip without an Engine and fail under `CI`. Use per-run labelled projects as the existing helpers do.
