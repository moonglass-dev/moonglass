# ADR-0002: Control-plane storage

- **Status:** Accepted
- **Date:** 2026-11-16

## Context

Self-hosting must stay a single static binary with no database server to run, and nothing may need its own on-call (S rule 5). Cloud runs control planes for many accounts (V§5.2), and each account's data must stay out of every other account's reach. Agents write the code, so one backend with one set of tests keeps every store ticket small.

## Decision

1. **SQLite is the only backend,** through the pure-Go driver `modernc.org/sqlite` (ADR-0010). Every control plane uses it: a self-hosted one, and a Cloud one, which is one instance per account on the same binary, run by us. There is no shared multi-tenant database.
2. **One storage interface,** `internal/store`, with one implementation, `store/sqlite`. Nothing outside the storage package writes SQL.
3. **One conformance suite** runs on every change: the same fixtures, queries and migrations, including a second org that must see none of the first org's rows through any store method (S§7).
4. **A control plane is one process.** Commands are never routed between control-plane nodes.
5. **Moving a control plane** is `moonglass export` and an import on the other side, with the agents re-enrolled without a restart (W7-2); restoring its backup (decision 6) on another box also works.
6. **Control-plane backup:** a scheduled consistent snapshot (`VACUUM INTO`), age-encrypted to the owner recovery key and uploaded to the user's bucket (S§3.7, W7-5). A Cloud instance is backed up the same way, to our bucket.
7. **Multi-user from the first schema:** organisations, memberships and roles exist from day one (M§1). A control plane may hold several organisations, all belonging to one customer: the operator of a self-hosted box, or one Cloud account. Bootstrap creates the first (W7-1); members join by invitation (LN-3).
8. **Migrations** are numbered plain SQL files, applied by a small runner inside `internal/store`. No migration dependency.
9. **Lookups before the org is known** (a session or an API token by its hash, an agent by its public key, a user by external identity, a user's orgs) read a small fixed set of lookup tables. `token_hashes` is one of them. Each row holds only a lookup key (a hash, an agent public key or an external identity ID), the IDs it maps to (`user_id`, `org_id`, the row's own ID), an expiry and a revoked marker. Never a secret, a wrapped key, a plaintext token, scopes or names. Everything else is read with the org taken from the lookup's result, and every store method filters on it. The list of lookup tables lives in one file in `internal/store`, and the conformance suite asserts it against the schema.

The agent's own SQLite database on each server is separate and not covered here (S§2, ADR-0010).

## Consequences

- A store ticket writes one migration and one set of tests.
- The org filter in each store method is the lock between orgs. Every org in one control plane belongs to one customer, so a missing filter could show one of a customer's orgs to that same customer, never another customer's data. The second-org checks in the conformance suite are permanent.
- Cloud is many small processes, upgraded and backed up one by one, with nothing shared between accounts.
- `internal/store` is trusted core: migrations and the org filters are reviewed line by line (B§1).

## Alternatives rejected

- **A shared Postgres with row-level security for many accounts:** a second backend for every store ticket, and one database whose policy bug would reach every account.
- **Per-org SQLite files plus Litestream in one shared service:** thousands of replication streams, file-level restores and a routing table.
- **Postgres for self-hosted:** breaks the single-binary install.
- **A distributed SQLite with consensus:** a cluster to operate.
- **Turso:** too young to hold a control plane.

## Links

- S rule 5, S§2, S§3.7, S§7, S§9
- V§5.2, V§6, M§1
- B§3: FA1-1, W1-2, W7-1, W7-5, W7-2, LN-3, V1-2
- `docs/threat-model.md` §3, boundary B2
