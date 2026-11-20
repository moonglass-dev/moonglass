---
verdict: ready
reason: ""
---

# Plan for W2-4

Context: W2-4b and W2-4d write `stop_after` on `previous` and `retired` rows, but nothing acts on it yet. This ticket adds the stops themselves, a lock per app, the due stops in the start-up reconcile, retention (rows and images), and `Executor.Run`. No contract changes: the schema already has `stop_after`, `container_id` and `created_at`, and every Engine call it needs (container inspect, stop and remove, image remove) is in the ADR-0014 client. "Insertion order" is SQLite's `rowid`: `releases` is a rowid table, so this needs no schema change.

## Files to change

- `internal/agent/state/stops.go` (new):
  - `type DueStop struct { AppID, ReleaseID, Status, ContainerID string; StopAfter time.Time }`.
  - `func (d *DB) DueStops(ctx context.Context, now time.Time) ([]DueStop, error)`: `SELECT app_id, release_id, status, container_id, stop_after FROM releases WHERE status IN ('previous','retired') AND stop_after IS NOT NULL AND stop_after <= ? ORDER BY stop_after, app_id, release_id`, with `formatTime(now)`. A plain text comparison is correct because every stored time is the fixed `formatTime` form (UTC, whole seconds, `Z`). Parse `stop_after` with `time.Parse(time.RFC3339, …)` into `StopAfter`. Errors name the database path.
  - `func (d *DB) RecordStopped(ctx context.Context, appID, releaseID string, now time.Time) error`: one transaction. Read `status, stop_after`. Return an error and write nothing when the row is unknown, the status is not `previous` or `retired`, `stop_after` is NULL ("already recorded as stopped"), or `stop_after` is after `now` ("not due until …"). Otherwise use `updateOne`: for `previous`, `SET stop_after = NULL`. For `retired`, `SET stop_after = NULL, container_id = ''`. Both use `WHERE app_id=? AND release_id=? AND status=?`. Every message names the app and the release.
  - `func (d *DB) Retain(ctx context.Context, now time.Time) ([]string, error)`: one transaction.
    1. `DELETE FROM releases WHERE status IN ('failed','aborted') AND created_at < ?`, with `formatTime(now.Add(-7*24*time.Hour))`.
    2. Select the retired rows that fall outside the three newest and whose stop has run: `SELECT app_id, release_id, image_id FROM (SELECT app_id, release_id, image_id, stop_after, container_id, ROW_NUMBER() OVER (PARTITION BY app_id ORDER BY created_at DESC, rowid DESC) AS n FROM releases WHERE status = 'retired') WHERE n > 3 AND stop_after IS NULL AND container_id = ''`. Delete each one with `DELETE … WHERE app_id=? AND release_id=? AND status='retired'`.
    3. For each distinct non-empty image ID of the deleted rows, keep it in the result only when `NOT EXISTS (SELECT 1 FROM releases WHERE image_id = ? AND status IN ('candidate','current','previous','retired'))`.
    4. Return the IDs in sorted order (`slices.Sorted`), so the result is deterministic. The `apps` table is never written. Current, previous and candidate rows are never selected, so the foreign keys from `apps` cannot block a delete. If one ever did, the transaction fails loudly.
  - Constants: `retentionRetired = 3` and `retentionFailedAge = 7 * 24 * time.Hour`.
