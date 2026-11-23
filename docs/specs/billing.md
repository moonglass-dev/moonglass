# Billing

This is the behaviour spec for how Cloud and Business accounts are billed. Prices and plans are `docs/specs/pricing.md`. The code lives in `moonglass-dev/cloud` and `moonglass-dev/business` (W7-4); this repository holds the spec, so the open core, the dashboard and the pricing page agree with it. Nothing here runs in the open build, and no plan state ever reaches an agent.

## Accounts

- **One account is one control plane.** On Cloud it is one `moonglass server` instance in managed instance mode (V1-2) with its own SQLite file (ADR-0002). Self-hosted Business is one licence for one control plane.
- An account holds any number of organisations, people and servers. Its owner is the person who signed it up; owners of the control plane may manage billing.
- **Sign-up** asks for a GitHub or Google sign-in and an account name, and nothing else. It creates the instance, makes the person its first owner (the bootstrap of ADR-0005 decision 2, done by the Cloud service instead of a printed token), and opens the dashboard's first-run flow (`docs/specs/ui.md`, "First run").
- **One free Cloud server per GitHub account.** A GitHub account cannot open a second Cloud control plane on the free first server. Unlimited organisations live on that one account. A Google-only signup gets one free first server tied to that Google identity; linking GitHub later does not add a second free server.
- Cloud opens to the waitlist in waves, founding members first (W8-I).

## Payment

- **Stripe** takes payment, holds the card and sends receipts, through Stripe Checkout and the Stripe customer portal. Moonglass never sees a card number. Stripe Tax collects sales tax and VAT where it is due.
- **Monthly or yearly**, chosen at checkout and changeable at any time from Settings → Billing. Yearly is ten months' price, charged up front.
- **A trial** is 14 days with no card, and applies to paid servers and to Business. The free first Cloud server is not a trial and does not need a card. Three days before a trial ends, the Inbox and an email (when the account has one) say so. At the end it asks for a card if anything paid remains. A trial of paid servers or Business that ends without a card lapses (below). An account that is back to one free server and no Business stays on Cloud at $0.

## The server count

- The count is the number of servers enrolled in the control plane and not removed, read from the control plane's own `servers` table. An offline server still counts until it is removed.
- On Cloud, the first enrolled server is free. Each server after that is a paid server at the Cloud extra-server price. Self-hosted Business has no server charges.
- **Before a server enrols,** the enrolment dialog shows the price with it ("This makes 3 servers: $10.00 a month from today"). Enrolment tokens on Cloud are only issued after that confirmation. The first server says it is free ("This is your free server: $0.00 a month").
- **Adding** a paid server prorates the difference to the end of the period and charges it on the next invoice. **Removing** one credits the unused part of the period on the next invoice.
- The free first Cloud server is never refunded as credit when the count is zero.
- The paid-server count is sent to Stripe as the subscription quantity whenever it changes, and checked against the control plane once a day. A difference is fixed in the customer's favour and logged for us to investigate.

## Invoices

- Stripe sends every invoice by email and keeps them in the customer portal; Settings → Billing lists them with links.
- Business accounts may pay by invoice with 30-day terms, and may get a purchase order or a yearly contract at the listed price.

## Idle pause

A free Cloud control plane (one server, no paid servers, no Business) with no login for 30 days is paused.

- A login is any successful dashboard sign-in by a member of the account.
- Three days before the pause, the Inbox and an email (when the account has one) say the control plane will pause, and that signing in keeps it running.
- While paused, the Cloud service stops the control plane process. Apps on the user's server keep running. The dashboard, deploys and enrolment are unavailable until the next login.
- The next login starts the control plane again. The dashboard, deploys and enrolment work. The Inbox records that the control plane resumed after an idle pause.
- There is no app cap on a free Cloud account, paused or not.
- Idle pause is not a lapse. A paid server or a Business subscription means the account is never paused for idle.
- No plan state reaches the agent. A paused control plane cannot send it commands; the agent keeps running every app it already has.

## Lapse

A subscription lapses when a payment fails and is not fixed within 14 days, or when a trial of paid servers or Business ends without a card.

| Time after the lapse | Cloud | Business licence |
|---|---|---|
| 0 to 30 days | Everything works. A banner in the dashboard and an Inbox item say how to fix the payment | Everything works. The same banner |
| 30 to 90 days | The control plane stays up. New servers, projects and members cannot be added. Deploys, rollbacks, backups, restores, logs and export keep working | Business settings freeze: existing SSO connections keep signing people in, SCIM and audit export stop, and settings cannot be edited until renewal |
| After 90 days | We email the owner a final export (`moonglass export`, W7-2), then delete the instance and its backups after 14 more days. The agents keep running every app; they reconnect when the export is imported elsewhere | No further change |

- A lapse never stops a running app, never removes a guardrail, never locks out an owner, and never blocks a rollback, a restore or an export.
- Paying at any point restores everything at once, with no data lost before deletion.

## Canceling

- Cancel from Settings → Billing. The plan runs to the end of the paid period, then follows the lapse table, and the dashboard offers an export first.
- Yearly plans canceled within 30 days of a charge are refunded in full.
- Canceling a founding subscription ends the founding discount. A later signup pays list price.

## Licences for self-hosted Business

- A licence is a file signed with our Ed25519 licence key. It names the account, the expiry and the plan. The Business module checks it offline, with no phone-home, so it works air-gapped.
- A licence renews every year and is sent by email and from the customer portal a month before expiry.
- The open build never reads a licence (V§6).

## What the control plane records

- A Cloud instance stores its plan, its founding flag, its founding start and end dates, its free-server allotment, the paid-server count and the date of its next invoice in its own database, written only by the Cloud service through the extension points of V1-1. The open core reads none of it.
- Billing events (a new plan, a failed payment, a lapse, a renewal, an idle pause, a resume) are written to the account's audit trail as `system` events, so the Activity view shows them.
