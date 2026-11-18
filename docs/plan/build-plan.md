# Moonglass — Build plan

_How Moonglass gets built, one ticket at a time. It builds on `stack.md` (S§n) and `milestones.md` (M§n). The ticket pipeline that runs this plan is `docs/pipeline/README.md`; the ticket list below is what it reads._

**Short version**

- Every change is one ticket, one branch `p/<ID>`, one PR, squash-merged when CI is green and review passes.
- The pipeline drives each ticket through ticket → plan → tests → approve → build → review → merge. You are involved only for contract or ADR changes, security-sensitive decisions, and tickets marked blocked or needs-human.
- A gate at the end of every week, with a demo you run yourself and a rule for when to stop.
- Clean from the first commit. No placeholders, no fallbacks, no code for later tickets. When a decision changes, the old code is deleted in the same change.

---

## 1. Risk labels

Each ticket carries one risk label. It decides how much testing and review the pipeline demands (`docs/pipeline/README.md`, "Review rule").

| Risk | Applies to | Pipeline | You |
|---|---|---|---|
| **high** | The trusted core in `AGENTS.md`: protocol, policy, keys, auth, store (RLS and migrations), registry authorisation, install script and systemd units, release signing and self-update | Named acceptance tests, approved and locked before code; Opus review always, against the threat-model checklist | Read every line of the merged diff in the weekly review. Only one high ticket is in flight at a time |
| **standard** | Deploy, agent runtime, proxy, server, API handlers, CLI, backups, import, UI with CSP | Named acceptance tests, approved and locked; Opus review always | The test diff first, then error paths and anything touching Docker, the filesystem or the network |
| **low** | Docs, UI screens, scripts, fixtures, benchmark, site | Tests optional; Opus review unless the skip rule applies | Skim |

Tests and `make dev` must be safe to run in two worktrees at once: ports from `:0`, temp data directories, and Docker resources labelled per test run and cleaned up afterwards. Up to three tickets run at once, each in its own worktree.

---

## 2. Gates

Each gate is a demo at the end of the week that you run on the freshly reinstalled fleet (M§1), plus criteria. The week's `Wn-E` ticket automates as much of the demo as can run in CI. **Stop rule:** a failed gate means you file fix tickets for the failure, and the next week's tickets depend on them.

| Gate | Passes when | If it fails |
|---|---|---|
| **G0** end of week 0 | Repo and CI live on GitHub with branch protection. Threat model, ADRs and UI foundations approved. Protocol v0 merged. SPIKE-1 decided and ADR-0001 updated. Waitlist live; at least five interviews booked | Do not start week 1 |
| **G1** skeleton | M§3 week 1 demo. Agent and proxy together ≤ 100 MB. Escape suite green | Fix the design before any deploy code |
| **G2** deploys | Zero failed requests across ten deploys. Rollback < 5 s. A failing pre-deploy step leaves the old release serving. Deploy suite required in CI | No week 3 work on the deploy path until explained and fixed |
| **G3** git and secrets | `git push` goes live on the 2 GB box, through the build-in-CI template. Database holds only ciphertext. The disk-fill demo passes | Secrets and disk tickets carry over; git can proceed |
| **G4** rollback and UI | Crash-loop rollback within 2 min. Shadow-mode report, then a real rollback after opt-in. Core screens usable from the keyboard alone. A second server receives the wrapped key (moved from G3) | |
| **G5** agent safety | The M§3 week 5 demo. Every operation has authz-matrix rows. Alpha opens | Alpha waits |
| **G6** data | Restore onto a different server with only the recovery key. Compose redeploy leaves the database untouched | |
| **G7** self-hosted and releases | A real Coolify project imported and deployed. Agent upgrade under load with zero failures. A bad canary build reverts itself. Seven-day soak started | Soak restarts after the fix |
| **G8** launch | Soak clean for 7 days. Adversarial review findings fixed or documented as limits. Docs, benchmark and comparison page done | Launch moves (M§8 open decision 1) |

---

## 3. Tickets

One line per ticket, read by `scripts/ticket-status`:

`- **ID** Title — intent · depends_on: IDs or — · risk: low|standard|high [· kind: build|review|human] [· after: N] [· paths: seed paths]`

- IDs are stable; renumbering is not allowed. New tickets get new IDs.
- `kind` defaults to `build`. `human` tickets are yours: the pipeline never starts them, and an agent ticket may not depend on an unfinished one. `review` tickets are drift reviews.
- `after: N` holds a review ticket until N tickets are merged.
- `paths` seeds the ticket's `allowed_paths`; the ticket step settles the final list.
- Status lives in `docs/tickets/<ID>.md`, never here.

### Week 0 — contracts and plumbing

