---
verdict: ready
reason: ""
---

# Plan for W2-4b

No contract changes: no proto, no registry operation, no agent.toml field, no migration (the `switched_at`, `stop_after`, `aborted` columns and status already exist in `0001_releases.sql`). No new module. `Abort` is already in `docs/specs/release.md`; only its attribution moves from W2-4 to W2-4b.

## Files to change

- `internal/release/release.go`: `func (s State) Abort(id string) (State, error)`. With no candidate: `illegal("abort %q with no candidate; deploy first", id)`. With another id: `illegal("abort %q, candidate is %q", id, s.Candidate.ID)`. Otherwise it copies `s`, sets `Candidate = nil` and `Phase = PhaseNone`, and leaves `Current` and `Previous` as they were. It is legal in both phases.

- `internal/agent/state/state.go`:
  - Constants `statusCurrent`, `statusPrevious`, `statusRetired`, `previousStopAfter = 10 * time.Minute`, `retiredStopAfter = 30 * time.Second`.
  - `func (d *DB) RecordSwitch(ctx context.Context, appID, releaseID string, next release.State, now time.Time) error`. Inside one transaction:
    - `readState`. An unknown app is an error.
    - Compute `want, err := stored.Switch(releaseID)`. An error, or `!sameState(want, next)`, returns an error and writes nothing.
    - Update the candidate row to `status='current', switched_at=?` with the guard `WHERE app_id=? AND release_id=? AND status='candidate'`.
    - If `stored.Current != nil`: update that row to `status='previous', stop_after=now+10m` with the guard `status='current'`.
    - If `stored.Previous != nil`: update that row to `status='retired', stop_after=now+30s` with the guard `status='previous'`.
    - Each update must affect exactly one row, or it is an error and the transaction rolls back.
    - Update the `apps` row from `next`, then commit.
    - Times use `formatTime` (UTC RFC 3339).
  - `func (d *DB) RecordAbort(ctx context.Context, appID, releaseID string, next release.State, detail string) error`. Inside one transaction:
    - `readState`.
    - Compute `want, err := stored.Abort(releaseID)` with the `sameState` check.
    - Update the row with `status='aborted', error=?, container_id=''` and the guard `status='candidate'`. It must affect one row.
    - Update the `apps` row from `next`, then commit.
    - `detail` is stored as given and never logged.
  - Two small private helpers keep this short:
    - `setAppState(ctx, tx, appID string, st release.State) error`: the existing `UPDATE apps …` statement.
    - `updateOne(ctx, tx, query string, args ...any) error`: exec, then require `RowsAffected() == 1`.
    - `RecordPreDeploy` is switched to both helpers in the same change, so the codebase has one way to do this.

- `internal/deploy/deploy.go`:
  - `Executor` gains the fields `proxy *Proxy` and `healthLimit time.Duration`.
  - `func New(engine *Engine, p *policy.Policy, db *state.DB, proxy *Proxy) *Executor` sets `healthLimit: healthTimeout` (60 s).
  - There are no callers outside `internal/deploy`. The only caller is `newExecutor` in `helpers_test.go`.

