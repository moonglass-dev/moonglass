Ticket cannot pass its gates reliably: the locked `TestRollbackUnderLoadIsUnderFiveSeconds` requires an `r10` answer after the ten-deploys' last switch returns, but its own `Rollback` call closes that window about 10 ms later, so the test fails in roughly one run in four (3 of 11 local runs).

# W2-7 build is blocked

Everything the ticket asks for is implemented and every gate is green, except that the rollback suite test is nondeterministic. `make ci` passed its gates on one run today and failed on `./test/deploy` on another, with the same message. That is not something this ticket's `allowed_paths` can fix.

## What the locked test requires

`test/deploy/helpers_test.go` `tenDeploys` returns one mark per switch, the last being the switch to `r10`. `test/deploy/deploy_test.go` `checkOrder` requires, for every mark, a response whose release equals the mark's and whose `arrived` time is at or after the mark's `returned` time. The rollback test then calls the rollback with no pause:

```go
marks := s.tenDeploys()

called := time.Now()
res := s.x.Rollback(context.Background(), "rollback-"+s.app, &agentv1.Rollback{AppId: s.app})
```

So `r10` has to answer in the interval between the `r10` switch returning and the rollback's route push taking effect.

## Why no implementation can meet it

`docs/specs/release.md` "Rollback" step 3 requires a running previous release to be neither started nor gated, so that a rollback finishes in under 5 seconds (G2). "Known trade-off" keeps the previous release running for 10 minutes after each switch, so the previous release (`r09`) is running and the rollback is two state reads, one Engine inspect and one `Proxy.PushRouted`.

Measured on a failing run, the proxy's `/load` for `r10` is logged at `1795168275.482234` and the rollback's `/load` at `1795168275.492378`: 10 ms apart. The load generator sends 50 requests per second, one every 20 ms, and its retry rule sends a request the reload cut before its answer onto the `r09` route. A response carrying `r10` inside a 10 ms window is therefore not guaranteed; whether one arrives is chance.

Slowing `Rollback` on purpose would contradict its own spec step and the G2 claim, and `internal/deploy/rollback.go`, `internal/deploy/switch.go` and the assertions in `test/deploy/deploy_test.go` are outside `allowed_paths`. The owner exception lets W2-7 edit `test/deploy/deploy_test.go` and `test/deploy/helpers_test.go` only to pass a request id to `Deploy`, `SwitchRelease` and `Rollback`, which is already done; it does not allow an assertion change.

## Evidence

`CGO_ENABLED=1 go test -race -count=1 -run TestRollbackUnderLoadIsUnderFiveSeconds ./test/deploy`:

- 8 of 11 runs passed; 3 failed with
  `after the operation at … returned at …, no response carried r10, want one within 2s`.
- `make ci` failed once with the same message and passed once. The failures show the `r10` push and the rollback push 10 ms apart.

`TestTenDeploysUnderLoadDropNothing` is stable: it keeps load running for 2 s after its last switch, so its `r10` mark always gets an answer.

## What is implemented

- `internal/deploy/proxy.go`: `ProxyConfig.Listen`, `checkListen` and the one document builder. `Push` and `PushRouted` refuse a malformed value before any request, so nothing is posted and no commit runs; `Run` inherits the same refusal.
- `internal/deploy/reconcile.go`: `Executor.ScopeReconcile`, cloning the labels into `scope`, which `Reconcile` and `Run`'s start-up reconcile already apply.
- `internal/deploy/deploy.go`: the `scope` field comment.
- `Makefile`: `test-deploy` in `.PHONY`, running `CGO_ENABLED=1 go test -race -count=1 ./test/deploy`.
- `.github/workflows/ci.yml`: the `deploy-suite` job, one entry per Engine major 25–29 on `ubuntu-22.04`, Docker packages at the newest patch of each major (checked against `download.docker.com/linux/ubuntu/dists/jammy/stable/binary-amd64/Packages` today), the apt key fingerprint checked, an Engine-major check, SHA-pinned actions, and `make test-deploy` last.
- `testdata/tests.txt`: all seven named tests.

`make ci` gates `proto-check`, `fmt-check`, `mod-check`, `lint`, `check-test-list` and `build-all` are green; `test` is green except for the flake above.

## Decision needed

Either let the rollback test keep load for a beat after `tenDeploys` and before `Rollback` (for example 200 ms, so the `r10` mark gets its answer), or end the `r10` mark's window at the rollback's route push while still requiring an `r10` answer before it. Both are changes to the locked `test/deploy/deploy_test.go` or `test/deploy/helpers_test.go` that the owner must allow.

Blocked at status: tests-locked