- **W0-1** Bootstrap the repository — `make ci` green on a clean clone, static amd64 and arm64 builds, `AGENTS.md`, `CLAUDE.md`, test-list snapshot · depends_on: — · risk: low
- **W0-2** Threat model, ADRs, UI foundations — every statement traces to the plan docs or is marked Proposed · depends_on: W0-1 · risk: low
- **W0-3** Protocol v0 — envelope, Enroll, Heartbeat and error codes; codegen per ADR-0008, deterministic `make proto`, CI fails on stale code, a fuzz target for envelope decoding · depends_on: W0-2 · risk: high
- **SPIKE-1** Memory and proxy upgrade spike — the 48-hour test from M§2.2 on branch `spike/1-proxy`, never merged; output `docs/spikes/0001-proxy-memory.md` and ADR-0001 naming exactly one upgrade mechanism · depends_on: W0-1 · risk: high · kind: human
- **W0-4** Fleet scripts — wipe and rebuild the three VPSs from a script; the provider token comes from your environment at run time and agents never see it · depends_on: W0-1 · risk: low
- **W0-5** Waitlist page — per M§2.2 experiment 1, in the separate site repository · depends_on: — · risk: low · kind: human

### Week 1 — walking skeleton

- **W1-1** Container-spec sanitizer and `agent.toml` schema — table-driven tests for every deny rule and scoped allow; malicious Compose fixtures are rejected · depends_on: W0-2 · risk: high
- **W1-2** Storage foundation — orgs, users, memberships, roles, servers, enrolment tokens; RLS on `org_id`; conformance suite on both backends · depends_on: W0-2 · risk: high
- **W1-3** Enrolment — one-time token, fingerprint printed, control-plane key pinned; `moonglass server` and `moonglass agent enroll` · depends_on: W0-3, W1-2 · risk: high
- **W1-4** Connection and heartbeat — outbound WSS, reconnect with backoff and jitter, offline and recovery in an integration test · depends_on: W1-3 · risk: high
- **W1-5** Proxy process — Caddy 2.11.9 embedded, admin API on a Unix socket, counter module, one route over HTTPS · depends_on: SPIKE-1 · risk: standard
- **W1-6** Install script and systemd units — service user, hardened units, Docker group for the agent only, signature checks against the two pinned release keys (ADR-0007) · depends_on: W1-4, W1-5 · risk: high
- **W1-7** Memory budget check — CI fails when idle agent plus proxy is above budget · depends_on: W1-5, W0-4 · risk: low

### Week 2 — deploy, switch, roll back

The weeks 2–3 preflight (2026-11-17) added W2-8 to W2-12 and moved W2-S to week 4. W2-13 was split out of W2-3 (2026-11-18) to keep W2-3 under the size limit; W2-3 keeps its ID, so its dependents are unchanged. The high-risk tickets of week 2 are W2-8, W2-9, W2-11a, W2-11b, W2-11c, W2-11d, W2-12 and W2-5 (raised: it reaches `internal/registry`); they run one at a time.

