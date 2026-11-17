# Moonglass

Moonglass is an open-source deploy platform for your own servers: zero-downtime deploys, one-click restores, nothing to SSH into, in under 100 MB. A control plane coordinates; a small agent and proxy on each server do the work and keep running without it.

One static binary, `moonglass`, with the roles `server`, `agent`, `proxy` and the CLI. Config lives at `/etc/moonglass/agent.toml`, data at `/var/lib/moonglass`. Environment variables are `MOONGLASS_*`. The Go module is `moonglass.dev/moonglass`; the code is in the private repository `github.com/moonglass-dev/moonglass`. The product was renamed: never reintroduce the old working name.

## Repo map

| Path | What |
|---|---|
| `cmd/moonglass/` | The binary's entry point and role wiring |
| `internal/<package>/` | Product code, one package per area: `agent`, `api`, `cli`, `keys`, `policy`, `protocol`, `proxy`, `registry`, `release`, `server`, `store`, and `installtest` (tests for `scripts/install`) |
| `proto/` | Protocol `.proto` sources; generated Go is in `internal/protocol/agentv1/` |
| `scripts/` | `install`, `measure-memory`, `fleet/`, and the pipeline wrappers `ticket-status`, `verify`, `ship` |
| `tools/pipeline/` | The pipeline's deterministic logic, behind those wrappers |
| `deploy/systemd/` | Hardened units |
| `testdata/` | `tests.txt` (the list of every test) and shared fixtures |
| `docs/plan/` | Research, vision, stack, milestones and `build-plan.md`, the ticket list |
| `docs/specs/`, `docs/adr/`, `docs/threat-model.md` | Behaviour, decisions, and what every change is reviewed against |
| `docs/tickets/` | One file per ticket (front matter holds its status) and `<ID>/` with its result files |
| `docs/pipeline/` | How the ticket pipeline runs, and every agent prompt |

## Commands

`fmt`, `fmt-check` and `lint` need `golangci-lint` at the version pinned in `.github/workflows/ci.yml`.

| Command | Does |
|---|---|
| `make ci` | The one gate: `fmt-check`, `lint`, `proto-check`, `test`, `check-test-list`, `build-all` |
| `make test` | Every test, uncached, shuffled, with the race detector; writes `coverage.out` |
| `make test-one PKG=./cmd/moonglass NAME=TestName` | One test |
| `make fmt` | Apply gofumpt and goimports |
| `make test-list` | Rewrite `testdata/tests.txt`. Run it after adding tests |
| `make generate` | Rewrite the generated REST and CLI from the registry |
| `make proto` | Regenerate protocol Go from `proto/` |
| `make build` / `make build-all` | Static binaries in `bin/` |
| `make memory-budget` | The idle-memory test alone |

## Rules for every agent

- **Never ask questions.** Decide from the files you were given. If you cannot finish, write `docs/tickets/<ID>/BLOCKED.md` with the reason on its first line, and stop.
- **Allowed paths.** Change only the paths in your ticket's `allowed_paths`, plus the ticket's own docs and `testdata/tests.txt`. A directory entry covers everything under it.
- **Protected paths** change only through a ticket whose `allowed_paths` names them at or below their own root: `.github/`, `.claude/`, `docs/adr/`, `docs/pipeline/`, `docs/plan/`, `tools/pipeline/`, `scripts/`, `proto/`, `*.proto`, `*.pb.go`, `Makefile`, `.golangci.yml`, `.gitignore`, `AGENTS.md`, `CLAUDE.md`, `docs/threat-model.md`, `go.mod`, `go.sum`.
- **Banned words** in added lines outside docs: TODO, FIXME, XXX, HACK, fallback, legacy.
- **Contracts** (`.proto` files, registry operations, the `agent.toml` schema, the storage schema) and ADRs change only after the owner approves. The plan step stops such a ticket as needs-human.
- Only the pipeline scripts change a ticket's `status`, commit result files, push, open PRs or merge.

## Definition of done

- Every test named in the ticket exists, passes and is not skipped.
- `make ci` is green, and `scripts/verify check <ID>` passes every gate.
- The spec is updated when behaviour changed.
- No placeholders, no code for later tickets.

## Engineering rules

- One way to do each thing.
- No fallback, legacy or compatibility code. When a design changes, delete the old path in the same change.
- Fail loudly. Wrap errors in context (`fmt.Errorf("…: %w", err)`) and say what to do.
- No new dependency without an accepted ADR that names the module.
- Standard library first. Plain functions, plain SQL, obvious names.
- Authorisation is declared next to the operation, never in hidden middleware.
- Generated code is never edited by hand.
- `CGO_ENABLED=0`, static binaries.

## Testing rules

- Write the failing test first.
- Table-driven tests, with failure messages that show the expected and actual values.
- Never delete, skip, weaken or hard-code a test to get green. If an unrelated test fails, fix it properly or block.
- Integration tests use a real Docker Engine, not mocks of its API.
- Tests are safe to run in two worktrees at once: ports from `:0`, temp directories, Docker resources labelled per run and removed afterwards.

## Security rules

- Never read `.env` files or anything under a `secrets` path.
- Never log secret values or tokens.
- No protocol message ever carries a shell string for the host.
- `agent.toml` is local policy the control plane cannot change.
- Every container spec and build is denied by default for: `privileged`; bind mounts of host paths other than named volumes and the agent-managed data directory; any mount of `docker.sock`, `containerd.sock` or the Docker data root, under any path or alias; host network, PID, IPC, UTS or user namespaces, and `network_mode: container:<id>` across projects; `cap_add`, `devices`, `device_cgroup_rules`; `security_opt` that disables seccomp, AppArmor or SELinux; `sysctls` outside the allow-list; a custom `cgroup_parent`; a non-default `runtime`; BuildKit entitlements `security.insecure` and `network.host`. The only exception is a scoped allow in `agent.toml` (`docs/threat-model.md` §6).
- Docs never claim more than the threat model supports.

## Trusted core

These paths are `risk: high`: named tests locked before code, Opus review always, and the owner reads every line.

`internal/protocol`, `internal/policy`, `internal/keys`, `internal/auth`, `internal/store`, `internal/registry`, `internal/update`, `scripts/install`, `deploy/systemd`, and release signing and self-update.

## Gotchas

- Docker Engine 25 through 29 must all work.
- Builds must fit a 2 GB server.
- Agent plus proxy idle at or under 100 MB.
- Browser-side encryption does not protect against a compromised control plane serving JavaScript.
- UI work follows `docs/specs/ui.md`, and `ui/AGENTS.md` once it exists.
