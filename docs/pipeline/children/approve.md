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
- A test changes or removes an existing test line. The owner allowed more exceptions, listed here: W2-8 may edit `internal/protocol/deploy_test.go`, and W2-12 may edit `internal/proxy/proxy_test.go` and `internal/proxy/memory_test.go` (2026-11-17); W3-7 may edit `internal/protocol/deploy_test.go`, changing the unknown type `11` to `15` in `TestDeployMessagesRejectUnknownType` (2026-11-18); W5-3a may edit `internal/policy/config_test.go`, so a present empty `[sources]` list allows nothing of that kind instead of being an error (decision D52, 2026-11-18); FR1-10 may edit `internal/store/projects_conformance_test.go` to check the error code through interface{ SQLState() string } instead of importing pgconn (decision ADR-0010, 2026-11-18); W2-3 may edit `internal/deploy/registry_test.go` so `pushTag` reads the digest from the status line as well as `aux` (the assertion does not change) (2026-11-18); W2-11b may edit `internal/store/releases_conformance_test.go` to list every `ReleaseStatus` case explicitly so `exhaustive` passes (2026-11-18); W5-5a may edit `internal/protocol/deploy_test.go` (15 to 2147483647 in `TestDeployMessagesRejectUnknownType`, because W5-5a assigns `STOP_APP` = 15); W2-11c may edit `internal/registry/registry_test.go` and `internal/registry/target_test.go`, the latter replacing `other == tt.kind` with `errors.Is(other, tt.kind)` so `errorlint` passes (2026-11-19); FR1-1 may edit `internal/agent/enroll_test.go`, `internal/agent/connect_test.go`, `internal/server/enroll_test.go`, `internal/server/owner_token_test.go`, `cmd/moonglass/enroll_test.go` and `cmd/moonglass/connect_test.go` for the new `Enroll` signature, the first line `Run` prints and the required `--server-fingerprint` flag (in `cmd/moonglass/connect_test.go`, only `TestAgentConnectRunsProxyPush` taking the fingerprint `newTestServer` returns and passing it as `--server-fingerprint`) (2026-11-19); W2-11d may edit `internal/api/routes_test.go` so `TestEveryMountedRouteIsOperationOrAllowlisted` calls each operation with valid inputs over seeded rows (a project, an environment and a server), because a request with missing required inputs is 400 before authentication (2026-11-20), and may edit `internal/registry/projects_test.go`, replacing `kind != tt.wantKind` with `!errors.Is(kind, tt.wantKind)` so `errorlint` passes (the assertion does not change) (2026-11-20); W2-10a may edit `internal/agent/state/state_test.go`, `predeploy_test.go`, `switch_test.go`, `rollback_test.go`, `stops_test.go` and `current_releases_test.go`, and `internal/deploy/helpers_test.go`, `deploy_test.go`, `predeploy_test.go`, `switch_test.go`, `rollback_test.go`, `domains_test.go`, `domain_owner_test.go`, `registry_test.go`, `stop_test.go`, `retain_test.go` and `run_test.go`, only to pass the request id, the `state.Command` value and the finished time to the changed `Executor` methods and `state` writes (no assertion changes) (2026-11-20); and, by a later owner decision the same day, W2-10a may change two assertions in `internal/agent/state/state_test.go`: `TestPackageImportsReleaseAndStoreOnly` permits exactly `moonglass.dev/moonglass/internal/protocol/agentv1` (every other path at or under `internal/protocol`, `internal/keys` and `internal/deploy` stays forbidden), and `TestOpenMigratesSpecSchema` wants the tables `[apps commands releases schema_migrations]`; and W2-7 may edit `test/deploy/load_test.go` so the load generator retries a request once, on a new connection, when its connection closes before any byte of an answer arrived and it did not time out, counts those retries, and `TestLoadGeneratorCountsEveryNon2xx` cuts its connection-error request after the first answer byte; it may add `TestLoadGeneratorRetriesOnceACutBeforeAnswer` (`docs/specs/deploy-suite.md`, "The load generator", 2026-11-20), and may edit `test/deploy/deploy_test.go` and `test/deploy/helpers_test.go` only to pass a request id to `Executor.Deploy`, `SwitchRelease` and `Rollback` (W2-10a's signatures; no assertion changes) (2026-11-20); and, by a later owner decision the same day, W2-7 may edit `test/deploy/deploy_test.go` so `TestRollbackUnderLoadIsUnderFiveSeconds` pauses 200 ms (`time.Sleep(200 * time.Millisecond)`) after `tenDeploys` and before it calls `Rollback`, so the `r10` mark has its answer under steady load (`docs/specs/deploy-suite.md`, "Rollback under 5 seconds"; no assertion changes); and W2-10b may edit `internal/registry/routes_test.go`, replacing the expected synopsis in `TestAgentConnectSynopsisNamesProxyFlags` with the one that names `--policy`, `internal/agent/connect_test.go`, only to pass an idle fake `Executor` in each `ConnectOptions` (no assertion changes), and `cmd/moonglass/connect_test.go`, only to isolate `TestAgentConnectRunsProxyPush` from other managed containers on the Engine (its `DOCKER_HOST` points at a proxy that answers `GET /v*/containers/json` with `[]` and forwards everything else; no assertion changes), and its locked tests `internal/agent/dispatch_test.go` and `internal/agent/resend_test.go`, whose switches on the protobuf `MessageType` `.golangci.yml` exempts from `exhaustive` (2026-11-20); and FR6-6 may edit `internal/agent/state/stops_test.go`, replacing the `Status: "retired"` / `Status: "previous"` expectations with `Retired: true` / `Retired: false` (the assertions do not change) (2026-11-20).
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
