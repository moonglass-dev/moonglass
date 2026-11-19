---
verdict: ready
reason: ""
---

# Plan for W4-1d

Context: the store gains the `link_token_hashes` lookup table and the row-level `step_ups` table, with four methods, so W4-1 (linking) and W4-1c (step-up) have storage to build on. The schema is the one `docs/specs/auth.md` "Storage" already fixes (owner-approved 2026-11-18), so no contract decision is open. Nothing outside `internal/store` changes. The `Store` interface has no other implementers: `internal/api/errors_test.go` embeds it, which is unaffected.

## Files to change

- `internal/store/migrations/pg/0007_link_tokens_step_ups.sql` (new; 0006 is the highest file on `main`):
  - `CREATE TABLE link_token_hashes`:
    - `hash TEXT NOT NULL PRIMARY KEY`
    - `id TEXT NOT NULL UNIQUE`
    - `user_id TEXT NOT NULL`
    - `expires_at TEXT NOT NULL`
    - `used_at TEXT`
  - `CREATE TABLE step_ups`:
    - `org_id TEXT NOT NULL REFERENCES orgs (id)`
    - `id TEXT NOT NULL PRIMARY KEY`
    - `user_id TEXT NOT NULL`
    - `token_id TEXT NOT NULL DEFAULT ''`
    - `session_id TEXT NOT NULL DEFAULT ''`
    - `operation TEXT NOT NULL`
    - `project_id TEXT NOT NULL DEFAULT ''`
    - `environment_id TEXT NOT NULL DEFAULT ''`
    - `input_digest TEXT NOT NULL`
    - `confirm_hash TEXT`
    - `created_at TEXT NOT NULL`
    - `expires_at TEXT NOT NULL`
    - `signed_in_at TEXT`
    - `approved_at TEXT`
    - `declined_at TEXT`
    - `used_at TEXT`
    - `CHECK ((token_id = '') <> (session_id = ''))`
  - `ALTER TABLE step_ups ENABLE ROW LEVEL SECURITY;` and `CREATE POLICY org_isolation ON step_ups` with `USING` and `WITH CHECK` both `(org_id = nullif(current_setting('app.org_id', true), ''))`, as on `rollbacks`.
  - The `CHECK` is the boolean inequality "token empty XOR session empty". It works the same in Postgres (boolean `<>`) and SQLite (0/1 integers `<>`).
  - The file has no GRANT: `pg.Migrate` already grants data access on every table to the application role (`internal/store/pg/pg.go`).
- `internal/store/migrations/sqlite/0007_link_tokens_step_ups.sql` (new): the same two `CREATE TABLE` statements, with no RLS lines.
- `internal/store/lookup.go`: append `"link_token_hashes"` to `LookupTables`.
- `internal/store/store.go`:
  - `ErrTokenUsed = errors.New("store: token already used")` and `ErrTokenExpired = errors.New("store: token expired")`. Same variables, so identities are unchanged. `internal/server/enroll.go` matches them with `errors.Is` only. No code or test compares the old text.
  - `type LinkToken struct { ID, UserID, ExpiresAt string }`, with a doc comment saying the bearer is stored only as `HashToken` in `link_token_hashes`, so the type has no hash field.
  - `type StepUp struct { OrgID, ID, UserID, TokenID, SessionID, Operation, ProjectID, EnvironmentID, InputDigest, CreatedAt, ExpiresAt, SignedInAt, ApprovedAt, DeclinedAt, UsedAt string }`. The doc comment says it binds one user, one credential (exactly one of `TokenID` and `SessionID`), one operation, one target and one input digest; an unset marker reads as empty text; there is no confirmation-hash field.
  - `Store` gains these four methods, each with a doc comment stating the errors listed in the ticket:
    - `AddLinkToken(ctx context.Context, userID, tokenHash string, expires time.Time) (LinkToken, error)`
    - `UseLinkToken(ctx context.Context, linkID string, now time.Time) (LinkToken, error)`
    - `CreateStepUp(ctx context.Context, orgID string, su StepUp, now, expires time.Time) (StepUp, error)`
    - `StepUp(ctx context.Context, orgID, id string) (StepUp, error)`
