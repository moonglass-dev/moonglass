---
verdict: ready
reason: ""
---

# Plan for FR6-5

## Files to change

- `docs/specs/registry.md`, section "Audit" (currently lines 147 and 149). Only these two lines change.
  - Line 147, the sentence "Each event names the principal kind (`human` or `agent`), user id, token id and operation." becomes:
    "Each event names the principal kind (`human` or `agent`), user id, token id, session id, step-up id and operation. `session_id` and `step_up_id` are empty when they don't apply; `docs/specs/auth.md` gives the session and step-up that set them."
    Everything else on the line stays exactly as it is: the three outcomes `allowed` / `denied` (role, scope or pending approval) / `error` (the handler failed), "after the decision", the unauthenticated-request sentence, and "`reason` never holds a token or handler error text."
  - Line 149, the array inside `hash = SHA-256(JSON [...])` becomes exactly
    `[prev_hash, org_id, seq, id, at, principal_kind, user_id, token_id, session_id, step_up_id, operation, outcome, reason]`.
    ", hex encoded. Events start at `seq` 1 …" and the `VerifyAudit` sentence stay unchanged.
- Nothing else: line 151 (W5-1b / W5-5 paragraph), the "Tokens" HTTPS sentence (FR6-1) and the "Ed25519 challenge" text (FR1-7) are untouched. No code, test, `docs/specs/auth.md` or `testdata/tests.txt` change.

## Tests to write

- None. The ticket names no tests (`tests: []`) and changes only a spec. Checks the build step runs instead:
  - `grep -c 'token_id, operation, outcome, reason' docs/specs/registry.md` prints 0 (the 11-field array is gone).
  - `grep -c 'token_id, session_id, step_up_id, operation, outcome, reason' docs/specs/registry.md` prints 1.
  - The new array equals the argument order of `eventHash` in `internal/registry/audit.go:19` (`PrevHash, OrgID, Seq, ID, At, PrincipalKind, UserID, TokenID, SessionID, StepUpID, Operation, Outcome, Reason`) and the array in `docs/specs/auth.md:200`.
  - `git diff --stat main` lists only `docs/specs/registry.md` and `docs/tickets/FR6-5*`.
  - `make ci` and `scripts/verify check FR6-5` pass.

## Edge cases

- Field order: `session_id` and `step_up_id` go after `token_id` and before `operation`, as in the code. Copy the array from auth.md line 200 rather than retyping it.
- Naming: in prose, use "session id" and "step-up id" like the existing "user id, token id", and the backticked column names `session_id` / `step_up_id` in the emptiness sentence so the acceptance criterion ("names `session_id` and `step_up_id` as event fields that are empty when they don't apply") is met literally.
- Banned words (TODO, FIXME, XXX, HACK, fallback, legacy): the rule covers added lines outside docs, and the new text uses none of them anyway.
- Size gate: `countsTowardSize` in `tools/pipeline/verify.go` excludes `.md` and `docs/`, so the diff counts 0 lines against `max_diff_lines: 3`.

## Risks

- Claiming more than the threat model supports: the new text adds only field names and where they come from. It does not describe tamper resistance beyond what the "Audit" paragraph already says and threat-model §2 "Audit trail" supports.
- Scope creep: a reviewer will reject edits to line 151 or other R-6 findings. Change exactly two lines; check with `git diff main -- docs/specs/registry.md` that there are two `-` and two `+` lines.
- Contradicting auth.md: the emptiness wording mirrors auth.md "Storage" ("empty when they don't apply") so the two specs read the same.
