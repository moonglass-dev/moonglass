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

`Register` also rejects an empty or duplicate name, an invalid role, and a destructive operation whose `MinRole` is below admin. Deploy and rollback are not destructive. A handler is `func(ctx, Call, In) (Out, error)`; `Call` carries the org and user ids and the target's resolved project and environment ids, and has no methods, so a handler cannot write an audit event.

`registry.Operations()` lists the shipped operations as `registry.Declaration` values, each with its `Spec` and `In`, the `reflect.Type` of its input, which generation reads ("Inputs and errors"); W2-1 ships `servers.list` (`GET /v1/servers`, viewer, not destructive).

### Operations added in weeks 2 and 3

Specified by the weeks 2–3 preflight (owner decisions D6, D18, D16). Registry operations are a contract (ADR-0006 decision 9): each ticket adds exactly the operations listed for it, regenerates the OpenAPI file and the CLI, and adds authz-matrix rows.

**Targets.** Inputs name projects, environments and services by name. `registry.Invoke` resolves the target's names in the caller's org and the scope check uses their ids ("Inputs and errors"); the handler resolves any other name, such as a service, after authorisation. An operation's target is the project and environment it acts on: empty for org-level operations, the project with an empty environment for project-level ones.

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
| W3-2b | `keys.recovery.add` | `POST /v1/keys/recovery` | owner | no | org |
| W3-2b | `keys.recovery.list` | `GET /v1/keys/recovery` | viewer | no | org |
| W3-2d | `environments.key.create` | `POST /v1/environments/key` | member | no | project, environment |
| W3-2d | `secrets.set` | `PUT /v1/secrets` | member | no | project, environment |
| W3-2d | `secrets.list` | `GET /v1/secrets` | viewer | no | project, environment |

The six W2-11 operations take these inputs, each a required string:

| Operation | Inputs |
|---|---|
| `projects.create` | `name` |
| `projects.list` | none |
| `environments.create` | `project`, `name` |
| `environments.list` | `project` |
| `services.create` | `project`, `environment`, `name`, `server` (a server id of the caller's org) |
| `services.list` | `project`, `environment` |

A create returns the created row: a project and an environment are `id`, `name` and `created_at`; a service is `id`, `name`, `server` (its server id) and `created_at`. A list returns `projects`, `environments` or `services`, the rows in the store's name order, and `[]` rather than null when there are none.

The three creates refuse a store error with fixed text built from the caller's inputs, never from the store's error text: `store.ErrInvalidName` is `ErrInvalidInput`, `<kind> name "<name>" must be 1 to 63 bytes of [a-z0-9-], starting and ending with a letter or digit`, where `<kind>` is `project`, `environment` or `service`; `store.ErrDuplicate` is `ErrConflict`, `project "<name>" already exists`, `environment "<name>" already exists in project "<project>"`, or `service "<name>" already exists in environment "<environment>" of project "<project>"`; `store.ErrNotFound` from `environments.create` is `ErrNotFound`, `project "<project>" not found`, and from `services.create` it is `ErrNotFound`, `server "<server>" not found`. Any other store error is a 500.

- From W6-4b, `services.list` returns each service's `compose_hash`: the hash stored on its current release, or empty when it has none (`docs/specs/compose.md`).
- From W3-2d, `environments.list` returns each environment's `public_key` (`age1…`), or `""` until it has one, which the CLI pins (`docs/specs/secrets.md`). From W4-2 it also returns `protected`.
- **`deploy`** takes `project`, `environment`, `service`, exactly one of `image` (a reference) or `git` (`<owner>/<repo>@<40-hex commit>`, from W3-4), and optional `port`, `health_path`, `domain` (repeated), `pre_deploy` (repeated, the argv) and `pre_deploy_reversible`. The rules of `docs/specs/protocol.md` apply to each. It mints the release id, then:
  - **One deploy per service.** At most one release per service is `running` and at most one `queued`. A new request while one is queued marks the queued one `superseded` (its caller gets that status) and queues itself. Rollbacks are not queued behind deploys.
  - The server drives the steps: `BUILD` for a git source (W3-4), then `DEPLOY`, then `RUN_PRE_DEPLOY` when `pre_deploy` is set, then `SWITCH_RELEASE`. Each waits for its `CommandResult` (`docs/specs/connection.md`). A step that is not `SUCCEEDED` ends the release as `failed` or `rejected` with the agent's `detail`.
  - The call returns when the release reaches a final status, and returns that release.
  - For `git`, `image` sent to the agent's `DEPLOY` is the `moonglass.local/<project>/<service>:<release_id>` reference the build produced (`docs/specs/build.md`).
  - A service whose server is not connected fails at once with `server <id> is offline`.
- **Service settings on `deploy`** (W6-1d, owner decision of 2026-11-19). `deploy` also takes these optional inputs, each sent in `DEPLOY` as the field `docs/specs/protocol.md` names ("Service settings") and refused with `ErrInvalidInput`, before a release is minted, when it breaks that field's rule. They are stored on the release (`docs/specs/storage.md`).
  - `command` (repeated): the exec argv, `Deploy.command`.
  - `volume` (repeated): `<name>:<target>` or `<name>:<target>:ro`, one `VolumeMount` each.
  - `label` (repeated): `<key>=<value>`, split at the first `=`, one entry of `Deploy.labels` each.
  - `restart`: empty, `no` or `on-failure`.
  - `healthcheck` (repeated): the test, as `Healthcheck.test`. With it, `healthcheck_interval_ms`, `healthcheck_timeout_ms` and `healthcheck_retries` are required, and `healthcheck_start_period_ms` and `healthcheck_start_interval_ms` are optional. Without it all five must be absent.
  - From W6-4b, `compose_hash`: empty, or 64 lowercase hex characters. It is stored on the release only and is not sent to the agent (`docs/specs/compose.md`).
- **`rollback`** takes `project`, `environment`, `service`, sends `ROLLBACK`, and returns when its result arrives. Under the G2 conditions it finishes in under 5 seconds (`docs/specs/release.md`). It is audited like every call.
- **`releases.list`** returns the service's releases, newest first, at most 50: id, status, `image_ref` or git source, `image_id`, `repo_digest`, `detail`, times, and which are current and previous. For a tag, `repo_digest` is the digest that actually ran.
- **`servers.deploy-key`** takes `server` and returns its deploy public key (`docs/specs/build.md`), or a 404 that says the server has not reported one yet.
- **`keys.recovery.add`**, **`keys.recovery.list`**, **`environments.key.create`**, **`secrets.set`** and **`secrets.list`** are specified in "Secrets operations (W3-2b, W3-2d)" below.

### Secrets operations (W3-2b, W3-2d)

Owner decision of 2026-11-20, splitting W3-2. Every input follows "Inputs and errors": `project` and `environment` are required strings on the three environment-level operations and are not repeated in the tables below. The store methods are `docs/specs/storage.md` ("Recovery recipients, environment keys and secrets (W3-2a)").

**Binary values.** A ciphertext travels as a JSON string holding the standard base64 encoding, with padding (RFC 4648 section 4), of the binary age file. Armor is not accepted. The value **is an age file** when it decodes as that encoding, and the decoded bytes start with the version line `age-encryption.org/v1` followed by `\n` and contain a line that starts with `--- `, the header's MAC line. The control plane checks nothing more: it cannot check the recipient (`docs/specs/secrets.md`). Sizes are of the decoded bytes.

| Operation | Inputs | Output |
|---|---|---|
| `keys.recovery.add` | `recipient` | `id`, `recipient`, `created_at` |
| `keys.recovery.list` | none | `recipients`: `id`, `recipient`, `created_at` each, oldest first |
| `environments.key.create` | `public_key`; `wrapped` (list of strings) | `environment_id`, `public_key`, `created_at` |
| `secrets.set` | `kind`, `name`, `ciphertext` | `kind`, `name`, `updated_at` |
| `secrets.list` | none | `secrets`: `kind`, `name`, `updated_at` each, by kind then name |

Every input is a required string except `wrapped`, a required list. A list output is `[]` rather than null when empty. No output holds a ciphertext.

- **`keys.recovery.add`** (W3-2b) parses `recipient` with `keys.ParseRecipient` and stores the canonical text. A recipient that does not parse is `ErrInvalidInput`, `recipient must be an age X25519 recipient (age1…)`. `store.ErrDuplicate` is `ErrConflict`, `this org already has a recovery key or an environment key: adding a recovery key is a recipient change and needs step-up` (ADR-0003 decision 12). Its generated subcommand is `moonglass keys recovery add --recipient age1…`.
- **`keys.recovery.list`** (W3-2b, owner decision D21) returns the org's recovery recipients. They are public keys. The CLI checks them against its pins before it wraps an environment key (`docs/specs/secrets.md`).
- **`environments.key.create`** (W3-2d) takes the environment's `public_key`, parsed like `recipient` (`public_key must be an age X25519 recipient (age1…)`), and one `wrapped` element per recovery recipient on record, each `<recovery recipient id>:<ciphertext>`: the `id` from `keys.recovery.list`, a colon, and the wrapped key record in the binary form above. An X25519 age stanza does not name its recipient, so the copy is matched only by that id. An element without a colon, with an id that is not 32 lowercase hex characters, or with a ciphertext that is not an age file of at most 1 KiB is `ErrInvalidInput`, `wrapped copy <n> must be <recovery recipient id>:<base64 age file of at most 1 KiB>`, `<n>` counting from 1. Two elements with the same id are `ErrInvalidInput`, `wrapped copy <n> repeats recovery recipient <id>`. `store.ErrDuplicate` is `ErrConflict`, `environment "<environment>" of project "<project>" already has a key`, and `store.ErrRecipientsMismatch` is `ErrConflict`, `the wrapped copies must name exactly the org's recovery recipients: list them with moonglass keys recovery list`.
- **`secrets.set`** (W3-2d) stores `ciphertext` under `kind` and `name`, replacing an existing value of that kind and name. `kind` other than `env` or `registry` is `ErrInvalidInput`, `kind must be env or registry`. The name rules are `docs/specs/secrets.md` ("Kinds of secret"): an `env` name that breaks them is `ErrInvalidInput`, `env secret name "<name>" must match [A-Z_][A-Z0-9_]*, be at most 128 bytes and not start with MOONGLASS_`; a `registry` name that breaks them is `ErrInvalidInput`, `registry secret name "<name>" must be a lowercase DNS name with an optional port`. A ciphertext that is not an age file is `ErrInvalidInput`, `ciphertext must be standard base64 of an age file`. The decoded ciphertext may be at most 33 KiB (33792 bytes) for `env` and 5 KiB (5120 bytes) for `registry`: the plaintext limit plus 1 KiB for age's header and tags. A larger one is `ErrInvalidInput`, `ciphertext for <kind> secret "<name>" is over <limit> bytes`. `store.ErrNoEnvironmentKey` is `ErrConflict`, `environment "<environment>" of project "<project>" has no key yet: create it with moonglass environments key create`. The messages never quote the ciphertext.
- **`secrets.list`** (W3-2d) returns kinds, names and times, never ciphertext. Values are never read back through the API (`docs/specs/secrets.md`).
- W3-2b and W3-2d regenerate the OpenAPI file and the CLI and add authz-matrix rows for their operations. W3-2d generates `environments key create` and `secrets set` subcommands; W3-2 moves both to `registry.LocalCommands`, so the generator then drops them ("Generated CLI").
- Tests. W3-2b: `TestRecoveryAddStoresCanonicalRecipient`, `TestRecoveryAddRefusesInvalidRecipient`, `TestRecoveryAddRefusesSecondKey` (409 with the message above), `TestRecoveryListReturnsRecipients`. W3-2d: `TestEnvironmentKeyCreateRefusals` (one row per refusal above), `TestEnvironmentKeyCreateStoresCopies`, `TestEnvironmentsListReturnsPublicKey`, `TestSecretsSetChecksKindNameAndSize` (each limit at and one over), `TestSecretsSetRefusesNonAgeCiphertext`, `TestSecretsSetReplacesValue`, `TestSecretsListHasNoCiphertext`.

### Protected flag (W4-2)

_Deferred to v1 with approvals (the plan audit, `docs/review/plan-audit.md`, 2026-11-22). When it is built, every environment starts unprotected (ADR-0006 decision 4 as amended); the first-environment rule below is withdrawn._

W4-2 stores `environments.protected` and does not gate deploys. W5-2 adds that gate. Until then a deploy to a protected environment runs and is audited. There is no plan-cap check in the open build. Resource limits are deferred to v1.

| Ticket | Name | Method, path | `MinRole` | Destructive | Target |
|---|---|---|---|---|---|
| W4-2 | `environments.set-protected` | `POST /v1/environments/protected` | member | no | project, environment |

- **`environments.set-protected`** takes `project`, `environment` and `protected` (bool).
- **`environments.create`** sets `protected` false. The column is `NOT NULL` (`docs/specs/storage.md`).
- **`deploy`** is not gated on `protected` until W5-2. Until then a deploy to a protected environment runs and is audited like every other call.

### Auto-rollback (W4-3)

Owner decision of 2026-11-20. The behaviour is `docs/specs/auto-rollback.md`, the storage `docs/specs/storage.md` ("Auto-rollback").

| Ticket | Name | Method, path | `MinRole` | Destructive | Target |
|---|---|---|---|---|---|
| W4-3 | `services.auto-rollback.set` | `POST /v1/services/auto-rollback` | member | no | project, environment |
| W4-3 | `auto-rollbacks.list` | `GET /v1/auto-rollbacks` | viewer | no | project, environment |
| W4-3 | `auto-rollbacks.mark` | `POST /v1/auto-rollbacks/mark` | member | no | project, environment |

- **`services.auto-rollback.set`** takes `project`, `environment`, `service` and any of the seven settings (`off`, `error_rate_enforce`, `restart_limit`, `health_fail_seconds`, `error_rate_percent`, `error_rate_multiple`, `error_rate_min_requests`). An input left out keeps its stored value; a value outside its range is `ErrInvalidInput`. It returns all seven. The change reaches the agent with the next `deploy`, and the answer says so.
- **`deploy`** sends the service's stored settings as `Deploy.auto_rollback`, always with all seven values. From W4-3, `services.list` returns each service's settings.
- **`auto-rollbacks.list`** takes `project`, `environment`, `service` and returns its reports, newest first, at most 50, with every column of the table except `org_id`.
- **`auto-rollbacks.mark`** takes `project`, `environment`, `service`, `id` and `verdict` (`right` or `wrong`). A report that is not that service's is not found. The caller's user id is recorded as `verdict_by`.
- Each is audited like every call.

### Sign-in and step-up (W4-1)

`sessions.list`, `sessions.revoke`, `identities.list` and `identities.link`, the `StepUp` flag, session authentication and the sign-in routes are specified in `docs/specs/auth.md`.

## Authorisation

`registry.Invoke` allows a call only when all of these pass, in this order:

1. **Role.** The caller's role in the org ranks at least `MinRole` (owner > admin > member > viewer).
2. **Scope.** One of the token's scope rows matches the operation, project and environment. `*` matches anything. An org-level call has an empty target, so only a `*` row matches it.
3. **Destructive gate.** A destructive call by an agent identity (`Principal.Agent`) returns `ErrPendingApproval` and does not run. W2-1 mints no agent identities and has no approval flow. W4-2 does not add a protected-environment deploy gate; W5-2 does. Until W5-2 a deploy to a protected environment runs and is audited. Secret-recipient gates are added by the tickets that build them.

A denied call does not run the handler. Authorisation lives only in `Invoke`; no middleware and no caller-set flag changes it.

Secret reveal, grant, rotate and every other secret-recipient change also need the step-up of ADR-0005 decision 3. They are not added until W4-1 builds it (ADR-0003 decision 11).

From W5-1b the order is agent, role, scope, destructive gate, step-up, and each operation also declares `Level` and `AgentCallable` (`docs/specs/agent-identities.md`, "Authorisation order"). The authz matrix that checks every operation against it is `internal/registry/testdata/authz-matrix.txt` (W5-1).

## Week 5 operations

Specified by the week 5 preflight (owner decisions D28 to D70, approved 2026-11-18):

- Agent identities and their scoped tokens, `agents.*` (W5-1c, W5-1): `docs/specs/agent-identities.md`.
- `services.delete`, `services.restore`, `services.purge` and the `deleted` input of `services.list` (W5-5d, W5-5e), and the same for environments and projects (W5-8): `docs/specs/delete.md`.
- `audit.list` (W5-5): `docs/specs/audit.md`.

## Tokens

An owner token is `mg_` plus 52 lowercase unpadded base32 characters, from 256 bits of `crypto/rand`. `MintToken` stores `SHA-256` of the full text in `token_hashes`, and the name, expiry, human owner and last four characters in `tokens`. The plaintext is returned once and never stored or logged. An owner token has exactly one scope row, `* * *`; `MintToken` rejects any other row, a missing name and an expiry that is not in the future. There is no registration endpoint: a token is minted in process (`Server.MintOwnerToken`), and `moonglass server --print-owner-token NAME` prints one (`docs/specs/enrolment.md`).

From W5-1, `MintToken` also mints agent identities' tokens, with 1 to 32 checked scope rows, and every token expires 1 hour to 90 days after minting (`docs/specs/agent-identities.md`, "Tokens"). A person's token keeps exactly `* * *`.

`Registry.Authenticate` hashes the bearer text, finds the token and its holder's role, and returns `ErrUnauthenticated` for a malformed, unknown, expired or revoked token.

## Audit

Every `Invoke` by an authenticated principal writes one event after the decision: `allowed`, `denied` (role, scope or pending approval) or `error` (the handler failed). A request whose token fails to authenticate has no org and writes no event. Each event names the principal kind (`human` or `agent`), user id, token id, session id, step-up id and operation. `session_id` and `step_up_id` are empty when they don't apply; `docs/specs/auth.md` gives the session and step-up that set them. `reason` never holds a token or handler error text.

`hash = SHA-256(JSON [prev_hash, org_id, seq, id, at, principal_kind, user_id, token_id, session_id, step_up_id, operation, outcome, reason])`, hex encoded. Events start at `seq` 1 with an empty `prev_hash`. `VerifyAudit` recomputes the chain and names the first bad sequence number when an event is changed, removed or reordered.

W5-1b adds `agent_id` to the event and the hash, and W5-5 adds `project_id`, `environment_id` and the `system` principal kind, `audit.list`, and `moonglass audit verify`, which checks the chain on the CLI (`docs/specs/agent-identities.md`, `docs/specs/audit.md`).

## Routes and commands that are not operations

`registry.NonOperationRoutes` is the fixed list of routes outside the registry, with the authorisation each enforces: `GET /v1/agent` (agent listener, Ed25519 challenge), `POST /v1/enroll` (single-use enrolment token), `GET /v1/logs/stream` (viewer read of that project and environment) and `GET /v1/usage/stream` (viewer read of that project and environment). The two stream routes take `project`, `environment` and `service` (`docs/specs/live.md`). SSE is not a registry operation. `registry.LocalCommands` is the fixed list of local-only subcommands (`version`, `server`, `keys init`, `agent enroll`, `agent connect`, `proxy`, `audit verify` from W5-5, `databases create` from W6-1 and `compose apply` from W6-4), each with what it touches. Tests fail when the server mounts a route, or the binary has a subcommand, that is on neither list nor in the registry.

## Generation

`make generate` rewrites `internal/api/openapi.json` (Huma) and `internal/cli/generated.go` from `registry.Operations()`. The tests `TestGeneratedOpenAPIIsCurrent` and `TestGeneratedCLIIsCurrent` run in `make test`, so `make ci` fails when either file is stale.

The generated runner sends `Authorization: Bearer $MOONGLASS_TOKEN` to `$MOONGLASS_URL` and prints a success response to stdout; errors are printed as "Generated CLI" says, with exit code 1. It accepts an `https://` `MOONGLASS_URL` with Go's standard certificate verification, and `http://` only for the host `localhost` or a loopback IP literal; anything else exits 2 with a message naming `MOONGLASS_URL` and sends nothing. `cli.Run` exits 2 for an unknown subcommand, or when the local handlers passed to it differ from `registry.LocalCommands`.

## Inputs and errors

Owner decision of 2026-11-18 (ADR-0006 decisions 3 and 9). W2-11c, W2-11f and W2-11g build it; every later operation follows it. Where another spec shows a different command shape, this section wins, and the ticket that builds that command corrects its spec.

### Input fields

An operation's `In` is a Go struct. Each field has a `json` tag in snake_case and a `doc` tag, and is one of four kinds: string, boolean, integer (`int64`, written in decimal) or list of strings. A field is optional when its `json` tag has `omitempty`, and required otherwise; the `required` tag is never used. An input of any other kind, such as a list of objects, is allowed only for an operation whose subcommand is a local command ("Generated CLI").

### Targets, authorisation and audit

- `registry.Target` carries names, `Project` and `Environment`. `internal/api` fills it from the input's `project` and `environment` fields, which every project- and environment-level operation has as required inputs. It never resolves a name itself.
- `registry.Invoke` resolves the target with one `store.ResolveTarget` call in the caller's org, as the first half of the scope check: after the role check, before the scope rows are matched. A call the role check refuses reads nothing from the store.
- A name that does not resolve stands for an id that no scope row names, so only a `*` in that column matches it. When no row matches, the call is denied exactly as for a target the token does not cover: 403, outcome `denied`, the same reason. So a scoped token cannot learn which names exist outside its scope.
- **Which name did not resolve** (owner decision of 2026-11-19, W2-11h). `ResolveTarget` returns the project id with an error matching `store.ErrNotFound` when the project exists and the environment does not, and two empty ids when the project does not exist (`docs/specs/storage.md`). So the one call tells `Invoke` both which message applies and which column is unresolved: with an empty project id both columns are unresolved; with a project id, the project column is matched by that id and only the environment column is unresolved. `Invoke` never calls `ResolveTarget` a second time. A token scoped to another project gets the same denial whether or not the environment exists, because its row's project id differs either way.
- A store failure while resolving the target is not a refusal: the handler does not run, the call is answered 500, and the event has outcome `error` and reason `the target could not be resolved`.
- When a row does match, the call is answered 404, `project "<name>" not found` or `environment "<name>" not found in project "<project>"`, and the handler does not run. The event has outcome `error` and reason `not found`.
- On success the handler gets the ids in `Call.ProjectID` and `Call.EnvironmentID` and never resolves the target's names again. Other names, such as `service` or `server`, are the handler's to resolve in those ids, after authorisation.
- A request that fails decoding or the input schema (below) is answered 400 by `internal/api` before `Invoke`, so it writes no audit event: no operation was called. `internal/api` checks the shape before it authenticates, so a malformed request is 400 even with a bad token. The schema is public in `internal/api/openapi.json`, so this reveals nothing.

### Handler refusals

A handler answers a request it cannot carry out with `registry.Refuse(kind, message)`, where kind is one of `registry.ErrInvalidInput`, `registry.ErrNotFound` or `registry.ErrConflict`. The result matches its kind with `errors.Is`. The message is written for the caller from the inputs and fixed text, such as `project "web" already exists`; it never wraps another error's text.

- A handler maps every store sentinel it can meet to a refusal: `store.ErrInvalidName` to `ErrInvalidInput`, `store.ErrNotFound` to `ErrNotFound`, `store.ErrDuplicate` to `ErrConflict`. Cross-field rules, such as exactly one of `image` or `git` on `deploy`, are `ErrInvalidInput`.
- Any other handler error, including a store sentinel the handler did not map, is a 500.
- A refusal wrapped by another error is not a refusal, so it is a 500: the wrapper's text could carry another error's text.
- The audit event of a refusal has outcome `error` and reason `invalid input`, `not found` or `conflict`. Every other handler error keeps the reason `the handler returned an error`.

### REST inputs

- `GET`: every input is a query parameter named as its `json` tag. A list is the parameter repeated, `?domain=a&domain=b` (Huma's `explode` form); a comma is part of a value. A boolean is `true` or `false`.
- `POST` and `PUT`: every input is a field of one JSON object in the body, sent with `Content-Type: application/json`, and there are no query parameters. A body over Huma's 1 MiB limit is 413.
- Unknown query parameters and unknown body fields are refused with 400: `internal/api` checks every query parameter name against the inputs, and the body schema has no additional properties.
- An optional input that is absent takes its zero value.

### Errors

| Status | When | `detail` |
|---|---|---|
| 400 | The request does not decode or does not match the input schema: a missing required input, a wrong type, an unknown parameter or field | `invalid input for <operation>`, with `errors` listing each `location` (such as `body.name`, `query.project`) and its `message` |
| 400 | A refusal of kind `ErrInvalidInput` | the refusal's message |
| 401 | A missing, unknown, expired or revoked token | `the token is missing, unknown, expired or revoked: mint a new one` |
| 403 | A denied call, or one pending approval | `your role or the token's scope does not allow this operation`, or `this destructive call is pending approval` |
| 404 | An unknown target within the token's scope, or a refusal of kind `ErrNotFound` | the message above, or the refusal's message |
| 409 | A refusal of kind `ErrConflict` | the refusal's message |
| 413 | A body over 1 MiB | Huma's message |
| 500 | Anything else, such as a store failure | `the control plane could not complete the call; see its logs and retry`, with the error logged and never sent |

- Huma's 422 is never sent: `internal/api` turns every schema failure into the 400 above.
- Bodies are Huma's `application/problem+json`. An `errors` entry never carries the input `value`, so a ciphertext or other input sent in the wrong place is not echoed back.
- No message carries a token, a secret value, a ciphertext, or the text of a wrapped error.

### Generated CLI

Each operation's subcommand is its name with dots turned into spaces, followed only by flags.

- **One flag per input field,** named `--` plus the `json` tag with `_` turned into `-`: `--health-path`, `--pre-deploy-reversible`. The one exception is `environment`, whose flag is `--env`. There are no aliases and no short flags, so `--environment` is an unknown flag.
- **Syntax** is the standard library's `flag` package: `--name value` or `--name=value`. A boolean is `--name` for true or `--name=false`. An integer is decimal, and anything else exits 2 with `--<flag> must be an integer`. A list is the flag repeated, one element each, in order: `--domain a.example --domain b.example`. A value that starts with `-` is written `--name=-v`.
- **Missing required flags** exit 2 before anything is sent, with `moonglass: <subcommand>: --<flag> is required` for the first one in field order. An optional flag that is not given is not sent.
- **No positional arguments and no stdin.** An unknown flag or any argument that is not a flag exits 2 with the subcommand's usage, which lists its flags, required ones first, each with its `doc`.
- **Request.** A `GET` sends the given flags as query parameters; `POST` and `PUT` send one JSON object. The request goes to `$MOONGLASS_URL` with the bearer token, as "Generation" says.
- **Output.** A 2xx prints the response body to stdout and exits 0. Any other status prints `moonglass: <subcommand>: <detail>` to stderr, then one line `  <location>: <message>` per `errors` entry, and exits 1.
- **Global flags** of the runner, such as W4-1c's `--step-up`, are not inputs, and no input field may take their name.

A command that needs work on the client, such as reading stdin, a file or a pin, encrypting, or calling more than one operation, is a local command: it is on `registry.LocalCommands` with what it touches (ADR-0006 decision 7). The generator writes no subcommand for an operation whose subcommand name is on that list. The local command calls the operation through the same request and output code as the generated runner, and names its flags for that operation's inputs by the rule above. `make generate` fails, naming the operation and the field, when an operation has an input of a kind with no flag and its subcommand is not a local command. So:

- **W3-2** adds `secrets set` (reads stdin, encrypts, and may first call `environments.list`, `keys.recovery.list` and `environments.key.create`) and `environments key create` (makes the key pair and the wrapped copies) to `registry.LocalCommands`. W3-2c changes the `keys init` entry's `Touches` to name `keys.recovery.add` and `pins.json`. `keys recovery add` stays generated: `moonglass keys recovery add --recipient age1…`, the retry line of `docs/specs/secrets.md`.
- **W3-4** moves `deploy` to `registry.LocalCommands` when it adds `moonglass.toml`, which the command reads. Until then `deploy` is generated (W2-5).
- **W6-1** adds `databases create` (makes a password, encrypts two secrets, calls `secrets.list`, `releases.list`, `services.create`, `secrets.set` and `deploy`; `docs/specs/managed-services.md`). It is not an operation: every step is an existing operation with its own authorisation.
- **W6-4** adds `compose apply` (reads Compose files, encrypts secrets, asks for confirmation on stdin, and calls `services.list`, `secrets.list`, `services.create`, `secrets.set`, `deploy` and `services.delete`; `docs/specs/compose.md`). It is not an operation either.

### Tests (W2-11c, W2-11f, W2-11g)

W2-11c, W2-11f and W2-11g ship no new operation, so these tests register test operations with `registry.Register` and generate from a test list.

- W2-11c `internal/registry`: `TestInvokeResolvesTargetAfterRoleCheck` (a role denial makes no store call), `TestInvokeUnknownTargetInScopeIsNotFound` (404 error, outcome `error`, reason `not found`, handler not run), `TestInvokeUnknownTargetOutOfScopeIsDenied` (the same error and reason as a known target out of scope), `TestInvokePassesResolvedIDsToHandler`, `TestRefusalKindsAndAuditReasons`, `TestUnmappedStoreErrorIsNotARefusal`.
- W2-11f `internal/api`: `TestQueryInputsDecoded` (repeated lists, booleans, integers), `TestBodyInputsDecoded`, `TestUnknownInputsRefused` (query and body, 400), `TestSchemaFailuresAre400` (missing required, wrong type; never 422; no `value` in `errors`), `TestRefusalStatuses` (400, 404, 409 with the refusal's message), `TestUnmappedHandlerErrorIs500`.
- W2-11g `internal/registry`: `TestOperationsListInTypes`; `internal/cli`: `TestGeneratedFlagNames` (`--env`, hyphens, no aliases), `TestGeneratedFlagsSentAsQueryOrBody`, `TestGeneratedRepeatedBooleanAndIntegerFlags`, `TestGeneratedMissingRequiredFlagExits2` (nothing sent), `TestGeneratedRefusesPositionalAndUnknownFlags`, `TestGeneratedPrintsErrorDetail`, `TestGenerateRefusesInputWithoutFlag`, `TestGenerateSkipsLocalCommands`.