- `internal/deploy/stop.go` (new):
  - `const stopWait = 30` (seconds before SIGKILL), `const retainInterval = time.Hour` and `const statusRetired = "retired"`.
  - `func (x *Executor) lockApp(appID string) func()`: takes `x.locksMu`, gets or creates `x.locks[appID]` (a `*sync.Mutex`), releases `locksMu`, locks the app mutex and returns its `Unlock`.
  - `func (x *Executor) stopDue(ctx context.Context, now time.Time) error`: calls `x.db.DueStops(ctx, now)`, runs `x.stopOne(ctx, d.AppID, d.ReleaseID, now)` for each entry, and returns `errors.Join` of the failures, so one bad row never blocks the others.
  - `func (x *Executor) stopOne(ctx context.Context, appID, releaseID string, now time.Time) error`: `defer x.lockApp(appID)()`, then calls `DueStops(ctx, now)` again and finds the row. When the row is no longer listed, return nil, because a rollback or a switch changed it. Otherwise, when `ContainerID != ""`:
    - `found, err := x.engine.owned(ctx, id, appID, releaseID)`. When the container is not owned, return `container %s is not a Moonglass container of app %s release %s; it was not stopped or removed` and write nothing.
    - When found: `x.engine.stop(ctx, id)`. For `retired`, also `x.engine.remove(ctx, id)`.
    - For `retired`, whether or not the container was found, `x.engine.clearName(ctx, preDeployName(appID, releaseID), appID, releaseID)`. That is the existing "remove only when managed with the same app and release, otherwise error" rule, reused.
    - Finally `x.db.RecordStopped(ctx, appID, releaseID, now)`. Errors wrap `stop release %s of app %s: …`.
  - `func (e *Engine) owned(ctx context.Context, id, appID, releaseID string) (bool, error)`: `ContainerInspect` within `call(ctx)`. A not-found answer returns `(false, nil)`, which the caller treats as already stopped and removed. Otherwise it returns `ownedBy(labels, appID, releaseID)` and an error when the container is not owned.
  - `func ownedBy(labels map[string]string, appID, releaseID string) bool`: `managed == "true" && app == appID && release == releaseID`. `clearName` in `container.go` switches to this helper, so there is one ownership check.
  - `func (e *Engine) stop(ctx context.Context, id string) error`: `ContainerStop(call(ctx), id, client.ContainerStopOptions{Signal: "SIGTERM", Timeout: &wait})` with `wait := stopWait`. A not-found answer is not an error.
  - `func (x *Executor) retain(ctx context.Context, now time.Time) error`: calls `ids := x.db.Retain(ctx, now)`, then `x.engine.removeImage(ctx, id)` for each ID, and joins the errors.
  - `func (e *Engine) removeImage(ctx context.Context, id string) error`: `ImageRemove(call(ctx), id, client.ImageRemoveOptions{PruneChildren: true})`, never with `Force`. `isNotFound(err) || isConflict(err)` returns nil, because the image is left for the disk guard.
  - `func isConflict(err error) bool`: the same shape as `isNotFound`, `errors.As(err, &interface{ Conflict() })` or the Engine's `conflict:` message.
  - `func (x *Executor) Run(ctx context.Context, onError func(error))`:
    1. `report(x.Reconcile(ctx))`, then `report(x.retain(ctx, x.now()))`.
    2. A 1-second ticker runs `report(x.stopDue(ctx, x.now()))`, and an `x.retainEvery` ticker runs `report(x.retain(ctx, x.now()))`.
    3. It returns when `ctx.Done()` closes. `report` drops nil errors, a nil `onError` and errors after `ctx` has ended, as `Proxy.report` does.
- `internal/deploy/deploy.go`:
  - New `Executor` fields: `now func() time.Time` (set to `time.Now` in `New`), `retainEvery time.Duration` (set to `retainInterval`), `scope []string` (label filters for the container list in `Reconcile`, nil in production, so tests on a shared Engine touch only their own project, as `x.reconcile(ctx, scope…)` already lets them), `locksMu sync.Mutex` and `locks map[string]*sync.Mutex` (made in `New`).
  - `Deploy` and `fail` use `x.now()` in place of `time.Now()`, so every executor time comes from one clock.
- `internal/deploy/switch.go`: `SwitchRelease` starts with `defer x.lockApp(appID)()` and commits with `x.now()`.
- `internal/deploy/rollback.go`: `Rollback` starts with `defer x.lockApp(appID)()` and commits with `x.now()`.
- `internal/deploy/reconcile.go`: `Reconcile(ctx)` becomes `errors.Join(x.reconcile(ctx, x.scope...), x.stopDue(ctx, x.now()))`, run in that order. `reconcile` keeps its signature, because `reconcile_test.go` calls it and no existing test line may change.
- `internal/deploy/container.go`: `clearName` uses `ownedBy`.
- `internal/deploy/testdata/server/main.go`: at the start of `main`, `go exitOnTerm()`, which does `signal.Notify(c, syscall.SIGTERM)`, waits for `<-c` and calls `os.Exit(0)`. As PID 1 the test server would otherwise ignore SIGTERM, and every stop would wait the full 30 seconds. With this change "stop within 2 seconds" is testable. SIGKILL paths are unchanged.
- `docs/specs/release.md`:
  - "SwitchRelease" step 5: the per-app lock held by `SwitchRelease`, `Rollback` and each stop, and the re-read under it. The managed-label check right before the stop. A carried-out stop clears `stop_after`, and for `retired` also `container_id`. The `-pre` step container goes with its retired release. A container the Engine no longer has counts as stopped.
  - "Keep five": newest means `created_at`, then insertion order. A retired row is deleted only after its stop ran. Images are removed by ID, without force, and an image in use or missing is left for the disk guard.
  - A new "Run" paragraph: reconcile and retention at start, due stops every second, retention every hour, failures to `onError` and retried. `moonglass agent connect` runs `Executor.Run` for its whole life from W2-10.
