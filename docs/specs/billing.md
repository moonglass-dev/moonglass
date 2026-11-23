# Billing

This is the behaviour spec for how Cloud and Business accounts are billed. Prices and plans are `docs/specs/pricing.md`. The code lives in `moonglass-dev/cloud` and `moonglass-dev/business` (W7-4); this repository holds the spec, so the open core, the dashboard and the pricing page agree with it. Nothing here runs in the open build, and no plan state ever reaches an agent.

## Accounts

- **One account is one control plane.** On Cloud it is one `moonglass server` instance in managed instance mode (V1-2) with its own SQLite file (ADR-0002). Self-hosted Business is one licence for one control plane.
- An account holds any number of organisations, people and servers. Its owner is the person who signed it up; owners of the control plane may manage billing.
- **Sign-up** asks for a GitHub or Google sign-in and an account name, and nothing else. It creates the instance, makes the person its first owner (the bootstrap of ADR-0005 decision 2, done by the Cloud service instead of a printed token), and opens the dashboard's first-run flow (`docs/specs/ui.md`, "First run").
- Cloud opens to the waitlist in waves, founding members first (W8-I).

## Payment

- **Stripe** takes payment, holds the card and sends receipts, through Stripe Checkout and the Stripe customer portal. Moonglass never sees a card number. Stripe Tax collects sales tax and VAT where it is due.
- **Monthly or yearly**, chosen at checkout and changeable at any time from Settings → Billing. Yearly is ten months' price, charged up front.
- **A trial** is 14 days with no card. Three days before it ends, the Inbox and an email (when the account has one) say so. At the end it asks for a card. A trial without a card lapses (below).

## The server count

- The count is the number of servers enrolled in the control plane and not removed, read from the control plane's own `servers` table. An offline server still counts until it is removed.
- **Before a server enrols,** the enrolment dialog shows the price with it ("This makes 3 servers: $6.00 a month from today"). Enrolment tokens on Cloud are only issued after that confirmation.
- **Adding** a server prorates the difference to the end of the period and charges it on the next invoice. **Removing** one credits the unused part of the period on the next invoice.
- Included servers (2 on Cloud, 10 on Business on Cloud) are never refunded when the count is lower.
- The count is sent to Stripe as the subscription quantity whenever it changes, and checked against the control plane once a day. A difference is fixed in the customer's favour and logged for us to investigate.

## Invoices

- Stripe sends every invoice by email and keeps them in the customer portal; Settings → Billing lists them with links.
- Business accounts may pay by invoice with 30-day terms, and may get a purchase order or a yearly contract at the listed price.

## Lapse

A subscription lapses when a payment fails and is not fixed within 14 days, or when a trial ends without a card.

| Time after the lapse | Cloud | Business licence |
|---|---|---|
| 0 to 30 days | Everything works. A banner in the dashboard and an Inbox item say how to fix the payment | Everything works. The same banner |
| 30 to 90 days | The control plane stays up. New servers, projects and members cannot be added. Deploys, rollbacks, backups, restores, logs and export keep working | Business settings freeze: existing SSO connections keep signing people in, SCIM and audit export stop, and settings cannot be edited until renewal |
| After 90 days | We email the owner a final export (`moonglass export`, W7-2), then delete the instance and its backups after 14 more days. The agents keep running every app; they reconnect when the export is imported elsewhere | No further change |

- A lapse never stops a running app, never removes a guardrail, never locks out an owner, and never blocks a rollback, a restore or an export.
- Paying at any point restores everything at once, with no data lost before deletion.

## Cancelling

- Cancel from Settings → Billing. The plan runs to the end of the paid period, then follows the lapse table, and the dashboard offers an export first.
- Yearly plans cancelled within 30 days of a charge are refunded in full.

## Licences for self-hosted Business

- A licence is a file signed with our Ed25519 licence key. It names the account, the expiry and the plan. The Business module checks it offline, with no phone-home, so it works air-gapped.
- A licence renews every year and is sent by email and from the customer portal a month before expiry.
- The open build never reads a licence (V§6).

## What the control plane records

- A Cloud instance stores its plan, its founding flag, its included servers and the date of its next invoice in its own database, written only by the Cloud service through the extension points of V1-1. The open core reads none of it.
- Billing events (a new plan, a failed payment, a lapse, a renewal) are written to the account's audit trail as `system` events, so the Activity view shows them.
