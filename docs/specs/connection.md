# Agent connection

W1-4 implements the agent's outbound connection to the control plane and its heartbeat. It is faithful to `docs/specs/protocol.md` (envelope, Heartbeat, HeartbeatAck), `docs/specs/enrolment.md` (pinned control-plane key, agent identity files), threat-model boundary B3 and stack §3.1. The dependency is ADR-0012.

## Transport

The agent dials `wss://HOST/v1/agent`. It never listens. `moonglass server` serves this endpoint on `--agent-listen ADDR` (default `127.0.0.1:8443`; port 443 in production is an install choice, W1-6). `--listen` stays the plain HTTP enrolment listener.

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

## Commands

- `moonglass agent connect [--data-dir DIR] --url URL` — `URL` is `https://` or `wss://`; `http://` is refused. The path `/v1/agent` is joined to it. Connection errors go to stderr.
- `moonglass server --agent-listen ADDR`.

## Route registration

The operation registry does not exist yet, so `/v1/agent` is mounted beside its authorisation (an enrolled client certificate). It joins the fixed route list when W2-1 lands.

## Out of scope

Deploy, SwitchRelease, Rollback, RunPreDeploy (W2-2). Install scripts and units (W1-6). Live logs (W4-4). Moving enrolment onto TLS. Revocation.
