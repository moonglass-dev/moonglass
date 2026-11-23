# Sign-in, sessions and step-up

What W4-1a, W4-1d, W4-1e, W4-1f, W4-1b, W4-1c and W4-1 build, per ADR-0005 as amended on 2026-11-18. The owner approved this spec on 2026-11-18, including the registry operations, the tables and the step-up rules below, which are contracts (ADR-0006 decision 9, ADR-0002). The self-hosted path (bootstrap token, `server login-link`) is W7-1.

**At launch** (ADR-0005 as amended by the plan audit, `docs/review/plan-audit.md`, 2026-11-22): sign-in is GitHub on the user's own box, with `server login-link` for the first sign-in and emergencies. Google (W4-1b) is v1. Until it lands, every place below that names Google shows or accepts GitHub only, and the Google variables are not read.

## Which ticket builds what

| Ticket | Builds |
|---|---|
| W4-1a | The tables: `session_hashes`, and the two new `audit_events` columns, on both backends with conformance |
| W4-1d | The tables: `link_token_hashes` and `step_ups`, on both backends with conformance |
| W4-1e | Session authentication in the registry: `Registry.Authenticate` with the `__Host-mg_session` cookie and `Moonglass-Org`, or a bearer, never both; `Principal.SessionID`; the audit hash and event with `session_id` and `step_up_id`; `http.CrossOriginProtection` on every route of the HTTP listener |
| W4-1f | `internal/auth` with GitHub: the operator variables with the pairing and public-URL rules, `state`, PKCE and the `__Host-mg_oauth` cookie, the GitHub exchange and `/user`, sign-in sessions, sign-out, the failure page and its CSP, the routes on `registry.NonOperationRoutes` mounted under `/auth`, and the read-only `Store.UserByIdentity` |
| W4-1b | Google: the provider and its `nonce`, the ID-token `iss`, `aud`, `exp` and `nonce` checks, and the Google variables |
| W4-1c | Step-up: the `StepUp` flag, the `step_up_required` answer, the step-up page, the CLI's `--step-up` |
| W4-1 | `sessions.list`, `sessions.revoke`, `identities.list`, `identities.link`, the link landing, and `moonglass server --print-owner-link` |

## Decisions in short

- **Sign-in is GitHub** (Google in v1). There are no passwords and no email links in the MVP, so there is no email provider (ADR-0005 decisions 1 and 5).
- **No sign-up in W4-1.** An identity that is not linked to a user is refused. A user gets their first identity from `moonglass server --print-owner-link` on the box, or from `identities.link`. There is no open sign-up (ADR-0005 open question 2, closed 2026-11-22); members are added through the commercial module.
- **A step-up approves one exact request.** It is not a "recently signed in" flag on the user. The person does a fresh provider round trip, then presses Approve on a page that shows the request. The approval is single use, lasts 10 minutes, and is bound to one user, one credential, one operation, one target and one digest of the inputs.
- **The control plane keeps no provider token.** The provider's access token is used once to learn the subject and then dropped.
- **Standard library only.** OAuth 2.0 with PKCE over `net/http`, and `http.CrossOriginProtection` for cross-site requests. Nothing here needs a new module.

## Operator configuration

Read from the environment at `moonglass server` start. Secret values are never logged.

| Variable | Meaning |
|---|---|
| `MOONGLASS_PUBLIC_URL` | The origin people reach the control plane at, such as `https://app.moonglass.dev`. It must be `https`, except for host `localhost` or `127.0.0.1`. It has no path. Redirect URIs and links are built from it |
| `MOONGLASS_GITHUB_CLIENT_ID`, `MOONGLASS_GITHUB_CLIENT_SECRET` | A GitHub OAuth App. Its callback URL is `<public>/auth/github/callback` |
| `MOONGLASS_GOOGLE_CLIENT_ID`, `MOONGLASS_GOOGLE_CLIENT_SECRET` | A Google OAuth client of type "Web application". Its redirect URI is `<public>/auth/google/callback` |

- A provider is on when both of its variables are set. When only one of the two is set, the server refuses to start and names the missing variable.
- When any provider is on and `MOONGLASS_PUBLIC_URL` is unset or invalid, the server refuses to start and says why.
- With no provider on, the sign-in routes answer 404 `sign-in is not configured on this server`, and step-up cannot complete.
- The provider endpoints are fixed in code. Tests point a provider at an `httptest` server through its constructor, not through the environment.

## Identities

A person is a `user_id` (`docs/specs/storage.md`). An identity is one `user_identities` row:

- **GitHub:** `github:<id>`, where `id` is the numeric `id` from `GET https://api.github.com/user`. It is never the login, because a login can be renamed.
- **Google:** `google:<sub>`, from the ID token. It is never the email address, because Google says an account's address can change.

