# Moonglass — Phase 2: Vision

_2026-11-15, revised 2026-11-16 after an outside review. Builds on `research.md`, cited as R§n. Decisions already taken: the free hosted tier stays; the product is called Moonglass (moonglass.dev). This document is mostly about making it pay._

**Short version**

- Lead with calm, correct deploys: zero-downtime, one-click restores, nothing to SSH into, under 100 MB. Agent guardrails are the demo, not the headline.
- The open core covers everything one person needs, plus multi-user with basic roles and PR previews. Teams pay for approver rules, agent policy rules and identity federation, through hosted Team or a self-hosted Team licence. This is settled before launch, so nothing moves behind a paywall later.
- Free hosted stays, narrower: 1 person, 2 servers, no managed subdomains.
- Honest maths: at a realistic 1–2% free-to-paid rate, the free tier alone needs ~29,000–57,000 active free orgs to reach $10K MRR. That is more than any competitor is known to have. **$10K MRR comes from four lines together**, and free conversion is one of the smaller ones (5.6).
- Temps already sells approval gates, at $399/org/mo. Our angle is **guardrails at a team price, on a footprint 40x smaller**.
- Limits block growth, never operation. The agent never checks a plan.

---

## 1. Positioning

**Headline:** Calm deploys on your own servers.
**Subline:** Zero-downtime deploys, one-click restores and nothing to SSH into, in under 100 MB.

The longer statement, for the README and the about page:

> **Moonglass is an open-source deploy platform for your own servers.** Deploys don't drop requests, restores are one click, there is nothing to SSH into, and it runs in under 100 MB. Our control plane never holds a readable secret, and it can only ask your server for a short, typed list of things.

**The demo, not the headline:** *Hand it to your coding agent. It ships to staging on its own, asks you before production, and cannot touch what you've protected.*

Each claim is something we have to be able to prove in public:

| Claim | Proof we ship with it |
|---|---|
| "your own servers" | We sell no compute. Zero inbound ports for us on the server. |
| "zero-downtime deploys" | A published load test: zero failed requests across ten consecutive deploys. **Scoped honestly:** at launch this covers single-service apps (image, Dockerfile or git) behind the proxy with a health check. The exact shapes are listed in the docs (S§5.3). Compose services you changed restart in place until v1; services you didn't change are never touched. |
| "one-click restores" | Restore runs in CI on every change. The docs show a restore onto a *different* server using only the owner's recovery key. |
| "nothing to SSH into" | Outbound-only agent, no SSH keys in our hands. Logs, restore, rollback and disk cleanup all work without a shell. We do not claim the user's SSH port is closed. Most servers still run SSH, and it simply isn't ours. |
| "under 100 MB" | Agent plus proxy combined, measured over 48 hours before week 1 and in CI after. A reproducible benchmark repository anyone can rerun. |
| "never holds a readable secret" | The database stores ciphertext and wrapped keys only. The threat model states plainly what a takeover of our *running* service would get (S§4). |
| "a short, typed list of things" | The public `.proto` file. The agent also rejects privileged containers, host namespaces, host bind mounts, added capabilities and Docker-socket mounts unless the server's own `/etc/moonglass/agent.toml` allows them (S§3.2). Without that check, the typed protocol would not be enough. |
| the agent demo | A reproducible script: a coding agent with a valid token deploys to staging, waits for approval on production, and is refused when it tries to delete the production database. |

Why this and not "agent-safe PaaS": Coolify already ships scoped MCP tokens and an audit log (R§5), and Temps sells approval gates (R§3). Neither Coolify nor Dokploy can match the "nothing to SSH into" and "short, typed list" rows without a rewrite, because they drive servers over SSH from the panel (R§3). Temps can match them; it cannot match the footprint or the price.

### 1.1 Against Temps Premium

Temps is the closest product and the only one selling guardrails today. The comparison page says this calmly and fairly:

| | Temps Premium | Moonglass |
|---|---|---|
| Approval gates | In a $399/org/mo self-hosted licence | One approval gate per protected environment, free in the open core. Approver rules and agent policy rules in Team: $29/mo hosted, or a self-hosted Team licence |
| Tamper-evident audit | Hash-chained, in Premium | Hash-chained, free everywhere |
| Smallest server | 4 GB RAM minimum (Docker plus TimescaleDB) | Agent and proxy under 100 MB; fits a 2 GB VPS with room for apps |
| Hosted control plane | Temps Cloud, $29/mo, invite-only beta | Free for one person; Pro $9; Team $29 |
| Analytics, error tracking | Bundled; it is their identity | Not ours to build (§4) |
| Maturity | Pre-1.0, ~920 stars | Pre-launch. We are behind, which is why the page must be fair |

The risk is that Temps cuts its price. Footprint and an open-source approval gate are the parts a price cut does not erase.

### 1.2 Voice

Moonglass should feel calm, elegant, premium and soft. In practice:

- **Plain claims with numbers.** "Zero failed requests across ten deploys", not "blazing-fast".
- **No war words in public copy.** Words like moat, blast radius, kill switch, hostage-taking, attack surface and "the one to beat" are fine in these planning docs and stay out of the site, docs and videos.
- **Softer phrasing for the same fact.** "Nothing to SSH into", not "no open SSH" (which is also inaccurate). "Asks you before production", not "the agent that cannot delete prod". Use "light" in prose and give the exact number.
- **No dunking on competitors.** The comparison page credits what Coolify, Dokploy and Temps do well.
- **"PaaS" goes in the subline and SEO only**, never the headline.
- **Watch the word "agent".** It means both the Moonglass process on the server and the user's coding agent. Public copy always says "Moonglass agent" or "coding agent", never just "agent" (open decision 8.3).

---

## 2. Target user

**Stage 1, reputation: the solo builder.**
One to three VPSs (Hetzner, DigitalOcean), ships with Claude Code or Cursor, currently on Coolify, Dokploy, Railway Hobby or raw Compose. Cares that it is light, that the dashboard is not an attack surface, that deploys do not drop requests, and that restores do not need a shell. Will not pay much. Will post about it.

**Stage 2, revenue: the 3–10 person product team or small agency.**
Runs production on its own servers to control cost, has no dedicated ops person, lets coding agents open PRs and increasingly deploy. The buyer is the tech lead or CTO, not the developers.

One thing the issue data makes clear: the stage-1 audience does not ask for what stage 2 buys. Governance requests sit at 0–1 votes in both leading repos, while previews, backups and proxies get dozens (R§10). **The indie community will not pull the paid tier into existence. It has to be designed for, and sold to, a different person.** That is also why previews stay in the open core and governance goes in the commercial module: the split follows who asks for what.

**Not the target:** teams that want someone else to run the servers (Railway, Render), Kubernetes shops (Northflank), homelab users managing media stacks (Komodo, Portainer).

---

## 3. What we win on

In priority order. If two conflict, the higher one wins.

