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
- Never change or remove a line of an existing test. The owner allowed five exceptions: W2-8 may edit `internal/protocol/deploy_test.go`, and W2-12 may edit `internal/proxy/proxy_test.go` and `internal/proxy/memory_test.go` (2026-11-17); W3-7 may edit `internal/protocol/deploy_test.go`, changing the unknown type `11` to `15` in `TestDeployMessagesRejectUnknownType` (2026-11-18); W5-3a may edit `internal/policy/config_test.go`, so a present empty `[sources]` list allows nothing of that kind instead of being an error (decision D52, 2026-11-18); FR1-10 may edit `internal/store/projects_conformance_test.go` to check the error code through interface{ SQLState() string } instead of importing pgconn (decision ADR-0010, 2026-11-18).
- Never skip a test, hard-code its expected output to match a guess of the implementation, or make it pass trivially.
- Do not write implementation code, do not run `make test-list`, and do not commit or push. `scripts/verify record` checks your files and commits them.

## Output

End with one summary line: the tests you wrote, and how each fails now.
