# Moonglass — Vision

_Builds on `research.md`, cited as R§n. The product is Moonglass (moonglass.dev). Prices and plans are specified in `docs/specs/pricing.md`, billing in `docs/specs/billing.md`, and the public copy in `docs/plan/landing.md`._

**Short version**

- **Moonglass is the deploy platform for your own servers that Coolify and Dokploy users switch to and stay on.** It covers everything 99% of their users rely on: git push-to-deploy, Dockerfile, Railpack and Compose builds, PR previews, domains with automatic TLS, variables and secrets, one-click databases with backups and restore, logs and metrics, many servers, rollbacks, webhooks and notifications, teams and roles, an API, a CLI, MCP, one-click templates and a Coolify import.
- **It is better where it counts:** one static binary, deploys that never drop a request, agents that only dial out, deploys a coding agent proposes and a person approves from an inline diff, upgrades that are one click and revert themselves, and a calm, dense dashboard.
- **Dashboard first.** Everything is doable in the web dashboard: secrets, keys, environments, approvals, restores, settings. The CLI, the API and MCP mirror it. Nothing is CLI-only, and a feature is done only when its dashboard flow ships.
- **Free forever, self-hosted, with nothing held back:** unlimited people, servers, apps, deploys and backups, with roles and approvals. No seat prices, no usage surprises, no "contact sales".
- **Paid where we do the work:** Cloud runs the control plane for you at $4 a month for two servers and $2 for each one after. Business adds SSO, SCIM, audit log export and priority support for $29 a month. Founding members pay less for life.
- **Built in three launchable steps:** the alpha (the deploy path and the dashboard), the launch (the features most people use every day) and v1 (everything else, Cloud and Business). Each step is a release people can run, and each is a step toward v1 (M§3).

---

## 1. Positioning

**Headline:** Every app on your servers, in one calm place.
**Subline:** Moonglass deploys, watches and rolls back everything you run, without dropping a request.

The longer statement, for the README and the about page:

> **Moonglass is an open-source deploy platform for your own servers.** Push to deploy with zero downtime, restore a database with one click, preview every pull request, and let your coding agent propose deploys you approve from a diff. The control plane never SSHes into your servers, never holds a readable secret, and can only ask a server for a short, typed list of things. Agent and proxy run in under 100 MB.

Each claim is something we prove in public:

| Claim | Proof we ship with it |
|---|---|
| "your own servers" | We sell no compute. Zero inbound ports for us on the server |
| "without dropping a request" | A published load test: zero failed requests across ten consecutive deploys, for every shape listed in the docs (S§5.3), including Compose services that change |
| "one-click restores" | A Restore button on the database's screen. Restore runs in CI on every change, including onto a *different* server using only the owner's recovery key |
| "never SSHes into your servers" | The agent dials out, and we hold no SSH keys. Day-to-day work needs no shell: deploys, logs, a terminal into a container, rollback, restore and disk cleanup. Installing does. We do not claim the user's SSH port is closed |
| "under 100 MB" | Agent plus proxy combined, measured over 48 hours (SPIKE-1) before it goes into any public copy, and in CI on every change. A reproducible benchmark repository anyone can rerun |
| "never holds a readable secret" | Values are encrypted in the browser or the CLI. The database stores ciphertext and wrapped keys only. The threat model states plainly what a takeover of the *running* control plane would get (S§4) |
| "a short, typed list of things" | The public `.proto` file. The agent also rejects privileged containers, host namespaces, host bind mounts, added capabilities and Docker-socket mounts unless the server's own `/etc/moonglass/agent.toml` allows them (S§3.2) |
| "your coding agent proposes, you approve" | A reproducible script: a coding agent with a token scoped to staging deploys there on its own, and its production deploy waits in the Inbox with its diff until a person approves it |

### 1.1 Against the alternatives

The comparison page says this calmly and credits what each does well:

