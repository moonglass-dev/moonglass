---
verdict: ready
reason: ""
---

# Plan for W2-4c

No contract, ADR, module or security decision is needed. `deploy.NewProxy` and `Proxy.Run` (W2-4a), `deploy.Connect` (ADR-0014), `state.Open` (`docs/specs/agent-state.md`) and the flag behaviour (`docs/specs/connection.md`, "Commands") all exist and are settled. The synopsis is a `LocalCommands` string, not a registry operation. All the wiring stays in `cmd/moonglass`; `internal/agent` is not touched.

## Files to change

- `cmd/moonglass/main.go`:
  - New `type agentConnectArgs struct { dataDir, url string; proxy deploy.ProxyConfig }`. Parsing sets only `Socket` and `Issuer`; the run function sets `OnError`.
  - New `func parseAgentConnect(args []string) (agentConnectArgs, bool)`. Its flags:
    - `--data-dir`, default `/var/lib/moonglass/agent`
    - `--url`
    - `--proxy-socket`, default `proxy.DefaultAdminSocket`. `internal/proxy` already exports this constant, and `TestProxyAdminSocketDefault` pins it to `/run/moonglass-proxy/admin.sock`.
    - `--tls-issuer`, default `acme`

    It returns false on a parse error, a positional argument, an empty `--url`, an empty `--proxy-socket`, or an issuer other than `acme`, `acme-staging` or `internal`. The issuer check is a plain `switch`: `deploy.issuerApps` is unexported and outside `allowed_paths`, and it stays as the second check inside `Push`.
  - `runAgentConnect(args, stderr)` keeps its signature for the `local` map. It creates the `signal.NotifyContext` and calls `agentConnect(ctx, args, stderr)`.
  - New `func agentConnect(ctx context.Context, args []string, stderr io.Writer) int`, in this order:
    1. Call `parseAgentConnect`. On false, write `usage` to stderr and return 2. Nothing is opened before this step.
    2. Wrap stderr in a `lockedWriter` (`struct{ mu sync.Mutex; w io.Writer }` whose `Write` takes the lock), because the connect loop and the push goroutine both report errors. One `report := func(err error){ fmt.Fprintf(w, "moonglass: %v\n", err) }` serves as both `agent.ConnectOptions.OnError` and `ProxyConfig.OnError`.
    3. `engine, err := deploy.Connect(ctx)`. On error, print `moonglass: <err>` and return 1.
    4. `db, err := state.Open(ctx, a.dataDir)`. On error, close the engine, print the error and return 1.
    5. Create `runCtx, cancel := context.WithCancel(ctx)`, `p := deploy.NewProxy(engine, db, a.proxy)` and `done := make(chan struct{})`, then start `go func(){ _ = p.Run(runCtx); close(done) }()`. `Run` returns nil only when its context ends.
    6. Run `err = agent.Connect(runCtx, a.dataDir, a.url, opts)`. Then, explicitly and in this order: `cancel()`, `<-done`, `db.Close()`, `engine.Close()`. This makes sure `Run` has finished before the database closes, including when `Connect` fails early (for example, no keys). Close errors are printed and make the exit code 1. A `Connect` error is printed and returns 1. Otherwise the command returns 0.
  - Imports added: `sync`, `internal/deploy`, `internal/agent/state` and `internal/proxy` (`proxy.go` already links it into the binary). The Docker client is not imported (§3 B5, ADR-0014).
- `internal/registry/routes.go`: the `agent connect` synopsis becomes `agent connect [--data-dir DIR] [--proxy-socket PATH] [--tls-issuer ISSUER] --url URL`. Nothing else in the file changes.
- `docs/specs/connection.md`: unchanged, because the built behaviour matches it.

The counted diff (non-test, non-docs) is about 70 lines, under the 150-line cap.

## Tests to write

- `./cmd/moonglass TestAgentConnectProxyFlagDefaults`, in a new `cmd/moonglass/connect_test.go`. A table over `parseAgentConnect`:
  - `--url https://x` alone parses, with `Socket == "/run/moonglass-proxy/admin.sock"` and `Issuer == "acme"`.
  - `--proxy-socket /tmp/a.sock --tls-issuer internal` gives those values.
  - `--tls-issuer acme-staging` gives that value.

  Failure messages print the expected and actual values. No socket is dialled.
- `./cmd/moonglass TestAgentConnectRejectsBadProxyFlags`. A table with two rows, `--proxy-socket ""` and `--tls-issuer letsencrypt`, each also given `--data-dir <tempdir> --url https://127.0.0.1:1` and run through `run([]string{"agent","connect",...})`. For each row:
  - the exit code is 2
  - stderr is exactly `usage`
  - stdout is empty
  - `<tempdir>/agent.db` does not exist (`os.Stat` returns `fs.ErrNotExist`)
