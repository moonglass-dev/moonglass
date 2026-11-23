# Moonglass — Phase 2: Vision

_2026-11-15, revised 2026-11-16 after an outside review, and on 2026-11-22 after the plan audit and its roast (`docs/review/plan-audit.md`). Builds on `research.md`, cited as R§n. The product is called Moonglass (moonglass.dev). This document is mostly about making it pay._

**Short version**

- Lead with calm, correct deploys on your own servers: zero-downtime deploys, one-click restores, a control plane that never SSHes into your servers, in under 100 MB.
- **The launch is self-hosted and open source, for one person.** There is no hosted service at launch and no free hosted tier.
- **One paying buyer:** agencies and 3–10 person teams running production on their own servers. They pay for what exists only when several people share servers: members and roles, approvals, and later SSO. That is sold as a Team licence, or as a control plane we run for them.
- **One price to start:** founding pilots at $199 a month, including setup and migration. The list price is set after five pilots.
- Honest maths: $10K MRR is about 50 customers at about $200. If pilots will not close at $150 or more a month, Moonglass is a $2–4K MRR product, and the plan says so instead of chasing hobbyist volume (5.3).
- Limits block growth, never operation. The agent never checks a plan.

---

## 1. Positioning

**Headline:** Calm deploys on your own servers.
**Subline:** Zero-downtime deploys, one-click restores and a control plane that never SSHes into your servers, in under 100 MB.

The longer statement, for the README and the about page:

> **Moonglass is an open-source deploy platform for your own servers.** Deploys don't drop requests, restores are one click, the control plane never SSHes into your servers, and it runs in under 100 MB. Our control plane never holds a readable secret, and it can only ask your server for a short, typed list of things.

**The demo, not the headline:** *Give your coding agent a token. It ships to staging on its own and cannot touch production.* Asking you before production is the approval gate, which is part of Team (5.1).

Each claim is something we have to be able to prove in public:

| Claim | Proof we ship with it |
|---|---|
| "your own servers" | We sell no compute. Zero inbound ports for us on the server. |
| "zero-downtime deploys" | A published load test: zero failed requests across ten consecutive deploys. **Scoped honestly:** at launch this covers single-service apps (image, Dockerfile or git) behind the proxy with a health check. The exact shapes are listed in the docs (S§5.3). Compose is v1. |
| "one-click restores" | A Restore button on the database's screen. Restore runs in CI on every change, and the docs show a restore onto a *different* server using only the owner's recovery key. |
| "never SSHes into your servers" | The agent dials out, and we hold no SSH keys. Day-to-day work needs no shell: deploys, logs, rollback, restore and disk cleanup. Installing does, and so does debugging inside a container until container exec (v1). We do not claim the user's SSH port is closed. |
| "under 100 MB" | Agent plus proxy combined, measured over 48 hours (SPIKE-1) before this goes into any public copy, and in CI after. A reproducible benchmark repository anyone can rerun. |
| "never holds a readable secret" | Values are encrypted in the CLI. The database stores ciphertext and wrapped keys only. The threat model states plainly what a takeover of the *running* control plane would get (S§4). |
| "a short, typed list of things" | The public `.proto` file. The agent also rejects privileged containers, host namespaces, host bind mounts, added capabilities and Docker-socket mounts unless the server's own `/etc/moonglass/agent.toml` allows them (S§3.2). Without that check, the typed protocol would not be enough. |
| the coding-agent demo | A reproducible script: a coding agent with a token scoped to staging deploys there, and is refused when it tries to deploy to or delete in production. |

Why this and not "agent-safe PaaS": Coolify already ships scoped MCP tokens and an audit log (R§5), and Temps sells approval gates (R§3). Teams that gate production today mostly do it with GitHub Environments and branch protection, which cost nothing. Neither Coolify nor Dokploy can match the "never SSHes in" and "short, typed list" rows without a rewrite, because they drive servers over SSH from the panel (R§3).

### 1.1 Against Temps Premium and GitHub Environments

Temps is the closest product and the only one selling guardrails as a product. GitHub Environments is what most teams already use to gate production. The comparison page says this calmly and fairly:

| | Temps Premium | GitHub Environments | Moonglass |
|---|---|---|---|
| Approval gates | In a $399/org/mo self-hosted licence | Free with Actions, enforced on GitHub | In Team (v1), enforced on your server |
| Tamper-evident audit | Hash-chained, in Premium | GitHub's audit log | Hash-chained, free everywhere |
| Smallest server | 4 GB RAM minimum (Docker plus TimescaleDB) | n/a | Agent and proxy under 100 MB; fits a 2 GB VPS with room for apps |
| Hosted control plane | Temps Cloud, $29/mo, invite-only beta | n/a | Run for Team customers, one control plane each |
| Maturity | Pre-1.0, ~920 stars | Mature | Pre-launch. We are behind, which is why the page must be fair |

