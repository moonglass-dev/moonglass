---
verdict: ready
reason: ""
---

# Plan for W4-1a

The tables, columns and methods are the owner-approved contract in `docs/specs/auth.md` ("Storage", approved 2026-11-18), so no contract decision is open. Every method is plain SQL in the common subset. No new module, no ADR change, no registry change.

## Before building

The branch is three commits behind `main`. `main` has `internal/store/migrations/{pg,sqlite}/0004_releases.sql`, `internal/store/releases.go` and 88 more lines in `store.go`. Merge `origin/main` first. The new migration is therefore `0005_sessions.sql` on both backends.

## Files to change

- `internal/store/migrations/pg/0005_sessions.sql` and `internal/store/migrations/sqlite/0005_sessions.sql` (new, identical text):
  ```sql
  CREATE TABLE session_hashes (
  	hash TEXT NOT NULL PRIMARY KEY,
  	id TEXT NOT NULL UNIQUE,
  	user_id TEXT NOT NULL,
  	expires_at TEXT NOT NULL,
  	revoked_at TEXT
  );

  ALTER TABLE audit_events ADD COLUMN session_id TEXT NOT NULL DEFAULT '';
  ALTER TABLE audit_events ADD COLUMN step_up_id TEXT NOT NULL DEFAULT '';
  ```
  There is no RLS, no policy and no grant on Postgres. `pg.Migrate` already grants on every table. No applied migration is edited.
- `internal/store/lookup.go`: append `"session_hashes"` to `LookupTables`.
- `internal/store/store.go`:
  - `type Session struct { ID, UserID, ExpiresAt string }`, with a doc comment saying it has no hash field and the bearer is stored only as `HashToken`.
  - `AuditEvent` gains `SessionID string` and `StepUpID string`, after `TokenID`.
  - `Store` gains, each with a doc comment:
    - `AddSession(ctx context.Context, userID, sessionHash string, expires time.Time) (Session, error)`
    - `SessionByHash(ctx context.Context, sessionHash string, now time.Time) (Session, error)`
    - `RevokeSession(ctx context.Context, userID, sessionID string, now time.Time) error`
- `internal/store/sessions.go` (new). These are methods on `*SQLStore` that use `s.db` and `s.q` directly, with no `inOrg`, like `AddEnrolmentToken` and `UseEnrolmentToken`:
  - `AddSession`: `id, err := NewID()`. Then `INSERT INTO session_hashes (hash, id, user_id, expires_at) VALUES ($1, $2, $3, $4)` with `formatTime(expires)`. An error goes through `s.dupOr(err, "insert session")`, which gives `ErrDuplicate` for a repeated hash. It returns `Session{ID: id, UserID: userID, ExpiresAt: formatTime(expires)}`.
  - `SessionByHash`: `SELECT id, user_id, expires_at FROM session_hashes WHERE hash = $1 AND revoked_at IS NULL AND expires_at > $2` with `formatTime(now)`. This is the same lexical RFC3339-UTC comparison `UseEnrolmentToken` uses. `sql.ErrNoRows` becomes `fmt.Errorf("session by hash: %w", ErrNotFound)`. Any other error is wrapped as `select session hash`.
  - `RevokeSession`: one statement, `UPDATE session_hashes SET revoked_at = COALESCE(revoked_at, $1) WHERE id = $2 AND user_id = $3` with `formatTime(now)`. `COALESCE` keeps the first `revoked_at`. The method reads `RowsAffected()`. 0 means `fmt.Errorf("revoke session %s: %w", sessionID, ErrNotFound)`: no session with that ID belongs to that user. Otherwise it returns nil. Postgres and SQLite both count matched rows, so a second revoke returns nil.
- `internal/store/tokens.go`:
  - `appendAuditOnce`: the column list ends `..., prev_hash, hash, session_id, step_up_id`. Values run `$1`…`$14` in order, and the arguments gain `ev.SessionID, ev.StepUpID`.
  - `AuditEvents`: the `SELECT` ends `..., prev_hash, hash, session_id, step_up_id`, and the `Scan` gains `&ev.SessionID, &ev.StepUpID`.
  - Nothing else changes: not the head read, the order or the retry.