- **W2-1** Operation registry — Huma REST and the CLI generated from one registry; scope and `destructive` per operation; hashed owner tokens; hash-chained audit written by the registry · depends_on: W1-2 · risk: high
- **W2-2** Protocol: deploy messages and release state machine — Deploy, SwitchRelease, Rollback, RunPreDeploy; a pure, fully unit-tested state machine · depends_on: W0-3 · risk: high
- **W2-8** Protocol: deploy identity, service settings and command results — Deploy gains project, environment, service, port, health_path, domains and has_pre_deploy; the CommandResult message; validation, protocol-abuse tests and a fuzz target (`docs/specs/protocol.md`) · depends_on: W2-2 · risk: high · paths: proto/, internal/protocol/, docs/specs/protocol.md
- **W2-9** Container spec pass-through fields — Cmd, Env, Labels, AgentLabels, Networks, RestartPolicy and LogConfig carried in Checked; user labels in the dev.moonglass. namespace rejected; environment values kept out of every error (`docs/specs/container-policy.md`) · depends_on: W1-1 · risk: high · paths: internal/policy/, testdata/escape/, docs/specs/container-policy.md
- **W2-13** Agent state database — internal/agent/state opens <data-dir>/agent.db (mode 0600, the pragmas of `docs/specs/agent-state.md`) and migrates it with store.Migrate; the apps and releases tables; the binding, candidate and failed-release records, each written in one transaction (`docs/specs/agent-state.md`) · depends_on: W2-2 · risk: standard · paths: internal/agent/state/, docs/specs/agent-state.md, go.mod, .golangci.yml
- **W2-3** Deploy executor — pull an image or use one already in the local store, create the project network, sanitize, create and start the candidate container, record the release through internal/agent/state (`docs/specs/release.md`, `docs/specs/agent-state.md`); its start-up reconcile removes only the managed containers that are not the current, previous or candidate release of their app; real Docker in tests · depends_on: W1-1, W2-2, W2-8, W2-9, W2-13 · risk: standard · paths: internal/deploy/, docs/specs/release.md, go.mod, go.sum, .golangci.yml
- **W2-12** Proxy admin socket group — the agent-facing admin socket moves to /run/moonglass-proxy/admin.sock, mode 0660, group moonglass-proxy-admin; the install script creates the group and the agent unit joins it (ADR-0001 decision 3) · depends_on: W1-6 · risk: high · paths: internal/proxy/, deploy/systemd/, scripts/install, internal/installtest/, docs/specs/install.md, docs/specs/proxy.md
- **W2-4** Switch and drain — health-gated switch, connection draining, the previous release kept running unrouted for 10 minutes, keep five releases, the Abort transition; the stops due by `stop_after` in the start-up reconcile and the hourly retention rule (`docs/specs/release.md`, `docs/specs/agent-state.md`) · depends_on: W2-3, W1-5, W2-12 · risk: standard · paths: internal/deploy/, internal/release/, internal/agent/, cmd/moonglass/, docs/specs/release.md, docs/specs/agent-state.md
- **W2-6** Pre-deploy steps — a sanitized one-off container from the new image before the candidate starts; failure aborts the deploy; steps are irreversible by default (`docs/specs/release.md`) · depends_on: W2-3 · risk: standard · paths: internal/deploy/
- **W2-10** Command channel — the server sends typed commands to the agent of a service and awaits its CommandResult; each request id runs once, re-sent commands return the stored result; the agent loads agent.toml and its state at start and dispatches to internal/deploy (`docs/specs/connection.md`) · depends_on: W2-4, W2-6 · risk: standard · paths: internal/server/, internal/agent/, cmd/moonglass/, docs/specs/connection.md
- **W2-11a** Projects, environments and services on the control plane — tables and store methods, conformance on both backends (`docs/specs/storage.md`) · depends_on: W2-1 · risk: high · paths: internal/store/, docs/specs/storage.md
- **W2-11b** Releases and rollbacks on the control plane — tables and store methods, conformance on both backends (`docs/specs/storage.md`) · depends_on: W2-11a · risk: high · paths: internal/store/, docs/specs/storage.md
- **W2-11c** Operation inputs and errors — generated CLI and REST inputs, and the 400, 404 and 409 error mapping (`docs/specs/registry.md`) · depends_on: W2-11a · risk: high · paths: internal/registry/, internal/api/, internal/cli/, docs/specs/registry.md
- **W2-11d** Project, environment and service operations — the six create and list operations (`docs/specs/registry.md`) · depends_on: W2-11a, W2-11b, W2-11c · risk: high · paths: internal/registry/, internal/api/, internal/cli/, docs/specs/registry.md
- **W2-5** Rollback and CLI operations — `moonglass deploy`, `rollback`, `releases list`; one deploy per service with newest winning; steps re-sent after a server restart; rollback under 5 s and audited (`docs/specs/registry.md`) · depends_on: W2-1, W2-4, W2-10, W2-11d · risk: high · paths: internal/server/, internal/registry/, internal/api/, internal/cli/
- **W2-7** Deploy-correctness suite and Docker matrix — load generator, Engine 25–29 on ubuntu-22.04, `make test-deploy` (`docs/specs/deploy-suite.md`); after it merges the owner makes the Engine 29 job a required check · depends_on: W2-4 · risk: standard · paths: test/deploy/, .github/workflows/ci.yml, Makefile, docs/specs/deploy-suite.md
- **W2-E** Week 2 end-to-end — the G2 demo path as one CI test: ten deploys with zero failed requests, rollback under 5 s, a failing pre-deploy step leaves the old release serving · depends_on: W2-5, W2-6, W2-7 · risk: standard · paths: test/e2e/

### Week 3 — git push to live, secrets, disk

The preflight split secrets by step-up (ADR-0003 decision 11): week 3 sets secrets; granting, delivering, revealing and rotating wait for W4-1 and are listed in week 4. Git builds use the server's deploy key; the GitHub App is W4-10. W3-4 is raised to high because it reaches `internal/registry`. The week 3 planning (2026-11-17, owner decisions D19 to D27) added W3-9 and W3-10 and settled the recovery-recipient pins, the `keys init` order, the deploy-key dependency, the optional disk report and the per-server build queue.

