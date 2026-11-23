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

`MessageType` values: `ENROLL_REQUEST`, `ENROLL_RESPONSE`, `HEARTBEAT`, `HEARTBEAT_ACK`, `ERROR`, `DEPLOY`, `SWITCH_RELEASE`, `ROLLBACK`, `RUN_PRE_DEPLOY`, `COMMAND_RESULT`, `BUILD`. W4-4 adds `LOG_REQUEST`, `LOG_CHUNK` and `USAGE`, and W5-5a adds `STOP_APP` and `REMOVE_APP` (below), and W7-6d adds `UPDATE_OFFER` and `UPDATE_REPORT` (below).

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

W7-6d adds `update_channel` to `Heartbeat` and validates it; W7-6e sets it from `update.channel` in agent.toml. The agent maps the file's lower-case value to the upper-case wire value: `update.channel = "stable"` reports `STABLE`, `update.channel = "canary"` reports `CANARY`, and an absent key or an absent file reports `STABLE`. The field is empty only on an agent that does not set it, and such an agent is never sent an `UpdateOffer`. Any other non-empty value is rejected: `stable`, `UNSPECIFIED` and whitespace are not accepted. The field carries no `UNSPECIFIED` value; the zero value is empty (`docs/specs/update.md` §1).

## Deploy commands (W2-2)

Four commands, each a protobuf type carried in the envelope. None has a field that is a host shell command or a host path. They have no response type of their own: an `ERROR` envelope reports a message that could not be accepted at all, and a `CommandResult` (W2-8, below) reports what happened when the agent acted on one. The legal order of these commands for one app is `docs/specs/release.md`.

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

W2-8 adds these to the `.proto`, validates them in `internal/protocol`, and regenerates the Go.

Opening a payload rejects unknown fields at every nesting level, including inside `AppState`, before any later code acts.

### Deploy fields

| Field | Type | Rule |
|---|---|---|
| `project`, `environment`, `service` | `string` | Required. Each a name: 1 to 63 bytes of `[a-z0-9-]`, starting and ending with a letter or digit. The same format the control plane uses (`docs/specs/storage.md`), and a valid path element for `agent.toml` blocks. |
| `port` | `uint32` | Optional. 0 means none; otherwise 1 to 65535. The port the container listens on. |
| `health_path` | `string` | Optional. Requires `port`. 1 to 256 bytes, starts with `/`, only `[A-Za-z0-9._~/-]`. |
| `domains` | `repeated string` | Optional, 0 to 16 entries. Requires `port`. Each a lowercase DNS name: at least two labels, each 1 to 63 bytes of `[a-z0-9-]` not starting or ending with `-`, at most 253 bytes, no wildcard, no trailing dot. No duplicates. |
| `has_pre_deploy` | `bool` | Whether a `RunPreDeploy` for this release follows. It is the `hasPreDeploy` input of `release.State.Deploy`. |

**Identity binding.** `SwitchRelease`, `Rollback` and `RunPreDeploy` stay keyed by `app_id` and do not repeat the names. The agent binds an `app_id` to its `project`, `environment` and `service` at the first `Deploy` for it and stores the binding (`docs/specs/agent-state.md`). A later `Deploy` for that `app_id` that names a different project, environment or service is answered `REJECTED`, naming the stored triple, and nothing is done. A control plane cannot move an app into another project's `agent.toml` blocks.

**Without a port** there is no route and no HTTP health check. The service counts as healthy once its container has been running for 10 seconds without a restart (`docs/specs/release.md`). This covers worker services. A `Deploy` with a `healthcheck` (below) uses the Engine's health status instead, port or not; the databases of W6-1 do. There is no `UpdateRoutes` message: changing a domain needs a redeploy.

### CommandResult

A new `MessageType`, `COMMAND_RESULT`, sent by the agent only. Its `request_id` is the `request_id` of the command it answers. The agent sends exactly one per command envelope it accepted: `DEPLOY`, `SWITCH_RELEASE`, `ROLLBACK`, `RUN_PRE_DEPLOY`, `BUILD` (W3-7), `STOP_APP` and `REMOVE_APP` (W5-5b).

