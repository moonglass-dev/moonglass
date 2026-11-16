# Moonglass — Build plan

_2026-11-16. How Moonglass gets built, one reviewable ticket at a time. It builds on `stack.md` (S§n) and `milestones.md` (M§n). It takes the agent-driven build playbook as a set of ideas, not a set of rules._

**Short version**

- Every change is one ticket, one branch, one fresh agent session, one PR. It merges only when CI is green, a reviewer agent (Opus) has checked it, and you have reviewed it at the ticket's level.
- About 55 tickets over weeks 0–8, each sized to review in 15–30 minutes. **Your review time is the real constraint.** If review falls behind, the week slips; you do not review less.
- A gate at the end of every week, with a demo you run yourself and a rule for when to stop.
- Clean from the first commit. No placeholders, no fallbacks, no code for later tickets. When a decision changes, the old code is deleted in the same change.

---

## 1. Roles

| Role | Who | Does |
|---|---|---|
| **Owner** | You | Decisions and ADRs; approves specs and plans; reviews at the ticket's level; merges; runs Friday demos; talks to alpha users |
| **Groundwork** | Fable, while it is available | Week 0 only: W0-1 and W0-2, the repository and the contract documents |
| **Implementer** | Sonnet, in a fresh Claude Code session for each ticket, from week 0 (W0-3) onward | One ticket, test first, pastes evidence, stops |
| **Reviewer** | Opus, in a fresh, read-only session. Never the model that wrote the code | Checks the diff against the spec and the threat model. Correctness only, no style |
| **Spec partner** | A planning session | Interviews you, then drafts specs and ADRs. Writes no code |

The implementer never reviews its own work, and the reviewer never sees the implementer's conversation.

---

## 2. The loop, for every ticket

1. **Spec ready.** The ticket links an approved `docs/specs/<area>.md` or ADR. If a blocking decision is still open, the ticket is not ready.
2. **Ticket filed** from the template (`.github/ISSUE_TEMPLATE/ticket.md`): outcome, packages it may touch, acceptance tests by name, verify commands, security notes, out of scope.
3. **Plan** (prompt T2, plan mode, no edits). You approve the plan or correct it in the ticket.
4. **Implement** (T3, fresh session) on branch `t/<id>-<slug>`. Tests are written and shown failing first. Ends with `make ci` evidence.
5. **CI** on GitHub. Nothing else counts as a pass.
6. **Review** (T4, Opus). Findings go in the PR.
7. **Fix** (T5) if there are findings. **Anti-slop pass** (T6) if the code works but is untidy.
8. **You review** at the ticket's level (§3), then squash-merge.

**If an implementer drifts twice, stop the session.** Sharpen the ticket and start fresh. Don't argue with a long context.

---

## 3. Review levels

| Level | Applies to | Reviewer agent | You |
|---|---|---|---|
| **Core** | `internal/protocol`, `internal/policy`, `internal/keys`, `internal/auth`, `internal/store` (RLS and migrations), registry authorisation, install script and systemd units, release signing and self-update | Always, with the threat-model checklist | **Every line.** You can explain each function before merging |
| **Standard** | Deploy, agent runtime, proxy, server, API handlers, CLI, backups, import | Always | The test diff first, then error paths and anything touching Docker, the filesystem or the network |
| **Light** | Docs, UI screens, scripts, fixtures, benchmark, site | Always | Skim; check UI against `docs/specs/ui.md` |

Only one Core ticket is in review at a time.

---

## 4. Lanes

- **Primary lane:** one significant ticket at a time, either in the week's demo path or in the trusted core. You watch the first minute of the session.
- **Secondary lane:** only tickets marked *secondary* below, in a separate `git worktree`, never touching the primary ticket's packages. Rebase it after the primary merges, not during.
- **Background:** research, test fixtures, the benchmark, the site. Each gets its own branch and PR.

Tests and `make dev` must be safe to run in two worktrees at once: ports from `:0`, temp data directories, and Docker resources labelled per test run and cleaned up afterwards. `AGENTS.md` states this.

