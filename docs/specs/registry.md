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

## Authorisation

`registry.Invoke` allows a call only when all of these pass, in this order:

1. **Role.** The caller's role in the org ranks at least `MinRole` (owner > admin > member > viewer).
2. **Scope.** One of the token's scope rows matches the operation, project and environment. `*` matches anything. An org-level call has an empty target, so only a `*` row matches it.
3. **Destructive gate.** A destructive call by an agent identity (`Principal.Agent`) returns `ErrPendingApproval` and does not run. W2-1 mints no agent identities and has no approval flow; protected-environment and secret-recipient gates are added by the tickets that build them.

A denied call does not run the handler. Authorisation lives only in `Invoke`; no middleware and no caller-set flag changes it.

## Tokens

An owner token is `mg_` plus 52 lowercase unpadded base32 characters, from 256 bits of `crypto/rand`. `MintToken` stores `SHA-256` of the full text in `token_hashes`, and the name, expiry, human owner and last four characters in `tokens`. The plaintext is returned once and never stored or logged. An owner token has exactly one scope row, `* * *`; `MintToken` rejects any other row, a missing name and an expiry that is not in the future. There is no registration endpoint: the first token is minted in process (`Server.MintOwnerToken`).

`Registry.Authenticate` hashes the bearer text, finds the token and its holder's role, and returns `ErrUnauthenticated` for a malformed, unknown, expired or revoked token.

## Audit

Every `Invoke` by an authenticated principal writes one event after the decision: `allowed`, `denied` (role, scope or pending approval) or `error` (the handler failed). A request whose token fails to authenticate has no org and writes no event. Each event names the principal kind (`human` or `agent`), user id, token id and operation. `reason` never holds a token or handler error text.

`hash = SHA-256(JSON [prev_hash, org_id, seq, id, at, principal_kind, user_id, token_id, operation, outcome, reason])`, hex encoded. Events start at `seq` 1 with an empty `prev_hash`. `VerifyAudit` recomputes the chain and names the first bad sequence number when an event is changed, removed or reordered.

## Routes and commands that are not operations

`registry.NonOperationRoutes` is the fixed list of routes outside the registry, with the authorisation each enforces: `GET /v1/agent` (agent listener, Ed25519 challenge) and `POST /v1/enroll` (single-use enrolment token). `registry.LocalCommands` is the fixed list of local-only subcommands (`version`, `server`, `agent enroll`, `agent connect`, `proxy`), each with what it touches. Tests fail when the server mounts a route, or the binary has a subcommand, that is on neither list nor in the registry.

## Generation

`make generate` rewrites `internal/api/openapi.json` (Huma) and `internal/cli/generated.go` from `registry.Operations()`. The tests `TestGeneratedOpenAPIIsCurrent` and `TestGeneratedCLIIsCurrent` run in `make test`, so `make ci` fails when either file is stale.

The generated runner sends `Authorization: Bearer $MOONGLASS_TOKEN` to `$MOONGLASS_URL` and prints the response; a 4xx or 5xx goes to stderr with exit code 1. `cli.Run` exits 2 for an unknown subcommand, or when the local handlers passed to it differ from `registry.LocalCommands`.

## REST errors

401 for a missing, unknown, expired or revoked token; 403 for a denied call or a call pending approval; 500 for a store failure, with a message that points to the control-plane logs.