| | Coolify | Dokploy | Moonglass |
|---|---|---|---|
| How the panel reaches servers | SSH from the panel | SSH from the panel | The server's agent dials out; the panel holds no SSH keys |
| A deploy that changes a Compose service | Recreates every container | Recreates the changed services | Unchanged services are never touched; changed stateless services switch blue/green |
| Approvals | None | None | A coding agent's or a teammate's deploy waits for a person, with an inline diff, free |
| Secrets in the database | Readable by the panel | Readable by the panel | Ciphertext only |
| Footprint | 300 MB to 1.2 GB idle (R§3) | Similar (R§3) | Agent and proxy under 100 MB; fits a 2 GB VPS with room for apps |
| Upgrades | A script, then hope | A script, then hope | One click, signed, and a failed upgrade reverts itself |
| Free self-hosted | Everything, unlimited | Core free; SSO and audit logs paid | Everything, unlimited, including teams, roles, approvals and the audit trail |
| Hosted control plane | $5/mo for 2 servers, +$3 each | $4.50 per server, or $15 for 3 | $4 for 2 servers, +$2 each, unlimited people |

Temps sells approval gates in a $399 licence (R§3), and many teams gate production with GitHub Environments. Both are credited on the page: Moonglass's gate is enforced on your own control plane, works for deploys a coding agent proposes over MCP, and shows the diff where you approve.

### 1.2 Voice

Moonglass should feel calm, elegant, premium and soft. In practice:

- **Plain claims with numbers.** "Zero failed requests across ten deploys", not "blazing-fast".
- **No war words in public copy.** Words like moat, blast radius, kill switch and attack surface are fine in these planning docs and stay out of the site, the docs and the videos.
- **Softer phrasing for the same fact.** "Never SSHes into your servers", not "no open SSH" (which is also inaccurate). Use "light" in prose and give the exact number.
- **No dunking on competitors.** The comparison page credits what Coolify, Dokploy, Temps and GitHub do well.
- **"PaaS" goes in the subline and SEO only**, never the headline.
- **Two kinds of agent.** In the dashboard, **Agents** are coding agents and the tokens they use. The Moonglass process on each server is the **Moonglass agent** and appears under Servers. Public copy always says "coding agent" or "Moonglass agent", never a bare "agent" in prose. The CLI keeps `moonglass agent` for the server process.

---

## 2. Who it is for

**Builders on their own servers.** One to twenty VPSs (Hetzner, DigitalOcean, OVH), shipping with Claude Code, Cursor or a team, today on Coolify, Dokploy, Kamal or raw Compose. They want push-to-deploy that never drops a request, previews, databases they can restore without a shell, and a panel that cannot own their servers.

**Agencies and small teams.** Several people sharing servers, often for several clients: one control plane with an organisation per client, roles, approvals, and an audit trail. They get all of that free when they self-host, and pay for Cloud when they would rather not run the control plane, or for Business when their clients ask for SSO and audit exports.

**Teams letting coding agents ship.** A coding agent gets a token scoped to an environment. It deploys to staging on its own; its production deploys wait for a person, who approves from the diff.

**Not the target:** teams that want someone else to run the servers (Railway, Render), Kubernetes shops (Northflank), homelab users managing media stacks (Komodo, Portainer).

---

## 3. What we win on

In priority order. If two conflict, the higher one wins.

1. **Correct deploys.** Zero downtime for every shape in S§5.3, instant rollback with no Swarm or external registry, automatic rollback on crash loops, failed health checks and a raised error rate. Compose done properly: unchanged services are never touched, stateless services switch blue/green, stateful services stop and then start, and the dashboard says which before you confirm.
2. **A complete dashboard.** Every capability has its dashboard flow, keyboard-complete, calm and dense (`docs/specs/ui.md`). The CLI, API and MCP are generated from the same operations.
3. **Safety on the server.** Outbound-only agent, no SSH keys held by us, typed commands, container-spec sanitization, secrets the control plane cannot read, a tamper-evident audit trail, approvals enforced on the server rather than by a flag the caller sets. Answers 117 advisories at the two leaders and the Railway breach (R§0, R§3).
4. **Day 2 without SSH.** One-click restore, readable failure reasons, pre-deploy migration steps that cannot half-apply, a container terminal in the dashboard, a full disk that degrades gracefully, and upgrades that revert themselves.
5. **Footprint.** A single static binary; agent and proxy under 100 MB together. Incumbents idle at 300 MB to 1.2 GB, and Temps needs 4 GB (R§3).
6. **First hour.** One install on one VPS, sign in with GitHub, connect a repository, and a live deploy in minutes. A Coolify import moves an existing setup in one pass.

