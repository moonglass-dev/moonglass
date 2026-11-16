# W0-1 — Bootstrap the Moonglass repository

You are bootstrapping the repository for **Moonglass**, an open-source deploy platform for your own servers. The working directory, `/Users/moonglass-dev/moonglass`, holds only planning documents and is not a git repository yet.

This ticket creates the repository's plumbing and the standing instructions every later agent will follow. **It contains no product code.**

## Read first

1. `build-plan.md`, all of it: roles, the ticket loop, review levels and change control.
2. `stack.md`: the five rules, §2, §3.2, §7, §8, §9.
3. `milestones.md` §1, and `vision.md` §1.2 (voice).
4. Skim `research.md`; you do not need it for this ticket.

## Principles, non-negotiable

- **Super clean and minimal.** No placeholders, no stub subcommands that print "not implemented", no TODO/FIXME, no commented-out code, no "just in case" options, no fallback or compatibility paths. If this ticket does not need something, it does not exist yet.
- **No guessing versions.** Check the current stable releases of Go, golangci-lint and each GitHub Action live before pinning them, and say in your report where you checked.
- **Stdlib first.** No Go module dependencies in this ticket. golangci-lint is a tool, not a module dependency.
- If anything here conflicts with the planning documents, **stop and ask** rather than choosing.

## Do

1. **Git.** `git init -b main`. No remote, no push.

2. **Move the planning documents**, content unchanged:
   ```
   docs/plan/research.md  vision.md  stack.md  milestones.md  build-plan.md
   docs/prompts/01-bootstrap-repo.md  02-contracts.md
   ```
   Create a directory only when a file goes into it in this ticket.

3. **Go module** `moonglass.dev/moonglass`, using the current stable Go release, with a `toolchain` line.

4. **`cmd/moonglass/main.go`**, the single binary, standard library only.
   - Exactly one subcommand for now: `moonglass version`. It prints version, commit and build date, injected through `-ldflags`, defaulting to `dev`, `unknown`, `unknown`.
   - `moonglass` with no arguments, or with an unknown subcommand, prints a short usage to stderr and exits with status 2.
   - Tests cover both behaviours.
   - Do **not** add `server`, `agent`, `proxy` or `import`. Later tickets add each one with real behaviour.

5. **`Makefile`**, with these targets and no others:
   - `build`: static binary for the host into `bin/`, `CGO_ENABLED=0`, `-trimpath`, version ldflags from `git describe`.
   - `build-all`: `linux/amd64` and `linux/arm64` static binaries into `bin/`.
   - `fmt`: apply the formatters. `fmt-check`: fail if anything is unformatted.
   - `lint`: golangci-lint.
   - `test`: `go test -count=1 -shuffle=on ./...`. `test-one`: `PKG=` and `NAME=` select one test.
   - `race`: tests with `-race`.
   - `test-list`: write a sorted, deterministic list of every test as `package TestName` to `testdata/tests.txt`. `check-test-list`: fail if the committed file differs from a fresh run. This is what makes a deleted test visible in review.
   - `ci`: `fmt-check`, `lint`, `test`, `race`, `check-test-list`, `build-all`, in that order.
   - No targets for things that do not exist yet (`proto`, `ui`, `dev`). Their tickets add them.

6. **`.golangci.yml`**, golangci-lint v2 format.
   - Linters: `errcheck`, `govet` (all analyzers), `staticcheck`, `unused`, `ineffassign`, `gosec`, `errorlint`, `exhaustive`, `godox` (TODO, FIXME, XXX, HACK), `nolintlint` (require a specific linter and a reason), `depguard`, `forbidigo`.
   - `depguard`: allow the standard library and `moonglass.dev/moonglass` only. Add a comment that a new dependency requires an ADR (`docs/adr/`) and an entry here.
   - `forbidigo`: no `fmt.Print*` and no `log.Print*` outside `cmd/`.
   - Formatters: `gofumpt` and `goimports` (local prefix `moonglass.dev/moonglass`).
   - Add no linters beyond this list.

7. **CI**, `.github/workflows/ci.yml`.
   - Triggers: pull requests, and pushes to `main`.
   - Runs on `ubuntu-latest`. Go version from `go.mod`, with the module cache. golangci-lint at a pinned version. Runs `make ci`.
   - Every action pinned to a full commit SHA, with the version in a comment.
   - `permissions: contents: read`. Concurrency cancels superseded runs on the same branch.
   - Nothing else yet. The Docker matrix, Postgres service and deploy suite arrive with their tickets.

