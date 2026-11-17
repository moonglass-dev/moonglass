# ADR-0012: Connection dependencies

- Status: accepted
- Date: 2026-11-17

## Context

W1-4 adds the agent's outbound WSS connection to the control plane (stack §3.1, threat-model boundary B3). Stack §3.1 names `coder/websocket` for it. A new module requires an ADR and a depguard entry.

## Decision

Pin `github.com/coder/websocket v1.8.15` in `go.mod` and in the depguard allow list in `.golangci.yml`, and import it only from `internal/agent` and `internal/server`.

- `cmd/moonglass` calls into `internal/agent` and `internal/server` and does not import the module. depguard denies it from `cmd/`, `internal/protocol`, `internal/policy`, `internal/keys` and `internal/store`.
- TLS identity code lives in `internal/keys` and uses the standard library only.
- The module is pure Go, so `cgo` stays disabled and `make build-all` still produces a static ELF.

## Consequences

- One WebSocket implementation serves both ends of the connection.
- Upgrading `coder/websocket` is a change to this ADR and to `go.mod`, in one ticket.

## Alternatives rejected

- **`gorilla/websocket`.** Stack §3.1 picks `coder/websocket`, which takes a `context` on every call and needs no extra locking for concurrent writers.
- **A hand-written WebSocket framing layer.** More trusted-core code to read for no gain.

## Open questions

None.

## Links

- `docs/specs/connection.md`
- `docs/threat-model.md` boundary B3
- S§3.1