- `internal/store/linktokens.go` (new), on `*SQLStore`, through `s.db` with `s.q`, and with no org bound, like `sessions.go`:
  - `AddLinkToken`: `NewID()`, then `lt := LinkToken{ID: id, UserID: userID, ExpiresAt: formatTime(expires)}`, then `INSERT INTO link_token_hashes (hash, id, user_id, expires_at) VALUES ($1, $2, $3, $4)`. An insert error goes through `s.dupOr(err, "insert link token")`, so a repeated hash is `ErrDuplicate`.
  - `UseLinkToken`: `stamp := formatTime(now)`, then one statement:
    `UPDATE link_token_hashes SET used_at = $1 WHERE id = $2 AND used_at IS NULL AND expires_at > $3 RETURNING id, user_id, expires_at`.
    - A returned row is the result.
    - On `sql.ErrNoRows`, read `SELECT used_at FROM link_token_hashes WHERE id = $1` into a `sql.NullString`, and return:
      - no row: `fmt.Errorf("use link token: %w", ErrNotFound)`;
      - `used.Valid`: `ErrTokenUsed`, checked first, so a used token that has since expired is still `ErrTokenUsed`;
      - anything else: `ErrTokenExpired`.
    - Any other error is wrapped with `use link token:` / `read link token:`.
    - The expiry comparison is lexical RFC3339 UTC text and strict, as in `SessionByHash`.
- `internal/store/stepups.go` (new):
  - `const stepUpColumns`: the 15 columns. Each of the five nullable columns is wrapped as `COALESCE(signed_in_at, '')` and so on, so NULL reads back as empty text. `confirm_hash` is never selected.
  - `scanStepUp(row interface{ Scan(...any) error }) (StepUp, error)`, as `scanRollback` does it.
  - `CreateStepUp`. Before any SQL it validates, each check with an error that names its rule:
    - `UserID` empty: `create step-up: user id is empty: a step-up binds one user`;
    - `Operation` empty: `create step-up: operation is empty: a step-up binds one operation`;
    - `InputDigest` empty: `create step-up: input digest is empty: a step-up binds one input digest`;
    - `(su.TokenID == "") == (su.SessionID == "")`: `create step-up: exactly one of token id and session id must be set, got both` (or `got neither`).

    It then calls `NewID()`. The output is built from the input's binding fields only: `out := StepUp{OrgID: orgID, ID: id, UserID: su.UserID, TokenID: su.TokenID, SessionID: su.SessionID, Operation: su.Operation, ProjectID: su.ProjectID, EnvironmentID: su.EnvironmentID, InputDigest: su.InputDigest, CreatedAt: formatTime(now), ExpiresAt: formatTime(expires)}`. The caller's ID, OrgID, times and markers are therefore dropped. Inside `s.inOrg(ctx, orgID, …)` it runs `INSERT INTO step_ups (org_id, id, user_id, token_id, session_id, operation, project_id, environment_id, input_digest, created_at, expires_at) VALUES ($1 … $11)`. An insert error goes through `s.dupOr(err, "insert step-up")`. The marker columns and `confirm_hash` stay NULL.
  - `StepUp`: inside `s.inOrg`, `SELECT stepUpColumns FROM step_ups WHERE id = $1 AND org_id = $2`. `sql.ErrNoRows` becomes `fmt.Errorf("step-up %s is not in org %s: %w", id, orgID, ErrNotFound)`. `scanStepUp` wraps the scan with `%w`, so `errors.Is(err, sql.ErrNoRows)` is checked on the scan error.
- `internal/store/schema_test.go`: in `TestLookupTablesMatchSchema`, add `want = append(want, "link_token_hashes")` after the `session_hashes` line. No line is removed.
- `docs/specs/storage.md`:
  - Under "Lookup tables", add a bullet: `link_token_hashes`. Its lookup key is `SHA-256` of a link bearer, it maps to the link's `id` and `user_id`, with `expires_at` and a `used_at` marker, and it has no RLS because it is read before an org is known.
  - Extend "Sessions, links and step-ups":
    - the `step_ups` columns and their defaults (text targets and credentials default to empty, markers NULL);
    - the credential `CHECK`;
    - `ENABLE ROW LEVEL SECURITY` and `org_isolation` on Postgres, with grants through `pg.Migrate`, which is unchanged;
    - the four methods and their errors;
    - the conformance cases: `runLinkTokensConformance`, `TestLinkTokenStoredHashedOnly`, `runStepUpsConformance`, `TestPostgresRLSOnStepUps` and `TestStepUpConstraintsEnforced`.
- `docs/specs/auth.md`: unchanged. "Storage" matches the plan column for column.
- `testdata/tests.txt`: `make test-list` after the tests are added.

## Tests to write

Write all of these first and see them fail to compile or fail. Files: `internal/store/linktokens_conformance_test.go`, `internal/store/stepups_conformance_test.go` and `internal/store/stepups_test.go`.

