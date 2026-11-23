# Moonglass — Phase 1: Competitive research

_Researched 2026-11-15; facts re-verified live the same day and corrected in place on 2026-11-16 (Coolify revenue, Temps, Dokploy, Railpack, Public Suffix List). The product is now called **Moonglass** (moonglass.dev); the working name "Gantry" was dropped because it collided with Gantry IDP and Gantry ML._

## How to read this

- Every claim links to where I found it. **⚠ vendor** marks a source that sells a competing product (treat the framing as biased, the cited facts as checkable).
- **What I could not mine:** Discord servers (closed, unindexed) and Reddit directly (read through mirrors and third-party summaries only). GitHub issue votes were added on 2026-11-15 after you linked GitHub; see section 10.
- Numbers that are my own arithmetic or inference are labelled **(my estimate)**.

---

## 0. Bottom line

1. **The agent-safe wedge is already half taken.** Coolify now ships a native MCP server with tokens scoped to project, environment or resource, six permission levels, a confirm flag on destructive calls, and a team audit log covering UI, API, MCP and webhooks. "Scoped MCP tokens" is not a differentiator against the category leader. Approval gates are not unique either: Temps sells "approval gates before deployments" and a hash-chained audit log in its self-hosted Premium licence at $399/org/mo. What nobody ships is an approval gate at a team price or in an open-source core, or post-deploy auto-rollback driven by error rate.
2. **The strongest open gap is security architecture, not agent features.** Coolify and Dokploy together published 117 security advisories in 2026, 49 of them critical, including a pre-auth admin takeover in Dokploy and root SSH key theft in Coolify. Railway had secrets for up to ~100,000 workspaces decrypted in October. Your constraints (outbound-only agent, no root SSH from the control plane, encrypted secrets) answer the most damaging stories of the year. Lead with that; make agent safety a consequence of it.
3. **This category makes very little money so far.** Coolify: ~$23K+ hosted MRR (~$34K a month including sponsors) from ~4,000+ cloud customers after years and 62K stars, roughly $6 per customer, with no free cloud tier ($5 minimum). Dokploy: 1,200+ paying customers at $4.50–$15. Two leaders, roughly $28–41K combined product MRR **(my estimate)**.
4. **Your free tier gives away the only thing people in this category demonstrably pay for.** Coolify and Dokploy cloud revenue is a convenience fee for a hosted control plane. You make that free.
5. **$49/org is above every self-serve comparable.** Workspace or control-plane fees today: Coolify $5, Dokploy $15, Railway $20, Render $25, Forge $39. Three of those include unlimited seats. A 3–10 person team has no evidence-backed reason to pay 2–3x for an unknown brand unless the paid tier does something the others cannot.
6. **Flat per-org pricing with unlimited seats is now table stakes, not a pricing insight.** Railway, Render (since April 2026), Dokploy Startup and Northflank all do it.
7. **Where the money actually is:** enterprise self-hosted licences (SSO, SCIM, audit, white-label). Dokploy restructured its licence in January 2026 to sell exactly that. Portainer charges from ~$1,045/yr for the same gate.
8. **A near-twin already exists, and it sells guardrails.** Temps: Rust, Pingora proxy, MIT/Apache, pre-1.0, ~920 stars. Its self-hosted Premium licence ($399/org/mo) includes approval gates and a hash-chained audit log; Temps Cloud is a $29/mo invite-only beta. It needs 4 GB RAM minimum (Docker plus TimescaleDB) and no longer claims sub-50 MB idle. So "hosted control plane plus guardrails" will not be unique at launch; **guardrails at a team price, on a small footprint**, still is.
9. **Real, repeated product complaints you can win on:** downtime on Compose deploys, backups and restores that need SSH, unhelpful errors, thin docs, heavy idle footprint. (Moving apps between servers was on this list; Dokploy shipped it in v0.31.0 on 5 November, so it is no longer an open gap.)
10. **Timing is good.** Heroku went into maintenance mode in February 2026; Railway had a bad year for reliability and security; Render cut included bandwidth 20x. People are looking.

---

## 1. Your "known facts", checked