1. **Correct deploys.** Zero-downtime for apps, instant rollback with no Swarm or external registry, automatic rollback on crash loops and failed health checks, and an error-rate rollback that starts in shadow mode. Zero-downtime Compose follows in v1. Coolify's most-voted request, deferred to v5 (R§3).
2. **Small blast radius.** Outbound-only agent, no SSH keys held by us, typed commands, container-spec sanitization, secrets the hosted side cannot read, tamper-evident audit trail. Answers 117 advisories at the two leaders and the Railway breach (R§0, R§3).
3. **Day 2 without SSH.** One-click restore, readable failure reasons, pre-deploy migration steps that cannot half-apply, a full disk that degrades gracefully instead of taking the server down. (Moving apps between servers is now parity, since Dokploy shipped it in v0.31.0. We build it in v1 as table stakes.)
4. **Footprint.** A single static binary; agent and proxy under 100 MB together. Incumbents idle at 300 MB–1.2 GB, and Temps needs 4 GB (R§3).
5. **Guardrails enforced on the server, at a team price.** Per-agent identity, scopes, out-of-band human approval, soft delete, protected environments. Coolify relies on a flag the caller sets; one MCP deploy already bypassed its own queue and took an app down (R§10, Coolify #12442). Temps enforces properly but charges $399.
6. **First hour.** One command to install, `moonglass import` from Compose or Coolify, a deploy in under five minutes, docs that answer the question (R§4).

Correct deploys moved above blast radius in this revision. The reason: the indie audience feels deploys before it feels architecture. The ordering of the *architecture* has not changed.

UI quality supports all six. It is not on the list, because a good UI does not keep users whose deploys drop requests.

---

## 4. What we deliberately won't do

- **No Kubernetes, no Swarm, no scheduler.** Many independent servers under one control plane. If you need bin-packing, you have outgrown us.
- **No compute, no managed databases.** We never hold your workload.
- **No template arms race.** Twenty gold-standard services we test on every release, not 300 we cannot maintain.
- **No bundled analytics, error tracking or session replay.** That is Temps' identity (R§3).
- **No per-seat pricing.**
- **No paywall on safety.** Scoped tokens, agent identities, the audit trail, soft delete, container sanitization, the local policy file and one approval gate per protected environment are free everywhere. MFA is free everywhere: through GitHub sign-in at launch, with our own passkeys and TOTP in v1.
- **No paywall on what the community asks for most.** PR previews, including Compose, are in the open core.
- **No plan checks in the agent, no kill switch.** A lapsed subscription never stops a running app, a deploy to existing resources, a rollback or an export. A lapsed licence never switches a guardrail off (5.2).
- **No home-made licence, no mid-flight relicensing.** Apache-2.0 core; the commercial module lives in its own repository under its own licence from the first commit (R§3, Dokploy).
- **No canvas view in the MVP.** Nothing in the research shows demand for it; revisit after v1.
- **No limits on things that live on your disk.** See 5.1.

---

## 5. The money: keeping the free tier and making it pay

### 5.1 One rule for what is limited

**We limit only what we store, relay or probe, and what exists only when more than one actor is involved.**

| Brief said | Verdict | Why |
|---|---|---|
| Limit backups on free | **Drop.** | Backups go to the user's bucket and cost us nothing. Capping them reads as hostage-taking, and a free user who loses data blames us. |
| Less frequent metrics and health checks on free | **Split.** | On-box collection is free at any frequency. Probes from *our* network cost us and are fair to limit. |
| Shorter retention on free | **Split.** | Logs and metrics on the user's disk: their call. The audit trail we hold off-box: limited in the UI, with free export to their bucket on every plan. |
| 1 user | **Keep.** The main trigger. | |
| Server cap, generous | **Keep, at 2.** | Coolify's $5 plan includes 2 servers (R§2). "Free is what Coolify charges for" is a clean line. |
| Managed subdomains on free | **Drop from Free.** | The biggest abuse surface (phishing), the only free feature that depends on the Public Suffix List and Let's Encrypt limits, and a real cost to us. Custom domains stay unlimited. |

### 5.2 What is open and what is paid

Settled before launch, so it never has to change under users' feet:

| Open core, Apache-2.0 | Commercial module, separate repository and licence |
|---|---|
| Everything one person needs | **Approver rules:** who may approve which environment, two-person approval, required approvers, time windows |
| Multi-user with basic roles: owner, admin, member, viewer | **Agent policy rules** across agents and repositories: which agent may deploy which repo where, rate limits, allowed hours |
| Agent identities and scoped tokens | SAML, SCIM, custom roles (Enterprise) |
| Protected environments with one human approval gate | Compliance exports, air-gapped packaging (Enterprise) |
| PR previews, including Compose and per-preview migrations (v1) | Hosted-only parts: billing, entitlements, abuse handling |
| Audit trail, soft delete, container sanitization, local policy file | |
| Passkeys, TOTP and generic OIDC sign-in (v1) | |

It is sold three ways: included in **hosted Team**, as a **self-hosted Team licence**, and as **Enterprise**.

How the licence behaves:

- The licence is a signed file checked offline by the commercial module on the control plane. There is no phone-home and no check in the agent, so it works air-gapped.
- **A lapsed licence freezes rules; it never removes a guardrail.** Existing approver and policy rules keep being enforced. You cannot add or edit them until you renew, and you can always delete them.

Why previews stay open, against the reviewer's suggestion: they are the most-voted request in the category (R§10), and Coolify ships them free. Putting them behind a licence is the Dokploy-style backlash we are trying to avoid. They still help revenue, on hosted, where they make a second seat useful.

### 5.3 Plans

| | **Free** (hosted) | **Pro** (hosted) | **Team** (hosted) | **Team licence** (self-hosted) | **Enterprise** | **Open source** (self-hosted) |
|---|---|---|---|---|---|---|
| Price | $0, no card | $9/mo | $29/mo, founding price | Proposed: $29/mo + $3/server over five, billed annually (8.1) | From ~$499/mo, annual | $0 |
| Humans | 1 | 1 | Unlimited, basic roles | Unlimited, basic roles | Unlimited, custom roles | Unlimited, basic roles |
| Servers | 2 | 5 included, then $3 each | 5 included, then $3 each | Same meter | Contract | Unlimited |
| Apps, databases, deploys, backups, on-box logs and metrics | Unlimited | Unlimited | Unlimited | Unlimited | Unlimited | Unlimited |
| Agent identities with scoped tokens | 2 | 10 | Unlimited | Unlimited | Unlimited | Unlimited |
| Protected environments with an approval gate | 1 | 3 | Unlimited | Unlimited | Unlimited | Unlimited |
| Approver rules, agent policy rules | – | – | Yes | Yes | Yes | – |
| Concurrent PR previews (v1) | 1 | Unlimited | Unlimited | Unlimited | Unlimited | Unlimited |
| Managed subdomains (v1) | – | Yes | Yes | n/a | Yes | n/a |
| Zero-downtime, auto-rollback, soft delete, container sanitization | Yes | Yes | Yes | Yes | Yes | Yes |
| Audit trail searchable in our UI | 14 days | 90 days | 1 year | Your disk | Custom | Your disk |
| Probes from our network | 5 min, 1 region | 1 min | 1 min, 3 regions, on-call routing | n/a | Custom | n/a |
| Sign-in at launch | GitHub, email link | Same | Same | GitHub, login link from the server | + SAML, SCIM | GitHub, login link from the server |
| Sign-in in v1 | + passkeys, TOTP | Same | Same | + OIDC | | + passkeys, TOTP, OIDC |
| Support | Community | Email | Priority | Email | SLA | Community |

The Enterprise floor is a placeholder anchored on Render's $499 Scale plan, the cheapest public tier with SSO and SCIM (R§3).

- **Pro at $9.** The only proven payer in this category is an individual at $5–15 (R§6). At 5 servers it is $9 against Coolify's $14.
- **$3 per server beyond five**, on both paid plans and the Team licence. It is the same meter Coolify, Dokploy and Easypanel use (R§2). It lets the sticker sit at $29 while revenue follows the size of what is being run. Team bills: 5 servers $29, 8 servers $38, 12 servers $50, 20 servers $74, 30 servers $104.
- **The Team licence costs the same as hosted Team**, so price never pushes a team one way or the other; they choose on operations.

### 5.4 Would a 3–10 person team pay $49 a month?

**As a flat sticker at launch: mostly no.** From the tech lead's seat:

- Dokploy gives unlimited users for $15, Railway for $20, Render for $25 with audit logs included (R§1, R§6).
- Self-hosting Coolify, or our own open core, is free. A team this size has someone who can run one more binary.
- We have no track record, and the product holds deploy rights to production.

**At $29 plus metering: yes, if three things are true.** The team would feel the loss within a week. Hosted is clearly less work than self-hosting (zero upgrades, alerts that fire when their server is down, no dashboard to expose). And the bill is predictable.

**The week-one test for each candidate paid feature**

| Feature | Missed within a week? | Evidence | Role |
|---|---|---|---|
| More than one human (hosted) | Immediately | Every comparator gates it | **The hosted trigger.** Free on self-hosted with basic roles. |
| PR previews, unlimited (hosted) | At the first pull request | 128 votes across four Dokploy issues (R§10) | **The reason to stay on hosted.** In the open core, so it sells hosting, not a licence. |
| Approver rules | The first time an agent or a junior deploys to production | Temps sells it at $399; no community pull (R§5, R§10) | **The licence trigger.** Sold to the lead, not discovered by the dev. |
| Agent policy rules | When the second agent or second repo arrives | PocketOS post-mortems ask for this (R§3) | **The story.** Gets us the meeting; rarely the line item. |
| Audit trail for a year | At the first incident or customer security questionnaire | Render includes it at $25 (R§6) | Expected, not premium. |
| Multi-region probes and on-call routing | When production has paying users | Standard | Bundle filler. |
| Faster metrics, longer on-box retention, more backups | Never; they would rather self-host | R§6 | **Not a trigger. Removed.** |

**How to get to $49 honestly:** launch Team at $29 as a founding price, locked for early customers. Once there are ~50 paying teams and public proof for approvals and rollback, test $49 on new signups.

### 5.5 The free-to-paid trigger

**Primary: a second human.** Inviting anyone with write access on hosted starts a 14-day Team trial with no card. At the end the org picks Team or drops back to one member. Read-only share links to a deploy log or status page stay free.

**Secondary:** a third server (Pro), a second concurrent preview (Pro), a managed subdomain (Pro, v1), a second protected environment.

Rules for how limits behave:

- They bite at the moment of *adding* something, in the flow, with the price and a one-click trial.
- They never bite at runtime. Nothing running is ever stopped, throttled or hidden.
- Hitting a limit never blocks deploy, rollback, restore or export of what already exists.

### 5.6 Does it add up? Redone at 1–2%

The previous base case assumed 4% of active free orgs pay (3% Pro, 1% Team). That number was anchored on Coolify. But Coolify has no free hosted plan, so its payers are people who chose paid hosting over free self-hosting. That is not a free-to-paid conversion rate (R§6). Free hosted tiers in developer tools usually convert in the low single digits. **I now plan on 1–2%.**

All figures are **my estimates**. Assumed: 70% of payers on Pro at ~$10, 30% on Team at ~$35. "Active" means at least one deploy in the last 30 days.

| Free → paid | MRR per 1,000 active free orgs | Active free orgs for $10K MRR | Payers at that point |
|---|---|---|---|
| 1.0% | $175 | ~57,000 | ~570 |
| 1.5% | $263 | ~38,000 | ~570 |
| 2.0% | $350 | ~29,000 | ~570 |
| _Old base case, 4%_ | _$650_ | _~15,400_ | _~615_ |

**What that means, honestly:** 29,000–57,000 *active* hosted free orgs is roughly the size of Coolify's entire internet-visible install base (~52,900, R§1), and Coolify has had years and 62K stars. **The free tier alone will not get a solo founder to $10K MRR in a reasonable time.** Its job is reputation and trial, so we hold its cost down (1 person, 2 servers, no managed subdomains, invite-gated at first).

**One way $10K MRR actually adds up** (illustrative, my estimate):

| Line | Assumption | MRR |
|---|---|---|
| Free → paid | 10,000 active free orgs at 1.5% | ~$2,600 |
| Paid-first hosted | ~175 orgs that sign up straight to Pro or Team: self-hosters who want it managed, people with 3+ servers. This is Coolify's whole model (4,000+ customers, no free plan), and 175 is ~4% of their base | ~$3,500 |
| Self-hosted Team licences | ~50 teams at ~$40 | ~$2,000 |
| Enterprise | ~4 contracts at ~$6K a year | ~$2,000 |
| **Total** | | **~$10,100** |

Reading it:

- **No single line gets there.** Free conversion is the second-smallest line. Paid-first hosted is the largest, and it depends on hosted being visibly better than self-hosting (5.7, item 5).
- **Timeline: 18–30 months if adoption goes well.** Pro is the first revenue, from about week 10. Team and licences follow from about week 14. Enterprise comes after a year of public credibility.
- **Cost of the free tier stays small if the architecture holds:** about one to three cents per active org per month in compute (S§5.5). Abuse handling, email deliverability and your time are the real costs. That is why there are no managed subdomains on free and why the launch is invite-gated.
- **If paid-first hosted stays near zero**, hosted is a convenience people won't pay for. Then the business is the Team licence plus Enterprise, and the free hosted tier should shrink rather than grow.

### 5.7 Levers that move conversion, in order

1. **Make the second seat the natural next step.** Deploy notifications, approval requests and preview links all go to people. Every one is an invitation surface.
2. **Hosted must be visibly better than self-hosting our own core**, or teams take the free path: zero-touch upgrades, off-box alerting and audit, managed subdomains, probes that fire when the server is down.
3. **Previews that work for Compose.** The most-requested thing the incumbents have not finished (R§10). Open source, but a team behaviour that is easiest on hosted.
4. **Auto-start the trial at the limit; never ask for a card first.**
5. **Distribution where servers are born:** Hetzner and DigitalOcean marketplace apps, `moonglass import` for Coolify and Compose users, and a reproducible benchmark anyone can rerun (milestones M§5).
6. **Agencies.** One org per client is how they already work. A partner price for 5+ orgs turns one buyer into several subscriptions.

### 5.8 Gates: how we know early whether it is working

Proposed thresholds, mine, to be argued with. Recalibrated to 1–2%:

| Checkpoint | Healthy | Stop and rethink |
|---|---|---|
| 1,000 active free orgs | ≥ 15 paying from free (1.5%), of which ≥ 4 Team | < 7 paying, or < 2 Team |
| Paid-first hosted signups, monthly, from month 2 of billing | ≥ 15 | < 5 |
| Orgs that hit the seat limit | ≥ 30% start the Team trial | < 10% |
| Team trials | ≥ 35% convert | < 15% |
| Paying teams at month 3 | ≥ 90% still paying | < 75% |
| Teams we can observe | Majority hosted, or self-hosted teams buying the licence | Majority self-hosting without a licence |

If the last row goes wrong, lean on the Team licence and Enterprise, and shrink free hosted. Do not cut the price.

---

## 6. Enforcing limits while keeping projects portable

**Principle: plans are entitlements on a control plane. They are never properties of a project or of an agent.**

1. **The agent has no concept of a plan.** It obeys any correctly signed, policy-allowed command from the control plane it is enrolled with. The open-source core contains no licence check, so a self-hosted control plane is unlimited by construction.
2. **The hosted control plane checks entitlements only on "add" operations:** invite a member, enrol a server, open a preview, add a protected environment, mint an agent identity, claim a managed subdomain.
3. **Over the limit after a downgrade or lapse:** everything keeps running. Existing resources stay deployable and rollback-able. Extra seats become read-only; extra servers keep serving but accept no new apps. Export always works.
4. **A project is a file plus state on the server.** Desired state lives in a declarative `moonglass.toml`. Running state (containers, volumes, certificates, deploy history) lives with the agent. The control plane is a replaceable coordinator.
5. **Moving between hosted and self-hosted is a re-enrolment, not a redeploy.**
   - `moonglass export` writes a versioned, documented bundle: project definitions, history, members, and secrets as ciphertext with their wrapped keys. It is a logical export, so it works between hosted Postgres and self-hosted SQLite (S§3.7).
   - `moonglass agent enroll --to <new control plane>` on each server swaps coordinator and trust root. Containers are not restarted by design.
   - Secrets never pass through either control plane in plaintext. Agents keep their keys, and the owner's recovery key works with either control plane (S§3.11).
   - At launch: export plus re-enrolment, tested in CI. In v1: the public live-migration demo, and a per-release test that no container restarts.
6. **What stops abuse of Free without hurting real users:**
   - One free org per account; a server enrols in one org at a time; sign-in through an established GitHub account or a verified email.
   - Hosted signups are invite-gated for the first few weeks after launch, then open (M§3, week 8).
   - No managed subdomains on Free. This was the abuse surface I worried about most (R§6).
   - Sharing a login to dodge the seat limit defeats approvals and per-person audit, which are the reasons a team wants us. We do not police it beyond MFA.
7. **Why portability helps conversion.** The stage-1 user adopts because leaving is one command, and import/export is a top-ten request at Dokploy (R§10). Teams that stay on hosted do so because it is less work, the only kind of retention worth having at this price.

---

## 7. Tensions handed to `stack.md`, and where they landed

| Tension | Resolution |
|---|---|
| Ciphertext-only control plane vs a usable UI | Envelope keys per environment, owner recovery key, write-only values in the UI, reveal through the CLI. Honest limits on browser encryption stated (S§3.11, S§4) |
| Web terminal vs "a short, typed list" | No host shell ever; container exec in v1, CLI-only, human-only, fresh second factor (S§5.2) |
| Zero-downtime Compose | MVP leaves unchanged services alone and restarts changed ones in place; blue/green for swappable services in v1. The exact shapes are documented (S§5.3) |
| Error-budget rollback defaults | Crash-loop and health-check rollback on by default; error-rate rollback in shadow mode until the user turns it on (S§5.4) |
| Cost per free org | About one to three cents per active org per month (S§5.5) |

---

## 8. Decisions

### Confirmed 2026-11-15

1. Pro at $9 for individuals.
2. Team at $29 plus $3 per server over five; $49 is a later price test.
3. No limits on backups or on-box metrics.
4. Free tier: 1 person, 2 servers. _Amended 2026-11-16: no managed subdomains._
5. _Superseded 2026-11-16 by the split in 5.2._ (Was: Apache-2.0 core including multi-user, with SAML/SCIM, air-gap packaging and compliance exports in a separate commercial module.)
6. "Works financially" means **$10K MRR for a solo founder**, with other revenue avenues expected along the way. Every later decision is weighed against one person building and operating this.

### Confirmed 2026-11-16

1. The product is **Moonglass**, at moonglass.dev.
2. The licence split in 5.2. PR previews stay in the open core; approver rules, agent policy rules, SAML/SCIM and compliance exports go in the commercial module. Sold as hosted Team, a self-hosted Team licence and Enterprise.
3. Free hosted: 1 person, 2 servers, no managed subdomains.
4. Plan on 1–2% free-to-paid, and on four revenue lines (5.6).
5. Auto-rollback: crash-loop and health-check triggers on by default; the error-rate trigger runs in shadow mode until enabled.
6. Lead with correct deploys and footprint; agent guardrails are the demo. Position explicitly against Temps Premium (1.1).
7. Hosted signups invite-gated for a few weeks after launch, then open.

### Open

1. **Self-hosted Team licence price.** I propose the same meter as hosted Team, billed annually. Needed before the Team launch (~week 14), not before week 8.
2. **The user-content domain** for managed subdomains. It must be a separate registrable domain, not a subdomain of moonglass.dev, for cookie and phishing isolation. Needed in week 0 for the Public Suffix List application.
3. **"Agent" naming.** Keep "Moonglass agent" for the server process and always say "coding agent" for Claude Code and the like, or give the server role a different public name. The CLI stays `moonglass agent` either way.
