# Moonglass — Phase 4: Milestones

_2026-11-15, revised 2026-11-16 after an outside review. Builds on `research.md` (R§n), `vision.md` (V§n) and `stack.md` revision 3 (S§n)._

**Short version**

- **Week 8 is a free public launch for reputation.** No billing yet. Pro arrives around week 10, open hosted signups around week 11, Team and the self-hosted Team licence around week 14.
- **The eight weeks were too full, so the scope moved.**
  - To v1: passkeys and TOTP, Railpack, zero-downtime Compose, metrics charts, managed subdomains, the live-migration demo.
  - Into the MVP: container-spec sanitization, the recovery key, pre-deploy steps, disk-full handling, `moonglass import`, signed releases with a canary channel.
  - Net: less soak-sensitive work, more security-critical work.
- **Week 0 now runs three experiments with pass/fail thresholds** before any product code: a waitlist page on moonglass.dev, a 48-hour agent-and-proxy memory spike, and ten migration interviews.
- **Every week ends with a demo you run yourself on real servers.** That is the human check on agent-written code.
- **Launch is invite-gated on hosted for about three weeks, then open.** Self-hosted is open from day one.

---

## 1. How the plan is built

- **Tests first.** Each week's acceptance tests are written before the code and stay in CI forever (S§7).
- **Friday demo on the test fleet:** three cheap VPSs (2 GB x86, 2 GB arm64, 4 GB x86), freshly reinstalled.
- **Parallel tracks** suit agent development: agent/runtime, control plane/API, UI, docs. Each track gets a written spec; the protocol file and the operation registry are the contracts between them.
- **Multi-user from the first schema.** The MVP shows one person per org on hosted. Organisations, memberships, roles and `org_id` on every row exist from day one, so teams in v1 are not a migration.
- **The product name is a single constant** in code, binary name, config paths (`/etc/moonglass/`), environment variables (`MOONGLASS_*`) and the Go module path (`moonglass.dev/moonglass`, a vanity import so the module path never depends on the GitHub org name).

**Never cut:**
- Zero-downtime deploys for the shapes promised in S§5.3, instant rollback, and auto-rollback (crash and health triggers on, error-rate trigger in shadow mode)
- The typed protocol, the local policy file and container-spec sanitization
- Encrypted secrets with the recovery key
- Scoped tokens, approvals on protected environments with step-up, the audit trail
- Disk-full protection
- Signed releases with automatic revert
- One-command install

**Cut order if a week slips** (first to go at the top):
1. The Coolify importer (keep Compose import)
2. One-click Redis (keep Postgres)
3. Webhook notifications (keep email)
4. Current resource usage in the UI (keep live logs)
5. Re-enrolment between hosted and self-hosted (keep `moonglass export`)

If the week 1 or week 2 gates slip by more than a week, move the launch to week 10. Do not cut from the never-cut list to hold the date.

---

## 2. Week 0: before the clock starts

### 2.1 Things with lead times

| Do | Why now |
|---|---|
| **Name: done.** Moonglass, moonglass.dev owned. Claim the GitHub org (`moonglass` or the nearest available), package names and social handles | Blocks launch assets and the repository |
| **Buy a separate user-content domain** for managed subdomains (V§8, open decision 2) | Needed for the Public Suffix List application; cannot be a subdomain of moonglass.dev |
| **Apply to the Public Suffix List** on cookie-isolation grounds, and **ask Let's Encrypt for a rate-limit adjustment** | No SLA; often one to two weeks, sometimes months, and requests aimed at Let's Encrypt limits are rejected (S§3.4). Managed subdomains are v1, so this no longer blocks launch |
| Repository, CI on real Docker, test fleet; **release signing key on a hardware token; canary and stable channels** | Everything after depends on it; the first agent binary must already be signed |
| Write the threat model (S§4), the first protocol file, the **container-spec deny list** (S§3.2) and the **key design** (S§3.11) as documents | These are the specs the agents build against and the reviewer agent checks against |
| Agent workflow: track specs, reviewer-agent brief, definition of done | So week 1 produces product, not process |

### 2.2 Three experiments, with thresholds

Thresholds are my proposals. Each one says what we do if it fails, so a miss changes the plan rather than getting explained away.

