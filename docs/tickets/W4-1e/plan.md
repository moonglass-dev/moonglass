---
verdict: ready
reason: ""
---

# Plan for W4-1e

Context: W4-1a already ships `session_hashes`, `Store.SessionByHash`, `Store.AddSession`, `Store.RevokeSession`, and the `session_id` / `step_up_id` columns that `AppendAudit` and `AuditEvents` read and write (`internal/store/tokens.go`). This ticket makes the registry and REST layer accept a session, records it in the audit chain, and puts `http.CrossOriginProtection` (Go 1.25+, the toolchain is 1.27.4) in front of the HTTP listener. It needs no contract change: no `.proto`, no registry operation, no `agent.toml` or storage schema change, and no new module. The hash is already approved in `docs/specs/auth.md`, "Storage".

## Files to change

- `internal/registry/registry.go`
  - `Principal` gains `SessionID string`, placed after `TokenID`. It is empty for a bearer.
  - `(*Registry).audit` sets `SessionID: p.SessionID` and `StepUpID: ""` on the sealed `store.AuditEvent`. Write `StepUpID` out explicitly so W4-1c has one obvious place to fill it.
- `internal/registry/token.go`
  - Pull out `func wellFormed(bearer string) bool` (has the `mg_` prefix and is `len(tokenPrefix)+tokenSecretLen` long). `Authenticate` uses it and otherwise stays as it is, error text included.
  - Add `func (r *Registry) AuthenticateSession(ctx context.Context, session, orgID string) (Principal, error)`:
    1. `!wellFormed(session) || orgID == ""` → `ErrUnauthenticated`.
    2. `r.st.SessionByHash(ctx, store.HashToken(session), r.now())`. `store.ErrNotFound` (unknown, expired or revoked) → `ErrUnauthenticated`. Any other error → `fmt.Errorf("authenticate session: %w", err)`, which becomes a 500. The store error never contains the bearer or its hash.
    3. `r.st.Memberships(ctx, orgID)` (org-bound through `inOrg`, so RLS applies on Postgres). Find the row whose `UserID == sess.UserID`. If there is none → `ErrUnauthenticated`. A store error → `fmt.Errorf("authenticate session: list members of the org: %w", err)`.
    4. Return `Principal{OrgID: orgID, UserID: sess.UserID, SessionID: sess.ID, Role: m.Role, Scopes: []Scope{{Project: "*", Environment: "*", Operation: "*"}}}`, with empty `TokenID` and `Agent` false.
  - No refusal path puts `session` or `orgID` into the error text.
- `internal/registry/audit.go`: `eventHash` marshals `[]any{e.PrevHash, e.OrgID, e.Seq, e.ID, e.At, e.PrincipalKind, e.UserID, e.TokenID, e.SessionID, e.StepUpID, e.Operation, e.Outcome, e.Reason}`. `VerifyAudit` already calls `eventHash`, so the two cannot drift. Update the doc comment to name the 13 fields.
- `internal/api/api.go`
  - Constants: `sessionCookie = "__Host-mg_session"`, `orgHeader = "Moonglass-Org"`, and three fixed details:
    - `bothDetail = "send a bearer token or a session cookie, not both"`
    - `sessionDetail = "the session is missing, expired or revoked, or Moonglass-Org is missing or names an org you are not a member of: sign in again and send Moonglass-Org"`
    - `orgMismatchDetail = "Moonglass-Org names a different org than the token's: drop the header or use a token of that org"`
  - Sentinels, each wrapping `registry.ErrUnauthenticated` so `errors.Is` still works: `errBothCredentials`, `errSession` and `errOrgMismatch`.
  - `principal(ctx, reg, r *http.Request) (registry.Principal, error)` replaces the current header-only version. The call site gets the request with `r, _ := humago.Unwrap(ctx)`. Check that signature against huma v2.40.0 while implementing. The cookie is read with `r.Cookie(sessionCookie)` from the standard library. The rules:
    1. `auth := r.Header.Get("Authorization")`, `cookie, cerr := r.Cookie(sessionCookie)`. A credential is present when `auth != ""` or `cerr == nil` (an empty cookie value counts as present).
    2. Both present → `errBothCredentials`, before either is checked, so neither store lookup runs.
    3. Cookie only → if `r.Header.Get(orgHeader) == ""`, return `errSession`. Otherwise call `reg.AuthenticateSession(ctx, cookie.Value, org)`. An `ErrUnauthenticated` result is mapped to `errSession`. Any other error is returned as is.
    4. Otherwise run the existing bearer path: a `Bearer ` prefix, then `reg.Authenticate`. Then, if `org := r.Header.Get(orgHeader); org != "" && org != p.OrgID`, return `errOrgMismatch`.
  - `httpError` checks `errBothCredentials`, `errSession` and `errOrgMismatch` first, all as 401 with their fixed details, then the existing `ErrUnauthenticated` case. No detail echoes the cookie, the token or the header value.
  - `authDoc` and the OpenAPI parameters stay unchanged, so `internal/api/openapi.json` and the generated CLI do not change. Session clients are browsers, documented in `docs/specs/auth.md`.
