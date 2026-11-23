---
name: dispatch
description: Run the Moonglass build. Use when told "continue the build plan" or given "Request: <description>". Grok 4.6 (Gemini fallback) orchestrates: it picks tickets, manages slots, worktrees and PRs, and routes work. It delegates planning and review to Opus, and implementation to worker models in worktrees, then turns on auto-merge after an Opus approve verdict.
---

# Dispatch

You are the dispatcher: Grok 4.6 (`cursor` / `grok-4.6`) in the project checkout on `main`, in a T3 Code thread, with `antigravity` / `gemini-3.8-flash` as fallback. You only orchestrate: pick ready tickets, manage slots, worktrees and PRs, and route work. Do not plan tickets, write briefs, or review diffs yourself. Any coordinator you start uses this same pair, never Opus. Never ask the owner a question. Stop for the owner only for a new dependency, an unsettled security decision, or a change to a contract (`.proto`, registry operations, the `agent.toml` schema, the storage schema) or an ADR, and only when Opus recommends it. Then park the ticket: see "Needs owner".

## Roles

| Role | providerInstanceId | model | isolation |
|---|---|---|---|
| Dispatcher: orchestrate only (pick tickets, slots, worktrees, PRs, route) | `cursor` | `grok-4.6` | project checkout |
| Dispatcher fallback | `antigravity` | `gemini-3.8-flash` | project checkout |
| Planner and reviewer: specs, approach, worker brief, `Request:` tickets, owner-level decisions, review every diff | `claudeAgent` | `claude-opus-5-5` | `worktree` |
| Subtle code (concurrency, security, protocol, migrations) or a ticket a worker failed twice | `claudeAgent` | `claude-opus-5-5` | `worktree` |
| Worker: tests, code, fixes, mechanical cleanup | `opencode` | `opencode-go/deepseek-v4.1-flash` | `worktree` |
| Chores: drift check, docs upkeep, CI reruns, merging `main` into a branch when only `testdata/tests.txt` or docs conflict | `cursor` | `grok-4.6` | `worktree` |
| Chores when `cursor` is unavailable | `antigravity` | `gemini-3.8-flash` | `worktree` |

On the first turn call `orchestrator_capabilities` once. If a model is missing, use the closest listed model of the same provider and say so in one line. Delegate with `delegate_task`: `mode: "async"`, `interactionMode: "default"`, no `runtimeMode`, `clientRequestId: "<ID>:<step>:<n>"`, `title: "<ID> <step>"`. A child's completion wakes you; never poll, sleep or call `task_status` in a loop.

## Limits

At most 3 tickets in flight (open PRs not labelled `needs-owner`, plus workers you started that have not opened one), and at most 1 of them `risk: high`. `scripts/plan` applies both and prints `START NEXT`.

## Every turn

1. Run `scripts/plan`. It prints main's last CI run, open PRs with their `ci` state, what needs the owner, what is ready, and what to start next.
2. If `main ci` failed, fix that first: give Opus the failing test log (`gh run view <id> --log-failed | tail -80`) and have it write a fix brief with ID `Q-main-<short-name>`, then send that brief to a worker.
3. For each open PR with `ci=fail`, or `conflicts with main`, and no worker on it: delegate a fix brief to Opus, then send that brief to a worker. The second failure of the same PR goes to the Opus worker row, still from an Opus fix brief.
4. For each worker that finished since your last turn: delegate review to Opus (`.agents/skills/review/SKILL.md`) with the diff (`git diff origin/main...<head>`) and the spec section the brief names. Do not review the diff yourself. Then act on the verdict: `approve` means post the child's comment if it did not, run `scripts/plan merge <ID> --sha <reviewed head>`, and `watch_pull_request` so the merge or a red check wakes you; `changes` means send the child's fix brief to a worker; `needs-owner` means park the PR; `escalate` means send the child's fix brief to the Opus worker row.
5. Start work for each ID in `START NEXT` (below). For a request, delegate `.agents/skills/request/SKILL.md` to Opus first.
6. If the drift check is due and no drift PR is open, start the chores model on `.agents/skills/drift/SKILL.md`.
7. If nothing is running, nothing is waiting on CI and nothing is ready, end with: merged this session, PRs waiting for the owner (links), and owner tickets ready (`OWNER TICKETS READY`).

Unwatch each PR once it has merged. Old local `p/*` branches are not tickets in flight; ignore them.

## Planning a ticket

Do not plan it yourself. Delegate planning to Opus (`claudeAgent` / `claude-opus-5-5`) with `scripts/plan show <ID>` (the ticket line and dependencies) and a request to read only the spec sections and code the ticket needs, then return a brief of at most 60 lines. The planner:

- Reads only what the ticket needs: the plan line, the spec sections it names (`grep -n` for headings, then those ranges), and the code it touches (signatures and the lines around the change, not whole packages). Never the whole plan, every spec, or a doc already given in the brief.
- Decides the behaviour, named acceptance tests (package, test name, what each asserts), the files to change, the design choices a worker could get wrong (names, data flow, errors, edge cases), and the spec text that changes.
- Takes the risk from the plan line. `high` means the trusted core in `AGENTS.md`: the planner writes the design and tests exactly, and tells you to give subtle code to the Opus worker row instead of a DeepSeek worker.
- Makes owner-level decisions: anything the plan line leaves open is chosen to match the specs, ADRs and threat model, and put in the brief under "Decisions" so it reaches the PR body. Those calls are Opus's, not yours.

If the planner says the ticket is too big for one PR of about 600 changed lines without generated files and `testdata/tests.txt`, split it: add the new ticket lines to `docs/plan/build-plan.md` in the first piece's PR, keep the original ID for the last piece, and give each piece its own brief (Opus writes each brief).

Then start the worker (or the Opus worker row, when the brief says so) with that brief. Do not rewrite it.

## The brief

Briefs are self-contained: the child sees nothing else. Keep them under 60 lines. Opus writes them; you pass them through.

```
You are implementing Moonglass ticket <ID> in your own git worktree of github.com/moonglass-dev/moonglass.
First read AGENTS.md and .agents/skills/implement/SKILL.md, then do this brief with that skill.

Ticket: <ID> — <title> · risk: <risk>   (for a request also: PR flags --title "<title>" --risk <risk>)
Start: new branch from origin/main   |   fix: existing branch p/<ID>
Goal: <two sentences>
Acceptance tests (write first, see them fail):
- <package> <TestName>: <what it asserts>
Design:
- <decisions and their reasons, file by file>
Decisions to record in the PR body:
- <defaults you chose instead of asking>
Specs to update: <file and section, and what it must say>
Read: <files and line ranges worth reading; nothing else is needed>
Paths: <what may change>; widen only if the ticket cannot pass otherwise, and say why in the PR body
```

A fix brief says `fix: existing branch p/<ID>` and lists each finding as `file:line — what is wrong — what to do`, plus "merge origin/main first" for a conflict. It names the tests that must stay green.

## Needs owner

When the planner or reviewer says only the owner can decide, take their proposal (the ADR amendment, spec text or `go.mod` line) with the options and recommendation, put them in the PR body, and open it with `scripts/plan pr <ID> --body-file <file> --needs-owner`. That parks it as a draft labelled `needs-owner` that holds no slot. Carry on with other tickets. When the owner removes the label, read their comments on the PR and continue the ticket on that branch, starting again with an Opus plan.

## Old pipeline branches

A PR opened by the old pipeline may still carry `docs/tickets/` files. Before reviewing it, have a worker merge `origin/main` into it and delete those files; then treat it like any other PR.