### 1.2 Voice

Moonglass should feel calm, elegant, premium and soft. In practice:

- **Plain claims with numbers.** "Zero failed requests across ten deploys", not "blazing-fast".
- **No war words in public copy.** Words like moat, blast radius, kill switch, hostage-taking, attack surface and "the one to beat" are fine in these planning docs and stay out of the site, docs and videos.
- **Softer phrasing for the same fact.** "Never SSHes into your servers", not "no open SSH" (which is also inaccurate). Use "light" in prose and give the exact number.
- **No dunking on competitors.** The comparison page credits what Coolify, Dokploy, Temps and GitHub do well.
- **"PaaS" goes in the subline and SEO only**, never the headline.
- **Watch the word "agent".** It means both the Moonglass process on the server and the user's coding agent. Public copy always says "Moonglass agent" or "coding agent", never just "agent" (open decision 8.3).

---

## 2. Target user

**Stage 1, reputation: the solo builder.**
One to three VPSs (Hetzner, DigitalOcean), ships with Claude Code or Cursor, currently on Coolify, Dokploy, Kamal or raw Compose. Cares that it is light, that the control plane cannot own the server, that deploys do not drop requests, and that restores do not need a shell. Gets the open-source build free and will not pay. Will post about it.

**Stage 2, revenue: agencies and 3–10 person product teams.**
Run production on their own servers to control cost, have no dedicated ops person, often manage servers for several clients. The buyer is the tech lead, CTO or agency owner, not the developers. This is the only paying buyer until the pilots say otherwise.

The issue data makes one thing clear: the stage-1 audience does not ask for what stage 2 buys. Governance requests sit at 0–1 votes in both leading repos, while previews, backups and proxies get dozens (R§10). **The indie community will not pull the paid tier into existence. It has to be sold, one customer at a time, to a different person.**

**Not the target:** teams that want someone else to run the servers (Railway, Render), Kubernetes shops (Northflank), homelab users managing media stacks (Komodo, Portainer).

---

## 3. What we win on

In priority order. If two conflict, the higher one wins.

