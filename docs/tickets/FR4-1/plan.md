---
verdict: ready
reason: ""
---

# Plan for FR4-1

No contract, ADR, module or security decision is needed. `--proxy-socket` and `--tls-issuer` already exist (`cmd/moonglass/main.go` `parseAgentConnect`). `testEngine` and `adminClient` already exist in `internal/proxy/agent_push_test.go`, same package `proxy_test`. `startTimeout` is in `internal/proxy/proxy_test.go`. `tools/pipeline/verify.go` `ownerTestEdits` lets FR4-1 edit `internal/proxy/memory_test.go`. Test files and docs do not count toward `max_diff_lines`.

## Files to change

- `internal/proxy/memory_test.go`, `TestProxyAndAgentIdleUnderBudget`:
  - First statement, before `os.MkdirTemp` and the `go build`: `testEngine(t)`, with the result discarded. It skips with `no Docker Engine: <err>` when no Engine answers. Under `CI` it fails with `CI must provide Docker`. Its cleanup closes the Engine.
  - The agent `startProcess` call becomes
    `startProcess(t, "agent", bin, "agent", "connect", "--data-dir", agentDir, "--proxy-socket", adminSock, "--tls-issuer", "internal", "--url", "https://"+ln.Addr().String())`.
    `adminSock` is the same `filepath.Join(tmp, "admin.sock")` value that goes to the proxy's `--admin-socket`. No `/run` path appears.
  - The existing waits stay as they are: the upgrades loop, then the `os.Stat(adminSock)` loop. After them and before `time.Sleep(idleSettle)`, add an inline poll on the same `deadline`, which is `startTimeout` from the start:
    ```go
    admin := adminClient(adminSock)
    var grace any
    var graceErr error
    for {
        resp, err := admin.Get("http://localhost/config/apps/http/grace_period")
        if err != nil {
            graceErr = err
        } else {
            grace = nil
            graceErr = json.NewDecoder(resp.Body).Decode(&grace)
            _ = resp.Body.Close()
        }
        if graceErr == nil && grace == "30s" {
            break
        }
        if time.Now().After(deadline) {
            t.Fatalf("GET /config/apps/http/grace_period on %s: expected %q (the agent's first push) within %s, got %#v (error %v)", adminSock, "30s", startTimeout, grace, graceErr)
        }
        time.Sleep(50 * time.Millisecond)
    }
    ```
  - Add `"encoding/json"` to the imports. Nothing else changes: `memoryCeilingKiB` (102400), `idleSettle` (3 s), `measureRSS`, `startProcess` and the failure message all stay. No new helper is added.
- `docs/specs/memory.md`, "What is measured":
  - Step 3: the agent command reads `moonglass agent connect --data-dir <temp> --proxy-socket <temp admin socket> --tls-issuer internal --url https://<loopback>`.
  - Step 4: "Waits until the agent has reached the control plane, the proxy admin socket exists, and the agent's first push has loaded into the test's proxy (`GET /config/apps/http/grace_period` answers `"30s"`, `docs/specs/release.md` "Proxy config"). Then both sit idle for 3 seconds. Nothing is deployed and no requests are made beyond that poll."
  - The paragraph after the steps says:
    - Every port is `:0` and every directory is temp, so two worktrees can run the check at once.
    - The agent pushes only to the test's own proxy, through the temp admin socket, never to `/run/moonglass-proxy/admin.sock`. `--tls-issuer internal` keeps ACME out of the pushed config.
    - The test needs a Docker Engine, because `agent connect` opens one (`docs/specs/connection.md`, "Commands"). It deploys nothing and creates no Docker resources.
    - With no Engine the test skips before it builds anything, except under `CI`, where it fails (`docs/specs/deploy-suite.md`).
    - It needs no Postgres and no non-loopback network, and reads no cloud token. The enrolment token is never printed.

## Tests to write

- The ticket names no new test. The change is to the existing `./internal/proxy TestProxyAndAgentIdleUnderBudget`. After the change, that test:
  - skips or fails through `testEngine` before it builds or starts anything;
  - passes `--proxy-socket <adminSock> --tls-issuer internal` to the agent;
  - samples memory only after the test's proxy reports `grace_period` `"30s"`;
  - with an Engine, passes with the total at or under 102400 KiB.
- `testdata/tests.txt` is unchanged, because no test name is added. `make test-list` should leave it as it is.

## Edge cases

- **No Engine, `CI` unset.** `testEngine` calls `t.Skipf("no Docker Engine: %v", err)` as the first statement, so there is no binary build, no control plane and no process. Covered by running the test on a host without Docker.
- **No Engine, `CI` set.** `testEngine` calls `t.Fatalf` with "CI must provide Docker".
- **Push before the proxy is up.** The agent's `Proxy.Run` retries every second, and `OnError` lines go to the agent's stderr. That stderr is logged only on failure. The poll waits up to `startTimeout` (15 s).
- **Proxy config before the push.** The initial config has no `apps.http`. The GET then answers an error body, a non-`"30s"` value or `null`, and the loop keeps polling. On timeout the failure names the socket, the last decoded value and the last error.
- **Agent fails to start**, for example on a bad flag or an Engine that refuses. The upgrades loop fails within `startTimeout`, as it does today, and cleanup logs the agent's stderr.
- **Two worktrees at once.** Each run has its own `/tmp/mgmem*` directory, socket and ports. The system socket is never named.

## Risks

- **RSS grows.** Since W2-4c the agent now holds a Docker client, the state DB and the pusher. The sample could go over 102400 KiB. The ticket keeps the ceiling, so a red result here is a real G1 finding to report, not a reason to raise the ceiling.
- **tests-kept gate.** The agent line is replaced, so one line of a test on main is removed. `ownerTestEdits["FR4-1"]` covers `internal/proxy/memory_test.go`. No other test file is touched.
- **Lint.** Comparing `grace == "30s"` between an interface and a string constant is valid Go and passes vet. `resp.Body` is closed on every successful GET. `testEngine(t)` with its result discarded is not flagged, because the helper is not a pure function.
- **Paths.** Only `internal/proxy/memory_test.go` and `docs/specs/memory.md` change. Nothing changes in `cmd/moonglass`, `internal/deploy` or `scripts/measure-memory`.
