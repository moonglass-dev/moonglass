---
verdict: needs-human
reason: "The owner must add FR6-6 with internal/agent/state/stops_test.go to ownerTestEdits in tools/pipeline/verify.go, or the tests-kept gate fails on the required Status-to-Retired edits."
---

# Plan for FR6-6

## What I found

The ticket requires changing the existing `TestDueStopsListsPreviousAndRetiredPastStopAfter` in `internal/agent/state/stops_test.go`. Its eight `Status: "retired"` / `Status: "previous"` expectations have to become `Retired: true` / `Retired: false`, because `DueStop.Status` is removed. Those lines exist on `main`.

The `tests-kept` gate (`tools/pipeline/verify.go` `removedTestLines`) runs `git diff --numstat` against main. It fails when any `_test.go` file that exists on main has removed lines, unless `ownerTestEdits[id][path]` allows it. `ownerTestEdits` has no FR6-6 entry. Editing those lines removes 8 lines from `stops_test.go`, so `scripts/verify check FR6-6` cannot pass. `tools/pipeline/` is a protected path outside this ticket's `allowed_paths`. Keeping a `Status` field would break the acceptance criterion "`state.DueStop` has no `Status` field". So no plan inside the ticket's paths can build it.

Needed: the owner adds this entry to `ownerTestEdits` in `tools/pipeline/verify.go`, then reruns the plan:

    "FR6-6": {
        "internal/agent/state/stops_test.go": true,
    },

## Plan once the allowance exists (for the next round)

- `internal/agent/state/stops.go`: in `DueStop`, replace `Status string` with `Retired bool`. `DueStops` scans `status` into a local `status string` and sets `stop.Retired = status == statusRetired`.
- `internal/agent/state/state.go:35`: change the comment to `// The command types the agent records; W3-4 adds build, and W5-5b adds stop_app and remove_app.`
- `internal/deploy/stop.go`: delete the `statusRetired` constant and its comment. Replace both `stop.Status == statusRetired` checks with `stop.Retired`.
- `internal/agent/state/stops_test.go`:
  - Move the existing expectations case for case, with nothing dropped.
  - Add `TestDueStopsMarksRetiredRows`: a table over r1 (retired) and r2 (previous), using `stopRecordSetup` and a `now` of t0+20m. It checks the full `DueStop` values.
  - Add `TestDueStopRetiredAfterSwitch`: three `switchApp` calls, then a `now` past both stops. r1 is `Retired` true and r2 is `Retired` false.
  - Add `TestDueStopRetiredAfterRollback`: two switches, then `mustRollback` and `RecordRollback`, with a `now` past the rolled-from row's +30s. r2 is listed with `Retired` true and r1 isn't listed.
- `internal/deploy/status_text_test.go`: add `TestDeployDeclaresNoReleaseStatusText`.
  - It reads `filepath.Glob("*.go")` and skips `_test.go` files.
  - For each file it runs `parser.ParseFile`, then `ast.Inspect` for `*ast.BasicLit` of kind `STRING`, and applies `strconv.Unquote`.
  - It fails with `fset.Position(lit.Pos())`, the file and line, on any of the six status texts.
- Run `make test-list`.
- Counted diff: about 10 lines, under the cap of 40.
- Security (threat model §3 B5): the `ownedBy` check and its order stay the same. A retired row is still stopped, removed and has its pre-deploy step container cleared before `RecordStopped`. A previous row is still stopped and kept.
