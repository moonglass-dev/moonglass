Ticket cannot pass its gates: a second ci.yml job breaks the locked TestReleaseWorkflowPinsEveryAction, and Caddy's config reload cuts connections that are mid-TLS-handshake, so the locked zero-drop suite tests fail at nearly every run.

# W2-7 build is blocked

Both blockers are outside `allowed_paths`, so no change this ticket may make can clear them.

## Blocker 1: `ci.yml` may hold only one `steps:` key

`internal/releasetest/release_test.go:875` (`TestReleaseWorkflowPinsEveryAction`, on `main`, untouched by this ticket) calls
`parseStepsAt(t, ciPath, …)`, and that helper fails unless `.github/workflows/ci.yml` contains exactly one `steps:` key:

```
--- FAIL: TestReleaseWorkflowPinsEveryAction (0.00s)
    release_test.go:875: .github/workflows/ci.yml: 2 steps: keys, want exactly 1
```

The ticket requires a second job in the same file, with a matrix over Engine 25–29, and a GitHub Actions job cannot be
written without `steps:`. A reusable-workflow job (`jobs.<id>.uses:`) would avoid the key but needs a new file under
`.github/workflows/`, which `allowed_paths` does not cover. `internal/releasetest/` is not in `allowed_paths`, and changing
a test line that exists on `main` fails `tests-kept`.

Decision needed: allow `internal/releasetest/release_test.go` to accept one `steps:` per job (or to parse the `ci` job by
id), or name the new workflow file in `allowed_paths`.

## Blocker 2: the zero-drop assertion contradicts Caddy's reload

Both suite tests fail in most runs, with `EOF` requests:

```
--- FAIL: TestTenDeploysUnderLoadDropNothing
    deploy_test.go:39: load under ten deploys: 1 .. 5 of ~1300 requests failed, first error …: EOF at 2.4 s after start
--- FAIL: TestRollbackUnderLoadIsUnderFiveSeconds
    deploy_test.go:77: load under ten deploys and one rollback: 1 .. 5 of ~1300 requests failed, first error …: EOF
```

Every failure lands at the instant of a `SwitchRelease` push (reload), and this is not a product regression of this ticket,
not the document this ticket writes, and not this host. I reproduced it with a standalone Caddy-only program (no Moonglass
code at all: no guard, no counter, no Docker, no container upstream), reloading the server config every 2 s under the
suite's own connection pattern (a fresh TLS connection per request, 50 per second):

| Build | Reloads | Result |
|---|---|---|
| Caddy 2.11.9, Go 1.27.4, macOS | 20 | 4–9 failed of 2000 |
| Caddy 2.11.7, Go 1.26.2, macOS | 20 | 4–5 failed of 2000 |
| Caddy 2.11.9, Go 1.27.4, Linux (arm64 container) | 20 | 4 failed of 2000 |
| same, **keep-alives on** | 20 | 0 failed |
| same, **plain HTTP** (no TLS) | 20 | 0 failed |
| same, **no reload** | 0 | 0 failed |

It also reproduces against the real `moonglass proxy` process with agent-style pushes (2 failed of 1000). So:

- Caddy's 30 s `grace_period` does keep requests on established connections, as `docs/specs/release.md` "Draining" says.
- A connection that is **still in its TLS handshake** when the old server stops is closed before its request is served.
  Nothing in the pushed document changes that: I tried `https_port`, `shutdown_delay` (Caddy skips the delay while the
  listener stays in use), and `protocols: ["h1"]`; all still fail.
- `docs/specs/deploy-suite.md` "The load generator" mandates keep-alives **off**, so every request pays the handshake and
  one or two per run fall in that window, which fails "zero failed requests" and both locked tests.

The document is also fully pinned by locked tests (`TestProxyPushBuildsCompleteConfig` asserts it byte for byte, and
`TestProxyPushListenSetsAddressAndDisablesRedirects` requires the `Listen` document to equal the plain one except `listen`
and `automatic_https`), so `internal/deploy/proxy.go` cannot add anything that would help.

Decision needed, one of:

1. The suite reuses connections, or tolerates a request cut during a reload's handshake (changes `docs/specs/deploy-suite.md`
   and the locked `test/deploy/load_test.go`).
2. The proxy reloads without a new server, so no handshake is ever cut (Caddy-level work in `internal/proxy`, not this ticket).
3. The zero-drop claim is narrowed to requests that reached the proxy (a spec change).

Option 1 also needs the ordering assertion revisited: with keep-alives, a request on an old connection is served by the old
route, which is why the spec turned keep-alives off.

## What is implemented and green

Kept, because it is correct and passes everything that does not need the two blockers resolved:

- `internal/deploy/proxy.go`: `ProxyConfig.Listen`, `checkListen` (refused before any read or post, next to the issuer
  check) and the one document builder. Empty `Listen` still produces the exact current document.
- `internal/deploy/reconcile.go`: `Executor.ScopeReconcile(labels ...string)`; `internal/deploy/deploy.go`: the `scope`
  comment only.
- `Makefile`: `test-deploy` in `.PHONY`, `CGO_ENABLED=1 go test -race -count=1 ./test/deploy`.
- `.github/workflows/ci.yml`: the `deploy-suite` matrix job (Engine 25–29, pinned apt versions, fingerprint check, Engine
  major check, SHA-pinned actions). **This is what fails Blocker 1**; it is left in place so the conflict is reproducible.
  Reverting it removes that failure and leaves the ticket's CI requirement unimplemented.
- `testdata/tests.txt`: rewritten by `make test-list`; the six named tests are listed.
- `git grep -n 'Listen:' -- cmd/ internal/agent/` finds no `ProxyConfig` (only unrelated `net.Listen` calls in a test), and
  `git grep -n ScopeReconcile -- cmd/ internal/agent/` finds nothing.

`make fmt`, `make lint`, `make mod-check`, `make proto-check`, `make check-test-list` and `make build-all` pass. `make ci`
fails only on `test`: `internal/releasetest TestReleaseWorkflowPinsEveryAction` (Blocker 1) and, in most runs, the two
`test/deploy` tests (Blocker 2); `internal/deploy` is green, including the three named Listen and scope tests. Last
`make ci`: `ok proto-check`, `ok fmt-check`, `ok mod-check`, `ok lint`, `FAIL test`, `ok check-test-list`, `ok build-all`.

Blocked at status: tests-locked