**1. Waitlist page on moonglass.dev (a fake door).** _Runs weeks 0–3._
- **What:** the headline and subline from V§1, three short proof points, and two buttons: "Join the hosted waitlist" and "Tell me when self-hosted ships". One optional question: "What do you deploy with today?" It is an honest waitlist, not a fake checkout, and the page says when each will be ready.
- **Where:** a short post in the places R§3 lists, and the week 2 video once it exists.
- **Pass:** ≥ 300 signups, and ≥ 35% choose hosted.
- **Fail and what changes:**
  - Under 100 signups: the positioning does not land. Rework the headline before the week 5 video.
  - Under 20% hosted: the hosted revenue lines in V§5.6 are weaker than assumed. Weight the Team licence and Enterprise, and keep free hosted small.

**2. 48-hour memory spike.** _Runs in week 0, before the budget in S§6 is written into any public copy._
- **What:** a throwaway `moonglass agent` and `moonglass proxy` (Caddy 2.11.9 with a stub counter module) on the 2 GB VPS. Ten routes, steady synthetic traffic (~20 requests a second), one certificate issuance, one simulated deploy an hour. Also tests the proxy upgrade path (S§3.3): systemd socket activation versus `SO_REUSEPORT`.
- **Pass:**
  - Combined RSS at or under 100 MB at the 99th percentile.
  - Under 5% growth between hour 2 and hour 48.
  - Proxy at or under 60 MB.
  - One proxy upgrade under load with zero failed requests.
- **Fail and what changes:**
  - Over budget: tune the memory limit, drop HTTP/3 by default, or move the public number before anything says "under 100 MB".
  - Neither upgrade path is clean: proxy upgrades become a short, announced, drained restart, and the docs say so.

**3. Ten migration interviews.** _Runs weeks 0–2._
- **What:** 30-minute calls with people running production on Coolify, Dokploy or Railway. Ask what made them pick it, what last went wrong, what a move would take, and what they would pay for. Show the import idea and check what their Coolify API can export.
- **Pass:** at least 6 of 10 describe a recent pain the MVP solves (dropped requests, restores needing SSH, a security worry, footprint, a full disk), **and** at least 3 would move a production app within a month of launch.
- **Fail and what changes:** fewer than 4 of 10 means the lead is wrong. Revisit V§1 before week 5, and check whether guardrails should lead after all.
- **Side effect:** the alpha group. Aim to leave with five people who will run the private alpha from week 5.

---

## 3. The eight weeks

### Week 1: skeleton, sanitizer and the two gates
- **Build:**
  - Install script.
  - `agent enroll` over an outbound connection, printing the key fingerprint; heartbeat.
  - `moonglass proxy` as its own systemd unit with Caddy 2.11.9 embedded, configured by the agent over a Unix socket; our counter module; one hand-written route with automatic HTTPS.
  - **Container-spec sanitizer with its escape suite**, before any `Deploy` exists.
- **Demo:**
  - On a fresh VPS, one command installs agent and proxy, the server shows as online, and a real domain serves over HTTPS.
  - Restart and upgrade the agent under load: the proxy keeps serving with zero failed requests.
- **Gates:** combined idle memory at or under 100 MB in CI, matching the week 0 spike. The sanitizer rejects every fixture in the escape suite.

### Week 2: deploy, switch, roll back, pre-deploy steps
- **Build:**
  - Typed `Deploy`, `SwitchRelease`, `Rollback`, `RunPreDeploy`.
  - Health-gated switch and connection draining.
  - Keep the last five releases; deploy history.
  - One deploy per app at a time.
  - Pre-deploy steps in a sanitized one-off container (S§3.13).
  - **The operation registry** generating REST and the CLI, with owner tokens and hash-chained audit events. It lands here rather than in week 5 so the API is never written twice (build plan W2-1).
  - In the secondary lane: the UI shell (static build embedded in the binary, design tokens, ⌘K, a "servers online" page).
- **Demo:**
  - A load generator holds steady traffic through ten consecutive deploys with **zero failed requests**, then `rollback` completes in under five seconds.
  - A failing pre-deploy step leaves the old release serving.
- **Output:** the first public video. Start building in public here, and link it from the waitlist page.

