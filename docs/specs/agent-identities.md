# Agent identities and scoped tokens

The registry operations, the `Spec` fields, the tables and the audit hash below are contracts (ADR-0006 decision 9, ADR-0002). The decisions are listed at the end.

What W5-1a, W5-1b, W5-1c and W5-1 build, per ADR-0005 decision 8 and ADR-0006 decision 3. It builds on `docs/specs/auth.md` (W4-1: sessions, `StepUp`, the `step_up_required` answer), which is why W5-1a depends on W4-1.

## Which ticket builds what

| Ticket | Builds |
|---|---|
| W5-1a | The `agent_identities` table, `tokens.agent_id`, `audit_events.agent_id`, their store methods, with conformance |
| W5-1b | `Spec.Level` and `Spec.AgentCallable` on every operation; `Principal.AgentID`; agent-token authentication and its role; the check order and its errors; `agent_id` in the audit hash |
| W5-1c | `agents.create`, `agents.list`, `agents.revoke` |
| W5-1 | `agents.tokens.create`, `agents.tokens.list`, `agents.tokens.revoke`; the new `MintToken` rules; the authz matrix; the build-in-CI template's agent token |

## Decisions in short

- **An agent identity belongs to one person**, who creates it. Its tokens act with that person's current role, never above admin.
- **Some operations are never for agents.** Each operation declares `AgentCallable`. Credentials, sign-in, approvals, key and protection changes, and every step-up operation are `No`.
- **Scope rows are checked when a token is minted.** A row that could never match, or that names an operation the identity may not call, is refused then, not discovered later.
- **The authz matrix is a hand-written file of expected outcomes.** It is a test file, written before the code it checks, and a test fails when an operation has no rows.

## Agent identities

An agent identity is a named principal for a coding agent, a CI job or a script. People sign in; agent identities hold tokens.

- **Name:** the name format of `docs/specs/storage.md` (1 to 63 bytes of `[a-z0-9-]`, starting and ending with a letter or digit), unique in the org. A revoked identity keeps its name, so a name is never reused and the audit trail stays unambiguous.
- **Owner:** the `user_id` of the person who created it. It never changes.
- **Revoked:** an identity can be revoked. Revoking it revokes all its tokens in the same transaction. It cannot be un-revoked.
- The open build has no cap on identities per org (no plan checks, V§6).

### Role

An agent token acts with its owner's role in the org, read at each request, capped at admin (ADR-0006 decision 3):

| Owner's role | Agent acts as |
|---|---|
| owner | admin |
| admin | admin |
| member | member |
| viewer | viewer |

- A role change takes effect on the next request, because `Authenticate` reads the membership each time (it already joins `memberships`).
- When the owner's membership is gone, the join finds nothing and every token of that person's identities is refused with 401. The identities are not deleted; an admin can still list and revoke them.
- Because of the cap, owner-only operations are never reachable by an agent identity.

## Spec fields (W5-1b)

`Spec` gains two fields. Like `Destructive` and `StepUp`, neither has a default: `Register` rejects the zero value.

| Field | Values | Meaning |
|---|---|---|
| `Level` | `registry.LevelOrg`, `registry.LevelProject`, `registry.LevelEnvironment` | The target the operation acts on: the "Target" column of the operations tables in `docs/specs/registry.md` |
| `AgentCallable` | `registry.Yes`, `registry.No` | Whether an agent identity may call it at all |

`Register` also rejects `StepUp: Yes` with `AgentCallable: Yes`. A step-up needs a person at a browser, so a step-up operation is never an agent's.

**The rule for `AgentCallable`** (one rule, like the role rule): `No` for operations that manage credentials or sign-in (sessions, identities, agent identities and their tokens), approvals (W5-2), key material and recipients, the protected flag, and every step-up operation. `Yes` for everything else.

Declarations for the operations specified so far:

| Operations | `Level` | `AgentCallable` |
|---|---|---|
| `servers.list`, `servers.deploy-key`, `projects.create`, `projects.list`, `keys.recovery.list`, `audit.list` | org | yes |
| `keys.recovery.add` | org | no |
| `environments.create`, `environments.list` | project | yes |
| `services.create`, `services.list`, `deploy`, `rollback`, `releases.list`, `secrets.set`, `secrets.list` | environment | yes |
| `environments.key.create` (the key pair is made on the caller's machine; ADR-0003), `environments.set-protected` | environment | no |
| `sessions.list`, `sessions.revoke`, `identities.list`, `identities.link` | org | no |
| `agents.create`, `agents.list`, `agents.revoke`, `agents.tokens.create`, `agents.tokens.list`, `agents.tokens.revoke` | org | no |
| `services.delete`, `services.restore`, `environments.delete`, `environments.restore` (`docs/specs/delete.md`) | environment | yes |
| `services.purge`, `environments.purge` | environment | no |
| `projects.delete`, `projects.restore` | project | yes |
| `projects.purge` | project | no |

Later operations (W4-8, W3-3, W4-9, W4-10, W5-2, W7-6f and the rest) declare both fields under the same rule in the ticket that adds them, and add their authz-matrix rows. W5-4 lists only operations with `AgentCallable: Yes` as MCP tools.

## Authentication and the principal (W5-1b)

- `Principal.Agent` (bool) is replaced by `Principal.AgentID` (string, empty for a person). There is one field for one fact; every user of `Agent` changes in the same ticket.
- `Authenticate`, for a bearer token whose `tokens.agent_id` is set, returns `AgentID`, the owner as `UserID`, the capped role, and the token's scope rows.
- Sessions are always people. A session principal never has `AgentID`.

## Authorisation order (W5-1b)

`registry.Invoke` checks, in this order, and stops at the first failure:

| # | Check | Fails with | Matrix code |
|---|---|---|---|
| 1 | **Agent.** An agent principal calling an `AgentCallable: No` operation | 403 `agent identities cannot call <op>; a person must run it` (`ErrAgentNotAllowed`) | `H` |
| 2 | **Role.** The principal's role ranks at least `MinRole` | 403 (`ErrRoleTooLow`) | `R` |
| 3 | **Scope.** A scope row matches the operation and target | 403 (`ErrOutOfScope`) | `S` |
| 4 | **Destructive gate.** A destructive call by an agent principal | 403 pending approval (`ErrPendingApproval`) | `P` |
| 5 | **Step-up** (W4-1c) | 403 `step_up_required` | `U` |
| — | All pass: the handler runs | | `A` |

`ErrAgentNotAllowed`, `ErrRoleTooLow` and `ErrOutOfScope` each also match `ErrDenied` with `errors.Is`. Check 1 replaces W4-1c's separate refusal of agents for step-up operations: that path is deleted in W5-1b, and `docs/specs/auth.md` ("Asking") is updated to point here. W5-2 adds the protected-environment gate between checks 4 and 5.

## Tokens (W5-1)

**`MintToken`** becomes a `Registry` method, because it checks rows against the registered operations. It mints two kinds of token, and replaces the rule that every token is an owner token:

| | A person's token | An agent identity's token |
|---|---|---|
| Minted by | `moonglass server --print-owner-token` only (`Server.MintOwnerToken`) | `agents.tokens.create` only |
| `agent_id` | empty | the identity |
| Scope rows | exactly one, `* * *` | 1 to 32 rows, each valid (below); `* * *` is allowed |
| Expiry | at least 1 hour and at most 90 days ahead | the same |

The hook's 90-day expiry fits the shared limit, so it does not change. No other way to mint a person's token is added: people use sessions, or the hook.

**Valid scope rows.** A row is given as `<project>/<environment>/<operation>`, with names or `*`. The handler resolves names to ids in the caller's org, and the store keeps ids (ADR-0006 decision 3). Each row must satisfy all of these, or the call is answered 400 naming the row and the rule:

1. Exactly three parts separated by `/`.
2. `project` is `*` or the name of a live project of the org.
3. `environment` is `*`, or the name of a live environment of that project. A named environment needs a named project.
4. `operation` is `*` or the name of a registered operation.
5. A named operation is `AgentCallable: Yes`.
6. A named operation's `MinRole` is at most the role the identity acts with at mint time (its owner's, capped at admin).
7. The row can match the operation's `Level`: an org-level operation needs `*/*`; a project-level operation needs `*` as the environment.
8. No two rows are the same.

A row stays valid after minting even if the owner's role later drops; the role check then refuses the call. A row whose project or environment is later purged never matches again.

**Lifetime.** Agent tokens have no default expiry: `expires_in_hours` is required, 1 to 2160 (90 days). An identity holds at most 5 tokens that are neither expired nor revoked, so rotation can overlap; a sixth is refused with 409.

## Registry operations

Each is org-level, `AgentCallable: No`, and not destructive. Revoking a credential deletes nothing and stops nothing (ADR-0006 decision 4), so revoking is not destructive.

| Ticket | Name | Method, path | `MinRole` | Destructive | StepUp |
|---|---|---|---|---|---|
| W5-1c | `agents.create` | `POST /v1/agents` | member | no | yes |
| W5-1c | `agents.list` | `GET /v1/agents` | viewer | no | no |
| W5-1c | `agents.revoke` | `POST /v1/agents/revoke` | member | no | no |
| W5-1 | `agents.tokens.create` | `POST /v1/agents/tokens` | member | no | yes |
| W5-1 | `agents.tokens.list` | `GET /v1/agents/tokens` | viewer | no | no |
| W5-1 | `agents.tokens.revoke` | `POST /v1/agents/tokens/revoke` | member | no | no |

