# W0-2 — Threat model, ADRs and UI foundations

This ticket writes the documents every later ticket is built and reviewed against. **It is documentation only.** The owner reviews all of it before any product code starts.

## Read first

- `AGENTS.md`
- `docs/plan/build-plan.md`
- `docs/plan/stack.md`, all of it. This is the main source.
- `docs/plan/vision.md` §1 (including §1.2 voice), §4, §5.2 (licensing)
- `docs/plan/milestones.md` §1 and §3

## Rules for this ticket

- **Every statement traces to `docs/plan/`, or is marked _Proposed_.** Do not introduce technologies, numbers or behaviour the plan does not contain without that mark.
- **Do not decide what the plan leaves open.** Put it under "Open questions" at the end of the relevant document, with the options and what each would change.
- **No fallbacks in the decisions.** Where the plan still says "X or Y", the ADR records both candidates and names the spike or owner decision that will pick exactly one. It does not describe one as the fallback for the other.
- Be concise. These are working documents for agents and one human reviewer, not marketing.

## Write

### 1. `docs/threat-model.md`, at most 250 lines

A standalone brief: a reviewer agent given only this file must be able to review any PR.
- **Assets:** host root, user secrets, database backups, tokens, the release signing key, the audit trail.
- **Trust boundaries:** an ASCII diagram covering browser and CLI, control plane, database, agent, proxy, Docker Engine, containers and the user's bucket.
- **Attacker table:** from stack.md §4 — database copy, running control plane, leaked token, malicious image, escape.
- **Honest limits:** the three from stack.md §4, plus the browser-encryption table from stack.md §3.11.
- **Container-spec deny list:** from stack.md §3.2, and how exceptions are scoped.
- **Trusted-core paths:** from `AGENTS.md`.
- **Reviewer checklist:** the questions a reviewer asks of every diff:
  - new protocol fields or messages
  - policy or sanitizer bypass
  - secret plaintext in logs, errors, the database or temp files
  - token scope holes
  - authz-matrix rows missing for a new operation
  - root steps in install scripts
  - systemd unit privileges
  - fallback or dead code
  - tests weakened or removed

### 2. ADRs in `docs/adr/`

Use one template for every ADR: Status · Date · Context · Decision · Consequences · Alternatives rejected · Open questions · Links. Each is at most about 80 lines.

| File | Decision, from the plan |
|---|---|
| `0001-two-process-proxy.md` | `moonglass agent` and `moonglass proxy` are separate processes from one binary; agent → proxy over Caddy's admin API on a Unix socket. Status: Accepted. Open: the proxy upgrade mechanism (systemd socket activation or `SO_REUSEPORT`), decided by SPIKE-1, after which exactly one is built |
| `0002-control-plane-storage.md` | Hosted: one managed Postgres with RLS on `org_id`. Self-hosted: SQLite. One storage interface and one conformance suite. Logical export. Open: the managed Postgres provider |
| `0003-secrets-envelope-keys.md` | Environment keys, agent keys, the owner recovery key, wrapping, grant, rotate, one reveal path. What browser encryption does and does not protect |
| `0004-container-spec-policy.md` | Deny by default, scoped allows in `agent.toml`, checked after Compose interpolation, BuildKit entitlements. Include a _Proposed_ draft of the `agent.toml` shape; it is finalised by ticket W1-1 |
| `0005-mvp-authentication.md` | Hosted: GitHub and email magic links. Self-hosted: bootstrap token and `server login-link`, with no email sign-in. Step-up for approvals. Passkeys and TOTP in v1 |
| `0006-operation-registry.md` | One registry is the only API surface; it generates REST, CLI and later MCP. Scope and `destructive` flag are declared per operation; authz and audit happen in the registry. It lands in week 2 (W2-1) so the API is never written twice |
| `0007-releases-and-updates.md` | Signed releases; canary and stable channels; staged rollout; self-update with automatic revert. The single compatibility window after launch: the control plane speaks the current and previous agent protocol version, and nothing older (build-plan §8) |

Also write `docs/adr/README.md`: one line per ADR, plus the rule that a changed decision means an amendment the owner approves, then one ticket that changes everything and deletes the old form.

Do **not** write ADR-0008 (protobuf codegen tool). Ticket W0-3 writes it.

### 3. `docs/specs/ui.md`, the UI foundations

Inputs:
- `vision.md` §1.2: calm, elegant, premium, soft.
- The owner's priorities: speed, performance, UX and security.
- A super clean, minimal interface **inspired by Linear but original**. Do not copy Linear's layout pixel for pixel, its icons, colours or typography.

Sections:
- **Principles:** five or six short lines, for example "keyboard first", "nothing waits on a spinner that could be instant", "one accent colour".
- **Information architecture:** organisation → projects → environments → services → deploys; servers; approvals; audit; settings. Name the routes.
- **Layout:** sidebar, list, detail; how it collapses at narrow widths.
- **Keyboard model:**
  - a ⌘K palette whose commands are generated from the operation registry, showing only what the signed-in user may run
  - `g` + letter to navigate
  - single keys inside lists
  - `?` opens a sheet of every shortcut
  - focus is always visible
- **Design tokens** (_Proposed_ values):
  - type scale; 4 px spacing grid; radius
  - a neutral grey ramp plus one accent
  - semantic colours for deploy states (building, live, failed, rolled back, waiting for approval, would have rolled back)
  - light and dark themes from the same tokens
- **Components:** the Bits UI primitives used, and the few composite components (list row, status pill, diff view, log stream, command palette, approval card).
- **Motion:** at most 150 ms, ease-out, honours reduced motion.
- **States:** empty states that offer the next action; loading shows a skeleton only after 150 ms; errors say what happened and what to do.
- **Live data:** server-sent events; what updates live and what does not.
- **Performance budgets** (_Proposed_): initial JavaScript gzipped, route change with cached data, no layout shift, the static build embedded in the binary.
- **Security:** a strict CSP with no inline scripts; no third-party scripts or fonts at run time (self-hosted fonts); secret inputs are write-only and never echoed back; destructive actions need a typed confirmation or an approval, never a single click.
- **Accessibility:** WCAG 2.2 AA, contrast in both themes, everything reachable by keyboard.
- **Open questions for the owner:** typeface, accent colour, logo and mark, density default.

## Acceptance

- Every ADR and the threat model point to the stack.md or vision.md sections they come from.
- A search finds no technology in these documents that the plan does not name, unless it is marked _Proposed_.
- No document describes a fallback; every "X or Y" names the spike or decision that picks one.
- `make ci` is still green.
- A single commit: `W0-2: Threat model, ADRs and UI foundations`.

## Report, then stop

Paste the file list with line counts. Then list every _Proposed_ item and every open question, grouped by document, so the owner can decide them in one pass. Stop and wait for review.
