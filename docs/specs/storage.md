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
- `token_hashes` — lookup key is `SHA-256` of an API token (`docs/specs/registry.md`). Maps to `org_id` and `token_id`, with a revoked marker. It has no RLS because the org is not known when it is read.


### Row-level tables

- `orgs` — `org_id` equals the org's `id`. Created with a row for itself.
- `memberships` — `org_id`, `user_id`, `role`, uniqueness on `(org_id, user_id)`. `role` is one of `owner`, `admin`, `member`, `viewer` (ADR-0006). There is no separate `roles` table: the allowed values are the role list, enforced by a constraint on both backends.
- `servers` — `org_id`, `id` (the `server_id`), `created_at`. The public key lives only in `agent_keys`.
- `tokens` — `org_id`, `id`, `user_id` (the human owner), `name`, `last4`, `expires_at`, `created_at`. Never the secret; the `mg_` prefix is constant and not stored.
- `token_scopes` — `org_id`, `token_id`, `project`, `environment`, `operation`; `*` is the wildcard. An owner token has exactly one row, `* * *`.
- `audit_events` — `org_id`, `seq` (from 1, primary key with `org_id`), `id`, `at`, `principal_kind` (`human` or `agent`), `user_id`, `token_id`, `operation`, `outcome` (`allowed`, `denied` or `error`), `reason`, `prev_hash`, `hash`. `prev_hash` is empty for `seq` 1. The registry is the only writer, through `AppendAudit`, which reads the chain head and inserts in one transaction and retries when another writer takes the same `seq`.

A person is a stable `user_id` minted when their first `user_identities` row is written. There is no `users` table with names or other profile fields: those would need `org_id` and RLS (a person can belong to several orgs) and are not needed to enrol or store memberships. `user_identities` plus `user_orgs` plus `memberships` are the user tables for this ticket.

Identifiers are 16 random bytes encoded as 32 lowercase hex characters, stored as `TEXT`. Timestamps are UTC RFC3339 text with a `Z`. These are the portable defaults; do not introduce UUID extensions or `timestamptz`.

### Projects, environments, services and releases (W2-11)

Specified by the weeks 2–3 preflight (owner decisions D6, D18). All are row-level tables with `org_id`.

- `projects` — `org_id`, `id`, `name`, `created_at`; unique `(org_id, name)`.
- `environments` — `org_id`, `id`, `project_id`, `name`, `created_at`; unique `(org_id, project_id, name)`. W4-2 adds `protected`, boolean `NOT NULL`. The org's first environment is created true; later ones are created false. W4-2 stores the flag and does not gate deploys. W5-2 adds that gate. Until then a deploy to a protected environment runs and is audited. There is no plan-cap check in the open build. Resource limits are deferred to v1. The environment key is W3-2.
- `services` — `org_id`, `id`, `project_id`, `environment_id`, `name`, `server_id`, `current_release_id`, `previous_release_id` (nullable), `created_at`; unique `(org_id, environment_id, name)`. A service runs on exactly one server, chosen when it is created. The service `id` is the protocol's `app_id`. The two release columns copy the `app` state of the latest `CommandResult`. W2-11a creates `services` without `current_release_id` and `previous_release_id`; W2-11b adds both with `releases`.
- `releases` — `org_id`, `id` (the protocol's `release_id`), `service_id`, `image_ref` (or empty for a git source), `git_repository`, `git_commit` (empty for an image; W3-4), `port`, `health_path`, `domains` (JSON array), `pre_deploy` (JSON argv, empty for none), `pre_deploy_reversible`, `status`, `step`, `request_id`, `image_id`, `repo_digest`, `detail`, `created_at`, `finished_at`.
  - `status` is `queued`, `running`, `succeeded`, `failed`, `rejected` or `superseded`.
  - `step` is the command in flight (`build`, `deploy`, `pre_deploy`, `switch`) and `request_id` its envelope id (`docs/specs/connection.md`), so a restarted server can re-send it.
  - `image_id` and `repo_digest` come from the `CommandResult`, so the history shows what actually ran (owner condition on D4).
- `rollbacks` — `org_id`, `id`, `service_id`, `request_id`, `status` (`running`, `succeeded`, `failed`, `rejected`), `detail`, `created_at`, `finished_at`.

**Names** of projects, environments and services are 1 to 63 bytes of `[a-z0-9-]`, starting and ending with a letter or digit: the protocol's name format (`docs/specs/protocol.md`). They are never renamed, because the agent binds an app to them (owner decision D1). Identifiers and timestamps follow the rules below.

The conformance suite gains: create a project, an environment and a service on a server of the same org; reject a service on another org's server; reject duplicate names; record a release and its result; a second org sees none of them.

W2-11a adds `runProjectsConformance`, run as `TestProjectsConformanceSQLite` and `TestProjectsConformancePostgres`, covering: creating and listing a project, an environment and a service back with their ids, names, parent ids and `server_id`, in name order; refusing a service on another org's or an unknown server, a service in another org's environment and an environment in another org's project, with `ErrNotFound` and no row written; refusing a duplicate project, environment and service name with `ErrDuplicate` while the same name under another parent or org succeeds; `ResolveTarget` for a known project and environment, for a project alone, and `ErrNotFound` for an unknown name or a name only in another org; a second org listing nothing of the first. It also adds `TestPostgresRLSOnProjectTables`, which checks the `org_isolation` policy on `projects`, `environments` and `services` as the application role (unset, empty and nonexistent `app.org_id` return nothing; an `INSERT` for another org is refused by `WITH CHECK`; `rowsecurity` is on), and `TestProjectNamesValidated`, which checks the name format on all three create methods. The release cases come with W2-11b.

### Recovery recipients, environment keys and secrets (W3-2)

Specified by the weeks 2–3 preflight (`docs/specs/secrets.md`). Row-level tables; they hold public keys and ciphertext only.

- `recovery_recipients` — `org_id`, `id`, `recipient` (an age X25519 recipient, `age1…`), `created_at`. W3-2 allows one per org (ADR-0003 decision 12).
- `environment_keys` — `org_id`, `environment_id` (unique), `public_key` (`age1…`), `created_at`.
- `wrapped_environment_keys` — `org_id`, `environment_id`, `recipient_kind` (`recovery`, or `agent` from W4-8), `recipient_id` (a `recovery_recipients.id` or a `server_id`), `ciphertext` (an age file), `created_at`; unique `(org_id, environment_id, recipient_kind, recipient_id)`.
- `secrets` — `org_id`, `environment_id`, `kind` (`env` or `registry`), `name`, `ciphertext` (an age file), `updated_at`; unique `(org_id, environment_id, kind, name)`.

### Server deploy keys (W3-8)

`servers` gains `deploy_key`: the server's SSH deploy public key from its latest `Heartbeat` (`docs/specs/protocol.md`), or empty. It is a public key.

### Sessions, links and step-ups (W4-1a)

`session_hashes` and `link_token_hashes` (lookup tables), `step_ups` (row-level), and the `session_id` and `step_up_id` columns of `audit_events` are specified in `docs/specs/auth.md`.

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
