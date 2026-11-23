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