- `docs/specs/agent-state.md`, "Rules": the retention ordering and the "stop has run" condition. A carried-out stop is one transaction that clears `stop_after` (and `container_id` for `retired`). `commands` pruning arrives with W2-10.

## Tests to write

- `./internal/agent/state TestDueStopsListsPreviousAndRetiredPastStopAfter` (new `stops_test.go`): two apps. App A has switches r1→r2→r3 at T0 and T0+1m, then a rollback at T0+2m, so r2 is current with no stop, r3 is retired and due at T0+2m30s, and r1 is retired and due at T0+1m30s. App B has switches at T0+3m (previous due at T0+13m). Add a candidate, a failed row and an aborted row. `DueStops(T0+20m)` equals the exact slice ordered by `stop_after`, then app, then release. `DueStops(T0+2m)` drops the rows not yet due.
- `./internal/agent/state TestRecordStoppedClearsStopAfter`: a table of cases. A due previous row keeps `container_id` and has NULL `stop_after`. A due retired row has an empty `container_id` and NULL `stop_after`. The error cases (current, candidate, not yet due, already stopped, unknown) each return an error, and `snapshot(t, d)` is identical before and after.
- `./internal/agent/state TestRetainKeepsFiveAndPrunesOldRows`:
  - App A has current, previous and five retired rows, with stops recorded on all of them. `Retain` deletes the two oldest retired rows. Insertion order breaks a tie in `created_at`.
  - App C has an old retired row outside the five whose `stop_after` is still set. That row is kept.
  - Failed and aborted rows created 8 days before `now` are deleted, and rows created 6 days before are kept.
  - App B's rows are judged on their own.
  - The returned IDs are exactly the images of the deleted rows, minus one image ID that app B's kept current row shares.
  - The `apps` rows are unchanged (`tableRows`).
- `./internal/deploy TestStopPreviousStopsAndKeepsContainer` (Docker, new `stop_test.go`): deploy and switch r1. Set `x.now` to now minus 11 minutes, then deploy and switch r2. Reset the clock and call `x.stopDue(ctx, time.Now())`. r1's container exists and is not running. Its row is `previous` with the same `container_id` and NULL `stop_after`. Then `Rollback` is `SUCCEEDED`, and r1 is running.
- `./internal/deploy TestStopRetiredRemovesContainerAndPreDeployStep` (Docker): r1 is deployed with a pre-deploy step and `RunPreDeploy`, then switched. r2 and r3 are switched, the r3 switch with the clock shifted back 31 seconds. `stopDue` removes `mg-<app>-r1` and `mg-<app>-r1-pre`. The r1 row is `retired` with an empty `container_id` and NULL `stop_after`. The r2 and r3 containers and the `mg-<project>` network are unchanged.
- `./internal/deploy TestStopNeverStopsRestoredRelease` (Docker): r1 then r2, with r2 switched with the clock shifted back 11 minutes, so r1 is due.
  - Take `due := DueStops(now)`, then run `Rollback`.
  - Call `x.stopOne` for the stale r1 entry. r1 keeps running and its health probe (`probe` to its IP) passes.
  - Call `stopDue(now+31s)`. r2 is removed and r1 is still running.
  - Concurrency: hold `x.lockApp(app)`, start `Rollback` in a goroutine, and assert it has not returned after 300 ms. Release the lock and assert it returns. Then do the same with `stopOne`.
- `./internal/deploy TestStopRefusesUnmanagedContainer` (Docker): make r1 a due previous row. Create an unmanaged fixture container, and point r1's `container_id` at it with raw SQL on `agent.db`, as the existing tests open it. `stopDue` returns an error naming the container ID, the app and the release. The fixture is still running and present, and the row is unchanged.
- `./internal/deploy TestReconcileCarriesOutDueStops` (Docker): set `x.scope` to this test's project label. Create one unreferenced managed fixture. App A has a due retired row and a due previous row. App B has a previous row that is not due yet. `Reconcile` removes the fixture, keeps A's current, stops A's previous and records it, and handles A's retired row. B's previous row keeps its `stop_after`, and its container is running.
- `./internal/deploy TestRetainRemovesImagesOutsideFive` (Docker, new `retain_test.go`):
  - Import images r1 to r9 with distinct entrypoints, so the image IDs differ, and assert they are distinct.
  - Write rows through `x.db`: `RecordCandidate` and `RecordSwitch` with increasing times, no containers, then `RecordStopped` on every retired row. That leaves seven retired rows, four of them outside the five.
  - One deleted row shares a kept row's image. One deleted row's image is used by a created fixture container.
  - `x.retain` returns nil. The two free images are gone (`ImageInspect` answers not found). The shared image and the in-use image are present.
  - A labelled test volume and `mg-<project>` are still there.