| Field | Type | Rule |
|---|---|---|
| `outcome` | `CommandOutcome` | `SUCCEEDED`; `FAILED` (the agent tried and could not finish); or `REJECTED` (refused before acting: an illegal transition, an identity mismatch, a policy rejection, a disk refusal). `UNSPECIFIED` (the zero value) and unknown values are rejected. |
| `detail` | `string` | At most 4096 bytes. Empty on success. Plain text for a person, such as a rendered policy `Rejection`. Never an environment value, a secret or a credential. |
| `app` | `AppState` | Required. The app's release state after the command: `current_release_id`, `previous_release_id`, `candidate_release_id` (each empty or an identifier) and `phase` (`NONE`, `PENDING` or `READY`; `UNSPECIFIED` (the zero value) and unknown values are rejected). |
| `image_id` | `string` | For `DEPLOY` and `BUILD`: the image ID the release runs (`sha256:` plus exactly 64 lowercase hex). Empty otherwise. |
| `repo_digest` | `string` | For `DEPLOY`: the registry digest the reference resolved to (`<repository>@sha256:<64 lowercase hex>`). Empty for an image built on the server. |

**Direction.** Commands travel from the control plane to the agent only; `Heartbeat` and `CommandResult` from the agent to the control plane only. Either side closes the connection on a message travelling the wrong way (`docs/specs/connection.md`).

## Builds and server reports (W3-7)

W3-7 adds them to the `.proto`. Field numbers: `BUILD` = 11; `Build` `app_id` = 1, `release_id` = 2, `project` = 3, `environment` = 4, `service` = 5, `repository` = 6, `commit` = 7; `Heartbeat` `disk` = 2, `deploy_key` = 3; `DiskReport` `data_root_free_bytes` = 1, `data_root_total_bytes` = 2, `agent_dir_free_bytes` = 3, `agent_dir_total_bytes` = 4, `level` = 5; `DiskLevel` `UNSPECIFIED` = 0, `OK` = 1, `LOW` = 2, `CRITICAL` = 3.

`Build`, a new type `BUILD` = 11:

| Field | Type | Rule |
|---|---|---|
| `app_id`, `release_id` | identifiers | As for `Deploy`. The built image is tagged for this release. |
| `project`, `environment`, `service` | names | As for `Deploy`, under the same binding rule. |
| `repository` | `string` | A GitHub repository as `<owner>/<repo>`. `owner` is 1 to 39 bytes of `[A-Za-z0-9-]` not starting with `-`. `repo` is 1 to 100 bytes of `[A-Za-z0-9._-]`, not `.` or `..`, not starting with `-` or `.`. No scheme, host or path: the agent builds the clone URL itself (`docs/specs/build.md`). |
| `commit` | `string` | Exactly 40 lowercase hex characters. No branch or tag names. |

`Heartbeat` fields:

| Field | Type | Rule |
|---|---|---|
| `disk` | `DiskReport` | Optional: absent means not measured. When present, `data_root_free_bytes`, `data_root_total_bytes`, `agent_dir_free_bytes`, `agent_dir_total_bytes` (`uint64`), each free value at most its total, and `level`: `OK`, `LOW` or `CRITICAL` (`docs/specs/disk.md`); `UNSPECIFIED` (0) and unknown levels are rejected. The agent always sends it (W3-6). |
| `deploy_key` | `string` | The server's SSH deploy public key as one OpenSSH line, `ssh-ed25519 <base64> moonglass-deploy`, at most 256 bytes; empty until the key exists (`docs/specs/build.md`). It is a public key, not a secret. |

Free at most total, for each of the two filesystem pairs, is the only rule relating the numbers. Zero totals are allowed when the matching free value is also zero. Nothing ties `level` to the byte counts, because the hysteresis that computes it (W3-6) means one sample cannot give it.

`deploy_key` is exactly `ssh-ed25519`, one single space, non-empty padded standard base64 (RFC 4648 §4, strict, no CR or LF), one single space, `moonglass-deploy`. No newline, no other key type, no other comment. The protocol checks the line's shape only; whether the base64 decodes to a well-formed Ed25519 wire key is W3-8, in `internal/keys`.

