---
verdict: needs-human
reason: "Executor.Run's start-up Reconcile removes every dev.moonglass.managed container on the Engine that the suite's fresh agent DB does not know, and test/deploy cannot narrow it (Executor.scope is unexported and deploy.go is outside allowed_paths). Decide either to add an exported reconcile scope to internal/deploy (add internal/deploy/deploy.go and docs/specs/release.md to W2-7's allowed_paths) or to drop \"runs Executor.Run for the whole test\" from docs/specs/deploy-suite.md and W2-7."
---

# Plan for W2-7

## What I found

- `docs/specs/deploy-suite.md` ("What the suite asserts") and the ticket require `Executor.Run` to run for the whole of both suite tests, "so stops and retention happen as in production". The same spec and the acceptance criteria require that "the two tests and two worktrees can run at once".
- `Executor.Run` (`internal/deploy/stop.go`) calls `x.Reconcile(ctx)` first. `Reconcile` (`internal/deploy/reconcile.go`) lists every container labelled `dev.moonglass.managed=true`, narrowed only by `x.scope`. It removes each one whose app's current, previous or candidate release in *this executor's* agent database does not match the container's release label.
- `x.scope` is unexported. `deploy.New` leaves it nil, which is correct in production, where every managed container on the server is the agent's. Only in-package tests set it. `TestExecutorRunStopsAndRetainsUntilCancelled` and the stop tests set `x.scope = []string{"dev.moonglass.project=" + p.name}` for exactly this reason.
- `test/deploy` is a separate package, so it cannot set `scope`. `allowed_paths` does not include `internal/deploy/deploy.go` or `reconcile.go`, so the plan cannot add an exported way to set it either.
- The result: each suite test starts its executor with an empty agent database. Its `Run` then force-removes every managed container on the Engine. That covers the other suite test's releases (the two tests can then no longer run at once), another worktree's suite or `internal/deploy` tests, and, under `make test` / `make ci`, the untagged Docker tests in `internal/deploy` that `go test ./...` runs in parallel with `./test/deploy`. On a developer machine it would also remove real Moonglass-managed containers. Under `CI` this makes `make ci` flaky, and it contradicts the spec's isolation rule ("Each test ... removes everything with that label", never anything else).
- Starting `Run` before the suite creates anything does not help, because the containers it removes belong to other tests.

## Decision needed (owner)

One of:

1. **Exported reconcile scope.** Add one field, for example `deploy.Scope` or an `Executor` option, that narrows `Reconcile` to a label filter. Production never sets it, and the suite sets it to `dev.moonglass.project=<test project>`. This needs `internal/deploy/deploy.go` (and the in-package tests that set `x.scope` today, so that only one way remains) plus `docs/specs/release.md` added to W2-7's `allowed_paths`. It fits in the 250-line budget only if the in-package tests move to the same option.
2. **No `Executor.Run` in the suite.** Remove that sentence from `docs/specs/deploy-suite.md` and the ticket. The suite's stops then never run: retired containers are removed by the test's own cleanup, and the claims under test (zero drops, rollback under 5 s) do not depend on stops within the test's duration. Retired stops happen 30 s after a switch, and the previous release stays running for 10 minutes.

## Everything else checked (ready once the above is settled)

- `ProxyConfig.Listen` in `internal/deploy/proxy.go`: validate it with `net.SplitHostPort` and a numeric port via `strconv.ParseUint(port, 10, 16)`, with a non-empty host required, next to `issuerApps` at the top of `push`. When it is set, the server gets `listen` `[Listen]` and `automatic_https` `{"disable_redirects": true}`. Caddy v2.11.9 `autohttps.go` adds a TLS connection policy for any server with a qualifying host matcher, whatever its port, so `<project>.moonglass.test` is served over TLS from the internal issuer on `127.0.0.1:<port>` without `https_port`.
- Caddy's `/load` returns only after the old servers have started shutting down (`modules/caddyhttp/app.go` Stop waits on `startedShutdown`). So "no later response carries an older id" can be asserted for requests sent after both `SwitchRelease`/`Rollback` returned and the first new-id response arrived.
- `github.com/moby/moby/client` is already allowed for `test/deploy` by ADR-0014 (amended) and the `docker-deps` depguard rule, so no lint or ADR change is needed.
- Image import follows `internal/deploy/helpers_test.go` `importImage`: one `go build` of `test/deploy/testdata/server` per test, then one import per release with `ENTRYPOINT ["/app","<release_id>"]`. The reachability check runs after the first `Deploy` and before its `SwitchRelease`, because the health gate also probes the container IP from the host.
