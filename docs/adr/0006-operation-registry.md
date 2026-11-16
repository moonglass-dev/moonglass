# ADR-0006: The operation registry

- **Status:** Accepted
- **Date:** 2026-11-16

## Context

Moonglass offers the same operations to people, scripts and coding agents. If REST, the CLI and MCP were each written by hand, an operation could be reachable through one surface without the scope check or audit record the others have. Another product in the category shipped an MCP deploy tool that skipped its own deploy queue and took an app down (S§3.12).

## Decision

1. **One registry is the only API surface.** Every operation is declared once, with its inputs, outputs, required scope and a `destructive` flag (S§3.12). No endpoint exists outside the registry (S§2).
2. **Surfaces are generated from it:** REST routes through Huma, which also produces the OpenAPI description; CLI subcommands; and, from week 5, MCP tools through the official Go SDK (S§2, B W2-1, B W5-4).
3. **Authorisation is declared per operation and enforced in the registry,** never in hidden middleware and never by a flag the caller sets (`AGENTS.md`, S§3.12).
4. **Audit events are written by the registry, never by handlers.** They are hash-chained and record which human or agent identity acted (B W2-1, S§3.10).
5. **A destructive operation called by an agent identity returns "pending approval" with a link.** A human approves in the UI with step-up, or with `moonglass approve` (S§3.12).
6. **The registry lands in week 2 (W2-1),** earlier than the week 5 agent-safety work, so the API is never written twice. Week 5 adds MCP and scoped agent tokens to the same registry (B§6, M§3 week 2).
7. **Clients are derived, not hand-written:** the UI's typed API client is generated from the OpenAPI description, and the ⌘K palette lists the registry's operations (S§3.16, B W2-S).
8. **Registry operations are a contract.** Changing one needs an approved amendment and one ticket that updates every user (B§8).

## Consequences

- "MCP for everything" costs nothing extra, and nothing is reachable by a coding agent without also being scoped and audited (S§3.12).
- The authorisation matrix can be exhaustive: every operation × every token scope × every role, asserting allow or deny. It is a permanent suite (S§7).
- Discipline is the cost: a quick endpoint added outside the registry is a defect, and the reviewer checklist asks for it.
- Registry authorisation is reviewed at Core level (B§3).

## Alternatives rejected

- **Hand-written REST, CLI and MCP surfaces:** three places for a scope check to be missing (S§3.12).
- **Building the registry in week 5 with the agent-safety work:** the week 2 to 4 API would be written twice (B W2-1).
- **Authorisation in middleware:** it hides the rule away from the operation it guards (`AGENTS.md`).

## Open questions

1. **Scope vocabulary and the role mapping.** The plan says tokens carry project, environment and allowed operations, and that roles are owner, admin, member and viewer (S§3.10, V§5.2). It does not define the scope names or which role may run which operation. W2-1 needs the minimum for owner tokens; W5-1 needs all of it.
2. **What counts as `destructive`.** The plan gives examples (deleting a production database) but no rule. _Proposed:_ the flag has no default, so an operation that omits it does not build.
3. **HTTP routes that are not operations.** The agent's WebSocket endpoint, sign-in callbacks, server-sent event streams and the static UI are not request-and-response operations, yet "no endpoint exists outside the registry". Options: (a) the registry gains declared entries for streams and callbacks, each with its own authorisation, so the matrix covers them; (b) one short, fixed list of non-registry routes in a single file, covered by its own test. Decide before W2-1.
4. **Local-only CLI commands.** `moonglass version`, `keys init`, `agent enroll` and `server login-link` run on a machine and call no API. The plan does not say whether they are declared in the registry or beside it.
5. **How surfaces are generated.** "Generated" can mean built at start-up from the registry, or written to checked-in files by a build step. The second brings the rule that generated code is never edited by hand and a CI staleness check, as W0-3 has for protobuf.

## Links

- S§2 (API, MCP), S§3.10, S§3.12, S§3.16, S§7, S§8
- M§3 weeks 2 and 5
- B§3, B§6: W2-1, W2-S, W5-1, W5-2, W5-4, W5-5; B§8
- `docs/threat-model.md` §3 boundary B1, checklist item 5
