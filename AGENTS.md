# Moonglass

## What this is

Moonglass is an open-source deploy platform for your own servers: zero-downtime deploys, one-click restores, nothing to SSH into, in under 100 MB.
A control plane coordinates; a small agent and proxy on each server do the work and keep running without it.

- One static binary, `moonglass`, with the roles `server`, `agent`, `proxy` and the CLI. Each role is added by its own ticket; only `moonglass version` exists today.
- Config lives at `/etc/moonglass/agent.toml`, data at `/var/lib/moonglass`.
- Environment variables are `MOONGLASS_*`. The domain is moonglass.dev. The Go module is `moonglass.dev/moonglass`.
- The product was renamed. Never reintroduce the old working name; the one historical note is in `docs/plan/research.md`.

## Sources of truth

- `docs/plan/`: research, vision, stack, milestones and the build plan.
- `docs/adr/`: decisions.
- `docs/specs/`: behaviour, one file per area.
- `docs/threat-model.md`: what every change is reviewed against.

Stop and ask before changing a contract (`.proto` files, registry operations, the `agent.toml` schema, the storage schema) or an ADR.

## Commands

`fmt`, `fmt-check` and `lint` need `golangci-lint` on your `PATH`, at the version pinned in `.github/workflows/ci.yml`.

| Command | Does |
|---|---|
| `make build` | Static binary for this machine, in `bin/moonglass` |
| `make build-all` | Static `linux/amd64` and `linux/arm64` binaries in `bin/` |
| `make fmt` | Apply gofumpt and goimports |
| `make fmt-check` | Fail if any file is unformatted |
| `make lint` | Run golangci-lint |
| `make test` | Run every test, uncached, in shuffled order |
| `make test-one PKG=./cmd/moonglass NAME=TestName` | Run one test |
| `make race` | Run every test with the race detector |
| `make test-list` | Rewrite `testdata/tests.txt`, the list of every test |
| `make check-test-list` | Fail if `testdata/tests.txt` differs from a fresh list |
| `make ci` | `fmt-check`, `lint`, `test`, `race`, `check-test-list`, `build-all`, in that order |

## Engineering rules

- One way to do each thing.
- No fallback, legacy or compatibility code. When a design changes, delete the old path in the same change.
- Fail loudly. Wrap errors in context (`fmt.Errorf("…: %w", err)`) and say what to do.
- No new dependency without an ADR.
- Standard library first.
- Plain functions, plain SQL, obvious names.
- Authorisation is declared next to the operation, never in hidden middleware.
- Generated code is never edited by hand.
- `CGO_ENABLED=0`, static binaries.

## Testing rules

- Write the failing test first.
- Table-driven tests, with failure messages that show the expected and actual values.
- Never delete, skip, weaken or hard-code a test to get green. If an unrelated test fails, fix it properly or stop and report.
- Run `make test-list` after adding tests.
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

Changes to these paths get line-by-line review by the owner. Most do not exist yet.

- `internal/protocol`
- `internal/policy`
- `internal/keys`
- `internal/auth`
- `internal/store`
- `internal/registry`
- `scripts/install`
- `deploy/systemd`
- release signing and self-update

## Definition of done

- `make ci` is green.
- Evidence is pasted: the exact command and the tail of its output.
- Every acceptance test named in the ticket exists and passes.
- Specs are updated if behaviour changed.
- No placeholders.
- One commit per ticket, titled `<ID>: <title>`.

## Gotchas

- Docker Engine 25 through 29 must all work.
- Builds must fit a 2 GB server.
- Agent plus proxy idle at or under 100 MB.
- Browser-side encryption does not protect against a compromised control plane serving JavaScript.

## Pointers

- `docs/plan/build-plan.md`: the ticket loop.
- `docs/specs/ui.md`: UI work. Also `ui/AGENTS.md`, once it exists.
