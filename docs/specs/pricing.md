# Pricing

This is the behaviour spec for Moonglass's plans and prices. The pricing page, the landing copy (`docs/plan/landing.md`), the dashboard's billing screens and the Business licence all say exactly what this spec says. The reasons are in `docs/plan/vision.md` §5. Billing mechanics are in `docs/specs/billing.md`.

## Rules every plan follows

1. **No seat prices.** No plan charges for people, members, invitations, viewers, coding agents or tokens.
2. **One meter, and you control it.** The only thing that changes a bill is the number of extra servers connected to a Cloud control plane. Adding a server shows the new monthly price before the server enrols; removing one lowers the next invoice. Builds, deploys, previews, bandwidth, logs, metrics, backups, projects, environments and organisations are never counted.
3. **No core feature gates.** Every open-core feature works the same on Free, Cloud and Business (`docs/plan/vision.md` §5.1).
4. **Every price is public.** There is no "contact sales" plan and no custom quote. Invoices, a purchase order or a yearly contract are available on Business at the listed price.
5. **A lapse never stops anything that runs.** No plan state reaches the agent. Apps keep running, and deploys, rollbacks, backups, restores and export keep working (`docs/specs/billing.md`, "Lapse").
6. **Prices only go down for existing subscribers.** A list-price rise applies to new subscriptions only; a fall applies to everyone at their next invoice.

## The plans

### Free

- **$0, forever.** Self-hosted: the control plane runs on the user's box (`docs/specs/install.md`).
- Unlimited people, organisations, servers, apps, deploys, backups, teams, roles, approvals and MCP.
- Every open-core feature, including projects, environments, services, previews, databases, the audit trail and its view, and sign-in with GitHub, Google, OpenID Connect, passkeys and TOTP.
- Community support: GitHub Discussions and the community chat.

### Cloud

- We host the control plane; the user brings the servers. The user's servers run the Moonglass agent and proxy as on Free; their apps and data stay on the user's servers.
- **The first server is free, with no card.** Each extra server is **$5 a month** flat. Yearly is ten months' price: **$50 a year per extra server.**
- One free Cloud server per GitHub account. Unlimited organisations live on that one Cloud account.
- Unlimited people, no seats, and every open-core feature.
- Email support.
- A 14-day no-card trial applies to paid servers (`docs/specs/billing.md`). The free first server is not a trial and does not need a card.
- An idle free Cloud account (one server, no paid servers) with no login for 30 days has its control plane paused. Apps on the user's server keep running. The dashboard, deploys and enrolment resume on the next login. There is no app cap (`docs/specs/billing.md`, "Idle pause").
- An account moves to its own box, or back, with `moonglass export` (W7-2) at any time, on any plan.

Coolify Cloud and Dokploy Cloud checked 2026-11-23. The free first server is the hook. Cloud matches Coolify at two servers and is cheaper than Dokploy Hobby at one and two. From three servers Coolify is cheaper ($3 per extra server against $5). Dokploy Hobby is $4.50 per server for one user; Startup is $15 for three servers with unlimited people, then $4.50 per extra. Cloud has unlimited people at every count.

| Servers | Cloud per month | Coolify Cloud | Dokploy Hobby (1 user) | Dokploy Startup (unlimited people) |
|---|---|---|---|---|
| 1 | $0 | $5 | $4.50 | $15 |
| 2 | $5 | $5 | $9 | $15 |
| 3 | $10 | $8 | $13.50 | $15 |
| 5 | $20 | $14 | $22.50 | $24 |
| 10 | $45 | $29 | $45 | $46.50 |
| 25 | $120 | $74 | $112.50 | $114 |

### Business

- **$29 a month per account**, hosted or self-hosted. Yearly: $290 a year.
- On Cloud, server charges are the same as Cloud: first server free, $5 a month per extra server, on top of $29. Business does not bundle servers.
- Self-hosted Business is a $29 licence for one control plane, with any number of servers.
- Everything in Cloud, plus the Business module:
  - SAML single sign-on, and SSO enforcement that makes the identity provider the only sign-in for an organisation's members (the owner keeps a break-glass `server login-link` on self-hosted, `docs/specs/auth.md`).
  - SCIM provisioning of members and their roles.
  - Audit log export: every audit event to an S3-compatible bucket on a schedule, or streamed to a webhook or a SIEM, with the hash chain intact so the export can be verified with `moonglass audit verify`.
  - Priority support: a reply within one business day, and a shared chat channel.
- A 14-day no-card trial, on Cloud or with a trial licence.

### Founding members

- The first 1,000 Cloud accounts and the first 100 Business accounts are founding members.
- Founding price is **40% off list for the first 12 months only**, then list price: **$3 a month per extra Cloud server, $17 a month Business.** Yearly is ten months' price.
- The discount is not for life. It ends when the 12 months end, or sooner if the subscription is canceled.
- Cloud founding and Business founding are separate places. An account can hold both. A plan change does not start a new 12-month clock.
- The pricing page shows how many founding places are left.

## What the dashboard shows

- **Free:** nothing about plans. No banner, no upsell, no locked menu item. Settings → About shows the version and a link to the pricing page.
- **Cloud and Business:** Settings → Billing shows the plan, the founding term if any, the servers counted, the next invoice and its date, the card, invoices, idle-pause state if it applies, and the change and cancel buttons (`docs/specs/billing.md`).
- **Adding a server on Cloud:** the enrolment dialog states the new monthly price before it shows the install command (`docs/specs/ui.md`, "Servers").
- **Business features on Free or Cloud:** the SSO, SCIM and audit export sections in Settings say what they do and link to Business. They are the only places a plan is mentioned.

## Where this is built

| Part | Where |
|---|---|
| The open core: every feature, on every plan | This repository, Apache-2.0 |
| The extension points a licensed build uses, with no licence check in the open build | This repository (V1-1) |
| The managed instance mode of `moonglass server` | This repository (V1-2) |
| Sign-up, billing, the Cloud fleet, its backups and upgrades | `moonglass-dev/cloud`, its own licence (W7-4) |
| SAML, SCIM, SSO enforcement, audit export and the offline licence check | `moonglass-dev/business`, its own licence (W7-4, V1-5) |