- `docs/specs/storage.md`:
  - Under "Lookup tables", add a `session_hashes` bullet: the lookup key is the SHA-256 of a session bearer; it maps to the session `id` and `user_id`, with `expires_at` and a `revoked_at` marker; it has no RLS (`docs/specs/auth.md`).
  - On the `audit_events` line, add "W4-1a adds `session_id` and `step_up_id`, empty when they don't apply (`docs/specs/auth.md`)."
- `docs/specs/auth.md`: no change. "Storage" matches this design. "`revoked_at` (empty when live)" is implemented as NULL, like `token_hashes.revoked_at`. The ticket's scope names a null column, so this is not a contradiction.
- `internal/store/schema_test.go`: add one line after `want := []string{...}` in `TestLookupTablesMatchSchema`: `want = append(want, "session_hashes")`. No line is removed (see Risks).
- `testdata/tests.txt`: regenerate it with `make test-list`.

## Tests to write

All tests go in the new file `internal/store/sessions_conformance_test.go` (`package store_test`). It reuses `newSQLite`, `newPGEnv`, `openApp`, `rawDB`, `sqliteSchema`, `pgSchema`, `dumpAll` and `randHex` from `helpers_test.go`. Write the tests first and see them fail to compile or fail before writing any code.

- `./internal/store TestSessionsConformanceSQLite`: `runSessionsConformance(t, s)` on `newSQLite`.
- `./internal/store TestSessionsConformancePostgres`: `runSessionsConformance(t, newPGEnv(t, true).openApp(t))`, which runs as the application role.
- `runSessionsConformance(t, s store.Store)`: `now := time.Now().UTC().Truncate(time.Second)`, `exp := now.Add(time.Hour)`. Each user comes from `UserForIdentity(ctx, "sess:"+randHex)`. Each hash is `store.HashToken("mg_"+randHex(t, 16))`. A table of subtests:
  - "add and look up": the ID is 32 lowercase hex characters, the `UserID` matches, `ExpiresAt == exp.Format(time.RFC3339)`, and `SessionByHash(hash, now)` equals the returned `Session`.
  - "unknown hash": `SessionByHash` gives `ErrNotFound`.
  - "expiry": `SessionByHash(hash, exp)` and `SessionByHash(hash, exp.Add(time.Second))` both give `ErrNotFound`. `exp.Add(-time.Second)` returns the session.
  - "revoke by another user": `ErrNotFound`, and `SessionByHash` still returns the session.
  - "revoke unknown id": `RevokeSession(user, randHex(t,16), now)` gives `ErrNotFound`.
  - "revoke": after the user's own revoke, `SessionByHash` gives `ErrNotFound`, and a second `RevokeSession` gives nil.
  - "duplicate hash": a second `AddSession` with the same hash gives `ErrDuplicate`.
  - "independent sessions": one user has two sessions; revoking one leaves the other returned by `SessionByHash`.
  Every failure message prints the got and want values.
- `./internal/store TestAuditSessionColumnsConformanceSQLite` and `./internal/store TestAuditSessionColumnsConformancePostgres`: `runAuditSessionColumnsConformance(t, s)`. It creates an org and appends three events through `AppendAudit`, sealing as `runTokensAuditConformance` does (`Hash: "h"+randHex`). Event 1 has `SessionID: "s"+randHex` and `StepUpID: "u"+randHex`. Events 2 and 3 have neither. `AuditEvents` must then show: event 1's `SessionID` and `StepUpID` unchanged; events 2 and 3 with both empty; seq 1, 2, 3, with `evs[0].PrevHash == ""` and each `evs[i].PrevHash == evs[i-1].Hash`.
- `./internal/store TestSessionStoredHashedOnly`: `sqlite` and `postgres` subtests, as in `TestEnrolmentTokenStoredHashedOnly`. The postgres subtest reads through the superuser `rawDB(superDSN)`. `checkSessionHashedOnly(t, s, raw, cols)` uses `plain := "mg_" + strings.Repeat("c", 52)` and `other := "mg_" + strings.Repeat("d", 52)`. It checks that `store.HashToken(plain)` equals the hex `sha256.Sum256`, then calls `AddSession(user, HashToken(plain), now+1h)`. Then:
  - `SessionByHash(HashToken(other), now)` gives `ErrNotFound`;
  - in `dumpAll`, some cell equals the hash, and no row contains `plain` or `mg_`;
  - `SessionByHash(HashToken(plain), now)` returns the session's ID.