1. **Correct deploys.** Zero-downtime for single-service apps, instant rollback with no Swarm or external registry, automatic rollback on crash loops and failed health checks, and an error-rate rollback that starts in shadow mode. Compose is v1, done properly: stateless services blue/green, stateful services stop-then-start (S§5.3).
2. **Small blast radius.** Outbound-only agent, no SSH keys held by us, typed commands, container-spec sanitization, secrets the control plane cannot read, tamper-evident audit trail. Answers 117 advisories at the two leaders and the Railway breach (R§0, R§3).
3. **Day 2 without SSH.** One-click restore, readable failure reasons, pre-deploy migration steps that cannot half-apply, a full disk that degrades gracefully instead of taking the server down.
4. **Footprint.** A single static binary; agent and proxy under 100 MB together. Incumbents idle at 300 MB–1.2 GB, and Temps needs 4 GB (R§3).
5. **First hour.** One install on one VPS, GitHub sign-in, a deploy from git in minutes, and docs that answer the question (R§4). Importers are v1.
6. **Guardrails enforced on the server.** Scoped tokens and soft delete at launch; out-of-band approvals for teams in v1. Coolify relies on a flag the caller sets; one MCP deploy already bypassed its own queue and took an app down (R§10, Coolify #12442).

UI quality supports all six. It is not on the list, because a good UI does not keep users whose deploys drop requests.

---

## 4. What we deliberately won't do

- **No Kubernetes, no Swarm, no scheduler.** Many independent servers under one control plane. If you need bin-packing, you have outgrown us.
- **No compute, no managed databases.** We never hold your workload.
- **No free hosted tier and no hobbyist plan.** The open-source build is free and complete for one person. We do not sell $9 hosting against our own free binary (5.2).
- **No template arms race.** Twenty gold-standard services we test on every release, not 300 we cannot maintain.
- **No bundled analytics, error tracking or session replay.** That is Temps' identity (R§3).
- **No per-seat pricing.**
- **Safety for one person's servers is never paid for.** Scoped tokens, the audit trail, soft delete, container sanitization, the local policy file, encrypted secrets and step-up are free everywhere. What is paid is what exists only when several people share servers: members and roles, approvals and their rules, SSO (5.1). This is the line research recommends: charge for coordination, not data (R§6).
- **No paywall on what the community asks for most.** PR previews, including Compose, are in the open core when they ship (v1).
- **No plan checks in the agent, no kill switch.** A lapsed licence or subscription never stops a running app, a deploy to existing resources, a rollback or an export. A lapsed licence never switches a guardrail off (5.1).
- **No home-made licence, no mid-flight relicensing.** Apache-2.0 core; the commercial module lives in its own repository under its own licence from the first commit (R§3, Dokploy). The split in 5.1 is published before launch, so nothing moves behind a paywall later.
- **No canvas view in the MVP.** Nothing in the research shows demand for it; revisit after v1.
- **No limits on things that live on your disk.**

---

## 5. The money

### 5.1 What is open and what is paid

Settled before launch, so it never has to change under users' feet:

| Open core, Apache-2.0 | Commercial module, separate repository and licence |
|---|---|
| Everything one person needs: deploys, rollback, auto-rollback, backups and restore, secrets, unlimited servers and apps | **Members and roles:** inviting people, the four roles, removing them |
| Scoped tokens and agent identities | **Approvals:** protected environments with a human approval gate, approver rules, two-person approval, time windows |
| Audit trail, soft delete, container sanitization, local policy file, step-up | **Agent policy rules** across agents and repositories |
| PR previews, including Compose (v1) | SSO, SCIM, custom roles, compliance exports (later) |
| Passkeys, TOTP and generic OIDC sign-in (v1) | Agency features the pilots ask for, such as a workspace per client and reports for clients |

It is sold two ways, at the same price: a **Team licence** for a control plane the customer runs, or a **managed control plane** we run for them, one per customer on the same binary (ADR-0002 as amended).

How the licence behaves:

- The licence is a signed file checked offline by the commercial module on the control plane. There is no phone-home and no check in the agent, so it works air-gapped.
- **A lapsed licence freezes rules; it never removes a guardrail.** Existing approver and policy rules keep being enforced. Members keep their access. You cannot add or edit them until you renew, and you can always delete them.

Why members and approvals are paid, against the earlier plan to put basic roles in the open core: Moonglass is light and easy to self-host, so convenience alone will not make a team pay (plan audit, Roast 3). The second person is the trigger, on self-hosted as well as managed.

### 5.2 The offer

| | **Open source** | **Team, founding pilot** |
|---|---|---|
| Price | $0 | $199/mo, locked for 12 months, including setup and migration of the customer's apps |
| People | 1 | Unlimited, with roles |
| Servers, apps, deploys, backups | Unlimited | Unlimited |
| Where the control plane runs | The user's box | The customer's box (licence) or ours (managed) |
| Approvals | – | When built (v1), included |
| Support | Community | Direct, from the founder |

- **Why not $9 and $29.** At those prices $10K MRR needs 345–670 payers, more than one person can sell to or support (plan audit, Roast 1). No self-serve plan in the category clears $39 (R§6), so this is a sales-led price for a buyer with a budget, sold one at a time.
- **The list price and its meter** (per server, or per client org) are set after five pilots. The planning number is about $200 a month per customer.
- **No card-free trial loop and no billing system** until the list price exists. Pilots are invoiced by hand.

### 5.3 Does it add up?

All figures are **my estimates**.

- $10K MRR is about **50 customers at about $200**. That is a number one person can sell, migrate and support, unlike 345–670 hobbyist accounts.
- **Timeline:** the first pilot comes from the alpha. Fifty customers is a 12–24 month outcome if pilots convert and keep paying.
- **If pilots will not close at $150 or more a month**, while we are migrating their servers for them, the market will not pay enough. Then Moonglass is a $2–4K MRR product, and the plan is resized rather than stretched (5.4).
- Coolify reached ~$23K MRR from ~4,000 customers at ~$6 each after years and 62K stars (R§1). We are not trying to repeat that model.

### 5.4 Gates: how we know early whether it is working

| Checkpoint | Healthy | Stop and rethink |
|---|---|---|
| Interviews (G0, M§2.2) | ≥ 3 of 10 would move a real app within a month and pay the pilot price | < 3 |
| Pilot conversations by the end of the alpha | ≥ 5 | < 2 |
| Pilots signed at $150/mo or more | ≥ 3 within two months of the alpha | 0: plan for a small product |
| Pilots still paying at month 3 | ≥ 80% | < 60% |

Do not cut the price to pass a gate. A pilot that only signs at $29 is evidence against the plan, not a customer.

### 5.5 Levers, in order

1. **Agencies.** One control plane, one org per client, the same work they already do for each client.
2. **The solo user who brings Moonglass to work.** The open-source build is the top of the funnel; the second person at work is the sale.
3. **Teams already gating with GitHub Environments** who want the same gate enforced on their own servers, without a token that can reach production.
4. **The reproducible benchmark** and the honest comparison page (M§5).

---

## 6. Enforcing limits while keeping projects portable

**Principle: what is paid is code in the commercial module, never a check in the open build or the agent.**

1. **The agent has no concept of a plan.** It obeys any correctly signed, policy-allowed command from the control plane it is enrolled with.
2. **The open-source build contains no licence check.** The commercial module adds operations (inviting members, approvals) rather than unlocking code that is already there.
3. **After a lapse,** everything keeps running and stays deployable and rollback-able. Rules freeze (5.1). Export always works.
4. **A project is state on the server plus a coordinator.** Running state (containers, volumes, certificates, deploy history) lives with the agent. The control plane is a replaceable coordinator.
5. **Moving a control plane** (from the customer's box to ours, or back) is the control-plane backup (W7-5) restored on the other side. Secrets never pass through in plaintext: agents keep their keys, and the owner's recovery key works with either side (S§3.11). Re-enrolling agents without a restart, and `moonglass export`, are v1.

---

## 7. Tensions handed to `stack.md`, and where they landed

| Tension | Resolution |
|---|---|
| Ciphertext-only control plane vs a usable UI | Envelope keys per environment, owner recovery key. At launch the CLI sets values and the UI shows names only; browser entry is v1. Honest limits on browser encryption stated (S§3.11, S§4) |
| Web terminal vs "a short, typed list" | No host shell ever; container exec in v1, CLI-only, human-only, fresh second factor (S§5.2) |
| Zero-downtime Compose | v1: unchanged services are never touched, stateless services switch blue/green, and stateful services stop-then-start. The exact shapes are documented (S§5.3) |
| Error-budget rollback defaults | Crash-loop and health-check rollback on by default; error-rate rollback in shadow mode until the user turns it on (S§5.4) |
| Hosted and self-hosted | One binary, SQLite only. A managed control plane is one instance per customer (ADR-0002 as amended) |

---

## 8. Decisions

### Confirmed 2026-11-15

1. _Superseded 2026-11-22 (no Pro plan)._ Was: Pro at $9 for individuals.
2. _Superseded 2026-11-22 (founding pilots at $199)._ Was: Team at $29 plus $3 per server over five.
3. No limits on backups or on-box metrics.
4. _Superseded 2026-11-22 (no free hosted tier)._ Was: free hosted tier of 1 person and 2 servers.
5. _Superseded 2026-11-16 by the split in 5.2, and again on 2026-11-22 by 5.1._
6. "Works financially" means **$10K MRR for a solo founder**, with other revenue avenues expected along the way. Every later decision is weighed against one person building and operating this.

### Confirmed 2026-11-16

1. The product is **Moonglass**, at moonglass.dev.
2. _Superseded 2026-11-22 by 5.1._ Was: the open core included multi-user with basic roles and one approval gate; approver rules and SAML/SCIM in the commercial module.
3. _Superseded 2026-11-22._ Was: free hosted for 1 person and 2 servers.
4. _Superseded 2026-11-22 by 5.3._ Was: plan on 1–2% free-to-paid and four revenue lines.
5. Auto-rollback: crash-loop and health-check triggers on by default; the error-rate trigger runs in shadow mode until enabled.
6. Lead with correct deploys and footprint; agent guardrails are the demo. Position fairly against Temps Premium, and now GitHub Environments (1.1).
7. _Superseded 2026-11-22._ Was: hosted signups invite-gated for a few weeks after launch.

### Decided 2026-11-22 (plan audit; the owner confirms by merging its PR)

1. The launch is self-hosted and open source, for one person. No hosted service at launch.
2. One paying buyer, agencies and small teams; founding pilots at $199/mo; the list price after five pilots (5.2).
3. Members, roles and approvals are in the commercial module (5.1).
4. The control plane keeps SQLite only; a managed control plane runs one instance per customer (ADR-0002 as amended).
5. The headline claim is "never SSHes into your servers", not "nothing to SSH into".

### Open

1. **The list price and its meter.** After five pilots.
2. **The user-content domain** for managed subdomains. Not needed until managed subdomains (v1, with a managed control plane).
3. **"Agent" naming.** Keep "Moonglass agent" for the server process and always say "coding agent" for Claude Code and the like, or give the server role a different public name. The CLI stays `moonglass agent` either way.
