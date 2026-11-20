---
verdict: ready
reason: ""
---

# Plan for W2-7

Everything this ticket needs is already settled: `docs/specs/release.md` ("Listen", "Run"), `docs/specs/deploy-suite.md`, ADR-0014 (amended 2026-11-20 so `test/deploy` may import the moby client), and the `docker-deps` depguard rule in `.golangci.yml` (it already excludes `**/test/deploy/**`). There is no contract change, no new module and no new ADR. I checked Caddy v2.11.9 `modules/caddyhttp/autohttps.go`: a server whose routes have host matchers gets a TLS connection policy whatever port it listens on, and `disable_redirects` stops the :80 redirect server. So a document that changes only `listen` and `automatic_https` serves HTTPS on `127.0.0.1:<port>`, as the spec says. No `https_port` is needed.

## Files to change

- `internal/deploy/proxy.go`
  - `ProxyConfig` gets `Listen string`. Its comment: when set, it is the one address the HTTP server binds and Caddy adds no :80 redirect; only the deploy-correctness suite sets it; empty in production.
  - New `func checkListen(listen string) error`. Empty returns nil. Otherwise it runs `net.SplitHostPort` and then `strconv.ParseUint(port, 10, 16)`. Either failing returns `fmt.Errorf("proxy listen address %q is not host:port with a numeric port: %w", listen, err)`.
  - In `push`, call `checkListen(p.cfg.Listen)` right after `issuerApps`, before any `p.do`. `Push` and `PushRouted` both go through `push`, and `PushRouted` returns before `commit`. So a refused value posts nothing, runs no commit and leaves `p.last` untouched.
  - The server map is built as `server := map[string]any{"listen": []string{":443"}, "routes": routes}`. When `p.cfg.Listen != ""`, `server["listen"] = []string{p.cfg.Listen}` and `server["automatic_https"] = map[string]any{"disable_redirects": true}`. This is one builder, and with `Listen` empty the document is byte-for-byte the same as today. Adjust the `proxyServer` const comment to mention Listen.