- `./cmd/moonglass TestAgentConnectRunsProxyPush`:
  - **Engine check:** call `deploy.Connect(ctx)`. On error, `t.Fatalf` when `CI` is set and `t.Skipf` otherwise, as `testEngine` in `internal/deploy` does. Close the engine afterwards.
  - **Enrol:** start `newTestServer` from `enroll_test.go`, then `run(agent enroll --data-dir dir --url url token)` must return 0.
  - **Refusing URL:** `net.Listen("tcp","127.0.0.1:0")`, note the address, close the listener, and pass `--url https://<addr>`.
  - **Admin socket:**
    - Make a short directory with `os.MkdirTemp("", "mg")`, because macOS limits Unix socket paths to 104 bytes.
    - Listen with `net.Listen("unix", dir+"/admin.sock")`, set that as the `Listener` of `httptest.NewUnstartedServer(mux)`, and call `Start()`.
    - The mux answers `GET /config/admin` with `{"listen":"unix//x"}`, `GET /config/storage` with `{"module":"file_system","root":"/x"}` and `GET /config/` with `{}`.
    - `POST /load` reads the body, sends it on a buffered channel and answers 200.
  - **Run:** call `agentConnect(ctx, args, &syncBuffer)` in a goroutine with a cancellable context, `--proxy-socket` set to that socket, and `--tls-issuer internal`.
  - **Push:** one body arrives on the channel within 5 seconds. It unmarshals as JSON, its `admin` and `storage` equal the served values, and `apps.http` and `apps.tls` are present.
  - **Keeps running:** after about 1.5 more seconds the command has not returned. Re-pushes are allowed, because the served `/config/` differs from the pushed document.
  - **Exit:** cancel the context. The command returns within 10 seconds with exit 0, which is what a cancelled `agent.Connect` gives today.
  - **Database closed:** `<dir>/agent.db-wal` does not exist (in WAL mode the last close removes it), and `state.Open` followed by `Close` on the directory succeeds.
  - stderr is a buffer type guarded by a mutex, defined in the test file, so `-race` stays clean.
- `./internal/registry TestAgentConnectSynopsisNamesProxyFlags`, in a new `internal/registry/routes_test.go` (package `registry_test`). It finds the `agent connect` entry in `registry.LocalCommands` and fails if there is none. `Synopsis` must equal `agent connect [--data-dir DIR] [--proxy-socket PATH] [--tls-issuer ISSUER] --url URL`, and the failure message shows the expected and actual values.
- Run `make test-list` to add the four tests to `testdata/tests.txt`.

## Edge cases

- **Empty `--proxy-socket` or unknown issuer:** exit 2 before anything is opened. Covered by `TestAgentConnectRejectsBadProxyFlags`, which checks that no `agent.db` appears.
- **Missing `--url`, positional arguments or an unknown flag:** still exit 2 through the same false return of `parseAgentConnect`. This is today's behaviour, and `TestUsageOnMissingOrUnknownCommand` is unchanged.
- **No Docker Engine:** the `deploy.Connect` error goes to stderr with exit 1. `state.Open` never runs, so no `agent.db` is created.
- **Missing data directory:** the `state.Open` error names the path and says to create the directory. The engine is closed first, then the command exits 1.
- **Not enrolled:** `agent.Connect` returns the key error at once. The push goroutine is cancelled and waited for before the database closes, and the command exits 1.
- **Proxy down or socket missing:** `Run` ignores a failed probe, and push errors go to `OnError`, so the command keeps running. The refusing-URL part of `TestAgentConnectRunsProxyPush` covers the matching case for the connection: connect errors are printed and the command continues.
- **Two goroutines writing stderr:** handled by `lockedWriter`, and exercised under `-race` by `TestAgentConnectRunsProxyPush`.
- **Hostile flag values:**
  - No shell is used.
  - `NewProxy` dials the socket path only as `unix`. No TCP admin port is opened, and the command never creates the socket (§3 B4).
  - No key or token is printed. Errors come from existing wrapped errors, which carry no key material.

## Risks

- **Race detector on the shared stderr:** `lockedWriter` in the code and a locked buffer in the test.
- **Unix socket path too long on macOS** (`t.TempDir()` paths are long): use `os.MkdirTemp("", "mg")` with `t.Cleanup(os.RemoveAll)`.
- **Two worktrees running the tests at once:** ports come from `:0` and directories are temporary. The test creates no Docker resources: agent state is empty, so `Push` makes no Engine calls.
- **Flaky 5-second window:** `Run` pushes as soon as it starts, so the first `/load` arrives within milliseconds.
- **CI without Docker:** `TestAgentConnectRunsProxyPush` is meant to fail there. CI already provides an Engine for `internal/deploy`.
- **Wrong shutdown order** (closing the database while `Push` reads it): the explicit cancel, wait, close sequence prevents it, and the WAL-file check tests it.
- **Trusted-core review of `routes.go`:** the only change is the one synopsis string. The `cli` tests that compare the handlers with `LocalCommands` match on names, not synopses, so they stay green.
- **Issuer list duplicated in `cmd` and `deploy`:** kept on purpose, because the ticket requires rejection before anything opens and `deploy` is outside `allowed_paths`. `deploy` still refuses unknown issuers in `Push`.
