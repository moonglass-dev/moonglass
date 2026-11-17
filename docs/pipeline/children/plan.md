# Step 1: plan

Runs as Claude Opus in plan mode: you cannot write files. Your result goes in your final message.

## Inputs

Read in full:

- `AGENTS.md`
- `docs/tickets/<ID>.md`
- Every file in the ticket's `reads`, and the current source of every path in `allowed_paths`.
- `docs/threat-model.md`
- `docs/tickets/<ID>/rejected-plan-<n>.txt`, when your task lists it. Fix every problem it names.

## Do

Decide how the ticket gets built, without changing what it asks for.

Set `verdict: needs-human` and stop when building the ticket needs any of:

- a contract change: a `.proto` file, a registry operation, the `agent.toml` schema or the storage schema;
- a new or changed ADR;
- a module that no accepted ADR in `docs/adr/` names in full;
- a security decision that `docs/threat-model.md` and the specs do not settle;
- behaviour that contradicts a spec.

The reason is one line a human can act on: what decision is needed and where it belongs.

Otherwise set `verdict: ready` and write the plan. Only what this ticket needs: no abstractions, options or hooks for later tickets.

## Output

One summary line, then:

````
FILE: docs/tickets/<ID>/plan.md
`````text
---
verdict: ready
reason: ""
---

# Plan for <ID>

## Files to change

- `path/to/file.go`: what changes, the types and function signatures.

## Tests to write

- `./package/path TestName`: what it asserts. Every test the ticket names, plus any helper tests.

## Edge cases

- Each error path, limit and hostile input, and the test that covers it.

## Risks

- What could go wrong in review or in CI, and how the plan avoids it.
`````
````

For needs-human, the front matter is `verdict: needs-human` and `reason: <one line>`, and the body says what you found.
