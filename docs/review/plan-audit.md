# Plan audit

_2026-11-22, week 2 of 8. Checked against the docs map in `AGENTS.md`. **Owner** marks a contract, an ADR or a confirmed decision._

**Verdict.** The architecture is the right bet and its core is landing: the sanitizer, the typed protocol, release switching and a CI run of ten deploys with zero failed requests. The plan around that core is not the shortest path to revenue. It builds free multi-tenant hosting, a full secrets lifecycle and fleet rollouts before any outsider has deployed an app, and it ships self-hosting last. Ticket count overstates progress. Preflight splits each ticket 4–8 ways, which took the plan from 75 tickets (11-17) to 184, and unsplit tickets remain (W4-6, W5-2, W5-4, W6-2, W7-1). By my estimate, 72 merged is about 30% of the work. Of the open tickets, 68 are `high`: they run one at a time, and the owner reads every line of each. G0 (waitlist, interviews, SPIKE-1) was never met. All the owner tickets still open face the market.

## 1. Findings, ranked by impact

| # | Finding | Change |
|---|---|---|
| 1 | No evidence yet that anyone will switch | Before Dec 1: waitlist live (W0-5), 10 interviews booked with people from the R§10 threads, SPIKE-1 running. Week 5 waits on the M§2.2 thresholds |
| 2 | Self-hosting comes last (W7-1 is week 7), and FR6-1 is undecided, so the CLI cannot reach a real server over verified TLS. Hosted earns $0 at launch but brings Postgres ops, invite gating, entitlements and abuse work | **Owner:** launch self-hosted first. Decide FR6-1 now, and move W7-1 and W7-5 to right after W2-5. Make "one VPS, one command" the week-4 demo and the alpha |
| 3 | "Nothing to SSH into" is false for self-hosted. Daily sign-in and every step-up are `server login-link`, printed on the box (ADR-0005 decisions 2–3), and `secrets grant` needs a terminal | **Owner (ADR-0005):** make GitHub sign-in the daily path and keep the login link for emergencies. Bring passkeys forward for step-up. Until then, say "the control plane never SSHes into your server" |
| 4 | By V§5.6's own maths, the free hosted tier converts into the second-smallest revenue line, and paid-first hosted is the largest. R§0: free hosting "gives away the only thing people in this category demonstrably pay for" | **Owner (reverses V§8):** hosted is paid from day one, with a 14-day trial and no card, as at Coolify. Open source stays free. Revisit free hosted at 50 payers |
| 5 | The visible wedge comes late. Coolify already rolls single-service apps on a health check; its weak spot is Compose (#3767, 163 votes). W6-4 sits behind 17 agent-safety tickets that V§1 calls "the demo, not the headline" | Move W6-1a–d, W6-4a, W6-4c and W6-4 ahead of week 5. Lead with "your compose.yaml, redeployed without dropped requests" |
| 6 | Missing: no ticket to invite members, though a second person is the main paid trigger (V§5.5). Also hosted sign-up (ADR-0005, open question 2), billing, off-box offline alerts, a restore button (W6-3 is CLI only, against "one-click") and docs before W8-D | Add a ticket for each, with billing through a merchant of record, alerts as Slack and Discord webhooks, and a quick start before the alpha |
| 7 | Overbuilt for launch: W7-6f (staged rollout for a fleet of three), W4-1b (Google sign-in), W5-4 (MCP with OAuth 2.1, though no MCP issue has more than 2 votes), W3-3, W4-9, W5-8a, W5-8, W7-2, W7-3 (the Coolify importer) and W8-M | Move these ten to v1 (seven are `high`). Keep signed self-update with revert (W7-6e, W7-6g) |
| 8 | 49 of 184 tickets are fixes, and `high` covers all of store and registry, so create-and-list work queues behind security work | **Owner:** keep `high` for protocol, policy, keys, auth, update and install; store and registry create-and-list work becomes `standard` |
| 9 | Slow first hour: the first environment starts protected (registry.md), so a second server needs the recovery key at a terminal | Start unprotected, offer "protect production" after the first deploy, and time three alpha users to first deploy |

## 2. Positioning

| | Strength | Why switch | Built |
|---|---|---|---|
| **Coolify** | 62K stars, ~4,000 payers, scoped MCP, previews | No root shell from the control plane (61 advisories in 2026), Compose without downtime, restore without SSH, small footprint | Architecture done. Compose and restore in week 6. Footprint unproven |
| **Dokploy** | Nice UI, $15 for unlimited users | The same security case (a pre-auth takeover); rollback without Swarm or a registry | Done |
| **Kamal** | Zero-downtime, free | Kamal's deploys plus a UI, backups, secrets and auto-rollback | Weeks 4–6 |
| **Railway** | DX, zero-config builds | A flat fee on your own VPS, after its breach and a prod deletion by a coding agent | Push-to-deploy in week 4; zero-config builds in v1 |

The durable wedge is built first, which is right, but nobody can see it until they can install it. Treat Railway users as a v1 audience.

## 3. Money

- **Two offers, not six.** Open source: free and unlimited, with members and one approval gate. Cloud: Pro at $9 and Team at $29, each with 5 servers and then $3 a server. The Team licence and Enterprise wait until three teams ask in writing.
- **$10K MRR** (my estimate) takes ~670 payers at $15 or ~345 teams at $29. Coolify took years and 62K stars to reach ~4,000 payers (~$23K). Getting there in 18–30 months means ranking third in the category; a realistic month 12 is $1–3K. The fastest route is agencies and small teams, sold by hand.
- **Before anyone pays.** Pro: hosted with backups and a status page, billing, agent self-update, off-box alerts, git-push deploys (W4-10) and export. Team: invites, approvals (W5-2), the audit view and alerts to people. Previews carry the strongest team evidence (128 votes), so they come before Team.

## 4. Contradictions

| # | Says | But | Fix |
|---|---|---|---|
| C1 | Sign-in is GitHub plus email links (V§5.3, M§3 week 4, the ADR index) | GitHub plus Google, no email (ADR-0005 as amended, auth.md) | Update the three |
| C2 | The cut order drops webhooks and keeps email (M§1, M§4) | The MVP sends no email (ADR-0005 decision 5, W4-7) | Rewrite M§1 and M§4 |
| C3 | Changed Compose services restart in place until v1 (V§1, S§5.3) | They redeploy "never by stop-and-start" (compose.md) | Pick one; S§5.3 is public copy |
| C4 | "No paywall on safety" (V§4) | Free caps agent identities at 2 and protected environments at 1 (V§5.3) | Drop the caps |
| C5 | G0 gates week 1, and W1-5 depends on SPIKE-1 | G0 unmet, W1-5 merged, SPIKE-1 "deferred" (memory.md) | Record an owner decision with a date |
| C6 | The control plane holds no credentials after setup (ADR-0003) | The W4-10 GitHub App holds a key and a webhook secret | Decide before W4-10 |

Findings #3 and #6 are contradictions too: V§1 against ADR-0005, and V§5.3 against auth.md.

## 5. Riskiest assumptions, cheapest tests

| Assumption | Test | Pass |
|---|---|---|
| Users switch for the architecture (today's workaround is a VPN in front of the panel) | 10 interviews | ≥6 name a pain the MVP solves; ≥3 would move within a month |
| People pay for hosted when the open-source build is free | Show the price on the hosted waitlist button; take $29 founding reservations by payment link | ≥35% choose hosted; ≥3 reservations |
| Teams pay for approvals (0–1 votes in R§10) | Show a 2-minute mock of the W5-6 demo to five team leads | 2 ask for a pilot |
| Under 100 MB holds (today: one 3-second sample) | SPIKE-1, 48 h on the 2 GB box | p99 ≤100 MB; growth <5% |
| The core that agents wrote holds | A paid review of `internal/policy` and `internal/protocol` only | No open criticals at launch |
| Eight weeks is enough | M§1: a slip of over a week moves launch to week 10 | G2 passes by Dec 7 |

## 6. Dec 1, ten users, one payer

**Don't post it as a Show HN.** The guidelines say "If your work isn't ready for users to try out, please don't do a Show HN", and sign-up pages are off topic. Post a technical story with a 60-second video instead, and keep Show HN for the self-hosted alpha. Don't claim "under 100 MB", "one-click restores" or "nothing to SSH into" yet.

> **Moonglass – deploy to your own servers from a control plane that can't SSH in**
> Moonglass is an Apache-2.0 deploy platform. An agent on each server dials out and accepts only the commands in a public `.proto`. It refuses privileged containers, host mounts and the Docker socket unless the server's own `agent.toml` allows them. Today, in CI on Docker 25–29: ten deploys in a row with zero failed requests, and rollback in under 5 s [video]. Not built yet: the UI, git builds, secrets and backups. A self-hosted alpha opens in January. If you run production on Coolify, Dokploy or Kamal, I'd like 30 minutes to hear what last broke: [link]

**Ten users:** interviews Dec 1–7, then a one-box self-hosted build with image, git and Compose deploys (#2, #5). Install it with each user on a call. Measure the time to first deploy and whether they deploy again within a week.

**First payer, in January:** the alpha user with three or more servers, a team or clients. Run a single-tenant control plane for them at a locked $29 a month, billed by payment link, with migration help. It needs no multi-tenant Postgres and no entitlements, and it proves someone pays before W7-4 is built.

## Roast

_An outside roast of this audit and the plan, appended as received on 2026-11-22. Decisions on every finding follow it._

You aren't building a $10K MRR business right now; you're running an autonomous AI ticket factory to build a security-architecture thesis nobody asked for, in one of the worst-monetizing categories in developer tools. Both the build plan and the audit are way too charmed by the elegance of your Go binary to confront the brutal business and engineering realities staring right back at you.

Here is what will kill this project, ranked from most fatal to least, with what to change.

---

### 1. The $10K MRR math is a fantasy in a low-ARPU tarpit — and the audit lets you off the hook

Coolify has 62,000 GitHub stars, ~52,900 internet-visible installs, years of community momentum, and only ~4,000 paying customers (~$23K MRR). Your plan aims to make **nearly half of Coolify's revenue** as a solo founder by selling a $9 Pro plan and a $29 Team plan to developers whose defining trait is hating $20/mo PaaS bills. Even your revised four-line revenue table assumes you somehow conjure 10,000 active free orgs, 175 paid-first hosted customers, 50 self-hosted Team licensees, *and* 4 Enterprise contracts at $6K/year—which is four completely different go-to-market motions (PLG freemium, self-serve cloud, commercial open-core licensing, and enterprise sales) executed by one person.

**Where the audit is too soft:** The audit casually drops *"Getting there in 18–30 months means ranking third in the category"* and then tweaks pricing to $9/$29 as if ranking #3 in an open-source PaaS category automatically prints $10K MRR. In open-source dev tools, #3 usually makes $1K–$3K/mo until the founder burns out. Worse, the audit calculates you need 345–670 paying accounts to hit $10K MRR without asking what happens when 500 infrastructure customers depend on one person when a deploy, TLS renewal, or webhook breaks at 2 AM.

**Fix:** Pick **one** buyer with a real budget and raise your ACV by 5–10x. Either stop chasing $10K MRR from $9 hobbyists and build a $99–$299/mo product for agencies/small dev shops (multi-client fleet management, white-label status/audit reports, SLA backups, client handoff), or re-anchor your expectation that a solo Coolify competitor is a $2K–$4K MRR lifestyle product unless you find a much higher-value wedge.

---

### 2. You blew right past your own stop-gate to play with an LLM factory

Gate G0 explicitly states that if the waitlist isn't live, five interviews aren't booked, and `SPIKE-1` isn't decided: **"Do not start week 1."** Yet here you are at the end of week 2 with `W0-5` and `SPIKE-1` still unchecked, zero interviews done, and 72 merged PRs out of an exploded 184-ticket backlog. You didn't skip G0 by accident—spinning up Grok + Opus agents to merge 72 clean Go PRs feels like 10x progress, whereas getting on 10 Zoom calls and hearing *"I just put Coolify behind Tailscale, why would I migrate?"* is painful.

**Where the audit is too soft:** Sentence one of the audit hands you a participation trophy: *"The architecture is the right bet and its core is landing."* No staff engineer or founder should call an architecture "the right bet" when zero external users have confirmed the problem is painful enough to migrate production servers. The audit's own Section 5 admits the current workaround is simply putting a VPN in front of the panel.

**Fix:** Freeze the agent dispatcher **today**. Literally stop merging code for 7 days. Put up a dead-simple page, DM 50 Coolify/Dokploy/Kamal/agency operators, and run those 10 interviews before another line of Go lands. If fewer than 3 people say *"I will move a real app onto this next month and pay $X,"* pivot the product—not the ticket order.

---

### 3. Your self-hosted product actively cannibalizes your hosted revenue

Look at the structural contradiction at the heart of `vision.md`: you are engineering the free Apache-2.0 open-source binary to be **<100 MB, single-binary, one-command install, zero-SSH, unlimited users, unlimited servers, PR previews included, hash-chained audit logs included, AND one approval gate per environment included**. Coolify Cloud makes money largely because self-hosting Coolify is heavy (300 MB–1.2 GB RAM) and annoying to maintain. By making Moonglass absurdly light and effortless to self-host—and giving away multi-user and approval gates in the free open core—you systematically destroy every reason a technical 3–10 person team would pay you $29/mo + $3/server to host a 50 MB Go binary they can run on a $4 Hetzner box.

**Where the audit is wrong:** The audit tells you to launch self-hosted first (*"one VPS, one command"*) and still assumes hosted Cloud at $9/$29 will be your primary revenue engine. If self-hosted is that good and that free, you are training your entire early audience to never pay you.

**Fix:** Decide whether you are selling **convenience** (hosted control plane) or **team governance** (commercial license). If it's convenience, keep self-hosted intentionally single-user (no multi-user RBAC or approvals in the free open core; make teams pay for either Cloud or the Team license). If you refuse to gate multi-user in open source, accept that $9/$29 Cloud won't save you and build genuinely high-value team/agency features into the commercial repo (multi-client workspaces, SSO/OIDC, compliance packs, managed off-box alerting/backups) priced at $99+/mo.

---

### 4. You are massively over-building cryptographic Rube Goldberg machinery (and the audit barely touches it)

Before a single outsider has deployed a container, your 8-week MVP includes:

* End-to-end encrypted secrets using `age` + X25519 agent keys + `pins.json` recovery pinning + client-side TypeScript encryption in the browser (`W4-5`) + step-up auth + CLI-only reveal (`W4-9`).
* Dual storage backends (SQLite *and* Postgres with full Row-Level Security `org_isolation` policies and dual conformance suites for every table).
* Custom stdlib-only SSHSIG release verification requiring two offline hardware FIDO2 keys with `0x01`/`0x04` flags, SBOM attestations, and 10%/50%/100% staged canary rollouts.
* Hash-chained tamper-evident audit logs with client-side verification pinning (`moonglass audit verify`).
* A custom auto-rollback error-rate engine polling Caddy counters with shadow mode (`W4-3a–f`).

Worst of all, **E2EE browser secrets in a PaaS is security theater that ruins UX.** Because the control plane serves the SvelteKit UI that runs the TypeScript `age` encryption (`W4-5`), a compromised hosted control plane can just serve malicious JS and steal plaintext secrets anyway (which `vision.md` quietly concedes as *"Honest limits on browser encryption"*). So you've saddled every user with `pins.json`, recovery keys, terminal-based `secrets grant`, and write-only UI fields—for a threat model that still breaks the moment someone uses your web UI!

**Where the audit is too soft:** The audit only trims 10 peripheral tickets (Google sign-in, MCP, staged rollout, Coolify importer, etc.) and leaves the entire E2EE `age` secrets circus, dual SQLite+Postgres RLS, hash-chained audit logs, step-up auth, and error-rate auto-rollback untouched.

**Fix:** Kill E2EE secrets, `pins.json`, browser `age` encryption, step-up auth, hash-chained audit verification, error-rate auto-rollback, and dual-backend RLS for launch. Ship **SQLite-only** self-hosted first with standard encrypted-at-rest secrets (or KMS envelope encryption when you eventually launch hosted). Nobody switches off Coolify because Coolify doesn't have client-side `age` key-wrapping in TypeScript.

---

### 5. Your "AI-built security fortress" is already leaking security bugs at a 26% fix rate

Your entire technical moat is *"we are the ultra-hardened, zero-CVE alternative to Coolify's 61 advisories and Dokploy's pre-auth takeover."* Yet your build process uses LLM workers auto-merging to `main` with **0 required GitHub approving reviews** (just an Opus PR comment) while one human tries to read every line of 68+ open `high` tickets.

Look at the drift receipts in `build-plan.md`—**49 of 184 tickets (26.6%) are already fix tickets in week 2**, and look at what slipped past Opus review:

* `FD1-1`: `AddEnrolmentToken` didn't verify the token was hashed, allowing plaintext tokens to be stored.
* `FD1-2`: `EnrolmentTokens` queried a lookup table without RLS.
* `FD1-3`: SQLite DSN string concatenation broke on `?` or `#`, opening databases without pragmas.
* `FD1-11`: `scripts/install` ran plain `ssh-keygen -Y verify`, completely failing to enforce the FIDO2 flags your threat model brags about.
* `FD1-12`: The envelope fuzz test only unmarshaled/marshaled and never actually opened payloads.
* `FD1-13`: Image validation allowed invisible Unicode and bidi spoofing characters.
* `FD1-16`: The "no inbound port" test only grepped a single file (`connect.go`).

That is textbook AI-generated security-theater drift. One high-profile CVE in your first six months vaporizes your entire positioning, and you cannot personally audit 184 AI PRs without rubber-stamp fatigue.

**Where the audit is wrong:** The audit recommends downgrading store/registry tickets to `standard` to unblock velocity and paying for an external audit of **`internal/policy` and `internal/protocol` only**. Look at the bugs above: half of your worst security bugs were in `internal/store` (`FD1-1`, `FD1-2`, `FD1-3`) and `scripts/install` (`FD1-11`)—the exact packages the audit excludes from review!

**Fix:** Shrink the trusted computing base by 70% (see #4) instead of lowering review standards or paying to audit only two packages. Less code = smaller attack surface = code one staff engineer can actually hold in their head and own completely.

---

### 6. The audit's recommended "Compose wedge" is broken by your own specs

The audit tells you to pull Compose forward and lead with: *"your compose.yaml, redeployed without dropped requests."* Did the auditor actually read `W6-1c`, `W6-4a`, and `W6-4`?

* `W6-4a` hard-refuses **bind mounts, custom `networks`, and volume drivers/options**.
* `W6-1c` specifies that any app with a volume **never runs two releases at once and is never rolled back**.
* `W6-4` explicitly states that **a changed service with a named volume is refused**.

Think about what real-world `compose.yaml` files on Coolify look like: Postgres/SQLite/Redis/Ghost/n8n/MinIO/WordPress stacks with named volumes, config bind mounts, and internal networks. If your headline promise is *"drop in your compose.yaml for zero-downtime redeploys"* and the first thing Moonglass does is reject bind mounts, reject networks, and refuse to update any service with a named volume, users will close the tab in two minutes and flame you in the HN comments.

**Fix:** Do not market zero-downtime Compose until you support updating stateful services gracefully (stop-and-start for stateful services, blue/green for stateless services) and handling basic custom networks/volumes. Pick the wedge you can actually deliver cleanly: zero-downtime single-service + Dockerfile/git deploys with rock-solid rollback and dead-simple backups.

---

### 7. You are missing the table-stakes UX features users actually switch for

While you spent two weeks building FIDO2 SSHSIG verifiers and `pins.json` specs, look at what your MVP is missing for real developers trying to deploy apps:

* **No zero-config builds (Nixpacks/Railpack/Buildpacks) until v1** — forcing every user to write a Dockerfile or configure a CI template, killing your "deploy in under five minutes" promise.
* **No container exec / one-off shell until v1** — the second a Rails/Django/Node app needs a live console command or DB inspection, "nothing to SSH into" collapses and they're right back in SSH running `docker exec`.
* **One database per kind per environment** (`W6-1`) — weirdly restrictive for multi-service stacks.
* **No team invites, no billing, no off-box offline alerts, and no UI restore button** (`W6-3` is CLI-only, directly contradicting "one-click restores" on the landing page).

**Fix:** Cut Week 5 (agent safety) and Week 3/4 E2EE secrets entirely, and spend that time on the actual first-hour experience: Railpack/Nixpacks auto-builds, a working UI with a real one-click restore button, Slack/Discord offline alerts, and container exec (or stop promising "nothing to SSH into").

---

### 8. "Coding agent safety" is a solution looking for a problem (and GitHub already owns it)

Both `vision.md` and `plan-audit.md` admit governance and approval requests sit at **0–1 votes** in Coolify and Dokploy. Yet Week 5 burns 18+ tickets on agent identities, an 8×9 hand-written authz matrix, approval diffs, MCP OAuth 2.1, soft delete, and purge retention.

Neither your vision doc nor the audit mentions the actual incumbent for deployment guardrails: **GitHub Actions + GitHub Environments** (and GitLab CI). Teams that don't want Claude Code or a junior dev nuking production don't give coding agents production deploy credentials—they make them open a PR, and GitHub's native branch protection + deployment approvals gate production for free or pennies. Selling *"approver rules"* for $29/mo against GitHub Environments—while using Temps Premium's $399/mo sticker price (~920 stars, pre-1.0) as proof people will pay for it—is anchoring on an unproven competitor.

**Fix:** Move the entire Week 5 agent-safety stack (approvals, agent identities, local policy classes, soft-delete purge machinery, MCP) to **post-revenue v1**. Keep simple scoped API tokens (read-only / deploy-to-staging) and ship them only after paying users ask for guardrails.

---

### 9. The 8-week schedule (and the audit's "week 10" slip) is mathematically impossible

At the end of week 2, you have 72 merged tickets (~30% of the work), 112+ open tickets, multiple unsplit epics (`W4-6` entire UI, `W5-2` approvals, `W6-2` streaming encrypted S3 backups, `W7-1` self-hosted mode), and a 26%+ drift/fix tax. Worse, **68 open tickets are `high` risk and run strictly one at a time**. Even if you somehow spec, code, test, review, and personally read every line of 2 `high` tickets per day with zero new splits and zero new bug fixes, 68 serialized tickets alone takes 34 working days (~7 weeks). Pretending a slip just moves launch to Week 10 is pure denial—this scope is a 4-to-6-month build.

**Fix:** Throw away the 184-ticket roadmap. Write a ruthless **15-ticket "Ship to 10 real users in 14 days" list**: verified TLS (`FR6-1`) + self-hosted single binary + SQLite + GitHub sign-in + basic UI + git/image zero-downtime deploy + rollback + simple encrypted-at-rest env vars + Postgres backup/restore button. Delete or defer everything else until 10 strangers deploy real apps on it.

---

### 10. The audit's "first payer in January" tactic is disguised $29 consulting

In Section 6, the audit suggests getting your first payer by running a bespoke single-tenant control plane for an alpha user at a **locked $29/month**, including migration help. That does not validate a $10K MRR SaaS; it validates that someone will happily accept 10 hours of free DevOps migration labor and dedicated hosting for the price of two pizzas. And locking them at $29/month forever sticks you with bespoke infrastructure debt you can't afford to maintain.

**Fix:** If you want to validate paid willingness early with hands-on migration and dedicated hosting, charge **$199–$300/month** for a founding agency/team concierge pilot (or a $500 setup fee + $99/mo). If nobody will pay $150+/mo when you're literally migrating their servers for them, you just saved yourself 18 months of chasing a market that won't pay.

---

If you had to cut 70% of this roadmap tomorrow so a stranger could deploy a real app on their own VPS next week, which specific piece—E2EE secrets, Postgres multi-tenancy, or the Week 5 agent-safety stack—are you actually willing to kill first?

## Decisions on the audit and the roast

_2026-11-22. Every finding above, accepted or rejected. The accepted changes are applied in the build plan, vision, milestones, stack, ADRs, threat model and specs in the same PR. **Owner** marks a decision that reverses an earlier owner decision or amends an ADR or a contract, so it holds only once the owner approves that PR._

**The new shape.** The launch is self-hosted Moonglass on one VPS, open source and for one person, when G8 passes; there is no date. The alpha opens at G4, once G0 has passed. The paying buyer is agencies and small teams, sold founding pilots at $199 a month. The control plane drops Postgres for SQLite only. 22 tickets move to v1 (B§7) and FA1-1 is added. The plan goes from 184 tickets to 161, with 89 open; open `high` tickets drop from 68 to 54. The alpha needs 38 of them, 34 `high`, including the open `high` fix tickets and SPIKE-1.

| # | Finding | Decision | Why, or what changed |
|---|---|---|---|
| A1 | No evidence anyone will switch | Accept | G0 now gates the alpha, and passing the interviews includes paying the pilot price (M§2.2) |
| A2 | Self-hosting comes last | Accept · **Owner** | No hosted service at launch. W7-1 and W7-5 move into week 4. FR6-1 is on the owner checklist |
| A3 | "Nothing to SSH into" is false | Accept in part · **Owner** | GitHub is the everyday sign-in and step-up on the box, and the login link is for the first sign-in and emergencies (ADR-0005). The claim is now "never SSHes into your servers". Rejected: bringing passkeys forward, because a GitHub round trip already gives a step-up without SSH |
| A4 | Free hosted | Accept, widened by R1 · **Owner** | No free hosted tier and no $9 Pro (V§4, V§5) |
| A5 | Pull Compose ahead of week 5 | Reject | R6 is right: the specified subset refuses bind mounts, `networks` and changes to services with volumes, so it cannot carry the headline |
| A6 | Missing pieces | Accept in part | Offline alerts with an outside heartbeat URL (W4-7), a Restore button (W6-3), backup-failed alerts (W6-2) and the quick start before the alpha (W8-D) are in. Invites move to the commercial module (R3). Rejected: billing and hosted sign-up tickets, because pilots are invoiced by hand and there is no hosted sign-up without hosting |
| A7 | Overbuilt for launch | Accept | W4-1b, W5-4, W3-3, W4-9, W5-8a, W5-8, W7-2, W7-3 and W8-M go to v1. W7-6f keeps only the self-hosted channel offers |
| A8 | Lower `high` for store and registry | Reject | R5 is right: FD1-1, FD1-2, FD1-3 and FD1-11 came from exactly those paths |
| A9 | First environment starts protected | Accept | The protected flag (W4-2) goes to v1 with approvals. When it returns, every environment starts unprotected (ADR-0006) |
| C1 | Sign-in: GitHub plus email links | Accept | The vision, milestones and ADR index now say GitHub, with Google in v1 |
| C2 | Email versus webhooks | Accept | No email in the MVP; M§1 and M§4 rewritten |
| C3 | Compose restart rules | Accept | Compose is v1; S§5.3 and `compose.md` say what v1 will do |
| C4 | Caps on safety features | Accept | The caps left with the Free plan. V§4 now separates what one person needs (free) from what several people need (paid) |
| C5 | G0 skipped | Accept | G0 gates the alpha (B§2) |
| C6 | GitHub App credentials | Accept | W4-10 is v1, so ADR-0003 decision 10 holds at launch |
| §3 | Pro $9, Team $29 | Reject | R1 is right: those prices need 345–670 payers, more than one person can sell to or support |
| §5 | Paid review of two packages | Reject | R5 is right: W8-R reviews the whole trusted core, and an external review still comes before any "audited" claim |
| §6 | The Dec 1 message | Accept | Unchanged |
| §6 | First payer at $29 | Reject | R10 is right |
| R1 | $10K maths, one buyer | Accept · **Owner** | One buyer: agencies and 3–10 person teams. Founding pilot at $199/mo. $10K is about 50 such customers. If pilots will not close at $150 or more, it is a $2–4K product (V§5) |
| R2 | Skipped G0 | Accept in part | G0 gates the alpha, with the pivot rule in M§2.2. Rejected: the 7-day code freeze, because the alpha tickets cost agent time, not the owner's interview time (`high` diffs are read in the weekly review), and they are the deploy core that any interview outcome needs |
| R3 | Self-hosted cannibalises hosted | Accept · **Owner** | The open core is everything one person needs. Members, roles and approvals are the commercial module, sold as a Team licence or a managed control plane (V§5.1) |
| R4 | Crypto overbuilt | Accept in part · **Owner** | SQLite only (FA1-1). Browser secret entry (W4-5), reveal (W4-9), rotate and `RewrapEnvKey` (W3-3), the audit view and `audit verify` (W5-5) and the staged rollout go to v1. Rejected: dropping encrypted secrets, because "never holds a readable secret" is the claim Coolify and Dokploy cannot match, and the key code is already merged (W3-1). Rejected: dropping pins, because they live in two unbuilt tickets and stop a taken-over control plane from redirecting new secrets. Rejected: dropping step-up, because its store half is merged, it is what stops a stolen session minting tokens, granting keys or purging, and with GitHub it is a browser round trip. Rejected: dropping the error-rate trigger, because it is one standard ticket on counters already merged, it is wired through five approved specs, and no one else in the category ships it (R§5) |
| R5 | Security bugs from AI-built code | Accept | Risk labels stay. The cuts in R4 and R8 shrink the trusted core instead |
| R6 | The Compose wedge is broken | Accept | Compose is v1, and `compose.md` names the three rules to change first. The wedge is single-service zero-downtime deploys from an image, a Dockerfile or git, plus rollback and backups with restore |
| R7 | Missing table stakes | Accept in part | Offline alerts and the Restore button are in, and the SSH claim changes instead of container exec being built now. Rejected: Railpack for launch, because it is a pinned outside binary that needs its own ADR and a 2 GB memory test, and Dockerfiles plus the CI template cover the alpha; it is the first v1 build item if the interviews ask for it. Rejected: more than one database per kind, because one Postgres and one Redis per environment cover the alpha |
| R8 | Agent safety is a solution looking for a problem | Accept in part | Approvals, MCP, the demo script and environment and project deletion go to v1. W5-E is rewritten as the G5 path. The vision now names GitHub Environments as the incumbent. Rejected: deferring agent identities (W5-1a to W5-1), because without them the CI template ships a full owner token, the exact Railway failure S§4 promises against, and W5-1 builds the authz-matrix suite S§7 makes permanent. Rejected: deferring service soft delete (W5-5b to W5-5e), because a launch needs delete and the approved delete is soft. W5-3 stays because it wires code already merged in W5-3a |
| R9 | The schedule | Accept in part | No date: the launch is G8 and the alpha is G4 (M§8). The auto-rollback tickets move behind the alpha path. Rejected: replacing the plan with 15 tickets, because the 72 merged stay; the alpha is the roast's short list with its security fixes, 38 open tickets |
| R10 | The $29 pilot is consulting | Accept | Founding pilot at $199/mo with setup and migration, locked for 12 months |

**What gets killed first,** answering the roast's last question: Postgres multi-tenancy (ADR-0002 as amended), then most of week 5. The encrypted-secrets core stays.
