# Moonglass — Milestones

_Builds on `research.md` (R§n), `vision.md` (V§n) and `stack.md` (S§n). The ticket list is `build-plan.md` (B§n)._

**Short version**

- **Three milestones, each a release people can run, each a step toward v1:** the alpha, the launch and v1. There are no dates. A milestone ships when its gates pass (B§2).
- **Alpha: the deploy path and the dashboard.** One install on a VPS, GitHub sign-in, push-to-deploy with zero downtime, secrets and environments in the dashboard, domains with TLS, live logs, rollback, several servers, upgrades from the dashboard. It opens to the waitlist.
- **Launch: what most Coolify and Dokploy users use every day.** Auto-rollback and notifications, coding agents with inline approvals and MCP, teams and roles, deletion and the activity log, databases with backups and the Restore button, Railpack and Compose, PR previews, metrics and log history, templates and the Coolify import. It is the public launch.
- **v1: the rest.** Cloud and Business, passkeys and OIDC, approver rules and agent-verified approvals, a container terminal, volume backups, private networking, scheduled jobs, more git hosts and templates.
- **Every group of tickets ends with a demo the owner runs on real servers.** That is the human check on agent-written code.

---

## 1. How the plan is built

- **Tests first.** Each group's acceptance tests are written before the code and stay in CI forever (S§7).
- **A demo on the test fleet at every gate:** three cheap VPSs (2 GB x86, 2 GB arm64, 4 GB x86), freshly reinstalled.
- **Parallel tracks** suit agent development: agent and runtime, control plane and API, dashboard, docs. Each track gets a written spec; the protocol file and the operation registry are the contracts between them.
- **Multi-user from the first schema.** Organisations, memberships, roles and `org_id` on every row exist from day one, so teams are not a migration.
- **Dashboard first.** A capability is done when its dashboard flow ships. The CLI, API and MCP come from the same registry operations, so each flow exists on every surface (ADR-0006).
- **The product name is a single constant** in code, binary name, config paths (`/etc/moonglass/`), environment variables (`MOONGLASS_*`) and the Go module path (`moonglass.dev/moonglass`, a vanity import so the module path never depends on the GitHub organisation).

**Never cut:**
- Zero-downtime deploys for the shapes promised in S§5.3, instant rollback, and auto-rollback (crash and health triggers on, error-rate trigger in shadow mode until enabled)
- The typed protocol, the local policy file and container-spec sanitization
- Encrypted secrets with the recovery key
- Scoped tokens, step-up, approvals and the audit trail
- Backups with a restore you can click
- Disk-full protection
- Signed releases with automatic revert
- One-command install
- The dashboard flow for every capability

If the work runs long, a milestone waits. Nothing moves from a milestone to a later one to make a date, because there are no dates.

---

## 2. Before the alpha

### 2.1 Things with lead times

| Do | Why now |
|---|---|
| **Name: done.** Moonglass, moonglass.dev owned. The code is public at `github.com/moonglass-dev/moonglass` | The repository is live |
| Repository, CI on real Docker, test fleet; **two release signing keys on hardware tokens (ADR-0007); canary and stable channels** | Everything after depends on it; the first agent binary must already be signed |
| The threat model (S§4), the protocol file, the **container-spec deny list** (S§3.2) and the **key design** (S§3.11) as documents | These are the specs the agents build against and the reviewer checks against |
| The waitlist page (W0-5) from `docs/plan/landing.md` | The alpha opens to it |
| The user-content domain, its Public Suffix List entry and a Let's Encrypt rate-limit request | Cloud's managed subdomains (V1-4) need them |
| A Stripe account with Stripe Tax, and the Business licence key | Cloud and Business (W7-4) |

### 2.2 Three experiments, with thresholds

Thresholds are proposals. Each one says what changes if it fails, so a miss changes the plan rather than getting explained away.

**1. The waitlist on moonglass.dev.**
- **What:** the hero, the status section and the pricing from `docs/plan/landing.md`, a "Join the waitlist" form with one optional question ("What do you deploy with today?"), and the plan in public.
- **Where:** a technical post with the ten-deploy video, and the places R§3 lists. Not a Show HN: that waits for the launch, when anyone can install it.
- **Pass:** ≥ 1,000 signups before the alpha opens, at least a third of them on Coolify or Dokploy.
- **Fail and what changes:** under 300 signups means the positioning does not land: rework the headline and the demo before the launch.

