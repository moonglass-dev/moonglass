---
verdict: ready
reason: ""
---

# Plan for W2-10b

## Files to change

- `internal/deploy/policyenv.go` (new):
  - `func PolicyEnv(ctx context.Context, e *Engine) (policy.Env, error)`. It makes one `e.cli.Info(callCtx, client.InfoOptions{})` call under `call(ctx)`. On error it returns `fmt.Errorf("read Docker Engine info for agent.toml: %w", err)`.
  - It returns `policy.Env{Paths: policy.Paths{MountsRoot: "/var/lib/moonglass/mounts", DockerRoot: info.DockerRootDir, DockerSockets: []string{"/var/run/docker.sock", "/run/docker.sock"}, ContainerdSockets: []string{"/run/containerd/containerd.sock"}}, DefaultRuntime: info.DefaultRuntime, ContainerProject: func(ref string) (string, bool) { return e.containerProject(ctx, ref) }}`.
  - `func (e *Engine) containerProject(ctx context.Context, ref string) (string, bool)`: `ContainerInspect` under `call(ctx)` (the 60-second limit). Any error, a nil `Config`, or a missing or empty `labelProject` gives `"", false`. Otherwise it gives the label and true.
- `internal/agent/dispatch.go` (new):
  - `type Executor interface { Deploy(ctx context.Context, requestID string, d *agentv1.Deploy) *agentv1.CommandResult; SwitchRelease(ctx context.Context, requestID string, s *agentv1.SwitchRelease) *agentv1.CommandResult; Rollback(ctx context.Context, requestID string, r *agentv1.Rollback) *agentv1.CommandResult; RunPreDeploy(ctx context.Context, requestID string, r *agentv1.RunPreDeploy) *agentv1.CommandResult }`. `*deploy.Executor` satisfies it; `internal/agent` does not import `internal/deploy`.
  - `type command struct { run func(context.Context) *agentv1.CommandResult; id, appID string }`.
  - `func newCommand[M interface{ proto.Message; GetAppId() string }](env *agentv1.Envelope, msg M, run func(context.Context, string, M) *agentv1.CommandResult) (command, error)`: `protocol.Open(env, msg)`, then a command with the envelope's request id, `msg.GetAppId()`, and `run` bound to that id and msg. An Open error is wrapped as `connect: control plane sent a %v that does not open: %w`.
  - `func openCommand(env *agentv1.Envelope, x Executor) (command, error)`: one switch that lists every `MessageType` value, so `exhaustive` passes and a new type has to be decided here. `DEPLOY` maps to `newCommand(env, &agentv1.Deploy{}, x.Deploy)`, and the same for `SWITCH_RELEASE`, `ROLLBACK` and `RUN_PRE_DEPLOY`. Every other value, and `default`, returns `connect: control plane sent %v, which the agent does not accept: closing the connection`.
  - `type dispatcher struct { ctx context.Context; onError func(error); live *liveConn; pending map[string]bool; queues map[string][]command; wg sync.WaitGroup; mu sync.Mutex }`. `pending` holds the request ids that are queued or running; `queues` holds each app's FIFO, whose head is the running command. `type liveConn struct { c *websocket.Conn; failed chan<- error }`. Field order follows `fieldalignment`.
  - `newDispatcher(ctx context.Context, onError func(error)) *dispatcher`.
  - `attach(c, failed)` sets `live`. `detach(c)` clears it only while it is still `c``.
  - `submit(cmd)`, under `mu`: an id already in `pending` returns. Otherwise it marks the id pending and appends the command to `queues[appID]`. When that makes the queue length 1, it runs `wg.Add(1)` and starts `go d.drain(appID)`.
  - `drain(appID)` loops: it takes the head under `mu`, then runs `cmd.run(d.ctx)` with no lock. It deletes `pending[id]` under `mu`, then calls `send(id, res)`. Then, under `mu`, it pops the head; with the queue empty it deletes the queue and returns (`wg.Done`), and otherwise it loops. The id leaves `pending` before the send, so a re-send that arrives after the result went out is answered by the executor's stored record (rule 2), not ignored.
  - `send(id, res)`: `protocol.Wrap(COMMAND_RESULT, id, res)` and `protocol.Marshal`. An encode error goes to `onError` as `connect: encode result of request %s: %w`, and the result is dropped. Then it reads `live` under `mu`; with none, it returns (the stored record answers the re-send). Otherwise it writes under `context.WithTimeout(d.ctx, writeTimeout)`. On a write error it does a non-blocking send of `connect: send result of request %s: %w` on `live.failed`, which ends that session.
  - `wait()` is `wg.Wait()`.
- `internal/agent/connect.go`:
  - `ConnectOptions` gains `Executor Executor` ("runs each accepted command; required").
  - `Connect`: the first statement returns `errors.New("connect: ConnectOptions.Executor is nil: pass the executor that runs the deploy commands")` when `opts.Executor == nil`, before any key is read or anything is dialled. After setup it runs `d := newDispatcher(ctx, opts.OnError)` and `defer d.wait()`, so `Connect` returns only after every command it started has returned. `session` gains the `d *dispatcher` parameter.
  - New const `writeTimeout = 10 * time.Second`, the limit on writing one result.
  - `session` is rewritten as one loop for the connection's life. After dialling it makes `failed := make(chan error, 1)`, calls `d.attach(c, failed)` and defers `d.detach(c)`. The reader goroutine is unchanged. The state is `waiting string` (the id of the unacknowledged Heartbeat) plus one `timer`, which means "send the next Heartbeat" while `waiting == ""` and "the ack deadline" otherwise. It starts at 0 and relies on Go 1.27 `Reset` semantics. The select handles:
    - `ctx.Done`: normal close, as now.
    - `<-failed`: return that error.
    - A frame. A read error is "connection lost", as now. A text frame closes with `StatusUnsupportedData`, and an undecodable envelope with "bad envelope", with today's errors. `HEARTBEAT_ACK` with `waiting != ""` and the same id clears `waiting`, sets `acked = true` and runs `timer.Reset(opts.HeartbeatInterval)`. Any other ack closes with `StatusPolicyViolation` and `connect: expected HEARTBEAT_ACK for request %q, got one for %q`. Every other envelope goes to `openCommand`; an error closes with `StatusPolicyViolation` and is returned, and success calls `d.submit(cmd)`.
    - `timer.C`: with `waiting != ""`, return `connect: wait for heartbeat ack: %w` wrapping `context.DeadlineExceeded`. Otherwise send Heartbeat `strconv.Itoa(n)` (n++), set `waiting` and run `timer.Reset(ackTimeout)`.
  - `heartbeat` is replaced by `sendHeartbeat(ctx, c, id, version string) error`: Wrap, Marshal and Write under `ackTimeout`, with today's error texts. The old "frame outside a heartbeat" path is deleted. `coder/websocket` allows concurrent `Write`, which result writers and Heartbeats both do (`conn.go`: "All methods may be called concurrently except for Reader and Read").
- `cmd/moonglass/main.go`:
  - `agentConnectArgs` gains `policyFile string`. `parseAgentConnect` adds `fs.String("policy", "/etc/moonglass/agent.toml", "local policy file (agent.toml)")` and returns false when it is `""`, so the command exits 2 with the usage.
  - `agentConnect`: after `deploy.Connect` it calls `deploy.PolicyEnv(ctx, engine)` and then `policy.LoadFile(a.policyFile, env, 0)`. On either error it closes the Engine, reports `moonglass: <error>` and returns 1. Only then does it call `state.Open`.
  - It builds `x := deploy.New(engine, pol, db, p)` over the same `deploy.NewProxy` value `p`. One `sync.WaitGroup` runs `p.Run(runCtx)` and `x.Run(runCtx, report)`. It passes `agent.ConnectOptions{AgentVersion: version, OnError: report, Executor: x}`. After `Connect` it runs `cancel()` and `wg.Wait()`, and only then closes the database and the Engine. The old `done` channel is removed. It imports `internal/policy`, never moby.
- `internal/registry/routes.go`: the `agent connect` Synopsis becomes `agent connect [--data-dir DIR] [--policy FILE] [--proxy-socket PATH] [--tls-issuer ISSUER] --url URL`.
- `docs/specs/connection.md`:
  - "Agent (W2-10b)" gains four sentences:
    - A request id that is queued or running is not queued or started again.
    - Commands run under the agent's lifetime, not the connection's, so a dropped connection does not cancel one.
    - `Connect` refuses a nil executor before dialling.
    - An empty `--policy` prints the usage and exits 2.
  - The section header and rule 3 are marked built.
  - "Bad frames" says the agent reads frames for the whole connection: a `HeartbeatAck` that is not for the waiting Heartbeat closes it, and so does any type outside "Agent".
  - The `--policy` note under "Commands" says it was built in W2-10b.
- `testdata/tests.txt`: `make test-list`.

## Tests to write

Shared helpers go in `dispatch_test.go`, package `agent`, reusing `newFakeCP`, `writeAgent`, `genKey`, `mustMarshal`, `runConnect`, `fastSleep` and `waitFor`:
- `fakeExec`: a per-request-id gate channel. It records each call (id and app), the current and maximum concurrency per app, and the call count per id, under a mutex.
- `cmdCP`: each connection is numbered. A reader goroutine acks every Heartbeat, pushes each `COMMAND_RESULT` (connection number, id, decoded result) onto a channel, and signals on the first ack. The test sends raw frames on the current connection and can drop it.

Owner-allowed edits to existing tests:
- `internal/agent/connect_test.go`: `idleExecutor struct{}`, whose four methods each return `&agentv1.CommandResult{Outcome: FAILED, Detail: "idle executor"}`. Every `ConnectOptions{...}` gains `Executor: idleExecutor{}`. No assertion changes.
- `internal/registry/routes_test.go`: `TestAgentConnectSynopsisNamesProxyFlags` expects the new string.
- `cmd/moonglass/connect_test.go`, `TestAgentConnectRunsProxyPush`: after the existing Engine check, it starts a Unix-socket `httputil.ReverseProxy` in `os.MkdirTemp("", "mg")` to the real socket (`DOCKER_HOST` without `unix://`, or `/var/run/docker.sock`). The proxy answers `GET /v*/containers/json` (and the unversioned path) with `[]` and forwards everything else. `t.Setenv("DOCKER_HOST", "unix://"+proxySock)`. No assertion changes.