---

## 4. What we deliberately won't do

- **No Kubernetes, no Swarm, no scheduler.** Many independent servers under one control plane. If you need bin-packing, you have outgrown us.
- **No compute, no managed databases on our machines.** Your workloads and their data stay on your servers.
- **No feature gates on self-hosted.** Teams, roles, approvals, previews, templates, MCP, the audit trail and every safety feature are free and unlimited when you run the control plane. Business adds only enterprise identity (SAML, SCIM, SSO enforcement), audit export and priority support (5.1).
- **No per-seat pricing, no usage meters beyond servers you add yourself, no "contact sales".** Every price is on the pricing page.
- **No template arms race.** Fifty templates we test on every release, not 300 we cannot maintain.
- **No bundled analytics, error tracking or session replay.** That is Temps' identity (R§3).
- **No plan checks in the agent, no kill switch.** A lapsed subscription or licence never stops a running app, a deploy, a rollback, a backup, a restore or an export (§6).
- **No home-made licence, no relicensing.** The core is Apache-2.0. The Business module and the Cloud service live in their own repositories under their own licences (R§3, Dokploy). The split in 5.1 is published before launch, and nothing ever moves from the core into a paid plan.
- **No canvas view.** Nothing in the research shows demand for it.
- **No host shell.** A terminal reaches into a container, never the host.

---

## 5. The money

### 5.1 What is free and what is paid

| Open core, Apache-2.0: free forever | Business module, its own repository and licence |
|---|---|
| Every feature in this document: deploys, builds, previews, Compose, templates, databases, backups and restore, logs and metrics, many servers, notifications, the Coolify import | SAML single sign-on and SSO enforcement for an organisation |
| Teams: unlimited members, with the roles owner, admin, member and viewer | SCIM provisioning |
| Approvals: protected environments, inline diff approvals, approver rules, two-person approval | Audit log export to S3, a webhook or a SIEM, on a schedule or streamed |
| Coding agents: agent identities, scoped tokens, MCP | Priority support: a reply within one business day |
| Sign-in with GitHub, Google, any OpenID Connect provider, passkeys and TOTP | |
| The audit trail, its view and its verification; soft delete; container sanitization; the local policy file; encrypted secrets; step-up | |

Cloud is not a feature set. It is the same open core with the control plane run by us (5.2).

### 5.2 The plans

Three plans. Full rules in `docs/specs/pricing.md`.

| | **Free** | **Cloud** | **Business** |
|---|---|---|---|
| Price | $0, forever | $4/month with 2 servers, then $2/month per server | $29/month, then $2/month per server over 10 on Cloud |
| Who runs the control plane | You, on your own box | We do | Either: Business on Cloud, or a licence for your own box |
| People, projects, apps, deploys, backups | Unlimited | Unlimited | Unlimited |
| Every open-core feature | Yes | Yes | Yes |
| SSO, SCIM, audit export | | | Yes |
| Support | Community | Email | Priority |

- **Founding members.** The first 1,000 Cloud accounts and the first 100 Business accounts keep a founding price for as long as they stay subscribed: Cloud $3/month with 2 servers and $1.50 per extra server, Business $19/month.
- **Yearly billing** costs ten months' price for twelve.
- **Why these prices.** Coolify Cloud is $5 for two servers and $3 per server after; Dokploy Cloud is $4.50 per server for one person, or $15 for three servers with unlimited people (checked 2026-11-23). Cloud is cheaper than both at every server count, with unlimited people. Dokploy's SSO and audit logs are "contact sales"; ours are $29.

