# Steps fix and repair

Runs as OpenCode in the ticket's worktree. You write files directly. The worker uses this prompt for two steps:

- `fix`: the review asked for changes. Your task lists `docs/tickets/<ID>/review.json`.
- `repair`: the merge failed. Your task lists `docs/tickets/<ID>/merge.json`.

## Inputs

Read in full:

- `AGENTS.md`
- `docs/tickets/<ID>.md` and `docs/tickets/<ID>/plan.md`
- `docs/tickets/<ID>/tests.lock`: the test files you must not change.
- The file your task lists: `review.json` or `merge.json`.
- The ticket's change so far: `git diff origin/main...HEAD -- . ':!docs/tickets/<ID>/'`
- `docs/tickets/<ID>/rejected-<step>-<n>.txt`, when your task lists it. Fix every problem it names.

## Do: fix

Fix every entry in `blockers` and `majors`. Where a test can show the defect, add a test that fails first, then fix the code. Leave `minors` alone. Change nothing else.

If you are certain a finding is not a defect, leave that code unchanged and say why in your summary line. The next review round checks it.

## Do: repair

`merge.json` has `result` and `detail`:

- `ci-red`: CI failed on GitHub. `detail` names the failed checks. Run `make ci`, find the failure and fix it.
- `conflict`: `main` moved and conflicts with this branch. Run `git fetch origin` and `git merge origin/main`. Resolve each conflict so that both this ticket and the work already on `main` keep their behaviour. For `testdata/tests.txt` run `make test-list`; for `internal/api/openapi.json` and `internal/cli/generated.go` run `make generate`. Commit the merge. Never rebase.

## Then, for both

Run `make fmt`, `make test-list` and `make ci` until `make ci` is green.

## Rules

- Never change a locked test, and never weaken, skip or delete any test.
- Stay inside `allowed_paths`. Do not edit `docs/tickets/<ID>.md` or anything under `docs/tickets/<ID>/`.
- The `scripts/verify check` gates in `docs/pipeline/children/build.md` apply.
- Do not push, rebase or reset. `scripts/verify record` commits what is left and pushes.
- If the fix needs a contract or ADR change, write `docs/tickets/<ID>/BLOCKED.md` with the reason on its first line and stop.

## Output

End with one summary line: each finding or failure and what you did about it, and the result of the last `make ci`.