- **`agents.create`** takes `name`. The caller becomes the owner. It returns `id`, `name`, `owner` (the user id) and `created_at`. A taken name, revoked or not, is 409.
- **`agents.list`** returns every identity in the org, by name: `id`, `name`, `owner`, `created_at`, `revoked_at` (empty when live), and `live_tokens`.
- **`agents.revoke`** takes `agent` (a name). Any member may revoke any identity in the org: revoking fails closed, and a leaked token must be easy to stop. It returns `revoked_at`. An unknown name is 404; a revoked identity is 409.
- **`agents.tokens.create`** takes `agent` (a name), `name` (a token name in the name format), `expires_in_hours` and `scopes` (repeated row strings). Only the identity's owner may mint for it: another person's identity is 404, like `sessions.revoke`. A revoked identity is 409. It returns `token` (the `mg_` text, once), `id`, `name`, `last4`, `expires_at` and `scopes`. The text is never logged, stored or put in an audit event.
- **`agents.tokens.list`** takes `agent` and returns its tokens, newest first: `id`, `name`, `last4`, `created_at`, `expires_at`, `revoked` and `scopes`. Each scope is `project`, `environment` and `operation` as names (or `*`), with `project_id` and `environment_id`; a name is empty when its resource is gone.
- **`agents.tokens.revoke`** takes `agent` and `token` (the token id). Any member may revoke any agent token in the org. An unknown or another identity's token is 404; a revoked one is 409.

The CLI is generated: `moonglass agents tokens create --agent ci --name deploy --expires-in-hours 720 --scope web/staging/deploy --scope 'web/staging/releases.list'`. With step-up it prints the step-up line of `docs/specs/auth.md` and exits 1; the same command with `--step-up <id>` then mints.

**The build-in-CI template** (`examples/ci/github-actions.yml`, `docs/specs/build.md`) changes in W5-1: its comment says to create an identity such as `ci` and mint it a token scoped to `<project>/<environment>/deploy`.

## Storage (W5-1a)

**Org-scoped table:**
- `agent_identities`: `org_id`, `id`, `name`, `owner_user_id`, `created_at`, `revoked_at` (empty when live); unique `(org_id, name)`.

**Changes:**
- `tokens` gains `agent_id`, empty for a person's token or an `agent_identities.id` of the same org. `user_id` stays the person whose membership gives the role: for an agent token, the identity's owner.
- `audit_events` gains `agent_id`, empty for a person.
- `token_hashes`, the lookup table, is unchanged. Revoking an identity sets `revoked_at` on each of its tokens' lookup rows in the same transaction, so `Authenticate` needs no second check.

Store methods: create an identity; list identities with their live token counts; find one by name; revoke one with its tokens; add a token with `agent_id`; list an identity's tokens; revoke one token. `TokenByHash` returns `AgentID`.

Conformance covers: create and list an identity; refuse a duplicate name, including a revoked one; revoke an identity and find every one of its tokens revoked by hash; revoke one token and leave its siblings live; `TokenByHash` returns the current role after the membership's role changes and `ErrNotFound` once the membership is gone (the test changes the row with SQL, since no store method edits memberships yet); a second org sees none of it.

## Audit (W5-1b)

An agent's call is recorded with `principal_kind` `agent`, `user_id` the owner, `agent_id`, and `token_id`. The hash gains `agent_id` after `user_id`:

`SHA-256(JSON [prev_hash, org_id, seq, id, at, principal_kind, user_id, agent_id, token_id, session_id, step_up_id, operation, outcome, reason])`

W5-5 adds the target ids (`docs/specs/audit.md`). There is no compatibility code (build plan §4): a development database made before W5-1a is recreated.

## The authz matrix (W5-1)

ADR-0006 calls for an exhaustive matrix, every operation × every token scope × every role, and the threat model's checklist item 5 asks for its rows. It is `internal/registry/testdata/authz-matrix.txt`, written by hand. It is a test file: it does not count toward the size cap, and it is locked with the tests before code.

**Fixture.** One org with projects `p0`, `p1` and `p2`, created in that order. Its first environment, `p0/e0`, is protected and is never a target, so W5-2's protected gate does not change these rows. `p1` has environments `e1` and `e2`; `p2` has `e3`. Calls target the operation's level: nothing for org-level, `p1` for project-level, `p1/e1` for environment-level. Each call sends the zero value of the operation's input and no step-up header. The test classifies the error (codes above); a handler error, such as a 400 for the empty input, counts as `A`, because authorisation passed.

**Principals** (8):
- `human/owner`, `human/admin`, `human/member`, `human/viewer`: a person with that role and a `* * *` token. Sessions authorise like these, so they add no rows.
- `agent/owner`, `agent/admin`, `agent/member`, `agent/viewer`: an agent identity whose owner has that role.

