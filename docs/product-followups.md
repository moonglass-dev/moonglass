# Product follow-ups

Product and contract leftovers that should not block the current ticket. Owner-open questions stay listed at the bottom and are not to be decided in a ticket.

## Open

### W1-2 (Opus review minors, not fixed in the one product-bug round)

The major finding (the application role is not allowed to be a superuser, have `BYPASSRLS`, or own the tables) was fixed in #13. These remain:

- **`AddEnrolmentToken` accepts an arbitrary string as `tokenHash`.** A caller that passes the bearer value instead of `store.HashToken(value)` can persist the plaintext `mg_…` token. `TestEnrolmentTokenStoredHashedOnly` only covers the hash path. The store should reject a value that looks like a bearer token, or only accept a hex SHA-256.

- **`EnrolmentTokens` lists a non-RLS lookup table.** The listing is org-scoped only by `WHERE org_id = $1`. A missing-filter query against that table is not covered by the Postgres RLS test, and lookup rows are returned to the caller. Either the listing should go through an RLS table, or the missing-filter case should be tested and documented as a lookup-table exception.

- **SQLite DSN concatenation in `store/sqlite`:** `"file:" + path + "?_pragma=…"`. A path containing `?` or `#` is split by the driver's URI parser, so the database can open at a different file and pragmas (`foreign_keys`, `busy_timeout`) may not apply. Query-escape the path, or use a DSN form that cannot be split that way.

- **`TestLookupTablesMatchSchema` calls `newPGEnv` before opening SQLite.** When `MOONGLASS_TEST_POSTGRES` is empty the whole test skips, including the static `LookupTables` check and the SQLite schema checks. Split the SQLite and list assertions so they always run.

- **`github.com/jackc/pgx/v5` and `modernc.org/sqlite` are `// indirect` in `go.mod`** even though `internal/store/pg` and `internal/store/sqlite` import them. `go mod tidy` would promote them; a later tidy must not drop them.

## Owner decisions (do not close in a ticket)

- **W1-1 open questions 6 and 7** (`pid`/`ipc` `container:` cross-project; `service:` inheriting host networking). Left as-is; owner is deciding.
- **ADR-0002 open question 1:** managed Postgres provider and region. Left open; CI stays on `postgres:18`.
