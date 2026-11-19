---
verdict: ready
reason: ""
---

# Plan for W2-4a

The ticket fully settles behaviour. It needs no contract change: there is no new protocol field, no registry operation and no migration, because `apps.current_release_id` and `releases.domains/port/container_id` already exist. It adds no new module (stdlib `net/http` plus the moby client from ADR-0014) and no new ADR, and the threat model §3 B4/B5/B8 covers it.

## Files to change

- `internal/agent/state/state.go`:
  - `type CurrentRelease struct { Binding Binding; Record Record }`.
  - `func (d *DB) CurrentReleases(ctx context.Context) ([]CurrentRelease, error)` runs one query: `SELECT a.app_id, a.project, a.environment, a.service, r.release_id, r.image_ref, r.image_id, r.repo_digest, r.port, r.health_path, r.domains, r.has_pre_deploy, r.container_id FROM apps a JOIN releases r ON r.app_id = a.app_id AND r.release_id = a.current_release_id ORDER BY a.app_id`.
  - It decodes `domains` the way `Release` does. Errors are wrapped as `read current releases from agent state <path>: …`. An app with no current release drops out because of the inner join. It adds no imports.
- `internal/deploy/deploy.go`, in `Executor.Deploy`: after `st.Deploy` succeeds and `rec` is built, and before `x.prepare`, it calls `if err := x.checkDomains(ctx, appID, d.GetDomains()); err != nil { return x.fail(ctx, rec, st, err) }`.
  - `func (x *Executor) checkDomains(ctx context.Context, appID string, domains []string) error` reads `x.db.CurrentReleases`. A read error is returned as is, which gives `FAILED` with a failed row.
  - It builds `owner map[string]string` (domain → app_id) from the current releases whose `AppID != appID`. Then it walks `domains` in the order sent, and the first hit returns `rejected(fmt.Errorf("domain %s is already routed to app %s", name, owner))`.
  - The existing `fail` writes the `failed` row and maps the error to `REJECTED`. Candidates are never in `CurrentReleases`, so they never hold a domain.
- `internal/deploy/proxy.go` (new, about 230 lines). It never imports `internal/proxy` or Caddy.
  - `type ProxyConfig struct { Socket string; Issuer string; OnError func(error) }`.
  - `type Proxy struct { engine *Engine; db *state.DB; cfg ProxyConfig; client *http.Client; mu sync.Mutex; last atomic.Pointer[[]byte] }`.
  - `func NewProxy(engine *Engine, db *state.DB, cfg ProxyConfig) *Proxy`. The client's `http.Transport` has `DialContext` that ignores the address and dials `unix` at `cfg.Socket`. It sets `DisableKeepAlives: true`, so a restarted proxy never meets a stale pooled connection. Requests go to `http://localhost<path>`.
  - `func (p *Proxy) do(ctx context.Context, method, path string, body []byte) ([]byte, error)` wraps each call in `call(ctx)`, which gives the 60 s limit. It reads at most 16 MiB with `io.LimitReader`. A dial or transport error becomes `<method> <path> on proxy admin socket <sock>: %w`. A non-2xx answer becomes `<method> <path> on proxy admin socket <sock>: status <code>: <truncate(body)>`. It is the only HTTP path, and it is used only for `GET /config/admin`, `GET /config/storage`, `GET /config/` and `POST /load` (Content-Type `application/json`).
  - `func tlsApps(issuer string) (map[string]any, error)`:
    - `acme` gives `{"module":"acme"}`.
    - `acme-staging` gives `{"module":"acme","ca":"https://acme-staging-v02.api.letsencrypt.org/directory"}`.
    - `internal` gives `{"module":"internal"}` and also `pki: {"certificate_authorities":{"local":{"install_trust":false}}}`.
    - Anything else is an error: `TLS issuer %q is not one of acme, acme-staging or internal; pass one of those`.
    - It returns the `tls` (and, for `internal`, `pki`) entries of `apps`.
  - `func (e *Engine) containerIP(ctx context.Context, id, network string) (netip.Addr, error)` calls `ContainerInspect` with `call(ctx)` and returns `NetworkSettings.Networks[network].IPAddress`. A nil endpoint or an invalid address is an error: `has no address on network <network>`.
  - `func (p *Proxy) Push(ctx context.Context) error`:
    1. Lock `p.mu` for the whole call.
    2. Call `tlsApps(p.cfg.Issuer)`, which errors before any request.
    3. `GET /config/admin` and `GET /config/storage`, kept as `json.RawMessage`, which repeats them verbatim.
    4. `p.db.CurrentReleases(ctx)`.
    5. For each release with `len(Domains) > 0`: an empty `ContainerID` is an error. Otherwise call `containerIP(…, "mg-"+Binding.Project)`. A failure is `route app %s release %s: container %s: %w` (it says to deploy again).
    6. Build `routes` in that order. Each route is `{"match":[{"host":domains}],"handle":[{"handler":"moonglass_counter","release":id},{"handler":"reverse_proxy","upstreams":[{"dial":net.JoinHostPort(ip.String(), strconv.Itoa(port))}]}],"terminal":true}`.
    7. Set `http := {"grace_period":"30s"}`, plus `servers: {"moonglass": {"listen":[":443"],"routes":routes}}` only when there is at least one route.
    8. Marshal `{"admin","storage","logging":{"logs":{"default":{"level":"WARN"}}},"apps":{"http","tls" querida[,"pki"]}}`.
    9. `POST /load`. Only on success does it run `p.last.Store(&body)`.
  - `func (p *Proxy) Run(ctx context.Context) error`:
    - It calls `p.report(ctx, p.Push(ctx))`, then runs `time.NewTicker(time.Second)`.
    - Each tick calls `p.differs(ctx)`, and when that is true, `p.report(ctx, p.Push(ctx))`.
    - It returns `nil` on `ctx.Done()`.
    - `report` calls `OnError(err)` only when `err != nil`, `OnError != nil` and `ctx.Err() == nil`, so cancellation is not reported.
    - `differs` runs `GET /config/`. A failure gives `false`, which means no push and nothing reported. If `last` is nil it gives `true`. Otherwise it decodes both with `json.Unmarshal` into `any`: a decode error on the running body gives `true`, and the result is `!reflect.DeepEqual`.
