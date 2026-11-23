# Moonglass — Phase 4: Milestones

_2026-11-15, revised 2026-11-16 after an outside review, and on 2026-11-22 after the plan audit and its roast (`docs/review/plan-audit.md`). Builds on `research.md` (R§n), `vision.md` (V§n) and `stack.md` (S§n)._

**Short version**

- **The launch is a quality bar, not a date.** It is self-hosted, open-source Moonglass on one VPS, for one person: server, agent and proxy from one install. There is no hosted service at launch.
- **The alpha comes first.** It opens at G4, once the week 0 experiments (G0) have passed: a stranger deploys a real app from git, with a secret, on their own VPS.
- **Money comes from founding pilots,** sold by hand to agencies and small teams at $199 a month from the alpha onwards (V§5). There is no free tier or billing system at launch.
- **Moved to v1 by the plan audit:** hosted, Compose, approvals and MCP, Google sign-in, browser secret entry, reveal and rotate, the Coolify importer and export, staged rollout and marketplace images.
- **Every week ends with a demo you run yourself on real servers.** That is the human check on agent-written code.

---

## 1. How the plan is built

- **Tests first.** Each week's acceptance tests are written before the code and stay in CI forever (S§7).
- **Friday demo on the test fleet:** three cheap VPSs (2 GB x86, 2 GB arm64, 4 GB x86), freshly reinstalled.
- **Parallel tracks** suit agent development: agent/runtime, control plane/API, UI, docs. Each track gets a written spec; the protocol file and the operation registry are the contracts between them.
- **Multi-user from the first schema.** The open core is for one person, but organisations, memberships, roles and `org_id` on every row exist from day one, so the commercial module's members are not a migration.
- **The product name is a single constant** in code, binary name, config paths (`/etc/moonglass/`), environment variables (`MOONGLASS_*`) and the Go module path (`moonglass.dev/moonglass`, a vanity import so the module path never depends on the GitHub org name).

**Never cut:**
- Zero-downtime deploys for the shapes promised in S§5.3, instant rollback, and auto-rollback (crash and health triggers on, error-rate trigger in shadow mode)
- The typed protocol, the local policy file and container-spec sanitization
- Encrypted secrets with the recovery key
- Scoped tokens, step-up and the audit trail
- Backups with a restore you can click
- Disk-full protection
- Signed releases with automatic revert
- One-command install

**Cut order if the work runs long** (first to go at the top):
1. One-click Redis (keep Postgres)
2. Current resource usage in the UI (keep live logs)
3. The first-deploy wizard (keep the quick start)

Do not cut from the never-cut list to reach the launch sooner. The launch waits instead.

---

## 2. Week 0: before the clock starts

### 2.1 Things with lead times

| Do | Why now |
|---|---|
| **Name: done.** Moonglass, moonglass.dev owned. The code is public at `github.com/moonglass-dev/moonglass` under the `moonglass-dev` organization. Package names and social handles are still open | The repository no longer blocks development |
| Repository, CI on real Docker, test fleet; **two release signing keys on hardware tokens (ADR-0007); canary and stable channels** | Everything after depends on it; the first agent binary must already be signed |
| Write the threat model (S§4), the first protocol file, the **container-spec deny list** (S§3.2) and the **key design** (S§3.11) as documents | These are the specs the agents build against and the reviewer agent checks against |
| Agent workflow: track specs, reviewer-agent brief, definition of done | So week 1 produces product, not process |

The user-content domain, the Public Suffix List entry and the Let's Encrypt request wait for managed subdomains (v1).

### 2.2 Three experiments, with thresholds

Thresholds are my proposals. Each one says what we do if it fails, so a miss changes the plan rather than getting explained away. **Week 1 started before these ran** (plan audit, 2026-11-22), so they now gate the alpha (B§2, G0).

**1. Waitlist page on moonglass.dev.**
- **What:** the headline and subline from V§1, three short proof points, and two buttons: "Join the self-hosted alpha" and "Talk to us about a Team pilot ($199/mo, setup and migration included)". One optional question: "What do you deploy with today?" It is an honest waitlist, and the page says when the alpha will be ready.
- **Where:** a short post in the places R§3 lists, and the Dec 1 story (plan audit §6).
- **Pass:** ≥ 300 signups, and ≥ 3 pilot conversations booked.
- **Fail and what changes:**
  - Under 100 signups: the positioning does not land. Rework the headline before the alpha.
  - No pilot conversation by the end of the alpha: the paid line is in doubt. Plan for a small product (V§5.3).