**2. 48-hour memory spike (SPIKE-1).** _Before the alpha, and before "under 100 MB" goes into any public copy._
- **What:** a throwaway `moonglass agent` and `moonglass proxy` (Caddy 2.11.9 with the counter module) on the 2 GB VPS. Ten routes, steady synthetic traffic (about 20 requests a second), one certificate issuance, one simulated deploy an hour. Also tests the proxy upgrade path (S§3.3): systemd socket activation versus `SO_REUSEPORT`.
- **Pass:** combined RSS at or under 100 MB at the 99th percentile; under 5% growth between hour 2 and hour 48; proxy at or under 60 MB; one proxy upgrade under load with zero failed requests.
- **Fail and what changes:** over budget means tune the memory limit, drop HTTP/3 by default, or change the public number before anything says "under 100 MB". If neither upgrade path is clean, proxy upgrades stay a short, announced, drained restart, and the docs say so.

**3. Ten conversations with people switching.**
- **What:** 30-minute calls with people running production on Coolify, Dokploy, Kamal or Railway, and with agencies that run servers for clients. Ask what made them pick it, what last went wrong, what a move would take, and what is missing from Moonglass's feature list.
- **Pass:** at least 6 of 10 would move a real app once the launch milestone ships.
- **Fail and what changes:** fewer means a parity gap the list does not cover. File it as a ticket in the launch before the launch gate.
- **Side effect:** the first alpha users. Aim to leave with ten people who will run the alpha.

---

## 3. The milestones

Each group of tickets in B§3 ends with a gate. The demos below are the owner's, on the reinstalled fleet.

### Alpha

**Alpha 1 to 3: skeleton, deploys, git and secrets (G1 to G3).**
- **Build:** the install script; `agent enroll` over an outbound connection with the key fingerprint; heartbeat; `moonglass proxy` with Caddy embedded, configured by the agent over a Unix socket; the container-spec sanitizer with its escape suite; typed `Deploy`, `SwitchRelease`, `Rollback` and `RunPreDeploy`; the health-gated switch and draining; the operation registry generating REST and the CLI, with hash-chained audit; the control plane on SQLite; builds on the server from a GitHub repository and commit with a server-made deploy key; environment keys and encrypted secrets; disk-full handling.
- **Demo:** ten consecutive deploys under load with **zero failed requests**, then a rollback in under five seconds; a failing pre-deploy step leaves the old release serving; a commit builds and goes live on the 2 GB box; the stored secret is ciphertext in the database; a disk filled to 95% refuses the build, prunes the cache, and keeps the app serving.

**Alpha 4 to 6: sign-in, the one-box install, the dashboard, upgrades (G4).**
- **Build:** GitHub sign-in, sessions and step-up; server, agent and proxy on one box from one install, with the control plane's domain on the box's proxy; the control-plane backup; secret grant and delivery; private registries; live logs and current usage; the dashboard shell with Inbox, Servers and Projects, the project tabs, the live runtime panel and status dots; secrets, the recovery key and environments in the dashboard; the GitHub App and push-to-deploy; adding servers from the dashboard; the first-run flow; signed self-update with revert, and Update from the dashboard; the quick start.
- **Demo:** on a fresh VPS, one install; sign in with GitHub; make the recovery key; connect a repository; set a secret; push and watch it go live in the dashboard; roll back; add a second server; upgrade with one click, and watch a broken canary revert itself.
- **Then:** the alpha opens to the waitlist. Install it with the first users on a call and time their first deploy (§6).

### Launch

**Launch 1 to 4: auto-rollback, notifications, coding agents, teams, deletion (G5).**
- **Build:** auto-rollback with shadow mode for the error-rate trigger (S§5.4); notifications by webhook, Slack, Discord, Telegram and email, with an outside heartbeat URL; agent identities and scoped tokens with the authorisation matrix; local policy enforcement; protected environments and approvals with inline diffs; MCP; the Agents screen; members, invitations and roles; Google sign-in; soft delete, restore and purge for services, environments and projects; the Activity view.
- **Demo:** push a commit that crash-loops: it rolls back inside two minutes and a Slack message arrives. Push one that raises the 5xx rate: shadow mode reports "this would have rolled back" with the numbers; turn the trigger on and watch it roll back. A coding agent deploys to staging over MCP, and its production deploy waits in the Inbox until you approve it from the diff. Invite a teammate as a viewer, who can see everything and deploy nothing.

**Launch 5 and 6: databases, backups, restore, secrets complete (G6).**
- **Build:** one-click Postgres, MySQL, MariaDB, MongoDB and Redis; scheduled backups, compressed and encrypted to the environment key, to any S3-compatible bucket; the Restore button; restore in CI; reveal, rotate and grant in the dashboard.
- **Demo:** create Postgres from the dashboard, schedule a backup, destroy the server, then **restore onto a different server using only the recovery key**, from the Restore button.

