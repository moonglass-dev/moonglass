# Agent connection

W1-4 implements the agent's outbound connection to the control plane and its heartbeat. It is faithful to `docs/specs/protocol.md` (envelope, Heartbeat, HeartbeatAck), `docs/specs/enrolment.md` (pinned control-plane key, agent identity files), threat-model boundary B3 and stack §3.1. The dependency is ADR-0012.

## Transport

The agent dials `wss://HOST/v1/agent`. It never listens. `moonglass server` serves this endpoint on `--agent-listen ADDR` (default `127.0.0.1:8443`; port 443 in production is an install choice, W1-6).

FR1-1 moves `--listen` onto TLS 1.3 with the server's Ed25519 self-signed certificate (`keys.SelfSignedCert`). `POST /v1/enroll` and the registry REST routes share that listener; there is no plain-HTTP listener in production. The first connection is trust-on-first-use: `moonglass agent enroll --server-fingerprint FP` verifies the leaf key's SHA-256 against the fingerprint `moonglass server` prints at start, then `control-plane.pub` is the pin for every later connection (`docs/specs/enrolment.md`).

TLS is 1.3 only on both sides. Both sides authenticate with their Ed25519 identity from enrolment (mutual TLS). Go has no raw-public-key TLS, so each side presents a short-lived self-signed Ed25519 x509 certificate (`keys.SelfSignedCert`) as a carrier. Subject, SANs and expiry are not trusted:

- The agent skips chain verification and, in `VerifyPeerCertificate`, requires the server leaf key to equal the `control-plane.pub` pin (`keys.ClientTLSConfig`).
- The server requires a client certificate and, in `VerifyPeerCertificate`, accepts only a leaf key that is an enrolled server's public key in the store (`keys.ServerTLSConfig`). Presence in the store is the whole authorisation; revocation arrives with a later ticket.
- Proof of possession of each key is TLS 1.3 CertificateVerify.

A pin mismatch ends the handshake. The agent reads `agent-signing.key` and `control-plane.pub` and never writes the data dir, so a failed connect cannot change enrolment files. Private keys, the pin and tokens are not logged.

## Framing

Each WebSocket binary message is one length-prefixed protocol envelope (`internal/protocol`). The read limit is the 1 MiB envelope cap.

## Heartbeat

The agent sends a `Heartbeat` (with `agent_version`) as soon as it connects, then every heartbeat interval (default 30 seconds, S§5; injectable in tests). Request ids are a per-connection counter. The server answers each with a `HeartbeatAck` with the same request id. The agent waits up to 10 seconds for the ack.

## Bad frames

The agent closes the connection, and reconnects through backoff, on a text message, an oversize message, an undecodable envelope, anything other than a `HeartbeatAck`, a `HeartbeatAck` for another request id, or no ack within 10 seconds. The server closes the connection on the same kinds of frame, and on any envelope that is not a `Heartbeat`.

## Reconnect backoff

After a connection ends or a dial fails, the agent waits `random(0, min(30s, 1s * 2^attempt))` (full jitter) and dials again. `attempt` starts at 0, increases after each wait, and resets to 0 once a connection has completed at least one Heartbeat / HeartbeatAck exchange. The random source and the sleep are injectable. The agent runs until its context is cancelled.

## Command channel (W2-10)

Specified by the weeks 2–3 preflight (owner decision D7). From W2-10 the connection also carries the deploy commands and their `CommandResult`s (`docs/specs/protocol.md`). "Bad frames" above is narrowed accordingly: each side accepts the types listed here and closes on anything else.

### Control plane

- The server keeps at most one live connection per enrolled server. A new connection authenticated by the same key replaces the old one, which is closed.
- One control-plane node holds every connection. Routing commands between nodes through `LISTEN/NOTIFY` (ADR-0002 decision 6) is not built here.
- The server sends `DEPLOY`, `SWITCH_RELEASE`, `ROLLBACK` and `RUN_PRE_DEPLOY` (and `BUILD` from W3-4, `STOP_APP` and `REMOVE_APP` from W5-5b) to the server that runs the service, and accepts `HEARTBEAT` and `COMMAND_RESULT`. From W4-4 it also sends `LOG_REQUEST` and accepts `LOG_CHUNK` and `USAGE`.
- **Request ids for commands** are `c` followed by 32 lowercase hex characters from 128 random bits, minted by the server. The id is stored with the release step it belongs to (`docs/specs/storage.md`) before the command is sent.
- **One command at a time per app.** The server sends the next command for an app only after the previous one has a result.
- **One build at a time per server** (owner decision D26). The server sends a `BUILD` to a server only when no other `BUILD` to it is waiting for a result, and queues the rest in arrival order. The `BUILD` timeout therefore counts only the build itself. Queued builds wait on the control plane, not on the agent.
- **Timeouts** for a result, counted from sending: `DEPLOY` 20 minutes, `BUILD` 30 minutes, `RUN_PRE_DEPLOY` 20 minutes, `SWITCH_RELEASE` 3 minutes, `ROLLBACK` 3 minutes, `STOP_APP` 5 minutes, `REMOVE_APP` 10 minutes (W5-5b). On timeout the server closes the connection; the command is re-sent once the agent reconnects (below). The release step fails only when the re-sent command also times out.
- **Re-sending.** After the agent reconnects, the server re-sends every command that has no result yet, with its original request id. After the server itself restarts, the `deploy` and `rollback` operations re-send the step recorded in `releases.request_id` and `rollbacks.request_id` (W2-5, `docs/specs/storage.md`).
- A `CommandResult` whose request id the server did not send, or already has a result for, closes the connection.