- `internal/server/server.go`: `Handler()` returns `http.NewCrossOriginProtection().Handler(mux)`, with no trusted origins and no bypass patterns. Update its doc comment. `Routes()` and `mount()` are unchanged. The agent listener (`ServeAgents`) is a separate listener and stays unwrapped, per the ticket's "HTTP listener".
- `docs/specs/auth.md`: no change expected. The built behaviour matches "Authentication" and "Cross-origin requests".

Expected counted diff: about 90 non-test lines, under the 220 cap.

## Tests to write

- `./internal/registry TestAuthenticateSession` (new `session_test.go`, `registry_test` package, `newEnv`). The user is the owner of `e.orgID` and a member of a second org. A third org has no membership. Sessions are made with `registry.NewToken` and `e.st.AddSession(ctx, e.userID, store.HashToken(text), e.now.Add(14*24*time.Hour))`. The table has:
  - a live session with `e.orgID` → `{OrgID, UserID, SessionID: sess.ID, Role: owner, Scopes: [* * *]}`;
  - a live session with org2 → the same with `Role: member`;
  - an unknown well-formed `mg_` value → `ErrUnauthenticated`;
  - an expired session (`e.now` moved to the expiry) → `ErrUnauthenticated`;
  - a revoked session (`RevokeSession`) → `ErrUnauthenticated`;
  - a malformed value (`"abc"`, and `mg_` with 51 characters) → `ErrUnauthenticated`;
  - a non-member org → `ErrUnauthenticated`;
  - an empty org → `ErrUnauthenticated`.

  It compares with `reflect.DeepEqual` and prints the expected and actual principals. For each refusal it also asserts that `err.Error()` does not contain the session text.