- `internal/deploy/switch.go` (new):
  - Constants: `healthTimeout = 60 * time.Second`, `healthInterval = time.Second`, `probeTimeout = 2 * time.Second`, `healthPasses = 3`, `portlessRun = 10 * time.Second`.
  - `func (x *Executor) SwitchRelease(ctx context.Context, s *agentv1.SwitchRelease) *agentv1.CommandResult` follows the ticket's order:
    1. `db.App`. An error is `FAILED`. An unknown app is `REJECTED` with `app %s has no release on this server; deploy it first`.
    2. `next, err := st.Switch(id)`. An error is `REJECTED` with `err.Error()` and `st`, and nothing else is touched.
    3. `db.Release`. An error or a missing row is `FAILED`.
    4. `x.healthGate(ctx, b.Project, rec)`. An error returns `x.abortSwitch(ctx, st, rec, cause)`.
    5. `x.proxy.PushRouted(ctx, state.CurrentRelease{Binding: b, Record: rec}, func(ctx context.Context) error { return x.db.RecordSwitch(ctx, appID, id, next, time.Now()) })`. An error is `FAILED` with `err.Error()` and `st`.
    6. Otherwise `SUCCEEDED` with `next`.
  - `func (x *Executor) abortSwitch(ctx, st release.State, rec state.Record, cause string) *agentv1.CommandResult` mirrors `abortPreDeploy`:
    - `st.Abort`, then `x.engine.remove(ctx, rec.ContainerID)`. A not-found error from `isNotFound` is ignored; any other error is appended to the detail.
    - `db.RecordAbort(…, truncate(detail))`.
    - The answer is `FAILED` with the aborted state. If recording fails, it is `FAILED` with `st`.
  - `func (x *Executor) healthGate(ctx context.Context, project string, rec state.Record) error` runs synchronously and starts no goroutine:
    - `gateCtx, cancel := context.WithTimeout(ctx, x.healthLimit)`.
    - Read `base, err := x.engine.runState(gateCtx, rec.ContainerID)`. If the container is not running, fail at once.
    - If `rec.Port != 0`, find the address with the existing `x.engine.containerIP(gateCtx, rec.ContainerID, "mg-"+project)` and `net.JoinHostPort(ip, port)`.
    - Loop on a `time.NewTicker(healthInterval)`, with the first attempt immediate:
      - Read `runState`. Not running, restarting, or a `RestartCount` different from `base` is an immediate error: `container %s exited (status %s, exit code %d)` or `container %s restarted`.
      - With no port: healthy once `time.Since(start) >= portlessRun`.
      - With a port: `probe(gateCtx, addr, rec.HealthPath)`. Success increments `passes`; failure resets it to 0 and keeps the reason as `last`. Three passes in a row means healthy.
      - When `gateCtx` is done, return `release %s of app %s did not pass its health gate within %s: <last>; the old release keeps serving`.
  - `func probe(ctx context.Context, addr, path string) error`:
    - Each attempt uses `context.WithTimeout(ctx, probeTimeout)`.
    - With an empty `path`: `(&net.Dialer{}).DialContext(ctx, "tcp", addr)`, then close the connection.
    - Otherwise a `GET http://addr+path` through the package-level `healthClient`:
      - `&http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}, CheckRedirect: func(...) error { return http.ErrUseLastResponse }}`.
      - The body is drained with `io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))` and closed. It is never turned into a string.
      - A status of 200 to 399 passes. Anything else is `GET <url> answered status %d`.

- `internal/deploy/engine.go` or `container.go`: `func (e *Engine) runState(ctx, id string) (runState, error)`.
  - `type runState struct{ Running bool; Status string; ExitCode int; RestartCount int }`.
  - It calls `ContainerInspect` under `call(ctx)`. `Running` is `State.Running && !State.Restarting`.

- `internal/deploy/proxy.go`:
  - `Push(ctx)` becomes `p.mu` locking plus `p.push(ctx, nil)`.
  - The new `func (p *Proxy) PushRouted(ctx context.Context, routed state.CurrentRelease, commit func(context.Context) error) error` holds `p.mu`, calls `p.push(ctx, &routed)`, then `commit(ctx)`.
    - If the commit fails, it clears `p.last` (`p.last.Store(nil)`), so `Run`'s next probe pushes again from stored state and the proxy stops routing a release that state does not record. Then it returns the commit error.
    - Holding `mu` across the commit means a `Run` push cannot read the old current release between the routed push and the commit.
  - The private `push(ctx, routed *state.CurrentRelease) error` is today's body. After `CurrentReleases`, when `routed != nil`, it replaces the entry with the same `AppID`, or appends it and re-sorts by `AppID` with `slices.SortFunc`, so app_id order holds. Everything else is unchanged, including the domain-less skip and recording the 2xx document as the last pushed.

- `internal/deploy/testdata/server/main.go`: two new argv modes. The existing modes stay.
  - `health <path> <code>`: listens on `:8080`.
    - `<path>` answers `<code>`, with `Location: /followed` for a 3xx.
    - `/followed` answers 500.
    - Any other path answers 404.
    - Every answer has the body `health-body-must-not-leak`.
  - `die <seconds> <code>`: serves 200 on `:8080`, then exits with `<code>` after `<seconds>`.
  - The usage line names them.

- `docs/specs/release.md`:
  - In the Events table and in Transitions, `Abort` is added by W2-4b.
  - "SwitchRelease" step 3: the pushed document routes the candidate in place of the app's stored current release, through `Proxy.PushRouted`.
  - Step 4: the commit runs inside that push's lock. A failed commit is `FAILED` with the stored state unchanged, and the last pushed document is cleared so `Run` restores the stored routes.
  - "Proxy config", One pusher: mention `PushRouted`.

- `docs/specs/agent-state.md`, `releases` and Rules:
  - A switch sets the new current row `current` with `switched_at` set to now, the old current `previous` with `stop_after` = now + 10 min, and the old previous `retired` with `stop_after` = now + 30 s, all in RFC 3339 UTC.
  - A failed gate sets the candidate row `aborted`, with the detail as `error` and an empty `container_id`.

