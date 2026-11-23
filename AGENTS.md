# Moonglass

Moonglass is an open-source deploy platform for your own servers: zero-downtime deploys, one-click restores, nothing to SSH into, in under 100 MB. A control plane coordinates; a small agent and proxy on each server do the work and keep running without it.

One static binary, `moonglass`, with the roles `server`, `agent`, `proxy` and the CLI. Config lives at `/etc/moonglass/agent.toml`, data at `/var/lib/moonglass`. Environment variables are `MOONGLASS_*`. The Go module is `moonglass.dev/moonglass`; the code is at `github.com/moonglass-dev/moonglass`. The product was renamed: never reintroduce the old working name.

## How work happens

One line starts it, in a T3 Code thread run by Grok 4.6 (cursor provider; antigravity / gemini-3.8-flash fallback) with `.agents/dispatcher-prompt.md`:

- `continue the build plan`: work through `docs/plan/build-plan.md`.
- `Request: <description>`: any feature, bug or behaviour change in plain words (`.agents/skills/request/SKILL.md`).

Per ticket: the dispatcher (Grok 4.6, Gemini fallback) picks the work and routes it. It delegates planning to Opus, which writes a brief → a worker model writes the tests and code in its own worktree on `p/<ID>`, runs `make ci` and opens the PR → the dispatcher delegates review of the diff to Opus, then squash auto-merges when `ci` is green. Status comes from the plan and GitHub (`scripts/plan`); nothing about a ticket's progress is committed. The skills in `.agents/skills/` (mirrored at `.claude/skills/`) say who does what: `dispatch`, `request`, `implement`, `review`, `drift`.

## Docs map: the sources of truth

| What | Where |
|---|---|
| Why the product exists, who it is for, what it is not | `docs/plan/vision.md` |
| Architecture and stack decisions (S§) | `docs/plan/stack.md` |
| Milestones and weekly demos (M§) | `docs/plan/milestones.md` |
| The ticket list, gates, change control, owner checklist and GitHub settings (B§) | `docs/plan/build-plan.md` |
| Market and competitor research | `docs/plan/research.md` |
| Behaviour, one spec per area | `docs/specs/` |
| Decisions, one ADR each, and the index | `docs/adr/` |
| What every change is reviewed against | `docs/threat-model.md` |
| UI routes, layout and tokens | `docs/specs/ui.md`, `docs/specs/ui-tokens.css` |

When code and a spec disagree, the spec wins until a change updates both in the same PR. A decision that changes a contract or an ADR needs the owner first.

## Repo map

| Path | What |
|---|---|
| `cmd/moonglass/` | The binary's entry point and role wiring |
| `internal/<package>/` | Product code, one package per area: `agent`, `api`, `cli`, `deploy`, `keys`, `policy`, `protocol`, `proxy`, `registry`, `release`, `server`, `store`, `update`; and `installtest` (tests for `scripts/install`) and `releasetest` (tests for `.github/workflows/release.yml` and the action pins in `ci.yml`) |
| `test/deploy/` | The deploy-correctness suite against a real Docker Engine (`docs/specs/deploy-suite.md`) |
| `ui/` | The SvelteKit UI (`docs/specs/ui.md`) |
| `proto/` | Protocol `.proto` sources; generated Go is in `internal/protocol/agentv1/` |
| `scripts/` | `install`, `measure-memory`, `fleet/`, and `plan` (workflow status and PR steps; logic in `tools/plan/`) |
| `deploy/systemd/` | Hardened units |
| `testdata/` | `tests.txt` (the list of every test) and shared fixtures |
| `.agents/` | The dispatcher prompt and the workflow skills |

## Commands

`fmt`, `fmt-check` and `lint` need `golangci-lint` at the version pinned in `.github/workflows/ci.yml`.

