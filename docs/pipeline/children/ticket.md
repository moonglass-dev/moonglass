# Step 0: ticket

Runs as Claude Opus in plan mode: you cannot write files. Your result goes in your final message.

## Inputs

Read in full:

- `AGENTS.md`
- `docs/tickets/<ID>.md`: the skeleton. Its title and first paragraph are the ticket's line from the build plan.
- `docs/tickets/TEMPLATE.md`: the format, including the comment at the end.
- `docs/plan/build-plan.md`: §1 (risk labels), the ticket's line, and the lines of the tickets it depends on.
- `docs/threat-model.md`
- Every spec in `docs/specs/` and ADR in `docs/adr/` that the ticket's intent touches, and the source of the packages it will change.
- `docs/tickets/<ID>/rejected-ticket-<n>.txt`, when your task lists it: why your previous round was rejected. Fix every problem it names.

## Do

Expand the skeleton into the full ticket, following `TEMPLATE.md`:

- Front matter: exactly the keys `id`, `title`, `status`, `risk`, `depends_on`, `allowed_paths`, `max_diff_lines`, `tests`, `reads`.
  - `status: todo`.
  - `risk`: the build plan's label. Raise it to `high` if `allowed_paths` reaches the trusted core in `AGENTS.md`. Never lower it.
  - `depends_on`: exactly the build plan's list, in the same order.
  - `allowed_paths`: every file or directory the change needs and nothing else. Include the spec when behaviour changes. Include `go.mod` and `go.sum` only when an accepted ADR names the new module.
  - `max_diff_lines`: your estimate of counted lines times 1.5. At most 600, and at most 400 for `high`. If the work cannot fit, the ticket is too big: block it (below) with the reason `split: <how>`.
  - `tests`: for `standard` and `high`, three to ten acceptance tests as `./package/path TestName`. Use names that do not exist yet. For `low`, list tests only when behaviour changes.
  - `reads`: the specs, ADRs and source files the implementer must read.
- Body: `# <title>`, a short intent paragraph, then `## Scope`, `## Acceptance criteria` (one line per named test, and anything else that must hold), `## Out of scope` (neighbouring tickets by ID) and `## Security notes` (the threat-model sections touched and what must hold).

Decide nothing the specs and ADRs leave open. If the behaviour is not specified well enough to write testable acceptance criteria, block the ticket. A needed contract or ADR change is not a reason to block here: write the ticket, and the plan step stops it for a human.

## Output

One summary line, then the complete file:

````
FILE: docs/tickets/<ID>.md
`````text
---
id: <ID>
...
`````
````

To block instead, give only the summary line and:

````
FILE: docs/tickets/<ID>/BLOCKED.md
`````text
<the reason, one line>

<what is missing and which document should hold it>
`````
````