**2. 48-hour memory spike (SPIKE-1).** _Before the alpha, and before the budget in S§6 goes into any public copy._
- **What:** a throwaway `moonglass agent` and `moonglass proxy` (Caddy 2.11.9 with a stub counter module) on the 2 GB VPS. Ten routes, steady synthetic traffic (~20 requests a second), one certificate issuance, one simulated deploy an hour. Also tests the proxy upgrade path (S§3.3): systemd socket activation versus `SO_REUSEPORT`.
- **Pass:**
  - Combined RSS at or under 100 MB at the 99th percentile.
  - Under 5% growth between hour 2 and hour 48.
  - Proxy at or under 60 MB.
  - One proxy upgrade under load with zero failed requests.
- **Fail and what changes:**
  - Over budget: tune the memory limit, drop HTTP/3 by default, or move the public number before anything says "under 100 MB".
  - Neither upgrade path is clean: proxy upgrades become a short, announced, drained restart, and the docs say so.

**3. Ten migration interviews.**
- **What:** 30-minute calls with people running production on Coolify, Dokploy, Kamal or Railway, and with agencies that run servers for clients. Ask what made them pick it, what last went wrong, what a move would take, and what they would pay for. Ask whether they would move a real app within a month, and whether they would pay the pilot price.
- **Pass:** at least 6 of 10 describe a recent pain the MVP solves (dropped requests, restores needing SSH, a security worry, footprint, a full disk), **and** at least 3 would move a real app within a month and pay the pilot price.
- **Fail and what changes:** fewer than 3 who would move and pay means the product is wrong, not the ticket order. Stop and rethink V§1 and V§5 before the alpha opens.
- **Side effect:** the alpha group. Aim to leave with five people who will run the alpha.

---

## 3. The weeks

Week numbers are an order, not a calendar. Each week ends with its demo.

### Week 1: skeleton, sanitizer and the two gates
- **Build:**
  - Install script.
  - `agent enroll` over an outbound connection, printing the key fingerprint; heartbeat.
  - `moonglass proxy` as its own systemd unit with Caddy 2.11.9 embedded, configured by the agent over a Unix socket; our counter module; one hand-written route with automatic HTTPS.
  - **Container-spec sanitizer with its escape suite**, before any `Deploy` exists.
- **Demo:**
  - On a fresh VPS, one command installs agent and proxy, the server shows as online, and a real domain serves over HTTPS.
  - Restart and upgrade the agent under load: the proxy keeps serving with zero failed requests.
- **Gates:** combined idle memory at or under 100 MB in CI. The sanitizer rejects every fixture in the escape suite.

### Week 2: deploy, switch, roll back, pre-deploy steps
- **Build:**
  - Typed `Deploy`, `SwitchRelease`, `Rollback`, `RunPreDeploy`.
  - Health-gated switch and connection draining.
  - Keep the last five releases; deploy history.
  - One deploy per app at a time.
  - Pre-deploy steps in a sanitized one-off container (S§3.13).
  - **The operation registry** generating REST and the CLI, with owner tokens and hash-chained audit events.
- **Demo:**
  - A load generator holds steady traffic through ten consecutive deploys with **zero failed requests**, then `rollback` completes in under five seconds.
  - A failing pre-deploy step leaves the old release serving.
- **Output:** the first public video, used in the Dec 1 story.

### Week 3: git push to live, with secrets and a disk that cannot fill
- **Build:**
  - The control plane on SQLite only (FA1-1, ADR-0002 as amended).
  - Builds on the server from a GitHub repository and commit, with a deploy key generated on the server (`moonglass deploy --git`); builds of a Dockerfile or prebuilt image through BuildKit; the build-in-CI template.
  - **Environment keys, `moonglass keys init`, and `secrets set` from the CLI.** The control plane stores ciphertext only.
  - Build-cache cap and image cleanup; **disk-full handling** (S§3.13).
- **Demo:**
  - `git push` goes live on the 2 GB server, through the build-in-CI template. The stored secret is shown to be ciphertext in the control-plane database.
  - Fill the disk to 95%: the build is refused, the cache is pruned, an alert fires, and the app keeps serving.

### Week 4: sign-in, self-hosted on one box, the real UI — the alpha
- **Build:**
  - **GitHub sign-in, sessions list and revoke, step-up.**
  - **Self-hosted mode:** server, agent and proxy on one box from one install, GitHub as the everyday sign-in, `server login-link` for the first sign-in and emergencies; the control-plane backup.
  - `secrets grant` for each server, secret delivery, environment variables and private registries.
  - Live logs and current resource usage.
  - The UI shell and the screens for projects, services, deploys, logs and servers.
  - The quick start.
