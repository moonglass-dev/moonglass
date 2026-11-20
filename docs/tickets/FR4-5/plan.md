---
verdict: ready
reason: ""
---

# Plan for FR4-5

## Files to change

- `docs/specs/connection.md`: in "## Route registration", replace the single paragraph line
  "The operation registry does not exist yet, so `/v1/agent` is mounted beside its authorisation (an enrolled client certificate). It joins the fixed route list when W2-1 lands."
  with exactly one new paragraph line:

  "`GET /v1/agent` is not a registry operation. It is on `registry.NonOperationRoutes`, the fixed list of routes outside the registry (`docs/specs/registry.md`). It is mounted only on the agent listener (`--agent-listen`), not on the `--listen` API listener. Its authorisation is the mutual TLS 1.3 handshake in "Transport": the server accepts only a client leaf key that is an enrolled server's public key in the store, and proof of possession is TLS 1.3 CertificateVerify. No other authorisation runs on the route. `TestAgentRoutesMatchTheDeclaredList` in `internal/server` fails when the agent listener mounts a route that is not on `registry.NonOperationRoutes`."

  The heading, the blank lines around it and every other section stay byte-identical. Diff: 1 line removed, 1 added (within `max_diff_lines: 3`).

Facts the text rests on, checked against the code:
- `internal/registry/routes.go`: `NonOperationRoutes` holds `{"GET", "/v1/agent", …}`.
- `internal/server/agent.go`: `mountAgent` adds only `"GET "+agentPath`; `ServeAgents` wraps the listener with `AgentTLSConfig()`, which is `keys.ServerTLSConfig(s.key, s.authorizeAgent)`; `authorizeAgent` accepts a key only when `ServerByPublicKey` finds it. `handleAgent` runs no further check.
- `internal/server/server.go`: `Run` serves `ServeAgents` on `agentListen` and `s.Handler()` (enrolment plus registry operations, `mount()`) on `listen`; `/v1/agent` is not in `mount()`.
- `internal/server/agent_routes_test.go`: `TestAgentRoutesMatchTheDeclaredList` errors on any `AgentRoutes()` entry not on `registry.NonOperationRoutes`.
- "Transport" in the same spec: TLS 1.3 only, client leaf key must be an enrolled server's public key, CertificateVerify is proof of possession.

## Tests to write

- None. The ticket lists `tests: []` and changes docs only. `make ci` must stay green; no test file or `testdata/tests.txt` changes.

## Edge cases

- Overclaiming: the text names no revocation, no expiry check and no Subject/SAN trust; it repeats only what "Transport" and threat-model §3 B3 state. "Out of scope" ("Revocation.") is untouched.
- Acceptance phrases: after the edit, `grep -n "does not exist yet\|when W2-1 lands" docs/specs/connection.md` returns nothing (the only occurrences are in the replaced line).
- Wording: the ticket names `TestAgentRoutesMatchTheDeclaredList` explicitly; keep its exact name and package.

## Risks

- `internal/registry/routes.go` and `docs/specs/registry.md` describe the `/v1/agent` authorisation as an "Ed25519 challenge(-response)", which differs from the mutual-TLS wording. The ticket puts both out of scope (FR1-7 and a later fix); the new paragraph follows "Transport" and the code, so do not touch those files. The `agent.go` comment "joins the fixed route list when the operation registry lands" is FR1-7's.
- Scope drift: `git diff --stat main` must show only `docs/specs/connection.md` plus this ticket's docs. No `make fmt`/`make test-list` side effects are expected, since no Go changes.
- Line count: write the paragraph as one line (the spec uses one line per paragraph) so the diff stays at 2 lines.
