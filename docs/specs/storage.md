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
- `session_hashes` — lookup key is `SHA-256` of a session bearer. Maps to the session `id` and `user_id`, with `expires_at` and a `revoked_at` marker. It has no RLS because the org is not known when it is read (`docs/specs/auth.md`).
- `link_token_hashes` — lookup key is `SHA-256` of a link bearer. Maps to the link's `id` and `user_id`, with `expires_at` and a `used_at` marker. It has no RLS because the org is not known when it is read (`docs/specs/auth.md`).
- `purge_due` (W5-5c) — lookup key is the id of a soft-deleted row. Maps to `org_id`, with the expiry at which it is purged. It lets the hourly purge find due rows without knowing their orgs (`docs/specs/delete.md`).


### Row-level tables

- `orgs` — `org_id` equals the org's `id`. Created with a row for itself.
- `memberships` — `org_id`, `user_id`, `role`, uniqueness on `(org_id, user_id)`. `role` is one of `owner`, `admin`, `member`, `viewer` (ADR-0006). There is no separate `roles` table: the allowed values are the role list, enforced by a constraint on both backends.
- `servers` — `org_id`, `id` (the `server_id`), `created_at`. The public key lives only in `agent_keys`. FR1-2 adds `agent_age_recipient` — text, `NOT NULL DEFAULT ''`, the agent's raw 32-byte X25519 public key as lowercase hex from `EnrollRequest.agent_age_recipient`, empty until FR1-2's migration.
- `tokens` — `org_id`, `id`, `user_id` (the human owner), `name`, `last4`, `expires_at`, `created_at`. Never the secret; the `mg_` prefix is constant and not stored. W5-1a adds `agent_id`: empty for a person's token, otherwise the agent identity that holds it, whose owner is `user_id` (`docs/specs/agent-identities.md`).
- `agent_identities` (W5-1a) — `org_id`, `id`, `name`, `owner_user_id`, `created_at`, `revoked_at`; unique `(org_id, name)` (`docs/specs/agent-identities.md`).
- `token_scopes` — `org_id`, `token_id`, `project`, `environment`, `operation`; `*` is the wildcard. An owner token has exactly one row, `* * *`.
- `audit_events` — `org_id`, `seq` (from 1, primary key with `org_id`), `id`, `at`, `principal_kind` (`human` or `agent`), `user_id`, `token_id`, `operation`, `outcome` (`allowed`, `denied` or `error`), `reason`, `prev_hash`, `hash`. `prev_hash` is empty for `seq` 1. The registry is the only writer, through `AppendAudit`, which reads the chain head and inserts in one transaction and retries when another writer takes the same `seq`. W5-1a adds `agent_id`; W5-5c adds `project_id`, `environment_id` and the principal kind `system` (`docs/specs/agent-identities.md`, `docs/specs/audit.md`). W4-1a adds `session_id` and `step_up_id`, empty when they don't apply (`docs/specs/auth.md`).

A person is a stable `user_id` minted when their first `user_identities` row is written. There is no `users` table with names or other profile fields: those would need `org_id` and RLS (a person can belong to several orgs) and are not needed to enrol or store memberships. `user_identities` plus `user_orgs` plus `memberships` are the user tables for this ticket.

Identifiers are 16 random bytes encoded as 32 lowercase hex characters, stored as `TEXT`. Timestamps are UTC RFC3339 text with a `Z`. These are the portable defaults; do not introduce UUID extensions or `timestamptz`.

### Projects, environments, services and releases (W2-11)

Specified by the weeks 2–3 preflight (owner decisions D6, D18). All are row-level tables with `org_id`.