- Test files (all in allowed paths):
  - `internal/release/release_test.go`.
  - `internal/agent/state/switch_test.go` (new).
  - `internal/deploy/switch_test.go` (new).
  - `internal/deploy/helpers_test.go`: `newExecutor` becomes a wrapper of the new `newExecutorAdmin(t, e) (*Executor, string, *fakeAdmin)`. That function builds a `fakeAdmin` and `NewProxy(e, db, ProxyConfig{Socket: fake.path, Issuer: "internal"})` and passes the proxy to `New`.
  - `internal/deploy/proxy_test.go`: `fakeAdmin` gains an optional `onLoad func()` that runs inside the `/load` handler before it answers. It is used to assert the push happened before the commit.
  - `testdata/tests.txt` is regenerated with `make test-list`.

## Tests to write

- `./internal/release TestAbortClearsCandidate`: table-driven.
  - Rows: a `pending` candidate and a `ready` candidate, each with no Current, with Current, and with Current plus Previous.
  - Each row asserts that the candidate is nil, the phase is `none`, `Current` and `Previous` are equal to the input, and a following `Deploy("next", …)` is legal.
  - Illegal rows: no candidate, and a different id. Each asserts `errors.Is(err, ErrIllegalTransition)` and that the returned state equals the input.
- `./internal/agent/state TestRecordSwitchWritesStatusesAndStopAfter`: on a real database in `t.TempDir()`:
  - Record r1, switch it at `now1`, then r2 at `now2`, then r3 at `now3`.
  - After each switch, read the rows with direct SQL: status, `switched_at`, `stop_after`. The new current has `switched_at` = now. The old current is `previous` with `stop_after` = now + 10m. The old previous is `retired` with `stop_after` = now + 30s. All values are `time.RFC3339` UTC with a `Z`. The `apps` row equals `next`.
  - Refusals: a `next` that is not `stored.Switch(id)` (for example the wrong Previous), a different id, and a `pending` candidate (recorded with `HasPreDeploy` and a pending state). Each returns an error, and the full set of rows plus the apps row is byte-for-byte unchanged.
- `./internal/agent/state TestRecordAbortMarksCandidateAborted`:
  - The candidate row becomes `aborted` with the given `error` and `container_id` `''`.
  - The `apps` row has `candidate_release_id` NULL and phase `none`, and `Current` and `Previous` are unchanged. This is checked with an app that already has current and previous.
  - A `next` that does not follow from the stored state writes nothing.
- `./internal/deploy TestHealthGateHTTPPath`: needs real Docker. `x.healthLimit` is 6 s.
  - Each case deploys an image with entrypoint `/app health /healthz <code>`, port 8080 and health_path `/healthz`, then calls `x.healthGate` on the stored record.
  - 200 passes. 302 passes; if redirects were followed it would get 500.
  - 500 fails after at least 6 s and under 6 s + 3 s.
  - `health /other 200` with health_path `/healthz` fails (404), which proves the probe uses the stored path.
  - The error contains `<IP on mg-<project>>:8080/healthz`, with the IP from `containerIPOn`, and does not contain `health-body-must-not-leak`.
- `./internal/deploy TestHealthGateTCPConnect`:
  - With the default server on port 8080 and no health_path, the gate passes.
  - With the same image deployed on port 9 and no health_path, nothing listens there, so the gate fails at the shortened limit.
- `./internal/deploy TestHealthGateWithoutPort`:
  - Port 0 and limit 15 s, with the default server.
  - It passes, and the elapsed time from the gate's start is at least 10 s.
- `./internal/deploy TestHealthGateFailsWhenContainerExitsOrRestarts`: table over HTTP, TCP and no-port modes, with limit 30 s and entrypoint `/app die 1 1` obstacles.
  - Restarts: under `unless-stopped`, the gate fails well before 30 s with an error naming a restart or an exit.
  - Exits: the test calls `ContainerUpdate` to set the restart policy to `no` right after `Deploy`, so the container stays exited. The gate fails well before 30 s with an `exited` error.
- `./internal/deploy TestSwitchReleaseRoutesCandidateAndCommits`: uses `newExecutorAdmin`.
  - Deploy r1, r2 and r3 one after another, each with domain `sw.example.com`, and send a `SwitchRelease` after each.
  - In each `/load` (`onLoad`), the test reads `storedApp` and asserts that `Current` is still the old one, so the push came before the commit.
  - Each pushed document's route equals `expectedRoute([sw.example.com], rN, <rN container IP>:8080)`.
  - After each switch: the answer is `SUCCEEDED`, its `App` equals `appState(next)`, the stored state matches, and the row statuses, `switched_at` and `stop_after` follow `RecordSwitch`'s rules, within the time window around the call.
  - No container of r1 or r2 is stopped or removed. They remain running.