**Launch 7 to 10: builds, previews, metrics, templates, import (G7).**
- **Build:** Railpack zero-config builds; Compose done properly, in the dashboard and the CLI; PR previews with their own URLs and databases; metrics charts and log history; the template format, twenty tested templates and the gallery; the Coolify import.
- **Demo:** import a Coolify setup; deploy a template with its database; open a pull request and click its preview URL; close it and watch the preview go; see a week of CPU and requests in the runtime panel.

**Launch 11: harden and launch (G8).**
- **Build:** nothing new. Fix what the soak and the alpha users found; the adversarial review of the whole trusted core; the launch docs; the benchmark repository; the comparison page; the landing and pricing pages.
- **Demo:** the launch, as a Show HN. Two videos: zero dropped requests through ten deploys, and a coding agent's deploy approved from the Inbox.

### v1

**v1 1 and 2: Cloud and Business (G9).**
- **Build:** the extension points of the licensed build; managed instance mode; the Cloud service, sign-up and billing (`docs/specs/billing.md`) in its own repository; staged rollout; export, import and re-enrolment between Cloud and self-hosted; managed subdomains; the Business module.
- **Demo:** sign up for Cloud, connect a server, deploy, add a server and see the price change before it enrols, then export to a self-hosted box and watch the agents re-enrol. **Cloud opens to the waitlist, founding members first.**

**v1 3 and 4: the rest (G10).**
- **Build:** passkeys and TOTP; OpenID Connect sign-in; approver rules and two-person approval; agent-verified approvals; the container terminal; scheduled key rotation; volume backups and restore drills; moving a service between servers; private networking between servers; scheduled jobs; resource limits; build servers; GitLab, Gitea and Forgejo; thirty more templates; the Dokploy import; DNS-01 wildcard certificates; uptime checks; declarative environments; marketplace images; the external security review.
- **Demo:** approve a production deploy with a passkey that the server itself verifies; open a terminal into a container from the dashboard; move a service with its database to another server.

---

## 4. Every capability, by milestone

| Capability | Alpha | Launch | v1 |
|---|---|---|---|
| Git push-to-deploy | GitHub App; Dockerfile and image builds | Railpack | GitLab, Gitea, Forgejo; build servers |
| Compose | | Unchanged services untouched, stateless blue/green, stateful stop-then-start, in the dashboard and CLI | Declarative environments from the repository |
| PR previews | | Preview environments, URLs, databases, PR comments and statuses | Previews from GitLab, Gitea and Forgejo |
| Domains and TLS | Custom domains, automatic HTTPS, DNS check | Wildcard preview domains | DNS-01 wildcards; managed subdomains on Cloud |
| Variables and secrets | Set in the dashboard and CLI, encrypted end to end; recovery key and environments in the dashboard | Reveal, rotate and grant in the dashboard | Scheduled rotation |
| Databases | | Postgres, MySQL, MariaDB, MongoDB, Redis; backups to S3; Restore button | Volume backups and restore drills; move between servers |
| Logs and metrics | Live logs, current usage | Log history, metrics charts | Uptime checks |
| Servers | Many servers, outbound agents, add from the dashboard | | Private networking between servers |
| Rollbacks | Instant rollback | Auto-rollback, shadow mode for the error rate | |
| Webhooks and notifications | The Inbox | Webhooks, Slack, Discord, Telegram, email, outside heartbeat | |
| Teams and roles | | Members, invitations, four roles, organisations | Approver rules |
| Coding agents | | Agent identities, scoped tokens, approvals with inline diffs, MCP | Agent-verified approvals |
| Sign-in | GitHub, login link | Google | Passkeys, TOTP, OpenID Connect; SAML and SCIM in Business |
| Audit | The hash chain is written from the first operation | The Activity view, `audit verify` | Export in Business |
| API, CLI | Generated from the registry | API reference in the docs | |
| Templates | | Twenty tested templates, the gallery | Fifty |
| Import | | Coolify | Dokploy; export and re-enrol between Cloud and self-hosted |
| Upgrades | Signed self-update, revert, Update in the dashboard | | Staged rollout on Cloud |
| Container access | | | The container terminal |
| Other | Disk-full handling, control-plane backup | Deletion with restore | Scheduled jobs, resource limits, marketplace images |
| Hosting | Self-hosted | Self-hosted | Cloud and Business |

**Not planned:** SAML outside Business; load balancing across servers; image vulnerability scanning; point-in-time recovery for Postgres; Podman; a canvas view (V§4).

---

## 5. Distribution

Reach a small team cannot buy, in the order it happens:

