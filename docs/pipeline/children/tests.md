# Step 2: tests

Runs as OpenCode in the ticket's worktree. You write files directly.

## Inputs

Read in full:

- `AGENTS.md`, especially the testing rules.
- `docs/tickets/<ID>.md`
- `docs/tickets/<ID>/plan.md`
- Every file in the ticket's `reads`, and the existing tests in the packages you will test.
- `docs/tickets/<ID>/approval.json`, when your task lists it: the reviewer's blockers from the last round. Fix every one.
- `docs/tickets/<ID>/rejected-tests-<n>.txt`, when your task lists it. Fix every problem it names.

## Do

Write the failing acceptance tests, before any implementation exists.

- Write every test named in the ticket's `tests`, with exactly that name, in that package. Add the plan's other tests.
- Each test asserts the behaviour in the acceptance criteria, through the package's public surface, with failure messages that show the expected and the actual value. Table-driven where there are cases.
- The tests must fail now, because the behaviour is missing. A compile error from a function or type the ticket adds counts as failing.
- Tests are safe in two worktrees at once: ports from `:0`, `t.TempDir()`, Docker resources labelled per run and removed. No network beyond localhost. No sleeps for timing.
- Run `make fmt`.

## Rules

- Write only `_test.go` files inside `allowed_paths`, and fixtures under `testdata/` directories.
- Never change or remove a line of an existing test. The owner allowed more exceptions, listed here: W2-8 may edit `internal/protocol/deploy_test.go`, and W2-12 may edit `internal/proxy/proxy_test.go` and `internal/proxy/memory_test.go` (2026-11-17); W3-7 may edit `internal/protocol/deploy_test.go`, changing the unknown type `11` to `15` in `TestDeployMessagesRejectUnknownType` (2026-11-18); W5-3a may edit `internal/policy/config_test.go`, so a present empty `[sources]` list allows nothing of that kind instead of being an error (decision D52, 2026-11-18); FR1-10 may edit `internal/store/projects_conformance_test.go` to check the error code through interface{ SQLState() string } instead of importing pgconn (decision ADR-0010, 2026-11-18); W2-3 may edit `internal/deploy/registry_test.go` so `pushTag` reads the digest from the status line as well as `aux` (the assertion does not change) (2026-11-18); W2-11b may edit `internal/store/releases_conformance_test.go` to list every `ReleaseStatus` case explicitly so `exhaustive` passes (2026-11-18); W5-5a may edit `internal/protocol/deploy_test.go` (15 to 2147483647 in `TestDeployMessagesRejectUnknownType`, because W5-5a assigns `STOP_APP` = 15); W2-11c may edit `internal/registry/registry_test.go` and `internal/registry/target_test.go`, the latter replacing `other == tt.kind` with `errors.Is(other, tt.kind)` so `errorlint` passes (2026-11-19); FR1-1 may edit `internal/agent/enroll_test.go`, `internal/agent/connect_test.go`, `internal/server/enroll_test.go`, `internal/server/owner_token_test.go`, `cmd/moonglass/enroll_test.go` and `cmd/moonglass/connect_test.go` for the new `Enroll` signature, the first line `Run` prints and the required `--server-fingerprint` flag (in `cmd/moonglass/connect_test.go`, only `TestAgentConnectRunsProxyPush` taking the fingerprint `newTestServer` returns and passing it as `--server-fingerprint`) (2026-11-19); W2-11d may edit `internal/api/routes_test.go` so `TestEveryMountedRouteIsOperationOrAllowlisted` calls each operation with valid inputs over seeded rows (a project, an environment and a server), because a request with missing required inputs is 400 before authentication (2026-11-20), and may edit `internal/registry/projects_test.go`, replacing `kind != tt.wantKind` with `!errors.Is(kind, tt.wantKind)` so `errorlint` passes (the assertion does not change) (2026-11-20); W2-10a may edit `internal/agent/state/state_test.go`, `predeploy_test.go`, `switch_test.go`, `rollback_test.go`, `stops_test.go` and `current_releases_test.go`, and `internal/deploy/helpers_test.go`, `deploy_test.go`, `predeploy_test.go`, `switch_test.go`, `rollback_test.go`, `domains_test.go`, `domain_owner_test.go`, `registry_test.go`, `stop_test.go`, `retain_test.go` and `run_test.go`, only to pass the request id, the `state.Command` value and the finished time to the changed `Executor` methods and `state` writes (no assertion changes) (2026-11-20); and, by a later owner decision the same day, W2-10a may change two assertions in `internal/agent/state/state_test.go`: `TestPackageImportsReleaseAndStoreOnly` permits exactly `moonglass.dev/moonglass/internal/protocol/agentv1` (every other path at or under `internal/protocol`, `internal/keys` and `internal/deploy` stays forbidden), and `TestOpenMigratesSpecSchema` wants the tables `[apps commands releases schema_migrations]`; and W2-7 may edit `test/deploy/load_test.go` so the load generator retries a request once, on a new connection, when its connection closes before any byte of an answer arrived and it did not time out, counts those retries, and `TestLoadGeneratorCountsEveryNon2xx` cuts its connection-error request after the first answer byte; it may add `TestLoadGeneratorRetriesOnceACutBeforeAnswer` (`docs/specs/deploy-suite.md`, "The load generator", 2026-11-20).
- Never skip a test, hard-code its expected output to match a guess of the implementation, or make it pass trivially.
- Do not write implementation code, do not run `make test-list`, and do not commit or push. `scripts/verify record` checks your files and commits them.

## Output

End with one summary line: the tests you wrote, and how each fails now.
