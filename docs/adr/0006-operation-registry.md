# ADR-0006: The operation registry

- **Status:** Accepted
- **Date:** 2026-11-16

## Context

Moonglass offers the same operations to people, scripts and coding agents. If REST, the CLI and MCP were each written by hand, an operation could be reachable through one surface without the scope check or audit record the others have. Another product in the category shipped an MCP deploy tool that skipped its own deploy queue and took an app down (S§3.12).

## Decision

1. **One registry is the only API surface.** Every operation is declared once, with its inputs, outputs, scope, lowest role and a `destructive` flag (S§3.12). The flag has no default: an operation that omits it does not build. No operation exists outside the registry.
2. **Surfaces are generated from it as checked-in files:** the OpenAPI description, the CLI subcommands, the UI's typed client and, from W5-4, the MCP tool list. They are never edited by hand, and CI regenerates them and fails if anything differs, the same rule W0-3 has for protobuf. Huma serves the REST routes from the registry (S§2, B W2-1, B W5-4).
3. **Authorisation is declared per operation and enforced in the registry,** never in hidden middleware and never by a flag the caller sets (`AGENTS.md`, S§3.12). A call is allowed only when all three hold:
   - **Role.** The caller's role (owner, admin, member, viewer; V§5.2) reaches the operation's declared lowest role. A token acts with its owning human's role. The declared role follows one rule: viewer for operations that only read, and for operations that act only on the caller's own sessions and identities (amended 2026-11-18, `docs/specs/auth.md`); member for changes that are not destructive; admin for destructive operations; owner for secret reveal, changing secret recipients and membership changes. A build check fails a destructive operation declared below admin.
   - **Scope.** Some scope row on the token matches. A row is (project, environment, operation): project and environment are opaque IDs, the operation is its registry name, and any of the three may be `*`. A W2-1 owner token is one row, `* * *`; W5-1's agent tokens use the same fields.
   - **Gates.** The three gates below pass.
4. **Three separate gates,** none a substitute for another. Each needs a sign-in fresher than ten minutes, checked on the server at the moment of approval (S§3.10, ADR-0005):
   - **`destructive`:** the operation deletes, irreversibly replaces or stops a service, a release, a volume or stored data. Deploy and rollback are not destructive. Called by an agent identity, it returns "pending approval" with a link; a human approves in the UI or with `moonglass approve` (S§3.12).
   - **Protected-environment deploy:** any deploy or source change in a protected environment needs a human approval, whoever asks. No role skips it. An org's first environment starts protected and uses the plan's allowance (one on Free, V§5.3); later environments start unprotected until someone switches them on. The first-deploy wizard (W7-7) asks the same question and never overrides a flag already set.
   - **Secret-recipient change:** granting, rotating or removing a server from an environment key is owner-only.
5. **Audit events are written by the registry, never by handlers.** They are hash-chained and record which human or agent identity acted (B W2-1, S§3.10).
6. **Routes that are not operations** (the Moonglass agent's WebSocket, the sign-in callback, the identity-link, step-up and login-link pages, server-sent event streams, the static UI) are one short fixed list in one file beside the registry. Each entry states its path, method and authorisation. A test asserts every mounted route is an operation or on that list, and that each entry's authorisation holds.
7. **Local-only CLI commands** (`version`, `keys init`, `agent enroll`, `server login-link`) are one list beside the registry, each stating what it touches. They are not in REST, MCP or the palette. A test asserts every subcommand is generated or on that list.
8. **The registry lands in week 2 (W2-1),** so the API is never written twice. Week 5 adds MCP and scoped agent tokens to it (B§6, M§3 week 2). The ⌘K palette lists its operations (S§3.16, B W2-S).
9. **Registry operations are a contract.** Changing one needs an approved amendment and one ticket that updates every user (B§8).

## Consequences

- "MCP for everything" costs nothing extra, and nothing is reachable by a coding agent without also being scoped and audited (S§3.12).
- The authorisation matrix is exhaustive: every operation × every token scope × every role, asserting allow or deny. It is a permanent suite (S§7). W5-1 completes it.
- Every change to an operation's shape shows as a diff in generated files.
- Discipline is the cost: an operation or route outside the two declared places is a defect, and the reviewer checklist asks for it.
- Registry authorisation is reviewed at Core level (B§3): `internal/registry` is trusted core. API handlers stay Standard.

## Alternatives rejected

- **Hand-written REST, CLI and MCP surfaces:** three places for a scope check to be missing (S§3.12).
- **Building the registry in week 5 with the agent-safety work:** the week 2 to 4 API would be written twice (B W2-1).
- **Authorisation in middleware:** it hides the rule away from the operation it guards (`AGENTS.md`).
- **A free list of allowed roles per operation:** nothing for a reviewer to check it against.
- **Protected deploys and recipient changes as `destructive`:** one gate would stand in for the others, and a deploy would need admin.
- **Streams, callbacks or local commands inside the registry:** more shapes in trusted core, and every generator must skip them.
- **Surfaces built only at start-up:** a change to an operation would be visible only in Go source.

## Links

- S§2 (API, MCP), S§3.10, S§3.12, S§3.16, S§7, S§8; V§5.2 (roles)
- M§3 weeks 2 and 5
- B§3, B§6: W2-1, W2-S, W5-1, W5-2, W5-4, W5-5; B§8
- `docs/threat-model.md` §3 boundary B1, checklist item 5