- **Demo:** on a fresh VPS, one install; sign in with GitHub; deploy an app from git with a secret; watch it in the UI.
- **Then:** if G0 has passed, the alpha opens with the interviewees. Install it with each person on a call and time their first deploy.

### Week 5: auto-rollback, scoped tokens, deletion
- **Build:**
  - Auto-rollback with shadow mode for the error-rate trigger (S§5.4).
  - Notifications: signed webhooks with Slack and Discord formats, and an outside heartbeat URL, so a monitor elsewhere alerts when the box itself is down.
  - Agent identities with scoped tokens, and the authorisation matrix.
  - Enforcement of the local policy file.
  - Service soft delete, restore and purge, in the CLI and the UI.
- **Demo:**
  - Push a commit that crash-loops. It rolls back automatically inside two minutes, and a Slack message arrives.
  - Push a commit that raises the 5xx rate. Shadow mode reports "this would have rolled back" with the numbers. Turn the trigger on, push again, and watch it roll back in the UI.
  - A coding agent with a token scoped to staging deploys there, and is refused in production.

### Week 6: databases, backups, restore
- **Build:**
  - One-click Postgres and Redis.
  - Scheduled backups, compressed and encrypted to the environment key, to any S3-compatible bucket; a Restore button; restore in CI.
- **Demo:** create Postgres, schedule a backup, destroy the server, then **restore onto a different server using only the recovery key**, from the Restore button.

### Week 7: releases
- **Build:**
  - **Agent self-update from the canary channel, with signature check and automatic revert.** The control plane offers each channel's release to every agent on it at once.
  - The release publish job.
  - First-deploy wizard.
- **Demo:**
  - Upgrade the agent under load with zero failed requests.
  - Push a deliberately broken canary build and watch the agent revert itself.
- **Start:** a seven-day soak on the fleet with scheduled deploys, backups, certificate renewals, a disk filling slowly, and canary updates.

### Week 8: harden and launch
- **Build:** nothing new.
  - Fix what the soak test and alpha users found.
  - Adversarial pass by the reviewer agent over the whole trusted core. Fuzzing left running.
  - Docs: the zero-downtime shapes (S§5.3, word for word); the threat model with its limits; the recovery-key page; an honest comparison page including Temps Premium and GitHub Environments (V§1.1).
  - The reproducible benchmark repository (§5). Landing page, written in the voice in V§1.2.
- **Demo:** the launch, as a Show HN of the self-hosted release. Two videos: zero dropped requests through ten deploys, and a restore onto a new server.

---

## 4. The cut: every item from your brief

### At launch

| Item | Scope at launch |
|---|---|
| Build and deploy Docker images | Yes |
| Git sources | GitHub, through the build-in-CI template; the GitHub App in v1 |
| Registry images | Public and private |
| Connect your own servers | Yes, outbound agent |
| Logs and metrics | Live logs and current resource usage; charts in v1 |
| Database deployments, one-click databases | Postgres and Redis |
| Notifications and alerts | Signed webhooks with Slack and Discord formats: deploy failed, rolled back, would have rolled back, server offline, disk low, backup failed. An outside heartbeat URL. No email |
| Scheduled backups, S3 storage | Postgres dumps to any S3-compatible bucket, encrypted to the environment key |
| Database restoration | A Restore button, including onto a new server with the recovery key |
| Environment variables and secrets, encrypted at rest and in transit | Yes; envelope keys and recovery key; values set from the CLI, names shown in the UI |
| Health checks and alerting | On the server, plus offline detection from the control plane |
| Low footprint | Measured, published, reproducible |
| Domains and SSL | Custom domains with automatic HTTPS; managed subdomains in v1 |
| Self-hosted on one box | Yes: this is the launch |
| Scoped API tokens | Yes, through agent identities; MCP in v1 |
| Security at its base | Typed protocol, container-spec sanitization, local policy, no inbound ports |
| Zero-downtime deploys, graceful shutdown | Yes for single-service apps; exact shapes documented (S§5.3) |
| Docker Compose | v1 |
| Zero-config builds | v1 (Railpack via CLI); the launch has Dockerfile, image and the build-in-CI template |
| Instant rollback, auto-rollback | Yes; error-rate trigger in shadow mode until enabled |
| Pre-deploy steps (migrations) | Yes; failures abort the deploy; irreversible by default |
| Environments | Yes; protected environments in v1, with approvals |
| Deployment diffs and approvals | v1, in Team |
| Resource limits, deployment history | Deployment history yes; resource limits in v1 |
| Image and container cleanup, Docker cleanup | Yes, including the build cache, plus disk-full handling |
| Network management | One isolated network per project |
| Control-plane backup and recovery | Yes |
| Audit events, MFA, session management | The audit trail is written and hash-chained; its view is v1. Sessions yes. MFA through GitHub sign-in; passkeys and TOTP in v1 |
| Deployment wizard, one-command install, CLI | Yes |
| Import from elsewhere | v1 |
| Releases | Signed, canary and stable channels, automatic revert; staged rollout with a managed fleet |
| Deployment concurrency control | One per app; newest wins |
| Linear-style UI | Core screens |