New tests:
- `./internal/agent TestResentCommandReturnsStoredResult` (`resend_test.go`, standard library only, real Engine):
  - It pings the real socket with `GET /_ping`. On failure it skips, or fails under `CI`.
  - A counting reverse proxy on a Unix socket counts `POST /v*/containers/create`, and `DOCKER_HOST` points at it.
  - Project `t`+12 hex. It builds `../deploy/testdata/server` with `CGO_ENABLED=0 GOOS=linux GOARCH=runtime.GOARCH`, tars it as `app` and imports it as `moonglass.local/<p>/web:<rel>` with `POST /images/create?fromSrc=-&repo=…&tag=…&changes=ENTRYPOINT ["/app"]`. It fails on an `errorDetail` in the stream.
  - `t.Cleanup`, registered first, removes over the real socket: containers and networks with `dev.moonglass.project=<p>`, then `mg-<p>`, then the image (`force=1`).
  - It builds `deploy.Connect`, `state.Open(t.TempDir())`, `deploy.PolicyEnv`, `policy.Parse(nil, env)`, `deploy.NewProxy` over a socket that does not exist, and `deploy.New`, and never calls `Run`.
  - Connection 1: after the first ack it sends one `DEPLOY` frame (bytes kept), waits for its result, then closes. Connection 2: after the first ack it sends the identical bytes and waits for the result.
  - It asserts: both results have the request id; `proto.Equal(first, second)`; the first is `SUCCEEDED` (the detail shown on failure); and the create count is exactly 1.