**Scope columns** for agent principals (9), with `OP` the operation under test and `OTHER` `servers.list` (or `projects.list` when `OP` is `servers.list`). The fixture writes these rows through the store, not `MintToken`, so rows that minting would refuse are tested too:

| # | Row |
|---|---|
| 1 | `* * *` |
| 2 | `* * OP` |
| 3 | `* * OTHER` |
| 4 | `p1 * OP` |
| 5 | `p1 e1 OP` |
| 6 | `p1 e2 OP` |
| 7 | `p2 * OP` |
| 8 | `p1 * *` |
| 9 | `p1 e1 *` |

**Format.** One line per operation and principal, in `Operations()` order then the principal order above. A person's line has one code; an agent's has nine, in column order. `#` starts a comment.

```
# operation        principal     codes
deploy             human/viewer  R
deploy             agent/owner   A A S A A S S A A
services.delete    agent/owner   P P S P P S S P P
agents.create      agent/admin   H H H H H H H H H
```

**Tests:**
- `TestAuthzMatrix` runs every line and fails on the first difference, naming the operation, principal, column, expected code and actual result.
- `TestAuthzMatrixCoversEveryOperation` fails when an operation in `Operations()` lacks any of its 8 lines, when a line names an operation that is not registered, or when a line is repeated. Adding an operation without rows turns the suite red.

The matrix is one of the permanent suites (ADR-0006, Consequences). Every later ticket that adds an operation adds its 8 lines.

**Other named tests:**
- W5-1a: `TestAgentIdentitiesConformanceSQLite`.
- W5-1b: `TestRegisterRejectsUnsetLevelAndAgentCallable`, `TestRegisterRejectsStepUpForAgents`, `TestAgentTokenRoleIsCappedAtAdmin`, `TestAgentTokenFollowsOwnerRole`, `TestAuditRecordsAgentIdentity`.
- W5-1: `TestMintTokenScopeRules` (a table of every rule above), `TestAgentTokenExpiryLimits`, `TestAgentTokenTextShownOnce`.

## Errors

| Status | When |
|---|---|
| 400 | An invalid scope row, name or expiry |
| 403 | Any check of "Authorisation order" |
| 404 | An unknown identity or token; another person's identity in `agents.tokens.create` |
| 409 | A taken identity name; a revoked identity or token; a sixth live token |

## Not in W5-1

Approvals for an agent's pending call (W5-2). MCP sign-in for agents (W5-4). Scoped tokens for people. Moving an identity to another owner.

## Decisions

Each line is a choice no ADR or earlier spec fixed.

- **D28** An agent identity is owned by the person who creates it, and its tokens act with that person's current role. Recommended: yes. The alternative, an identity with a role of its own, survives its owner leaving but needs a second role model and an ADR-0006 rewrite.
- **D29** The agent role is capped at admin, so owner-only operations are never reachable by agents. Recommended: yes.
- **D30** `Spec` gains `Level` and `AgentCallable`, neither with a default, and step-up implies `AgentCallable: No`. Recommended: yes; the declarations table above applies the rule.
- **D31** `environments.key.create`, `environments.set-protected` and `keys.recovery.add` are `AgentCallable: No`. Recommended: yes. An agent could keep the private half of a key it generated, or switch protection off ahead of its own deploy.
- **D32** Any member creates identities they own; only the owner mints an identity's tokens; any member revokes any identity or agent token. Recommended: yes.
- **D33** `agents.create` and `agents.tokens.create` need step-up; lists and revokes do not. Recommended: yes. Minting is a lasting grant; revoking must be fast.
- **D34** Scope rows are refused at mint when they could never match or name an operation the identity may not call (rules 1 to 8). Recommended: yes.
- **D35** An agent token may hold `* * *`. Recommended: yes. The agent check, the admin cap, the destructive gate, step-up and W5-2's protected gate hold for every agent token, whatever its rows.
- **D36** Every token expires 1 hour to 90 days after minting; agent tokens state it with a required `expires_in_hours`. Recommended: yes.
- **D37** At most 5 live tokens per identity; no cap on identities in the open build. Recommended: yes.
- **D38** A removed owner's agent tokens get 401; their identities stay listed and revocable. Recommended: yes.
- **D39** W5-1 adds no scoped tokens for people. Recommended: yes; people use sessions or the hook.
- **D40** Revoked identity names are never reused. Recommended: yes.
- **D41** The authz matrix is a hand-written test file: 8 principals, 9 scope columns, codes `A R S H P U`, the check order above, and a fixture that never targets a protected environment. Recommended: yes. A generated snapshot would let the build step write its own expectations.
- **D42** `audit_events` gains `agent_id` in the hash. Recommended: yes; the chain then names the agent without a join to `tokens`.