---

## 5. Gates

Each gate is a Friday demo you run on the freshly reinstalled fleet (M§1), plus criteria. **Stop rule:** a failed gate means the next week's primary lane works only on the failure.

| Gate | Passes when | If it fails |
|---|---|---|
| **G0** end of week 0 | Repo and CI live on GitHub with branch protection. Threat model, ADRs and UI foundations approved. Protocol v0 merged. SPIKE-1 decided and ADR-0001 updated. Waitlist live; at least five interviews booked | Do not start week 1 |
| **G1** skeleton | M§3 week 1 demo. Agent and proxy together ≤ 100 MB. Escape suite green | Fix the design before any deploy code |
| **G2** deploys | Zero failed requests across ten deploys. Rollback < 5 s. A failing pre-deploy step leaves the old release serving. Deploy suite required in CI | No week 3 work on the deploy path until explained and fixed |
| **G3** git and secrets | `git push` goes live on the 2 GB box. Database holds only ciphertext. A second server receives the wrapped key. The disk-fill demo passes | Secrets and disk tickets carry over; git can proceed |
| **G4** rollback and UI | Crash-loop rollback within 2 min. Shadow-mode report, then a real rollback after opt-in. Core screens usable from the keyboard alone | |
| **G5** agent safety | The M§3 week 5 demo. Every operation has authz-matrix rows. Alpha opens | Alpha waits |
| **G6** data | Restore onto a different server with only the recovery key. Compose redeploy leaves the database untouched | |
| **G7** self-hosted and releases | A real Coolify project imported and deployed. Agent upgrade under load with zero failures. A bad canary build reverts itself. Seven-day soak started | Soak restarts after the fix |
| **G8** launch | Soak clean for 7 days. Adversarial review findings fixed or documented as limits. Docs, benchmark and comparison page done | Launch moves (M§8 open decision 1) |

---

## 6. Tickets

Format: **ID — title** · packages · needs · level · lane. Then the acceptance in brief; the full acceptance lives in the ticket. IDs are stable; renumbering is not allowed.

### Week 0 — contracts and plumbing

- **W0-1 — Bootstrap the repository** · repo root, `cmd/moonglass`, CI · — · Light · primary. Prompt: `docs/prompts/01-bootstrap-repo.md`. `make ci` is green on a clean clone. Static amd64 and arm64 builds. `AGENTS.md`, `CLAUDE.md`, test-list snapshot.
- **W0-2 — Threat model, ADRs, UI foundations** · `docs/` · W0-1 · Light (you review all of it) · primary. Prompt: `docs/prompts/02-contracts.md`. Every statement traces to the plan docs or is marked *Proposed*.
- **W0-3 — Protocol v0** · `proto/moonglass/agent/v1`, `internal/protocol` · W0-2 · Core · primary. Covers the envelope, Enroll, Heartbeat and error codes. The codegen tool is chosen in ADR-0008. `make proto` is deterministic, and CI fails if generated code is stale. A fuzz target for envelope decoding. Short enough to audit in one sitting.
- **SPIKE-1 — Memory and proxy upgrade** · branch `spike/1-proxy`, **never merged** · W0-1 · you decide · secondary. Runs the 48-hour test from M§2.2. Output: `docs/spikes/0001-proxy-memory.md` with the numbers, and ADR-0001 updated to name exactly one upgrade mechanism.
- **W0-4 — Fleet scripts** · `scripts/fleet/`, `scripts/measure-memory` · W0-1 · Light · secondary. Wipe and rebuild the three VPSs from a script. The provider token comes from your environment at run time; agents never see it.
- **W0-5 — Waitlist page** · separate repository for the site · — · Light · background. Per M§2.2 experiment 1.

### Week 1 — walking skeleton