- `docs/specs/release.md`, "Proxy config": `ProxyConfig{Socket, Issuer, OnError}`. It names server `moonglass` with `listen [":443"]` (Caddy adds the :80 redirect), `logging` WARN, `grace_period` 30s, and the no-routes rule (only `grace_period`, no server). Routes are in `app_id` order with `terminal: true`. It gives the exact issuer JSON with `pki` for `internal` only, the bad-issuer error, and the gone-container error (nothing posted). The last pushed is recorded only on a 2xx `/load`.
- `docs/specs/proxy.md`, "Admin API": one line saying the config the agent pushes is specified in `docs/specs/release.md`, "Proxy config".
- `testdata/tests.txt`: `make test-list`.

## Tests to write

- `./internal/agent/state TestCurrentReleasesListsEveryAppsCurrentRelease` (in `state_test.go`, appended):
  - Seeding uses `mustDeploy`/`mustRecord` with `sampleRecord`. App `a` keeps the domains; app `b` has `Domains: nil, Port: 0`; app `c` is a candidate only. App `d` gets only `RecordFailed`.
  - Current releases come from `d.db.ExecContext("UPDATE apps SET current_release_id = candidate_release_id, candidate_release_id = NULL, phase = 'none' WHERE app_id = ?")` for `a` and `b`.
  - It asserts `CurrentReleases` deep-equals `[{sampleBinding(), recA}, {sampleBinding(), recB with Domains []string{}}]`. That second entry is the stored JSON `[]` decoded, so the test asserts the decoded form.
  - It also asserts that a fresh DB returns an empty result and nil.