Opening a payload rejects unknown fields at every nesting level, including inside `DiskReport`.

## Log and usage streaming (W4-4)

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

## Stop and remove an app (W5-5a)

Two commands, server to agent, each answered with one `CommandResult` (`docs/specs/delete.md`). Each payload has one field, `app_id`, an identifier as for `Deploy`; opening it rejects unknown fields. Field numbers: `STOP_APP` = 15, `StopApp.app_id` = 1; `REMOVE_APP` = 16, `RemoveApp.app_id` = 1. Messages added later take numbers after these.

| Message | Type | Fields |
|---|---|---|
| `StopApp` | `STOP_APP` | `app_id` |
| `RemoveApp` | `REMOVE_APP` | `app_id` |

Both are `delete` class for `agent.toml` (`docs/specs/local-policy.md`). In their `CommandResult`, `image_id` and `repo_digest` are empty. What the agent does with them is `docs/specs/delete.md`.

## Service settings (W6-1a)

These fields carry a service's container settings for the databases of W6-1 and the Compose services of W6-4 (`docs/specs/managed-services.md`, `docs/specs/compose.md`). How the agent applies them is `docs/specs/release.md` ("Service settings, volumes and names"). No field is a host path, and none is run by a host shell: `command` and `healthcheck.test` run inside the container.

`Deploy` field numbers: `env_key` = 11 and `secrets` = 12 (W4-8, `docs/specs/secrets.md`); `command` = 13, `volumes` = 14, `labels` = 15, `restart` = 16, `healthcheck` = 17 (W6-1a); `auto_rollback` = 18 (W4-3a, "Auto-rollback (W4-3a)" below). The numbers do not move.

| Field | Type | Rule |
|---|---|---|
| `command` | `repeated string` | Optional. An exec argv that replaces the image's command: 0 to 32 entries, each 1 to 1024 bytes with no NUL. Never joined, never passed to a host shell, as for `RunPreDeploy.args`. |
| `volumes` | `repeated VolumeMount` | Optional, 0 to 8 entries, no two with the same `target`. |
| `labels` | `map<string, string>` | Optional, 0 to 32 entries. A key is 1 to 128 bytes of `[A-Za-z0-9._/-]`, starting with a letter or digit, and never starting with `dev.moonglass.` (only the agent sets those: ADR-0004 decision 11). A value is at most 1024 bytes with no NUL. Labels are not secrets and may be shown. |
| `restart` | `string` | Empty, `no` or `on-failure`. Empty means `unless-stopped`, the default of every app. |
| `healthcheck` | `Healthcheck` | Optional. Absent means the image's own check, if it has one, which the agent ignores (`docs/specs/release.md`, "Health"). |

`VolumeMount`: `name` = 1, `target` = 2, `read_only` = 3.

| Field | Type | Rule |
|---|---|---|
| `name` | `string` | 1 to 63 bytes of `[a-z0-9_.-]`, starting with a letter or digit. The agent makes the Docker volume name from it and the binding (`docs/specs/release.md`); it is never a host path. |
| `target` | `string` | The path in the container: 2 to 256 bytes, starting with `/`, no NUL, no empty, `.` or `..` element, no trailing `/`. |
| `read_only` | `bool` | |

`Healthcheck`: `test` = 1, `interval_ms` = 2, `timeout_ms` = 3, `start_period_ms` = 4, `start_interval_ms` = 5, `retries` = 6.

| Field | Type | Rule |
|---|---|---|
| `test` | `repeated string` | Docker's form, each entry 1 to 1024 bytes with no NUL: `NONE` alone; `CMD` followed by 1 to 31 argv entries; or `CMD-SHELL` followed by exactly one entry, which the container's own shell runs. Anything else is rejected. |
| `interval_ms`, `timeout_ms` | `uint32` | 1000 to 300000. Required unless `test` is `NONE`. |
| `start_period_ms` | `uint32` | 0 to 600000. |
| `start_interval_ms` | `uint32` | 0, or 1000 to 300000. |
| `retries` | `uint32` | 1 to 10. Required unless `test` is `NONE`. |