Nothing else about the person is stored: no email, name or avatar. Display names are a later decision (W2-S, W4-6).

Two identities with the same email address are never joined automatically. Linking is always explicit (ADR-0005 decision 6).

## The provider round trip

One flow serves sign-in, step-up and linking. Its `purpose` says which.

1. **Start.** `GET /auth/{provider}/start` with `purpose` set to `sign-in`, `step-up` or `link`, and the purpose's argument:
   - `return`, a path for sign-in;
   - `org` and `id` for step-up;
   - `link`, the link token's row ID, for linking.

   The server makes `state` (32 random bytes, base64url), a PKCE `code_verifier` (32 random bytes, base64url) and, for Google, a `nonce`. It sets the cookie `__Host-mg_oauth` (`HttpOnly`, `Secure`, `SameSite=Lax`, `Path=/`, `Max-Age=600`) to those values plus the purpose and its argument. Then it redirects to the provider:
   - **GitHub:** `https://github.com/login/oauth/authorize` with `client_id`, `redirect_uri`, `state`, `code_challenge` (S256), `code_challenge_method=S256` and no `scope`.
   - **Google:** `https://accounts.google.com/o/oauth2/v2/auth` with `client_id`, `redirect_uri`, `response_type=code`, `scope=openid email`, `state`, `nonce`, `code_challenge` (S256) and `code_challenge_method=S256`.
2. **Callback.** `GET /auth/{provider}/callback`. The server requires the cookie, compares `state` to the cookie in constant time, and clears the cookie whatever the result. It then exchanges `code` with `code_verifier` and the client secret:
   - **GitHub:** `POST https://github.com/login/oauth/access_token` with `Accept: application/json`, then `GET https://api.github.com/user` with the access token.
   - **Google:** `POST https://oauth2.googleapis.com/token`. The ID token comes directly from Google over TLS, so its signature is not checked (OpenID Connect Core 3.1.3.7). Its claims are checked: `iss` is `https://accounts.google.com` or `accounts.google.com`, `aud` is the client ID, `exp` is in the future, and `nonce` equals the cookie's.

   The access token is not stored. Any failure shows a page with one plain sentence and a link back to `/sign-in`, and never shows the provider's error text. A missing or mismatched `state` is a failure.
3. **Purpose.** The identity string goes to the purpose's handler, described below.