- `./internal/deploy TestDeployRejectsDomainRoutedByAnotherApp` (new `internal/deploy/domains_test.go`, Docker). It is table-driven, and each case has its own executor and project. Its helpers, added in that file:
  - `deployDomains(t, e, x, p, app, rel string, domains []string) *agentv1.CommandResult` imports the image, sets `Port 8080` and `Domains`, and deploys.
  - `makeCurrent(t, dataDir, app string)` is a direct SQL `UPDATE apps …` as above.

  The cases:
  1. Other app current with `x.example.com`. A Deploy with `[y.example.com, x.example.com]` is `REJECTED` with detail exactly `domain x.example.com is already routed to app <other>`. `storedRow` has status `failed` and `Err` equal to the detail. `storedApp` is unchanged (`known=false`). `requireNoContainerNamed`.
  2. Other app candidate only: `SUCCEEDED`.
  3. Own current release `r1` with the domain, then `r2` keeps it: `SUCCEEDED`.
  4. Own candidate exists (illegal second Deploy) with a domain the other app's current routes: `REJECTED`, the detail contains `illegal` (it is the transition error, not `already routed`), and `storedRow(r2)` is not found.
- `./internal/deploy TestProxyPushBuildsCompleteConfig` (new `internal/deploy/proxy_test.go`, Docker). It uses a fake admin socket helper `fakeAdmin` in the same file:
  - It is an `httptest.NewUnstartedServer` whose `Listener` is `net.Listen("unix", <os.MkdirTemp("/tmp","mgpush")>/admin.sock)`.
  - It holds fixed `admin` `{"listen":"unix//x|0600"}` and `storage` `{"module":"file_system","root":"/x"}`, a mutex-guarded `loads [][]byte` and a request count. It has settable `loadStatus`/`loadBody`/`loadDelay`, `inflight`/`maxInflight` counters, and `configAnswer` (the last load or a fixed body).
  - It has `close()` and `reopen()` at the same path.

  The test seeds these releases through `deployDomains` plus `makeCurrent` on one executor's DB (`x.db`):
  - app `a` current with two domains, port 8080;
  - app `b` current with one domain, port 8081;
  - app `c` current with no domains;
  - app `d` candidate with domains.

  `NewProxy(e, x.db, {Socket, Issuer:"internal"})`, and `Push` returns nil. The test asserts exactly one load, which, decoded, deep-equals an expected document built by hand in the test. The IPs come from `requireContainer(...).NetworkSettings.Networks["mg-"+p.name].IPAddress`. A second sub-case uses fresh empty state, and `apps.http` equals `{"grace_period":"30s"}`.
- `./internal/deploy TestProxyPushIssuers` (no Docker, empty state, nil engine): table over `acme`, `acme-staging` and `internal`. It checks `apps.tls` exactly and that `apps.pki` is present only for `internal` (and equals the spec value). The `letsencrypt` case returns an error containing `letsencrypt`, `acme`, `acme-staging` and `internal`, and the fake has 0 requests.
- `./internal/deploy TestProxyPushFailuresPushNothing`: a table of subtests.
  - No socket (a path in an empty temp dir): error, 0 loads.
  - `/config/admin` 500: error containing `500`, 0 loads.
  - `/load` 400 with body `bad config here`: error contains `400` and `bad config here`, 1 load.
  - Removed container (Docker subtest using `testEngine`): a routed current release, its container force-removed through `e.cli.ContainerRemove`. The error contains the app id, the release id and the container id, and there are 0 loads.

  The last part keeps `/load` at 400 and sets `configAnswer` to the failed body. It starts `Run`, and within 3 s it sees at least 2 loads (the start push and a probe-driven push), which proves that a failed push is not recorded as last.
- `./internal/deploy TestProxyPushesOneAtATime` (no Docker): `loadDelay` 50 ms, 10 goroutines calling `Push` behind a `sync.WaitGroup`. All return nil, there are 10 loads, and `maxInflight == 1`.
- `./internal/deploy TestProxyRunPushesAgainWhenRunningConfigDiffers` (no Docker): `configAnswer` is the last load, and `OnError` appends to a mutex-guarded slice. The test polls the load count with deadlines:
  1. 1 load within 2 s.
  2. Still 1 after 3 s.
  3. It switches `configAnswer` to a fixed start-up config, and there is a second load within 3 s.
  4. It closes the socket for 2.5 s (several probes) and asserts `OnError` is empty. It reopens with the start-up config answer and expects a new load within 3 s.
  5. It sets `loadStatus` 400 and the start-up answer, and expects an `OnError` error containing `400` within 3 s. The `/config/` request count keeps rising afterwards.
  6. It cancels and expects `Run` to return nil within 2 s.
