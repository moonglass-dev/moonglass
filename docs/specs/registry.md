# Operation registry

ADR-0006 makes `internal/registry` the only API surface. This spec is what W2-1 builds. The registry does not import Huma (ADR-0013); `internal/api` does.

## Operations

An operation is a `Spec` and a handler, registered with `registry.Register`.

| Field | Meaning |
|---|---|
| `Name` | Unique, dotted, such as `servers.list`. The CLI subcommand is the name with dots turned into spaces. |
| `Summary` | One line, used by the OpenAPI file and the CLI usage. |
| `Method`, `Path` | The REST route. |
| `MinRole` | Lowest role that may call it: `viewer`, `member`, `admin` or `owner`. |
| `Destructive` | `registry.Yes` or `registry.No`. The zero value is `Unset` and `Register` rejects it, so an operation that omits the flag does not register. |

`Register` also rejects an empty or duplicate name, an invalid role, and a destructive operation whose `MinRole` is below admin. Deploy and rollback are not destructive. A handler is `func(ctx, Call, In) (Out, error)`; `Call` carries the org and user ids and has no methods, so a handler cannot write an audit event.

`registry.Operations()` lists the shipped specs; W2-1 ships `servers.list` (`GET /v1/servers`, viewer, not destructive).

### Operations added in weeks 2 and 3

Specified by the weeks 2–3 preflight (owner decisions D6, D18, D16). Registry operations are a contract (ADR-0006 decision 9): each ticket adds exactly the operations listed for it, regenerates the OpenAPI file and the CLI, and adds authz-matrix rows.

**Targets.** Inputs name projects, environments and services by name. The handler resolves them in the caller's org; the scope check uses their ids. An operation's target is the project and environment it acts on: empty for org-level operations, the project with an empty environment for project-level ones.

| Ticket | Name | Method, path | `MinRole` | Destructive | Target |
|---|---|---|---|---|---|
| W2-11 | `projects.create` | `POST /v1/projects` | member | no | org |
| W2-11 | `projects.list` | `GET /v1/projects` | viewer | no | org |
| W2-11 | `environments.create` | `POST /v1/environments` | member | no | project |
| W2-11 | `environments.list` | `GET /v1/environments` | viewer | no | project |
| W2-11 | `services.create` | `POST /v1/services` | member | no | project, environment |
| W2-11 | `services.list` | `GET /v1/services` | viewer | no | project, environment |
| W2-5 | `deploy` | `POST /v1/deploys` | member | no | project, environment |
| W2-5 | `rollback` | `POST /v1/rollbacks` | member | no | project, environment |
| W2-5 | `releases.list` | `GET /v1/releases` | viewer | no | project, environment |
| W3-8 | `servers.deploy-key` | `GET /v1/servers/deploy-key` | viewer | no | org |
| W3-2 | `keys.recovery.add` | `POST /v1/keys/recovery` | owner | no | org |
| W3-2 | `environments.key.create` | `POST /v1/environments/key` | member | no | project, environment |
| W3-2 | `secrets.set` | `PUT /v1/secrets` | member | no | project, environment |
| W3-2 | `secrets.list` | `GET /v1/secrets` | viewer | no | project, environment |

