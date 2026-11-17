# Control-plane storage foundation

W1-2 implements ADR-0002: one storage interface, two backends, one conformance suite. This spec is the behaviour the ticket builds. It does not change the ADR.

## Backends

- **Postgres** (hosted, CI): `store/pg`, driver `github.com/jackc/pgx/v5` at the version pinned in ADR-0010. CI runs `postgres:18` as a service (ADR-0002 decision 13). Tests that need Postgres read the URL from `MOONGLASS_TEST_POSTGRES`. When that variable is empty they skip, except when `CI` is set, in which case they fail: CI must provide the service.
- **SQLite** (self-hosted): `store/sqlite`, driver `modernc.org/sqlite` at the version pinned in ADR-0010. Pure Go, no cgo. Every test that can run on SQLite does.

Nothing outside `internal/store` writes SQL. `LISTEN/NOTIFY`, logical export, backups, and the agent's on-server SQLite are out of scope.

## Roles and connections (Postgres)

Migrations run as a role that owns the tables. The application connects as a different role that does not own the tables and cannot bypass RLS (`BYPASSRLS` must be false; `NOLOGIN` is not required for the test role, but the role must not be a superuser and must not own the tables). Tests using the CI superuser URL create these two roles (or equivalent disposable roles) themselves. SQLite has no roles; the same migrations still run.

## Schema

SQL stays in the common subset of Postgres and SQLite, except that RLS policies, the application role, and `SET app.org_id` live only in `store/pg`.

Every table has `org_id`, except the lookup tables listed in one file in `internal/store` (ADR-0002 decision 12). The conformance suite asserts that list against the schema on both backends. A lookup row holds only: a lookup key, the IDs it maps to (`user_id`, `org_id`, the row's own ID), an expiry, and a revoked (or used) marker. Never a secret, a wrapped key, a plaintext token, scopes or names.

### Lookup tables

- `user_identities` — lookup key is an external identity (a single string such as `provider:subject`). Maps to `user_id`.
- `user_orgs` — lookup key is `user_id`. Maps to `org_id`. One row per org the user belongs to. This is how a request finds the caller's orgs before `app.org_id` is set.
- `agent_keys` — lookup key is the agent's public key. Maps to `server_id` and `org_id`.
- `enrolment_token_hashes` — lookup key is `SHA-256` of the bearer token. Maps to `org_id`, expiry, and a used/revoked marker. The plaintext token is never stored. Format of the bearer value is ADR-0005 (256 bits, `mg_` plus 52 lowercase unpadded base32); this ticket stores and looks up the hash only.

### Row-level tables

- `orgs` — `org_id` equals the org's `id`. Created with a row for itself.
- `memberships` — `org_id`, `user_id`, `role`, uniqueness on `(org_id, user_id)`. `role` is one of `owner`, `admin`, `member`, `viewer` (ADR-0006). There is no separate `roles` table: the allowed values are the role list, enforced by a constraint on both backends.
- `servers` — `org_id`, `id` (the `server_id`), `created_at`. The public key lives only in `agent_keys`.

A person is a stable `user_id` minted when their first `user_identities` row is written. There is no `users` table with names or other profile fields: those would need `org_id` and RLS (a person can belong to several orgs) and are not needed to enrol or store memberships. `user_identities` plus `user_orgs` plus `memberships` are the user tables for this ticket.

Identifiers are 16 random bytes encoded as 32 lowercase hex characters, stored as `TEXT`. Timestamps are UTC RFC3339 text with a `Z`. These are the portable defaults; do not introduce UUID extensions or `timestamptz`.

## Transactions and isolation

Every request that reads or writes an RLS table runs in a transaction that has set `app.org_id` to that org (Postgres). Store methods that take an org run that way. Lookups that exist to find the org (a token hash, an agent public key, an external identity, a user's orgs) do not require `app.org_id` and do not return a secret.

On Postgres each RLS table has the policy `USING (org_id = nullif(current_setting('app.org_id', true), ''))` with the same expression as `WITH CHECK`. Under the application role a `SELECT` with the `org_id` filter removed therefore returns:

- nothing when `app.org_id` is unset, empty, or names a nonexistent org;
- only org A's rows, never org B's, when `app.org_id` is A.

An `INSERT` of a row for org B under `app.org_id` A is rejected by `WITH CHECK`. `TestPostgresMissingOrgFilterReturnsNothing` asserts all of this.

Grants live in `pg.Migrate`, not in the numbered files, because the application role's name is not known to a migration: after applying the files as the owner it grants `SELECT, INSERT, UPDATE, DELETE` (no DDL) on every table except `schema_migrations` to the application role. The application role never migrates; `pg.Open` has no migration entry point.

SQLite has no RLS. The same fixtures and queries run, always including an `org_id` predicate in store methods. The missing-filter test is Postgres-only.

## Migrations

Numbered plain SQL files per backend, applied by a small runner inside `internal/store`. No migration library. Applying an already-applied set is a no-op. The two schemas must describe the same tables and columns (names and nullability). A named test compares them.

## Conformance

One suite runs against both backends: create an org; mint a user via an external identity; add a membership; look up the user's orgs; reject a duplicate membership; store a server and find it by public key; store an enrolment token by hash, look it up, mark it used, and refuse a second use; a second org cannot see the first's memberships, servers, or tokens through store methods. Migrations are applied first. The suite is the same code for both backends.

Enrolment, the server and agent commands, heartbeat, and authn are later tickets. This ticket only provides the store.
