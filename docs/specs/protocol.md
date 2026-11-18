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

Four commands, each a protobuf type carried in the envelope. None has a field that is a host shell command or a host path. W2-2 gave them no response type. W2-8 adds `CommandResult` (below): an `ERROR` envelope reports a message that could not be accepted at all, and a `CommandResult` reports what happened when the agent acted on one. The legal order of these commands for one app is `docs/specs/release.md`.

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

## Deploy identity, service settings and results (W2-8)

Specified by the weeks 2–3 preflight (owner decisions D1, D7, D9). W2-8 adds these to the `.proto`, validates them in `internal/protocol`, and regenerates the Go. Until W2-8 merges, they are not on the wire.

### Deploy gains

| Field | Type | Rule |
|---|---|---|
| `project`, `environment`, `service` | `string` | Required. Each a name: 1 to 63 bytes of `[a-z0-9-]`, starting and ending with a letter or digit. The same format the control plane uses (`docs/specs/storage.md`), and a valid path element for `agent.toml` blocks. |
| `port` | `uint32` | Optional. 0 means none; otherwise 1 to 65535. The port the container listens on. |
| `health_path` | `string` | Optional. Requires `port`. 1 to 256 bytes, starts with `/`, only `[A-Za-z0-9._~/-]`. |
| `domains` | `repeated string` | Optional, 0 to 16 entries. Requires `port`. Each a lowercase DNS name: at least two labels, each 1 to 63 bytes of `[a-z0-9-]` not starting or ending with `-`, at most 253 bytes, no wildcard, no trailing dot. No duplicates. |
| `has_pre_deploy` | `bool` | Whether a `RunPreDeploy` for this release follows. It is the `hasPreDeploy` input of `release.State.Deploy`. |

**Identity binding.** `SwitchRelease`, `Rollback` and `RunPreDeploy` stay keyed by `app_id` and do not repeat the names. The agent binds an `app_id` to its `project`, `environment` and `service` at the first `Deploy` for it and stores the binding (`docs/specs/agent-state.md`). A later `Deploy` for that `app_id` that names a different project, environment or service is answered `REJECTED`, naming the stored triple, and nothing is done. A control plane cannot move an app into another project's `agent.toml` blocks.

**Without a port** there is no route and no HTTP health check. The service counts as healthy once its container has been running for 10 seconds without a restart (`docs/specs/release.md`). This covers worker services and the databases of W6-1. There is no `UpdateRoutes` message: changing a domain needs a redeploy.

### CommandResult

A new `MessageType`, `COMMAND_RESULT`, sent by the agent only. Its `request_id` is the `request_id` of the command it answers. The agent sends exactly one per command envelope it accepted: `DEPLOY`, `SWITCH_RELEASE`, `ROLLBACK`, `RUN_PRE_DEPLOY`, and from W3-7 `BUILD`.

| Field | Type | Rule |
|---|---|---|
| `outcome` | `CommandOutcome` | `SUCCEEDED`; `FAILED` (the agent tried and could not finish); or `REJECTED` (refused before acting: an illegal transition, an identity mismatch, a policy rejection, a disk refusal). `UNSPECIFIED` and unknown values are rejected. |
| `detail` | `string` | At most 4096 bytes. Empty on success. Plain text for a person, such as a rendered policy `Rejection`. Never an environment value, a secret or a credential. |
| `app` | `AppState` | The app's release state after the command: `current_release_id`, `previous_release_id`, `candidate_release_id` (each empty or an identifier) and `phase` (`NONE`, `PENDING` or `READY`). |
| `image_id` | `string` | For `DEPLOY` and `BUILD`: the image ID the release runs (`sha256:` plus 64 hex). Empty otherwise. |
| `repo_digest` | `string` | For `DEPLOY`: the registry digest the reference resolved to (`<repository>@sha256:<64 hex>`). Empty for an image built on the server. |

**Direction.** Commands travel from the control plane to the agent only; `Heartbeat` and `CommandResult` from the agent to the control plane only. Either side closes the connection on a message travelling the wrong way (`docs/specs/connection.md`).

