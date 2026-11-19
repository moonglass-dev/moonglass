---
verdict: ready
reason: ""
---

# Plan for FR2-4

## Files to change

- `.golangci.yml`, under `linters.settings.depguard.rules`, and nowhere else:
  - `age-deps`: replace the comment and the one-line `files` list with:
    ```yaml
    age-deps:
      # age is imported only by internal/keys (ADR-0015 decision 2). Every
      # file outside internal/keys is covered, so new packages are too.
      list-mode: lax
      files:
        - "$all"
        - "!**/internal/keys/**"
      deny:
        - pkg: filippo.io/age
          desc: only internal/keys may import age (ADR-0015)
    ```
    The sentence "depguard file exclusions were not applied for policy-deps, so every package that exists today is listed." is deleted.
  - `docker-deps`: replace the fixed list with:
    ```yaml
    docker-deps:
      # The Docker client (github.com/moby/moby/client and /api) is imported
      # only by internal/deploy, internal/build and internal/agent/disk
      # (ADR-0014 decision 2). Every other file is covered, so new packages
      # are too.
      list-mode: lax
      files:
        - "$all"
        - "!**/internal/deploy/**"
        - "!**/internal/build/**"
        - "!**/internal/agent/disk/**"
      deny:
        - pkg: github.com/moby/moby
          desc: only internal/deploy, internal/build and internal/agent/disk may import the Docker client (ADR-0014)
    ```
  - New `deploy-deps`, placed right after `docker-deps`:
    ```yaml
    deploy-deps:
      # internal/deploy never imports internal/proxy, which links Caddy, and
      # never imports Caddy itself (docs/specs/release.md "Proxy config",
      # ADR-0011). The agent pushes proxy config over the admin socket.
      list-mode: lax
      files:
        - "**/internal/deploy/**"
      deny:
        - pkg: moonglass.dev/moonglass/internal/proxy
          desc: internal/deploy never imports internal/proxy, which links Caddy (docs/specs/release.md, ADR-0011)
        - pkg: github.com/caddyserver/caddy/v2
          desc: only internal/proxy may import Caddy (ADR-0011)
    ```
  - `main`, `proxy-deps`, `websocket-deps`, `huma-deps`, `sqlite-deps`, `policy-deps` and `pgx-deps` are unchanged.
- `docs/tickets/FR2-4/`: the worker's result file with the verification commands and their output.

## Tests to write

- None. The ticket names no tests (`tests: []`) and changes no Go code, so `testdata/tests.txt` stays as it is.
- Verification uses scratch files that are not committed. Each file holds only `package <dir name>` and one blank import, and is deleted after the run. For every case, run `golangci-lint run ./<dir>/...` and record the command and its depguard line in the result file:
  1. `_ "filippo.io/age"` in `internal/deploy/zz_scratch.go`, `internal/update/zz_scratch.go` and `tools/pipeline/zz_scratch.go`: each reports `age-deps`.
  2. `_ "github.com/moby/moby/client"` in `internal/agent/zz_scratch.go`, `internal/keys/zz_scratch.go` and `internal/scratch/zz_scratch.go` (a new directory, `package scratch`): each reports `docker-deps`.
  3. `_ "moonglass.dev/moonglass/internal/proxy"` and, in a second run, `_ "github.com/caddyserver/caddy/v2"` in `internal/deploy/zz_scratch.go`: each reports `deploy-deps`.
  4. A negative check: `_ "github.com/moby/moby/client"` in `internal/deploy/zz_scratch.go` reports no `docker-deps` error. This shows the exclusion works.
- Then delete every scratch file and `internal/scratch/`. Confirm with `git status --porcelain` that only `.golangci.yml` and the ticket docs changed, and run `make lint` and `make ci` green.

## Edge cases

- Coverage is a superset of the old rules. `$all` with exclusions covers every directory the old fixed lists named. `age-deps` now also covers `internal/deploy`, `internal/update` and `tools/`. `docker-deps` only excludes the three ADR-0014 packages, and it is the first rule to cover packages the plan adds later. Cases 1 and 2 show this.
- Test files are covered: `$all` matches `_test.go` files too. `internal/keys/record_test.go` (age) and the `internal/deploy/*_test.go` files (moby) sit inside their exclusions, so they stay legal.
- `github.com/moby/moby` is a prefix deny. It covers both `/client` and `/api`, the same prefix the existing rule uses.
- Rules overlap without cancelling each other: depguard v2 checks every rule whose `files` match. The strict `main` allow of `moonglass.dev/moonglass` does not cancel the `deploy-deps` deny of `internal/proxy`. Case 3 shows this.
- `internal/build` and `internal/agent/disk` do not exist yet. Excluding paths that don't exist is harmless, and it matches ADR-0014 decision 2.
- Glob form follows the working `policy-deps` and `pgx-deps` rules (`"!**/<dir>/**"`), so the exclusions apply the same way.

## Risks

- `make lint` could fail on existing code if a boundary is already broken. Checked on the current tree: only `internal/keys` imports age, only `internal/deploy` imports moby, and `internal/deploy` imports neither `internal/proxy` nor Caddy. Run `make lint` before writing up results.
- A scratch file could be committed by mistake. Use a unique name (`zz_scratch.go`), delete it right after each run, and check `git status` before finishing.
- Diff budget (80 lines): about 25 lines removed and 40 added, which fits.
- The local golangci-lint (2.15.0) may differ from the version pinned in CI (golangci-lint-action v9.3.0). Run `make lint` with the pinned version if the Makefile picks it. The config uses only keys that already work in this file.
- No rule is loosened. `proxy-deps` and `websocket-deps` are untouched, as the ticket says (out of scope).
