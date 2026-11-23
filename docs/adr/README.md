# Architecture decision records

One file per decision. Each follows the same template: Status · Date · Context · Decision · Consequences · Alternatives rejected · Open questions (only when a spike or a named owner decision still holds one) · Links.

Sources are cited as S§ (`docs/plan/stack.md`), V§ (`vision.md`), M§ (`milestones.md`) and B§ (`build-plan.md`). Each ADR states its current decision only.

| ADR | Decision |
|---|---|
| [0001](0001-two-process-proxy.md) | `moonglass agent` and `moonglass proxy` are separate processes from one binary; the agent configures the proxy over Caddy's admin API on a Unix socket |
| [0002](0002-control-plane-storage.md) | Every control plane runs on SQLite; a Cloud control plane is one instance per account; one storage interface and one conformance suite |
| [0003](0003-secrets-envelope-keys.md) | Secrets and backups are encrypted to a per-environment key that is stored only wrapped, to agent keys and to the owner recovery key; every key operation works in the browser and in the CLI |
| [0004](0004-container-spec-policy.md) | The agent denies dangerous container specs by default; the only exceptions are scoped allows in `agent.toml` |
| [0005](0005-authentication.md) | Sign-in with GitHub, Google, OpenID Connect, passkeys and TOTP, never passwords; a bootstrap token for the first owner; members by invitation; step-up for everything that matters; email through the operator's SMTP; SAML and SCIM in Business |
| [0006](0006-operation-registry.md) | One operation registry is the only API surface; REST, the CLI, the dashboard's client and MCP are generated from it, and it does authorisation, approvals and audit |
| [0007](0007-releases-and-updates.md) | Releases carry OpenSSH signatures from two pinned FIDO2 keys, flow from canary to stable, revert themselves, upgrade from the dashboard when the operator chooses, and roll out in stages on Cloud; after launch the control plane speaks the current and previous agent protocol version only |
| [0008](0008-protobuf-codegen.md) | Official `protoc` + `protoc-gen-go` (`google.golang.org/protobuf`), versions pinned, generated Go checked in |
| [0009](0009-policy-dependencies.md) | `compose-go/v2 v2.17.0` and `go-toml/v2 v2.4.3`, imported only by `internal/policy`, for Compose resolution and strict `agent.toml` parsing |
| [0010](0010-storage-dependencies.md) | `modernc.org/sqlite v1.62.0`, imported only by `internal/store/sqlite` and `internal/agent/state` |
| [0011](0011-proxy-dependencies.md) | `caddy/v2 v2.11.9`, imported only by `internal/proxy`, for the embedded proxy; a proxy upgrade is a drained restart until SPIKE-1 decides ADR-0001 |
| [0012](0012-connection-dependencies.md) | `coder/websocket v1.8.15`, imported only by `internal/agent` and `internal/server`, for the agent's outbound WSS connection |
| [0013](0013-api-dependencies.md) | `huma/v2 v2.40.0`, imported only by `internal/api` through the standard-library adapter, for the REST API and OpenAPI generated from the registry |
| [0014](0014-docker-client.md) | `moby/moby/client v0.6.3` and `moby/moby/api v1.57.0`, imported only by `internal/deploy`, `internal/build`, `internal/agent/disk` and the `test/deploy/` suite; Engine API 1.44 (Docker 25) or newer, negotiated |
| [0015](0015-age-dependency.md) | `filippo.io/age v1.3.2`, imported only by `internal/keys`, X25519 only; test vectors checked with the stock `age` tool |
| [0016](0016-ui-toolchain.md) | Bun 1.4.4 installs and builds the UI, exact pins for Svelte, SvelteKit, Vite, Tailwind and Bits UI; `make ui` builds the embedded UI, and no build output is committed |
| [0017](0017-containerd-errdefs-dependency.md) | `github.com/containerd/errdefs v1.0.0`, imported only by `internal/deploy`, for typed HTTP 409 conflict detection on image removal |
| [0018](0018-browser-age.md) | `age-encryption 0.3.1` in the dashboard, imported only by `ui/src/lib/keys/` and loaded only on screens that handle keys |


## Changing a decision

An ADR is a contract (B§4). To change one:

1. Propose the new decision on a `needs-owner` PR and get the owner's approval.
2. Rewrite the ADR to state the new decision as its only decision, and land one ticket that changes every user of it and deletes the old form in the same change.

An open question in an ADR is closed the same way: the spike or owner decision it names picks exactly one option, the ADR is rewritten to state it, and only that option is built.