### Week 3: git push to live, with secrets and a disk that cannot fill
- **Build:**
  - GitHub App connection; deploy key generated on the server.
  - Builds from a Dockerfile or prebuilt image through BuildKit; the build-in-CI template; private registry pulls.
  - Environment variables.
  - **Environment keys, `moonglass keys init`, browser and CLI encryption, `secrets reveal`, `secrets grant`, `secrets rotate`.**
  - Build-cache cap and image cleanup; **disk-full handling** (S§3.13).
- **Demo:**
  - `git push` goes live on the 2 GB server. The stored secret is shown to be ciphertext in the control-plane database.
  - Add a second server to the environment and watch it receive the wrapped key.
  - Fill the disk to 95%: the build is refused, the cache is pruned, an alert fires, and the app keeps serving.

### Week 4: auto-rollback, environments, sign-in, first real UI
- **Build:**
  - Auto-rollback with shadow mode for the error-rate trigger (S§5.4).
  - Environments with a protected flag; resource limits.
  - Live logs and current resource usage.
  - **GitHub sign-in, email magic links, sessions list and revoke.**
  - UI for projects, services, deploys and logs.
  - Email and webhook notifications.
- **Demo:**
  - Push a commit that crash-loops. It rolls back automatically inside two minutes, and a notification arrives.
  - Push a commit that raises the 5xx rate. Shadow mode reports "this would have rolled back" with the numbers. Turn the trigger on, push again, and watch it roll back in the UI.

### Week 5: the agent-safety story
- **Build:**
  - MCP generated from the operation registry that has existed since week 2 (official Go SDK v1.7+).
  - Agent identities with scoped tokens.
  - Approval flow for protected environments and destructive operations, with a diff of what will change and **step-up re-authentication**.
  - Soft delete; audit trail, hash-chained; enforcement of the local policy file.
- **Demo:** a coding agent connected over MCP deploys to staging freely, has its production deploy held for your approval with a diff, and is refused when it tries to delete the production database. Every step appears in the audit trail under the agent's name.
- **Also:** private alpha opens with the interviewees. Second public video.

### Week 6: databases, backups, restore, Compose
- **Build:**
  - One-click Postgres and Redis.
  - Scheduled backups, compressed and encrypted to the environment key, to any S3-compatible bucket; one-click restore; restore in CI.
  - **Compose import:** unchanged services are never touched, and changed services restart in place, with the UI listing them first.
- **Demo:**
  - Create Postgres, schedule a backup, destroy the server, then **restore onto a different server using only the recovery key**.
  - Redeploy a Compose app and show the database container was never touched.
- **Check:** run the swappable/pinned classifier over 30 popular open-source Compose files and record how many services land in each class. This sizes v1 blue/green; it is not a launch item.