- `internal/deploy/reconcile.go`: add `func (x *Executor) ScopeReconcile(labels ...string) { x.scope = slices.Clone(labels) }`. Its doc comment says it narrows `Reconcile`, and so `Run`'s start-up reconcile, to managed containers that also carry every `key=value` label; it is called before `Run`; production never calls it (`docs/specs/release.md`, "Run"). Docker ANDs repeated `label` filters, which `reconcile` already relies on. Nothing else changes.
- `internal/deploy/deploy.go`: the `scope` field comment becomes "set only by ScopeReconcile; nil in production, where every managed container on the server is ours". One line.
- `internal/deploy/proxy_test.go`: the two Listen tests (below), using the existing `fakeAdmin`, `newExecutor`, `deployDomains`, `makeCurrent`, `decodeAny`, `requireJSONEqual` and `newProxyDB`.
- `internal/deploy/scope_test.go` (new): `TestScopeReconcileRemovesOnlyMatchingContainers`.
- `test/deploy/` (new; test files only, like `internal/installtest`):
  - `testdata/server/main.go`: `package main`, standard library only. It takes `os.Args[1]` as the release id and exits 2 without it. An `http.Server` on `:8080` has a mux where `GET /healthz` answers 200 and every other path answers 200 with a body of exactly the release id. On SIGTERM (`signal.NotifyContext`) it calls `srv.Shutdown` with a 25 s context, which finishes the requests in flight, then exits 0.
  - `main_test.go`: `TestMain(m)` calls `flag.Parse()`. When `test.list` is set it returns `os.Exit(m.Run())` without building, so `make test-list` and `check-test-list` stay fast. Otherwise it makes one `os.MkdirTemp` dir and builds two binaries there:
    - `go build -o <dir>/moonglass moonglass.dev/moonglass/cmd/moonglass` with `CGO_ENABLED=0`;
    - `go build -o <dir>/server ./testdata/server` with `CGO_ENABLED=0 GOOS=linux GOARCH=runtime.GOARCH`.

    It stores them in package vars `moonglassBin` and `serverBin`, runs `m.Run()`, removes the dir and exits with the code. A build failure is printed with `fmt.Fprintf(os.Stderr, …)` (forbidigo forbids only `fmt.Print*`) and ends in `os.Exit(1)`.
  - `helpers_test.go`:
    - `connectEngine(t) (*deploy.Engine, *client.Client)`: `deploy.Connect`, plus a moby client built as `client.New(client.WithHost(cmp.Or(os.Getenv("DOCKER_HOST"), "unix:///var/run/docker.sock")))` and pinged with `NegotiateAPIVersion: true`, mirroring `internal/deploy/engine.go`. If either fails, the test skips, or calls `t.Fatalf` when `CI` is set.
    - `newProject(t, cli) string`: `"t" + 12 lowercase hex`. It registers a cleanup that, pass or fail:
      - force-removes every container labelled `dev.moonglass.project=<p>`;
      - removes every network with that label;
      - removes every image listed with filter `reference=moonglass.local/<p>/*`, by ID with force. Listing first means an image retention already removed is simply not listed.
      - lists all three again and calls `t.Errorf` for anything left.
    - `importRelease(t, cli, project, rel) string`: tars `serverBin` as `/app` (0755) and calls `cli.ImageImport` as `moonglass.local/<p>/web:<rel>` with `ENTRYPOINT ["/app","<rel>"]`. This is the same shape as `internal/deploy/helpers_test.go` `importImage`. It returns the ref.
    - `startProxy(t) (sock string)`:
      - `dir := os.MkdirTemp("", "mgd")`;
      - start `exec.Command(moonglassBin, "proxy", "--data-dir", dir, "--admin-socket", dir+"/admin.sock")` with stdout and stderr in a mutex-guarded buffer;
      - poll `GET /config/` over a unix-socket `http.Client` every 50 ms, up to 30 s. If the process exits first, fail with its output.
      - Cleanup: SIGTERM, wait up to 45 s (Caddy's 30 s grace plus margin). If it is still running, `Kill` and `t.Errorf` "proxy did not exit". Log the output when `t.Failed()`. Then `os.RemoveAll(dir)`.
    - `reservePort(t) string`: `net.Listen("tcp","127.0.0.1:0")`, read the port, close, return `"127.0.0.1:<port>"`.
    - `localCA(t, sock) *x509.CertPool`: `GET /pki/ca/local` over the socket and decode `root_certificate` into a pool that holds only that root.
    - `type suite struct { t; cli; project, app, domain, listen, sock string; x *deploy.Executor }`.
    - `newSuite(t) *suite`, in this order:
      1. `connectEngine`, `newProject` (its cleanup runs last), `startProxy`, `reservePort`;
      2. `state.Open(ctx, t.TempDir())`, `policy.Parse(nil, policy.Env{})`;
      3. `deploy.NewProxy(engine, db, deploy.ProxyConfig{Socket: sock, Issuer: "internal", Listen: listen})` and `deploy.New(engine, pol, db, proxy)`;
      4. `x.ScopeReconcile("dev.moonglass.project=" + project)`;
      5. `go x.Run(runCtx, errs.add)`, with a cleanup that cancels, waits for `Run` to return, closes the db, and calls `t.Errorf` for every error `Run` reported.

      The app is `app-web-<project>` and the domain `<project>.moonglass.test`.
    - `(s *suite) deploy(rel)`: `importRelease`, then `x.Deploy(ctx, &agentv1.Deploy{AppId, ReleaseId, Image, Project, Environment: "production", Service: "web", Port: 8080, HealthPath: "/healthz", Domains: []string{domain}})`. It requires `SUCCEEDED` and shows the detail.
    - `(s *suite) switchTo(rel) (called, returned time.Time)`: `x.SwitchRelease`, requiring `SUCCEEDED`.
    - `(s *suite) requireReachable(rel)`: inspects `mg-<app>-<rel>` with `s.cli` and takes its IP on `mg-<project>`. It GETs `http://<ip>:8080/healthz` with a 2 s client, retrying up to 10 s. If that never works, it skips with "container IP <ip> on mg-<project> does not answer from the host (Docker Desktop?)", or calls `t.Fatalf` under `CI`.
    - `(s *suite) firstRelease()`:
      1. deploy `r00`, then `requireReachable("r00")`, then `switchTo("r00")`;
      2. build the load config: `localCA`, dial `s.listen`, host `s.domain`;
      3. warm up: GET once a second, up to 30 s, until one answer is 200 with body `r00`. Caddy may still be issuing the internal cert.

      It returns the config.
    - `(s *suite) tenDeploys(l *load) []mark`: for `i := 1..10`, `rel := fmt.Sprintf("r%02d", i)`, deploy then `switchTo`. Each mark is `{release: rel, banned: r00..r(i-1), called, returned, within: 2s}`.
  - `load_test.go`: the load generator and its test.
    - `type loadConfig struct { addr, host string; roots *x509.CertPool; rate int }`, where rate 0 means 50.
    - `type response struct { sent, arrived time.Time; release string }`.
    - `type loadResult struct { total, failed int; firstErr error; firstErrAt, started time.Time; responses []response }`.
    - `startLoad(cfg) *load` builds one `http.Client{Timeout: 5 * time.Second}` with an `http.Transport` set to:
      - `Proxy: nil` and `DisableKeepAlives: true`;
      - `DialContext` that always dials `cfg.addr`;
      - `TLSClientConfig{RootCAs: cfg.roots, ServerName: cfg.host, MinVersion: tls.VersionTLS12}`. There is no `InsecureSkipVerify` and no system pool.

      A ticker at `time.Second / rate` starts one goroutine per tick, tracked by a `WaitGroup`, which does `GET https://<host>/`. The body is read through `io.LimitReader(…, 256)`.
    - Any error, timeout or non-2xx counts as failed. Under the mutex, the first failure is kept as a `fmt.Errorf("GET %s: %w"|"status %d", …)` together with `time.Now()`. A 2xx appends a `response` with its sent time, its arrival time and the trimmed body.
    - `(l *load) stop() loadResult` stops the ticker, waits for every in-flight request (no cancellation) and returns the result.
    - `(r loadResult) String()` gives the count, the total, the first error and its time, plus the offset from `started`. The suite tests use it in their failure messages.
  - `deploy_test.go`: the two suite tests, both `t.Parallel()`, plus `checkOrder(t, responses, marks, loadEnd)`.
    - Each mark is `{release, banned []string, called, returned time.Time, within time.Duration}`.
    - For each mark i, find the first response with `arrived ≥ returned` and `release == mark.release`. Fail unless it exists and arrives within `within` of `returned`.
    - Then fail on any response with `arrived` after that first one and before the next mark's `called` (or `loadEnd` for the last mark) whose release is in `banned`. Name the release, its arrival time and the mark.
    - Any 2xx body that is not a release of the run also fails.
    - The window ends at the next operation's call because the rollback mark legitimately brings back an older id.
- `Makefile`: add `test-deploy` to `.PHONY`, plus a target with a comment pointing at `docs/specs/deploy-suite.md` and saying `make test` runs it too:
  ```
  test-deploy:
  	CGO_ENABLED=1 go test -race -count=1 ./test/deploy
  ```
  Nothing else changes.
- `.github/workflows/ci.yml`: add a `deploy-suite` job; the `ci` job is untouched and `permissions: contents: read` stays at the top.
  - Job settings:
    - `name: deploy suite (Docker Engine ${{ matrix.engine.major }})`;
    - `runs-on: ubuntu-22.04`, `timeout-minutes: 30`;
    - `strategy.fail-fast: false`.
  - Matrix entries, with versions looked up at `download.docker.com/linux/ubuntu/dists/jammy/stable/binary-amd64/Packages` on 2026-11-20:

    | major | `docker-ce` / `docker-ce-cli` | `containerd.io` |
    |---|---|---|
    | 25 | `5:25.0.5-1~ubuntu.22.04~jammy` | `1.7.29-1~ubuntu.22.04~jammy` |
    | 26 | `5:26.1.4-1~ubuntu.22.04~jammy` | `1.7.29-1~ubuntu.22.04~jammy` |
    | 27 | `5:27.5.1-1~ubuntu.22.04~jammy` | `1.7.29-1~ubuntu.22.04~jammy` |
    | 28 | `5:28.5.2-1~ubuntu.22.04~jammy` (needs containerd.io ≥ 1.7.27) | `1.7.29-1~ubuntu.22.04~jammy` |
    | 29 | `5:29.10.0-1~ubuntu.22.04~jammy` (needs containerd.io ≥ 2.1.5) | `2.4.2-4~ubuntu.22.04~jammy` |

    Every entry uses `docker-buildx-plugin` `0.39.0-1~ubuntu.22.04~jammy`. The containerd 1.7 line is kept for 25–28 because those Engines predate containerd 2.
  - Step 1: `actions/checkout`, same SHA as `ci`, with `persist-credentials: false`.
  - Step 2, "Install Docker Engine": `set -euo pipefail`, with versions passed through `env:` from the matrix, never `${{ }}` inside `run`.
    1. Fetch `https://download.docker.com/linux/ubuntu/gpg`.
    2. Require `gpg --show-keys --with-colons` to show fingerprint `9DC858229FC7DD38854AE2D88D81803C0EBFCD88`, then install it as `/etc/apt/keyrings/docker.asc`.
    3. Write `deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu jammy stable` and run `apt-get update`.
    4. Purge whichever of the `moby-*`, `docker-*`, `containerd*` and `runc` packages are installed. The list comes from `dpkg-query`, filtered to status `installed`.
    5. Run `apt-get install -y --no-install-recommends --allow-downgrades docker-ce=$V docker-ce-cli=$V containerd.io=$C docker-buildx-plugin=$B`, then `sudo systemctl restart docker`.
  - Step 3, "Check Engine major": `v=$(docker version --format '{{.Server.Version}}')`, run without sudo so socket access is checked too. Fail with `Docker Engine $v, want major $MAJOR` unless `${v%%.*} == $MAJOR`.
  - Step 4: `actions/setup-go`, same SHA, `go-version-file: go.mod`.
  - Step 5: `run: make test-deploy`. GitHub sets `CI=true`, so a missing Engine or an unreachable container IP fails.
- `testdata/tests.txt`: `make test-list`, which adds the six named tests.
- `docs/specs/deploy-suite.md`, `docs/specs/release.md`, `docs/adr/0014-docker-client.md`, `.golangci.yml`: no change expected. Edit a spec only if the implementation shows a statement is wrong.

## Tests to write

- `./internal/deploy TestProxyPushListenSetsAddressAndDisablesRedirects`
  - Setup: `testEngine`, `testProject`, `newExecutor`, then `deployDomains(app, "r1", ["listen.example.com"])` and `makeCurrent`.
  - Push the same db once through `NewProxy(e, x.db, {Socket: fakeA, Issuer: "internal"})` and once through `{Socket: fakeB, Issuer: "internal", Listen: "127.0.0.1:8443"}`.
  - Assert on B: server `moonglass` has `listen` `["127.0.0.1:8443"]` and `automatic_https` `{"disable_redirects": true}`.
  - Assert on A: `listen` `[":443"]` and no `automatic_https` key.
  - Delete `listen` from both servers and `automatic_https` from B; the two decoded documents must then be equal (`requireJSONEqual`).
- `./internal/deploy TestProxyPushRefusesMalformedListen`
  - Table: `127.0.0.1`, `:https`, `127.0.0.1:port`, `127.0.0.1:`, `[::1`, `127.0.0.1:65536`.
  - Each case uses `NewProxy(nil, newProxyDB(t), {Socket: fake.path, Issuer: "internal", Listen: v})`.
  - `Push` returns an error containing `v`.
  - `PushRouted(ctx, state.CurrentRelease{}, commit)` returns an error containing `v`, and the commit flag stays false.
  - `fake.requestCount() == 0` and `fake.loadCount() == 0`.
  - `pusher.last.Load()` is nil before and after.
- `./internal/deploy TestScopeReconcileRemovesOnlyMatchingContainers`
  - Setup: `testEngine`, two `testProject`s `p1` and `p2`, both cleaned up. Import one image under `p1`.
  - Use `createFixture(fixtureSpec(pN, testAppID(pN, "ghost"), "r1", image, true), testContainerName(...))` for each project.
  - Run `x, _ := newExecutor`, `x.ScopeReconcile(testLabelProject+"="+p1.name)`, then `x.Reconcile(ctx)`, which must return nil.
  - Then `requireNoContainerNamed` for p1's container, and p2's container still has its original ID (`containerIDsNamed`).
- `./test/deploy TestLoadGeneratorCountsEveryNon2xx`
  - Server: `httptest.NewUnstartedServer` with `Config.ConnState` counting `StateNew`, then `StartTLS`.
  - The handler numbers requests atomically:
    - n=3 answers 418 and records the time it did;
    - n=6 answers 500;
    - n=9 answers 503;
    - n=12 hijacks and closes the connection with no response;
    - every other request answers 200 with body `r1`.
  - Run `startLoad({addr: srv.Listener.Addr(), host: "example.com", roots: pool of srv.Certificate()})`. Wait (up to 10 s) until at least 20 requests are served, then `stop()`.
  - Assert:
    - `failed == 4` exactly;
    - `total ==` requests served `==` new connections;
    - `firstErr` contains `418`;
    - `firstErrAt` is no earlier than the server's 418 time and within 1 s of it;
    - `len(responses) == total-4`, all `r1`.
- `./test/deploy TestTenDeploysUnderLoadDropNothing`
  - `newSuite`, `firstRelease`, `startLoad`, `tenDeploys`, then 2 s more of load, then `stop`.
  - Assert `failed == 0`; the message is the result's `String()`.
  - Then `checkOrder(responses, marks, end)`.
- `./test/deploy TestRollbackUnderLoadIsUnderFiveSeconds`
  - Same setup and `tenDeploys` as the previous test.
  - `called := time.Now()`, then `res := x.Rollback(ctx, &agentv1.Rollback{AppId})`, then `elapsed := time.Since(called)`. Both are monotonic.
  - Assert `SUCCEEDED` (show the detail) and `elapsed < 5s` (show elapsed).
  - Append the mark `{release: "r09", banned: ["r10"], called, returned, within: 1s}`, keep load for 2 s more, then `stop`.
  - Assert `failed == 0`, then `checkOrder`.

## Edge cases

- Malformed `Listen`, each refused before any socket request or commit (`TestProxyPushRefusesMalformedListen`):
  - no port (`127.0.0.1`);
  - a named port (`:https`, `127.0.0.1:port`);
  - an empty port (`127.0.0.1:`);
  - an unclosed bracket (`[::1`);
  - an out-of-range port (`127.0.0.1:65536`).
- An empty `Listen` gives the unchanged document: the A side of `TestProxyPushListenSetsAddressAndDisablesRedirects` and the existing `TestProxyPushBuildsCompleteConfig`, which stays untouched.
- `ScopeReconcile` must leave another run's unreferenced managed container alone (`TestScopeReconcileRemovesOnlyMatchingContainers`). Without a scope, behaviour is unchanged (`TestReconcileRemovesUnreferencedContainers`, untouched).
- Load generator failures: a non-2xx, a dropped connection (EOF) and a timeout (the 5 s client limit) each count as failed. Keep-alives off is proven by counting connections (`TestLoadGeneratorCountsEveryNon2xx`).
- No Engine: the tests skip, or fail under `CI` (`connectEngine`).
- Container IPs unreachable from the host: the tests skip, or fail under `CI` (`requireReachable`, which runs before the first switch, since the health gate would fail there too).
- The proxy exits at start-up: the test fails with its output. The proxy ignores SIGTERM: `Kill` and `t.Errorf`.
- Images retention already removed: cleanup removes only what the `reference` filter lists.
- An in-flight request at the moment of a reload finishes on the old route (Caddy's grace period). The check window opens at the first new-id response, so an old id that arrives before it is allowed, as the spec says.
- After a rollback, an older id legitimately returns, so each mark's ban window ends at the next operation's call time.
- `make test-list` must not build the binaries: `TestMain` skips the builds under `-test.list`.

## Risks

- **Counted lines.** Only `proxy.go` (about 30), `reconcile.go` and `deploy.go` (about 10), the Makefile (about 5) and `ci.yml` (about 80) count, roughly 125 against the 300 cap. Tests and `testdata` do not count.
- **Flaky ordering check.** An old-route response could arrive after the first new-id response only if it was in flight more than one 20 ms tick longer than a later request. Requests have no keep-alive and the upstream answers in about a millisecond, so this is unlikely. The check follows the spec's wording, and a failure names the response and its times so a real reorder is visible.
- **Certificate issuance after the first push is asynchronous.** The 30 s warm-up before load starts covers it. Load starts only after one `r00` answer over TLS that trusts only the local CA.
- **Port reuse race.** Another process could take the reserved port before Caddy binds it. This is the spec's accepted design. The failure is loud: the first switch's push fails with Caddy's bind error.
- **Run time.** About 11 health gates of about 2 s each, plus imports, per test. The two suite tests run in parallel, well inside `go test`'s default 10-minute timeout under `-race`. `TestMain` builds `moonglass` once; on CI that is about a minute cold.
- **Runner Docker conflicts.** The preinstalled Docker or Moby packages are purged before the pinned install, `--allow-downgrades` covers 25–28, and the major check proves which Engine ran. The runner user stays in the `docker` group, and the unprivileged `docker version` proves socket access.
- **Supply chain.** Actions use the same commit SHAs as `ci`. The apt key's fingerprint is verified. Every package version is exact. Matrix values go through `env:`, not inline expressions.
- **Threat model.**
  - §6: every test container goes through `Executor.Deploy` with an empty `agent.toml`, so the sanitizer runs as in production. There is no host network or bind mount.
  - §8: the suite runs only `go build` and the built binary.
  - §3: production never sets `Listen` or calls `ScopeReconcile`. The worker confirms that `git grep -n 'Listen:' -- cmd/ internal/agent/` and `git grep -n ScopeReconcile -- cmd/ internal/agent/` find nothing.
- **Banned words.** Avoid "fallback" and "legacy" in comments, error strings and the YAML.
- **No dependency change.** `go.mod` and `go.sum` stay as they are: `github.com/moby/moby/client` and `/api` are already required, and everything else is the standard library or this module.