- **W1-1 — Container-spec sanitizer and `agent.toml` schema** · `internal/policy`, `testdata/escape/` · W0-2 · Core · primary. Table-driven tests for every deny rule and every scoped allow. Malicious Compose fixtures (socket mount by alias, `privileged` hidden behind interpolation or `extends`) are rejected.
- **W1-2 — Storage foundation** · `internal/store`, `store/pg`, `store/sqlite`, migrations · W0-2 · Core · primary. Tables: orgs, users, memberships, roles, servers, enrolment tokens. RLS on `org_id`. The conformance suite runs against both backends, including a query with the `org_id` filter deliberately removed that must return nothing. Postgres runs as a CI service.
- **W1-3 — Enrolment** · `internal/keys` (agent identity only), `internal/agent`, server endpoint · W0-3, W1-2 · Core · primary. One-time token (a replay fails), the fingerprint printed, the control-plane key pinned. Adds the real `moonglass server` and `moonglass agent enroll` subcommands.
- **W1-4 — Connection and heartbeat** · `internal/agent`, `internal/server` · W1-3 · Core · primary. Outbound WSS, reconnect with backoff and jitter, the server goes offline and recovers in an integration test.
- **W1-5 — Proxy process** · `internal/proxy`, `moonglass proxy` · SPIKE-1 · Standard · secondary. Caddy 2.11.9 embedded; admin API on a Unix socket; counter module; one route over HTTPS. Caddy's internal issuer locally, ACME staging on the fleet.
- **W1-6 — Install script and systemd units** · `scripts/install`, `deploy/systemd/` · W1-4, W1-5 · Core · primary. Service user, hardened units, Docker group access for the agent only. Verifies every downloaded artifact's signature against the two pinned release keys before installing (ADR-0007).
- **W1-7 — Memory budget check** · CI, `scripts/measure-memory` · W1-5, W0-4 · Light · secondary. CI fails above budget.
- → **G1**

### Week 2 — deploy, switch, roll back

- **W2-1 — Operation registry** · `internal/registry`, `internal/api`, `internal/cli` · W1-2 · Core · primary. Huma REST and the CLI are generated from one registry. Every operation declares its scope and `destructive` flag. Owner tokens, stored hashed. Hash-chained audit events written by the registry, never by handlers. _Moved earlier than M§3 week 5 so the API is never written twice; week 5 adds MCP and scoped agent tokens to the same registry._
- **W2-2 — Protocol: deploy messages and release state machine** · `proto`, `internal/protocol`, `internal/release` · W0-3 · Core · primary. Deploy, SwitchRelease, Rollback, RunPreDeploy. The state machine is pure code with no Docker, fully unit-tested.
- **W2-3 — Deploy executor** · `internal/deploy` · W1-1, W2-2 · Standard · primary. Pull or load an image, create the project network, sanitize, start, record the release in agent SQLite. Real Docker in tests.
- **W2-4 — Switch and drain** · `internal/deploy`, `internal/proxy` · W2-3, W1-5 · Standard · primary. Health-gated switch, connection draining, keep five releases, one deploy per app (newest wins).
- **W2-5 — Rollback and CLI operations** · `internal/deploy`, registry operations · W2-1, W2-4 · Standard · primary. `moonglass deploy`, `rollback`, `releases`. Rollback under 5 s, and audited.
- **W2-6 — Pre-deploy steps** · `internal/deploy` · W2-3 · Standard · primary. A sanitized one-off container from the new image. A failure aborts the deploy. Steps are irreversible by default.
- **W2-7 — Deploy-correctness suite and Docker matrix** · `test/deploy/`, CI · W2-4 · Standard · secondary. The load generator can be built in week 1. Docker 25–29 in CI, with at least one matrix job required to merge.
- **W2-S — UI shell** · `ui/`, `ui/AGENTS.md`, embed · W0-2, W2-1 · Standard (CSP) · secondary. SvelteKit static build embedded in the binary, design tokens, layout, a ⌘K palette listing the registry's operations, and a "servers online" page. No other business logic.
- → **G2**, first public video

### Week 3 — git push to live, secrets, disk

