# Pricing

This is the behaviour spec for Moonglass's plans and prices. The pricing page, the landing copy (`docs/plan/landing.md`), the dashboard's billing screens and the Business licence all say exactly what this spec says. The reasons are in `docs/plan/vision.md` §5. Billing mechanics are in `docs/specs/billing.md`.

## Rules every plan follows

1. **No seat prices.** No plan charges for people, members, invitations, viewers, coding agents or tokens.
2. **One meter, and you control it.** The only thing that changes a bill is the number of servers connected to a Cloud control plane. Adding a server shows the new monthly price before the server enrols; removing one lowers the next invoice. Builds, deploys, previews, bandwidth, logs, metrics, backups, projects, environments and organisations are never counted.
3. **No core feature gates.** Every open-core feature works the same on Free, Cloud and Business (`docs/plan/vision.md` §5.1).
4. **Every price is public.** There is no "contact sales" plan and no custom quote. Invoices, a purchase order or a yearly contract are available on Business at the listed price.
5. **A lapse never stops anything that runs.** No plan state reaches the agent. Apps keep running, and deploys, rollbacks, backups, restores and export keep working (`docs/specs/billing.md`, "Lapse").
6. **Prices only go down for existing subscribers.** A list-price rise applies to new subscriptions only; a fall applies to everyone at their next invoice.

## The plans

### Free

- **$0, forever.** Self-hosted: the control plane runs on the user's box (`docs/specs/install.md`).
- Unlimited people, organisations, servers, projects, environments, services, deploys, previews, databases and backups.
- Every open-core feature, including teams and roles, approvals and approver rules, coding agents and MCP, the audit trail and its view, and sign-in with GitHub, Google, OpenID Connect, passkeys and TOTP.
- Community support: GitHub Discussions and the community chat.

### Cloud

- **$4 a month with 2 servers, then $2 a month for each additional server.** Yearly: $40 a year with 2 servers, then $20 a year per additional server.
- We run the control plane: one `moonglass server` instance per account, with its own SQLite file (ADR-0002), its backups encrypted to the owner's recovery key, and its upgrades.
- The user's servers run the Moonglass agent and proxy as on Free; their apps and data stay on the user's servers.
- Unlimited people and organisations in the account, and every open-core feature.
- Email support.
- A 14-day trial with 2 servers and no card. At the end of the trial the account asks for a card; without one it lapses (`docs/specs/billing.md`).
- An account moves to its own box, or back, with `moonglass export` (W7-2) at any time, on any plan.

| Servers | Cloud per month | Coolify Cloud (2026-11-23) | Dokploy Cloud, unlimited people (2026-11-23) |
|---|---|---|---|
| 2 | $4 | $5 | $15 |
| 5 | $10 | $14 | $24 |
| 10 | $20 | $29 | $46.50 |
| 25 | $50 | $74 | $114 |

### Business

- **$29 a month per account**, hosted or self-hosted. Yearly: $290 a year.
- On Cloud, Business includes 10 servers; each additional server is $2 a month ($20 a year).
- Self-hosted, Business is a licence for one control plane, with any number of servers.
- Everything in Cloud, plus the Business module:
  - SAML single sign-on, and SSO enforcement that makes the identity provider the only sign-in for an organisation's members (the owner keeps a break-glass `server login-link` on self-hosted, `docs/specs/auth.md`).
  - SCIM provisioning of members and their roles.
  - Audit log export: every audit event to an S3-compatible bucket on a schedule, or streamed to a webhook or a SIEM, with the hash chain intact so the export can be verified with `moonglass audit verify`.
  - Priority support: a reply within one business day, and a shared chat channel.
- A 14-day trial with no card, on Cloud or with a trial licence.

### Founding members

- The first 1,000 Cloud accounts and the first 100 Business accounts are founding members.
- Founding price, for as long as the subscription stays active: **Cloud $3 a month with 2 servers and $1.50 per additional server; Business $19 a month**, with the same included servers. Yearly is ten months' price.
- A founding price survives a plan change between Cloud and Business, and ends only when the subscription is cancelled and not renewed within 30 days.
- The pricing page shows how many founding places are left.

## What the dashboard shows

- **Free:** nothing about plans. No banner, no upsell, no locked menu item. Settings → About shows the version and a link to the pricing page.
- **Cloud and Business:** Settings → Billing shows the plan, the founding price if any, the servers counted, the next invoice and its date, the card, invoices, and the change and cancel buttons (`docs/specs/billing.md`).
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
