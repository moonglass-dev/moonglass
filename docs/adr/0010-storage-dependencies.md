# ADR-0010: Storage dependencies

- Status: accepted
- Date: 2026-11-16

## Context

W1-2 adds `internal/store`, the one storage interface of ADR-0002 with a Postgres backend (hosted) and a SQLite backend (self-hosted). The repository had no database driver. A new module requires an ADR and a depguard entry. The self-hosted binary must stay a static, cgo-free ELF (`make build-all`).

## Decision

Pin these two modules, and import them only from `internal/store` and its subpackages:

- `github.com/jackc/pgx/v5 v5.13.0` — the Postgres driver. Its `stdlib` package gives `database/sql`, so one migration runner and one query layer serve both backends.
- `modernc.org/sqlite v1.62.0` — a pure-Go SQLite driver, so `CGO_ENABLED=0` holds.

Both versions are recorded in `go.mod` and in the depguard allow list in `.golangci.yml`. No ORM and no migration library are added: migrations are numbered plain SQL applied by a small runner in `internal/store` (`docs/specs/storage.md`).

## Consequences

- Only `internal/store/pg` imports pgx and only `internal/store/sqlite` imports modernc. Nothing else writes SQL.
- Both drivers have transitive modules (`modernc.org/libc`, `golang.org/x/sys` and others) that stay indirect.
- `cmd/moonglass` does not import the store yet, so release binaries do not link the drivers until a later ticket wires it. `make build-all` stays static.
- CI runs `postgres:18` as a service and sets `MOONGLASS_TEST_POSTGRES` (ADR-0002 decision 13).

## Alternatives rejected

- **`lib/pq`.** In maintenance mode; pgx is the maintained driver.
- **`mattn/go-sqlite3`.** Requires cgo, which breaks the static build.
- **An ORM or a migration library.** More surface than two small schemas need; the conformance suite tests the SQL directly.

## Open questions

None.

## Links

- `docs/specs/storage.md`
- `docs/adr/0002-control-plane-storage.md`
- `docs/plan/stack.md`
- B§6: W1-2