- `./internal/deploy TestExecutorRunStopsAndRetainsUntilCancelled` (Docker, new `run_test.go`):
  - Set `x.scope` and `x.retainEvery = time.Second`.
  - Setup: an unreferenced managed fixture; a failed row created 8 days ago, through `x.db.RecordFailed`; r1, r2 and r3 switched, r3 with the clock shifted back 28 seconds, so r1 becomes due about 2 seconds later; a second app whose due previous row points at an unmanaged fixture through raw SQL.
  - Start `Run` in a goroutine, with `onError` writing to a channel.
  - At start, the fixture is removed and the old failed row is gone.
  - r1's container is gone by its `stop_after` + 2 seconds.
  - `onError` receives the unmanaged-container error and `Run` keeps going.
  - A second 8-day-old failed row, written after start, is gone within 3 seconds.
  - After `cancel()`, `Run` returns within 2 seconds.
- Run `make test-list` to add the ten names to `testdata/tests.txt`.

## Edge cases

- A row that is no longer due when its stop runs: a rollback restored it, a switch retired it, or another stop already ran. It is skipped by the re-read under the lock (`TestStopNeverStopsRestoredRelease`). `RecordStopped` refuses it too (`TestRecordStoppedClearsStopAfter`).
- A recorded container ID that names a container without our labels: refused, nothing stopped or written, and the error names the container, the app and the release (`TestStopRefusesUnmanagedContainer`).
- A recorded container the Engine no longer has (for example, removed by the start-up reconcile for a retired release), or an empty `container_id`: it counts as stopped and removed, and the row is recorded (`TestReconcileCarriesOutDueStops`).
- A `-pre` name held by a foreign container: `clearName` returns an error and leaves it in place. The row stays due and the error is reported every turn, which is loud by design.
- A stop that is already stopped: the Engine answers 304 and the client returns nil.
- Image removal answered with in use or conflict, or with not found: not an error (`TestRetainRemovesImagesOutsideFive`). Any other Engine error is joined and reported, and the row is already gone, so the disk guard owns that image afterwards.
- An image ID shared between a deleted row and a kept row of any app: not returned (`TestRetainKeepsFiveAndPrunesOldRows`).
- A retired row outside the five whose stop is still due: kept, so its container is never orphaned (`TestRetainKeepsFiveAndPrunesOldRows`).
- A failing step in `Run`: reported and retried, and `Run` continues. Nothing is reported after `ctx` ends (`TestExecutorRunStopsAndRetainsUntilCancelled`).
- Time limits: stop, remove and inspect each run inside `call(ctx)` (60 seconds). The stop's own wait is 30 seconds, below that limit.
- Lock order: the app lock is always taken before `Proxy.mu` (`PushRouted`), and stops never take `Proxy.mu`, so there is no deadlock. Deploy and RunPreDeploy do not take the app lock, as the ticket says. A stop only ever acts on `previous` and `retired` rows, never on a candidate.

## Risks

- Unscoped `Reconcile` on the shared Engine would remove other worktrees' containers. Tests set `x.scope`, and production leaves it nil.
- `tests-kept`: no line of an existing test changes. `reconcile`'s variadic signature stays, and the new tests go in new files.
- Docker tests that wait 30 seconds per stop: avoided by the SIGTERM handler in the test server.
- Identical image IDs from identical imports would make the image test meaningless or hit "referenced in multiple repositories": avoided with distinct entrypoints and an assertion that the IDs are distinct.
- Window functions need SQLite 3.25 or later. `modernc.org/sqlite` ships 3.4x.
- The race detector: `x.now` and `x.retainEvery` are set only before `Run` starts or while no goroutine runs. The lock map is guarded by `locksMu`.
- The size limit: about 300 counted production lines. Tests and docs do not count.
- Security (B5): every stop and remove follows a label check made right before the call. Images are removed only by an ID from a deleted row, never forced. No network or volume call is added. No message field reaches any of this (B3). Errors name only IDs (§9.3).
