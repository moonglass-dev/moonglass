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

### W1-3 (Opus review minors, not fixed in the one product-bug round)

The blocker (the token was spent before the data dir was proven writable) was fixed in #16. These remain:

- **`moonglass server` creates an org only with `--print-enroll-token`, keyed off a `DIR/org` side file** rather than "if the store has no orgs" as the spec says. Losing `DIR/org` creates a second org. `server.Run`, `firstOrg` and `runServer` have no test.
- **Re-enrolling a data dir that already holds keys to the same control plane** hits the duplicate-key path, spends the new token and reports `TOKEN_INVALID`, which tells the operator to get a new token that can never work. Report "key already registered" instead.
- **The agent accepts a plain `http://` URL to any host.** The spec says plain HTTP is for loopback only, but nothing enforces it, and a pin taken over clear text outlives W1-4's TLS. Reject non-loopback `http://` until W1-4.
- **`--print-enroll-token` prints the live bearer to stdout,** which is the journal under systemd. Warn in the spec, or write it to a 0600 file.
- **`agent enroll` uses `http.DefaultClient` and `context.Background()`:** no timeout and no signal handling, so a black-holed URL hangs forever.

### W1-5 (Opus review minors, not fixed in the one product-bug round)

The major finding (a pushed config could move the admin API to TCP or the certificate keys out of the data dir) was fixed in #18 with a guard socket in front of Caddy's admin API. These remain:

- **A rejected counter config leaves a phantom release.** Caddy provisions before it validates, so `countsFor(c.Release)` registers the release (even `""`) before validation fails, and `GET /moonglass/counters` reports it for the life of the process.
- **WebSocket sessions are recorded as request latency.** The counter times `ServeHTTP`, which for an upgraded (101) request returns only when the connection closes. A 10-minute WebSocket lands in the >1000 ms bucket and inflates `latency_nanos_total`, which auto-rollback (W4-3) is meant to read. Time to response headers instead, or exclude upgrades.
- **`moonglass proxy --data-dir DIR` has no success-path test.** Only usage errors are tested at the CLI; flag wiring to `proxy.Run`, SIGTERM shutdown and exit 0 are not.
- **CI time grew from about 2 to 12 minutes** with Caddy linked (race and `build-all`). Worth caching or splitting before more tickets queue behind it.

## Owner decisions (do not close in a ticket)

- **W1-1 open questions 6 and 7** (`pid`/`ipc` `container:` cross-project; `service:` inheriting host networking). Left as-is; owner is deciding.
- **ADR-0002 open question 1:** managed Postgres provider and region. Left open; CI stays on `postgres:18`.
