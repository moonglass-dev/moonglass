---
verdict: ready
reason: ""
---

# Plan for FR1-10

## Files to change

- `.golangci.yml`, under `linters.settings.depguard.rules` only:
  - `policy-deps`: replace the whole rule with:
    ```yaml
    policy-deps:
      # compose-go and go-toml are imported only by internal/policy
      # (ADR-0009). Every file outside internal/policy is covered, so new
      # packages are too.
      list-mode: lax
      files:
        - "$all"
        - "!**/internal/policy/**"
      deny:
        - pkg: github.com/compose-spec/compose-go/v2
          desc: only internal/policy may import compose-go (ADR-0009)
        - pkg: github.com/pelletier/go-toml/v2
          desc: only internal/policy may import go-toml (ADR-0009)
    ```
    The pgx deny entry and the old comment saying file exclusions "were not applied" are deleted.
  - Add a new `pgx-deps` rule directly after `policy-deps`:
    ```yaml
    pgx-deps:
      # pgx is imported only by internal/store/pg (ADR-0010). Every file
      # outside internal/store/pg is covered, so new packages are too.
      list-mode: lax
      files:
        - "$all"
        - "!**/internal/store/pg/**"
      deny:
        - pkg: github.com/jackc/pgx/v5
          desc: only internal/store/pg may import pgx (ADR-0010)
    ```
  - `sqlite-deps`: replace the comment and the hand-written `files` list. The `deny` entry and its desc stay as they are:
    ```yaml
    sqlite-deps:
      # modernc sqlite is imported only by internal/store/sqlite and
      # internal/agent/state (ADR-0010, amended 2026-11-17). Every file
      # outside those two directories is covered, so new packages are too.
      list-mode: lax
      files:
        - "$all"
        - "!**/internal/store/sqlite/**"
        - "!**/internal/agent/state/**"
      deny:
        - pkg: modernc.org/sqlite
          desc: only internal/store/sqlite and internal/agent/state may import modernc sqlite (ADR-0010)
    ```
  - Nothing else changes: `main`, `proxy-deps`, `websocket-deps`, `huma-deps`, `age-deps` (including its comment), the other linters, exclusions and formatters stay as they are. The diff is about 40 lines, under `max_diff_lines: 80`.
- `docs/tickets/FR1-10/`: the worker records the bite check here (see Tests). No code is committed for it.

## Tests to write

The ticket names no Go tests, and `testdata/tests.txt` does not change. Verification:

- `make lint` and `make ci` are green on the branch. This shows the five current importers still lint clean: `internal/policy/compose.go`, `internal/policy/parse.go`, `internal/store/pg/pg.go`, `internal/store/sqlite/sqlite.go` and `internal/agent/state/state.go`. A grep of every `.go` file finds no other importer, test files included.
- Bite check, run once by hand and recorded in `docs/tickets/FR1-10/bite-check.md` with the command and the depguard lines it printed. Each case is a temporary file added, linted and deleted:
  1. `internal/server/zz_depguard_probe.go` (package `server`) with blank imports of `github.com/jackc/pgx/v5`, `modernc.org/sqlite`, `github.com/compose-spec/compose-go/v2/loader` and `github.com/pelletier/go-toml/v2`. `make lint` fails with all four descs: pgx (ADR-0010), modernc (ADR-0010), compose-go (ADR-0009) and go-toml (ADR-0009).
  2. `internal/store/zz_depguard_probe.go` (package `store`) with blank imports of pgx and modernc. Lint fails with the pgx-deps and sqlite-deps descs.
  3. `internal/store/sqlite/zz_depguard_probe.go` (package `sqlite`) with a blank import of pgx. Lint fails with the pgx-deps desc, and there is no sqlite-deps error, because modernc is allowed there.
  
  Each probe file is deleted before commit, and `git status` shows only `.golangci.yml` and the ticket docs. Blank-importing the root `github.com/jackc/pgx/v5` package (or a module subpackage) is enough, because depguard matches by prefix. All of these modules are already required in `go.mod`, so no `go.mod` change is needed.

## Edge cases

- Overlap: each module is denied by exactly one rule. compose-go and go-toml are only in policy-deps, pgx only in pgx-deps, modernc only in sqlite-deps. `main` (strict) still allows all four, so an excluded directory passes `main` and is not matched by its own deny rule. Bite cases 1 and 2 show one error per module.
- Exclusions too wide: the patterns are exactly the ADR-named directories. `!**/internal/store/pg/**` does not exempt `internal/store` top level from pgx, as bite case 2 shows. `!**/internal/store/sqlite/**` does not exempt sqlite from pgx, as bite case 3 shows.
- Test files: `$all` covers `_test.go` files as well, so a test in another package cannot import a driver either. No current test does.
- `tools/` and `cmd/`: both are covered by `$all`, as the old sqlite list intended.
- `internal/agent` top level: now denied modernc through `$all`, and its subpackage `state` is excluded. This replaces the old `**/internal/agent/*.go` pattern.

## Risks

- `$all` with `!` might not behave as assumed in the pinned golangci-lint (v2, `golangci-lint-action` v9.3.0). `huma-deps` already uses exactly this form, and R-1 confirms it works. The bite check proves each new rule fails where it should.
- A reviewer may flag that the comment in `age-deps` ("file exclusions were not applied for policy-deps") still has the false claim. The ticket puts `age-deps` out of scope, so it stays unchanged, and the worker says so in the result.
- A probe file left in by mistake would break CI and fall outside `allowed_paths`. The worker checks `git status` and `git diff --stat` before finishing.
- YAML style: `sqlite-deps` moves from the flow form `files: [...]` to the block form, to match `huma-deps`. It stays inside `.golangci.yml` and does not change `make fmt-check`.
