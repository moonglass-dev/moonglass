# The audit view and `moonglass audit verify`

**Status: Approved** by the owner on 2026-11-18 (week 5 preflight). The registry operation, the table columns, the audit hash and the local command below are contracts (ADR-0006 decisions 7 and 9, ADR-0002). The owner decisions are listed at the end.

The registry writes one hash-chained event per call (`docs/specs/registry.md`, "Audit"). This spec adds a way to read the chain, a way to check it that does not take the control plane's word for it, and the page that shows it.

## Which ticket builds what

| Ticket | Builds |
|---|---|
| W5-5c | `project_id` and `environment_id` on `audit_events`, and the `system` principal kind, on both backends with conformance |
| W5-5 | The target ids in each event and in its hash; `audit.list`; `moonglass audit verify` |
| W5-7 | The `/{org}/audit` page (`docs/specs/ui.md`) |

## What an event records

From W5-5, each event also records the target the call acted on, so the trail says where as well as who and what:

- `project_id` and `environment_id`: the ids `Invoke` checked the scope against, empty when the operation's level has none (`docs/specs/agent-identities.md`, `Level`).
- `principal_kind` is `human`, `agent`, or `system` for the retention purge (`docs/specs/delete.md`). A `system` event has empty `user_id`, `agent_id`, `token_id`, `session_id` and `step_up_id`.

The hash, with the columns of W4-1e and W5-1b:

`SHA-256(JSON [prev_hash, org_id, seq, id, at, principal_kind, user_id, agent_id, token_id, session_id, step_up_id, operation, project_id, environment_id, outcome, reason])`

There is no compatibility code (build plan §4): a development database made before W5-5c is recreated.

## `audit.list` (W5-5)

| Name | Method, path | `MinRole` | Destructive | StepUp | `AgentCallable` | Target |
|---|---|---|---|---|---|---|
| `audit.list` | `GET /v1/audit` | viewer | no | no | yes | org |

It only reads, so it is viewer by the ADR-0006 role rule. Events hold no secret: `reason` never holds a token or handler error text. Because it is org-level, only an agent row with `*` as project and environment reaches it.

**Inputs**, all optional:

| Input | Meaning |
|---|---|
| `before` | Return events with `seq` below this. Absent means from the newest |
| `limit` | 1 to 200; absent means 50 |
| `operation` | One operation name, exact |
| `outcome` | `allowed`, `denied` or `error` |
| `principal_kind` | `human`, `agent` or `system` |
| `agent` | An agent identity's name |

**Output:** `events`, newest first, and `next_before`, the `seq` to pass as `before` for the next page, or 0 when there is none. Each event carries every hashed field exactly as stored (`seq`, `id`, `at`, `org_id`, `principal_kind`, `user_id`, `agent_id`, `token_id`, `session_id`, `step_up_id`, `operation`, `project_id`, `environment_id`, `outcome`, `reason`, `prev_hash`, `hash`), so a client can recompute the chain. It also carries `agent_name`, `project_name` and `environment_name` for display; they are not hashed, and are empty for anything since purged.

A call to `audit.list` is itself audited, after it has read its page, so a page never contains its own event.

## `moonglass audit verify` (W5-5)

A local command (`registry.LocalCommands`), not an operation. A check that the control plane runs on its own data proves nothing when the control plane is the thing in doubt, so the CLI recomputes the chain itself.

1. It reads `MOONGLASS_URL` and `MOONGLASS_TOKEN` as the generated commands do, and calls `audit.list` with `limit` 200 and no filters, following `next_before` down to `seq` 1.
2. It checks the events in sequence order with `registry.VerifyAudit`, the same function and hash the registry uses: `seq` runs from 1 with no gap, each `prev_hash` is the hash before it, and each `hash` covers its own fields.
3. **The pin.** The CLI keeps the newest verified `seq` and `hash` per control-plane URL and org in `$XDG_CONFIG_HOME/moonglass/pins.json`, the file that holds its key pins (`docs/specs/secrets.md`). If a pin exists, the event at that `seq` must still have that `hash`.
4. On success it moves the pin to the newest event and prints one line to stdout: `audit chain verified: <n> events, newest <seq> <first 12 hex of hash>`.

**Exit codes:**

| Code | Meaning | Output |
|---|---|---|
| 0 | The chain verifies, and so does the pin if there was one | The line above |
| 1 | The chain is broken, or the pinned event changed | On stderr, the first bad `seq` and why: changed, missing, out of order, or `the audit chain was rewritten: event <seq> had hash <pinned> when this machine last verified it, and now has <hash>`. The pin is not moved |
| 2 | It could not check: a usage error, a missing variable, the server unreachable, a 4xx or 5xx, or a malformed response | On stderr, what failed and what to do |

**What it proves, and what it does not.** It detects an event changed, removed or reordered by anyone who did not recompute every later hash, such as a direct edit of the database. With a pin, it also detects a rewrite of any history this machine has already verified. A running control plane under someone else's control can still serve a fully recomputed chain to a machine that has never verified it. The threat model says so (§2, "Audit trail").

`Registry.VerifyAudit(ctx, orgID)`, the server-side check, is deleted in W5-5: nothing calls it, and the CLI path is the one way to verify. The pure `VerifyAudit(events)` stays.

## The audit page (W5-7)

`/{org}/audit` is a page route (`docs/specs/ui.md` §3): one full-width table, no inspector.

- **Columns:** time; who (a person's user id, an agent identity's name, or "Moonglass" for `system`); operation; where (project and environment names); outcome, with the reason on denials and errors; `seq`.
- **Filters:** outcome, principal kind, agent identity and operation, which map to the `audit.list` inputs. Pages load with "Load older", using `next_before`.
- `j` and `k` move through rows; `Enter` expands one to show its ids, `prev_hash` and `hash` in monospace.
- **No verification claim.** The page shows no "verified" badge. Its header says: "To check this trail, run `moonglass audit verify`. The CLI checks it independently of this page." A page served by the control plane cannot vouch for the control plane (threat model §5, item 2).

## Named tests

- W5-5c: `TestAuditTargetColumnsConformanceSQLite`, `TestAuditTargetColumnsConformancePostgres`.
- W5-5: `TestAuditHashCoversTarget`, `TestAuditListPagesNewestFirst`, `TestAuditListFilters`, `TestAuditListOmitsOwnEvent`, `TestAuditVerifyExitCodes` (0, 1 for a changed, a removed and a reordered event, 1 for a pin mismatch, 2 for a server error), `TestAuditVerifyMovesPinOnlyOnSuccess`.

## Owner decisions

Each line is a choice no ADR or earlier spec fixed. The owner approved every line as recommended on 2026-11-18.

- **D65** Events record the call's `project_id` and `environment_id`, in the hash. Recommended: yes; otherwise the trail cannot say where an agent acted.
- **D66** `principal_kind` gains `system`, used only by the retention purge. Recommended: yes.
- **D67** `audit.list` is viewer, org-level and callable by agents, pages newest first up to 200, and returns every hashed field. Recommended: yes.
- **D68** `moonglass audit verify` is a local command over `audit.list`, with a pin in `pins.json` and exit codes 0, 1 and 2; the server-side `VerifyAudit` method is deleted. Recommended: yes.
- **D69** The audit page shows no verification status and points to the CLI. Recommended: yes.
- **D70** The UI part moves to W5-7, which depends on W2-S and W4-6; W5-5 becomes high risk because it reaches `internal/registry`. Recommended: yes.