With `test` `NONE`, the other five fields must be 0.

Opening a `Deploy` rejects unknown fields inside `VolumeMount` and `Healthcheck`, like everywhere else. W6-1a names `TestDeployServiceSettingsRoundTrip`, `TestDeployServiceSettingsRejectMalformed` (a table with one row per rule above), `TestDeployServiceSettingsRejectUnknownNestedFields` and the fuzz target `FuzzOpenDeployServiceSettings`.

## Update messages (W7-6d)

Specified by `docs/specs/update.md` §1 and §6. W7-6d adds them to the `.proto`. They are wire format: once released they never change.

- `MessageType`: `UPDATE_OFFER` = 17, `UPDATE_REPORT` = 18. Numbers 12 to 16 belong to the live, delete and secrets messages (`docs/specs/live.md`, `docs/specs/delete.md`); the auto-rollback messages take 19 and 20 ("Auto-rollback (W4-3a)" below), and the secrets messages take numbers after 20.
- `UpdateOffer`: `version` = 1. An offer names no URL, path, channel, hash or older version.
- `UpdateReport`: `version` = 1, `sha256` = 2, `outcome` = 3 (`UpdateOutcome`), `reason` = 4 (`UpdateReason`), `proxy_restarted` = 5. The report has no free-text field.
- `Heartbeat`: `update_channel` = 4 (see "Heartbeat" above).
- `UpdateOutcome`: `UPDATE_OUTCOME_UNSPECIFIED` = 0, `UPDATE_OUTCOME_REJECTED` = 1, `UPDATE_OUTCOME_VERIFY_FAILED` = 2, `UPDATE_OUTCOME_SWITCHING` = 3, `UPDATE_OUTCOME_PASSED` = 4, `UPDATE_OUTCOME_REVERTED` = 5, `UPDATE_OUTCOME_CHECK_FAILED` = 6.
- `UpdateReason`: `UPDATE_REASON_UNSPECIFIED` = 0, `NOT_NEWER` = 1, `NOT_ON_CHANNEL` = 2, `REVERTED_BEFORE` = 3, `TRIAL_IN_PROGRESS` = 4, `POINTER` = 5, `DISK` = 6, `DOWNLOAD` = 7, `TOO_LARGE` = 8, `SIGNATURE` = 9, `CHECKSUM` = 10, `POINTER_MISMATCH` = 11, `WRITE` = 12, `RESTARTED` = 13, `NOT_CONNECTED` = 14, `EXE_MISMATCH` = 15, `PROXY` = 16, `DEADLINE` = 17, each with the prefix `UPDATE_REASON_`.

Protobuf enum values share the package scope, so every value of both new enums carries its prefix (`REJECTED` and `TOO_LARGE` collide with names outside them). In prose the names appear without the prefix. `docs/specs/update.md` §1 and §6 give the behaviour.

The validation rules, checked when a payload is opened:

- `version` in both messages: `vMAJOR.MINOR.PATCH`, three numeric identifiers of one or more ASCII digits, no leading zero unless the identifier is exactly `0`. No pre-release, no build metadata, no whitespace and no other character.
- `sha256`: empty when `outcome` is `REJECTED`; exactly 64 lowercase hex characters for every other outcome.
- `outcome`: one of the six values. `UNSPECIFIED` and unknown numbers are rejected.
- `reason`: unknown numbers are rejected, and each reason belongs to one outcome. `NOT_NEWER`, `NOT_ON_CHANNEL`, `REVERTED_BEFORE`, `TRIAL_IN_PROGRESS` and `POINTER` belong to `REJECTED`; `DISK`, `DOWNLOAD`, `TOO_LARGE`, `SIGNATURE`, `CHECKSUM`, `POINTER_MISMATCH` and `WRITE` to `VERIFY_FAILED`; `RESTARTED`, `NOT_CONNECTED`, `EXE_MISMATCH`, `PROXY` and `DEADLINE` to `REVERTED` and `CHECK_FAILED`; `SWITCHING` and `PASSED` allow `UNSPECIFIED` only.
- `proxy_restarted`: `false` with `REJECTED` and `VERIFY_FAILED`; either value otherwise.
- Unknown fields are rejected at every nesting level, as for every other payload.