- `./internal/agent TestCommandsForOneAppRunInArrivalOrder`: `DEPLOY`, `SWITCH_RELEASE` and `ROLLBACK` for app A (c1 to c3), back to back, each held 20 ms. The calls are c1, c2, c3 in order, the maximum concurrency for A is 1, and three results arrive with ids c1, c2 and c3.
- `./internal/agent TestCommandsForDifferentAppsRunConcurrently`: A blocks on its gate, and B is sent after it. B's result arrives while A has started and not returned. Then A is released and its result arrives.
- `./internal/agent TestRunningRequestIDIsNotStartedTwice`, two subtests:
  - (a) c1 for A blocks, and the CP drops connection 1. On connection 2 it re-sends c1, then sends a quick command for app B; B's result proves the re-send was read, because frames are handled in order. It releases c1. c1 has exactly 1 call, and exactly one result for c1 arrives, on connection 2.
  - (b) c1 for A blocks, c2 for A is queued, and c2 is re-sent on the same connection, synced the same way through B. After release c2 has 1 call and 1 result.
- `./internal/agent TestAgentClosesOnMessageItDoesNotAccept`: a table of `BUILD`, `STOP_APP`, `REMOVE_APP`, `UPDATE_OFFER`, `COMMAND_RESULT` and `HEARTBEAT` (empty messages through `protocol.Wrap`), plus a `DEPLOY` wrapping `Deploy{AppId: "Bad Id"}`, which `Open` rejects. Each is sent after the first ack. The agent closes and a second connection follows. The executor has 0 calls.
- `./internal/agent TestHeartbeatsContinueWhileCommandRuns`: interval 20 ms, and a command blocked. The beat count grows by at least 5 while it is blocked, and the connection count stays 1.
- `./internal/agent TestConnectRefusesNilExecutor` (helper): with no `Executor`, the error contains `ConnectOptions.Executor` and the CP has 0 TCP accepts.
- `./internal/deploy TestPolicyEnvReadsEngine` (`testEngine`, `testProject`, `importImage`):
  - `DockerRoot` and `DefaultRuntime` equal `e.cli.Info`, and the three fixed fields equal the Scope values.
  - `ContainerProject` gives `(p, true)` for a container labelled `dev.moonglass.project=<p>`. It gives `("", false)` for an unlabelled container (removed in its own cleanup) and for `"mg-missing-"+p.name`.
