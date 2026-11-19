---
verdict: ready
reason: ""
---

# Plan for FR4-2

## Files to change

- `internal/deploy/proxy.go`:
  - Add, directly above `issuerApps`:
    ```go
    // CheckIssuer is the one decision on which TLS issuers the agent accepts:
    // acme, acme-staging and internal. Anything else is an error naming the
    // value and the three accepted ones.
    func CheckIssuer(issuer string) error {
        switch issuer {
        case "acme", "acme-staging", "internal":
            return nil
        }
        return fmt.Errorf("TLS issuer %q is not one of acme, acme-staging or internal; pass one of those", issuer)
    }
    ```
    The error text is byte-for-byte the one `issuerApps` returns today.
  - `issuerApps(issuer string) (map[string]any, error)`: first statement is `if err := CheckIssuer(issuer); err != nil { return nil, err }`. The `switch issuer` keeps the three `case` branches that build `value` (and `apps["pki"]` for `internal`) and loses its `default:` branch, so the accept/refuse decision exists only in `CheckIssuer`. Doc comment updated: "issuer must pass CheckIssuer; a refused value is returned as its error before any request is sent." Signature and `Push` unchanged: `Push` still calls `issuerApps` before any `p.do`, so a refused issuer sends nothing.
- `cmd/moonglass/main.go`, `parseAgentConnect`: replace the 5-line `switch *issuer { case "acme", "acme-staging", "internal": default: return agentConnectArgs{}, false }` with
  ```go
  if err := deploy.CheckIssuer(*issuer); err != nil {
      return agentConnectArgs{}, false
  }
  ```
  `deploy` is already imported. The error is deliberately not printed (spec: bad value prints the usage and exits 2; ticket "Out of scope"). The flag help text `"TLS issuer: acme, acme-staging or internal"` stays (it is not the literal `"acme-staging"`).
- `testdata/tests.txt`: regenerated with `make test-list`.

Counted non-test diff: about 20 lines, well under 60.

## Tests to write

Existing tests are not edited (the `tests-kept` gate); new tests are appended.

- New file `internal/deploy/issuer_test.go` (package `deploy`, imports `context`, `strings`, `testing`; reuses `newFakeAdmin`, `newProxyDB`, `fakeAdmin.requestCount` from `proxy_test.go`):
  - `./internal/deploy TestCheckIssuerAcceptsTheThreeIssuers`: table `acme`, `acme-staging`, `internal`; `CheckIssuer(v)` is nil, failure message `CheckIssuer(%q) = %v, want nil`.
  - `./internal/deploy TestCheckIssuerRefusesOtherValues`: table `""`, `letsencrypt`, `ACME`, `" acme"`, `"acme-staging "`, `Internal`; error non-nil and its text contains `fmt.Sprintf("%q", v)` and each of `acme`, `acme-staging`, `internal`; failure messages show the value and the error text.
  - `./internal/deploy TestCheckIssuerAgreesWithPush`: table `acme`, `acme-staging`, `internal`, `letsencrypt`, `ACME`, `""`; per case a fresh `newFakeAdmin(t)`, `NewProxy(nil, newProxyDB(t), ProxyConfig{Socket: fake.path, Issuer: v}).Push(ctx)`. Asserts `(pushErr == nil) == (checkErr == nil)`; when `checkErr != nil`, `pushErr.Error() == checkErr.Error()` and `fake.requestCount() == 0`. (Accepted values with an empty `newProxyDB` and nil engine succeed, as `TestProxyPushIssuers` already shows.)
- `cmd/moonglass/connect_test.go`, appended:
  - `./cmd/moonglass TestAgentConnectIssuerFollowsDeployCheck`: table `acme`, `acme-staging`, `internal`, `letsencrypt`, `ACME`, `" internal"`, `""`; `got, ok := parseAgentConnect([]string{"--url", "https://x", "--tls-issuer", v})`; asserts `ok == (deploy.CheckIssuer(v) == nil)` and, when ok, `got.proxy.Issuer == v`. Test-file string literals are allowed (the "no `"acme-staging"`" rule covers non-test Go under `cmd/`).

Order: write the four tests first and see them fail to compile (no `CheckIssuer`), then add `CheckIssuer`, then change `issuerApps` and `parseAgentConnect`.

## Edge cases

- Empty issuer `""`: refused by `CheckIssuer` (`TestCheckIssuerRefusesOtherValues`, `TestCheckIssuerAgreesWithPush`, `TestAgentConnectIssuerFollowsDeployCheck`). On the command line, `--tls-issuer ""` returns ok=false and the usage exit 2 (same as before).
- Case and whitespace variants (`ACME`, `Internal`, `" acme"`, `"acme-staging "`): exact match only, no trimming or folding; covered by `TestCheckIssuerRefusesOtherValues` and the cmd test. Not widening the accepted set is the §9 check.
- Refused value reaching `Push` directly (bypassing the CLI): `issuerApps` returns `CheckIssuer`'s error before any admin-socket request; `TestCheckIssuerAgreesWithPush` asserts zero requests and identical text; `TestProxyPushIssuers/unknown issuer` still passes unchanged.
- Refused value on `agent connect`: `TestAgentConnectRejectsBadProxyFlags` (unchanged) keeps asserting stderr == usage, exit 2, no `agent.db`.
- `internal` remains the only branch that sets `apps.pki` with `install_trust: false`: `TestProxyPushIssuers` unchanged.

## Risks

- A `switch` with no `default` in `issuerApps` leaves `value` nil for a refused value; that path is unreachable because `CheckIssuer` runs first. Reviewers may read the default-less switch as an omission: the doc comment states it, and `TestCheckIssuerAgreesWithPush` proves the guard.
- Changing the error text would break `TestProxyPushIssuers` and the new tests: the plan copies the existing format string exactly.
- `tests-kept` gate: no existing test line is changed; new deploy tests go in a new file, the cmd test is appended at the end of `connect_test.go`.
- `make test-list` must be run so `check-test-list` passes; `make fmt` before `make ci`.
- No import changes outside the standard library; `internal/deploy` still does not import `internal/proxy` (ADR-0011). No contract, ADR, spec or threat-model change: behaviour already matches `docs/specs/release.md` ("Certificates") and `docs/specs/connection.md` (`--tls-issuer`).
