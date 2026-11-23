# ADR-0010: Storage dependencies

- Status: accepted
- Date: 2026-11-16
- Amended: 2026-11-17, agent state (owner decision D2, weeks 2–3 preflight)
- Amended: 2026-11-18, which packages import each driver (owner decision, FR1-10)
- Amended: 2026-11-22, pgx withdrawn: SQLite only (ADR-0002 as amended; the plan audit, `docs/review/plan-audit.md`). FA1-1 removes pgx from `go.mod`, its depguard rule `pgx-deps`, the CI `postgres:18` service and `MOONGLASS_TEST_POSTGRES`. Until FA1-1 merges, the code still matches the pgx text below; nothing new may use pgx.

## Context

W1-2 adds `internal/store`, the one storage interface of ADR-0002 with a Postgres backend (hosted) and a SQLite backend (self-hosted). The repository had no database driver. A new module requires an ADR and a depguard entry. The self-hosted binary must stay a static, cgo-free ELF (`make build-all`).

## Decision

Pin these two modules. Each is imported by exactly one backend package: pgx only by `internal/store/pg`, and `modernc.org/sqlite` only by `internal/store/sqlite` (and `internal/agent/state`, by the amendment below). The rule covers every Go file, tests included: no other package and no test file outside those directories imports either module or any of its subpackages.

- _Withdrawn 2026-11-22:_ `github.com/jackc/pgx/v5 v5.13.0` — the Postgres driver.
- `modernc.org/sqlite v1.62.0` — a pure-Go SQLite driver, so `CGO_ENABLED=0` holds.

Both versions are recorded in `go.mod` and in the depguard allow list in `.golangci.yml`. No ORM and no migration library are added: migrations are numbered plain SQL applied by a small runner in `internal/store` (`docs/specs/storage.md`).

**Amendment, 2026-11-17: the agent's own database.** `modernc.org/sqlite` may also be imported by `internal/agent/state`, the agent's on-server database (`docs/specs/agent-state.md`). It is the only SQL outside `internal/store`. It reuses the `internal/store` migration runner (`store.Migrate`), never a second one. It is not trusted core, because of this condition:

- `internal/agent/state` holds release and command records only. **No key material ever goes there**: no private key, no wrapped or unwrapped environment key, no agent X25519 or Ed25519 key, no deploy key, no token, no secret value or ciphertext, and no registry credential. Those stay under `internal/keys`, in its own files.
- If a later ticket needs key material in the agent database, that table moves into trusted core first, by an amendment to this ADR, and is not added to `internal/agent/state`.

**Amendment, 2026-11-18: one importer per driver.** The Decision said `internal/store` and its subpackages; the Consequences said only `internal/store/pg` imports pgx. The owner chose the narrower rule, now in the Decision above, so lint, tests and this ADR say the same thing.

- The depguard rule `pgx-deps` stays: pgx is denied in every file except under `internal/store/pg`, with no exclusion for test files.
- `internal/store/projects_conformance_test.go` must stop importing `github.com/jackc/pgx/v5/pgconn`. It checks the SQLSTATE through the standard library instead: `errors.As` into `interface{ SQLState() string }`, which `*pgconn.PgError` implements, and a comparison with `"42501"`. The assertion stays exactly as strict.
- FR1-10 may make that change to that one test file, alongside `.golangci.yml`. It changes the import and the type the error is matched into, and nothing else in the test.

## Consequences

- Only `internal/store/pg` imports pgx. Only `internal/store/sqlite` and `internal/agent/state` import modernc. Nothing else writes SQL.
- The control-plane schema checks (the two-backend parity test and the lookup-table list) cover `internal/store` only and never the agent database.
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
- `docs/specs/agent-state.md`
- `docs/adr/0002-control-plane-storage.md`
- `docs/plan/stack.md`
- B§6: W1-2
