# ADR-0011: Proxy dependencies

- Status: accepted
- Date: 2026-11-16

## Context

W1-5 adds `moonglass proxy`, the second process of ADR-0001. ADR-0001 decides that the proxy embeds Caddy 2.11.9 as a library. A new module requires an ADR and a depguard entry. The binary must stay a static, cgo-free ELF (`make build-all`), and the agent role must never load Caddy.

## Decision

Pin `github.com/caddyserver/caddy/v2 v2.11.9` in `go.mod` and in the depguard allow list in `.golangci.yml`, and import it only from `internal/proxy`.

- `cmd/moonglass` calls `proxy.Run` and does not import Caddy. depguard denies Caddy from `cmd/`, `internal/agent`, `internal/keys`, `internal/protocol` and `internal/policy`.
- `internal/proxy` imports only the Caddy packages it serves with: the core, `caddyhttp` with `reverseproxy`, `caddytls`, `caddypki` (the internal issuer needs the `pki` app) and `logging`. It does not import `caddy/v2/modules/standard`.
- Caddy's transitive modules stay indirect in `go.mod`.
- A proxy upgrade is a short, announced, drained restart. No socket activation and no `SO_REUSEPORT` is built; ADR-0001 keeps the open question that decides the upgrade mechanism.

## Consequences

- The release binary links Caddy, so it grows. Its idle memory is measured by W1-7, not here.
- Only the agent reads the counters, over the admin socket; the proxy never touches Docker.
- Upgrading Caddy is a change to this ADR and to `go.mod`, in one ticket.

## Alternatives rejected

- **`caddy/v2/modules/standard`.** Links every Caddy module, including ones we never configure, for no benefit.
- **Forking or patching Caddy.** ADR-0001 forbids it.

## Open questions

None. SPIKE-1's questions live in ADR-0001.

## Links

- `docs/specs/proxy.md`
- `docs/adr/0001-two-process-proxy.md`
- `docs/threat-model.md` boundary B4
- B§6: W1-5
