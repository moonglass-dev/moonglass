---
id: W9-9
title: One line, imperative, what the ticket delivers
status: todo
risk: standard
depends_on: [W9-1, W9-2]
allowed_paths:
  - internal/example
  - docs/specs/example.md
max_diff_lines: 400
tests:
  - ./internal/example TestNamedBehaviourHolds
reads:
  - docs/specs/example.md
  - docs/threat-model.md
---

# One line, imperative, what the ticket delivers

The intent from the build plan, expanded into two or three sentences: the behaviour that exists after this ticket, and the spec or ADR it implements.

## Scope

- What changes, package by package. Name the packages: they become `allowed_paths`.

## Acceptance criteria

- One line per test named in `tests`: the test, and the behaviour it proves.
- Anything else that must hold, stated so a script or a reviewer can check it.

## Out of scope

The neighbouring work that belongs to other tickets, by ID.

## Security notes

The threat-model sections (`docs/threat-model.md` §n) this ticket touches and what must hold. "None" only for docs-only tickets.

<!--
Front matter, all keys required. scripts/ticket-status rejects any other key.

- id: as in docs/plan/build-plan.md. The file is docs/tickets/<id>.md.
- status: todo, planned, tests-locked, built, in-review, merged, blocked, needs-human.
  Only the pipeline scripts change it (docs/pipeline/README.md).
- risk: low, standard or high (build plan §1). standard and high must name tests.
- depends_on: exactly the build plan's list.
- allowed_paths: every path the change may touch. A directory entry covers
  everything under it. Protected paths (AGENTS.md) need an entry at or below
  their own root. The ticket's own docs and testdata/tests.txt are always allowed.
- max_diff_lines: counted lines (added + removed) outside tests, docs, Markdown,
  generated files and go.sum. Usually 200 to 600.
- tests: `./package/path TestName`, each written by the tests step and locked.
- reads: the files a child must read beyond AGENTS.md and this ticket.
-->
