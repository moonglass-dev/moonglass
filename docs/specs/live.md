# Live logs and current usage

The owner approved these messages on 2026-11-17. W4-4 builds them. The fields are `docs/specs/protocol.md`. What each side sends and accepts on the agent connection is `docs/specs/connection.md`.

## Messages

`LOG_REQUEST` = 12, server to agent. `app_id` is required. An empty `release_id` means the current release. `tail` is a `uint32` from 0 to 1000. `follow` is a bool. `grep` is empty or 1 to 128 bytes, contains no NUL, and is a literal substring.

One open log stream per app. A new `LOG_REQUEST` for that app replaces the previous one.

`LOG_CHUNK` = 13, agent to server, with the same `request_id`. `data` is 1 to 65536 bytes on a non-final chunk. `final` is a bool. When the app has no container, the agent sends one chunk with `final` true and empty `data`. That does not close the connection.

`USAGE` = 14, agent to server, every 10 seconds, only while that app has a usage subscriber. It carries `app_id`, `cpu_percent` (`uint32`), `mem_bytes` and `mem_limit_bytes` from cgroup v2 `memory.current` and `memory.max`, `net_rx_bytes` and `net_tx_bytes` from the veth counters, and `disk_read_bytes` and `disk_write_bytes` from `io.stat`. The counters are cumulative. If the cgroup files are missing, the agent omits the sample. Nothing is written to SQLite.

## Request ids and reconnect

Stream request ids are `l` followed by 32 lowercase hex characters. On agent reconnect, the server re-opens streams that still have SSE subscribers. `Last-Event-ID` is ignored.

## SSE

SSE is not a registry operation. The routes are on `registry.NonOperationRoutes`, each authorised beside its entry as a viewer read of that project and environment:

- `GET /v1/logs/stream?project&environment&service`
- `GET /v1/usage/stream?project&environment&service`

The event names are `log` and `usage`. The server sends a comment keepalive every 15 seconds. `project`, `environment` and `service` are storage names (`docs/specs/storage.md`).

Chunks are relayed and stored nowhere: no row, no audit event, no server log line.
