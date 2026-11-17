# Architecture decision records

One file per decision. Each follows the same template: Status · Date · Context · Decision · Consequences · Alternatives rejected · Open questions (only when a spike or a named owner decision still holds one) · Links.

Sources are cited as S§ (`docs/plan/stack.md`), V§ (`vision.md`), M§ (`milestones.md`) and B§ (`build-plan.md`). The owner's decisions of 2026-11-16 are written into each ADR's decision section.

| ADR | Decision |
|---|---|
| [0001](0001-two-process-proxy.md) | `moonglass agent` and `moonglass proxy` are separate processes from one binary; the agent configures the proxy over Caddy's admin API on a Unix socket |
| [0002](0002-control-plane-storage.md) | Hosted runs on one managed Postgres with row-level security on `org_id`; self-hosted runs on SQLite; one storage interface and one conformance suite cover both |
| [0003](0003-secrets-envelope-keys.md) | Secrets and backups are encrypted to a per-environment key that is stored only wrapped, to agent keys and to the owner recovery key |
| [0004](0004-container-spec-policy.md) | The agent denies dangerous container specs by default; the only exceptions are scoped allows in `agent.toml` |
| [0005](0005-mvp-authentication.md) | MVP sign-in is GitHub and email links on hosted, a bootstrap token and `server login-link` on self-hosted, with step-up for approvals; passkeys and TOTP arrive in v1 |
| [0006](0006-operation-registry.md) | One operation registry is the only API surface; REST, the CLI and later MCP are generated from it, and it does authorisation and audit |
| [0007](0007-releases-and-updates.md) | Releases carry OpenSSH signatures from two pinned FIDO2 keys from W1-6, flow from canary to stable, roll out in stages and revert themselves; after launch the control plane speaks the current and previous agent protocol version only |
| [0008](0008-protobuf-codegen.md) | Official `protoc` + `protoc-gen-go` (`google.golang.org/protobuf`), versions pinned, generated Go checked in |
| [0009](0009-policy-dependencies.md) | `compose-go/v2 v2.17.0` and `go-toml/v2 v2.4.3`, imported only by `internal/policy`, for Compose resolution and strict `agent.toml` parsing |
| [0010](0010-storage-dependencies.md) | `pgx/v5 v5.13.0` and `modernc.org/sqlite v1.62.0`, imported only by `internal/store`, for the Postgres and SQLite backends |


## Changing a decision

An ADR is a contract (B§8). To change one:

1. Write an amendment to the ADR and get the owner's approval.
2. Land one ticket that changes every user of the decision and deletes the old form in the same change.

An open question in an ADR is closed the same way: the spike or owner decision it names picks exactly one option, the ADR is amended, and only that option is built.