- `./internal/store TestLinkTokensConformanceSQLite`: `s, _ := newSQLite(t); runLinkTokensConformance(t, s)`.
- `./internal/store TestLinkTokensConformancePostgres`: `newPGEnv(t, true)`, then `runLinkTokensConformance(t, env.openApp(t))`, which runs as the application role.
- `runLinkTokensConformance(t, s store.Store)` uses `now := time.Now().UTC().Truncate(time.Second)`, `exp := now.Add(10*time.Minute)`, users from `UserForIdentity("link:"+randHex)` and hashes from `store.HashToken("mg_"+randHex)`. Its subtests:
  - "add": `isHex32(lt.ID)`, `UserID` equal to the user, and `ExpiresAt == exp.Format(time.RFC3339)` ending in `Z`.
  - "use once": the first `UseLinkToken(id, now)` returns exactly the `AddLinkToken` value (struct `!=` check). A second use at `now` is `ErrTokenUsed`. A use at `exp.Add(time.Second)` is still `ErrTokenUsed`.
  - "expired": table over `at expiry` (`exp`) and `after expiry` (`exp+1s`). Each is `ErrTokenExpired` on a fresh unused token. A later `UseLinkToken(id, now)` on the same token is still `ErrTokenExpired`, because no row was changed. The before-expiry case is "use once".
  - "unknown id": `UseLinkToken(randHex(16), now)` is `ErrNotFound`.
  - "duplicate hash": a second `AddLinkToken` with the same hash is `ErrDuplicate`, and the first token is still usable.
  - "independent tokens": two tokens of one user have distinct IDs. Using A leaves B usable, and the use of B returns B.
- `./internal/store TestLinkTokenStoredHashedOnly`: `sqlite` and `postgres` subtests as in `TestSessionStoredHashedOnly`. `checkLinkTokenHashedOnly(t, s, raw, cols)`:
  - uses plaintext `"mg_" + strings.Repeat("e", 52)` and checks `HashToken` against `sha256`;
  - after `AddLinkToken`, `dumpAll` shows some cell equal to the hex hash and no row containing the plaintext or `mg_`;
  - `UseLinkToken(lt.ID, now)` then returns `lt`.
- `./internal/store TestStepUpsConformanceSQLite`: `s, raw := newSQLite(t); runStepUpsConformance(t, s, raw)`.
- `./internal/store TestStepUpsConformancePostgres`: `runStepUpsConformance(t, env.openApp(t), env.rawDB(t, env.superDSN))`. The store is the application role. The raw handle is used only to count rows.
- `runStepUpsConformance(t, s store.Store, raw *sql.DB)` uses orgs from `s.CreateOrg`, a user from `UserForIdentity`, and `countStepUps(t, raw)`, which runs `SELECT count(*) FROM step_ups` with no placeholders, so it works on both drivers. Subtests run in sequence. Its subtests:
  - "token credential with target": `in := StepUp{UserID, TokenID: randHex, Operation: "services.delete", ProjectID, EnvironmentID, InputDigest: randHex(32)}`, created at `now` with expiry `now+10m`. Checks: `isHex32(got.ID)`, `got.OrgID == orgA`, every input field equal, `CreatedAt`/`ExpiresAt` equal to the RFC3339 text, and the four markers empty. `s.StepUp(orgA, got.ID)` equals `got`.
  - "session credential, no target": `SessionID` set, with an empty target. It reads back with empty `TokenID`, `ProjectID` and `EnvironmentID` and the given `SessionID`.
  - "caller fields ignored": input sets `ID: "x"`, `OrgID: orgB`, `CreatedAt`, `ExpiresAt`, and all four markers to `"2000-01-01T00:00:00Z"`. Checks: the returned and read-back row has a fresh 32-hex ID, `OrgID == orgA`, times from `now`/`expires` and empty markers, and `s.StepUp(orgB, got.ID)` is `ErrNotFound`.
  - "refused": table over `both credentials`, `neither credential`, `empty user`, `empty operation` and `empty input digest`. Each `CreateStepUp` returns a non-nil error that is not `ErrNotFound`/`ErrDuplicate`, whose text contains the rule fragment (`exactly one of token id and session id`, `user id is empty`, `operation is empty`, `input digest is empty`). `countStepUps` is unchanged.
  - "unknown id": `StepUp(orgA, randHex(16))` is `ErrNotFound`.
  - "another org": `StepUp(orgB, idOfA)` is `ErrNotFound`, and `StepUp(orgA, idOfA)` still equals the stored row.
- `./internal/store TestPostgresRLSOnStepUps` follows `TestPostgresRLSOnRollbacks`:
  - Setup: two orgs, each with one step-up through the store as the application role.
  - `orgIDsUnder(app, unset|empty|nonexistent, "step_ups")` returns no rows. Under A it returns only A's rows, and at least one.
  - Raw `INSERT INTO step_ups (org_id, id, user_id, token_id, operation, input_digest, created_at, expires_at) VALUES ($1 … $8)` under `app.org_id = A`. For B it fails with `pgError` SQLSTATE `42501`. For A it succeeds.
  - `pg_tables.rowsecurity` for `step_ups` is true.
