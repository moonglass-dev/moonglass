# The ticket pipeline

One dispatcher thread in T3 Code takes `docs/plan/build-plan.md` and drives every ticket to a squash-merged PR on `main`. Models do the judgment: tickets, plans, tests, approval, code, review. Scripts do everything deterministic: state, locks, gates, PRs, merges. All state is in the repository, so any thread can be lost and rebuilt from it.

You are needed only for: a ticket in `needs-human` (a contract or ADR change, a new dependency, an unsettled security decision), a ticket in `blocked`, and the `kind: human` tickets in the plan.

## Who does what

```
dispatcher (Cursor, main checkout)
  ├─ scripts/ticket-status --json      which tickets are ready, in progress, mergeable
  ├─ scripts/ship merge <ID> --wait    CI green, catch up with main, CI green again, squash-merge
  └─ delegate_task × ≤3, isolation: worktree
       ticket worker (Cursor, one per ticket, own worktree, branch p/<ID>)
         loop: scripts/ticket-status next <ID>  →  run a script, or delegate one child
               child done → copy its result → scripts/verify record <ID>
         children, sharing the worker's worktree:
           ticket, plan, review, arch   Claude Opus, plan mode (cannot write; returns files)
           approve                      Cursor, plan mode (a second opinion on the tests)
           tests, build, fix, repair    OpenCode DeepSeek Flash, writes code
```

Exact provider instances and models: the table in `dispatcher.md` and `ticket-worker.md` (a test keeps the two identical). The dispatcher checks them with `orchestrator_capabilities` at the start of every session.

## Steps per ticket

| # | Step | By | Writes | Status after |
|---|---|---|---|---|
| 0 | ticket | Opus | `docs/tickets/<ID>.md`, from the plan line | todo |
| 1 | plan | Opus | `<ID>/plan.md`, verdict `ready` or `needs-human` | planned, or needs-human |
| 2 | tests | OpenCode | failing `_test.go` files | |
| 3 | approve | Cursor | `<ID>/approval.json`; `revise` sends tests back once | |
|   | lock | `scripts/verify lock` | `<ID>/tests.lock`: sha256 of every test file | tests-locked |
| 4 | build | OpenCode | the implementation | |
|   | check | `scripts/verify check` | `<ID>/check.json`: every gate; red sends build back once | |
|   | PR | `scripts/ship pr` | the pull request | built |
| 5 | review | Opus, or `scripts/verify skip-review` | `<ID>/review.json`; `changes` gets one fix round and a re-review | in-review |
| 6 | merge | dispatcher, `scripts/ship merge` | squash merge; `<ID>/merge.json` on a failure, then one repair round | merged |

Low-risk tickets with no named tests skip steps 2 and 3. `<ID>/` means `docs/tickets/<ID>/`. Result files live on the ticket branch and merge with it, so `main` keeps each ticket's full record.

The worker never decides what comes next: `scripts/ticket-status next` reads the ticket's files and returns the step, its inputs and its `clientRequestId` (`<ID>:<step>:<round>`). `scripts/verify record` checks a child's result before accepting it: the files it changed must be within that step's write scope (a plan-mode child gets nothing else in), and the result file must match its schema. A rejected result is discarded with the reason in `<ID>/rejected-<step>-<round>.txt`, which the next round reads.

## Gates (`scripts/verify check`)

`clean`, `ticket` (complete front matter and sections), `tests-locked` (locked files unchanged), `tests-kept` (no line of a test on `main` changed or removed), `paths` (only `allowed_paths`; protected paths only through an entry at or below their root), `size` (counted lines ≤ `max_diff_lines`), `words` (no TODO, FIXME, XXX, HACK, fallback, legacy), `dependencies` (every new module named in an accepted ADR), `named-tests` (each named test passes, not skipped), and `make-ci`. It also records the changed-line coverage from `coverage.out`.

## Review rule

Opus reviews every PR except one that meets all of these, which `scripts/verify skip-review` approves instead (recorded in `review.json` with `skipped: true` and the reason):

- `risk: low`, and every gate passed on the current commit;
- at most 200 counted lines;
- nothing in the trusted core and no protected path;
- if Go code changed, at least 80% of the changed executable lines are covered.

Review tickets (drift reviews, docs only) always skip model review: their output is itself a review.

