# Architecture decision records

One file per decision. Each follows the same template: Status · Date · Context · Decision · Consequences · Alternatives rejected · Open questions (only when a spike or a named owner decision still holds one) · Links.

Sources are cited as S§ (`docs/plan/stack.md`), V§ (`vision.md`), M§ (`milestones.md`) and B§ (`build-plan.md`). The owner's decisions of 2026-11-16 are written into each ADR's decision section.

| ADR | Decision |
|---|---|
| [0001](0001-two-process-proxy.md) | `moonglass agent` and `moonglass proxy` are separate processes from one binary; the agent configures the proxy over Caddy's admin API on a Unix socket |
| [0002](0002-control-plane-storage.md) | Every control plane runs on SQLite; a managed control plane is one instance per customer; one storage interface and one conformance suite (amended 2026-11-22: the shared Postgres is withdrawn) |
| [0003](0003-secrets-envelope-keys.md) | Secrets and backups are encrypted to a per-environment key that is stored only wrapped, to agent keys and to the owner recovery key |
| [0004](0004-container-spec-policy.md) | The agent denies dangerous container specs by default; the only exceptions are scoped allows in `agent.toml` |
| [0005](0005-mvp-authentication.md) | MVP sign-in is GitHub, after a bootstrap token on the box; `server login-link` is for emergencies; step-up guards recipient changes, token minting and purges; no email; Google, passkeys and TOTP arrive in v1 |
| [0006](0006-operation-registry.md) | One operation registry is the only API surface; REST, the CLI and later MCP are generated from it, and it does authorisation and audit; approvals and protected environments are v1 |
| [0007](0007-releases-and-updates.md) | Releases carry OpenSSH signatures from two pinned FIDO2 keys from W1-6, flow from canary to stable, revert themselves, and roll out in stages on a managed fleet (v1); after launch the control plane speaks the current and previous agent protocol version only |
| [0008](0008-protobuf-codegen.md) | Official `protoc` + `protoc-gen-go` (`google.golang.org/protobuf`), versions pinned, generated Go checked in |
| [0009](0009-policy-dependencies.md) | `compose-go/v2 v2.17.0` and `go-toml/v2 v2.4.3`, imported only by `internal/policy`, for Compose resolution and strict `agent.toml` parsing |
| [0010](0010-storage-dependencies.md) | `modernc.org/sqlite v1.62.0`, imported only by `internal/store/sqlite` and `internal/agent/state`; `pgx/v5` withdrawn 2026-11-22 and removed by FA1-1 |
| [0011](0011-proxy-dependencies.md) | `caddy/v2 v2.11.9`, imported only by `internal/proxy`, for the embedded proxy; a proxy upgrade is a drained restart until SPIKE-1 |
| [0012](0012-connection-dependencies.md) | `coder/websocket v1.8.15`, imported only by `internal/agent` and `internal/server`, for the agent's outbound WSS connection |
| [0013](0013-api-dependencies.md) | `huma/v2 v2.40.0`, imported only by `internal/api` through the standard-library adapter, for the REST API and OpenAPI generated from the registry |
| [0014](0014-docker-client.md) | `moby/moby/client v0.6.3` and `moby/moby/api v1.57.0`, imported only by `internal/deploy`, `internal/build`, `internal/agent/disk` and the `test/deploy/` suite; Engine API 1.44 (Docker 25) or newer, negotiated |
| [0015](0015-age-dependency.md) | `filippo.io/age v1.3.2`, imported only by `internal/keys`, X25519 only; test vectors checked with the stock `age` tool |
| [0016](0016-ui-toolchain.md) | Bun 1.4.4 installs and builds the UI, exact pins for Svelte, SvelteKit, Vite, Tailwind and Bits UI; `make ui` builds the embedded UI, and no build output is committed |
| [0017](0017-containerd-errdefs-dependency.md) | `github.com/containerd/errdefs v1.0.0`, imported only by `internal/deploy`, for typed HTTP 409 conflict detection on image removal |

Amended 2026-11-17 by the weeks 2–3 preflight: 0001 (decision 3, the admin socket group), 0003 (decisions 2 and 5, new 11 to 13), 0004 (new 10 to 12) and 0010 (the agent database).

Amended 2026-11-19 by the week 6 owner decisions: 0004 (new 15, a container joins only its project's network; new 16, Compose is resolved in the CLI and each service checked on the agent) and 0009 (the CLI reaches the Compose loader only through `internal/policy`).

Amended 2026-11-22 by the plan audit (`docs/review/plan-audit.md`; the owner approves on its PR): 0002 (SQLite only), 0003 (decisions 5, 6 and 8: the CLI grants every server; rotate, reveal and browser entry are v1), 0005 (decisions 1 to 3: GitHub everyday sign-in, Google v1), 0006 (decisions 4 and 8: approvals, protected environments and MCP are v1), 0007 (decision 3: staged rollout waits for a managed fleet) and 0010 (pgx withdrawn).


## Changing a decision

An ADR is a contract (B§8). To change one:

1. Write an amendment to the ADR and get the owner's approval.
2. Land one ticket that changes every user of the decision and deletes the old form in the same change.

An open question in an ADR is closed the same way: the spike or owner decision it names picks exactly one option, the ADR is amended, and only that option is built.
