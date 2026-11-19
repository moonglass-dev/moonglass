---
verdict: ready
reason: ""
---

# Plan for FR2-5

## Files to change

- `docs/specs/auth.md`, line 189, Storage section, the `session_hashes` bullet. Replace only the parenthetical on `revoked_at`:
  - Before: `` - `session_hashes`: `hash` (SHA-256 hex of the bearer), `id`, `user_id`, `expires_at`, `revoked_at` (empty when live). ``
  - After: `` - `session_hashes`: `hash` (SHA-256 hex of the bearer), `id`, `user_id`, `expires_at`, `revoked_at` (NULL while the session is live; set once, to the revocation time, when the session is revoked). ``
  - `hash`, `id`, `user_id`, `expires_at` and every other line in the file stay byte-for-byte the same. The diff is 1 line removed and 1 added, within `max_diff_lines: 3`.

## Tests to write

- None. The ticket names no tests (`tests: []`), and this is a docs-only change. `testdata/tests.txt` does not change.

## Edge cases

- Agreement with the schema: both `internal/store/migrations/sqlite/0005_sessions.sql` and `internal/store/migrations/pg/0005_sessions.sql` declare `revoked_at TEXT` with no `NOT NULL` and no default, so a new row holds NULL. This matches "NULL while the session is live".
- Agreement with the code: `internal/store/sessions.go` reads a session only `WHERE ... revoked_at IS NULL AND expires_at > $2`, and `RevokeSession` writes `revoked_at = COALESCE(revoked_at, $1)`, so the first revocation time is kept. This matches "set once, to the revocation time".
- Threat-model revocation rule (a revoked or expired session is refused, revocation is recorded once): the new wording states the same rule and does not weaken it.
- Banned words: none are added, and the rule covers only lines outside docs anyway.

## Risks

- Changing more than the parenthetical, such as rewording other columns or the W4-1a/W4-1d paragraph above it, would fail the "no other line changes" criterion. Edit only that one line, and check with `git diff main -- docs/specs/auth.md`.
- `docs/specs/storage.md` line 29 says "a `revoked_at` marker". That is accurate and outside `allowed_paths`, so leave it alone.
- Scope gate: `git diff --stat main` must list only `docs/specs/auth.md` and `docs/tickets/FR2-5*`. Run `make ci` and `scripts/verify check FR2-5`. No Go code changes, so CI risk is minimal.