| Channel | What | When |
|---|---|---|
| **A technical post** | The ten-deploy video, the plan in public, and the waitlist. Not a launch | Before the alpha |
| **Show HN** | The public launch, once anyone can install it | At the launch |
| **Reproducible benchmark repository** | Scripts that provision identical VPSs and measure idle memory, idle CPU and failed requests across ten deploys for Moonglass, Coolify, Dokploy and, where it fits the hardware, Temps. Method, raw data and versions published; pull requests from other projects welcome | At the launch; rerun on every release |
| **The Coolify import and the comparison page** | Switching in one pass, and a fair comparison (V§1.1) | At the launch |
| **Cloud for the waitlist** | Founding members first (`docs/specs/pricing.md`) | v1 |
| **Marketplace apps** | A DigitalOcean 1-Click and a Hetzner Cloud app | v1 |

The benchmark is written calmly (V§1.2). It reports numbers and credits what others do well; it is not a takedown.

---

## 6. What to measure from the alpha

| Measure | Why |
|---|---|
| Waitlist size, and the share on Coolify or Dokploy | The first read on V§5.4 |
| Time from install to first successful deploy | The first-hour promise (V§3) |
| Share of installs that reach a second deploy within a week | Early retention |
| Flows people finish in the dashboard, and the ones they drop to the CLI for | Whether the dashboard is complete |
| Shadow-mode "would have rolled back" events, and how many people marked right | Whether the error-rate trigger is ready to be on by default |
| Real auto-rollbacks fired, and how many people judged wrong | Whether the default triggers are right |
| Sanitizer rejections, by field | Which `agent.toml` exceptions people need, and whether the errors explain themselves |
| Disk-low events and prunes | Whether the thresholds are right on small servers |
| What alpha users ask for that is not in the plan | Parity gaps, filed as launch tickets |

---

## 7. Risks, and how each is retired early

### Technical

| Risk | Why it matters | Retired by |
|---|---|---|
| A security product written by agents, with no human who reviews every line | One authorisation or sanitization bug undoes the positioning | The permanent suites and the reviewer from the first ticket (S§7); `high` review on the whole trusted core; the adversarial review before the launch; an external review in v1; docs that never say "audited" before it is true |
| A container spec gets past the sanitizer | "A short, typed list" stops being true | Deny by default; the escape suite from the skeleton; the external review of the sanitizer |
| Browser-side key handling | The dashboard trusts the JavaScript the control plane serves | The strict CSP, the age library loaded only where keys are handled, the threat model's honest limits, and the CLI as the stronger path (S§3.11) |
| Users lose the recovery key and every server | Secrets and backups are unrecoverable, by design | The key saved and checked at first run; a second owner key; the quarterly check; plain warnings |
| Deploys drop requests under real conditions: WebSockets, slow starts, long uploads | It is the headline claim | Load tests on every change, across agent and proxy upgrades; the promise scoped to the shapes in S§5.3 |
| Compose files in the wild do not fit | Compose is a launch feature | LN-13 widens the subset before W6-4a builds it, and the survey (W6-5) checks thirty real files |
| Auto-rollback fires on healthy deploys, or rolls code back over a migrated schema | People switch it off | Shadow mode for the error-rate trigger; no auto-rollback after an irreversible step |
| Backup or restore silently produces something unusable | Data loss ends trust | Restore in CI on every change, including onto a fresh server; an unverifiable restore is reported as failed |
| A bad agent release reaches every server | Every user hit at once | Canary channel, 72 hours before stable, automatic revert; staged rollout on Cloud |
| Memory creeps above the target | The footprint claim is public | SPIKE-1; measured in CI on every change with a fixed budget |
| Docker differences across versions 25 to 29 | Breaks on users' servers | The version matrix in CI |
| Builds or logs exhaust a 2 GB server | First impression on the cheapest hardware | One build at a time; disk-full handling; tested on the 2 GB machines at every gate |

### Business

| Risk | Why it matters | Retired by |
|---|---|---|
| The parity list misses what switchers need | They try it and go back | The ten conversations, the alpha's requests filed as launch tickets, and the Coolify import as the test |
| Cloud at $4 does not cover its costs | Free self-hosted is not a business by itself | One small process per account (S§5.5); yearly billing to cut payment fees; Business at $29 |
| Coolify ships quickly and already has scoped MCP tokens | A feature-list wedge has a short shelf life | Lead with correct deploys, approvals and an architecture they cannot copy without a rewrite (R§5) |
| An unknown vendor asking for deploy rights | People hesitate | Open source from the first commit, the published threat model, the local policy file and the sanitizer |
| Licence backlash | The Dokploy story | The split in V§5.1 is published before the launch, and nothing ever moves out of the open core |
| Support load | Free users at scale | Docs that answer the question, error messages that say what to do, and priority support only on Business |
| Nobody notices | No funnel | The post, the videos, the benchmark repository, the import and the Show HN (§5) |
