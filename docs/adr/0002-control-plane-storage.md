# ADR-0002: Control-plane storage

- **Status:** Accepted
- **Date:** 2026-11-16

## Context

One person operates the hosted service, and agents write the code (S intro). Tenant isolation must survive a missing `WHERE` clause. Self-hosting must stay a single static binary with no database server to run. Nothing may need its own on-call (S rule 5).

## Decision

1. **Hosted: one managed Postgres.** Every table carries `org_id`. Each request sets `app.org_id` for its transaction, and row-level security policies refuse rows from any other org (S§3.7).
2. **Self-hosted: SQLite** through the pure-Go driver `modernc.org/sqlite`. The same `org_id` columns exist, so the schema has the same shape everywhere.
3. **One storage interface, two implementations** (`store/pg`, `store/sqlite`). Nothing outside the storage package writes SQL.
4. **One conformance suite** runs against both backends on every change: the same fixtures, queries and migrations. On Postgres it runs with RLS on and includes a query with the `org_id` filter deliberately removed, which must return nothing. A CI check confirms the two schemas describe the same tables and columns (S§3.7, S§7).
5. **SQL stays in the common subset.** Postgres-only features (RLS policies, `LISTEN/NOTIFY`) are allowed only inside the storage package.
6. **Control-plane nodes are stateless.** A command reaches the node holding an agent's connection through Postgres `LISTEN/NOTIFY`; there is no broker.
7. **Export is logical:** a versioned bundle that either backend can import (S§3.7, V§6).
8. **Control-plane backup:** the provider's point-in-time recovery on hosted. On self-hosted, a scheduled consistent snapshot (`VACUUM INTO`), age-encrypted to the owner recovery key and uploaded to the user's bucket (S§3.7, M§3 week 7).
9. **Multi-user from the first schema:** organisations, memberships and roles exist from day one (M§1).

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

## Open questions

1. **Managed Postgres provider and region.** Owner decision by week 4 (S§9 open 1). It must offer point-in-time recovery, a standby and `LISTEN/NOTIFY`, which rules out some poolers in transaction mode. The choice fixes the Postgres major version, how nodes connect, and where data resides. W1-2 runs Postgres as a CI service before then, so the CI version needs an interim owner choice.
2. **Database roles.** _Proposed:_ the application connects as a role that does not own the tables and cannot bypass RLS, and migrations run as a different role. The plan requires RLS but does not say this; without it, RLS does not bind the application. W1-2 needs a yes or no.
3. **Rows read before the org is known.** A session or token is looked up by its hash, and an agent by its key, before any `app.org_id` can be set. The plan does not say how those reads pass RLS. Options: (a) a small set of lookup tables without RLS, holding only a hash and the `org_id` it maps to; (b) one narrowly scoped database function per lookup. Option (a) keeps SQL in the common subset; option (b) keeps RLS on every table. W1-2 decides the schema either way.
4. **Migration runner.** The plan says plain SQL and standard library first, and names no tool. Options: (a) a small runner inside `internal/store` over numbered SQL files per backend; (b) a dependency, which needs its own ADR. This is trusted core, so (b) adds reviewed surface.
5. **Organisations on self-hosted.** The schema carries `org_id` everywhere, but the plan does not say whether a self-hosted install holds one organisation or several. It changes the bootstrap flow (W7-1) and the UI routes (`docs/specs/ui.md`).

## Links

- S rule 5, S§2, S§3.7, S§7, S§9 (decision and open 1)
- V§6 (export), M§1, M§3 week 7
- B§6: W1-2, W7-1, W7-2, W7-5
- `docs/threat-model.md` §3, boundary B2
