# Step 4: build

Runs as OpenCode in the ticket's worktree. You write files directly.

## Inputs

Read in full:

- `AGENTS.md`
- `docs/tickets/<ID>.md`
- `docs/tickets/<ID>/plan.md`
- `docs/tickets/<ID>/tests.lock`: the test files you must not change.
- Every file in the ticket's `reads`, and the source of every path in `allowed_paths`.
- `docs/tickets/<ID>/check.json`, when your task lists it: the gates that failed after your last round. Fix every failed gate.
- `docs/tickets/<ID>/rejected-build-<n>.txt`, when your task lists it. Fix every problem it names.

## Do

Implement the ticket until every named test passes and `make ci` is green.

1. Follow the plan. Only what the ticket needs.
2. If the ticket changes registry operations, run `make generate`.
3. Update the spec in `allowed_paths` when behaviour changed.
4. Run `make fmt`, then `make test-list`, then `make ci`. Repeat until `make ci` is green.

`scripts/verify check` runs after you and fails the round if any of these break:

- `tests-locked`: a file in `tests.lock` changed. `tests-kept`: a line of a test that exists on `main` was changed or removed.
- `paths`: a file outside `allowed_paths` changed (the ticket's own docs and `testdata/tests.txt` are always allowed).
- `size`: counted lines (added plus removed, outside tests, docs, Markdown, generated files and `go.sum`) exceed `max_diff_lines`.
- `words`: an added line outside docs contains TODO, FIXME, XXX, HACK, fallback or legacy, as a word in any case.
- `dependencies`: a module was added to `go.mod` that no accepted ADR names in full.
- `named-tests`: a named test did not pass, or skipped.
- `make-ci`: `make ci` failed.

## Rules

- No placeholders, no fallback paths, no code for later tickets. Errors are wrapped with context and say what to do.
- Never change a locked test, and never weaken, skip or delete any test. You may add new tests. The owner allowed three exceptions: W2-8 may edit `internal/protocol/deploy_test.go`, and W2-12 may edit `internal/proxy/proxy_test.go` and `internal/proxy/memory_test.go` (2026-11-17); W3-7 may edit `internal/protocol/deploy_test.go`, changing the unknown type `11` to `15` in `TestDeployMessagesRejectUnknownType` (2026-11-18).
- Do not edit `docs/tickets/<ID>.md` or anything under `docs/tickets/<ID>/`.
- Do not push, rebase or reset. Committing is optional: `scripts/verify record` commits what is left.
- If the ticket cannot be built as written (a contract or ADR change, a missing spec, a test that contradicts the spec), write `docs/tickets/<ID>/BLOCKED.md` with the reason on its first line and stop.

## Output

End with one summary line: what you built, and the result of the last `make ci`.
