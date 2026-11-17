# Agent ↔ control-plane protocol

This is the behaviour spec for the typed agent protocol. Protocol v0 (ticket W0-3) covers only the envelope, Enroll, Heartbeat and error codes. Later tickets add deploy and other commands without breaking these types.

Sources: `docs/plan/stack.md` §3.1, `docs/threat-model.md` §9, ADR-0007 (compatibility window, after launch only), ADR-0008 (codegen).

The `.proto` file is `proto/moonglass/agent/v1/agent.proto`. The hand-written codec is `internal/protocol`. Generated Go lives in `internal/protocol/agentv1` and is never edited by hand.

## Transport (out of scope for v0)

The agent dials outbound `wss://` on port 443. There is no inbound port on the server for the control plane. v0 specifies the messages, not the WebSocket, TLS or reconnect behaviour.

## Envelope

Every message is wrapped in an envelope before it is sent.

| Field | Type | Notes |
|---|---|---|
| `version` | `uint32` | Protocol v0 sends `1`. Any other value is rejected. |
| `type` | `MessageType` | One of the v0 types below. `UNSPECIFIED` and unknown values are rejected. |
| `request_id` | `string` | 1 to 64 bytes. Identifies a request/response pair. |
| `payload` | `bytes` | Encoded payload message for that type, possibly empty. |
| `error` | `Error` | Required when `type` is `ERROR`, and forbidden on any other type. This is the only place an error travels; it is not also encoded in `payload`. |

`MessageType` values: `ENROLL_REQUEST`, `ENROLL_RESPONSE`, `HEARTBEAT`, `HEARTBEAT_ACK`, `ERROR`, `DEPLOY`, `SWITCH_RELEASE`, `ROLLBACK`, `RUN_PRE_DEPLOY`.

Encode (`Marshal`) and decode (`Unmarshal`) round-trip a well-formed envelope. `Marshal` is deterministic. The on-the-wire encoding is a 4-byte big-endian length of the protobuf payload, followed by that many bytes of protobuf. The length must match the rest of the buffer exactly, so a truncated or trailing-garbage input is rejected.

Validation order for `Unmarshal`:

1. Reject empty input as malformed.
2. Reject input longer than `MaxEnvelopeSize` (1 MiB, 1048576 bytes, for the complete framed encoding, no separate payload cap) as too large, before any proto parse.
3. Reject input shorter than 4 bytes, or whose length prefix does not equal the remaining byte count, as malformed.
4. Parse the protobuf body. Parse errors are malformed.
5. Reject any unknown fields on the envelope and on a nested `Error`.
6. Validate version, type, request id, and any error (see below). `type` `ERROR` requires a present, valid `error`; any other type must have `error` unset.

`Marshal` validates first, then encodes, then rejects an encoding larger than `MaxEnvelopeSize`. Decode does not panic on malformed or oversized input.

## Enroll (types only in v0)

Enroll request and response are protobuf types carried in the envelope. Key generation, token storage and the CLI are W1-3. Length and cryptographic checks on the key bytes are W1-3; v0 only types the fields.

`EnrollRequest`:

| Field | Type | Notes |
|---|---|---|
| `token_id` | `string` | Identifies the one-time token. The token secret itself is not in the message. |
| `agent_signing_public_key` | `bytes` | Agent Ed25519 public key. |
| `agent_age_recipient` | `bytes` | Agent X25519 age recipient. |

`EnrollResponse`:

| Field | Type | Notes |
|---|---|---|
| `control_plane_public_key` | `bytes` | Control-plane public key to pin. |
| `result` | `EnrollResult` | `ACCEPTED`, `TOKEN_INVALID`, or `TOKEN_USED`. `UNSPECIFIED` and unknown values are rejected when the payload is opened. |

No field is a host shell command.

## Heartbeat (types only in v0)

`Heartbeat` carries `agent_version` (string). `HeartbeatAck` has no fields. Both round-trip through the envelope. Opening a Heartbeat acknowledgement as an Enroll request is an error. The control plane is expected to see heartbeats about every 30 seconds (S§5); v0 does not schedule them.

## Deploy commands (W2-2)

Four commands, each a protobuf type carried in the envelope. None has a field that is a host shell command or a host path. They have no response or result types: a failure travels in the existing `ERROR` envelope, and outcomes of execution belong to later tickets. The legal order of these commands for one app is `docs/specs/release.md`.

An identifier (`app_id`, `release_id`) is 1 to 64 bytes of `[A-Za-z0-9._-]`. Violations are rejected with `ErrMalformed` when the payload is opened, before any later code acts.

| Message | Type | Fields |
|---|---|---|
| `Deploy` | `DEPLOY` | `app_id`, `release_id`, `image` |
| `SwitchRelease` | `SWITCH_RELEASE` | `app_id`, `release_id` |
| `Rollback` | `ROLLBACK` | `app_id` only; it names no target release |
| `RunPreDeploy` | `RUN_PRE_DEPLOY` | `app_id`, `release_id`, `args`, `reversible` |

`image` is an image reference of 1 to 512 bytes with no whitespace, no control characters and no leading `-`. Validation is structural only; digest pinning is not required.

`args` is an exec argv: 1 to 32 entries, each 1 to 1024 bytes with no NUL. Every entry, including the first, must be non-empty. Entries are never joined and never passed to a host shell. `reversible` says whether the pre-deploy step can be undone (S§3.13); it is carried as sent and does not change the release transitions.

`RunPreDeploy` does not repeat the image: the executor takes it from the `Deploy` with the same `release_id`.

## Error codes

`Error` has exactly two fields: `code` (`ErrorCode`) and `detail` (`string`, at most 1024 bytes). The detail is not a shell command.

| Code | Meaning |
|---|---|
| `INVALID` | The message was well-formed enough to parse but not acceptable. |
| `UNAUTHORIZED` | The caller is not allowed to perform the request. |
| `TOO_LARGE` | The envelope or a field exceeded a size limit. |
| `UNKNOWN_TYPE` | The message type is not known. |
| `UNAVAILABLE` | The receiver cannot handle the request now. |

`UNSPECIFIED` (0) and unknown numeric codes are rejected. Validation does not panic.

## Compatibility

After launch, the control plane speaks the current and previous protocol version only (ADR-0007). v0 does not implement a compatibility window.

## Codegen

Generated Go is produced by the toolchain in ADR-0008, checked in, never edited by hand, and verified by `make proto`. CI fails if generated output is stale.
