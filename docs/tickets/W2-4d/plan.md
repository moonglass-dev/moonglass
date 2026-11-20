---
verdict: ready
reason: ""
---

# Plan for W2-4d

No contract change: `agentv1.Rollback` already exists (`GetAppId`), the schema already has `status`, `switched_at` and `stop_after`, and the spec and threat model already settle the behaviour. The work reuses `healthGate`, `engine.start`, `engine.runState`, `Proxy.PushRouted`, `updateOne`, `setAppState`, `sameState` and `readState`.

## Files to change

- `internal/agent/state/state.go`: add
  `func (d *DB) RecordRollback(ctx context.Context, appID string, next release.State, now time.Time) error`,
  built the same way as `RecordSwitch`: `BeginTx`, deferred `tx.Rollback()`, `readState(ctx, tx, appID)`. An unknown app returns `record rollback of app %s: app is not in agent state; deploy it first`. Then `want, err := stored.Rollback()`, and that error is wrapped. `!sameState(want, next)` returns `record rollback of app %s: the given state does not follow from the stored one; record the state Rollback returned`. Then, all through `updateOne` with the expected old status:
  1. restored row (`stored.Previous.ID`): `UPDATE releases SET status = 'current', switched_at = ?, stop_after = NULL WHERE app_id = ? AND release_id = ? AND status = 'previous'` with `formatTime(now)`;
  2. rolled-back row (`stored.Current.ID`, always set when Previous is set): `UPDATE releases SET status = 'retired', stop_after = ? WHERE … AND status = 'current'` with `formatTime(now.Add(retiredStopAfter))`;
  3. `setAppState(ctx, tx, appID, next)`, which writes the candidate id and phase exactly as they were, because `Rollback` leaves them alone;
  4. `tx.Commit()`. Each error is wrapped with `record rollback of app %s: %w`.
  The candidate row is never named, so it is untouched. Doc comment as for `RecordSwitch`.
- `internal/deploy/rollback.go` (new file): add
  `func (x *Executor) Rollback(ctx context.Context, r *agentv1.Rollback) *agentv1.CommandResult`:
  1. `b, st, known, err := x.db.App(ctx, appID)`: on error, `FAILED` (`read app %s: %v`) as SwitchRelease does. If `!known`, `REJECTED` with `app <id> has no release on this server; deploy it first`. `next, err := st.Rollback()`: on error, `REJECTED` with `err.Error()`. No Engine call happens before these checks.
  2. `rec, found, err := x.db.Release(ctx, appID, st.Previous.ID)`: on error, `FAILED`. If `!found`, `FAILED` with `release %s of app %s has no record on this server; deploy again`. If `rec.ContainerID == ""`, `FAILED` with the exact constant `previousGone = "the previous release's container is gone; deploy again"`.
  3. `rs, err := x.engine.runState(ctx, rec.ContainerID)`: if `isNotFound(err)`, `FAILED` with `previousGone`; any other error is `FAILED` with `release %s of app %s: container %s: %v`. If `!rs.Running`: `x.engine.start(ctx, rec.ContainerID)` and then `x.healthGate(ctx, b.Project, rec)`. Either error is `FAILED` with `truncate(err.Error())` and state `st`. Nothing is removed, stopped, pushed or written. If it is running: no start and no gate.
  4. `x.proxy.PushRouted(ctx, state.CurrentRelease{Binding: b, Record: rec}, func(ctx) error { return x.db.RecordRollback(ctx, appID, next, time.Now()) })`: on error, `FAILED` with `err.Error()` and state `st`.
  5. `result(SUCCEEDED, "", next)`.
  Every answer goes through the existing `result`, so `truncate` applies. No goroutine is started, and nothing stops or removes the rolled-back container. About 60 lines, all non-test code, well inside the 300-line cap (tests and docs are not counted).