## Auto-rollback (W4-3a)

Specified by `docs/specs/auto-rollback.md`. W4-3a adds them to the `.proto`. They are wire format: once released they never change.

- `MessageType`: `AUTO_ROLLBACK_REPORT` = 19 (agent to server), `AUTO_ROLLBACK_ACK` = 20 (server to agent, empty payload, the report's `request_id`).
- `Deploy`: `auto_rollback` = 18, an `AutoRollback` message. Optional: absent means the defaults of `docs/specs/auto-rollback.md`, "Settings".
- `AutoRollback`: `off` = 1, `error_rate_enforce` = 2, `restart_limit` = 3, `health_fail_seconds` = 4, `error_rate_percent` = 5, `error_rate_multiple` = 6, `error_rate_min_requests` = 7. The two `bool`s are free. Each `uint32` must lie in its range in "Settings": a present message sets all five, and 0 is rejected.
- `AutoRollbackReport`: `app_id` = 1, `release_id` = 2, `previous_release_id` = 3, `trigger` = 4 (`AutoRollbackTrigger`), `action` = 5 (`AutoRollbackAction`), `detail` = 6, `restart_count` = 7, `health_failing_seconds` = 8, `window_requests` = 9, `window_5xx` = 10, `previous_requests` = 11, `previous_5xx` = 12, `previous_measured` = 13, `app` = 14 (`AppState`, as in `CommandResult`), `detected_at` = 15.
- `AutoRollbackTrigger`: `AUTO_ROLLBACK_TRIGGER_UNSPECIFIED` = 0, `RESTARTS` = 1, `HEALTH` = 2, `ERROR_RATE` = 3, each with the prefix `AUTO_ROLLBACK_TRIGGER_`.
- `AutoRollbackAction`: `AUTO_ROLLBACK_ACTION_UNSPECIFIED` = 0, `ROLLED_BACK` = 1, `SHADOW` = 2, `HELD` = 3, `SKIPPED` = 4, `FAILED` = 5, each with the prefix `AUTO_ROLLBACK_ACTION_`.

The validation rules, checked when a payload is opened:

- `request_id` of a report and its ack: `a` followed by exactly 32 lowercase hex characters.
- `app_id`, `release_id`, `previous_release_id`: identifiers, as for `Deploy`; all three required, and `release_id` differs from `previous_release_id`.
- `trigger` and `action`: one of their values; `UNSPECIFIED` and unknown numbers are rejected. `SHADOW` belongs to `ERROR_RATE` only.
- `detail`: at most 4096 bytes; empty unless `action` is `FAILED`, and required then. The same rule as `CommandResult.detail`: never an environment value, a secret or a credential.
- The numbers (`uint32` `restart_count` and `health_failing_seconds`; `uint64` for the four counts): `window_5xx` at most `window_requests` and `previous_5xx` at most `previous_requests`; with `previous_measured` false both previous counts are 0. `restart_count` is set only for `RESTARTS`, `health_failing_seconds` only for `HEALTH`, the four counts only for `ERROR_RATE`; the others are 0.
- `app`: required, under the rules of `CommandResult.app`.
- `detected_at`: RFC 3339 UTC with a `Z`, at most 64 bytes.
- Unknown fields are rejected at every nesting level, as for every other payload.

**Direction.** `AUTO_ROLLBACK_REPORT` travels from the agent only and `AUTO_ROLLBACK_ACK` from the control plane only; the wrong way closes the connection (`docs/specs/connection.md`).

## Secret delivery (W4-8) and reveal (W4-9)

Those messages are specified in `docs/specs/secrets.md` and added to the `.proto` by those tickets. `Deploy.env_key` is field 11 and `Deploy.secrets` is field 12, so they cannot collide with the service settings above.

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