- **W3-1 — Keys** · `internal/keys` · W1-3 · Core · primary. Environment keys, agent X25519, `moonglass keys init` (the recovery key), wrap and unwrap. Test vectors are readable by the stock `age` tool.
- **W3-2 — Secrets: set, deliver, reveal** · `internal/keys`, registry operations, agent · W3-1, W2-1 · Core · primary. The CLI encrypts; the control plane stores ciphertext only; the agent decrypts at deploy. `reveal` re-encrypts to a one-time CLI key.
- **W3-3 — Grant and rotate** · same · W3-2 · Core · primary. `RewrapEnvKey` is refused for protected environments. `secrets grant` checks the fingerprint. `secrets rotate` re-encrypts all values and deletes old wrapped copies.
- **W3-4 — Git sources and builds** · `internal/build`, GitHub App, server deploy key · W2-3 · Standard · primary. Dockerfile builds through buildx with the insecure entitlements denied, plus the build-in-CI template.
- **W3-5 — Private registries and environment variables** · `internal/deploy` · W3-4 · Standard · primary.
- **W3-6 — Disk guard** · `internal/agent/disk` · W2-3 · Standard · secondary. Thresholds, prune order, reserve file, alerts (S§3.13).
- → **G3**

### Week 4 — sign-in, auto-rollback, the real UI

- **W4-1 — Sign-in** · `internal/auth` · W2-1 · Core · primary. GitHub OAuth, email magic links, `__Host-` sessions with list and revoke, and the step-up primitive.
- **W4-2 — Environments** · registry, store · W2-1 · Standard · primary. Protected flag, resource limits.
- **W4-3 — Auto-rollback** · `internal/rollback` · W2-4 · Standard · primary. Crash and health triggers on by default. The error-rate trigger in shadow mode, with "mark right or wrong" (S§5.4).
- **W4-4 — Live logs and current usage** · agent → server → SSE · W1-4 · Standard · primary.
- **W4-5 — Browser secret encryption** · `ui/`, age TypeScript · W3-2, W2-S · Core · primary. Values are write-only. CSP is enforced.
- **W4-6 — Core UI screens** · `ui/` · W2-S, W4-4 · Standard · secondary. Projects, services, deploys, logs, servers, against `docs/specs/ui.md`.
- **W4-7 — Notifications** · `internal/notify` · W4-3 · Standard · secondary. Email, and signed webhooks.
- → **G4**

### Week 5 — the agent-safety story

- **W5-1 — Agent identities and scoped tokens** · registry, `internal/auth` · W2-1 · Core · primary. The authz matrix covers every operation × scope × role.
- **W5-2 — Approvals** · registry, UI, CLI · W5-1, W4-1 · Core · primary. Pending state, a diff of what will change, step-up, `moonglass approve`.
- **W5-3 — Local policy enforcement** · `internal/policy`, agent · W1-1 · Core · primary. Forbid exec, deletes or repositories end to end.
- **W5-4 — MCP from the registry** · `internal/mcp` · W5-1 · Core · primary. Official Go SDK v1.7+, stateless Streamable HTTP, OAuth 2.1.
- **W5-5 — Soft delete and audit view** · registry, UI · W2-1 · Standard · secondary. Includes `moonglass audit verify` for the hash chain.
- **W5-6 — Agent-safety demo script** · `examples/agent-demo/` · W5-2 · Light · secondary.
- → **G5**, alpha opens

### Week 6 — databases, backups, restore, Compose

- **W6-1 — One-click Postgres and Redis** · `internal/services` · W2-3 · Standard · primary.
- **W6-2 — Backups** · `internal/backup` · W3-1, W6-1 · Core · primary. Dump, zstd, age to the environment key, S3, schedules. No temp files.
- **W6-3 — Restore** · `internal/backup` · W6-2 · Standard · primary. Restore in CI on every change, including onto a fresh server using only the recovery key.
- **W6-4 — Compose import and reconcile** · `internal/compose` · W1-1, W2-3 · Standard · primary. Unchanged services are never touched; changed ones restart in place, listed before you confirm.
- **W6-5 — Compose classifier survey** · `docs/spikes/0002-compose.md` · W6-4 · Light · secondary. Thirty files, counts per class. It sizes the v1 work; it is not product code.
- → **G6**