- `docs/specs/release.md`, "### Rollback": rewrite steps 1 to 5 in the executor's order. (1) Unknown app or illegal → `REJECTED`, nothing read or touched, plus the W6-1c sentence kept as it is. (2) A missing record is `FAILED`. An empty `container_id`, or a container the Engine does not find, is `FAILED` with the exact message, nothing pushed and the state unchanged. (3) A running container: no start and no gate, so the rollback finishes in under 5 seconds (G2). A stopped one: started, then gated with the app's project. A failed start or gate is `FAILED` with its detail, and the container is left in place. (4) The previous release is routed in place of the app's stored current release through `Proxy.PushRouted`, with the commit inside its lock. A failed push or commit is `FAILED` with the stored state unchanged, and the rollback can be sent again. (5) The commit is one transaction: the restored row becomes `current` with `switched_at` = now and `stop_after` cleared, the rolled-back row becomes `retired` with `stop_after` = now + 30 s, and the candidate is untouched. Answer `SUCCEEDED` with the new state.
- `docs/specs/agent-state.md`, "Rules": add a **A rollback** (W2-4d) bullet after the switch bullet. It is one transaction with the statuses and times above, the candidate row and the `apps` candidate and phase are unchanged, times are RFC 3339 UTC, and a state that does not follow from the stored one writes nothing.
- `testdata/tests.txt`: `make test-list` after adding the tests.

## Tests to write

State tests go in `internal/agent/state/rollback_test.go` and reuse `openTemp`, `sampleBinding`, `sampleRecord`, `mustDeploy`, `mustRecord`, `mustSwitch`, `readSwitchRow`, `requireStoredState`, `requireTime` and `snapshot`.

- `./internal/agent/state TestRecordRollbackRestoresPrevious`, table-driven:
  - "two switches": deploy and switch rel-1 then rel-2, then `RecordRollback(next = stored.Rollback(), now3)`. rel-1 is `current` with `switched_at` = now3 and `stop_after` NULL. rel-2 is `retired` with `stop_after` = now3 + 30 s in RFC 3339 UTC. The stored state has current rel-1, previous nil, no candidate.
  - "with a candidate": same, plus a rel-3 candidate deployed before the rollback. rel-3's row (`tableRows` or `readSwitchRow`) and the stored candidate and phase are unchanged.
  - Refusals, each comparing `snapshot` before and after and requiring an error: an unknown app; an app with only a current (one switch, no previous), with any `next`; a `next` that does not follow (for example the stored state itself, or a state with current = rel-2).

Deploy tests go in `internal/deploy/rollback_test.go`. They use real Docker and reuse `newSwitchExecutor`, `deployDomains`, `deployGate`, `switchMsg`, `requireAppState`, `requireStoredState`, `storedTimes`, `parseStoredTime`, `stateText`, `pushedRoutes`, `expectedRoute`, `containerIPOn`, `requireRunning`, `projectContainerIDs`, `singleContainerID` and `fakeAdmin.loadCount/loadAt/setLoadResponse`. A small helper `switchTwice(t, e, x, p, app, domain)` deploys and switches r1 then r2, and `rollbackMsg(app)` builds `&agentv1.Rollback{AppId: app}`.

- `./internal/deploy TestRollbackRunningPreviousRoutesAndCommits`: after `switchTwice`, `requireRunning(r1)`. It times `x.Rollback`: `SUCCEEDED` in under 5 s, and the result and stored state are current r1, previous empty. Exactly one new `/load`, whose only route equals `expectedRoute([domain], "r1", ip(r1):8080)`. r1 `current` with `switched_at` inside [before truncated to the second, after] and `stop_after` NULL. r2 `retired` with `stop_after` = r1's `switched_at` + 30 s. `requireRunning(r2)`, and the project's container IDs are unchanged.
- `./internal/deploy TestRollbackStoppedPreviousStartsAndGates`, with two subtests:
  - "starts and gates": after `switchTwice`, `ContainerStop` the r1 container. `Rollback` answers `SUCCEEDED`, `requireRunning(r1)`, one `/load` routing r1, and the stored state is rolled back.
  - "failed gate": `x.healthLimit = 6s`. r1 is deployed with `deployGate` using the entrypoint `/app health /healthz 500`, port 8080 and an empty health path, so the TCP gate passes and the switch succeeds. Then r2 is deployed and switched. The r1 container is stopped, and r1's stored `health_path` is set to `/healthz` with a direct SQL `UPDATE` on `agent.db`, so the restarted container answers the failing status. `Rollback` answers `FAILED`. The detail is non-empty and does not contain `health-body-must-not-leak`. The `/load` count is unchanged, `stateText` is unchanged (snapshot taken after the UPDATE), the stored state is still current r2 and previous r1, and the r1 container still exists (`singleContainerID`).
