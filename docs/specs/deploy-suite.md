# Docker in tests, and the deploy-correctness suite

How tests reach a real Docker Engine, and what W2-7 builds. Specified by the weeks 2–3 preflight (owner decision D12). Sources: `AGENTS.md` "Testing rules", stack §3.2 and §7, M§7.

## Tests that need Docker

- A test that needs the Engine uses `DOCKER_HOST`, or `/var/run/docker.sock` when that is unset. When no Engine answers, the test **skips**, except when `CI` is set, in which case it **fails**: CI must provide Docker. This is the same rule as Postgres in `docs/specs/storage.md`.
- Each test uses a project name unique to its run (`t` plus 12 random lowercase hex characters), so every container and `mg-<project>` network it creates carries that `dev.moonglass.project` label. The test removes everything with that label when it ends, pass or fail. Two worktrees can run the suite at once.
- **Test images are imported, not pulled.** A test builds a small static Go HTTP server (`CGO_ENABLED=0`) into a root filesystem tar and imports it with the Engine's image import, under `moonglass.local/<test project>/<service>:<release_id>`. That works on Engine 25 to 29 and needs no registry and no builder.
- **Pull and registry tests** run a local registry, `registry:2` pinned by digest, on a `:0` port, with basic auth when the test needs it. It is the only image pulled from Docker Hub.

## The deploy-correctness suite (W2-7)

- Package `test/deploy`. It drives `internal/deploy` and a real `moonglass proxy` against a real Engine.

### The proxy under test

Owner decision of 2026-11-20 (the W2-7 blocker): the proxy runs on the host, as the test user, on a loopback port, and the pushed document listens there.

- `TestMain` builds `./cmd/moonglass` once into a temp directory (`CGO_ENABLED=0`). Each test run starts that binary as `moonglass proxy --data-dir <temp> --admin-socket <temp>/admin.sock`, waits until the admin socket answers `GET /config/`, and stops it with SIGTERM when the run ends, pass or fail. The temp directory comes from `os.MkdirTemp("", "mgd")`, so the socket path stays under the 104-byte Unix socket limit of macOS.
- The run reserves a port by listening on `127.0.0.1:0` and closing that listener, and builds its `deploy.Proxy` with `deploy.ProxyConfig{Socket: <admin socket>, Issuer: "internal", Listen: "127.0.0.1:<port>"}` (`docs/specs/release.md`, "Listen"). Nothing binds :443 or :80, no root is needed, and two worktrees can run the suite at once.
- The proxy reaches each release at its container IP on `mg-<project>`, as in production. That works on a Linux host, where bridge networks are routed from the host; the suite runs on Linux CI. On a host where a container IP does not answer (Docker Desktop), the suite skips with that reason, except under `CI`, where it fails.
- The load generator trusts only the proxy's local CA root (`GET /pki/ca/local` over the admin socket, `docs/specs/proxy.md`) and sends every request to `127.0.0.1:<port>` with the test domain as TLS server name and `Host`. The test domain is `<test project>.moonglass.test`.

### The test image

- One small static Go HTTP server (standard library), built and imported as "Test images are imported, not pulled" says, once per release, under `moonglass.local/<test project>/web:<release_id>`. Each release's image differs only in the release id it answers with.
- It listens on port 8080, answers `GET /healthz` with 200, and answers every other path with 200 and a body of exactly its release id. It exits on SIGTERM after finishing the requests in flight.
- Each `Deploy` has `port` 8080, `health_path` `/healthz`, the test domain, and no pre-deploy step.

### What the suite asserts

- **The load generator** is our own Go code (standard library), sending steady requests (default 50 per second) through the proxy and counting every request that does not get a 2xx response. It opens a new connection for every request (keep-alives off), so each request is routed by the config live when it was sent. A request has a 5-second limit. When its connection closes before any byte of an answer arrived, and the request did not time out, the generator sends it once more on a new connection, as RFC 9110 §9.2.2 allows for an idempotent `GET` and as browsers do: a config reload ends the old Caddy server, which closes a connection still in its TLS handshake, or one whose request it has not started to read, without answering (measured 2026-11-20: 0 to 2 such cuts per 30 reloads at 50 requests per second, none with keep-alives on, none without TLS). The retry is counted and shown in the result; it is not a failed request. A non-2xx answer, a timeout, a connection error after the first answer byte, or a retry that fails again is a failed request. One failed request fails the test, and the failure message shows the count, the retries, the first error and when it happened.
- The suite runs `Executor.Run` for the whole test, so stops and retention happen as in production. Before `Run` it calls `Executor.ScopeReconcile("dev.moonglass.project=<project>")`, so the start-up reconcile over its fresh agent database removes only its own run's containers, never those of another test or worktree on the same Engine (`docs/specs/release.md`, "Run").
- **Ten deploys.** A first release is deployed and switched before the load starts. Then ten more releases follow under load, each a `Deploy` then a `SwitchRelease` through the executor, each `SUCCEEDED`. After each switch, a response with the new release id arrives within 2 seconds, and no request sent after the switch returned is answered with an older release id. A request already in flight when the switch returned may finish on the old route (`docs/specs/release.md`, "Draining"), so the check excuses an older id whose request was written before the switch returned; the load generator records that write time with `httptrace`.
- **Rollback under 5 seconds.** After the last switch, the test keeps load running for 200 ms, so the last release answers under steady load, and then calls `Executor.Rollback` under load. It must return `SUCCEEDED` less than 5 seconds after it was called, measured in the test with the monotonic clock: that is what "a rollback under 5 seconds" means here, in W2-5 and in gate G2. Within 1 second after it returns, a response carries the release before the last one, and no request sent after it returned is answered with the release it rolled back from.
- Zero failed requests across all ten deploys and the rollback. Named tests: `TestTenDeploysUnderLoadDropNothing` and `TestRollbackUnderLoadIsUnderFiveSeconds` (both in `test/deploy`), and `TestLoadGeneratorCountsEveryNon2xx` and `TestLoadGeneratorRetriesOnceACutBeforeAnswer` for the load generator itself against an `httptest` server that fails a known number of requests.
- `make test-deploy` runs only this suite (`go test -race -count=1 ./test/deploy`). `make test` runs it too.

## The Docker matrix (W2-7)

- A CI job per Engine major, 25 to 29, on `ubuntu-22.04`. Docker 25 is published only for Ubuntu 22.04 (`jammy`), not 24.04 (`noble`), at download.docker.com (checked 2026-11-17).
- Each job installs `docker-ce`, `docker-ce-cli`, `containerd.io` and `docker-buildx-plugin` at an exact version from Docker's apt repository, pinned in `ci.yml` (the newest patch of that major when W2-7 is built), then runs `make test-deploy`.
- The matrix runs on every push to `main`, nightly, and on a pull request that changes `internal/deploy/`, `internal/build/`, `internal/agent/disk/`, `test/deploy/`, `go.mod`, `go.sum`, the `Makefile` or `ci.yml`. Every other pull request still runs this suite once, in `make test` on `ubuntu-latest` with the runner's Docker.
- The required `ci` check needs the matrix: it fails when any Engine job fails, and passes a skipped matrix only on a pull request that does not touch those paths. No matrix job is a required check of its own.
- When GitHub retires `ubuntu-22.04` runners, the Engine 25 job needs a new home. That is a plan decision, not a quiet removal.