### 5.3 Does it add up?

All figures are estimates.

- **Cost.** A Cloud control plane is one `moonglass server` process with its SQLite file, tens of megabytes of memory (S§5.5). Hundreds fit on one small machine. Payment fees and support time, not compute, are the real costs.
- **Revenue goal: $10K MRR within 12 months of Cloud opening.** For example, 1,100 Cloud accounts at an average of $6 (three servers) and 100 Business accounts at an average of $35. Coolify reached about $23K MRR from about 4,000 customers at about $6 each (R§1), so the Cloud half is a share of the same market, priced below it.
- **Why free self-hosted helps.** Every self-hosted install is a future Cloud or Business account. People who stop wanting to run the box, or whose clients ask for SSO, move without changing tools (§6).

### 5.4 Gates: how we know early whether it is working

| Checkpoint | Healthy | Rethink |
|---|---|---|
| Waitlist before the alpha opens (M§2.2) | ≥ 1,000 signups | < 300: rework the headline before the launch |
| Alpha installs that reach a second deploy within a week | ≥ 50% | < 25%: fix the first hour before adding features |
| Coolify and Dokploy users in the alpha who would switch (M§6) | ≥ 6 of 10 asked | < 3: find what is missing before the launch |
| Cloud accounts 90 days after Cloud opens | ≥ 300 | < 100: check the price and the onboarding |
| Cloud accounts still paying after 3 months | ≥ 85% | < 70% |

Do not raise a price or add a gate to pass a checkpoint. A plan that only works with a seat price or a paywall on the core is evidence against the plan.

### 5.5 Levers, in order

1. **Switching from Coolify and Dokploy.** The import, the comparison page and the benchmark (M§5).
2. **Coding agents.** The approval demo: a coding agent ships to staging alone, and production waits for your click.
3. **Agencies.** One control plane, one organisation per client, Business when a client asks for SSO.
4. **The free self-hosted build** as the top of the funnel for Cloud.

---

## 6. Enforcing plans while keeping projects portable

**Principle: what is paid is a service we run or code in the Business module, never a check in the open build or the agent.**

1. **The agent has no concept of a plan.** It obeys any correctly signed, policy-allowed command from the control plane it is enrolled with.
2. **The open-source build contains no licence or plan check.** The Business module adds operations and sign-in providers through the licensed build's extension points (V1-1); it never unlocks code already in the open build.
3. **After a lapse,** everything keeps running and stays deployable, rollback-able and restorable. Business settings freeze and keep working for existing sign-ins; Cloud follows `docs/specs/billing.md`. Export always works.
4. **A project is state on the server plus a coordinator.** Running state (containers, volumes, certificates, deploy history) lives with the agent. The control plane is a replaceable coordinator.
5. **Moving a control plane** between Cloud and your own box is `moonglass export` and an import on the other side, with agents re-enrolled without a restart (W7-2). Secrets never pass through in plaintext: agents keep their keys, and the owner's recovery key works on either side (S§3.11).

---

## 7. How it gets built

Three milestones, each a release people can run (M§3, `docs/plan/build-plan.md`):

1. **Alpha:** the deploy path and the dashboard. One install on a VPS; sign in with GitHub; connect a repository; push to deploy with zero downtime; variables and secrets set in the dashboard; domains with automatic TLS; live logs and usage; rollback; more than one server; upgrades from the dashboard. Opens to the waitlist.
2. **Launch:** the features most people use every day. Auto-rollback, notifications, teams and roles, coding agents with approvals and MCP, deletion and the activity log, databases with backups and the Restore button, Railpack and Compose, PR previews, metrics, templates and the Coolify import. Announced publicly, with the benchmark and the comparison page.
3. **v1:** the rest. Cloud and Business, passkeys and OIDC, approver rules and agent-verified approvals, a container terminal, volume backups, private networking between servers, scheduled jobs, more git hosts and templates.

---

## 8. Open questions

1. **The user-content domain** for Cloud's managed subdomains (V1-4): a domain on the Public Suffix List, registered before V1-4 starts.
