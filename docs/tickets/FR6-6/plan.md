---
verdict: ready
reason: ""
---

# Plan for FR6-6

## Files to change

- `internal/agent/state/stops.go`:
  - `DueStop` loses `Status string` and gains `Retired bool` (field order: `StopAfter`, `AppID`, `ReleaseID`, `ContainerID`, `Retired`). Doc comment: "Retired is true for a retired row, whose container a stop removes, and false for a previous row, whose container a stop keeps."
  - `DueStops` keeps `selectDueStops` and its arguments unchanged. In the scan loop it scans `status` into a local `status string` (next to `stopText`) and sets `stop.Retired = status == statusRetired`. Nothing else changes. The status constants stay unexported.
- `internal/agent/state/state.go`: the comment above `CommandDeploy` becomes `// The command types the agent records; W3-4 adds build, and W5-5b adds stop_app and remove_app.` It no longer names W3-7.
- `internal/deploy/stop.go`: delete the `statusRetired` constant and its comment from the const block. In `stopOne`, both `if stop.Status == statusRetired {` become `if stop.Retired {`. The order of the ownership check, stop, remove, `clearName` and `RecordStopped` stays as it is.
- `internal/agent/state/stops_test.go`: in `TestDueStopsListsPreviousAndRetiredPastStopAfter`, every `Status: "retired"` becomes `Retired: true` and every `Status: "previous"` becomes `Retired: false`, case for case, with no entry dropped. Add the three new state tests below.
- `internal/deploy/status_text_test.go` (new): `TestDeployDeclaresNoReleaseStatusText`. It goes in its own file because `stop_test.go` is an integration file that needs Docker.
- `testdata/tests.txt`: regenerate with `make test-list`.

The non-test diff is about 12 lines, under the 40-line cap. Test files do not count toward it (`tools/pipeline/verify.go`).

## Tests to write

- `./internal/agent/state TestDueStopsMarksRetiredRows`: uses `stopRecordSetup` (r1 retired due t0+1m30s, r2 previous due t0+11m, r3 current, r4 candidate). Table-driven over `{name, releaseID, want DueStop}`:
  - "a retired row": `{AppID: "app-1", ReleaseID: "r1", ContainerID: "container-r1", StopAfter: t0.Add(90*time.Second), Retired: true}`
  - "a previous row": `{AppID: "app-1", ReleaseID: "r2", ContainerID: "container-r2", StopAfter: t0.Add(11*time.Minute), Retired: false}`

  `DueStops(ctx, t0.Add(20*time.Minute))` is called once. Each case finds its row by app and release (and fails with the full list when the row is missing), then uses `reflect.DeepEqual` against `want` and prints got and want with `%+v`.
- `./internal/agent/state TestDueStopRetiredAfterSwitch`: on a fresh `openTemp` DB, `switchApp` runs three times for `app-1`: r1 at t0, r2 at t0+1m, r3 at t0+2m. r1 then becomes retired, due t0+2m30s, and r2 becomes previous, due t0+12m. `DueStops(t0+20m)` must equal `[{r1, container-r1, t0+2m30s, Retired: true}, {r2, container-r2, t0+12m, Retired: false}]` exactly.
- `./internal/agent/state TestDueStopRetiredAfterRollback`: on a fresh DB, `switchApp` runs for r1 at t0 and for r2 at t0+1m. `mustRollback` then runs on the returned state, followed by `RecordRollback(ctx, testCommand(t, "app-1"), "app-1", rolledBack, t0.Add(2*time.Minute))`. r2 becomes retired, due t0+2m30s, and r1 becomes current with `stop_after` cleared. `DueStops(t0+20m)` must equal `[{r2, container-r2, t0+2m30s, Retired: true}]` exactly. The test also checks that no entry names r1.
- `./internal/deploy TestDeployDeclaresNoReleaseStatusText`: it runs `filepath.Glob("*.go")` in the package directory and skips `_test.go` files. It parses each file with `go/parser.ParseFile`, and `ast.Inspect` visits every `*ast.BasicLit` of kind `token.STRING`. It unquotes the literal with `strconv.Unquote` and reports `t.Errorf("%s: string literal %q is release status text owned by internal/agent/state; use state.DueStop.Retired", fset.Position(lit.Pos()), v)` for `candidate`, `current`, `previous`, `retired`, `failed` and `aborted`. It also fails when the glob finds no file. It needs no Docker. Today it fails on `stop.go:26`.
- The existing tests are unchanged and must pass: `TestStopPreviousStopsAndKeepsContainer`, `TestStopRetiredRemovesContainerAndPreDeployStep`, `TestStopNeverStopsRestoredRelease`, `TestStopRefusesUnmanagedContainer`, `TestReconcileCarriesOutDueStops` and `TestRecordStoppedClearsStopAfter`. `stopDueRow` never reads `.Status`, so it compiles as it is.

## Edge cases

- A previous row must still be stopped and kept, never removed: `Retired` is false, which `TestDueStopsMarksRetiredRows` and `TestDueStopRetiredAfterSwitch` check in state and `TestStopPreviousStopsAndKeepsContainer` checks in deploy.
- A retired row must still be stopped, removed and have its pre-deploy step cleared before `RecordStopped` clears its `container_id`, so no container is orphaned: covered by `TestStopRetiredRemovesContainerAndPreDeployStep` and `TestReconcileCarriesOutDueStops`.
- A release restored by a rollback is never listed, so it is never stopped: covered by `TestDueStopRetiredAfterRollback` and `TestStopNeverStopsRestoredRelease`.
- An unmanaged container is refused before any stop or removal (§3 B5). The `ownedBy` check and its order are untouched: covered by `TestStopRefusesUnmanagedContainer`.
- The zero value of `Retired` is false, which means "stop and keep". A `DueStop` built wrongly therefore keeps a container rather than removing one, and `RecordStopped` still decides from the stored status which columns it clears.
- The other status names are already absent from deploy's non-test code (checked by grep), so the new test passes once `stop.go:26` is gone.

## Risks

- Diff cap of 40: the non-test change is about 12 lines. Test files are not counted.
- `tests-kept` gate: each `Status:` line in `TestDueStopsListsPreviousAndRetiredPastStopAfter` is replaced one for one by a `Retired:` line on the same struct literal, and no case or assertion is dropped.
- The banned words in added lines are avoided, comments included.
- Allowed paths: `internal/agent/state/`, `internal/deploy/` and `testdata/tests.txt` only. No spec change, because behaviour is unchanged (`docs/specs/release.md`, "Switch" step 5). No schema, proto, policy or dependency change.
- Run `make fmt` after the field swap so the struct fields stay aligned the way gofumpt wants.
