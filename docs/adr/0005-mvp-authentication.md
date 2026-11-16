# ADR-0005: MVP authentication

- **Status:** Accepted
- **Date:** 2026-11-16

## Context

The eight weeks were too full, so passkeys and TOTP moved to v1 (S revision 3, M intro). The MVP still needs sign-in that has no passwords, no open registration window on a self-hosted box, and an out-of-band human step before anything that matters (S§3.10). Other products in the category have shipped a pre-authentication admin takeover and a first-admin registration window (S§3.10).

## Decision

1. **Hosted: GitHub sign-in and email magic links.** No passwords.
2. **Self-hosted: a bootstrap token, then login links.**
   - The first admin is created with a one-time bootstrap token printed in the terminal. There is never an open registration window.
   - Day-to-day sign-in uses `moonglass server login-link`, which prints a one-time link on the box. Access to the server is the credential.
   - GitHub sign-in reuses the GitHub App created for git sources.
   - There is no email sign-in on self-hosted in the MVP, so there is no SMTP to configure.
3. **Step-up.** Three separate things each require a sign-in fresher than ten minutes, checked on the server at the moment of approval: approving a protected deploy, approving a destructive operation, and changing secret recipients (ADR-0006 decision 4). `secrets reveal` requires it too (ADR-0003). On hosted, the fresh sign-in is a new GitHub round trip or a new email link. On self-hosted it is a fresh `moonglass server login-link`: access to the server stays the credential, and GitHub is never required for an approval.
4. **Lifetimes.** All one-time tokens fail on replay.

   | Credential | Lifetime |
   |---|---|
   | Session | 14 days from sign-in, absolute; revocable at any time |
   | Step-up freshness | 10 minutes (S§3.10) |
   | Email magic link | 15 minutes, one use |
   | `server login-link` | 10 minutes, one use |
   | Enrolment token | 1 hour, one use |
   | Bootstrap token | 1 hour, one use. Restarting `moonglass server` before the first admin exists prints a new one and voids the old |

5. **Email delivery on hosted** uses one transactional sender. Its credentials live only in the operator's environment, never in the database or the repository. No provider SDK without its own ADR. The provider is not yet chosen (Open questions).
6. **One person, two sign-in methods.** A GitHub sign-in and an email link with the same address are not automatically one user. Linking is an explicit action by the signed-in user, with step-up. An email link never attaches itself to an existing GitHub user.
7. **Sessions** are server-side, listable and revocable, bound to `__Host-` cookies.
8. **Tokens** are opaque random values stored hashed. Each has scope rows (project, environment, operation; ADR-0006), an expiry and an owner. Every token belongs to a human or a named agent identity, and the audit trail records which.
   - **One format for every bearer secret** (API and agent tokens, enrolment, bootstrap, login and magic links, session IDs): 256 bits from `crypto/rand`, written as `mg_` plus 52 characters of lowercase unpadded base32.
   - Stored only as SHA-256 of the full text, in the lookup tables (ADR-0002). 256 random bits cannot be guessed, so a slow password hash would only cost CPU.
   - Shown once, at creation; afterwards only the name, the prefix and the last four characters.
   - One-time tokens are marked used in the transaction that accepts them, so a replay fails (B W1-3).
9. **v1:** passkeys and TOTP for everyone, free; then generic OIDC sign-in. Passkeys and TOTP land before Team, so approvals can require a real second factor (M§4).
10. **Commercial module:** SAML and SCIM (V§5.2).

## Consequences

- A GitHub sign-in is usually two-factor, because GitHub requires 2FA for most active developer accounts. An email link is single-factor until v1. MFA depends on GitHub until then (S§2).
- An approval link sent to an inbox is out of band from the coding agent, which is the point of step-up (S§3.10).
- Self-hosted needs no SMTP and no OAuth app to reach a first sign-in.
- `internal/auth` is trusted core (B§3). W4-1 builds hosted sign-in and the step-up primitive; W7-1 builds the self-hosted path.

## Alternatives rejected

- **Passwords:** none anywhere (S§3.10).
- **An open first-admin registration window on self-hosted:** the takeover described in the context.
- **Passkeys and TOTP in the MVP:** moved to v1 to fit eight weeks (S revision 3).
- **GitHub as the self-hosted step-up:** it would make the GitHub App a requirement for approvals.
- **Linking accounts by matching email:** whoever controls an inbox would take over a GitHub user.
- **Capability tokens with offline verification:** elegant, but they add a policy language users must learn (S§3.10).
- **A slow password hash for tokens:** no benefit at 256 bits of entropy.
- **A different prefix per token kind:** more formats to parse and test.

## Open questions

1. **Which email provider sends hosted mail.** An owner decision, with an account. **W4-1 is blocked until it is chosen**: magic links and step-up on hosted need it. Requirement: decision 5.

## Links

- S§2, S§3.10, S§5.2, S§9; V§4 ("no paywall on safety"), V§5.2, V§5.3; M§4
- B§6: W2-1, W4-1, W5-1, W5-2, W7-1
- `docs/threat-model.md` §2, checklist item 4; ADR-0006 (scopes)