### v1, in this order

| Item | Note |
|---|---|
| The Team offer's code: members and roles, then approvals and protected environments | Commercial module (V§5.1), built as the pilots need them |
| Managed control planes | One instance per customer (ADR-0002 as amended) |
| Railpack zero-config builds | First, if the interviews ask for it |
| Container exec | CLI, human-only, second factor |
| Compose | Unchanged services untouched, stateless blue/green, stateful stop-then-start; revisit the refusals first (`docs/specs/compose.md`) |
| Passkeys and TOTP | So approvals can require a real second factor |
| PR previews, including Compose and per-preview migrations | Open core. The best-evidenced team feature (R§10) |
| The GitHub App | Push-to-deploy without a CI workflow |
| Google sign-in, generic OIDC | Google with managed control planes; OIDC in the open core |
| Browser secret entry, `secrets reveal`, `secrets rotate`, `RewrapEnvKey` | S§3.11 |
| The audit view and `moonglass audit verify` | The chain is written from launch |
| MCP | Generated from the registry |
| Environment and project deletion | Services can be deleted at launch |
| `moonglass export`, re-enrolment, `moonglass import` for Compose and Coolify | |
| Staged rollout | With a managed fleet |
| Metrics charts | From the samples the agent already collects |
| Resource limits | |
| Managed subdomains | Needs the PSL entry or a Let's Encrypt adjustment |
| Agent-verified approvals | Closes the gap in S§4 for protected environments |
| External security review | Protocol, sanitizer, key design, auth. Before any "audited" claim |
| Volume backups and scheduled restore drills | restic |
| Move a project between servers | Parity with Dokploy v0.31.0; depends on volume backups |
| Marketplace listings | Hetzner and DigitalOcean (§5) |
| MySQL/MariaDB and MongoDB | One-click |
| GitLab and Gitea/Forgejo | Git sources |
| External probes, scheduled jobs, private networking between servers, dedicated build servers, curated one-click services, wildcard certificates | As before |
| Automatic rotation of Moonglass-generated secrets; scheduled agent key rotation | S§3.11 |
| Declarative config applied from a repository | GitOps |

### Later

| Item | Note |
|---|---|
| SAML, SCIM, custom roles, air-gapped packaging, compliance exports | Commercial module |
| Secret-manager integrations | Requested at Komodo (R§10) |
| Load balancing across servers | Deliberately late (V§4) |
| Image vulnerability scanning, SBOM for user images | |
| Point-in-time recovery for Postgres | |
| Canvas view | No demand evidence yet (V§4) |
| Podman, log search, OpenTelemetry export | |

---

## 5. Distribution

Reach a solo founder cannot buy, in the order it happens:

| Channel | What | When |
|---|---|---|
| **The Dec 1 story** | A technical post with the ten-deploy video, linking the waitlist and asking for interviews. Not a Show HN (plan audit §6) | Dec 1 |
| **Show HN** | The self-hosted release, once anyone can install it | At launch |
| **Reproducible benchmark repository** | Scripts that provision identical VPSs and measure idle memory, idle CPU and failed requests across ten deploys for Moonglass, Coolify, Dokploy and, where it fits the hardware, Temps. Method, raw data and versions published; pull requests from other projects welcome | At launch; rerun on every release |
| **Marketplace apps** | A DigitalOcean 1-Click and a Hetzner Cloud app | v1 |
| **`moonglass import`** | Compose and Coolify | v1 |

The benchmark is written calmly (V§1.2). It reports numbers and credits what others do well; it is not a takedown.

---

## 6. What to measure from the alpha