| Command | Does |
|---|---|
| `make ci` | The one gate: `fmt-check`, `mod-check`, `words-check`, `proto-check`, `lint`, `test`, `check-test-list`, `build-all` |
| `make test` | Every test, with Go's test cache |
| `make test-race` | Every test, uncached, shuffled, with the race detector; writes `coverage.out`. CI runs it on every push to `main` and nightly |
| `make test-one PKG=./cmd/moonglass NAME=TestName` | One test |
| `make fmt` | Apply gofumpt and goimports |
| `make test-list` | Rewrite `testdata/tests.txt`. Run it after adding, renaming or removing tests |
| `make generate` | Rewrite the generated REST and CLI from the registry |
| `make proto` | Regenerate protocol Go from `proto/` |
| `make build` / `make build-all` | Static binaries in `bin/` |
| `make memory-budget` | The idle-memory test alone |
| `scripts/plan` | Ticket status: done, open PRs, needs owner, ready, start next. `scripts/plan help` for the rest |

## Rules for every agent

- **Never ask questions.** Owner-level decisions are Opus's, recorded under "Decisions" in the PR body. Workers follow the brief and the docs map. Only three things wait for the owner: a new dependency, an unsettled security decision, and a change to a contract or an ADR.
- **Stay in scope.** Change what the brief names. Widen it only when the ticket cannot pass otherwise, and say why in the PR body.
- **Contracts** (`.proto` files, registry operations, the `agent.toml` schema, the storage schema) and ADRs change only after the owner approves the change on a `needs-owner` PR.
- **Identity.** Every commit is authored and committed as `Moonglass Team <team@moonglass.dev>`, with no `Co-authored-by` trailers. No personal names or usernames in anything committed. `gh` runs with `GH_CONFIG_DIR=~/.config/gh-moonglass-bot` and touches only `moonglass-dev/moonglass`.
- **Banned words** outside docs: TODO, FIXME, XXX, HACK, fallback, legacy (`make words-check`).
- Workers never push to `main`, merge, or turn on auto-merge. The dispatcher does, after Opus review.

## Definition of done

- Every acceptance test in the brief exists, passes and is not skipped.
- `make ci` is green, and `ci` is green on the PR.
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
- Never delete, skip, weaken or hard-code a test to get green. If an unrelated test fails, fix it properly or report it.
- Integration tests use a real Docker Engine, not mocks of its API. With no Engine they skip, except under `CI`, where they fail. Images are imported, not pulled (`docs/specs/deploy-suite.md`).
- Tests are safe to run in two worktrees at once: ports from `:0`, temp directories, Docker resources labelled per run and removed afterwards.

## Security rules

- Never read `.env` files or anything under a `secrets` path.
- Never log secret values or tokens.
- No protocol message ever carries a shell string for the host.
- `agent.toml` is local policy the control plane cannot change.
- Every container spec and build is denied by default for: `privileged`; bind mounts of host paths other than named volumes and the agent-managed data directory; any mount of `docker.sock`, `containerd.sock` or the Docker data root, under any path or alias; host network, PID, IPC, UTS or user namespaces, and `network_mode: container:<id>` across projects; `cap_add`, `devices`, `device_cgroup_rules`; `security_opt` that disables seccomp, AppArmor or SELinux; `sysctls` outside the allow-list; a custom `cgroup_parent`; a non-default `runtime`; BuildKit entitlements `security.insecure` and `network.host`. The only exception is a scoped allow in `agent.toml` (`docs/threat-model.md` §6).
- Docs never claim more than the threat model supports.

## Trusted core

These paths are `risk: high`: exact acceptance tests in the brief, Opus writes or closely checks the subtle parts and always reviews, at most one in flight, and the owner reads every line after merge.

`internal/protocol`, `internal/policy`, `internal/keys`, `internal/auth`, `internal/store`, `internal/registry`, `internal/update`, `scripts/install`, `deploy/systemd`, and release signing and self-update.

## Gotchas

- Docker Engine 25 through 29 must all work.
- Builds must fit a 2 GB server.
- Agent plus proxy idle at or under 100 MB.
- Browser-side encryption does not protect against a compromised control plane serving JavaScript.
- UI work follows `docs/specs/ui.md`, and `ui/AGENTS.md` once it exists.
