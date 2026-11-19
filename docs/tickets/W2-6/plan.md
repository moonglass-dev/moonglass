---
verdict: ready
reason: ""
---

# Plan for W2-6

Context: W2-3 leaves a `Deploy` with `has_pre_deploy` as a `pending` candidate whose container is created but not started. This ticket adds `Executor.RunPreDeploy`, which runs the step in a checked one-off container, then starts the candidate or aborts the deploy. It needs no contract change: `RunPreDeploy` and `pre_deploy_reversible` already exist, `release.State.PreDeployDone` already exists, and `pre_deploy` is a `Kind` in `docs/specs/container-policy.md`.

## Files to change

- `internal/deploy/deploy.go`
  - `Executor` gains `preDeployLimit time.Duration`. `New` sets it to `preDeployTimeout = 15 * time.Minute` (a new const next to `pullTimeout` and `callTimeout` in `engine.go`, or in `predeploy.go`). `New`'s signature does not change.
  - `appSpec` stays as it is, and `Deploy` does not change.

- `internal/deploy/predeploy.go` (new)
  - `const labelRole = "dev.moonglass.role"`.
  - `func preDeploySpec(b state.Binding, rec state.Record, args []string) policy.Spec`:
    `s := appSpec(&agentv1.Deploy{AppId: rec.AppID, ReleaseId: rec.ReleaseID, Project: b.Project, Environment: b.Environment, Service: b.Service}, resolved{ID: rec.ImageID})`, which sets `Image` to the image ID because `RepoDigest` is empty. Then `s.Kind = "pre_deploy"`, `s.Cmd = slices.Clone(args)`, `s.RestartPolicy = "no"`, and `s.AgentLabels[labelRole] = "pre_deploy"`. `appSpec` builds a fresh map on every call, so no map is shared.
  - `func preDeployName(appID, releaseID string) string` returns `"mg-" + appID + "-" + releaseID + "-pre"`.
  - `func (x *Executor) RunPreDeploy(ctx context.Context, r *agentv1.RunPreDeploy) *agentv1.CommandResult`, in this order:
    1. `b, st, known, err := x.db.App(ctx, appID)`. A read error is `FAILED` with `read app <id>: <err>`, as in `Deploy`. If the app is unknown, the answer is `REJECTED` with `app <id> has no release on this server; deploy it first`, and the zero state.
    2. `ready, err := st.PreDeployDone(relID, true)`. An error is `REJECTED` with `err.Error()` and the state `st`. This covers a release that is not the candidate, a `ready` candidate and a second run.
    3. `rec, found, err := x.db.Release(ctx, appID, relID)`. An error, or `!found`, is `FAILED` with the state unchanged. A missing row breaks the foreign key invariant, and without its container ID the executor has nothing to abort.
    4. `checked, err := x.policy.Check(preDeploySpec(b, rec, r.GetArgs()))`. A `Rejection` is `REJECTED` with `err.Error()` as `detail` and `st` as the state. Nothing has been created and nothing is written.
    5. `code, err := x.engine.runStep(ctx, checked, b.Project, rec, x.preDeployLimit)`. An error leads to `x.abortPreDeploy(ctx, st, rec, r.GetReversible(), err.Error())`.
    6. If `code != 0`, the result is `abortPreDeploy` with `fmt.Sprintf("pre-deploy step exited with code %d; its output is in the logs of container %s", code, preDeployName(...))`.
    7. `x.engine.start(ctx, rec.ContainerID)`, the W2-3 start with its running check. An error leads to `abortPreDeploy`.
    8. `x.db.RecordPreDeploy(ctx, appID, relID, ready, r.GetReversible(), "")`. An error leads to `abortPreDeploy`, which removes the started candidate.
    9. The answer is `result(SUCCEEDED, "", ready)`, with phase `READY`.
  - `func (x *Executor) abortPreDeploy(ctx context.Context, st release.State, rec state.Record, reversible bool, cause string) *agentv1.CommandResult`:
    - `aborted, err := st.PreDeployDone(rec.ReleaseID, false)`. This cannot fail once step 2 has passed. If it does fail, the answer is `FAILED` with the error appended and the state `st`.
    - `detail := truncate(cause)`. Then `x.engine.remove(ctx, rec.ContainerID)`; a not-found error (`isNotFound`) counts as removed, and any other error is appended as `; removing the candidate container also failed: …`.
    - `x.db.RecordPreDeploy(ctx, …, aborted, reversible, truncate(detail))`. On error, `; recording the abort also failed: …` is appended and the answer is `FAILED` with the state `st`.
    - Otherwise the answer is `result(FAILED, detail, aborted)`.
  - `func (e *Engine) runStep(ctx context.Context, c policy.Checked, project string, rec state.Record, limit time.Duration) (int64, error)`:
    - It calls `ensureNetwork(ctx, project)`, then `clearName(ctx, name, rec.AppID, rec.ReleaseID)` (the candidate's leftover rule), then `create(ctx, c, name)`.
    - It starts the container with plain `ContainerStart` under `call(ctx)`, without `runningAfterStart`, because a step may exit at once.
    - It returns `e.waitStep(ctx, id, limit)`.
    - It never removes the step container.
  - `func (e *Engine) waitStep(ctx context.Context, id string, limit time.Duration) (int64, error)`:
    - It runs `waitCtx, cancel := context.WithTimeout(ctx, limit)` and `ContainerWait(waitCtx, id, {Condition: container.WaitConditionNotRunning})`, called after start. A `not-running` wait on a container that has already exited returns its code at once.
    - On `Result` it returns `StatusCode`. A non-nil `Error` field is an error.
    - When an `Error` arrives and `waitCtx` ran out while `ctx` did not, the step is over its limit. The executor kills the container with `ContainerKill(call(context.WithoutCancel(ctx)), id, {Signal: "SIGKILL"})`, then waits again (`not-running`, under `call(context.WithoutCancel(ctx))`) and returns that exit code (137). The caller turns it into the exit-code detail.
    - Any other wait error, including a cancelled `ctx`, kills the container the same way and returns `fmt.Errorf("wait for pre-deploy step %s: %w", id, err)`.
    - No log is read anywhere in the package.

- `internal/agent/state/state.go`
  - The query in `App` moves into `func readState(ctx context.Context, q interface{ QueryRowContext(context.Context, string, ...any) *sql.Row }, appID string) (Binding, release.State, bool, error)`, used by `App` with `d.db` and by `RecordPreDeploy` with the transaction. `App`'s behaviour and its error text do not change.
  - `const statusAborted = "aborted"`.
  - `func (d *DB) Release(ctx context.Context, appID, releaseID string) (Record, bool, error)` runs `SELECT image_ref, image_id, repo_digest, port, health_path, domains, has_pre_deploy, container_id FROM releases WHERE app_id = ? AND release_id = ?`, decodes `domains` from JSON, and returns `found=false` on `sql.ErrNoRows`. Errors name the app, the release and the database path.
  - `func (d *DB) RecordPreDeploy(ctx context.Context, appID, releaseID string, next release.State, reversible bool, detail string) error`, in one transaction:
    - It reads the stored state with `readState(tx)`. An unknown app is an error.
    - `want, err := stored.PreDeployDone(releaseID, next.Candidate != nil)`. An error (the release is not the pending candidate), or `want` differing from `next` in the current, previous and candidate ids or the phase (compared through `nullID` and the phase), means an error and no write.
    - On success it runs `UPDATE releases SET pre_deploy_reversible = ? WHERE app_id = ? AND release_id = ? AND status = 'candidate'`.
    - On abort it runs `UPDATE releases SET pre_deploy_reversible = ?, status = 'aborted', error = ?, container_id = '' WHERE … AND status = 'candidate'`.
    - In both cases it requires exactly one affected row, otherwise an error with a rollback.
    - It then runs `UPDATE apps SET current_release_id = ?, previous_release_id = ?, candidate_release_id = ?, phase = ? WHERE app_id = ?` from `next`, and commits.
    - `detail` is stored as given and never logged.

- `internal/deploy/testdata/server/main.go`
  - With no arguments it serves HTTP as now.
  - Otherwise `os.Args[1]` selects a mode:
    - `exit <n>` exits with code n.
    - `print <marker> <n>` writes the marker to stdout and stderr, then exits with code n.
    - `sleep <seconds>` sleeps, then exits 0.
    - Anything else writes a usage message to stderr and exits 2.
  - Standard library only. The image's ENTRYPOINT is `/app`, so `Cmd` = args reaches it as `os.Args[1:]`.

- `docs/specs/release.md`, section "Executor: pre-deploy steps (W2-6)". Add the three rules:
  - A policy `Rejection` of the step's spec is `REJECTED` with the state unchanged, and nothing is created or written.
  - A leftover `mg-<app_id>-<release_id>-pre` follows the candidate's leftover rule: a managed container of the same app and release is removed, and any other container fails the step and is left in place.
  - A candidate that fails to start after exit 0 aborts like an Engine error.
  - The order (transition checked first, an unknown app `REJECTED`) and the 60-second limit on every other Engine call can be stated in the same bullet list.

- `testdata/tests.txt`: regenerated with `make test-list`.

## Tests to write

`internal/deploy/predeploy_test.go`, new and additive only, because existing test lines on main must not change. It adds these helpers:

- `deployPending(t, e, x, p, app, rel)` imports the image the way `deployImported` does, sends `deployMsg` with `HasPreDeploy = true`, and requires `SUCCEEDED`.
- `storedReversible(t, dataDir, app, rel) sql.NullBool` reads `pre_deploy_reversible`.
- `preDeployMsg(app, rel string, reversible bool, args ...string)`.
- `containerLogs(t, e, id) string` reads the raw `ContainerLogs` stream with stdout and stderr and returns it as a string.

The named tests:

- `./internal/deploy TestPreDeploySpecDerivesFromCandidate` (no Docker). It uses a binding, and a record whose `RepoDigest` differs from its `ImageID`.
  - The expected spec is `appSpec(...)` for the candidate with the image ID, with `Kind`, `Cmd`, `RestartPolicy` and the role label changed. The test compares it to the actual spec with `reflect.DeepEqual`.
  - It also asserts `Image == rec.ImageID`, never the digest.
  - It walks the fields with reflection and asserts that every field outside `{Kind Project Environment Service Image Cmd AgentLabels Networks RestartPolicy LogConfig}` is zero, which covers `Env`, `Labels` and `Mounts`.
  - It asserts that mutating the `args` slice afterwards does not change `Cmd`.
- `./internal/deploy TestRunPreDeploySuccessStartsCandidate` is table-driven over `reversible` true and false. `deployPending`, then `RunPreDeploy` with `exit 0`, gives `SUCCEEDED` with phase `READY`. The test then checks:
  - The candidate is running.
  - The `-pre` container is stopped with `ExitCode == 0`, and its `Config.Image` equals the candidate image ID.
  - `Config.Cmd` equals args. The restart policy is `no`.
  - Its labels are exactly the six agent labels plus `dev.moonglass.role=pre_deploy`.
  - It is attached to `mg-<project>` only.
  - `Config.Env` equals the image's own `Config.Env` from `inspectImage`.
  - The row is `candidate` and the stored reversible value equals the sent one.
- `./internal/deploy TestRunPreDeployFailureAbortsDeploy`. A step with `exit 3` gives `FAILED` with exactly `pre-deploy step exited with code 3; its output is in the logs of container mg-<app>-<rel>-pre`. The test first deploys and switches nothing, so `Current` and `Previous` are empty. It records the state before the run and compares:
  - The result's and the database's `Current` and `Previous` are unchanged, and both have no candidate, with phase `NONE`.
  - No container has the candidate's name.
  - The row is `aborted` with `error` equal to that detail and an empty `container_id`.
  - The `-pre` container exists and is stopped with code 3.
  - A `Deploy` of a new release is then `SUCCEEDED`.
- `./internal/deploy TestRunPreDeployOutputStaysOutOfResult`. The step `print <randHex marker> 1` gives `FAILED`. The marker is in `containerLogs` of the `-pre` container. It is not in `prototext.Format(res)` or `proto.Marshal(res)` bytes, and not in any column of the release row (the raw row is read with `storedRow` fields plus `error`).
- `./internal/deploy TestRunPreDeployTimeoutKillsStep`. It sets `x.preDeployLimit = 3 * time.Second`, and the step is `sleep 120`.
  - The result is `FAILED` with the detail for code 137. The test reads the code from the inspected container and asserts the detail with that code.
  - The `-pre` container is not running.
  - The row is `aborted`, there is no candidate, and the candidate container is gone.
  - The time elapsed is under `3s + 60s`, far below 120 s.
- `./internal/deploy TestRunPreDeployEngineErrorAborts`. It runs `deployPending`, then force-removes the candidate container with `e.cli.ContainerRemove`, then runs a step with `exit 0`.
  - The result is `FAILED`, its detail names the start of the candidate container, and the state has no candidate.
  - The row is `aborted` with an empty `container_id`.
- `./internal/deploy TestRunPreDeployRejectsIllegalTransition` is table-driven, with these cases:
  - an unknown app id;
  - a pending candidate run with release id `other`;
  - a `ready` candidate, made with `deployImported` without `has_pre_deploy`;
  - a second `RunPreDeploy` after a successful one.

  For each case the result is `REJECTED`. The test snapshots the `-pre` container IDs, the `storedApp` state, `storedRow` and `storedReversible` before and after, and they are equal: no new `-pre` container for the first three cases, and the same ID in the fourth. For the cases with a running candidate, its container ID and `State.StartedAt` are unchanged and it is still running.
- `./internal/deploy TestRunPreDeployReplacesLeftoverStepContainer` has two subtests:
  - **Managed leftover.** `createFixture(fixtureSpec(p, app, rel, imageID, true))` at `mg-<app>-<rel>-pre`, then the step `exit 0`. The result is `SUCCEEDED`, and the `-pre` container ID differs from the fixture's, whose ID is gone.
  - **Foreign.** The fixture is made with `managed=false`. The result is `FAILED` with a detail that names the container. The deploy is aborted (no candidate, row `aborted`), and the fixture is still present with the same ID.
- `./internal/agent/state TestRecordPreDeployOutcome`, in `state_test.go` and appended. It is table-driven over states made with `mustDeploy(…, hasPreDeploy)` and `mustRecord`. It uses a current release so that `Current` and `Previous` are non-nil, and checks with `readRelease`, `readApp` and `snapshot`:
  - **Success.** The row stays `candidate`, `pre_deploy_reversible` is `1` or `0` as sent, the apps phase is `ready`, the candidate is unchanged, and `Release()` returns the stored record.
  - **Abort.** The row is `aborted` with the error and an empty `container_id`. The apps row has no candidate, phase `none`, and `current` and `previous` unchanged.
  - **Refusals.** An unknown app; a release that is not the candidate; a `ready` (non-pending) candidate; a `next` state that does not follow from the stored one (wrong phase). Each returns an error, and `snapshot` is identical before and after.
  - It also checks that `Release` of an unknown pair returns `found=false`.

## Edge cases

- Unknown app, a release that is not the candidate, a ready candidate, a repeated run: `REJECTED`, nothing touched. Covered by `TestRunPreDeployRejectsIllegalTransition`.
- A policy `Rejection` of the derived spec: this cannot happen with any `agent.toml` today, because the spec differs from the already-checked candidate spec only in `Kind`, `Cmd`, `RestartPolicy` and one agent label, and none of those is a denied field. The code path matches `Deploy`'s step 4, and the rule is pinned in the spec. No Docker test can make it happen without a policy change.
- Missing image, network failure, a foreign container at the `-pre` name, or a create, start, wait or kill error lead to an abort with the wrapped error. The foreign name is covered by `TestRunPreDeployReplacesLeftoverStepContainer`, and the other paths share the same `abortPreDeploy`.
- A step that exits at once: the executor starts it without `runningAfterStart` and waits `not-running` after start. Covered by the `exit 3` and `exit 0` tests.
- The time limit: the executor kills the step and reports code 137 as a normal exit-code failure. Covered by `TestRunPreDeployTimeoutKillsStep`. The kill and the second wait use `context.WithoutCancel`, so a cancelled caller still kills the step.
- The candidate fails to start after exit 0. Covered by `TestRunPreDeployEngineErrorAborts`. The remove of a container that is already gone counts as success.
- The success write fails after the candidate started: the deploy aborts, and the candidate is removed so it cannot run unrouted. If the record write fails during the abort too, the answer is `FAILED` with the unchanged state, and the start-up reconcile removes whatever is left.
- Output never read: nothing in the package calls `ContainerLogs`. Covered by `TestRunPreDeployOutputStaysOutOfResult`.
- `args` aliasing: `Cmd` is a clone. Covered by `TestPreDeploySpecDerivesFromCandidate`.
- A state write that does not follow, or a racing write: the transaction re-derives the state from the stored one and requires one affected row. Covered by the refusal rows of `TestRecordPreDeployOutcome`.
- Detail length: `truncate` applies to everything the result and the row carry.

## Risks

- The `tests-kept` gate fails on any changed line of an existing `_test.go`, so all new helpers go in new code or in appended code. `helpers_test.go`, `deploy_test.go` and the existing state tests are not edited. `testdata/server/main.go` is not a `_test.go` file and may be rewritten.
- Size: about 200 counted lines (`predeploy.go` about 140, `state.go` about 70, `deploy.go` about 3). Tests, testdata and docs do not count, so this stays under 350.
- Wait race: using `next-exit` before start could hang, so the plan calls `not-running` after start. The test image exits from argv without delay, and the tests assert the exit codes.
- Timeout flakiness: the time bound is generous (limit plus 60 s against a 120 s sleep).
- The reconcile keeps `-pre` containers, because their release label matches the candidate. After an abort they no longer match, so a later start-up reconcile removes the stopped `-pre` container. That is the W2-3 rule and is out of scope here; the ticket only requires that RunPreDeploy itself never removes it.
- Lint: the `ContainerWait` select must handle both channels. Errors are wrapped with `%w`, and no banned words are used.
- `Env` assertion: the imported image has no `Env` of its own, so the step's `Config.Env` must equal the image's `Config.Env`, possibly nil. The test compares to the inspected image, not to a literal.