- Existing tests that cover the change: `TestLookupTablesMatchSchema` (the lookup list, no `org_id`, no RLS on Postgres, no forbidden column names), `TestSchemasMatch` (`session_hashes` and both `audit_events` columns, with the same nullability on both backends), and `TestMigrateIdempotent`.

## Edge cases

- **Expiry boundary:** at `now == expires_at` the session is refused, because the comparison is strict `>`. The "expiry" subtest covers it. `formatTime` truncates to the second, so the conformance test truncates `now` too, and the boundary is exact.
- **Revoked:** `SessionByHash` filters `revoked_at IS NULL`. The "revoke" subtest covers it.
- **Cross-user revoke:** `RevokeSession` filters on both `id` and `user_id`. Another user's call matches nothing, gives `ErrNotFound`, and changes nothing. The "revoke by another user" subtest covers it.
- **Repeated revoke:** `COALESCE` keeps the first stamp, and `RowsAffected` is 1, so the call returns nil. The "revoke" subtest covers it.
- **Duplicate hash:** a primary-key violation becomes `ErrDuplicate` through `dupOr`. The "duplicate hash" subtest covers it. A `NewID` collision on the unique `id` would also be `ErrDuplicate`. At 128 random bits this is ignored.
- **Plaintext never stored:** no method takes the bearer, and `Session` has no hash field. `TestSessionStoredHashedOnly` checks every table on both backends.
- **Hostile hash input:** the hash is always a bound parameter, never interpolated. An unknown or garbage value finds no row and gives `ErrNotFound` ("unknown hash").
- **Events written before W4-1a:** they read back with `''` through the column `DEFAULT`. The chain hash is unchanged (`internal/registry` is untouched), so `VerifyAudit` still holds. The existing registry and API tests cover this.

## Risks

- **tests-kept gate:** `scripts/verify` fails any `_test.go` that existed on `main` and loses a line (`removedTestLines`). W4-1a is not in `ownerTestEdits`, and `tools/pipeline/` is protected. Editing the `want := []string{...}` literal would remove a line, so the plan adds `want = append(want, "session_hashes")` on a new line instead. The list grows and nothing is removed or loosened.
- **Migration number:** if the branch is not merged with `main` first, `0004_sessions.sql` would collide with `0004_releases.sql` after merge. Merge first and use `0005`.
- **`ALTER TABLE ... ADD COLUMN ... NOT NULL DEFAULT ''`:** SQLite allows it with a constant default, and Postgres 18 too. `TestSchemasMatch` checks that nullability matches. The RLS test's raw `INSERT INTO audit_events` names only the old columns, so it keeps passing through the default.
- **`COALESCE(revoked_at, $1)` parameter typing on pgx:** the type is inferred as `text` from `revoked_at`, so no cast is needed. The Postgres conformance test exercises it.
- **Size:** about 120 counted lines (two migrations of about 10 each, `store.go` about 25, `sessions.go` about 70, `tokens.go` about 6, `lookup.go` 1), well under 220. Tests and docs are not counted.
- **Interface growth:** the only other `store.Store` implementer is `internal/api/errors_test.go`'s `failingAudit`, which embeds `store.Store`, so it still compiles. No mock needs the new methods.
- **Scope creep:** nothing calls these methods, there is no session listing and no `step_ups` or `link_token_hashes`, and the audit hash is unchanged. That work is W4-1b, W4-1d and W4-1.
- **Gate:** run `make fmt`, `make test-list`, `make ci` and `scripts/verify check W4-1a` with `MOONGLASS_TEST_POSTGRES` set, so the Postgres variants run and do not skip.