- `services.create` takes `project`, `environment`, `name` and `server` (a server id of the caller's org).
- `environments.list` returns each environment's `public_key` once it has one, which the CLI pins (`docs/specs/secrets.md`). From W4-2 it also returns `protected`.
- **`deploy`** takes `project`, `environment`, `service`, exactly one of `image` (a reference) or `git` (`<owner>/<repo>@<40-hex commit>`, from W3-4), and optional `port`, `health_path`, `domain` (repeated), `pre_deploy` (repeated, the argv) and `pre_deploy_reversible`. The rules of `docs/specs/protocol.md` apply to each. It mints the release id, then:
  - **One deploy per service.** At most one release per service is `running` and at most one `queued`. A new request while one is queued marks the queued one `superseded` (its caller gets that status) and queues itself. Rollbacks are not queued behind deploys.
  - The server drives the steps: `BUILD` for a git source (W3-4), then `DEPLOY`, then `RUN_PRE_DEPLOY` when `pre_deploy` is set, then `SWITCH_RELEASE`. Each waits for its `CommandResult` (`docs/specs/connection.md`). A step that is not `SUCCEEDED` ends the release as `failed` or `rejected` with the agent's `detail`.
  - The call returns when the release reaches a final status, and returns that release.
  - For `git`, `image` sent to the agent's `DEPLOY` is the `moonglass.local/<project>/<service>:<release_id>` reference the build produced (`docs/specs/build.md`).
  - A service whose server is not connected fails at once with `server <id> is offline`.
- **`rollback`** takes `project`, `environment`, `service`, sends `ROLLBACK`, and returns when its result arrives. Under the G2 conditions it finishes in under 5 seconds (`docs/specs/release.md`). It is audited like every call.
- **`releases.list`** returns the service's releases, newest first, at most 50: id, status, `image_ref` or git source, `image_id`, `repo_digest`, `detail`, times, and which are current and previous. For a tag, `repo_digest` is the digest that actually ran.
- **`servers.deploy-key`** takes `server` and returns its deploy public key (`docs/specs/build.md`), or a 404 that says the server has not reported one yet.
- **`keys.recovery.add`** takes an age recipient. It succeeds only while the org has no recovery recipient and no environment key; otherwise it is refused with a message that adding another recovery key is a recipient change that needs step-up (ADR-0003 decision 12).
- **`environments.key.create`** takes the environment, its `public_key` and one wrapped copy per recovery recipient on record. It is refused when the environment already has a key, or when the wrapped copies do not name exactly the recovery recipients on record.
- **`secrets.set`** takes `kind`, `name` and `ciphertext`, and replaces an existing value of that name. **`secrets.list`** returns kinds, names and times, never ciphertext. Values are never read back through the API (`docs/specs/secrets.md`).

### Protected flag (W4-2)

W4-2 stores `environments.protected` and does not gate deploys. W5-2 adds that gate. Until then a deploy to a protected environment runs and is audited. There is no plan-cap check in the open build. Resource limits are deferred to v1.

| Ticket | Name | Method, path | `MinRole` | Destructive | Target |
|---|---|---|---|---|---|
| W4-2 | `environments.set-protected` | `POST /v1/environments/protected` | member | no | project, environment |

- **`environments.set-protected`** takes `project`, `environment` and `protected` (bool).
- **`environments.create`** sets `protected` true for the org's first environment and false for later ones. The column is `NOT NULL` (`docs/specs/storage.md`).
- **`deploy`** is not gated on `protected` until W5-2. Until then a deploy to a protected environment runs and is audited like every other call.

## Authorisation

`registry.Invoke` allows a call only when all of these pass, in this order:

1. **Role.** The caller's role in the org ranks at least `MinRole` (owner > admin > member > viewer).
2. **Scope.** One of the token's scope rows matches the operation, project and environment. `*` matches anything. An org-level call has an empty target, so only a `*` row matches it.
3. **Destructive gate.** A destructive call by an agent identity (`Principal.Agent`) returns `ErrPendingApproval` and does not run. W2-1 mints no agent identities and has no approval flow. W4-2 does not add a protected-environment deploy gate; W5-2 does. Until W5-2 a deploy to a protected environment runs and is audited. Secret-recipient gates are added by the tickets that build them.

A denied call does not run the handler. Authorisation lives only in `Invoke`; no middleware and no caller-set flag changes it.

Secret reveal, grant, rotate and every other secret-recipient change also need the step-up of ADR-0005 decision 3. They are not added until W4-1 builds it (ADR-0003 decision 11).

## Tokens

An owner token is `mg_` plus 52 lowercase unpadded base32 characters, from 256 bits of `crypto/rand`. `MintToken` stores `SHA-256` of the full text in `token_hashes`, and the name, expiry, human owner and last four characters in `tokens`. The plaintext is returned once and never stored or logged. An owner token has exactly one scope row, `* * *`; `MintToken` rejects any other row, a missing name and an expiry that is not in the future. There is no registration endpoint: the first token is minted in process (`Server.MintOwnerToken`).

`Registry.Authenticate` hashes the bearer text, finds the token and its holder's role, and returns `ErrUnauthenticated` for a malformed, unknown, expired or revoked token.

## Audit

Every `Invoke` by an authenticated principal writes one event after the decision: `allowed`, `denied` (role, scope or pending approval) or `error` (the handler failed). A request whose token fails to authenticate has no org and writes no event. Each event names the principal kind (`human` or `agent`), user id, token id and operation. `reason` never holds a token or handler error text.

`hash = SHA-256(JSON [prev_hash, org_id, seq, id, at, principal_kind, user_id, token_id, operation, outcome, reason])`, hex encoded. Events start at `seq` 1 with an empty `prev_hash`. `VerifyAudit` recomputes the chain and names the first bad sequence number when an event is changed, removed or reordered.

## Routes and commands that are not operations

`registry.NonOperationRoutes` is the fixed list of routes outside the registry, with the authorisation each enforces: `GET /v1/agent` (agent listener, Ed25519 challenge), `POST /v1/enroll` (single-use enrolment token), `GET /v1/logs/stream` (viewer read of that project and environment) and `GET /v1/usage/stream` (viewer read of that project and environment). The two stream routes take `project`, `environment` and `service` (`docs/specs/live.md`). SSE is not a registry operation. `registry.LocalCommands` is the fixed list of local-only subcommands (`version`, `server`, `agent enroll`, `agent connect`, `proxy`), each with what it touches. Tests fail when the server mounts a route, or the binary has a subcommand, that is on neither list nor in the registry.

## Generation

`make generate` rewrites `internal/api/openapi.json` (Huma) and `internal/cli/generated.go` from `registry.Operations()`. The tests `TestGeneratedOpenAPIIsCurrent` and `TestGeneratedCLIIsCurrent` run in `make test`, so `make ci` fails when either file is stale.

The generated runner sends `Authorization: Bearer $MOONGLASS_TOKEN` to `$MOONGLASS_URL` and prints the response; a 4xx or 5xx goes to stderr with exit code 1. `cli.Run` exits 2 for an unknown subcommand, or when the local handlers passed to it differ from `registry.LocalCommands`.

## REST errors

401 for a missing, unknown, expired or revoked token; 403 for a denied call or a call pending approval; 500 for a store failure, with a message that points to the control-plane logs.