- `./internal/proxy TestAgentPushLoadsIntoProxy` (new `internal/proxy/agent_push_test.go`, `package proxy_test`):
  - `deploy.Connect`, which skips without an Engine except under `CI`, where it is `t.Fatalf`.
  - A local `runStoppable(t, dataDir, sock) (stop func())` helper (a cancellable `proxy.Run`, waiting for `GET /config/`), plus a unix-socket admin client. Existing `runProxy` cannot stop early, and existing test files only gain lines.
  - It uses `tempProxyDir`, `state.Open(t.TempDir())`, and `deploy.NewProxy(engine, db, {Socket: sock, Issuer: "internal"})`. `Push` returns nil, and `GET /config/apps/http/grace_period` decoded is `"30s"`.
  - It saves `GET /config/` decoded as `want`, then starts `Proxy.Run` in a goroutine. It stops the proxy and starts it again on the same paths. Within 5 s, `GET /config/` decoded deep-equals `want`. Then it cancels `Run` and checks the nil return.

## Edge cases

- **Unknown issuer.** It errors before any socket read or post (`TestProxyPushIssuers`).
- **Missing socket, non-2xx GET, non-2xx `/load`.** Each returns an error, with the body truncated through `truncate`. Nothing is posted after a GET fails (`TestProxyPushFailuresPushNothing`).
- **Current container gone or stopped.** There is no address on `mg-<project>`, so the push errors naming the app, release and container, and nothing is posted (same test). An empty `container_id` is the same error path.
- **A failed push is never recorded as last.** The `Run` probe pushes again (same test).
- **Concurrent pushes.** They are serialised by `mu` (`TestProxyPushesOneAtATime`). `last` is atomic, so `Run`'s probe never races `Push` (the race detector runs in `make test`).
- **Proxy restart or socket gone.** The probe fails silently, and the next successful probe sees the start-up config and pushes (`TestProxyRunPushesAgainWhenRunningConfigDiffers` and `TestAgentPushLoadsIntoProxy`).
- **Push failure during `Run`.** It goes to `OnError` and `Run` continues. Cancellation errors are not reported, and `Run` returns nil.
- **Domain conflict order.** The first conflicting domain in sent order is named. Case 4 checks that a transition error takes precedence. The own app and other apps' candidates never conflict (`TestDeployRejectsDomainRoutedByAnotherApp`).
- **No routes.** There is no server, so nothing listens on :443 (`TestProxyPushBuildsCompleteConfig` empty case, and `TestAgentPushLoadsIntoProxy` loads it into real Caddy).
- **IPv6 container address.** `net.JoinHostPort` brackets it.
- **Large running config.** Reads are bounded at 16 MiB.

## Risks

- **depguard.**
  - `internal/deploy` imports only stdlib, `state` and moby (`deploy-deps` stays clean).
  - The new `internal/proxy` test imports `internal/deploy` and `internal/agent/state` but no moby or modernc directly, so `docker-deps` and `sqlite-deps` pass. The import is test-only, so the proxy binary and its memory budget are unchanged.
  - `state` gains no import, so `TestPackageImportsReleaseAndStoreOnly` still passes.
- **Unix socket path length on macOS.** The fake socket goes in `os.MkdirTemp("/tmp", …)`, as `tempProxyDir` does.
- **Caddy round-trip equality.** `GET /config/` returns the raw loaded JSON. The comparison decodes both sides the same way, so key order and whitespace don't matter.
- **Timing flake.** The ticker is 1 s against 2–5 s windows. Assertions poll with deadlines, never fixed sleeps, except the "no second post in 3 s" and "socket closed" windows that the ticket specifies. Keep-alives are disabled, so a reopened socket is never hit through a dead pooled connection.
- **Banned words** (`fallback`, `legacy`, and so on): avoided in code and comments.
- **Diff size.** Product code is about 230 (`proxy.go`) + 25 (`deploy.go`) + 45 (`state.go`), about 300 of the 450 cap. Tests and docs don't count.
- **Allowed paths.** Every change is inside `internal/deploy/`, `internal/agent/state/` and `internal/proxy/` (the test only), the two specs, and `testdata/tests.txt`.
- **Existing tests are only appended to.** The `tests-kept` gate passes.