- **W3-1** Keys — environment keys, the agent X25519 key as an age recipient, the key record, `moonglass keys init` writing the recovery key file, wrap and unwrap; test vectors readable by stock `age` (ADR-0015, `docs/specs/secrets.md`) · depends_on: W1-3 · risk: high · paths: internal/keys/, internal/cli/, internal/registry/, cmd/moonglass/, go.mod, go.sum, .golangci.yml, .github/workflows/ci.yml, docs/specs/secrets.md
- **W3-7** Protocol: builds and server reports — the Build message, and the optional disk report and the deploy key in Heartbeat; a present disk report is fully validated (`docs/specs/protocol.md`) · depends_on: W2-8 · risk: high · paths: proto/, internal/protocol/, docs/specs/protocol.md
- **W3-10** Owner token hook — `moonglass server --print-owner-token NAME` prints one owner token with a 90-day expiry, once, and never logs it; a walking-skeleton hook like `--print-enroll-token` (`docs/specs/enrolment.md`, `docs/specs/registry.md`) · depends_on: W2-1 · risk: high · paths: cmd/moonglass/, internal/server/, internal/registry/, docs/specs/enrolment.md, docs/specs/registry.md
- **W3-2** Secrets: set — `keys init` writes the key file, then registers the first recovery recipient and pins it, keeping the file and naming the retry when registration fails; `keys.recovery.list`; the CLI checks the recovery recipients against its pins, creates the environment key wrapped to them only, pins it, and encrypts each value; the control plane stores ciphertext only; env and registry kinds (`docs/specs/secrets.md`, `docs/specs/registry.md`) · depends_on: W3-1, W2-11d · risk: high · paths: internal/keys/, internal/store/, internal/registry/, internal/api/, internal/cli/, cmd/moonglass/, docs/specs/secrets.md, docs/specs/registry.md
- **W3-8** Server deploy key — the agent generates an SSH Ed25519 deploy key in internal/keys with golang.org/x/crypto/ssh (ADR-0015) and reports it in Heartbeat; the control plane stores it; `moonglass servers deploy-key` prints it (`docs/specs/build.md`) · depends_on: W3-7, W2-10, W2-11c · risk: high · paths: internal/keys/, internal/agent/, internal/server/, internal/store/, internal/registry/, internal/api/, internal/cli/, go.mod, go.sum, .golangci.yml
- **W3-9** Build host prerequisites — the agent unit's MemoryMax rises to 512M so git and buildx children fit; `scripts/install` checks git 2.30+ and the buildx plugin and prints one clear line for each that is missing (`docs/specs/install.md`, `docs/specs/build.md`) · depends_on: W2-12 · risk: high · paths: deploy/systemd/, scripts/install, internal/installtest/, docs/specs/install.md
- **W3-4** Git builds — `moonglass deploy --git <owner>/<repo>@<commit>` builds that commit on the server with its deploy key through docker buildx with insecure entitlements denied; the control plane sends one BUILD per server at a time and queues the rest; the agent rejects a concurrent BUILD; `moonglass.toml`; the build-in-CI template with an owner token from `--print-owner-token` (`docs/specs/build.md`, `docs/specs/connection.md`) · depends_on: W2-5, W3-7, W3-8, W3-9, W3-10 · risk: high · paths: internal/build/, internal/agent/, internal/server/, internal/registry/, internal/api/, internal/cli/, examples/ci/, docs/specs/build.md, docs/specs/connection.md
- **W3-6** Disk guard — thresholds, prune order, reserve file, the disk report in every Heartbeat; builds and then deploys refused as the disk fills (`docs/specs/disk.md`) · depends_on: W2-3, W3-4, W3-7 · risk: standard · paths: internal/agent/disk/, internal/agent/, internal/server/, internal/deploy/, internal/build/, docs/specs/disk.md
- **W3-E** Week 3 end-to-end — the G3 path in CI: a deploy by digest, as the build-in-CI template makes it, goes live; the database holds only ciphertext; the disk guard prunes in order · depends_on: W3-2, W3-4, W3-6 · risk: standard · paths: test/e2e/

### Week 4 — sign-in, auto-rollback, the real UI

The preflight moved W2-S, W3-3 and W3-5 here and added W4-8 to W4-10. Their IDs do not change. On 2026-11-18 the owner replaced email links with Google sign-in (ADR-0005 amended, `docs/specs/auth.md`) and split W4-1 into W4-1a, W4-1b, W4-1c and W4-1; W4-1 keeps its ID, so its dependents are unchanged.