### Agent

- The agent accepts `HEARTBEAT_ACK`, the command types above and, from W4-4, `LOG_REQUEST`, and sends `HEARTBEAT`, `COMMAND_RESULT` and, from W4-4, `LOG_CHUNK` and `USAGE`. Anything else closes the connection, as before.
- Commands for different apps run concurrently. Commands for one app run one at a time, in the order they arrived.
- From W5-3, every command first passes `agent.toml`'s command classes and sources, and a refused one is answered `REJECTED` (`docs/specs/local-policy.md`).
- A `BUILD` that arrives while another build runs on this server is answered `REJECTED` with `another build is running on this server`, and nothing is done. A re-sent `BUILD` with the running build's request id is not a second build ("Each request id runs once").
- At start, before connecting, the agent loads `agent.toml` with `policy.LoadFile` from `--policy FILE` (default `/etc/moonglass/agent.toml`, owner uid 0), with `policy.Env` from the Docker Engine (`DockerRootDir`, the default runtime, read through `internal/deploy`, because only it may import the Docker client: ADR-0014) and `ContainerProject` from the `dev.moonglass.project` label. A file that does not load stops the agent with the policy error. An absent file is the empty policy (`docs/specs/container-policy.md`).
- It opens its state database (`docs/specs/agent-state.md`) and checks the Engine API version (ADR-0014) before connecting.

### Each request id runs once

Owner condition on D7: a command re-sent after a reconnect must never run twice.

1. The agent records every executed command's request id in agent state (`commands`, `docs/specs/agent-state.md`), together with the `CommandResult` it produced. The record and the release-state change it made are written in one transaction, before the result is sent.
2. A command whose request id has a stored record is not run again. The agent sends the stored `CommandResult` unchanged.
3. A command whose request id is still running (re-sent after a reconnect, while the first run continues) is not started again. Its one result is sent on whichever connection is live when it finishes.
4. Records are kept for 7 days, longer than any re-send can arrive.

W2-10 must name a test that sends a `DEPLOY`, drops the connection after the agent has acted, reconnects, re-sends the same envelope, and asserts that the stored result comes back and that the Engine saw exactly one container create. `TestResentCommandReturnsStoredResult` is that test's name.

## Log and usage streams (W4-4)

The messages are `docs/specs/protocol.md`. The SSE routes are `docs/specs/live.md`.

- The server sends `LOG_REQUEST`. The agent accepts `LOG_REQUEST`. The agent sends `LOG_CHUNK` and `USAGE`. The server accepts `LOG_CHUNK` and `USAGE`.
- Stream request ids are `l` followed by 32 lowercase hex characters from 128 random bits, minted by the server. A `LOG_CHUNK` uses the `request_id` of its `LOG_REQUEST`.
- On agent reconnect, the server re-opens streams that still have SSE subscribers.
- `Last-Event-ID` is ignored.
- One `LOG_CHUNK` with `final` true and empty `data`, sent when the app has no container, does not close the connection.

## Commands

- `moonglass agent connect [--data-dir DIR] [--policy FILE] [--proxy-socket PATH] [--tls-issuer ISSUER] --url URL` — `URL` is `https://` or `wss://`; `http://` is refused. The path `/v1/agent` is joined to it. Connection errors go to stderr. `--proxy-socket` and `--tls-issuer` arrive with W2-4c, and `--policy` with W2-10.
  - `--proxy-socket PATH` is the proxy's agent-facing admin socket, default `/run/moonglass-proxy/admin.sock` (`docs/specs/proxy.md`). An empty value prints the usage and exits 2.
  - `--tls-issuer` is `acme` (the default, Let's Encrypt production), `acme-staging` (the fleet) or `internal` (tests and CI). Any other value prints the usage and exits 2, before anything is opened or dialled.
  - From W2-4c, the command opens the Docker Engine (ADR-0014) and the agent state database (`docs/specs/agent-state.md`) before it connects, and runs `deploy.Proxy.Run` (`docs/specs/release.md`, "Proxy config") until it exits. A proxy that is down does not stop the agent or its connection; the push is retried every second. An Engine or a database that does not open stops the command with that error and exit code 1.
  - Each ticket that adds a flag also updates the `agent connect` synopsis in `registry.LocalCommands` to this form, with the flags it has so far.
- `moonglass server --agent-listen ADDR`.

## Route registration

The operation registry does not exist yet, so `/v1/agent` is mounted beside its authorisation (an enrolled client certificate). It joins the fixed route list when W2-1 lands.

## Out of scope

The commands' execution (W2-3, W2-4, W2-6). Install scripts and units (W1-6). Live logs (W4-4). Revocation.
