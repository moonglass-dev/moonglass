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
3. **Step-up.** Approving a protected deploy, a destructive operation or a change to secret recipients requires a sign-in fresher than ten minutes: a new GitHub round trip or a new email link.
4. **Sessions** are server-side, listable and revocable, bound to `__Host-` cookies.
5. **Tokens** are opaque random values stored hashed. Each has rows naming project, environment and allowed operations, an expiry and an owner. Every token belongs to a human or a named agent identity, and the audit trail records which.
6. **v1:** passkeys and TOTP for everyone, free; then generic OIDC sign-in. Passkeys and TOTP land before Team, so approvals can require a real second factor (M§4).
7. **Commercial module:** SAML and SCIM (V§5.2).

## Consequences

- A GitHub sign-in is usually two-factor, because GitHub requires 2FA for most active developer accounts. An email link is single-factor until v1. MFA depends on GitHub until then (S§2).
- An approval link sent to an inbox is out of band from the coding agent, which is the point of step-up (S§3.10).
- Self-hosted needs no SMTP and no OAuth app to reach a first sign-in.
- `internal/auth` is trusted core (B§3). W4-1 builds hosted sign-in and the step-up primitive; W7-1 builds the self-hosted path.

## Alternatives rejected

- **Passwords:** none anywhere (S§3.10).
- **An open first-admin registration window on self-hosted:** the takeover described in the context.
- **Passkeys and TOTP in the MVP:** moved to v1 to fit eight weeks (S revision 3).
- **Capability tokens with offline verification:** elegant, but they add a policy language users must learn (S§3.10).

## Open questions

1. **Step-up on self-hosted.** The plan defines step-up as a new GitHub round trip or a new email link, and self-hosted has no email link. Options: (a) a fresh `moonglass server login-link`, which needs shell access to the box at approval time; (b) a GitHub round trip, which makes the GitHub App a requirement for approvals on self-hosted. Decide before W7-1.
2. **Lifetimes.** The plan gives ten minutes for step-up freshness and nothing else. W4-1 needs the session lifetime and the expiry of magic links, login links and the bootstrap token.
3. **Email delivery for hosted.** Magic links and email notifications need a sender, and the plan names none. V§5.6 lists email deliverability as a real cost.
4. **Token hashing and format.** The plan says "stored hashed" and does not name the function, the token length or a recognisable prefix. W2-1 needs them for owner tokens.
5. **One person, two sign-in methods.** The plan does not say whether a GitHub sign-in and an email link with the same address are the same user.

## Links

- S§2, S§3.10, S§5.2, S§9; V§4 ("no paywall on safety"), V§5.2, V§5.3; M§4
- B§6: W2-1, W4-1, W5-1, W5-2, W7-1
- `docs/threat-model.md` §2, checklist item 4; ADR-0006 (scopes)