- `./internal/deploy TestRollbackPreviousContainerGone`, with two subtests: (a) after `switchTwice`, `ContainerRemove(Force)` on r1; (b) r1's `container_id` set to `''` with direct SQL. Each answers `FAILED` with the exact detail `the previous release's container is gone; deploy again`, the `/load` count is unchanged, and `stateText` is unchanged.
- `./internal/deploy TestRollbackRefusalsAndPushFailure`: table of refusals: an unknown app; "first release only" (deploy and switch r1); "right after a rollback" (`switchTwice`, then `Rollback` succeeds). Each answers `REJECTED` with a non-empty detail. The result app equals the stored state, and `stateText`, the `/load` count and `projectContainerIDs` are unchanged. Push failure: after `switchTwice`, `fake.setLoadResponse(500, "nope")`. `Rollback` answers `FAILED` with a detail that contains `500`, and the stored state and `stateText` are unchanged. Then `setLoadResponse(200, "")`, and the re-sent `Rollback` answers `SUCCEEDED` with the stored state rolled back.
- `./internal/deploy TestRollbackLeavesCandidateUntouched`: after `switchTwice`, deploy r3 with a different domain, so the candidate is `ready` and running. `Rollback` answers `SUCCEEDED`. The result and stored candidate are r3 with phase ready, r3's row status is `candidate`, `requireRunning(r3)`, and the pushed routes contain exactly one route, r1's, none naming r3 or r3's container IP.

## Edge cases

- Unknown app or no previous: `REJECTED` before any read of the release record or any Engine call (refusal table; `stateText`, `/load` and containers unchanged).
- Previous record missing: `FAILED` with a message. This is a defensive read, the same branch SwitchRelease has. The foreign key from `apps.previous_release_id` prevents the missing row, so no test forges it.
- Empty `container_id`, or a container removed outside Moonglass: `FAILED` with the exact message (ContainerGone a/b).
- A container that is restarting, or that exits right after start: `runState` reports it as not running, `engine.start` and `runningAfterStart` refuse, and the answer is `FAILED` with the container left in place. This is the same path as the failed-gate subtest.
- A gate failure never carries a probe body: `probe` never reads the body into an error (asserted in the failed-gate subtest).
- Push non-2xx: `FAILED`, nothing committed, and a re-send succeeds (PushFailure).
- Commit failure inside `PushRouted`: the existing code clears `p.last`, so `Run` restores the stored routes. `RecordRollback`'s refusals are covered in the state test.
- A concurrent Run push between push and commit: impossible, because the commit runs inside `PushRouted`'s lock.
- The previous release has no domains: `route` skips it, and no special case is needed.

## Risks

- **Flaky timing in the 5 s assertion.** The running path makes only an inspect, two GETs and one POST to a local fake, with no gate. The start time is taken right before `Rollback`.
- **Gate-failure setup.** A health mode that fails would also fail the first switch. Deploying with an empty health path (TCP gate) and then setting the stored `health_path` with SQL in the test keeps it deterministic without changing `testdata/server`.
- **`isNotFound` on `runState` errors.** `runState` wraps with `%w`, and `isNotFound` also matches the "No such" text, so a removed container maps to the exact message. ContainerGone (a) asserts it.
- **Restart policy.** `unless-stopped` keeps an explicitly stopped container stopped, so the stopped-path test is stable.
- **Review: one push, gate and start path.** No new helper duplicates `healthGate`, `engine.start` or `PushRouted`. `RecordRollback` follows `RecordSwitch`, and `previousGone` is the only new constant.
- **Docs.** release.md must not claim the volume refusal (it keeps the "From W6-1c" sentence) or W2-4's stops.
- **CI.** Run `make test-list` and `make fmt`, then `make ci`. The new tests get no `t.Parallel()` at the top level, which matches the switch tests.
