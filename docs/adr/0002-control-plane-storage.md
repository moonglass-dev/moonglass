# ADR-0002: Control-plane storage

- **Status:** Accepted
- **Date:** 2026-11-16

## Context

One person operates the hosted service, and agents write the code (S intro). Tenant isolation must survive a missing `WHERE` clause. Self-hosting must stay a single static binary with no database server to run. Nothing may need its own on-call (S rule 5).

## Decision

1. **Hosted: one managed Postgres.** Every table carries `org_id`, except the lookup tables in decision 12. Each request sets `app.org_id` for its transaction, and row-level security policies refuse rows from any other org (S§3.7).
2. **Self-hosted: SQLite** through the pure-Go driver `modernc.org/sqlite`. The same `org_id` columns exist, so the schema has the same shape everywhere.
3. **One storage interface, two implementations** (`store/pg`, `store/sqlite`). Nothing outside the storage package writes SQL.
4. **One conformance suite** runs against both backends on every change: the same fixtures, queries and migrations. On Postgres it runs with RLS on and includes a query with the `org_id` filter deliberately removed, which must return nothing. A CI check confirms the two schemas describe the same tables and columns (S§3.7, S§7).
5. **SQL stays in the common subset.** Postgres-only features (RLS policies, `LISTEN/NOTIFY`) are allowed only inside the storage package.
6. **Control-plane nodes are stateless.** A command reaches the node holding an agent's connection through Postgres `LISTEN/NOTIFY`; there is no broker.
7. **Export is logical:** a versioned bundle that either backend can import (S§3.7, V§6).
8. **Control-plane backup:** the provider's point-in-time recovery on hosted. On self-hosted, a scheduled consistent snapshot (`VACUUM INTO`), age-encrypted to the owner recovery key and uploaded to the user's bucket (S§3.7, M§3 week 7).
9. **Multi-user from the first schema:** organisations, memberships and roles exist from day one (M§1). Self-hosted uses the same schema and may hold several organisations; bootstrap creates one (W7-1).
10. **Database roles.** The application connects as a role that does not own the tables and cannot bypass RLS. Migrations run as a different role. Without this, RLS does not bind the application.
11. **Migrations** are numbered plain SQL files per backend, applied by a small runner inside `internal/store`. No migration dependency.
12. **Lookups before the org is known** (a session or token by its hash, an agent by its public key, a user by external identity, a user's orgs) read a small fixed set of lookup tables without RLS. Each row holds only a lookup key (a hash, an agent public key or an external identity ID), the IDs it maps to (`user_id`, `org_id`, the row's own ID), an expiry and a revoked marker. Never a secret, a wrapped key, a plaintext token, scopes or names. Everything else is read from RLS tables after `app.org_id` is set from the lookup's result. The list of lookup tables lives in one file in `internal/store`, and the conformance suite asserts it against the schema on both backends.
13. **CI runs Postgres 18** (`postgres:18`) as the W1-2 service, the current stable major. CI moves to the provider's major when the provider is chosen, in one change.

The agent's own SQLite database on each server is separate and not covered here (S§2).

## Consequences

- RLS is a second lock: a query that forgets its filter returns nothing instead of another customer's rows.
- There are two backends to keep in step. The conformance suite and the schema check are permanent and never red.
- The managed provider supplies standby, failover and recovery; we run no database cluster.
- Scaling hosted means adding a stateless node, not sharding.
- `internal/store` is trusted core: RLS policies and migrations are reviewed line by line (B§3).

## Alternatives rejected

- **Per-org SQLite files plus Litestream on hosted:** thousands of replication streams, file-level restores and a routing table.
- **Postgres for self-hosted:** breaks the single-binary install.
- **A distributed SQLite with consensus:** a cluster to operate.
- **Turso:** too young to hold a control plane.
- **One Postgres function per pre-org lookup:** Postgres-only logic with no SQLite equivalent, where the backends can least afford to differ.

## Open questions

1. **Managed Postgres provider and region.** Owner decision by week 4 (S§9 open 1). It must offer point-in-time recovery, a standby and `LISTEN/NOTIFY`, which rules out some poolers in transaction mode. The choice fixes the production Postgres major, how nodes connect, and where data resides. Left open on purpose; CI uses decision 13 until then.

## Links

- S rule 5, S§2, S§3.7, S§7, S§9 (decision and open 1)
- V§6 (export), M§1, M§3 week 7
- B§6: W1-2, W7-1, W7-2, W7-5
- `docs/threat-model.md` §3, boundary B2