### Week 7 — self-hosted, portability, releases

- **W7-1 — Self-hosted mode** · `internal/server`, `internal/auth` · W1-2, W4-1 · Core · primary. One box, bootstrap token, `server login-link`, SQLite in the real path.
- **W7-2 — Export, import, re-enrol** · `internal/export` · W7-1 · Standard · primary.
- **W7-3 — `moonglass import`** · `internal/importer` · W6-4, W3-2 · Standard · primary. Compose and Coolify. Values are encrypted locally before upload.
- **W7-4 — Commercial module and hosted entitlements** · separate repository · W2-1 · Standard · secondary. Free tier: one person, two servers, no managed subdomains. The open build contains no plan checks.
- **W7-5 — Control-plane backup** · `internal/server` · W7-1 · Standard · secondary.
- **W7-6 — Release pipeline** · CI, `internal/update` · W1-6 · Core · primary. CI provenance, canary and stable channels, staged rollout, self-update with revert, and the agent's signature verifier. Signing itself starts at W1-6 (ADR-0007).
- **W7-7 — First-deploy wizard** · `ui/` · W4-6 · Light · secondary.
- → **G7**, soak starts

### Week 8 — harden and launch

- **W8-R — Adversarial review** · read-only · — · Core. A reviewer session over the whole trusted core, briefed with the threat model. Every finding becomes a ticket.
- **W8-n — Fixes** from the soak, the alpha and W8-R. Each is a normal ticket.
- **W8-D — Docs** · `docs/site/` · Light. Quick start, the zero-downtime shapes (S§5.3 word for word), the threat model and its limits, the recovery key, the comparison page.
- **W8-I — Invite gating** · commercial module · Standard.
- **W8-B — Benchmark repository** · separate repository · Light · background.
- **W8-S — Landing and pricing pages** · site repository · Light · background.
- **W8-M — Marketplace images** · `scripts/marketplace/` · Light · background.
- → **G8**, launch

Once the GitHub repository exists, a Light ticket turns this list into issues. Until then, this file is the backlog.

---

## 7. Prompts

`docs/prompts/01-bootstrap-repo.md` and `02-contracts.md` are written out in full. Every later ticket uses these templates. Fill the `<>` placeholders; paste the ticket text in full rather than summarising it.

**T1 — Spec** (planning session, plan mode)
```
You are helping me write the spec for <area> in Moonglass. Read AGENTS.md, docs/threat-model.md, ADRs <list>, and docs/plan/stack.md §<n>.
Interview me one question at a time until behaviour, errors, limits and security properties are unambiguous. Ask the questions whose answers change the design first.
Then write docs/specs/<area>.md: purpose, behaviour, interfaces (types, messages, operations), error cases, acceptance cases (each one testable), non-goals, security notes, open questions.
Do not write code. Do not decide anything the docs leave open; list it under open questions.
```

**T2 — Plan** (fresh session, plan mode, no edits)
```
Ticket <ID>:
<paste the ticket>
Read AGENTS.md and the spec and ADRs the ticket links. Do not edit any file.
Propose: the files to create or change; types and function signatures; each acceptance test by name and what it asserts; risks; anything ambiguous in the ticket.
Only what this ticket needs. No abstractions, options or hooks for later tickets. Stop and wait for my approval.
```

**T3 — Implement** (fresh session)
```
Implement ticket <ID> only, following the approved plan below, on branch t/<id>-<slug>.
Red-green: write the acceptance tests first, run them, and paste the failing output. Then implement until they pass.
Touch only the packages the ticket lists. Stop and ask before changing a contract (proto, registry operations, agent.toml schema, storage schema), an ADR, or adding a dependency.
No placeholders, no TODOs, no fallback paths, no code for later tickets. Errors are wrapped with context and say what to do.
Finish with `make ci`. Paste the exact commands and the tail of their output, run `make test-list`, and commit as "<ID>: <title>". Then stop.

Ticket:
<paste>
Approved plan:
<paste>
```

