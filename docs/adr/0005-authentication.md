# ADR-0005: Authentication

- **Status:** Accepted
- **Date:** 2026-11-16

## Context

Moonglass needs sign-in with no passwords, no open registration window on a self-hosted box, and an out-of-band human step before anything that matters (S§3.10). Other products in the category have shipped a pre-authentication admin takeover and a first-admin registration window (S§3.10). Teams of several people share a control plane, so members join by invitation, with any sign-in method they have.

## Decision

1. **Sign-in methods, all free:** GitHub (W4-1f) and Google (W4-1b) through OAuth apps the operator configures; any OpenID Connect provider the operator configures (V1-7); passkeys and TOTP (V1-6). There are no passwords anywhere, and email is never a sign-in method. SAML and SCIM are in the Business module (decision 11).
2. **The first owner: a bootstrap token, then a linked identity.**
   - On a self-hosted box, the first owner is created with a one-time bootstrap token printed in the terminal, and `moonglass server --print-owner-link` links their first identity. There is never an open registration window.
   - On Cloud, the Cloud service creates the first owner at sign-up, from the identity they signed up with (`docs/specs/billing.md`, "Accounts").
   - Day-to-day sign-in is any linked method. The GitHub OAuth app used for sign-in is not the GitHub App of W4-10.
   - `moonglass server login-link` prints a one-time link on the box for emergencies: the provider is down, or no OAuth app is set up yet. Access to the server is the credential.
3. **Step-up.** Separate things each require a sign-in fresher than ten minutes, checked on the server at the moment of approval: changing secret recipients, revealing a secret, minting a token, a purge, membership and protection changes, and approving a deploy or a destructive call that waits for approval (ADR-0006 decision 4). The fresh sign-in is a new round trip with any linked method, a passkey or TOTP once V1-6 ships, or a fresh `moonglass server login-link`, so no one provider is ever required for a step-up.
   - **A step-up approves one request.** It is a fresh round trip, then an Approve press on a page that shows the request. It is single use and lasts ten minutes. It is bound to the user, the credential that asked, the operation, its target and a digest of its inputs. A fresh sign-in alone never unlocks other calls. An OAuth provider cannot force a password to be typed again, so the Approve press on a page that states the request is what shows a person is present (`docs/specs/auth.md`).
4. **Lifetimes.** All one-time tokens fail on replay.

   | Credential | Lifetime |
   |---|---|
   | Session | 14 days from sign-in, absolute; revocable at any time |
   | Step-up freshness | 10 minutes (S§3.10) |
   | Step-up | 10 minutes from the request, one use |
   | Identity link (`identities.link`, `--print-owner-link`) | 10 minutes, one use |
   | Invitation | 7 days, one use |
   | `server login-link` | 10 minutes, one use |
   | Enrolment token | 1 hour, one use |
   | Bootstrap token | 1 hour, one use. Restarting `moonglass server` before the first owner exists prints a new one and voids the old |

5. **Email goes through the operator's SMTP server** (LN-5): `MOONGLASS_SMTP_*`, STARTTLS required, sent with the standard library's `net/smtp`, so there is no email dependency. With no SMTP set, nothing is sent: invitations are links the inviter copies, and notifications use the other channels. On Cloud, the Cloud service sets the SMTP server. Provider credentials, OAuth client secrets included, live only in the operator's environment (`MOONGLASS_*`), never in the database or the repository.
6. **One person, several sign-in methods.** Two identities with the same email address are not automatically one user. Linking is an explicit action by the user, with step-up. The one exception is the first identity of the first owner, which `moonglass server --print-owner-link` links on the box, without step-up (`docs/specs/auth.md`).
7. **Sessions** are server-side, listable and revocable, bound to `__Host-` cookies.
8. **Tokens** are opaque random values stored hashed. Each has scope rows (project, environment, operation; ADR-0006), an expiry and an owner. Every token belongs to a human or a named agent identity, and the audit trail records which.
   - **One format for every bearer secret** (API and agent tokens, enrolment, bootstrap, login links, identity links, invitations, step-up confirmations, session IDs): 256 bits from `crypto/rand`, written as `mg_` plus 52 characters of lowercase unpadded base32.
   - Stored only as SHA-256 of the full text, in the lookup tables (ADR-0002 decision 9). 256 random bits cannot be guessed, so a slow password hash would only cost CPU.
   - Shown once, at creation; afterwards only the name, the prefix and the last four characters.
   - One-time tokens are marked used in the transaction that accepts them, so a replay fails (B W1-3).
9. **Members join by invitation** (LN-3). An owner creates an invitation for a role; it is a one-time link, emailed when SMTP is set. The invitee signs in with any method, and that identity is linked to the new membership. There is no open sign-up on a control plane.
10. **Passkeys and TOTP** (V1-6) are available to every person, free. Either completes a step-up, and agent-verified approvals use passkey signatures (V1-9).
11. **The Business module** adds SAML single sign-on, SSO enforcement and SCIM through the licensed build's extension points (V1-1, V1-5). With SSO enforced, the identity provider is the only sign-in for an organisation's members, and the owner keeps `server login-link` on a self-hosted box.

## Consequences

- A GitHub sign-in is usually two-factor, because GitHub requires 2FA for most active developer accounts; a Google sign-in is when the account has 2-Step Verification on. Until V1-6, a second factor depends on the provider (S§2).
- Reaching the first sign-in needs no SMTP and no OAuth app: the owner link and the login link are printed on the box.
- A step-up page in the person's browser is out of band from the coding agent, which is the point of step-up (S§3.10).
- `internal/auth` is trusted core (B§1). W4-1a, W4-1d, W4-1e, W4-1f, W4-1c and W4-1 build GitHub sign-in and the step-up primitive; W7-1 builds the bootstrap token and the login link; W4-1b, LN-3, V1-6 and V1-7 add Google, invitations, passkeys and TOTP, and OpenID Connect.

## Alternatives rejected

- **Passwords:** none anywhere (S§3.10).
- **Email links as a sign-in method:** whoever reads the inbox signs in, and every self-hosted box would need SMTP before its first sign-in.
- **An open first-admin registration window on self-hosted:** the takeover described in the context.
- **One provider as the only step-up:** it would make that provider a requirement for approvals, so any linked method or a fresh login link completes a step-up.
- **The login link as the everyday self-hosted sign-in:** every sign-in and every step-up would need a shell on the box, which contradicts the claim that day-to-day work needs no SSH (V§1).
- **Linking accounts by matching email:** whoever controls an inbox would take over another sign-in.
- **SAML in the open core:** it is the enterprise feature Business sells, and every sign-in a person or a small team needs is free (V§5.1).
- **Capability tokens with offline verification:** elegant, but they add a policy language users must learn (S§3.10).
- **A slow password hash for tokens:** no benefit at 256 bits of entropy.
- **A different prefix per token kind:** more formats to parse and test.

## Links

- S§2, S§3.10, S§5.2, S§9; V§4, V§5.1
- B§3: W2-1, W4-1, W4-1b, W5-1, W5-2, W7-1, LN-3, LN-5, V1-6, V1-7, V1-5
- `docs/threat-model.md` §2, checklist item 4; ADR-0006 (scopes)