- `./internal/registry TestSessionCallAuditsSessionID`: register `t.op`. Invoke once with the principal from `AuthenticateSession` and once with `e.principal(owner, wildcard)`. `AuditEvents` returns two events. The first has `SessionID == sess.ID`, `TokenID == ""` and `StepUpID == ""`. The second has `SessionID == ""` and `TokenID == "tok-1"`.
- `./internal/registry TestAuditHashCoversSessionAndStepUp`: build two events with `twoEvents`, or a session call. Assert that each event's `Hash` equals the hex SHA-256 of `json.Marshal([]any{13 fields in order})`, computed in the test. Then, table-driven: setting `SessionID` or `StepUpID` on event 1 or event 2 makes `VerifyAudit` return an error naming `sequence <n>`.
- `./internal/api TestBearerAndSessionTogetherIs401` (new `session_test.go`, `api_test` package). The fixture is `server.New` over a store wrapper that embeds `store.Store` and counts `TokenByHash` and `SessionByHash` calls. It mints an owner token and adds a session. `GET /v1/servers` with both the bearer and the `__Host-mg_session` cookie (with and without `Moonglass-Org`) gives 401, and the problem `detail` equals `bothDetail` exactly. Both counters are 0 and `AuditEvents(org)` is empty.
- `./internal/api TestSessionNeedsMemberOrg`: a cookie with no header → 401. A cookie plus a header naming an org the user is not a member of → 401. A cookie plus the member org → 200, with one audit event whose `session_id` is the session id. No 401 body contains the cookie or the header value.
- `./internal/api TestBearerWithOtherOrgHeaderIs401`: an owner token with `Moonglass-Org` naming another existing org → 401 with `orgMismatchDetail`. With its own org → 200. With no header → 200.
- `./internal/server TestCrossOriginWriteRefused` (new `cross_origin_test.go`, `server` package, reusing `newFixture`, `issue` and `newRequest`, with an `httptest.NewRecorder` on `f.srv.Handler()`). The table:
  - A valid enrol `POST /v1/enroll` with `Sec-Fetch-Site: cross-site` → 403, `EnrolmentTokens` shows the token unused, and `Servers` is empty.
  - The same request with `Origin: https://evil.example` and no `Sec-Fetch-Site`, where Host differs → 403.
  - `POST /v1/servers` with an owner bearer and `Sec-Fetch-Site: cross-site` → 403, and no audit event is written.
  - The same valid enrol `POST` with no `Origin` or `Sec-Fetch-Site` (the CLI) → 200 ACCEPTED, and one server is registered.
  - `GET /v1/servers` with an owner bearer and `Sec-Fetch-Site: cross-site` → 200, with one audit event.
- `make test-list` rewrites `testdata/tests.txt`.

## Edge cases

- Both credentials, including an empty `Authorization` value with a cookie, or a malformed bearer with a valid cookie: refused before any lookup (`TestBearerAndSessionTogetherIs401`). This closes the case where one credential authorises the request and the other is audited (§9.4).
- An empty cookie value, or a non-`mg_` cookie: 401 with no store call (the `TestAuthenticateSession` malformed rows, plus the api path through `errSession`).
- An expired session at exactly its expiry: `SessionByHash` compares strictly (`expires_at > now`), covered by the expired row.
- A revoked session: covered.
- A hostile `Moonglass-Org` (garbage, another org's id): it is only ever a SQL parameter in an org-bound transaction, membership is required, and the value never appears in a detail (`TestSessionNeedsMemberOrg`, `TestBearerWithOtherOrgHeaderIs401`).
- A store failure in `SessionByHash` or `Memberships`: 500 with `internalDetail`, logged wrapped, with no bearer in the text, through the existing `httpError` path.
- Cross-site `GET` reaches the handler. The CLI's header-less `POST` reaches the handler. An `Origin` that differs from Host without `Sec-Fetch-Site` gets 403 (`TestCrossOriginWriteRefused`).
- An unmounted method on a mounted path (`POST /v1/servers`) cross-site gets 403 before the mux. That is expected: refusal comes first.

## Risks

- Existing hash tests: no test hard-codes the 11-field body. `TestAuditEventsAreHashChained` mutations still change the hash. Existing `internal/server` tests use Go's HTTP client, which sends no `Origin` or `Sec-Fetch-Site`, so `CrossOriginProtection` lets them through.
- `humago.Unwrap` signature: if v2.40.0 differs, use whatever the adapter exposes to reach `*http.Request`. Do not hand-parse the `Cookie` header.
- `docs/specs/registry.md`, "Audit", still shows the 11-field hash. It is outside `allowed_paths`, and `docs/specs/auth.md` (owner-approved) supersedes it. The reviewer should not count this as a spec contradiction. It belongs in a docs follow-up.
- Review strictness (trusted core): no middleware decides role or scope. `CrossOriginProtection` only refuses cross-site unsafe methods, and authorisation stays in `Invoke`. No banned words.