**T4 — Review** (Opus, fresh session, read-only)
```
You are reviewing a pull request for Moonglass, a deploy platform whose server agent has root-equivalent access to users' machines. You did not write this code and have no access to the conversation that did.
Read docs/threat-model.md, ticket <ID>, its spec, and the diff: git diff main...t/<branch>.
Report only:
- correctness bugs
- threat-model violations (use the checklist in docs/threat-model.md)
- acceptance cases with no test
- tests that were deleted, weakened, skipped or hard-coded
- fallback paths, dead code, or code for later tickets
- dependencies added without an ADR
For each finding give file:line, what breaks, and a concrete failing scenario. No style comments. Do not propose new abstractions or features. If you find nothing, say so plainly.
```

**T5 — Fix** (the implementer's session if its context is still small; otherwise fresh)
```
Address these review findings on branch t/<branch>. For each one, either fix it with a test that fails first, or explain precisely why it is not a defect. Change nothing else.
`make ci`, paste the evidence, commit as "<ID>: address review". Stop.
<findings>
```

**T6 — Anti-slop** (only after the behaviour is correct)
```
The behaviour on branch t/<branch> is correct and tested. Without changing behaviour, public contracts or the test list, simplify:
- remove dead code, unused parameters, needless interfaces and duplicated helpers
- remove comments that restate the code
- use plain names and keep functions small and obvious
`make ci` must stay green with an identical testdata/tests.txt. Summarise the diff. Stop.
```

**T7 — Weekly drift check** (Friday, Opus, read-only)
```
Compare main against docs/plan/*, docs/adr/* and docs/specs/*. List every place where the code and the documents disagree, any code with no ticket behind it, any fallback or legacy path, and any dependency not named in an ADR. Facts only, with file:line. Do not fix anything.
```

---

## 8. Change control

- **Contracts:** the `.proto` files, registry operations, the `agent.toml` schema, the storage schema and the ADRs. Changing one needs an ADR amendment you approve first. The change then lands as one ticket that updates every user of the contract and **deletes the old form**.
- **No compatibility code before launch.** There are no users, so there is nothing to stay compatible with.
- **After launch, one narrow window.** Because agents update in stages (S§3.14), the control plane speaks the current and the previous agent protocol version, and nothing older. ADR-0007 records this. It is the only compatibility code the product ever carries, and it is deleted one release later.
- **Spikes are never merged.** Their output is a document and a decision.
- **A mistake repeated twice becomes one line in `AGENTS.md`.** If `AGENTS.md` passes about 200 lines, it is too long; move the rule into a lint check or a hook.

---

## 9. A normal week

| When | What |
|---|---|
| Monday | Approve the week's plans (T2) for primary-lane tickets; start the first. Start one secondary-lane ticket |
| Each morning | Review and merge yesterday's PRs, Core first. Start the next primary ticket |
| Each afternoon | File and plan tomorrow's tickets. Answer reviewer findings |
| Friday | Wipe the fleet, run the demo, mark the gate. Run T7. Add `AGENTS.md` lines for repeated mistakes. Talk to alpha users from week 5 |

---

## 10. Week 0: your checklist

Things only you can do. The agents' tickets are W0-1 to W0-5 above.

- [ ] Create the GitHub org and the `moonglass` repository; turn on branch protection for `main` (required CI checks, no direct pushes, squash merge only).
- [ ] Serve the Go vanity import for `moonglass.dev/moonglass` from moonglass.dev (one `<meta name="go-import">` tag).
- [ ] Two release signing keys (`ed25519-sk`, `-O verify-required`) on two FIDO2 tokens, primary and backup, kept apart; both public keys committed to the repository before W1-6 (ADR-0007).
- [ ] Accounts and an API token for the three test-fleet VPSs.
- [ ] Buy the user-content domain; apply to the Public Suffix List; send Let's Encrypt the rate-limit request.
- [ ] Waitlist page live (W0-5); book ten migration interviews.