- `./internal/deploy TestSwitchReleaseHealthFailureAborts`:
  - Current is r1 (healthy, after a switch). The candidate r2 runs `health /healthz 500` with health_path `/healthz`, and `healthLimit` is 4 s.
  - The answer is `FAILED`, its detail does not contain the body marker, and its state has no candidate while `Current` stays r1.
  - The container `mg-<app>-r2` is gone. The r2 row is `aborted`, its `error` equals the detail, and `container_id` is empty.
  - The `/load` count did not change across the call.
  - A following `Deploy` of r3 is `SUCCEEDED`.
- `./internal/deploy TestSwitchReleaseRefusalsAndPushFailure`: table of refusals. Each is `REJECTED`; the stored state, a snapshot of all rows (`releaseRowText`-style) and `projectContainerIDs` are unchanged; and `/load` was not called.
  - Refusal cases: an unknown app; an app with no candidate (switched once already); a different release id; and a `pending` candidate (`has_pre_deploy`).
  - Push failure: `fake.setLoadResponse(500, "nope")` after a healthy candidate. The answer is `FAILED`, the stored state is unchanged, the phase is `ready`, the row is `candidate`, and `requireRunning` holds.
  - Then `setLoadResponse(200, "")` and send the same `SwitchRelease` again. It is `SUCCEEDED`.
- `./internal/deploy` existing tests: still pass with the new `New` signature. Nothing else is required, but `TestProxy*` must pass unchanged, which shows `Push` and `Run` behave as before.

## Edge cases

- Container IP lookup fails, for example when the container is gone: the gate fails, then abort. Covered by the abort path (removal ignores not found).
- A gate cancelled by the parent `ctx`: it returns an error and the release is aborted. This is the same path as a timeout.
- A probe is capped by both its 2 s timeout and the gate deadline, so the gate never runs past `healthLimit` + one inspect.
- Restart observed only as `Restarting=true`, or only as a changed `RestartCount`: both fail. Covered by the restart row.
- A redirect to an unreachable target: never followed. Covered by the 302 case.
- `HTTP_PROXY` in the agent's environment: `Proxy: nil` on the transport, so the probe always dials the container directly.
- The response body is never read into a string. The marker assertions in the HTTP and abort tests cover this.
- The commit fails after a 2xx push: `FAILED` with the stored state, and last pushed cleared. Not separately testable without a fault hook. `PushRouted`'s commit-error branch is small and reviewed; `RecordSwitch` refusals are covered in the state test.
- A first switch has no current release in `CurrentReleases`: the routed entry is appended and sorted by app id. Covered by the first switch in the routes test.
- A candidate with no domains: the routed push still happens and carries no route for that app. This is the same rule as `Push`.
- A switch for a `pending` candidate or another id: rejected before any read of the record or any Engine call. Covered.

## Risks

- **Diff budget (540 lines).** The Docker tests are long. The plan shares one `deployGate(t, e, x, p, app, rel, entrypoint []string, port uint32, path string) state.Record` helper across all gate and switch tests, keeps cases table-driven, reuses `fakeAdmin`, `expectedRoute`, `containerIPOn` and `storedRow`, and keeps the production code to about 200 lines. The `setAppState` and `updateOne` refactor shrinks `RecordPreDeploy`.
- **Test time.** Gate tests sleep for real seconds: at least 10 s for no-port, and the fail cases up to the shortened limit. Limits stay small (4 to 6 s, 15 s for no-port), and independent cases run with `t.Parallel()` under their own projects. That is safe because each uses its own project label and data directory.
- **macOS Docker Desktop.** Container bridge IPs cannot be reached from the host, so gate tests pass only on Linux, which is where CI and production run. This is not new (proxy tests only read the IP). No workaround is added; the probe target is fixed by the spec.
- **Lock held across the commit.** `RecordSwitch` is one short SQLite transaction. `Run` may wait for it, which is the intended serialisation. There is no deadlock, because the commit never calls back into `Proxy`.
- **Lint.** Use `errors.Is(err, http.ErrUseLastResponse)` style only where needed. The body drain discards the result (`_, _ =`). Keep wrapped errors with `%w`. Do not use the banned words (for example, never call anything "fallback").
- **Scope creep.** Nothing stops or removes the old current or previous container, and no goroutine outlives `SwitchRelease`. The switch test asserts that r1 and r2 are still running.