- `./cmd/moonglass TestAgentConnectPolicyFlagDefault`: with no flag, `policyFile` is `/etc/moonglass/agent.toml`; `--policy F` gives `F`; `--policy ""` makes `ok` false.
- `./cmd/moonglass TestAgentConnectRefusesPolicyThatDoesNotLoad` (real Engine, skip or CI-fail):
  - Two files: `bad-mode.toml` (`os.Chmod` 0666) and `bad.toml` (`[[[`, 0600). A TCP listener counts accepts.
  - For each file, `agentConnect` returns 1. `stderr` equals `"moonglass: " + policy.LoadFile(file, policy.Env{}, 0).Error() + "\n"`, and that expected error is required to be non-nil. No `agent.db` exists and there are 0 accepts.
- `./internal/registry TestAgentConnectSynopsisNamesPolicyFlag`: the Synopsis equals the Scope string exactly.

## Edge cases

- **Nil executor**: refused before any key read or dial (`TestConnectRefusesNilExecutor`).
- **Empty `--policy`**: exit 2 (`TestAgentConnectPolicyFlagDefault`).
- **A policy file that does not load**: exit 1 with no database and no dial (`TestAgentConnectRefusesPolicyThatDoesNotLoad`). As a non-root user, `bad.toml` fails on the owner check first. The test compares against `LoadFile`'s own error, so it holds as root and as non-root.
- **An absent policy file** is the empty policy (`TestAgentConnectRunsProxyPush`).
- **A failed Engine info call**: an error that names the Docker Engine, then exit 1.
- **An unknown, unlabelled or uninspectable container**, or a cancelled ctx: `"", false`, which counts as cross-project (`TestPolicyEnvReadsEngine`).
- **Hostile frames**: every non-accepted type, a payload that fails `Open`, a text, oversize or undecodable frame, an ack for another id or with no Heartbeat waiting. Each closes the connection, and nothing reaches the executor (`TestAgentClosesOnMessageItDoesNotAccept`, `TestAgentClosesOnBadFrame`).
- **A re-send while queued or running** is ignored (`TestRunningRequestIDIsNotStartedTwice`). **A re-send after finishing** gets the stored record (`TestResentCommandReturnsStoredResult`).
- **A result with no live connection** is dropped, and the stored record answers the re-send. **A failed result write** ends the session through the non-blocking `failed` channel, which never blocks the drain goroutine.
- **An encode error** goes to `OnError` with no payload secrets. The detail is already truncated by the executor.
- **Shutdown**: commands see `Connect`'s ctx cancelled, and `Connect` waits on `wg`. `submit` runs only on `Connect`'s goroutine, so `wg.Add` never races `wg.Wait`.

## Risks

- **`exhaustive` on `MessageType`**: `openCommand` lists every value explicitly.
- **`govet` fieldalignment** in non-test code: order the struct fields as `make lint` reports.
- **Locked test files**: only the owner-allowed edits are made. `connect_test.go` is formatter-excluded.
- **Diff size (non-test Go only)**: about 320 of the 400-line cap (`dispatch.go` about 150, `connect.go` about 80, `policyenv.go` about 50, `main.go` about 40, `routes.go` 2).
- **Flaky ordering**: the tests sync on results (the app-B sentinel), never on sleeps, and every wait is bounded by `waitFor`.
- **ADR-0014 / depguard**: `internal/agent` tests import `internal/deploy`, `internal/agent/state` and `internal/policy`, never moby, and there is no import cycle. Engine HTTP calls use `net/http`.
- **`t.Setenv`**: the tests that override `DOCKER_HOST` are not parallel.
- **`TestAgentConnectRunsProxyPush` with `Executor.Run`**: the start-up reconcile would touch other managed containers on a shared Engine. The `containers/json` → `[]` proxy isolates it.
- **Two near-identical synopsis tests**: the ticket names both.
