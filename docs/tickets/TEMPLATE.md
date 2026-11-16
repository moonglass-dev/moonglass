---
id: W0-0
level: standard
packages:
  - internal/example
tests:
  - ./internal/example TestExampleDoesTheThing
spec: docs/specs/example.md
reads:
  - docs/adr/0000-example.md
allow: []
---

# A verb and an outcome

## Outcome

What a user or the next ticket can do once this merges.

## Out of scope

What this ticket must not do.

## Acceptance tests

- `TestExampleDoesTheThing`: what it asserts.

## Security notes

Threat-model sections this touches. Write "none" if none.

## Done when

- `make ci` is green and every test above passes.

<!--
Frontmatter fields:
  id        Ticket id, same as the file name.
  level     core | standard | light. Decides the pipeline and the reviewer model.
  packages  Paths the diff may touch (a directory covers everything under it). Anything else fails verify.
  tests     Acceptance tests, one per line as "./package/path TestName". Each must pass and not be skipped.
  spec      The spec file this ticket implements. It may be edited; no other doc may.
  reads     Extra files the agent must read first.
  allow     Paths exempt from the protected-path, go.mod and package gates. Empty by default. Name go.mod to add a dependency.
-->