| Measure | Why |
|---|---|
| Waitlist size, alpha versus pilot interest | The first read on V§5 |
| Time from install to first successful deploy | The first-hour promise (V§3) |
| Share of installs that reach a second deploy within a week | Early retention |
| Pilot conversations, pilots signed and the price they signed at | The gates in V§5.4 |
| Shadow-mode "would have rolled back" events, and how many the user marked right | Whether the error-rate trigger is ready to be on by default |
| Real auto-rollbacks fired, and how many the user judged wrong | Whether the default triggers are right |
| Sanitizer rejections, by field | Which `agent.toml` exceptions people actually need, and whether the errors explain themselves |
| Disk-low events and prunes | Whether the thresholds are right on small servers |
| Alpha users who ask to add a second person | The Team trigger, before members exist |

---

## 7. Risks, and how each is retired early

### Technical

| Risk | Why it matters | Retired by |
|---|---|---|
| A security product written by agents, with no human who reviews every line | One authorisation or sanitization bug undoes the positioning | The permanent suites and the reviewer agent from week 1 (S§7); a smaller trusted core after the plan audit's cuts; `high` review on the whole trusted core; external review in v1; docs that never say "audited" before it is true |
| A container spec gets past the sanitizer | "A short, typed list" stops being true | Deny by default; the escape suite from week 1; an external review of the sanitizer specifically |
| Users lose the recovery key and every server | Secrets and backups are unrecoverable, by design | Mandatory `keys init`; a second owner key; the quarterly recovery-key check; plain warnings at setup and in the docs |
| Deploys drop requests under real conditions: WebSockets, slow starts, long uploads | It is the headline claim | Load tests from week 2 on every change, across agent and proxy upgrades; the promise scoped to the shapes in S§5.3 |
| A proxy upgrade drops connections | Rare, but visible | Proxy upgrades separate from agent upgrades; the handover tested in SPIKE-1 and the deploy suite |
| Auto-rollback fires on healthy deploys, or rolls code back over a migrated schema | Users switch it off and the feature is dead | Shadow mode for the error-rate trigger; no auto-rollback after an irreversible step; alpha users' real deploys |
| Backup or restore silently produces something unusable | Data loss ends trust permanently | Restore in CI on every change from week 6, including onto a fresh server; an unverifiable restore is reported as failed |
| A bad agent release reaches every server | Every user hit at once | Canary channel, 72 hours before stable, automatic revert |
| Memory creeps above the target | The footprint claim is public | SPIKE-1; measured in CI from week 1 with a fixed budget |
| Docker differences across versions 25 to 29 | Breaks on users' servers, not yours | Version matrix in CI from week 2 |
| Builds or logs exhaust a 2 GB server | First impression on the cheapest hardware | One build at a time; the build-in-CI template; disk-full handling; tested on the 2 GB machines every week |

### Business

| Risk | Why it matters | Retired by |
|---|---|---|
| Nobody pays $150 or more a month | There is no business at $9–29 for one person (V§5.2) | Pilots from the alpha; the gates in V§5.4; resize the plan rather than cut the price |
| The open-source build is good enough for teams | Teams self-host free forever | Members, roles and approvals in the commercial module (V§5.1) |
| Nobody wants to move | The workaround is a VPN in front of the panel | The ten interviews gate the alpha (§2.2) |
| Coolify ships quickly and already has scoped MCP tokens | A feature-list wedge has a short shelf life | Lead with correct deploys and architecture they cannot copy without a rewrite (R§5); publish the demos as they happen |
| GitHub Environments already gate production for free | Approvals alone will not sell | Approvals are part of Team, not the reason for it; the comparison page says so (V§1.1) |
| An unknown vendor asking for deploy rights | Indies hesitate, teams refuse | Open source from the first commit, the published threat model, the local policy file and sanitizer |
| Licence backlash | The Dokploy story | The split in V§5.1 is published before launch and never moves features out of the open core |
| Support load on one person | Fifty customers is a lot for one person | Pilots sold one at a time; docs before the alpha; error messages that say what to do, including every sanitizer rejection |
| Nobody notices | No funnel | The waitlist, the Dec 1 story, the videos, the benchmark repository and the Show HN at launch (§5) |

---

## 8. Decisions

**Resolved 2026-11-16**
1. The name: **Moonglass**.
2. _Superseded 2026-11-22: there is no hosted service at launch._ Was: invite-gated hosted signups at launch.
3. Alpha users come out of the week 0 interviews.

**Resolved 2026-11-20**
1. **GitHub organization:** `moonglass-dev`. The repository `github.com/moonglass-dev/moonglass` is public. The module path stays `moonglass.dev/moonglass`.

**Resolved 2026-11-22 (plan audit)**
1. **Week 8 is a quality bar, not a date.** The launch is G8, and the alpha opens at G4 after G0.
2. **The user-content domain** waits for managed subdomains (v1).
3. **The experiment thresholds** in 2.2 now include paying the pilot price.