### Week 7: self-hosting, portability, import, releases
- **Build:**
  - Self-hosted mode (server, agent and proxy on one box, a one-time bootstrap token, `moonglass server login-link`), with SQLite behind the same storage interface. The storage conformance suite runs green against both backends.
  - `moonglass export` and re-enrolment between hosted and self-hosted.
  - **`moonglass import`** for Compose and Coolify.
  - Free-tier limits on hosted (one person, two servers, no managed subdomains).
  - Control-plane backup (managed PITR on hosted; encrypted snapshot to the user's bucket on self-hosted).
  - **Agent self-update from the canary channel, with signature check and automatic revert**; staged rollout on hosted.
  - First-deploy wizard.
- **Demo:**
  - Import a real Coolify project from an alpha user and deploy it.
  - Upgrade the agent under load with zero failed requests.
  - Push a deliberately broken canary build and watch the agent revert itself.
- **Start:** a seven-day soak on the fleet with scheduled deploys, backups, certificate renewals, a disk filling slowly, and canary updates.

### Week 8: harden and launch
- **Build:** nothing new.
  - Fix what the soak test and alpha users found.
  - Adversarial pass by the reviewer agent over the protocol, sanitizer, key handling and auth surface. Fuzzing left running.
  - Docs: quick start; the "which apps get zero downtime" page (S§5.3, word for word); the threat model with its limits; the recovery-key page; an honest comparison page including Temps Premium (V§1.1).
  - The reproducible benchmark repository (§5). Landing and pricing pages, written in the voice in V§1.2.
- **Demo:** the launch. Three videos: zero dropped requests through ten deploys, a restore onto a new server, and a coding agent that asks before production.
- **Hosted signups are invite-gated** from the waitlist, in batches, so abuse and support load stay inside what one person can handle. Self-hosted is open to everyone.
- **Opening hosted (target ~week 11, after Pro ships).** Open signups once all three hold:
  - Abuse controls are live: account-age checks on GitHub sign-in, per-account rate limits, a report link on every deploy page.
  - Support takes under five hours a week for two weeks running.
  - No serious incident in the previous 14 days.

  The free funnel in V§5.6 needs thousands of orgs, which invites alone cannot supply, so the gate is deliberately short.

---

## 4. The cut: every item from your brief

### In the eight-week MVP

| Item | Scope at launch |
|---|---|
| Build and deploy Docker images | Yes |
| Git sources | GitHub only |
| Registry images | Public and private |
| Connect your own servers | Yes, outbound agent |
| Logs and metrics | Live logs and current resource usage; charts in v1 |
| Database deployments, one-click databases | Postgres and Redis |
| Notifications and alerts | Email and webhook: deploy failed, rolled back, would have rolled back, server offline, disk low, backup failed, approval needed |
| Scheduled backups, S3 storage | Postgres dumps to any S3-compatible bucket, encrypted to the environment key |
| Database restoration | One-click for Postgres, including onto a new server with the recovery key |
| Environment variables and secrets, encrypted at rest and in transit | Yes; envelope keys and recovery key; write-only in the web UI |
| Health checks and alerting | On the server, plus offline detection from the control plane |
| Low footprint | Measured, published, reproducible |
| Domains and SSL | Custom domains with automatic HTTPS; managed subdomains in v1 |
| Self-hosted on the master node | Yes |
| MCP for everything, scoped API and MCP tokens, MCP scopes | Yes, generated from one registry |
| Security at its base | Typed protocol, container-spec sanitization, local policy, no inbound ports |
| Zero-downtime deploys, graceful shutdown | Yes for single-service apps; exact shapes documented (S§5.3) |
| Docker Compose | Import; unchanged services untouched, changed services restart in place; blue/green in v1 |
| Zero-config builds | v1 (Railpack via CLI); MVP has Dockerfile, image and the build-in-CI template |
| Instant rollback, auto-rollback | Yes; error-rate trigger in shadow mode until enabled |
| Pre-deploy steps (migrations) | Yes; failures abort the deploy; irreversible by default |
| Environments | Yes, with a protected flag |
| Deployment diffs and approvals | Single approver, with a diff and step-up |
| Resource limits, deployment history | Yes |
| Image and container cleanup, Docker cleanup | Yes, including the build cache, plus disk-full handling |
| Network management | One isolated network per project |
| Control-plane backup and recovery | Yes |
| Audit events, MFA, session management | Audit and sessions, yes; MFA through GitHub sign-in at launch; passkeys and TOTP in v1 |
| Deployment wizard, one-command install, CLI | Yes |
| Import from elsewhere | `moonglass import` for Compose and Coolify |
| Releases | Signed, canary and stable channels, staged rollout, automatic revert |
| Declarative config | Export only |
| Deployment concurrency control | One per app; newest wins |
| Free-tier limits on hosted | One person, two servers, no managed subdomains, from day one |
| Linear-style UI | Core screens |

### v1, roughly weeks 9–20, in this order

| Item | Note |
|---|---|
| Billing and the Pro plan | First revenue, around week 10 |
| Open hosted signups | Around week 11, on the criteria in week 8 |
| Passkeys and TOTP | Before Team, so approvals can require a real second factor |
| Teams, basic roles, the Team plan; self-hosted Team licence | The main paid trigger (V§5.5), around week 14 |
| Approver rules and agent policy rules | Commercial module (V§5.2) |
| PR previews, including Compose and per-preview migrations | Open core. The best-evidenced team feature (R§10) |
| Zero-downtime Compose | Blue/green for swappable services, sized by the week 6 classifier check |
| Railpack zero-config builds | Through its CLI and BuildKit frontend, pinned, behind canary |
| Metrics charts | From the samples the agent already collects |
| Managed subdomains | Pro and Team; needs the PSL entry or a Let's Encrypt adjustment |
| Live hosted ↔ self-hosted migration without restart, and its demo | Tested on every release from here |
| Agent-verified approvals | Closes the gap in S§4 for protected environments |
| External security review | Protocol, sanitizer, key design, auth. Before any "audited" claim |
| Volume backups and scheduled restore drills | restic |
| Move a project between servers | Parity with Dokploy v0.31.0; depends on volume backups |
| Marketplace listings live | Hetzner and DigitalOcean (§5) |
| MySQL/MariaDB and MongoDB | One-click |
| GitLab and Gitea/Forgejo | Git sources |
| Generic OIDC sign-in | Open core |
| External probes from several regions, on-call routing | Team plan |
| Scheduled jobs | Cron-style |
| Private networking between servers | WireGuard |
| Dedicated build servers | The build-in-CI path covers the MVP |
| Container exec | CLI, human-only, second factor |
| Curated one-click services | About ten, including Ollama and a GitHub Actions runner |
| Wildcard certificates, more DNS providers | DNS-01 |
| Importers for Dokploy and others | Whatever the interviews say people are leaving |
| Automatic rotation of Moonglass-generated secrets; scheduled agent key rotation | S§3.11 |
| Retention and health-check schedule settings | Per app |
| Declarative config applied from a repository | GitOps |
| Anything cut from the MVP | |

### Later

| Item | Note |
|---|---|
| SAML, SCIM, custom roles, air-gapped packaging, compliance exports | Enterprise, in the commercial module; sells after a year of credibility (V§5.6) |
| Secret-manager integrations | Requested at Komodo (R§10) |
| Load balancing across servers | Deliberately late (V§4) |
| Image vulnerability scanning, SBOM for user images | |
| Point-in-time recovery for Postgres | |
| Canvas view | No demand evidence yet (V§4) |
| Podman, log search, OpenTelemetry export | |
| Agency and partner pricing | |

---

## 5. Distribution

The free funnel needs reach a solo founder cannot buy (V§5.6). Three channels that keep working after launch day:

| Channel | What | When |
|---|---|---|
| **Marketplace apps** | A DigitalOcean Marketplace 1-Click and a Hetzner Cloud app: a fresh server with the agent and proxy installed, waiting for an enrolment token | Built in week 7; submitted at launch. Both have review lead times I have not verified, so plan for listings to go live in v1 |
| **`moonglass import`** | Compose and Coolify at launch; Dokploy and others in v1. The comparison page links straight to it | Week 7 |
| **Reproducible benchmark repository** | Scripts that provision identical VPSs and measure idle memory, idle CPU and failed requests across ten deploys for Moonglass, Coolify, Dokploy and, where it fits the hardware, Temps. Method, raw data and versions published; pull requests from other projects welcome | Week 8; rerun on every Moonglass release |

The benchmark is written calmly (V§1.2). It reports numbers and credits what others do well; it is not a takedown.

---

## 6. What to measure from launch day

| Measure | Why |
|---|---|
| Waitlist size, hosted versus self-hosted split, and how fast invites convert to a first deploy | The first read on V§5.6 before billing exists |
| Servers enrolled; orgs with a deploy in the last 30 days | The denominator for every conversion number |
| Time from install, or from `moonglass import`, to first successful deploy | The first-hour promise (V§3) |
| Share of installs that reach a second deploy within a week | Early retention |
| Shadow-mode "would have rolled back" events, and how many the user marked right | Whether the error-rate trigger is ready to be on by default |
| Real auto-rollbacks fired, and how many the user judged wrong | Whether the default triggers are right |
| Sanitizer rejections, by field | Which `agent.toml` exceptions people actually need, and whether the errors explain themselves |
| Disk-low events and prunes | Whether the thresholds are right on small servers |
| Hosted versus self-hosted split | Whether hosted is worth paying for (V§5.8) |
| Orgs hitting the two-server limit | The Pro trigger, before Pro exists |
| Orgs that try to invite a second person | The Team trigger, before Team exists |
| After billing: paid-first signups | The largest line in V§5.6 |

The two limit measures tell you how large the paying population is before you build billing.

---

## 7. Risks, and how each is retired early

### Technical

| Risk | Why it matters | Retired by |
|---|---|---|
| A security product written by agents, with no human who reviews every line | One authorisation or sanitization bug undoes the positioning | The five permanent suites and the reviewer agent from week 1 (S§7); small trusted core; external review in v1; docs that never say "audited" before it is true |
| A container spec gets past the sanitizer | "A short, typed list" stops being true | Deny by default; checks after Compose interpolation; the escape suite from week 1; an external review of the sanitizer specifically |
| Users lose the recovery key and every server | Secrets and backups are unrecoverable, by design | Mandatory `keys init`; a second owner key; the quarterly recovery-key check; plain warnings at setup and on the pricing page |
| Deploys drop requests under real conditions: WebSockets, slow starts, long uploads | It is the headline claim | Load tests from week 2 on every change, across agent and proxy upgrades; the promise scoped to the shapes in S§5.3 |
| A proxy upgrade drops connections | Rare, but visible | Proxy upgrades separate from agent upgrades; the handover tested in the week 0 spike and the deploy suite |
| Two storage backends drift apart | A bug that exists only on self-hosted, found by users | One interface; the conformance suite against both on every change; SQL kept to the common subset |
| Auto-rollback fires on healthy deploys, or rolls code back over a migrated schema | Users switch it off and the feature is dead | Shadow mode for the error-rate trigger; no auto-rollback after an irreversible step; alpha users' real deploys in weeks 5–8 |
| Backup or restore silently produces something unusable | Data loss ends trust permanently | Restore in CI on every change from week 6, including onto a fresh server; an unverifiable restore is reported as failed |
| A bad agent release reaches every server | Every user hit at once | Canary channel, 72 hours before stable, staged rollout, automatic revert |
| Memory creeps above the target | The footprint claim is public | The week 0 spike; measured in CI from week 1 with a fixed budget |
| Docker differences across versions 25 to 29 | Breaks on users' servers, not yours | Version matrix in CI from week 2 |
| Builds or logs exhaust a 2 GB server | First impression on the cheapest hardware | One build at a time; the build-in-CI template; disk-full handling; tested on the 2 GB machines every week |

### Business

| Risk | Why it matters | Retired by |
|---|---|---|
| The free tier converts at 1–2%, not 4% | The free funnel alone needs ~29,000–57,000 active free orgs for $10K MRR (V§5.6) | Plan on four revenue lines; measure paid-first signups; recalibrated gates in V§5.8; keep free hosted cheap |
| No revenue until about month three, and $10K MRR is an 18–30 month outcome | Runway and motivation | Know it going in; Pro before Team because it is the cheapest revenue to ship |
| Temps cuts the price of Premium | Our "team price" angle narrows | The approval gate is free in our open core; the footprint gap cannot be closed with a price change; the comparison page stays fair so it stays credible |
| Coolify ships quickly and already has scoped MCP tokens | The agent-safety story can be claimed by someone else | Lead with correct deploys and architecture they cannot copy without a rewrite (R§5); publish the demos as they happen |
| An unknown vendor asking for deploy rights | Indies hesitate, teams refuse | Open source from the first commit, the published threat model, the local policy file and sanitizer, portability shown on the pricing page |
| Licence backlash | The Dokploy story | The split in V§5.2 is published before launch and never moves features out of the open core |
| PSL entry slow or refused | Managed subdomains stall | They are v1 and not on Free; the Let's Encrypt adjustment request runs in parallel; custom domains always work |
| Abuse of free hosted | Domain reputation and your time | No managed subdomains on Free; invite-gated for about three weeks; account-age checks and rate limits before opening |
| Support load on one person | Launch attention turns into a queue | Docs before launch; error messages that say what to do, including every sanitizer rejection; a public issue tracker for free users |
| Nobody notices | No funnel, no conversion data | The waitlist from week 0, three videos, the benchmark repository, marketplace apps and the importer (§5) |

---

## 8. Decisions

**Resolved 2026-11-16**
1. The name: **Moonglass**.
2. Invite-gated hosted signups at launch, opening around week 11 on the criteria in week 8.
3. Alpha users come out of the week 0 interviews.

**Open**
1. **Is week 8 a date or a quality bar?** I recommend a quality bar with week 10 as the fallback.
2. **The user-content domain** (V§8, open decision 2). Needed in week 0.
3. **GitHub org name**, if `moonglass` is taken.
4. **Experiment thresholds** in 2.2. They are my numbers; adjust them before the experiments start, not after the results arrive.