- **W4-1a** Sign-in tables — `session_hashes` and `link_token_hashes` lookup tables, the `step_ups` table, and `session_id` and `step_up_id` on `audit_events`, with their store methods, on both backends with conformance (`docs/specs/auth.md`) · depends_on: W2-1 · risk: high · paths: internal/store/, docs/specs/auth.md, docs/specs/storage.md
- **W4-1b** GitHub and Google sign-in — the provider round trip with PKCE on the standard library, `__Host-` sessions, sign-out, session authentication with `Moonglass-Org`, cross-origin protection, the operator variables, the audit hash with the new columns (`docs/specs/auth.md`) · depends_on: W4-1a · risk: high · paths: internal/auth/, internal/registry/, internal/api/, internal/server/, cmd/moonglass/, docs/specs/auth.md
- **W4-1c** Step-up — the `StepUp` flag, the `step_up_required` answer, the step-up page and its approval, `Moonglass-Step-Up` checked in Invoke, the CLI's `--step-up` (`docs/specs/auth.md`) · depends_on: W4-1b · risk: high · paths: internal/auth/, internal/registry/, internal/api/, internal/cli/, internal/server/, docs/specs/auth.md
- **W4-1** Sign-in operations and linking — `sessions.list`, `sessions.revoke`, `identities.list`, `identities.link` with step-up, the link landing, `moonglass server --print-owner-link` (`docs/specs/auth.md`) · depends_on: W4-1c, W3-10 · risk: high · paths: internal/auth/, internal/registry/, internal/api/, internal/cli/, internal/server/, cmd/moonglass/, docs/specs/auth.md, docs/specs/registry.md
- **W4-8** Secrets: first grant and delivery — `secrets grant` for an environment's first server, with step-up; Deploy carries that agent's wrapped key record and the ciphertexts; the agent unwraps in memory and never writes plaintext (`docs/specs/secrets.md`) · depends_on: W3-2, W4-1, W4-2, W2-10 · risk: high · paths: proto/, internal/protocol/, internal/keys/, internal/agent/, internal/server/, internal/store/, internal/registry/, internal/api/, internal/cli/
- **W3-3** Grant and rotate — `RewrapEnvKey` refused when the agent's own key record says protected; `secrets grant` checks the fingerprint; `secrets rotate` re-encrypts and deletes old wrapped copies; every one with step-up · depends_on: W4-8 · risk: high · paths: internal/keys/, internal/registry/, internal/agent/, internal/store/, proto/, internal/protocol/
- **W4-9** Secrets reveal — the agent re-encrypts one value to a one-time CLI key; owner-only, step-up, refused to agent identities, audited (ADR-0003 decision 8) · depends_on: W4-8 · risk: high · paths: proto/, internal/protocol/, internal/keys/, internal/agent/, internal/server/, internal/registry/, internal/api/, internal/cli/
- **W3-5** Private registries and environment variables — pull with the environment's registry credentials, which never enter a container; pass env secrets to services and pre-deploy steps at deploy · depends_on: W3-4, W4-8 · risk: standard · paths: internal/deploy/, internal/build/
- **W4-10** GitHub App — created from a manifest in a signed-in session; a push webhook deploys the pushed commit; the server's deploy key is added to the repository (`docs/specs/build.md`) · depends_on: W4-1, W3-4 · risk: high · paths: internal/github/, internal/registry/, internal/server/, internal/store/
- **W2-S** UI shell — SvelteKit static build embedded in the binary (ADR-0016), design tokens, layout, a ⌘K palette of registry operations, a "servers online" page, no other business logic · depends_on: W0-2, W2-1, W4-1 · risk: high · paths: ui/, internal/ui/, cmd/moonglass/, internal/server/, internal/registry/, Makefile, .github/workflows/ci.yml, .gitignore
- **W4-2** Environments — the protected flag, environments.set-protected, the flag in environments.list; until W5-2 a deploy to a protected environment runs and is audited; resource limits deferred to v1 · depends_on: W2-11d · risk: high · paths: internal/registry/, internal/store/, internal/api/, internal/cli/, docs/specs/storage.md, docs/specs/registry.md, docs/specs/secrets.md
- **W4-3** Auto-rollback — crash and health triggers on by default; the error-rate trigger in shadow mode with "mark right or wrong" (S§5.4) · depends_on: W2-4 · risk: standard · paths: internal/rollback/
- **W4-4** Live logs and current usage — LOG_REQUEST, LOG_CHUNK, USAGE, and two SSE routes on the non-operation list · depends_on: W2-10, W2-11a · risk: high · paths: proto/, internal/protocol/, internal/agent/, internal/server/, internal/registry/, docs/specs/protocol.md, docs/specs/connection.md, docs/specs/live.md, docs/threat-model.md
- **W4-5** Browser secret encryption — values are write-only; CSP is enforced; age in TypeScript · depends_on: W3-2, W2-S · risk: high · paths: ui/
- **W4-6** Core UI screens — projects, services, deploys, logs, servers, against `docs/specs/ui.md` · depends_on: W2-S, W4-4 · risk: standard · paths: ui/
- **W4-7** Notifications — signed webhooks; email waits for its own ADR (ADR-0005 decision 5) · depends_on: W4-3 · risk: standard · paths: internal/notify/
- **W4-E** Week 4 end-to-end — the G4 path in CI: crash-loop rollback within 2 min, the shadow-mode report, sign-in and session revoke, core screens driven from the keyboard, and a second server receives the wrapped key · depends_on: W4-1, W4-2, W4-5, W4-6, W4-7, W3-3 · risk: standard · paths: test/e2e/

**For the week 4 preflight** (the protected-flag item was decided by the owner on 2026-11-17; the other two stay open):
- W4-2 stores the protected flag and does not gate deploys. W5-2 adds that gate. Until then a deploy to a protected environment runs and is audited, so W4-E is not stalled. The org's first environment is created protected and later ones are not. There is no plan-cap check in the open build. Resource limits are deferred to v1, and there is no W4-11 ticket.
- W4-10's GitHub App private key and webhook secret are credentials the control plane would hold; ADR-0003 decision 10 says it holds none after setup.
- The servers screen (W2-S, W4-6) needs online state and the disk report, which `servers.list` does not return yet.

### Week 5 — the agent-safety story

- **W5-1** Agent identities and scoped tokens — the authz matrix covers every operation × scope × role · depends_on: W2-1 · risk: high · paths: internal/registry/, internal/auth/
- **W5-2** Approvals — pending state, a diff of what will change, step-up, `moonglass approve` · depends_on: W5-1, W4-1 · risk: high · paths: internal/registry/, internal/cli/, ui/
- **W5-3** Local policy enforcement — forbid exec, deletes or repositories end to end · depends_on: W1-1 · risk: high · paths: internal/policy/, internal/agent/
- **W5-4** MCP from the registry — official Go SDK v1.7+, stateless Streamable HTTP, OAuth 2.1 · depends_on: W5-1 · risk: high · paths: internal/mcp/
- **W5-5** Soft delete and audit view — includes `moonglass audit verify` for the hash chain · depends_on: W2-1 · risk: standard · paths: internal/registry/, ui/
- **W5-6** Agent-safety demo script — a script that walks the G5 demo: scoped token, refused operation, approval, audit · depends_on: W5-2 · risk: low · paths: examples/agent-demo/
- **W5-E** Week 5 end-to-end — the G5 path in CI: a scoped agent token is refused a destructive operation, an approval unblocks it after step-up, local policy refuses exec, the audit chain verifies · depends_on: W5-2, W5-3, W5-4, W5-5, W5-6 · risk: standard · paths: test/e2e/