- `projects` — `org_id`, `id`, `name`, `created_at`; unique `(org_id, name)`.
- `environments` — `org_id`, `id`, `project_id`, `name`, `created_at`; unique `(org_id, project_id, name)`. W4-2 adds `protected`, boolean `NOT NULL`. The org's first environment is created true; later ones are created false. W4-2 stores the flag and does not gate deploys. W5-2 adds that gate. Until then a deploy to a protected environment runs and is audited. There is no plan-cap check in the open build. Resource limits are deferred to v1. The environment key is W3-2.
- `services` — `org_id`, `id`, `project_id`, `environment_id`, `name`, `server_id`, `current_release_id`, `previous_release_id` (nullable), `created_at`; unique `(org_id, environment_id, name)`. A service runs on exactly one server, chosen when it is created. The service `id` is the protocol's `app_id`. The two release columns copy the `app` state of the latest `CommandResult`. W2-11a creates `services` without `current_release_id` and `previous_release_id`; W2-11e adds both with `rollbacks`.
- `releases` — `org_id`, `id` (the protocol's `release_id`), `service_id`, `image_ref` (or empty for a git source), `git_repository`, `git_commit` (empty for an image; W3-4), `port`, `health_path`, `domains` (JSON array), `pre_deploy` (JSON argv, empty for none), `pre_deploy_reversible`, `status`, `step`, `request_id`, `image_id`, `repo_digest`, `detail`, `created_at`, `finished_at`.
  - `status` is `queued`, `running`, `succeeded`, `failed`, `rejected` or `superseded`.
  - `step` is the command in flight (`build`, `deploy`, `pre_deploy`, `switch`) and `request_id` its envelope id (`docs/specs/connection.md`), so a restarted server can re-send it.
  - `image_id` and `repo_digest` come from the `CommandResult`, so the history shows what actually ran (owner condition on D4).
- `rollbacks` — `org_id`, `id`, `service_id`, `request_id`, `status` (`running`, `succeeded`, `failed`, `rejected`), `detail`, `created_at`, `finished_at`.

**Names** of projects, environments and services are 1 to 63 bytes of `[a-z0-9-]`, starting and ending with a letter or digit: the protocol's name format (`docs/specs/protocol.md`). They are never renamed, because the agent binds an app to them (owner decision D1). Identifiers and timestamps follow the rules below.

The conformance suite gains: create a project, an environment and a service on a server of the same org; reject a service on another org's server; reject duplicate names; record a release and its result; a second org sees none of them.

W2-11a adds `runProjectsConformance`, run as `TestProjectsConformanceSQLite` and `TestProjectsConformancePostgres`, covering: creating and listing a project, an environment and a service back with their ids, names, parent ids and `server_id`, in name order; refusing a service on another org's or an unknown server, a service in another org's environment and an environment in another org's project, with `ErrNotFound` and no row written; refusing a duplicate project, environment and service name with `ErrDuplicate` while the same name under another parent or org succeeds; `ResolveTarget` for a known project and environment, for a project alone, and `ErrNotFound` for an unknown name or a name only in another org; a second org listing nothing of the first. It also adds `TestPostgresRLSOnProjectTables`, which checks the `org_isolation` policy on `projects`, `environments` and `services` as the application role (unset, empty and nonexistent `app.org_id` return nothing; an `INSERT` for another org is refused by `WITH CHECK`; `rowsecurity` is on), and `TestProjectNamesValidated`, which checks the name format on all three create methods.

W2-11b adds `releases` and its queue. Every column is `NOT NULL`; text columns other than `id`, `org_id`, `service_id`, `domains`, `status` and `created_at` default to empty text. `domains` is a JSON array (`[]` for none) and `pre_deploy` a JSON argv, or empty text for no argv. `status` is checked against the six statuses and `step` against the empty step and the four steps, as `memberships.role` is checked. Two partial unique indexes, `releases_one_queued` on `(org_id, service_id) WHERE status = 'queued'` and `releases_one_running` on the same columns `WHERE status = 'running'`, are the database's guarantee of at most one queued and one running release per service.

The queue rules:

- `EnqueueRelease` reads the service inside the org-bound transaction (`ErrNotFound` when it is not the org's), then supersedes the service's queued release and inserts the new one as `queued`, both in one transaction. Losing the race for the queued slot shows up as a duplicate on the partial index and is retried with a fresh transaction, up to 10 times. The caller's status, step, request id, image ids, detail and times are ignored.
- `StartRelease` accepts only a `queued` release, `SetReleaseStep` and `SetReleaseImage` only a `running` one, and `FinishRelease` a `queued` or `running` one, ending it as `succeeded`, `failed` or `rejected`.
- Any other status returns `ErrReleaseState`, and another org's release returns `ErrNotFound`; nothing is changed in either case. An unknown step, or a finish status outside the three final ones, is refused before any SQL runs.
- `finished_at` stays empty until the release reaches a final status, and `step` keeps the last step that ran.
- The history is ordered by `created_at`, then `id`, descending, and capped at 50.
- The in-flight read returns the queued and running releases of the org's services on one server, oldest first, per org and server, so a reconnecting agent is never sent another org's step.

`releases` is not a lookup table and `LookupTables` is unchanged. On Postgres it has `ENABLE ROW LEVEL SECURITY` and the `org_isolation` policy; the application role gets its grants through `pg.Migrate`, which is unchanged.

W2-11b adds `runReleasesConformance`, run as `TestReleasesConformanceSQLite` and `TestReleasesConformancePostgres`, covering: enqueueing a release and reading every input field back, with no argv for an empty one; refusing an unknown or another org's service with `ErrNotFound` and writing nothing; starting a release, superseding the queued one and refusing a start in the wrong status; recording a step and the image the agent ran, refusing both on a queued or finished release and refusing an unknown step; finishing as each final status, refusing a second finish and a non-final status, and starting the queued release after a finish; the history order and its cap of 50; the in-flight read per server; eight concurrent enqueues leaving one queued and seven `superseded`; and a second org getting `ErrNotFound`, no rows and no change to the first org's releases. It also adds `TestPostgresRLSOnReleases`, which checks the `org_isolation` policy on `releases` as the application role (unset, empty and nonexistent `app.org_id` return nothing; an `INSERT` for another org is refused by `WITH CHECK`; `rowsecurity` is on), and `TestReleaseConstraintsEnforced`, which checks the `CHECK`s and both partial unique indexes with raw SQL on a migrated SQLite database.

W2-11e adds `services.current_release_id`, `services.previous_release_id` and `rollbacks`. `current_release_id` is `TEXT NOT NULL DEFAULT ''` and `previous_release_id` is nullable, `NULL` when the app has no previous release; neither carries a foreign key, because the store copies the ids it is given and does not interpret them. Every `rollbacks` column is `NOT NULL`; `request_id`, `detail` and `finished_at` default to empty text, as the release columns do; `status` is checked against `running`, `succeeded`, `failed` and `rejected`.

The rollback rules:

- `ServiceByName` returns the service of that name in its environment, with both release ids. A name in another environment of the org, or in another org, is `ErrNotFound`.
- `SetServiceReleases` copies a `CommandResult`'s `app.current_release_id` and `app.previous_release_id` in one `UPDATE` bound to the org, storing an empty previous id as `NULL`. Another org's service is `ErrNotFound` and nothing changes.
- `StartRollback` reads the service inside the org-bound transaction and records a `running` rollback with its envelope request id and `created_at` now, returning the stored row.
- `FinishRollback` accepts only a `running` rollback and ends it as `succeeded`, `failed` or `rejected` with `detail` and `finished_at` now. A final rollback is `ErrRollbackState` and another org's is `ErrNotFound`; nothing changes in either case. A status outside the three final ones is refused before any SQL runs, and `finished_at` stays empty until the rollback is final.
- The in-flight read returns the `running` rollbacks of the org's services on one server, oldest first, so a reconnecting agent is never sent another org's rollback.

`rollbacks` is not a lookup table and `LookupTables` is unchanged. On Postgres it has `ENABLE ROW LEVEL SECURITY` and the `org_isolation` policy; the application role gets its grants through `pg.Migrate`, which is unchanged.

W2-11e adds `runServiceReleaseStateConformance`, run as `TestServiceReleaseStateConformanceSQLite` and `TestServiceReleaseStateConformancePostgres`, covering: a new service reading back empty release ids from `CreateService`, `Services` and `ServiceByName`; `ServiceByName` returning every field of the service and `ErrNotFound` for an unknown name, a name in another environment of the org and a name asked by another org; `SetServiceReleases` reading back a current and a previous id through both reads, then a new current with an empty previous; and `ErrNotFound` for an unknown or another org's service, with the first org's ids kept. It also adds `runRollbacksConformance`, run as `TestRollbacksConformanceSQLite` and `TestRollbacksConformancePostgres`, covering: starting a rollback and reading it back in the in-flight list; refusing an unknown or another org's service with `ErrNotFound` and writing nothing; finishing as each final status, refusing a second finish with `ErrRollbackState` and a non-final status with an error that names the three final ones, leaving the rollback running; the in-flight read in created order with another server's and finished rollbacks left out; and a second org getting `ErrNotFound` from a finish, no rows from the in-flight reads of the first org's server and no change to the first org's rollback. `TestPostgresRLSOnRollbacks` checks the `org_isolation` policy on `rollbacks` as the application role (unset, empty and nonexistent `app.org_id` return nothing; an `INSERT` for another org is refused by `WITH CHECK`; `rowsecurity` is on), and `TestRollbackConstraintsEnforced` checks the `status` `CHECK` and the release-column defaults and nullability with raw SQL on a migrated SQLite database.


### Recovery recipients, environment keys and secrets (W3-2)

Specified by the weeks 2–3 preflight (`docs/specs/secrets.md`). Row-level tables; they hold public keys and ciphertext only.

- `recovery_recipients` — `org_id`, `id`, `recipient` (an age X25519 recipient, `age1…`), `created_at`. W3-2 allows one per org (ADR-0003 decision 12).
- `environment_keys` — `org_id`, `environment_id` (unique), `public_key` (`age1…`), `created_at`.
- `wrapped_environment_keys` — `org_id`, `environment_id`, `recipient_kind` (`recovery`, or `agent` from W4-8), `recipient_id` (a `recovery_recipients.id` or a `server_id`), `ciphertext` (an age file), `created_at`; unique `(org_id, environment_id, recipient_kind, recipient_id)`.
- `secrets` — `org_id`, `environment_id`, `kind` (`env` or `registry`), `name`, `ciphertext` (an age file), `updated_at`; unique `(org_id, environment_id, kind, name)`.

### Soft delete (W5-5c, W5-8a)

Specified by the week 5 preflight. `services` gains `deleted_at` and `purge_detail` (W5-5c); `environments` and `projects` gain `deleted_at` (W5-8a). Every list, lookup and `ResolveTarget` leaves deleted rows out. The rules, the store methods and their conformance cases are `docs/specs/delete.md`.

### Server deploy keys (W3-8)

`servers` gains `deploy_key`: the server's SSH deploy public key from its latest `Heartbeat` (`docs/specs/protocol.md`), or empty. It is a public key.

### Sessions, links and step-ups (W4-1a, W4-1d)

`session_hashes` (lookup table) and the `session_id` and `step_up_id` columns of `audit_events` are added by W4-1a. `link_token_hashes` (lookup table) and `step_ups` (row-level) are added by W4-1d. All four are specified in `docs/specs/auth.md`.

`link_token_hashes` holds the `TEXT` key `hash`, the `SHA-256` hex of an `mg_` link bearer, mapped to the link's `id` and `user_id`, with `expires_at` and `used_at` (NULL until the link is used). It has no `org_id` and no RLS, because it is read before an org is known.

`step_ups` has these columns: `org_id` (`TEXT NOT NULL REFERENCES orgs (id)`), `id` (the primary key), `user_id`, `operation`, `input_digest`, `created_at` and `expires_at` (`TEXT NOT NULL`); `token_id`, `session_id`, `project_id` and `environment_id` (`TEXT NOT NULL DEFAULT ''`); and `confirm_hash`, `signed_in_at`, `approved_at`, `declined_at` and `used_at` (nullable, NULL until set). A `CHECK` requires exactly one of `token_id` and `session_id` to be non-empty: `(token_id = '') <> (session_id = '')`, which is boolean SQL on Postgres and integer SQL on SQLite. On Postgres the table has `ENABLE ROW LEVEL SECURITY` and the `org_isolation` policy, `USING` and `WITH CHECK` both `(org_id = nullif(current_setting('app.org_id', true), ''))`; the application role gets its grants through `pg.Migrate`, which is unchanged.

The link-token rules:

- `AddLinkToken` mints the row's `id`, stores the hash with its expiry and returns the link token. `LinkToken` has no hash field, so no store method takes or returns the bearer. A repeated hash is `ErrDuplicate` and leaves the first token intact.
- `UseLinkToken` consumes the row in one conditional `UPDATE` that matches only an unused token whose `expires_at` is after `now`, and returns the row. The comparison is lexical RFC3339 UTC text and strict, so a token is refused at the second it expires.
- A refused use reports `ErrNotFound` for an unknown id, `ErrTokenUsed` for a token used before it expired, and `ErrTokenExpired` for an unused token at or after its expiry. The used check comes first, so a token used before it expired stays `ErrTokenUsed` whatever the expiry.
- A token seen at or after its expiry is marked used at that expiry, so its expiry is final: every later use reports `ErrTokenExpired`, never a token whose window has closed. A use that succeeds always stamps a time before the expiry, which is how the two are told apart. No method extends `expires_at`.

The step-up rules:

- `CreateStepUp` mints the row's `id` and stores it in the org with `created_at` now and `expires_at` the given expiry, then returns the stored row. The caller's `OrgID`, `ID`, times and markers are ignored, and `confirm_hash` and the four markers stay NULL. An empty `UserID`, `Operation` or `InputDigest`, and credentials that are both set or both empty, are refused before any SQL runs, with an error that names the rule and no row written.
- `StepUp` reads one row inside the org-bound transaction with `org_id` in the `WHERE`, so an id in another org is `ErrNotFound`. An unset marker column reads back as empty text.

W4-1d adds `runLinkTokensConformance`, run as `TestLinkTokensConformanceSQLite` and `TestLinkTokensConformancePostgres`, covering: adding a link token by hash and reading back its id, user and expiry; using it once before expiry and refusing a second use with `ErrTokenUsed`, including after the expiry has passed; `ErrTokenExpired` for an unused token at and after its expiry, and on every later use; `ErrNotFound` for an unknown id; `ErrDuplicate` for a repeated hash with the first token still usable; and two tokens of one user staying independent. It also adds `TestLinkTokenStoredHashedOnly`, which proves on both backends that the dump of every table holds the hex SHA-256 and no plaintext or `mg_` value. `runStepUpsConformance`, run as `TestStepUpsConformanceSQLite` and `TestStepUpsConformancePostgres`, covers: creating a step-up with a token credential, a project and an environment target, and reading every field back with empty markers; a session credential with no target reading back empty credential and target columns; the caller's id, org, times and markers being ignored; the five refusals writing no row; `ErrNotFound` for an unknown id; and a second org reading none of the first's rows. `TestPostgresRLSOnStepUps` checks the `org_isolation` policy on `step_ups` as the application role (unset, empty and nonexistent `app.org_id` return nothing; an `INSERT` for another org is refused by `WITH CHECK`; `rowsecurity` is on), and `TestStepUpConstraintsEnforced` checks the credential `CHECK`, the empty-text defaults and the NULL markers with raw SQL on a migrated SQLite database.

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
