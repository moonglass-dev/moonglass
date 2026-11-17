# ADR-0013: API dependencies

- Status: accepted
- Date: 2026-11-17

## Context

W2-1 serves the operation registry over REST (ADR-0006, stack §3.1). Stack §3.1 names Huma for it, so the OpenAPI description comes from the same typed declarations. A new module requires an ADR and a depguard entry.

## Decision

Pin `github.com/danielgtaylor/huma/v2 v2.40.0` in `go.mod` and in the depguard allow list in `.golangci.yml`, and import it only from `internal/api`.

- `internal/api` uses the standard-library adapter `adapters/humago` on `net/http`. No Chi, Fiber, Gin or other adapter is imported.
- `internal/registry`, `internal/cli`, `internal/server` and `cmd/moonglass` do not import Huma. depguard denies it from every package except `internal/api`.
- Huma's docs, schema and OpenAPI routes are switched off, so the server mounts operations only. The OpenAPI file is generated and checked in.
- The module is pure Go, so `cgo` stays disabled and `make build-all` still produces a static ELF.

## Consequences

- REST handlers and the OpenAPI description share one set of Go types.
- Upgrading Huma is a change to this ADR and to `go.mod`, in one ticket, and regenerates `internal/api/openapi.json`.

## Alternatives rejected

- **Hand-written handlers and OpenAPI.** The two drift, and every handler would repeat input validation.
- **Another Huma adapter.** The standard-library mux is enough, and a second router is a second thing to read.

## Open questions

None.

## Links

- `docs/specs/registry.md`
- ADR-0006
- S§3.1
