# ADR-0002: Control-plane storage

- **Status:** Accepted
- **Date:** 2026-11-16
- **Amended:** 2026-11-22, SQLite only (the plan audit, `docs/review/plan-audit.md`; the owner approves on its PR). Decisions 1, 3 to 6, 10 and 13 are withdrawn, and decisions 2, 4, 7, 8, 11 and 12 are rewritten. FA1-1 deletes the Postgres backend, its RLS tests and its CI service, and the Postgres text of `docs/specs/storage.md`, in one change.

## Context

One person operates every control plane, and agents write the code (S intro). Self-hosting must stay a single static binary with no database server to run. Nothing may need its own on-call (S rule 5).

The 2026-11-16 decision added a shared Postgres with row-level security for a hosted service with a free tier of many orgs. The plan audit removed that free tier and put self-hosted first: the paying customers are a few dozen teams and agencies (V§5). Each of them can have its own control plane, so the shared database, and the second backend every store ticket had to match, have no job left.

## Decision

1. _Withdrawn 2026-11-22._ Was: hosted on one managed Postgres with row-level security on `org_id`.
2. **SQLite is the only backend,** through the pure-Go driver `modernc.org/sqlite`. Every control plane uses it: a self-hosted one, and a managed one, which is one instance per customer on the same binary, run by us. There is no shared multi-tenant database.
3. _Withdrawn 2026-11-22._ Was: one storage interface, two implementations. There is one interface, `internal/store`, with one implementation, `store/sqlite`. Nothing outside the storage package writes SQL.
4. **One conformance suite** runs on every change: the same fixtures, queries and migrations, including a second org that must see none of the first org's rows through any store method (S§7).
5. _Withdrawn 2026-11-22._ Was: SQL kept to the common subset of Postgres and SQLite.
6. _Withdrawn 2026-11-22._ Was: stateless control-plane nodes routing commands through Postgres `LISTEN/NOTIFY`. A control plane is one process.
7. **Moving a control plane** is its backup (decision 8) restored on another box. A logical export is v1 (W7-2).
8. **Control-plane backup:** a scheduled consistent snapshot (`VACUUM INTO`), age-encrypted to the owner recovery key and uploaded to the user's bucket (S§3.7, W7-5). A managed control plane is backed up the same way.
9. **Multi-user from the first schema:** organisations, memberships and roles exist from day one (M§1). A control plane may hold several organisations, all belonging to one customer; bootstrap creates one (W7-1). Inviting members is the commercial module (V§5.1).
10. _Withdrawn 2026-11-22._ Was: separate database roles so RLS binds the application.
11. **Migrations** are numbered plain SQL files, applied by a small runner inside `internal/store`. No migration dependency.
12. **Lookups before the org is known** (a session or an API token by its hash, an agent by its public key, a user by external identity, a user's orgs) read a small fixed set of lookup tables. `token_hashes` is one of them. Each row holds only a lookup key (a hash, an agent public key or an external identity ID), the IDs it maps to (`user_id`, `org_id`, the row's own ID), an expiry and a revoked marker. Never a secret, a wrapped key, a plaintext token, scopes or names. Everything else is read with the org taken from the lookup's result, and every store method filters on it. The list of lookup tables lives in one file in `internal/store`, and the conformance suite asserts it against the schema.
13. _Withdrawn 2026-11-22._ Was: CI runs Postgres 18 as a service.

The agent's own SQLite database on each server is separate and not covered here (S§2).

## Consequences

- There is one backend, so a store ticket writes one migration and one set of tests.
- Without row-level security, the org filter in each store method is the only lock between orgs. Every org in one control plane belongs to one customer, so a missing filter can show one of a customer's orgs to that same customer, never another customer's data. The second-org checks in the conformance suite stay permanent.
- A managed fleet is many small processes, upgraded and backed up one by one, instead of one shared service.
- `internal/store` is trusted core: migrations and the org filters are reviewed line by line (B§3).

## Alternatives rejected

- **A shared Postgres with row-level security for many customers:** built for a free hosted tier that no longer exists, and a second backend for every store ticket (the 2026-11-16 decision, withdrawn).
- **Per-org SQLite files plus Litestream in one shared service:** thousands of replication streams, file-level restores and a routing table.
- **Postgres for self-hosted:** breaks the single-binary install.
- **A distributed SQLite with consensus:** a cluster to operate.
- **Turso:** too young to hold a control plane.

## Links

- S rule 5, S§2, S§3.7, S§7, S§9
- V§5, V§6, M§1
- B§3: FA1-1, W1-2, W7-1, W7-5; B§7: W7-2
- `docs/threat-model.md` §3, boundary B2