## Builds and server reports (W3-7)

Specified by the weeks 2–3 preflight (owner decisions D16, D17). W3-7 adds them to the `.proto`.

`Build`, a new type `BUILD` = 11:

| Field | Type | Rule |
|---|---|---|
| `app_id`, `release_id` | identifiers | As for `Deploy`. The built image is tagged for this release. |
| `project`, `environment`, `service` | names | As for `Deploy`, under the same binding rule. |
| `repository` | `string` | A GitHub repository as `<owner>/<repo>`. `owner` is 1 to 39 bytes of `[A-Za-z0-9-]` not starting with `-`. `repo` is 1 to 100 bytes of `[A-Za-z0-9._-]`, not `.` or `..`, not starting with `-` or `.`. No scheme, host or path: the agent builds the clone URL itself (`docs/specs/build.md`). |
| `commit` | `string` | Exactly 40 lowercase hex characters. No branch or tag names. |

`Heartbeat` gains:

| Field | Type | Rule |
|---|---|---|
| `disk` | `DiskReport` | Optional (owner decision D25): absent means not measured. When present, `data_root_free_bytes`, `data_root_total_bytes`, `agent_dir_free_bytes`, `agent_dir_total_bytes` (`uint64`), each free value at most its total, and `level`: `OK`, `LOW` or `CRITICAL` (`docs/specs/disk.md`); `UNSPECIFIED` and unknown levels are rejected. From W3-6 the agent always sends it. |
| `deploy_key` | `string` | The server's SSH deploy public key as one OpenSSH line, `ssh-ed25519 <base64> moonglass-deploy`, at most 256 bytes; empty until the key exists (`docs/specs/build.md`). It is a public key, not a secret. |

## Log and usage streaming (W4-4)

The owner approved these messages on 2026-11-17.

`LogRequest`, type `LOG_REQUEST` = 12, server to agent. One open log stream per app. A new `LOG_REQUEST` for that app replaces the previous one.

| Field | Type | Rule |
|---|---|---|
| `app_id` | identifier | Required. |
| `release_id` | identifier | Empty means the current release. Otherwise an identifier. |
| `tail` | `uint32` | 0 to 1000. |
| `follow` | `bool` | Whether the agent keeps sending after the tail. |
| `grep` | `string` | Empty, or 1 to 128 bytes with no NUL. A literal substring. |

`LogChunk`, type `LOG_CHUNK` = 13, agent to server, with the same `request_id` as the `LOG_REQUEST` it belongs to.

| Field | Type | Rule |
|---|---|---|
| `data` | `bytes` | 1 to 65536 bytes on a non-final chunk. A final chunk may be empty. |
| `final` | `bool` | The last chunk of this stream. |

When the app has no container, the agent sends one chunk with `final` true and empty `data`. That does not close the connection.

`Usage`, type `USAGE` = 14, agent to server, every 10 seconds, only while that app has a usage subscriber. The counters are cumulative. If the cgroup files are missing, the agent omits the sample. Nothing is written to SQLite.

| Field | Type | Rule |
|---|---|---|
| `app_id` | identifier | Required. |
| `cpu_percent` | `uint32` | |
| `mem_bytes` | `uint64` | From cgroup v2 `memory.current`. |
| `mem_limit_bytes` | `uint64` | From cgroup v2 `memory.max`. |
| `net_rx_bytes` | `uint64` | From the veth counters. |
| `net_tx_bytes` | `uint64` | From the veth counters. |
| `disk_read_bytes` | `uint64` | From `io.stat`. |
| `disk_write_bytes` | `uint64` | From `io.stat`. |

The streams and the SSE routes are `docs/specs/live.md`. What each side sends and accepts is `docs/specs/connection.md`.

## Secret delivery (W4-8) and reveal (W4-9)

Those messages are specified in `docs/specs/secrets.md` and added to the `.proto` by those tickets.

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