### Week 6 — databases, backups, restore, Compose

- **W6-1** One-click Postgres and Redis — managed database services deployed through the normal deploy path · depends_on: W2-3 · risk: standard · paths: internal/services/
- **W6-2** Backups — dump, zstd, age to the environment key, S3, schedules; no temp files · depends_on: W3-1, W6-1 · risk: high · paths: internal/backup/
- **W6-3** Restore — restore in CI on every change, including onto a fresh server with only the recovery key · depends_on: W6-2 · risk: standard · paths: internal/backup/
- **W6-4** Compose import and reconcile — unchanged services are never touched; changed ones restart in place, listed before you confirm · depends_on: W1-1, W2-3 · risk: standard · paths: internal/compose/
- **W6-5** Compose classifier survey — thirty files, counts per class; sizes the v1 work, not product code · depends_on: W6-4 · risk: low · paths: docs/spikes/0002-compose.md
- **W6-E** Week 6 end-to-end — the G6 path in CI: restore onto a different server with only the recovery key; a Compose redeploy leaves the database untouched · depends_on: W6-3, W6-4 · risk: standard · paths: test/e2e/

### Week 7 — self-hosted, portability, releases

W7-6 was split into W7-6a to W7-6f (2026-11-18): it was over the size limit for a high ticket, and no spec described self-update. W7-6 is no longer a ticket; W7-E and W8-M depend on the pieces they need. W7-6g was split out of W7-6c (2026-11-18): the publish job runs `scripts/install --state-dir`, which arrives with W7-6e, and uploads to a release origin that no decision names yet (`docs/specs/update.md` §10 item 24). Until the owner names that origin, how the workflow uploads to it and the upload secret's name in `docs/specs/update.md` §9, W7-6g's plan step stops it as needs-human. Attestations on the private repository need GitHub Enterprise Cloud or a public repository (§10 item 25); the owner settles that before a release tag is pushed.

- **W7-1** Self-hosted mode — one box, bootstrap token, `server login-link`, SQLite in the real path · depends_on: W1-2, W4-1 · risk: high · paths: internal/server/, internal/auth/
- **W7-2** Export, import, re-enrol — move a self-hosted control plane's state to another box and re-enrol its agents · depends_on: W7-1 · risk: standard · paths: internal/export/
- **W7-3** `moonglass import` — Compose and Coolify; values encrypted locally before upload · depends_on: W6-4, W3-2 · risk: standard · paths: internal/importer/
- **W7-4** Commercial module and hosted entitlements — separate repository; free tier of one person, two servers, no managed subdomains; the open build contains no plan checks · depends_on: W2-1 · risk: standard · kind: human
- **W7-5** Control-plane backup — scheduled, encrypted backups of the self-hosted control plane · depends_on: W7-1 · risk: standard · paths: internal/server/
- **W7-6a** Release signature verifier — an internal/update SSHSIG verifier on the standard library only: preamble, version 1, either of the two pinned keys, namespace moonglass-release, hash sha512, the Ed25519 signature over the application hash, flags, counter and message hash; flags 0x01 and 0x04 required and everything else rejected; tests sign sk-ssh-ed25519 with throwaway keys in Go; a fuzz target for the parser (ADR-0007 decision 1, threat-model checklist 6) · depends_on: W1-6 · risk: high · paths: internal/update/
- **W7-6b** Self-update spec — `docs/specs/update.md`, approved by the owner before it merges: how the agent discovers a release, and the self-hosted path (ADR-0007 decision 5); the artifact URL layout per channel; where the current and previous binaries live and who can write them under ProtectSystem=strict; the systemd revert, its unit or timer names, the five-minute window and what counts as not restarted; how the self-check result reaches the control plane; the rollout state, halt criteria as stored data, and the resume and withdraw operations; the drained proxy upgrade for a release that changes the proxy (ADR-0007 decision 6); the attestation action and how unsigned artifacts reach the owner for signing · depends_on: W1-6 · risk: low · paths: docs/specs/update.md
- **W7-6c** Release workflow with provenance — .github/workflows/release.yml, on a release tag, builds the static amd64 and arm64 artifacts, writes the SBOM and SHA256SUMS, copies the units with the glob deploy/systemd/moonglass-* so the revert units of W7-6e join each tar without an edit here, attests each binary and the SBOM, and uploads the unsigned tars to a draft release (steps 1 to 5 of "The workflow" in `docs/specs/update.md` §9); internal/releasetest checks the workflow file statically; signing stays offline on the owner's token, and the publish job is W7-6g (ADR-0007) · depends_on: W1-6, W7-6b · risk: high · paths: .github/workflows/release.yml, internal/releasetest/, docs/specs/update.md
- **W7-6d** Protocol: update messages — the messages `docs/specs/update.md` says the control plane sends and the agent reports, such as an update offer and a self-check result; validation, protocol-abuse tests and a fuzz target; no field carries a host path or a shell string (`docs/specs/protocol.md`) · depends_on: W7-6b, W3-7 · risk: high · paths: proto/, internal/protocol/, docs/specs/protocol.md
- **W7-6e** Agent self-update and revert — download into a private staging directory, verify with the W7-6a verifier and the signed SHA256SUMS, keep the previous binary and switch; the systemd revert after five minutes without a passing self-check; the self-check of ADR-0007 decision 4: running and not restarted, the pinned connection back, the running executable's SHA-256 equal to the verified file, the proxy check passing (`docs/specs/update.md`, `docs/specs/install.md`) · depends_on: W7-6a, W7-6b, W7-6d · risk: high · paths: internal/update/, internal/agent/, deploy/systemd/, scripts/install, internal/installtest/, docs/specs/install.md
- **W7-6g** Release publish job — the workflow_dispatch job of "Publish" in `docs/specs/update.md` §9, in .github/workflows/release.yml, with the inputs version and channel: download the signed set from the draft release, verify each arch with `scripts/install --from <dir> --prefix <tmp> --unit-dir <tmp> --state-dir <tmp>` and the channel pointer with `ssh-keygen -Y verify`, then upload to the release origin in the §9 order; it waits for the owner to name the origin, its upload step and its secret in `docs/specs/update.md` §9 (§10 item 24) · depends_on: W7-6c, W7-6e · risk: high · paths: .github/workflows/release.yml, internal/releasetest/, docs/specs/update.md
- **W7-6f** Channels and staged rollout — canary and stable channels on the control plane; stages of 10%, 50% and 100% with 24 hours between them; halt when at least 2 agents and at least 1% of those updated in the current stage have reverted, not reconnected within 10 minutes, or failed their self-check within their first hour; the halt holds until the owner resumes or withdraws the release; the same criteria block promotion from canary; 72 hours on canary before stable; no percentage rollout on self-hosted (`docs/specs/update.md`) · depends_on: W7-6d, W2-1 · risk: high · paths: internal/server/, internal/store/, internal/registry/, internal/api/, internal/cli/, docs/specs/update.md, docs/specs/storage.md, docs/specs/registry.md
- **W7-7** First-deploy wizard — the UI flow from a fresh install to a first live deploy · depends_on: W4-6 · risk: low · paths: ui/
- **W7-E** Week 7 end-to-end — the G7 path in CI: a Coolify fixture imported and deployed, an agent upgrade under load with zero failed requests, a bad canary reverts itself · depends_on: W7-2, W7-3, W7-5, W7-6e, W7-6f, W7-7 · risk: standard · paths: test/e2e/