- `./internal/store TestStepUpConstraintsEnforced`: on SQLite (`newSQLite`), with an org from `s.CreateOrg`. Raw `INSERT INTO step_ups (org_id, id, user_id, token_id, session_id, operation, input_digest, created_at, expires_at) VALUES (?, …)`, over a table of cases:
  - both set, both empty: refused with `CHECK constraint failed`;
  - token only, session only: succeed.

  A subtest "defaults" inserts with `session_id` set and with `token_id`, `project_id` and `environment_id` omitted. It reads those three back as empty text, and `confirm_hash`, `signed_in_at`, `approved_at`, `declined_at` and `used_at` as invalid `sql.NullString`.
- Existing tests that cover this change and stay unchanged: `TestLookupTablesMatchSchema` (with the one added line), `TestSchemasMatch`, `TestMigrateIdempotent`, the enrolment conformance cases on `ErrTokenUsed`/`ErrTokenExpired`, and `TestPostgresMissingOrgFilterReturnsNothing`.

## Edge cases

- Replay of a link token is `ErrTokenUsed` ("use once"). Replay after expiry is also `ErrTokenUsed`, because the used check comes first ("use once", third call).
- A link token at exactly its expiry is expired, because the comparison is strict `>` ("expired" / "at expiry"). A refused use does not stamp `used_at`, so it stays `ErrTokenExpired` ("expired", later use).
- Two concurrent uses are safe because the conditional `UPDATE` is one statement. This is the same guarantee `UseEnrolmentToken` relies on.
- A link token with an unknown ID, including any value that is not an ID, is `ErrNotFound` ("unknown id"). A repeated hash is `ErrDuplicate` and leaves the first token intact ("duplicate hash").
- The plaintext bearer is never stored (`TestLinkTokenStoredHashedOnly`).
- Step-up credentials that are both set or both empty are refused in Go ("refused") and in the database (`TestStepUpConstraintsEnforced`).
- An empty user, operation or digest is refused before SQL, and no row is written ("refused" plus the count).
- A step-up ID, org or times forged by the caller are ignored ("caller fields ignored").
- Cross-org reads are `ErrNotFound` through the store ("another org"). Without the filter, the RLS policy hides them, and `WITH CHECK` blocks cross-org inserts (`TestPostgresRLSOnStepUps`).
- `confirm_hash` is never written or read (no struct field; the "defaults" subtest checks it is NULL).

## Risks

- **Postgres `CHECK` portability:** `(token_id = '') <> (session_id = '')` is valid boolean SQL on Postgres and integer SQL on SQLite. `TestStepUpConstraintsEnforced` proves it on SQLite. The Postgres conformance refusal cases prove only the Go check, so the RLS test's own-org insert sets exactly one credential to exercise the constraint's accepting side.
- **`TestSchemasMatch`:** both files declare identical columns and nullability. The `DEFAULT ''` columns are `NOT NULL` in both files.
- **Lookup-table deny-list** (`TestLookupTablesMatchSchema`): `link_token_hashes` columns contain no `name`, `secret`, `scope`, `wrapped` or `plaintext`, and it has no `org_id`. `step_ups` has `org_id` and RLS, so the "neither RLS nor lookup" check passes.
- **Error text change:** only `errors.Is` callers exist (`internal/server/enroll.go`). The agent's own `ErrTokenUsed` is a separate variable and is not touched. The server and API test suites still run in `make ci` to confirm.
- **Placeholders:** each `$n` is used once and in order. `UseLinkToken` passes `stamp` twice, as `$1` and `$3`, the way `UseEnrolmentToken` does.
- **Banned words:** none are used in code or comments.
- **Diff budget (390):** about 60 migration lines, about 50 in `store.go`, about 55 in `linktokens.go`, about 85 in `stepups.go`, 1 in `lookup.go` and about 25 of spec. Tests and `testdata` are not counted.
- **Formatting and lint:** the build runs `make fmt` and then `make ci`. Postgres tests need `MOONGLASS_TEST_POSTGRES` locally; under `CI` they fail rather than skip.

## Verification

- `make test-one PKG=./internal/store NAME='TestLinkToken|TestStepUp|TestPostgresRLSOnStepUps|TestLookupTablesMatchSchema|TestSchemasMatch|TestMigrateIdempotent'` with `MOONGLASS_TEST_POSTGRES` set.
- `make test-list`, `make ci`, `scripts/verify check W4-1d`.
