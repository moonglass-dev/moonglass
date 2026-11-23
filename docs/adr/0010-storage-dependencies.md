# ADR-0010: Storage dependencies

- Status: accepted
- Date: 2026-11-16

## Context

`internal/store` is the one storage interface of ADR-0002, with one SQLite backend. The agent keeps its own release and command records on each server (`docs/specs/agent-state.md`). A new module requires an ADR and a depguard entry, and the binary must stay a static, cgo-free ELF (`make build-all`).

## Decision

1. Pin `modernc.org/sqlite v1.62.0`, a pure-Go SQLite driver, so `CGO_ENABLED=0` holds. The version is recorded in `go.mod` and in the depguard allow list in `.golangci.yml`.
2. **One importer per database.** Only `internal/store/sqlite` (the control plane) and `internal/agent/state` (the agent's own database) import the driver or any of its subpackages. The rule covers every Go file, tests included, and depguard enforces it.
3. **No ORM and no migration library.** Migrations are numbered plain SQL applied by the small runner in `internal/store` (`docs/specs/storage.md`). `internal/agent/state` reuses that runner (`store.Migrate`), never a second one.
4. **The agent database holds no key material.** `internal/agent/state` holds release and command records only: no private key, no wrapped or unwrapped environment key, no agent X25519 or Ed25519 key, no deploy key, no token, no secret value or ciphertext, and no registry credential. Those stay under `internal/keys`, in its own files. That is why `internal/agent/state` is not trusted core. A ticket that needs key material in the agent database moves that table into trusted core first, through a change to this ADR.

## Consequences

- Only `internal/store/sqlite` and `internal/agent/state` import the driver. Nothing else writes SQL.
- The control-plane schema checks (the lookup-table list) cover `internal/store` only and never the agent database.
- The driver's transitive modules (`modernc.org/libc`, `golang.org/x/sys` and others) stay indirect.
- `make build-all` stays static.

## Alternatives rejected

- **`mattn/go-sqlite3`.** Requires cgo, which breaks the static build.
- **A Postgres driver.** There is no Postgres backend (ADR-0002).
- **An ORM or a migration library.** More surface than two small schemas need; the conformance suite tests the SQL directly.

## Links

- `docs/specs/storage.md`
- `docs/specs/agent-state.md`
- `docs/adr/0002-control-plane-storage.md`
- `docs/plan/stack.md`
- B§3: W1-2, W2-13, FA1-1
