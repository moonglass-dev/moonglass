# Step 3: approve

Runs as Cursor in plan mode, separately from whoever wrote the tests: you cannot write files. Your result goes in your final message.

## Inputs

Read in full:

- `AGENTS.md`, especially the testing rules.
- `docs/tickets/<ID>.md`
- `docs/tickets/<ID>/plan.md`
- The test changes: `git diff origin/main...HEAD -- '*_test.go' '**/testdata/**'`
- `docs/tickets/<ID>/approval.json`, if it exists: your previous round's blockers.

## Do

Check the tests against the ticket. Do not judge the implementation; there is none yet.

A blocker is any of:

- A test named in the ticket's `tests` is missing, or has a different name or package.
- An acceptance criterion has no test that would fail if the behaviour were wrong.
- A test passes trivially, asserts nothing, skips, or hard-codes an expected value that only matches one guessed implementation.
- A test changes or removes an existing test line. The owner allowed more exceptions, listed here: W2-8 may edit `internal/protocol/deploy_test.go`, and W2-12 may edit `internal/proxy/proxy_test.go` and `internal/proxy/memory_test.go` (2026-11-17); W3-7 may edit `internal/protocol/deploy_test.go`, changing the unknown type `11` to `15` in `TestDeployMessagesRejectUnknownType` (2026-11-18); W5-3a may edit `internal/policy/config_test.go`, so a present empty `[sources]` list allows nothing of that kind instead of being an error (decision D52, 2026-11-18); FR1-10 may edit `internal/store/projects_conformance_test.go` to check the error code through interface{ SQLState() string } instead of importing pgconn (decision ADR-0010, 2026-11-18); W2-3 may edit `internal/deploy/registry_test.go` so `pushTag` reads the digest from the status line as well as `aux` (the assertion does not change) (2026-11-18); W2-11b may edit `internal/store/releases_conformance_test.go` to list every `ReleaseStatus` case explicitly so `exhaustive` passes (2026-11-18); W5-5a may edit `internal/protocol/deploy_test.go` (15 to 2147483647 in `TestDeployMessagesRejectUnknownType`, because W5-5a assigns `STOP_APP` = 15); W2-11c may edit `internal/registry/registry_test.go` and `internal/registry/target_test.go`, the latter replacing `other == tt.kind` with `errors.Is(other, tt.kind)` so `errorlint` passes (2026-11-19); FR1-1 may edit `internal/agent/enroll_test.go`, `internal/agent/connect_test.go`, `internal/server/enroll_test.go`, `internal/server/owner_token_test.go` and `cmd/moonglass/enroll_test.go` for the new `Enroll` signature, the first line `Run` prints and the required `--server-fingerprint` flag (2026-11-19).
- A test is unsafe in two worktrees at once (fixed ports, shared paths, unlabelled Docker resources), needs the network, or depends on timing.
- A test file is outside `allowed_paths` and `testdata/`.

Anything else, such as style or naming, goes in `notes`, never in `blockers`.

## Output

One summary line, then:

````
FILE: docs/tickets/<ID>/approval.json
`````text
{
  "round": <your round number>,
  "verdict": "approve",
  "blockers": [],
  "notes": "one short paragraph"
}
`````
````

`verdict` is `approve` when `blockers` is empty, and `revise` otherwise. Each blocker is one string: the file, the test, what is wrong and what would fix it. `round` must be the round in your task.
