---
name: implement
description: Implement one Moonglass brief in your own worktree - failing tests first, then code, make ci, one commit, open the PR. Use when a brief tells you to.
---

# Implement a brief

You are a worker. You were given a brief for one ticket. Do exactly the brief, in this worktree, and nothing else. Never ask questions: when the brief leaves a detail open, pick the option that matches `AGENTS.md` and the specs it names, and list it under "Decisions" in the PR body.

## 1. Branch

- New ticket: `git fetch -q origin main && git switch -c p/<ID> origin/main`. If that name exists, use `git switch -C p/<ID> origin/main`.
- Fix of an existing PR: `git fetch -q origin p/<ID> && git switch --detach FETCH_HEAD`. If the brief says to merge `main`: `git fetch -q origin main && git merge --no-edit origin/main`, resolve each conflict by keeping both sides' intent, and regenerate `testdata/tests.txt` with `make test-list` rather than editing it by hand.

## 2. Tests first

Write the acceptance tests the brief names, table-driven, with failure messages that show the expected and actual values. Run each with `make test-one PKG=./<pkg> NAME=<TestName>` and see it fail for the right reason (an assertion, or a missing function), not a typo. Keep that failing output for the PR body.

Integration tests use a real Docker Engine and skip without one, except under `CI`. Postgres tests read `MOONGLASS_TEST_POSTGRES`. Ports come from `:0`, files from `t.TempDir()`, Docker resources are labelled per run and removed.

## 3. Code

Write the least code that makes the tests pass and matches the brief's design. Follow `AGENTS.md`: one way to do each thing, no compatibility code, errors wrapped with context, no new dependency, no edits to generated code (`make generate`, `make proto` rewrite it), no banned words (TODO, FIXME, XXX, HACK, fallback, legacy) in added lines outside docs. Update the specs the brief names in the same change. Delete code the change makes unused.

## 4. Check

1. `make fmt`, then `make test-list` if you added, renamed or removed a test.
2. `make ci`. Every line must read `ok`. When a test unrelated to your change fails, run it once more with `make test-one`. If it passes, note it as flaky in the PR body. If it fails again, stop and report it. Never skip, weaken or delete a test to get green.

## 5. Commit and open the PR

1. One commit, author and committer `Moonglass Team <team@moonglass.dev>`, message `<ID>: <title>`, no trailers:
   `git add -A && git -c user.name='Moonglass Team' -c user.email='team@moonglass.dev' commit -q -m '<ID>: <title>'`
   A fix adds one more commit the same way, with the message `<ID>: <what the fix does>`.
2. Write the PR body to a file outside the repository (`f=$(mktemp)`), with these sections: `## What changed` (three lines at most), `## Tests` (each test and what it asserts, plus the failing output from step 2 in a short block), `## Decisions` (each default you chose, and why), `## Specs` (what changed, or "none"), `## Risk` (trusted core touched: yes or no, which paths).
3. `scripts/plan pr <ID> --body-file "$f"`, adding `--title` and `--risk` when the brief gives them. It pushes `HEAD` to `p/<ID>` and opens or updates the pull request. Never push to `main`, merge, or enable auto-merge yourself.

## 6. Report

Your final message, at most 12 lines: `PR <url> · head <sha> · make ci ok`, then the files changed, the decisions you made, and anything you are unsure of. When you cannot finish, start the message with `BLOCKED: <reason>` and say what you would recommend. Do not write any other file to explain it.