`return` must be a path that starts with `/` and not with `//` or `/\`. Anything else becomes `/`.

## Sessions

**Sign-in** (`purpose=sign-in`):
- The identity is looked up with `Store.UserByIdentity` (W4-1f, owner decision of 2026-11-19). It reads `user_identities` and returns the `user_id`, or `ErrNotFound` for an identity linked to no user, and never writes. `Store.UserForIdentity`, which mints a user for an unseen identity, is never called on a sign-in. `UserByIdentity` adds no table or column, so the storage schema does not change. W4-1f names `TestUserByIdentityConformanceSQLite` and `TestUserByIdentityConformancePostgres` (a linked identity returns its user; an unknown one is `ErrNotFound` and leaves `user_identities` and `user_orgs` unchanged).
- An identity linked to a user gets a session, and the browser is redirected to `return`.
- An unknown identity is refused with: "This GitHub account is not linked to a Moonglass user. Ask an owner of your organisation to link it." The message names Google for a Google account.

**A session** is an `mg_` bearer (ADR-0005 decision 8):
- It is stored only as SHA-256 in `session_hashes`, which maps it to the session ID and the `user_id`.
- It expires 14 days after sign-in, absolute. It is never extended.
- It is sent as the cookie `__Host-mg_session` (`HttpOnly`, `Secure`, `SameSite=Lax`, `Path=/`, `Max-Age` up to the expiry). Page script can never read it (`docs/specs/ui.md`).

**Authentication.** A request authenticates with a bearer token or a session cookie, never both. A request with both is answered 401 `send a bearer token or a session cookie, not both`. `Registry.Authenticate(ctx, bearer)` authenticates a bearer, as it does today, and `Registry.AuthenticateSession(ctx, session, orgID)` (W4-1e) a session; `internal/api` reads the `Authorization` header, the `__Host-mg_session` cookie and `Moonglass-Org`, applies the rules below, and calls exactly one of them.
- A session request must carry `Moonglass-Org: <org id>`, naming an org the user is a member of. Otherwise it gets 401.
- The principal has the session ID, the user's role in that org, and the single scope row `* * *`.
- A bearer token's org comes from the token. A `Moonglass-Org` header that differs from it gets 401.
- A malformed, unknown, expired or revoked session gets 401, like a token.

**Cross-origin requests.** Every route on the control plane's HTTP listener goes through `http.CrossOriginProtection`. A cross-site browser request with an unsafe method is refused with 403 before it reaches any handler. The CLI sends no `Origin` or `Sec-Fetch-Site`, so it is unaffected. With `SameSite=Lax` and this check, a session cookie never authorises a cross-site write.

**Sign-out.** `POST /auth/sign-out` revokes the current session, clears the cookie and redirects to `/sign-in`. It is the only way to end a session without an operation.

## Step-up

ADR-0005 decision 3 as amended: a step-up approves one request. It is checked in `registry.Invoke`, after role and scope and before the handler, never in a handler or middleware.

**The flag.** `Spec` gains `StepUp`, with the values `registry.Yes` or `registry.No`. Like `Destructive` it has no default, and `Register` rejects `Unset`. W4-1c ships no operation with `StepUp: Yes`. Its tests register one in a test registry. W4-1's `identities.link` is the first real one, followed by W4-8, W4-9 and W3-3.

**Asking.** A call to a `StepUp` operation without a step-up header:
- **From an agent identity** (`Principal.Agent`): refused with 403 `this operation needs a person to approve it with step-up`. No step-up is created. Approving an agent's request is W5-2.
- **From a person:** the registry creates a `step_ups` row. The row holds the org, the user, the credential (`token_id` or `session_id`), the operation, the target IDs, the input digest, and an expiry 10 minutes ahead. The answer is 403 with:

  ```json
  {"error": "step_up_required", "step_up_id": "<id>", "step_up_url": "<public>/step-up/<org>/<id>", "code": "ABCD-1234"}
  ```

  - The input digest is SHA-256 of the operation's input encoded with `encoding/json`.
  - `code` is the digest's first 8 hex characters, upper case, split by a hyphen.
  - A user may have at most 10 unexpired step-ups that are not yet used. An 11th is refused with 429.

**Approving.** `GET /step-up/{org}/{id}` shows one button per provider the user has linked: "Continue with GitHub" or "Continue with Google". It shows nothing about the request yet.
- The round trip runs with `purpose=step-up`.
- On the callback, the identity must belong to the step-up's user. If it doesn't, the page says "This step-up belongs to a different user" and records nothing.
- When it matches, the page shows the operation's summary, the project and environment names, and the code. It has two buttons, Approve and Decline, in a form that carries a one-time confirmation value. That value is an `mg_` bearer whose SHA-256 is stored on the row.
- `POST /step-up/{org}/{id}` with that value sets `approved_at` or `declined_at`. It is accepted only before the step-up expires, only once, and only from the same origin (cross-origin protection).
- The page then says: "Approved. Run the same command again with `--step-up <id>`, or go back to the page that asked."

**Using.** The caller sends the identical request again with `Moonglass-Step-Up: <id>`. `Invoke` accepts it only when all of these hold:
- the row is in the caller's org and belongs to the same user and the same credential;
- the operation, target and input digest are the same;
- it is approved, not declined, not used and not expired.

In one transaction it marks the row used and writes the audit event with `step_up_id`, then runs the handler. Any mismatch is 403 `step-up <id> does not match this request`. A used, declined or expired step-up says which of the three it is.

**What it proves.** GitHub can only force the account picker (`prompt=select_account`), and Google can only use `none`, `consent` or `select_account`. Neither can force the person to type a password again. So a fresh round trip proves the browser holds a live provider session for that user. The person present is shown by the Approve press on a page that states the exact request. A coding agent that holds only a token, or that can open a URL, cannot press it. A second factor at approval time is v1, with passkeys (ADR-0005 decision 9).

**CLI.** The generated runner gains a global `--step-up ID` flag, sent as `Moonglass-Step-Up`. On `step_up_required` it prints to stderr: "This needs step-up. Open <url>, check the code <code>, approve, then run the same command again with --step-up <id>." It exits 1. It does not wait or poll.

**Self-hosted** (W7-1) adds a fresh `server login-link` as one more way to complete the round trip on the step-up page. Nothing else changes.

## Linking

**`identities.link`** (`StepUp: Yes`) creates a link token for the caller's own user and returns `link_url` once.
- The token is an `mg_` bearer stored as SHA-256 in `link_token_hashes`, valid for 10 minutes and single use.
- `link_url` is `<public>/link/<token>`.

**`GET /link/{token}`** shows "Continue with GitHub" and "Continue with Google". The round trip runs with `purpose=link`. On the callback, in one transaction:
- the token must be unexpired and unused;
- the identity must not belong to another user;
- the server writes the `user_identities` row and marks the token used.

If the identity already belongs to this user, the page says so and the token is marked used. If it belongs to another user, the page says it is linked to a different Moonglass user and nothing changes.

**`moonglass server --print-owner-link`** gives the first owner their first identity. It is a walking-skeleton hook like W3-10's `--print-owner-token`, and access to the box is the credential.
- It prints one link token for the org's owner, the `local:owner` user from W3-10, once to stdout, and keeps serving.
- It refuses when that user already has a `github:` or `google:` identity. From then on, `identities.link` with step-up is the only way to add one.
- Known risk, accepted as for the first recovery recipient (ADR-0003 decision 12): whoever runs it first on the box can link their own account to the owner.

Removing an identity is not in W4-1.

## Registry operations

Each is org-level. Each acts only on the caller's own user: its sessions, its identities. So each is declared viewer, under the ADR-0006 decision 3 amendment of 2026-11-18. None is destructive.

| Ticket | Name | Method, path | `MinRole` | Destructive | StepUp |
|---|---|---|---|---|---|
| W4-1 | `sessions.list` | `GET /v1/sessions` | viewer | no | no |
| W4-1 | `sessions.revoke` | `POST /v1/sessions/revoke` | viewer | no | no |
| W4-1 | `identities.list` | `GET /v1/identities` | viewer | no | no |
| W4-1 | `identities.link` | `POST /v1/identities/link` | viewer | no | yes |

- **`sessions.list`** returns the caller's unexpired, unrevoked sessions: `id`, `created_at` (the expiry minus 14 days), `expires_at`, and `current`, which is true for the session making the call.
- **`sessions.revoke`** takes `id` and revokes that session. A session that is not the caller's, or does not exist, is 404.
- **`identities.list`** returns the caller's identities as `provider` (`github`, `google` or `local`) and `subject`.
- **`identities.link`** takes no input. It returns `link_url` and `expires_at`.

The existing operations do not change, and none of them gains `StepUp` here.

## Routes that are not operations

Added to `registry.NonOperationRoutes` (ADR-0006 decision 6):

| Method, path | Authorisation |
|---|---|
| `GET /auth/{provider}/start` | None. It only sets the `__Host-mg_oauth` cookie and redirects |
| `GET /auth/{provider}/callback` | `state` must equal the `__Host-mg_oauth` cookie. Then the purpose's own check |
| `POST /auth/sign-out` | The session cookie, same origin |
| `GET /step-up/{org}/{id}` | None. It shows nothing about the request |
| `POST /step-up/{org}/{id}` | The one-time confirmation value from a matching callback, same origin, before expiry |
| `GET /link/{token}` | The unexpired, unused link token |

The pages are server-rendered by `html/template` with no script. They are sent with `Content-Security-Policy: default-src 'none'; style-src 'self'; form-action 'self'; frame-ancestors 'none'`. `/sign-in` itself is the UI's (W2-S). Until W2-S, the provider start URLs are the way in.

`registry.LocalCommands` changes the `server` synopsis to add `--print-owner-link`.

## Storage

W4-1a adds `session_hashes` and the `session_id` and `step_up_id` columns of `audit_events` on both backends, with conformance. W4-1d adds `link_token_hashes` and `step_ups` on both backends, with conformance.

**Lookup tables** (ADR-0002 decision 12, no RLS):
- `session_hashes`: `hash` (SHA-256 hex of the bearer), `id`, `user_id`, `expires_at`, `revoked_at` (NULL while the session is live; set once, to the revocation time, when the session is revoked).
- `link_token_hashes`: `hash`, `id`, `user_id`, `expires_at`, `used_at`.

**Row-level table:**
- `step_ups`: `org_id`, `id`, `user_id`, `token_id`, `session_id` (exactly one is set), `operation`, `project_id`, `environment_id` (empty when the target has none), `input_digest`, `confirm_hash` (set at the callback), `created_at`, `expires_at`, `signed_in_at`, `approved_at`, `declined_at`, `used_at`.

**`audit_events`** gains `session_id` and `step_up_id`, empty when they don't apply. The hash becomes:

`SHA-256(JSON [prev_hash, org_id, seq, id, at, principal_kind, user_id, token_id, session_id, step_up_id, operation, outcome, reason])`

There is no compatibility code (build plan §4). A development database made before W4-1a is recreated.

W4-1a conformance covers:
- create a session, look it up by hash, revoke it, and refuse it once revoked or expired;
- the audit chain with the two new columns.

W4-1d conformance covers:
- mark a link token used once and refuse the second use;
- step-up rows visible only in their org.

## Errors

| Status | When |
|---|---|
| 401 | A missing, malformed, unknown, expired or revoked token or session; both sent at once; a bad or missing `Moonglass-Org` |
| 403 | A denied call; `step_up_required`; a step-up that does not match; an agent identity calling a step-up operation; a cross-origin write |
| 404 | `sessions.revoke` for a session that is not the caller's; sign-in routes when no provider is on |
| 429 | An 11th pending step-up |

## Not in W4-1

Sign-up and org creation on first sign-in. Inviting members. Removing an identity. Email of any kind. Self-hosted sign-in (W7-1). Approvals of agent requests (W5-2). The `/sign-in` screen (W2-S).