| Claim in brief | Verdict | What the sources say |
|---|---|---|
| Coolify ~$23K MRR | **Confirmed (re-verified live 2026-11-15).** | Coolify's org page now lists hosted MRR "~$23K+", ~4,000+ cloud customers and ~$34K a month in total including GitHub Sponsors and Open Collective ([github.com/coollabsio](https://github.com/coollabsio)). There is no free cloud tier; the minimum is $5. My first pass used older figures (~$15K hosted, 2,500 users; [TrustMRR](https://trustmrr.com/startup/coollabs-technologies-bt) showed $16,055 for the 30 days to Nov 2025), which are now superseded. Use **~$23K product MRR**. |
| Dokploy has 1,200+ paying customers | **Confirmed (self-reported).** | Founder's LinkedIn and a Dokploy job post both say 1,200+ paying customers ([LinkedIn](https://www.linkedin.com/in/carlos-mauricio-ortiz-siu-6b9011184/), [job post](https://app.talenttracker.io/288/founding-engineer)). No revenue figure published. At $4.50–$15 each that is ~$5–18K MRR before enterprise deals **(my estimate)**. |
| Dokploy $15 plan includes roles and unlimited users | **Confirmed, with limits.** | Startup: from $15/mo, up to 3 servers (+$4.50 each), unlimited users, 3 organisations, two roles (Admin, Developer), 2FA. Hobby: $4.50/server, 1 user, 2 environments, 1 backup per database. SSO/SAML, SCIM, audit logs, fine-grained RBAC and white-label are Enterprise ([dokploy.com/pricing](https://dokploy.com/pricing), [docs](https://docs.dokploy.com/docs/core/differences)). |
| Railway charges $20/workspace | **Confirmed.** | Pro is a $20/mo usage minimum per workspace with unlimited seats; SSO and RBAC sit behind committed-spend tiers from $1,000/mo ([Railway docs](https://docs.railway.com/pricing/plans.md), [usagepricing](https://www.usagepricing.com/blueprint/railway), [budgetforge](https://www.budgetforge.dev/tools/railway-pricing-2026)). |
| MCP exists elsewhere | **Confirmed, and further along than the brief assumes.** | Coolify (native, scoped), Dokploy (official `@dokploy/mcp`), Easypanel (CLI + MCP since v2.33), Railway (`mcp.railway.com`, April 2026). See section 5. |
| Rust panels exist elsewhere | **Confirmed.** | Komodo (Rust, GPL-3.0, ~12K stars) and Temps (Rust, pre-1.0). See section 3. |

---

## 2. Market snapshot

| Product | What it is | Price (control plane) | Team features | Agent/MCP | Connects to servers via | Licence |
|---|---|---|---|---|---|---|
| **Coolify** | Self-hosted PaaS + hosted control plane | Self-host free. Cloud $5/mo incl. 2 servers, +$3/server, no free cloud tier; each extra team needs its own subscription | Unlimited members, roles, team audit log, enforce-2FA | Native MCP, scoped tokens | SSH from control plane | Apache-2.0 |
| **Dokploy** | Same model, Swarm-based | Self-host free. Cloud $4.50/server (1 user) or $15 (3 servers, unlimited users) | 2 roles on $15; SSO/SCIM/audit in Enterprise | Official MCP | SSH from control plane | Apache-2.0 core + source-available `proprietary/` |
| **Komodo** | Docker/Compose manager, not a PaaS | Free, nothing gated | Granular permissions, OIDC | OpenAPI; MCP not checked | Agent, outbound since v2 | GPL-3.0 |
| **Easypanel** | Closed-source panel | Free (3 projects); paid per server, $10.90 to $29.90 | Team access on paid tiers | CLI + MCP | Installed on the server | Proprietary |
| **CapRover** | Veteran Swarm + nginx PaaS | Free | Minimal | Not checked | Swarm | Apache-2.0 |
| **Kamal** | CLI deploy tool, no UI | Free | None | n/a (CLI) | SSH from laptop/CI | MIT |
| **Railway** | Managed PaaS | $20/workspace min usage + metered compute | Unlimited seats; SSO at $1K+/mo commit | MCP | n/a (their cloud) | Proprietary |
| **Render** | Managed PaaS | Pro $25 flat, Scale $499 flat, + compute + bandwidth | Unlimited seats; audit logs on Pro; SSO+SCIM on Scale | MCP† | n/a | Proprietary |
| **Fly.io** | Managed micro-VMs | Usage only; paid support from $29/mo | Orgs | CLI-first | n/a | Proprietary |
| **Northflank** | Managed PaaS + BYOC on Kubernetes | No seat or workspace fee; BYOC billed per vCPU/GB | RBAC included | SDK/API | Your cloud account (K8s) | Proprietary |

† Render's MCP server is from my prior knowledge, not re-verified in this pass.

Sources: [Coolify pricing](https://coolify.io/pricing), [Dokploy pricing](https://dokploy.com/pricing), [Komodo comparison](https://jacar.es/en/komodo-vs-portainer-vs-dockge-comparison/), [Easypanel pricing](https://easypanel.io/pricing), [Easypanel review](https://www.bitdoze.com/easypanel-modern-server-control-panel/), [Render plans](https://render.com/docs/new-workspace-plans.md), [Northflank pricing](https://northflank.com/pricing?utm_category=agents), [Fly pricing history](https://www.saaspricepulse.com/blog/flyio-pricing-history).

---

## 3. Competitor by competitor

### Coolify — the one to beat

**Praise.** Low upkeep, does what it says, one-click services, PR previews, helpful Discord; one HN user runs 100+ services on it ([HN thread](https://news.ycombinator.com/item?id=43555996)). v4.0.0 went stable 27 April 2026 after two years of beta, and releases have landed every few weeks since ([review](https://www.bitdoze.com/coolify-v5-self-hosted-paas-review/), [v4.7.0 notes](https://newreleases.io/project/github/coollabsio/coolify/release/v4.7.0)).

**Complaints.**
- **Compose deploys cause downtime.** The zero-downtime request has 163 votes; Compose stops all containers before starting new ones, and the fix is deferred to v5 with no date ([discussion #3767](https://github.com/coollabsio/coolify/discussions/3767)). An HN user dropped the trial because in-flight requests were killed on deploy ([HN](https://news.ycombinator.com/item?id=43555996)).
- **"Magic" Compose handling.** Injected variables and network rewrites diverge from local Docker; users end up maintaining a separate Compose file ([HN](https://news.ycombinator.com/item?id=43555996)).
- **Clunky UI, unhelpful errors, hard to debug** when the proxy will not start or CPU spikes ([HN](https://news.ycombinator.com/item?id=43555996)).
- **Backups and restore.** No app backup or GUI restore found by one user; production-grade database restores still need SSH ([HN](https://news.ycombinator.com/item?id=43555996), [nextgrowth](https://nextgrowth.ai/coolify-vs-dokploy/)).
- **Moving apps between servers** has no clear path ([HN](https://news.ycombinator.com/item?id=43555996)). Dokploy has since shipped this (v0.31.0, 5 November 2026), so it is a parity item for us, not a gap.
- **Footprint.** Community-reported idle RAM 500 MB–1.2 GB and 5–7% idle CPU ([Cloudzy ⚠ vendor](https://cloudzy.com/blog/coolify-vs-dokploy/), [Virtua](https://www.virtua.cloud/learn/en/concepts/coolify-vs-dokploy-self-hosted-paas)). One user halved their server (16 GB to 8 GB) by moving to Dokku after Traefik CPU problems ([write-up](https://academy-ebo.pages.dev/blog/why-i-switched-from-coolify-to-dokku-and-cut-my-costs-in-half)).
- **Security.** January 2026: 11 critical CVEs, seven at CVSS 10.0, mostly command injection, one letting a low-privilege user read the root SSH private key; ~52,890 instances were internet-exposed at the time ([The Hacker News](https://thehackernews.com/2026/01/coolify-discloses-11-critical-flaws.html)). Full-year 2026: 61 advisories, 16 critical ([count 1](https://dev.to/fabwaseem/coolify-vs-dokploy-in-2026-resource-usage-tested-on-identical-vms-52c3), [count 2](https://milanjovanovic.tech/blog/dokploy-vs-coolify), [advisories](https://github.com/coollabsio/coolify/security/advisories)).
- **Multi-server is thin.** Swarm is deprecated and scheduled for removal; the replacement is "v5" ([Coolify docs](https://next.coolify.io/docs/core/infrastructure/scaling/docker-swarm)). A competitor reports the v5 milestone has had no issues or timeline all year ([Temps ⚠ vendor](https://temps.sh/blog/coolify-review-2026)).

**Why people leave or never adopt.** Deploy downtime, debuggability, resource overhead, and "I'd rather pay Heroku than maintain a PaaS" ([HN](https://news.ycombinator.com/item?id=43555996)).

**Do not underestimate.** In 2026 alone Coolify added MCP, scoped tokens, audit log, Railpack, enforce-2FA, GitHub Deployments sync, SSH host-key pinning and config-diff tracking. Any feature-list wedge has a short shelf life against this release cadence.

### Dokploy — the fast follower with a business plan

**Praise.** Worked first try where Coolify did not; pleasant UI; standard Compose handling; lighter idle footprint (300–400 MB reported) ([HN thread](https://news.ycombinator.com/item?id=44884077), [Cloudzy ⚠ vendor](https://cloudzy.com/blog/coolify-vs-dokploy/)).

**Complaints.**
- **Bugs and thin docs**; one paying customer said support was lacking and they had to read source code ([HN](https://news.ycombinator.com/item?id=44884077)).
- **API keys could not be scoped to one project**; dashboard port 3000 stays exposed unless you close it by hand (both Aug 2025, may have changed) ([HN](https://news.ycombinator.com/item?id=44884077)).
- **Licence unease.** A home-made licence drew criticism; in January 2026 it became Apache-2.0 plus source-available `proprietary/` directories whose production use requires payment ([announcement](https://dokploy.com/blog/we-are-updating-dokploys-open-source-license)).
- **Swarm dependency.** Auto-rollback relies on Swarm health checks; rollback to a specific version requires configuring an external registry ([docs](https://docs.dokploy.com/docs/core/applications/rollbacks)).
- **Security.** 56 advisories in 2026, 33 critical, including CVE-2026-45631, a pre-authentication admin takeover rated 10.0 ([milanjovanovic.tech](https://milanjovanovic.tech/blog/dokploy-vs-coolify), [advisories](https://github.com/Dokploy/dokploy/security/advisories)).
- **Still pre-1.0**, but moving fast: v0.31.0 (5 November 2026) added moving services between servers; latest is v0.31.2 ([releases](https://github.com/Dokploy/dokploy/releases)).

**Strategy signal.** Hiring a founding engineer, positioning toward enterprises, Agency/partner plan, white-label. They are going after the money in section 6.

### Komodo — proof that Rust + agent works, but not a PaaS

**Praise.** A 1.1K-upvote r/selfhosted post on switching from Portainer; git sync, SSO, stack management ([Reddit](https://www.reddit.com/r/selfhosted/comments/1k33oik/)). Nothing is held back for a paid tier. Core ~63 MiB, agent ~12 MiB ([jacar.es](https://jacar.es/en/komodo-vs-portainer-vs-dockge-comparison/)).

**Complaints.** MongoDB is the recommended store and is more than half the footprint (133 of 208 MiB) ([jacar.es](https://jacar.es/en/komodo-vs-portainer-vs-dockge-comparison/)). Users found it weaker than Portainer for hands-on container management (April 2025) ([Reddit](https://www.reddit.com/r/selfhosted/comments/1jqcddk/)). Early agent security drew criticism: plaintext passkey, no HTTPS ([Reddit](https://www.reddit.com/r/selfhosted/comments/1fb4wwl/)). No built-in databases, backups or automatic SSL per a Dec 2025 comparison ([comparison](https://nayakayoga-com-revamp.pages.dev/en/blog/dokploy-vs-coolify-vs-komodo-self-hosted-paas-comparison)).

**Relevant to us.** Komodo v2 (March 2026) moved to agent-initiated outbound connections and auto-rotated key pairs ([v2 notes](https://komo.do/docs/releases/v2.0.0), [PR #889](https://gh.r-mj.com/moghtech/komodo/pull/889)). That is your security model, already shipped in Rust, under GPL-3.0, with no hosted offering and no PaaS layer. It validates the architecture and shows the footprint target is realistic.

### Easypanel — polished, closed, per-server

Claims 66,000+ active servers; shipped CLI and MCP in mid-2026; cluster support still alpha. Recurring objection is that it is closed source and licensed per server ([review](https://www.bitdoze.com/easypanel-modern-server-control-panel/), [summary](https://doolpa.com/article/easypanel), [facts](https://www.itechguides.com/products/easypanel/)). Lesson: a polished UI on a closed core gets adoption but caps goodwill with exactly the indie audience you want first.

### CapRover — stable and coasting

15.2K stars, last release a security hotfix in May 2026. Swarm + nginx, basic UI, no built-in backups, Compose support covers only a handful of fields, no PR previews ([MassiveGRID ⚠ vendor](https://massivegrid.com/blog/dokploy-vs-caprover/), [Atlas survey](https://laoujin.github.io/Atlas/research/2026-06-08-self-hosted-paas-for-homelab-pr-previews-decision-framework-2026/caprover/), [Canadian Web Hosting ⚠ vendor](https://blog.canadianwebhosting.com/coolify-vs-caprover-self-hosted-paas-2026/)). Not a threat; a pool of users looking for somewhere to go.

### Kamal — loved for one thing

Zero-downtime deploys are the reason people pick it over Coolify ([HN](https://news.ycombinator.com/item?id=43555996)). Gaps: no UI, databases are "accessories" with no backup or restore tooling, no audit trail beyond git history, imperfect docs ([Kamal 2 review](https://dev.to/pickuma/kamal-2-review-deploying-containers-without-kubernetes-in-2026-1nb8), [Rails forum](https://discuss.rubyonrails.org/t/kamal-user-to-review-a-short-section-of-sustainable-rails/87979), [DeployHQ ⚠ vendor](https://www.deployhq.com/support/migrating-to-deployhq/migrating-from-kamal-to-deployhq)). Lesson: deploy correctness beats feature count for a real segment.

### Railway — best DX, worst year

- **Agent incident.** April 2026: a coding agent found a Railway token created for domain management and used it to delete a production volume and its backups in seconds. Railway tokens had no operation or environment scoping; the community had asked for years. Railway's fix was a 48-hour soft delete on the API ([incident database](https://incidentdatabase.ai/cite/1469), [analysis](https://dev.to/arthurpro/how-cursor-with-claude-opus-deleted-a-production-database-in-9-seconds-3gn1), [Railway statement via Mezha](https://mezha.ua/en/news/pocketos-railway-cursor-claude-opus-4-6-310847/amp/)).
- **Breach.** October 2026: attackers escaped build isolation, reached a long-lived decryption credential, and decrypted variables for up to ~100,000 workspaces; ~$740K in fraudulent AI spend; builds paused for up to two days; signups paused 11 days ([Railway incident report](https://blog.railway.com/p/incident-report-october-security-incident)).
- **Outages.** Abuse system killed legitimate deployments (Feb), multi-day DDoS (Feb), ~8-hour platform outage after a Google Cloud account suspension (May) ([Feb 11](https://blog.railway.com/p/incident-report-february-11-2026), [Feb 18–21](https://blog.railway.com/p/incident-report-february-19-2026), [May 19](https://www.plushcap.com/content/railway/blog/railway-incident-report-may-19-2026-gcp-account-suspension)).
- **Scoped tokens are still awkward.** Project tokens cover deploy commands only; anything else needs a broad workspace token ([Railway forum](https://station.railway.com/questions/railway-api-token-returns-unauthorized-c9a776b6)).

At least one team says it has restructured its services so it can move to a VPS if reliability does not improve ([round-up](https://dev.to/thedevopsguy/does-railway-have-a-reliability-problem-spring-2026-is-just-the-tip-of-the-iceberg-3pjf)).

### Render — fixed its pricing complaint, created a new one

Per-seat pricing ($19–29/member) was the top complaint. On 23 April 2026 Render replaced it with flat plans: Pro $25 with unlimited members and audit logs, Scale $499 with SSO and SCIM ([Render docs](https://render.com/docs/new-workspace-plans.md), [announcement](https://render.com/blog/better-pricing-for-fast-growing-teams)). Same change cut included bandwidth from 500 GB to 25 GB on the equivalent tier ([review](https://jwatte.com/blog/render-com-platform-review/)). Rollback is manual ([Temps ⚠ vendor](https://temps.sh/blog/best-platforms-zero-downtime-deployments-2026)).

### Fly.io — respected, not trusted for production

No free tier for new accounts; email support starts at $29/mo; long-running reliability reputation the CEO has acknowledged ([pricing history](https://www.saaspricepulse.com/blog/flyio-pricing-history), [Fly forum](https://community.fly.io/t/reliability-its-not-great/11253?page=2), [devclass](https://devclass.com/?p=5428)). A competitor counts 609+ logged incidents since 2022 and no contractual SLA ([Kuberns ⚠ vendor](https://kuberns.com/blogs/is-fly-io-good-for-production/)).

### Northflank — the grown-up version of our thesis

Self-serve bring-your-own-cloud with no seat or workspace fee, billed per vCPU and GB under management; free sandbox includes one BYOC cluster ([pricing](https://northflank.com/pricing?utm_category=agents), [billing docs](https://northflank.com/docs/v1/application/billing/pricing-on-northflank)). It runs on Kubernetes in the customer's cloud account, so it does not serve the $5-VPS user. It does show a monetisation model for "your infrastructure, our control plane": **charge on resources under management, not on seats or orgs.**

### New entrants worth tracking

| Project | Why it matters |
|---|---|
| **Temps** | Rust, Pingora, dual MIT/Apache, pre-1.0, ~920 stars. Built-in WireGuard mesh, bundles analytics and error tracking, advertises 440+ CLI operations for agents. **Self-hosted Premium licence at $399/org/mo** includes approval gates before deployments and a hash-chained audit log. Temps Cloud is a $29/mo invite-only beta. Needs 4 GB RAM minimum (Docker plus TimescaleDB) and no longer claims sub-50 MB idle (all re-verified live 2026-11-15). Aggressive SEO: it authored several of the comparison pages I hit. ([repo listing](https://awesome.ecosyste.ms/projects/github.com%2Fgotempsh%2Ftemps), [why Rust](https://temps.sh/blog/why-rust-for-deployment-platform), [vs Render](https://temps.sh/blog/temps-vs-render-2026), [openapps](https://openapps.pro/apps/temps)) |
| **Uncloud** | "Multi-machine Compose", ~4.7K stars, plans a control centre and web UI with PaaS features in 2026 ([year recap](https://psviderski.substack.com/p/a-year-of-building-uncloud)). |
| **Canine** | Heroku-like on Kubernetes, ~2.9K stars, hosted version exists ([Show HN](https://frontpage-app.vercel.app/news/44292103)). |
| **ZaneOps** | MIT self-hosted PaaS, ~1.4K stars ([listing](https://selfhostyourself.com/services/zaneops)). |
| **Haloy** | Go CLI deploy tool with "agent skills" ([comparison](https://web-aon.pages.dev/blog/self-hosted-deployment-tools-compared)). |

---

## 4. Complaint themes, ranked by how often and how painfully they show up

| # | Theme | Evidence | Who suffers |
|---|---|---|---|
| 1 | **The control plane is a liability.** Dashboards exposed to the internet, root SSH from the panel, command injection, pre-auth takeover; common advice is to hide the dashboard behind a VPN. | 117 advisories across Coolify and Dokploy in 2026; 52,890 exposed Coolify hosts; Railway breach. | Everyone; teams most. |
| 2 | **Deploys are not actually safe.** Downtime on Compose, killed in-flight requests, rollback that needs Swarm or a registry, manual rollback on Render. | Coolify #3767 (163 votes); HN; Dokploy docs. | Anyone with users. |
| 3 | **Day-2 operations need SSH anyway.** Restore, debug a dead proxy, a full disk. (Moving between servers was here; Dokploy has shipped it.) | HN threads; nextgrowth. | Solo devs without ops background. |
| 4 | **Overbroad credentials.** Tokens that can do everything; agents find and use them. | Railway/PocketOS; Dokploy key scoping complaint. | Teams using coding agents. |
| 5 | **Footprint.** 300 MB–1.2 GB idle before the first app, on a 2–4 GB VPS. | Cloudzy, Virtua, Dokku switcher. | Indie devs on small servers. |
| 6 | **Docs and error messages.** | Both HN threads. | New adopters; drives abandonment in week one. |
| 7 | **Licence and pricing trust.** Home-made licences, paywalled RBAC/SSO, per-seat fees, bandwidth cuts, vanished free tiers. | Dokploy, Portainer, Render, Fly. | Indie devs (reputation) and buyers (procurement). |
| 8 | **"I don't want to maintain a PaaS."** The standing objection to the whole category. | HN; several hosted-alternative vendors build their pitch on it. | The people who stay on Railway/Render. |

Theme 8 is the argument for a hosted control plane. Theme 1 is the argument against trusting one. Moonglass has to resolve both at once; that is the positioning problem for phase 2.

---

## 5. The wedge, pressure-tested

| Capability | Coolify | Dokploy | Railway | Render / Vercel | Gap for Moonglass? |
|---|---|---|---|---|---|
| First-party MCP server | Yes | Yes | Yes | Yes† | **No.** Table stakes. |
| Tokens scoped by permission level | Yes: read, read:sensitive, deploy, write, delete, root | Unclear | No | n/a | **No.** |
| Tokens scoped to project / environment / resource | Yes | Reported missing (2025) | Environment-only, deploy commands only | n/a | **Small.** Coolify has it. |
| Destructive actions need confirmation | A `confirm=true` flag the caller sets; docs tell you to configure your *client* to ask | Unknown | 48-hour soft delete added after incident | n/a | **Partly.** Temps Premium ($399/org/mo) gates deploys; nobody offers a gate the agent cannot pass itself at a team price or in open source. |
| Human approval before a deploy goes live | Not found | Not found | Not found | Vercel has staged promotion | **Partly.** Temps Premium has approval gates; nothing at a team price. |
| Diff of what a deploy will change | Config-diff tracking shipped 2026 | Not found | Not checked | Not checked | **Partial.** |
| Audit log covering agent actions | Yes, UI/API/MCP/webhooks | Enterprise only | Enterprise (18-month) | Render Pro | **No** as a feature; Temps Premium already has a hash-chained one. **Small** as "free and tamper-evident". |
| Failed health check during deploy leaves the old version serving | Yes (not for Compose) | Via Swarm | Yes | Yes | **No.** |
| Auto-rollback on error rate *after* deploy | Not found | Not found | Requested by users | Vercel compares error rates across versions on Pro/Enterprise | **Yes.** |
| Control plane cannot run arbitrary commands as root on your server | No (SSH) | No (SSH) | n/a | n/a | **Yes, and it is the big one.** |

"Not found" means absent from the docs and pages I read, not proven absent. † as above.

Sources: [Coolify rolling updates](https://n6cloud.com/?p=13547), [managed-platform deploy behaviour ⚠ vendor](https://temps.sh/blog/best-platforms-zero-downtime-deployments-2026), [Coolify MCP security](https://coolify.io/docs/mcp/security), [Coolify MCP overview](https://coolify.io/docs/integrations/mcp), [Coolify audit log](https://coolify.io/docs/core/team/audit-log), [Dokploy MCP](https://socket.dev/npm/package/@dokploy/mcp), [Railway token types](https://docs.railway.com/integrations/api), [Railway rolling-release request](https://station.railway.com/feedback/rolling-releases-canary-deployments-6c105072), [Vercel Rolling Releases](https://vercel.com/changelog/rolling-releases-are-now-generally-available), [Dokploy rollbacks](https://docs.dokploy.com/docs/core/applications/rollbacks).

**My read.** "Scoped MCP tokens" as a headline loses to Coolify on day one. Three things survive scrutiny:

1. **Enforcement lives on the server, not in the agent's client.** Approval happens out of band (a human clicks, or a second factor), deletes are soft by default, production is a protected environment. The PocketOS post-mortems ask for exactly this.
2. **Post-deploy auto-rollback on an error budget.** Nobody in the self-hosted set has it. It needs the proxy to see response codes, which your architecture gives you for free.
3. **A control plane that cannot own your servers.** Agent dials out, accepts only a narrow typed command set, secrets are encrypted so the hosted side cannot read them. After Coolify's CVEs and Railway's breach this is a claim people can verify and repeat.

Items 1 and 2 are features a competitor can copy in a quarter; Temps has already shipped a version of item 1, at $399/org/mo. Item 3 is architecture; Coolify and Dokploy would need a rewrite to match it. **Build the moat on 3, demo with 1 and 2.**

A caution on the audience: the agent-safety buyer is a team with production data and coding agents. The indie dev you want first mostly does not feel this pain yet. Footprint, deploy correctness and security are what earn reputation with indies; agent guardrails are what a team pays for. They are different pitches for different stages.

---

## 6. Will it make money? What the evidence says

**Price points people pay today for a control plane over their own servers**

| Product | Self-serve price | Seats | Payers / revenue |
|---|---|---|---|
| Coolify Cloud | $5 + $3/server, no free tier | Unlimited | ~4,000+ customers, ~$23K+ hosted MRR (~$34K incl. sponsors) |
| Dokploy Cloud | $4.50/server or $15 for 3 servers | 1, or unlimited on $15 | 1,200+ customers |
| Easypanel | $10.90–$29.90 per server | By tier | Not public |
| Laravel Forge | $12 / $19 / $39 flat | Team sharing on $39 | Not public |
| Temps Cloud | $29/mo, invite-only beta | No per-seat | Beta |
| Temps Premium (self-hosted licence) | $399/org/mo | Approval gates, hash-chained audit | Not public |
| _Managed PaaS, for reference_ | Railway $20, Render $25 | Unlimited | Compute margin on top |
| Portainer Business | From ~$1,045/yr | RBAC/SSO gated | Not public |

Sources: section 1 and 2 links, plus [Forge pricing](https://benjamincrozat.com/laravel-forge), [Forge team tier ⚠ vendor](https://deploynix.io/vs/forge), [Portainer pricing](https://botmonster.com/self-hosting/komodo-vs-portainer-vs-dockge-for-your-2026-homelab/), [Portainer gating](https://www.bitdoze.com/portainer-alternatives/).

**What this means for the $49 hypothesis**

- **Nothing self-serve in the category clears $39 for a hosted control plane.** $49 is 3x Dokploy's team plan and 2x Render's, and Render's includes audit logs. The exception is Temps Premium at $399/org/mo, a self-hosted licence sold on guardrails; it shows a ceiling for that buyer, not a team price.
- **Railway and Render can charge $20–25 because the real revenue is compute margin.** You have no compute margin by design. The subscription is the entire business.
- **Conversion is low, and it is not a free-to-paid rate.** Coolify's ~4,000 payers against ~52,890 internet-visible installs is roughly 7–8% **(my estimate; exposed hosts are not all installs, and the counts are from different dates)**. Coolify has no free hosted plan, so every one of those payers chose to pay rather than self-host; it says nothing about how a free hosted tier converts. And those payers are buying the hosted control plane, which you plan to give away to single users.
- **Illustrative maths (my estimate):** 500 paying orgs at $49 is $24.5K MRR, about what Coolify makes from 4,000+ customers. Getting 500 teams to pay 3x the going rate needs a paid tier that is clearly a different product, not the same product with longer retention.
- **Longer retention and faster metrics are weak triggers.** In a model where data lives on the customer's server, they cost you nothing and the customer knows it. Expect resentment, and expect people to self-host to escape the limit, which is free and unlimited by your own design.
- **The enterprise tier is real but slow.** Dokploy, Portainer and Railway all put SSO, SCIM and long audit retention behind sales-led plans. That is where four- and five-figure contracts are, and it takes a year of credibility to sell.

**Pricing shapes the evidence supports better than flat $49/org** (to decide in phase 2, not now):

1. **Meter what scales with value:** servers or environments under management beyond a free allowance (Coolify, Dokploy, Easypanel, Northflank all price this way).
2. **Charge for coordination, not data:** approvals, protected environments, agent policies, PR previews, SSO. These exist only when there is more than one human or agent, and they cannot be dodged by self-hosting solo.
3. **Sell a self-hosted team/enterprise licence** from early on. It is the only proven large-ticket item in the category.

**Costs the brief underweights.** A free hosted tier still pays for auth, websocket fan-out, alert delivery, wildcard-domain DNS and TLS, abuse handling and support. Railway's February outage came from its own abuse-enforcement system misfiring on legitimate users ([report](https://blog.railway.com/p/incident-report-february-11-2026)); free wildcard domains in particular tend to attract phishing (my view, from how free subdomain services generally get abused). And the hosted control plane becomes the target: the Railway breach was about stealing stored secrets at scale.

---

## 7. Gaps we can own, ranked

| Rank | Gap | Why it is open | Durability |
|---|---|---|---|
| 1 | **A control plane with a small blast radius**: outbound-only agent, no root SSH, narrow command set, secrets unreadable by the hosted side, signed and exportable audit trail | Coolify and Dokploy are SSH-from-panel designs with a heavy 2026 advisory record; Komodo proves the model but is not a PaaS | High. Architectural. |
| 2 | **Deploys that are correct by default**: zero-downtime including Compose, instant rollback without Swarm or an external registry, auto-rollback on post-deploy error rate | Top-voted Coolify request, deferred to v5; Dokploy's version drags in Swarm | Medium-high. Hard to retrofit. |
| 3 | **Server-enforced guardrails for agents at a team price**: out-of-band approval, soft delete, protected environments, per-agent identity in the audit log | Coolify and Dokploy have scopes and a caller-set flag; Temps has a real gate but only in a $399/org/mo licence | Medium. Copyable in months, and Temps could cut its price. |
| 4 | **Small footprint**: agent and proxy well under 100 MB, control plane a single binary | Incumbents idle at 300 MB–1.2 GB; Temps needs 4 GB minimum; Komodo's agent runs at ~12 MiB | Medium. |
| 5 | **Day-2 without SSH**: one-click restore, readable failure reasons, a full disk that does not take the server down | Repeated HN complaints across both leaders. Moving apps between servers is now parity (Dokploy v0.31.0) | Medium. Mostly execution. |
| 6 | **Docs and first-hour experience** | Named in nearly every negative thread | Low moat, high conversion impact. |
| 7 | **Honest licensing**: standard OSI licence, no SSO-style paywall on safety features | Dokploy and Portainer both drew fire | Reputation, not revenue. |

**Gaps I would not chase**

- **Template count.** Coolify lists 280–360+, Easypanel 750+. It is a maintenance treadmill and not why anyone churns.
- **Multi-node orchestration.** Swarm is being abandoned by Coolify; the alternatives are Kubernetes or building a scheduler. "Many independent servers, one control plane" covers the target user.
- **Bundled analytics / error tracking / session replay.** Temps is making that its identity. Different product.
- **UI as the moat.** Dokploy is already described as pleasant. A Linear-grade UI will win screenshots and launch-day attention; it will not hold users if deploys drop requests.

---

## 8. Where I disagree with the brief

1. **"Treat MCP scopes as a must-have differentiator."** They are a must-have. They are not a differentiator. Reframe the wedge as in section 5.
2. **"Free where others charge."** The thing others charge for is the hosted control plane. Giving it away wins indie goodwill and removes the category's only proven revenue line. That is a deliberate trade and may be the right one for reputation first; it is not free.
3. **"$49/org."** Unsupported by any comparable. Either the paid tier is a visibly different product, or the number comes down, or the meter changes.
4. **"Nothing excluded" contradicts your own paid list.** The brief puts audit logs, SSO/SAML and unlimited users under Paid. Pick one story. My view: safety features (audit log, MFA, scoped tokens, approvals for one environment) free everywhere; coordination-at-scale features paid. Paywalling audit logs right after a year of control-plane breaches would read badly.
5. **"Compute, builds, logs and backups run on the user's servers, so the free tier is cheap."** Cheaper, yes. The expensive parts of a free tier are abuse, support and being a breach target, and none of those move to the user's server.
6. **Indie reputation first, then team revenue.** Agreed on order. But the indie pitch (tiny, safe, correct deploys) and the team pitch (agent guardrails, approvals) are different products in the buyer's head. The first release has to pick the indie one and make sure the architecture does not block the team one.
7. **The must-have list.** Around 45 items, several of which Coolify took years to stabilise (backups, restores, Compose semantics). That is a phase 4 fight; flagging it now so it is not a surprise.

---

## 9. Questions phase 2 has to answer

1. If the hosted control plane cannot decrypt secrets, how do builds, previews and the web UI get them? This decides how strong claim #1 can honestly be.
2. What is the one paid capability a 5-person team would miss within a week of losing it?
3. Per-org flat, per-server metered, or hybrid? Model each against Coolify's ~$6 ARPU, remembering that Coolify's conversion is from self-hosted to paid, with no free hosted plan in between.
4. Does the first public demo lead with "an agent tried to delete prod and couldn't" or "60 MB, zero-downtime Compose, no inbound ports"? They reach different audiences.
5. Licence: Apache-2.0 everything, or a separate enterprise licence from day one? Dokploy's mid-flight change cost trust.
6. How does a project move between self-hosted and hosted control planes without the hosted one ever seeing plaintext secrets?

---

## 10. Addendum: GitHub issue votes (added after GitHub was linked)

Open issues ranked by 👍 via the GitHub search API, 2026-11-15. **Caveat:** Coolify routes feature requests to Discussions and closes issues aggressively (36 open issues against Dokploy's 570), and this session cannot sort Discussions, so Coolify's issue counts understate demand. The zero-downtime discussion in section 3 (163 votes) is the better Coolify signal.

| Repo | Top open requests (👍) |
|---|---|
| **Dokploy** | [Caddy support](https://github.com/Dokploy/dokploy/issues/1246) (71) · [Compose replicas + autoscaling](https://github.com/Dokploy/dokploy/issues/2579) (56) · [Preview deployments for Compose apps](https://github.com/Dokploy/dokploy/issues/2028) (53) · [Pulumi support](https://github.com/Dokploy/dokploy/issues/1555) (44) · [Pre/post-deploy commands](https://github.com/Dokploy/dokploy/issues/110) (43) · [Import/export projects](https://github.com/Dokploy/dokploy/issues/1733) (38) · [More backup destinations](https://github.com/Dokploy/dokploy/issues/416) (30, 58 comments) · [Own wildcard domain](https://github.com/Dokploy/dokploy/issues/1404) (28) · [Scale to zero](https://github.com/Dokploy/dokploy/issues/2154) (26) · [Network isolation concerns](https://github.com/Dokploy/dokploy/issues/2798) (24) |
| **Coolify** | [v5 tracking issue](https://github.com/coollabsio/coolify/issues/5685) (66, 176 comments) · [Backup manager in the UI](https://github.com/coollabsio/coolify/issues/2389) (24, 53 comments) · [Per-container info and network tab](https://github.com/coollabsio/coolify/issues/2495) (22) · [Backups for git-based Compose apps](https://github.com/coollabsio/coolify/issues/7528) (4, 47 comments) |
| **Komodo** | [Attach to container terminal](https://github.com/moghtech/komodo/issues/75) (88) · [Create networks in UI](https://github.com/moghtech/komodo/issues/57) (52) · [Show deploy output](https://github.com/moghtech/komodo/issues/77) (43) · [External secret providers](https://github.com/moghtech/komodo/issues/1419) (35) plus [a second request](https://github.com/moghtech/komodo/issues/677) (22) |
| **Kamal** | [Multiple instances of an app per server](https://github.com/basecamp/kamal/issues/1190) (41) · [Basic auth](https://github.com/basecamp/kamal/issues/1604) (23) · [More intuitive rollback](https://github.com/basecamp/kamal/issues/1658) (5) |
| **CapRover** | Nothing above 19 votes; most top issues date from 2019–2022. Consistent with "coasting". |

**What the votes add**

1. **PR previews are the most repeated request.** Dokploy has 28 open issues with "preview" in the title; the top four total 128 votes ([Compose](https://github.com/Dokploy/dokploy/issues/2028), [per-preview migrations](https://github.com/Dokploy/dokploy/issues/2462), [pre-built images](https://github.com/Dokploy/dokploy/issues/2386), [GitLab MRs](https://github.com/Dokploy/dokploy/issues/1483)). This is the strongest evidence for previews as a team-tier anchor.
2. **Nobody upvotes team governance.** Requests for better RBAC, approvals and audit sit at 0–1 votes in both repos ([Dokploy RBAC](https://github.com/Dokploy/dokploy/issues/1934), [Coolify preview approvals](https://github.com/coollabsio/coolify/issues/6571), [Coolify project-specific members](https://github.com/coollabsio/coolify/issues/6894), 1 vote but 25 comments). The open-source audience does not ask for what teams pay for. Do not expect the indie community to pull the paid tier into existence; it has to be sold to a different person.
3. **Agent-driven deploys create new failure modes.** A Coolify bug this month: the MCP deploy tool bypassed the per-app deploy queue, two overlapping rolling updates removed each other's containers and the app went down ([#12442](https://github.com/coollabsio/coolify/issues/12442)). The MCP IP allowlist was also not enforced for a time ([#11881](https://github.com/coollabsio/coolify/issues/11881)). Supports "enforce on the server, not in the client".
4. **Portability is wanted.** Import/export of projects has 38 votes at Dokploy ([#1733](https://github.com/Dokploy/dokploy/issues/1733)).
5. **Footprint regressions get noticed.** A Dokploy release that pushed idle RAM to 500–600 MB drew 12 votes and 16 comments ([#3755](https://github.com/Dokploy/dokploy/issues/3755)).
6. **Backups remain the top Coolify day-2 complaint** by comment volume, matching section 3.
7. **MCP itself draws almost no votes** (top MCP-titled issue in either repo: 2). Agent features are supply-led today, not demand-led from this audience.

---

## Source list

**Primary (vendor's own pages, release notes, incident reports)**
- Coolify: [org page](https://github.com/coollabsio) · [pricing](https://coolify.io/pricing) · [MCP security](https://coolify.io/docs/mcp/security) · [MCP overview](https://coolify.io/docs/integrations/mcp) · [audit log](https://coolify.io/docs/core/team/audit-log) · [discussion #3767](https://github.com/coollabsio/coolify/discussions/3767) · [Swarm deprecated](https://next.coolify.io/docs/core/infrastructure/scaling/docker-swarm) · [advisories](https://github.com/coollabsio/coolify/security/advisories) · [v4.7.0](https://newreleases.io/project/github/coollabsio/coolify/release/v4.7.0)
- Dokploy: [pricing](https://dokploy.com/pricing) · [editions](https://docs.dokploy.com/docs/core/differences) · [licence change](https://dokploy.com/blog/we-are-updating-dokploys-open-source-license) · [rollbacks](https://docs.dokploy.com/docs/core/applications/rollbacks) · [advisories](https://github.com/Dokploy/dokploy/security/advisories) · [founder LinkedIn](https://www.linkedin.com/in/carlos-mauricio-ortiz-siu-6b9011184/)
- Komodo: [v2.0.0](https://komo.do/docs/releases/v2.0.0) · [releases](https://github.com/moghtech/komodo/releases)
- Easypanel: [pricing](https://easypanel.io/pricing)
- Railway: [plans](https://docs.railway.com/pricing/plans.md) · [API tokens](https://docs.railway.com/integrations/api) · [October security report](https://blog.railway.com/p/incident-report-october-security-incident) · [Feb 11](https://blog.railway.com/p/incident-report-february-11-2026) · [Feb 18–21](https://blog.railway.com/p/incident-report-february-19-2026)
- Render: [new plans](https://render.com/docs/new-workspace-plans.md) · [announcement](https://render.com/blog/better-pricing-for-fast-growing-teams)
- Northflank: [pricing](https://northflank.com/pricing?utm_category=agents) · [BYOC billing](https://northflank.com/docs/v1/application/billing/pricing-on-northflank)
- Vercel: [Rolling Releases GA](https://vercel.com/changelog/rolling-releases-are-now-generally-available)
- Fly.io: [reliability thread](https://community.fly.io/t/reliability-its-not-great/11253?page=2)

**Community (HN, Reddit, forums)**
- [HN: Coolify thread](https://news.ycombinator.com/item?id=43555996) · [HN: Dokploy thread](https://news.ycombinator.com/item?id=44884077)
- Reddit r/selfhosted (read via mirrors): [Komodo switch](https://www.reddit.com/r/selfhosted/comments/1k33oik/) · [Komodo gaps](https://www.reddit.com/r/selfhosted/comments/1jqcddk/) · [Komodo agent security](https://www.reddit.com/r/selfhosted/comments/1fb4wwl/)
- [Railway forum: token scoping](https://station.railway.com/questions/railway-api-token-returns-unauthorized-c9a776b6) · [Railway forum: rolling releases request](https://station.railway.com/feedback/rolling-releases-canary-deployments-6c105072)

**Press and independent analysis**
- [The Hacker News: Coolify CVEs](https://thehackernews.com/2026/01/coolify-discloses-11-critical-flaws.html) · [AI Incident Database #1469](https://incidentdatabase.ai/cite/1469) · [PocketOS analysis](https://dev.to/arthurpro/how-cursor-with-claude-opus-deleted-a-production-database-in-9-seconds-3gn1) · [The Register: Heroku](https://www.theregister.com/2026/02/09/heroku_freeze/)
- [Advisory counts](https://milanjovanovic.tech/blog/dokploy-vs-coolify) · [Resource test](https://dev.to/fabwaseem/coolify-vs-dokploy-in-2026-resource-usage-tested-on-identical-vms-52c3) · [Komodo footprint](https://jacar.es/en/komodo-vs-portainer-vs-dockge-comparison/) · [TrustMRR: Coolify](https://trustmrr.com/startup/coollabs-technologies-bt)

**Re-verified live 2026-11-15 (corrections applied 2026-11-16)**
- Coolify: [org page](https://github.com/coollabsio) (hosted MRR, total incl. sponsors, cloud customers) · [pricing](https://coolify.io/pricing) (no free cloud tier) · [discussion #3767](https://github.com/coollabsio/coolify/discussions/3767) · [#12442](https://github.com/coollabsio/coolify/issues/12442)
- Temps: [site and pricing](https://temps.sh) · [repository](https://github.com/gotempsh/temps) (stars, pre-1.0, system requirements)
- Dokploy: [releases](https://github.com/Dokploy/dokploy/releases) (v0.31.0 server moves, v0.31.2 latest)
- Railpack: [releases](https://github.com/railwayapp/railpack/releases) (v0.43.0)
- Public Suffix List: [guidelines](https://github.com/publicsuffix/list/wiki/Guidelines) (no SLA; rejects entries meant to avoid Let's Encrypt limits) · Let's Encrypt: [rate limits](https://letsencrypt.org/docs/rate-limits/) (50 certificates per registered domain per week)
- Caddy 2.11.9 · official MCP Go SDK v1.7+ (2026-07-28 spec, stateless Streamable HTTP)

**Vendor-authored comparisons (⚠ biased framing)**
- Temps, Cloudzy, MassiveGRID, Deploynix, Kuberns, DeployHQ, Canadian Web Hosting, as linked inline.