8. **`AGENTS.md`**, at most 150 lines, factual, no shouting. Sections:
   - **What this is:** two lines on what Moonglass is. The binary is `moonglass`, with roles `server`, `agent`, `proxy` and the CLI as they are added. Config lives at `/etc/moonglass/agent.toml`, data at `/var/lib/moonglass`, environment variables are `MOONGLASS_*`, the domain is moonglass.dev. The product was renamed; never reintroduce the old working name (see `docs/plan/research.md`). Do not write the old name in this file.
   - **Sources of truth:** `docs/plan/`, `docs/adr/`, `docs/specs/`, `docs/threat-model.md`. Stop and ask before changing a contract (`.proto` files, registry operations, the `agent.toml` schema, the storage schema) or an ADR.
   - **Commands:** the exact `make` targets, with one line each.
   - **Engineering rules:**
     - one way to do each thing
     - no fallback, legacy or compatibility code; when a design changes, delete the old path in the same change
     - fail loudly, with errors wrapped in context (`fmt.Errorf("…: %w", err)`) that say what to do
     - no new dependency without an ADR
     - standard library first
     - plain functions, plain SQL, obvious names
     - authorisation declared next to the operation, never in hidden middleware
     - generated code is never edited by hand
     - `CGO_ENABLED=0`, static binaries
   - **Testing rules:**
     - write the failing test first
     - table-driven tests, with failure messages that show expected and actual values
     - never delete, skip, weaken or hard-code a test to get green; if an unrelated test fails, fix it properly or stop and report
     - `make test-list` after adding tests
     - integration tests use a real Docker Engine, not mocks of its API
     - tests are safe to run in two worktrees at once: ports from `:0`, temp directories, Docker resources labelled per run and removed afterwards
   - **Security rules:**
     - never read `.env` files or anything under a `secrets` path
     - never log secret values or tokens
     - no protocol message ever carries a shell string for the host
     - `agent.toml` is local policy the control plane cannot change
     - container specs are denied by default for: privileged mode; host bind mounts; host network, PID, IPC, UTS or user namespaces; added capabilities; devices; Docker or containerd socket mounts; disabled seccomp or AppArmor. The exception is a scoped allow in `agent.toml`.
     - docs never claim more than the threat model supports
   - **Trusted core:** `internal/protocol`, `internal/policy`, `internal/keys`, `internal/auth`, `internal/store`, `scripts/install`, `deploy/systemd`, release signing and self-update. Changes there get line-by-line review by the owner. Name the paths even though most do not exist yet.
   - **Definition of done:**
     - `make ci` green
     - evidence pasted (exact command and the tail of its output)
     - every acceptance test named in the ticket exists and passes
     - specs updated if behaviour changed
     - no placeholders
     - one commit per ticket, titled `<ID>: <title>`
   - **Gotchas:** Docker Engine 25–29 must all work; builds must fit a 2 GB server; agent plus proxy idle at or under 100 MB; browser-side encryption does not protect against a compromised control plane serving JavaScript.
   - **Pointers:** `docs/plan/build-plan.md` for the ticket loop; `docs/specs/ui.md` for UI work, and `ui/AGENTS.md` once it exists.

9. **`CLAUDE.md`**, containing exactly one line: `@AGENTS.md`

10. **`README.md`**, at most 30 lines, in the calm voice of `vision.md` §1.2. What Moonglass is, "Status: pre-alpha, not usable yet", how to build and test. Does not duplicate `AGENTS.md`.

11. **`LICENSE`**: the full Apache License 2.0 text. Copyright line: `Copyright 2026 The Moonglass Authors`.

12. **`.gitignore`**: `bin/`, coverage output, `.DS_Store`, editor directories. Nothing speculative.

13. **`.github/pull_request_template.md`**, with these sections:
    - Ticket
    - What changed
    - Evidence (commands and output)
    - Tests added or changed, with a reason for any removed
    - Security notes
    - Trusted core touched (yes or no, which paths)

14. **`.github/ISSUE_TEMPLATE/ticket.md`**, the ticket template:
    - Title: a verb and an outcome
    - Spec and ADR links
    - Packages it may touch
    - User-visible outcome
    - Out of scope
    - Acceptance tests: name and what each asserts
    - Verify commands
    - Security notes
    - Review level (Core, Standard or Light)
    - Lane (primary or secondary)
    - Done when

15. **`testdata/tests.txt`**, generated by `make test-list`.

## Do not

- Create a GitHub repository, add a remote, or push.
- Write product code, `.proto` files, UI, Docker configuration, or files for later tickets.
- Change the content of anything in `docs/plan/`.

## Acceptance

- `make ci` passes in a fresh clone: `git clone` this repository into a temp directory and run it there.
- `file bin/moonglass-linux-amd64 bin/moonglass-linux-arm64` reports statically linked executables.
- `moonglass version` prints the three fields. An unknown subcommand exits with status 2. Tests cover both.
- No `TODO`, `FIXME`, `XXX` or `HACK` markers in Go code, the `Makefile` or CI files (`godox` enforces this for Go).
- A case-insensitive search for the old product name finds exactly one file: `docs/plan/research.md`, with its historical note.
- A single commit: `W0-1: Bootstrap repository`.

## Report, then stop

Paste:
- `git ls-files`
- the tail of `make ci` from the fresh clone
- the versions you pinned and where you verified each one
- anything you were unsure about, or decided that the docs did not

Then stop and wait for review.
