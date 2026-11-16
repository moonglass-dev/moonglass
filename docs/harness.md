# Ticket harness

`scripts/ticket` runs one ticket at a time through its pipeline, one fresh Claude session per step. It needs `git`, `jq`, `perl`, `gh`, `claude`, and `golangci-lint` for `make ci`.

## Run a ticket

1. Copy `docs/tickets/TEMPLATE.md` to `docs/tickets/<ID>.md` and fill it in.
2. Dry run, no Claude calls, nothing written: `scripts/ticket run <ID> --dry-run`
3. Go: `scripts/ticket run <ID>`. It stops at the first state that needs you. Run it again to continue.

Every command prints one JSON line `{id, state, needs_human, reason?}` and exits 0 (progress), 1 (needs you) or 2 (usage error). Logs, prompts, plans, reviews and `usage.log` (cost and turns per call) are in `.ticket/<ID>/`. The worktree is `../moonglass.worktrees/<ID>`, on branch `t/<ID>-<slug>`, created from `main`; the base SHA is in `.ticket/<ID>/state.json`.

| Command | Does |
|---|---|
| `start` | Creates the worktree and records the base SHA |
| `tests` | Core only: plan (read-only), then failing tests, committed and locked |
| `impl` | Implements, then runs `verify`. One automatic retry with the failure output |
| `verify` | Runs the gates |
| `pr` | Pushes and opens the PR |
| `review` | Fresh read-only session. Opus for core, Sonnet for standard. Posts findings to the PR |
| `fix` | One round on blocker and major findings, then `verify` and push |
| `run [--approve]` | Runs the next steps. `--approve` accepts the core tests |
| `status` | Prints the current state |
| `reset --yes` | Deletes the worktree, branch and `.ticket/<ID>` |

Pipelines: core = tests, you approve, impl, verify, pr, review (Opus), fix, you merge. Standard = impl, verify, pr, review (Sonnet), fix, merge when CI is green. Light = impl, verify, pr, merge batch. The harness never merges.

## States

| State | Meaning | You |
|---|---|---|
| `started` | Worktree ready | `run` |
| `plan_blocked` | The plan needs a decision (contract change, new dependency, blocking question) or the tests stage failed | Read `plan.json`, `verify.txt`, fix the ticket, `run` |
| `tests_ready` | Core tests written, red on base, locked | Read them, then `run --approve` |
| `impl_blocked` | The agent stopped, or a call failed or timed out | Read `reason`; sharpen the ticket; `impl` |
| `verify_failed` | A gate failed, also after the retry | Read `verify.txt`; fix in the worktree and commit, or `impl`; then `verify` |
| `verified` | All gates passed | `run` |
| `pr_open` / `pr_blocked` | PR open / push or `gh` failed | `run` / fix, then `pr` |
| `review_findings` | Blocker or major findings exist | `run` runs the one fix round |
| `review_blocked` / `fix_failed` | Review call failed / fix failed or its round is used up | Read `review.md`; fix by hand, `verify` |
| `reviewed` | No blocker or major findings | `run` |
| `ready` | Everything passed | Review at the ticket's level, check GitHub CI, merge |

If an implementer drifts twice, `reset` and sharpen the ticket.

## Verify gates

Run on the worktree, cheapest first, stopping at the first failure. Output is the failed gate, test names and at most 60 lines, in `verify.txt`.

1. `clean`: everything committed, at least one commit past the base.
2. `paths`: no protected path (`.github`, `.claude`, `Makefile`, `.golangci.yml`, `AGENTS.md`, `CLAUDE.md`, `docs/adr`, `*.proto`, `*.pb.go`, `scripts/ticket`, `schemas`); `go.mod` and `go.sum` unchanged; every changed path inside the ticket's `packages`, its `spec`, or `testdata/tests.txt`. The ticket's `allow` list exempts named paths.
3. `size`: non-test diff under 800 lines. Generated `*.pb.go` is excluded, as are `*_test.go`, `testdata/`, and `go.sum`.
4. `words`: no TODO, FIXME, fallback or legacy in added lines (Markdown excluded).
5. `tests-immutable`: no changed or deleted lines in existing `*_test.go`; core's locked test files unchanged.
6. `named-tests`: each test in the ticket's `tests` passes and is not skipped.
7. `new-tests-red`: every added `Test` or `Fuzz` function fails on the base commit (a compile failure counts). Core and standard tickets must add at least one.
8. `ci`: a fresh `make ci` (override with `TICKET_CI_CMD`).

## Settings and limits

- Headless calls use `--setting-sources project`, `--strict-mcp-config`, `--permission-mode dontAsk` and `--no-session-persistence`, so only `.claude/settings.json` applies and anything not allowed there is denied. Planning has Read, Grep and Glob only; review adds `git diff` and `git log`.
- Sonnet everywhere; Opus only for core review. Per-call dollar caps and timeouts are `TICKET_BUDGET_*` and `TICKET_SECS_*` in the script. `gtimeout` is used when installed, otherwise a perl fallback.
- Agents run on your Mac as you. Deny rules constrain the agent's tool calls, not code it writes: `go test` and `make` run that code with your privileges. The harness runs everything under a scrubbed environment (no tokens), but a Go test can still read your files. Read the test diff before approving, as the build plan already says.
- Structured output: `notes` is meant to be at most 400 characters; the schema can only ask for it in the description.
- Test the harness with `make test-harness`.