### Week 8 — harden and launch

- **W8-R** Adversarial review — a read-only review of the whole trusted core, briefed with the threat model; every finding becomes a fix ticket · depends_on: W7-E · risk: low · kind: review
- **W8-D** Docs — quick start, the zero-downtime shapes (S§5.3 word for word), the threat model and its limits, the recovery key, the comparison page · depends_on: W7-E · risk: low · paths: docs/site/
- **W8-I** Invite gating — in the commercial module · depends_on: W7-4 · risk: standard · kind: human
- **W8-B** Benchmark repository — separate repository · depends_on: — · risk: low · kind: human
- **W8-S** Landing and pricing pages — site repository · depends_on: — · risk: low · kind: human
- **W8-M** Marketplace images — scripts that build provider marketplace images with Moonglass preinstalled · depends_on: W7-6g · risk: low · paths: scripts/marketplace/
- **W8-E** Launch end-to-end — fresh install on the reinstalled fleet, deploy, roll back, back up and restore, all from the quick start · depends_on: W8-R, W8-D, W8-M · risk: standard · paths: test/e2e/

### Drift reviews

A fresh Opus child reviews the whole repository for architecture drift and appends fix tickets to §6 (`docs/pipeline/children/arch-review.md`).

- **R-1** Drift review 1 — code against plan, ADRs, specs and threat model; one fix ticket per finding · depends_on: — · risk: low · kind: review · after: 22
- **R-2** Drift review 2 — code against plan, ADRs, specs and threat model; one fix ticket per finding · depends_on: R-1 · risk: low · kind: review · after: 31
- **R-3** Drift review 3 — code against plan, ADRs, specs and threat model; one fix ticket per finding · depends_on: R-2 · risk: low · kind: review · after: 40
- **R-4** Drift review 4 — code against plan, ADRs, specs and threat model; one fix ticket per finding · depends_on: R-3 · risk: low · kind: review · after: 49
- **R-5** Drift review 5 — code against plan, ADRs, specs and threat model; one fix ticket per finding · depends_on: R-4 · risk: low · kind: review · after: 58
- **R-6** Drift review 6 — code against plan, ADRs, specs and threat model; one fix ticket per finding · depends_on: R-5 · risk: low · kind: review · after: 67

---

## 4. Change control