## Budgets and stopping

- Step limits per ticket: ticket 2, plan 2, tests 2, approve 2, build 2, fix 1, repair 1, review 3, arch 2; and 3 worker starts. Going over blocks the ticket.
- `.pipeline/STOP` in the main checkout stops every script that changes state, in every worktree, with exit code 3. Delete it to resume.
- A blocked ticket stops only itself and the tickets that depend on it.
- One high-risk ticket in flight at a time; at most three workers.
- Every 9 merged tickets a drift review ticket (R-n) becomes ready: a fresh Opus reviews the whole repository and appends fix tickets to the plan. Each week ends with an end-to-end ticket (`Wn-E`).

## When a ticket needs you

`scripts/ticket-status` lists it under `attention`, with the reason. `docs/tickets/<ID>/BLOCKED.md` holds the reason and the status it stopped at. For `needs-human`, the reason is also in `<ID>/plan.md`.

1. Fix the cause on `main` in a normal PR: edit the ticket, the spec, the ADR, or the plan line.
2. Run `scripts/ticket-status set <ID> resume`. It returns the ticket to the status it stopped at (`todo` for needs-human, with the plan discarded), deletes `BLOCKED.md`, and resets the step budget.
3. The dispatcher's next turn restarts its worker.

To stop a ticket yourself: `scripts/ticket-status set <ID> blocked --reason "<why>"`.

## Commands

| Command | Does |
|---|---|
| `scripts/ticket-status` | Table of every ticket. `--json` for the full state |
| `scripts/ticket-status start <ID>` | Worker: check out `p/<ID>`, count a worker start |
| `scripts/ticket-status next <ID> [--peek]` | Worker: claim the next step (`--peek` shows it without claiming) |
| `scripts/ticket-status set <ID> <status>\|resume [--reason TEXT]` | Change a status by hand, or resume a stopped ticket |
| `scripts/verify record <ID>` | Worker: check, commit and push a child's result |
| `scripts/verify lock <ID>` | Hash the approved tests |
| `scripts/verify check <ID>` | Run every gate, write `check.json` |
| `scripts/verify skip-review <ID>` | Record a review skip when `check.json` allows one |
| `scripts/ship pr <ID>` | Push and open or reuse the pull request |
| `scripts/ship merge <ID> [--wait]` | Advance the merge; `--wait` repeats every 30 s while CI runs |

Every command prints JSON. Exit codes: 0 success, 1 a gate or result failed, 2 a usage or environment error, 3 the stop file exists. The logic is `tools/pipeline`, tested by `make test`.

## Branch protection assumed

- `main` requires a pull request and the `ci` check (`.github/workflows/ci.yml`, which runs `make ci`) to pass, and allows squash merge only.
- Branches need not be up to date before merging: `scripts/ship merge` merges `main` into the branch and waits for CI again itself. If the pull request conflicts, GitHub never starts the `pull_request` workflow, so a branch with no checks is merged with `main` immediately instead of waiting. `ci` also runs on every push to `p/*` (`.github/workflows/ci.yml`), which is what produces that check before the pull request can merge.
- Pull request bodies link to the ticket file and its result files with `https://github.com/<repo>/blob/p/<ID>/...` URLs. A relative path in the body is resolved against the pull request page and does not open the file.
- Nobody pushes to `p/*` branches except the pipeline. A push there makes the scripts' pushes fail loudly.
- `gh` on the machine running T3 is logged in with permission to push, open PRs and merge.

## Why it differs from the original brief

- Read-only children run in `plan` interaction mode with the inherited runtime mode, not the narrowest runtime mode. T3 runs narrow runtime modes as approval-required, which pauses for a human on every tool call. Plan mode cannot write files, so those children return their result files in their final message and the worker copies them; `scripts/verify record` then rejects anything outside the step's scope.
- Code-writing children share the worker's worktree instead of getting a second one. The worker already runs in its own worktree per ticket, and the scripts read the child's changes from it; a nested worktree would need its commits moved back.
- Before merging, the dispatcher merges `main` into the branch rather than rebasing, and repair rounds do the same. The squash merge flattens the branch anyway, and merging never rewrites commits that are already pushed.
- `ship merge --wait` waits for CI inside the script. Nothing wakes the dispatcher when CI finishes, and polling from the model would spend tokens on every check.