- Contracts: the `.proto` files, registry operations, the `agent.toml` schema, the storage schema and the ADRs. Changing one needs an ADR amendment you approve first: the plan step stops the ticket as needs-human. The change then lands as one ticket that updates every user of the contract and **deletes the old form**.
- No compatibility code before launch. There are no users, so there is nothing to stay compatible with.
- After launch, one narrow window. Because agents update in stages (S§3.14), the control plane speaks the current and the previous agent protocol version, and nothing older. ADR-0007 records this. It is the only compatibility code the product ever carries, and it is deleted one release later.
- Spikes are never merged. Their output is a document and a decision.
- A mistake repeated twice becomes one line in `AGENTS.md`, or better a gate in `scripts/verify`. If `AGENTS.md` passes about 200 lines, it is too long; move the rule into a lint check.

---

## 5. Owner checklist

Things only you can do.

- [x] Repository for now: private `github.com/moonglass-dev/moonglass`. `main` requires a pull request and a passing `ci` check, and squash merge is the only merge. It moves to a GitHub organization later. `moonglass` is taken by a user account, so the organization name stays open (M§8).
- [ ] Serve the Go vanity import for `moonglass.dev/moonglass` from moonglass.dev (one `<meta name="go-import">` tag).
- [ ] Two release signing keys (`ed25519-sk`, `-O verify-required`) on two FIDO2 tokens, primary and backup, kept apart; both public keys committed to the repository before W1-6 (ADR-0007).
- [ ] Accounts and an API token for the three test-fleet VPSs.
- [ ] Buy the user-content domain; apply to the Public Suffix List; send Let's Encrypt the rate-limit request.
- [ ] Waitlist page live (W0-5); book ten migration interviews.
- [ ] The `kind: human` tickets above: SPIKE-1, W0-5, W7-4, W8-I, W8-B, W8-S.

---

## 6. Fix tickets

Drift reviews and W8-R append fix tickets at the end of this section. The ID is `F`, the review's ID without its dash, a dash and a number: `FR1-1`, `FR1-2`, `FW8R-1`.

- **FR1-1** Enrolment and REST over TLS — the owner decides and specs how `POST /v1/enroll` and the registry REST routes get TLS and how a new agent authenticates the control-plane key before pinning it; then the server serves them only over TLS and the agent and CLI refuse `http://` (R-1 finding 1) · depends_on: — · risk: high · paths: internal/server/, internal/agent/, internal/cli/, internal/keys/, cmd/moonglass/, docs/specs/enrolment.md, docs/specs/connection.md, docs/specs/registry.md
- **FR1-2** Store the agent age recipient — a storage-schema amendment, approved by the owner, that keeps each server's `agent_age_recipient` from enrolment, a migration on both backends, `AddServer` storing it and conformance tests (R-1 finding 2) · depends_on: — · risk: high · paths: internal/store/, internal/server/, docs/specs/storage.md, docs/specs/enrolment.md
- **FR1-3** Agent key files in internal/keys — move loading and atomic writing of `agent-signing.key`, `agent-agree.key`, `control-plane.pub` and `fingerprint` from internal/agent into internal/keys, with no change to file names or modes (R-1 finding 3) · depends_on: — · risk: high · paths: internal/keys/, internal/agent/, docs/specs/enrolment.md
- **FR1-4** agent.toml load fails closed — `policy.LoadFile` refuses a file whose owner uid cannot be read, checks mode and owner once each, drops the unused `Uid()` assertion and wraps every error with the path (R-1 finding 4) · depends_on: — · risk: high · paths: internal/policy/, testdata/escape/
- **FR1-5** Remove dead code in the data_dirs check — delete the unused `want` path and the always-false project comparison in `dataDirAllowed` (R-1 finding 5) · depends_on: FR1-4 · risk: high · paths: internal/policy/
- **FR1-6** One token generator — enrolment tokens and their format check use `registry.NewToken` and one shared format check; the copies in internal/server are deleted (R-1 finding 6) · depends_on: — · risk: high · paths: internal/server/, internal/registry/
- **FR1-7** Correct the /v1/agent authorisation text — `registry.NonOperationRoutes` and `docs/specs/registry.md` describe mutual TLS with an enrolled agent key, as `docs/specs/connection.md` does; delete the stale W2-1 comment in internal/server/agent.go (R-1 finding 7) · depends_on: — · risk: high · paths: internal/registry/, internal/server/, docs/specs/registry.md
- **FR1-8** Writable data directory for the agent unit — the agent unit gets `/var/lib/moonglass/mounts` as a state directory, `docs/specs/install.md` names it, and an install test checks the unit (R-1 finding 8) · depends_on: — · risk: high · paths: deploy/systemd/, internal/installtest/, docs/specs/install.md
- **FR1-9** Tidy go.mod and gate it — run `go mod tidy` so the direct modules are direct and unused ones are gone, and add a `go mod tidy -diff` check to `make ci` (R-1 finding 9) · depends_on: — · risk: low · paths: go.mod, go.sum, Makefile
- **FR1-10** depguard boundaries for ADR-0009 and ADR-0010 — rewrite `policy-deps` as `$all`-with-exclusion rules so compose-go and go-toml are denied outside internal/policy, pgx outside internal/store/pg and modernc outside internal/store/sqlite and internal/agent/state (R-1 finding 10) · depends_on: — · risk: low · paths: .golangci.yml
