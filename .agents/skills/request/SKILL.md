---
name: request
description: Turn a plain-words request ("Request: <description>") into a Moonglass ticket and an implementation brief. The dispatcher delegates this to Opus (claudeAgent / claude-opus-5-5). Use for any feature, bug or behaviour change that is not already a build-plan ticket.
---

# Intake a request

The owner writes `Request: <description>` to the dispatcher. The dispatcher delegates this skill to Opus (`claudeAgent` / `claude-opus-5-5`). Turn it into a ticket, check it against the sources of truth, and write the worker brief. Return those to the dispatcher. Do not start a worker, review a PR, or turn on auto-merge. Do not ask questions.

## 1. Write the ticket

- **ID:** `Q-<two or three words>`, lowercase, with dashes, for example `Q-cli-timeout`. The branch is `p/<ID>`, and the PR title `<ID>: <title>`.
- **Title:** a verb and an outcome, under 60 characters.
- **Goal:** two sentences on the user-visible behaviour.
- **Acceptance tests:** package, test name and what each asserts. A docs-only change needs none.
- **Risk:** `high` if it touches the trusted core in `AGENTS.md`, `standard` for product code elsewhere, and `low` for docs, scripts and UI screens.

## 2. Check it against the sources

Read only the parts that apply, through the docs map in `AGENTS.md`: `docs/plan/vision.md` for scope, the specs that cover the behaviour, the ADRs that constrain it, and `docs/threat-model.md` for anything security-relevant. Then:

- **Fits:** go on.
- **Changes a decision written in a spec, the plan or the vision:** go on, and change that text in the same PR. Say what changed and why under `## Decisions`.
- **Needs the owner** (a new dependency, a security decision, or a change to a contract or an ADR): write the proposed change with the options and your recommendation, and park it (`dispatch`, "Needs owner").
- **Already covered** by an open build-plan ticket: run that ticket instead, and widen its brief if needed.
- **Too big for one PR:** the first PR adds `FQ-<n>` ticket lines to `docs/plan/build-plan.md` §6 (low risk, reviewed and auto-merged like any PR). Then those tickets run through the normal loop.

## 3. Write the brief

Write the brief (`dispatch`, "The brief"), and add `--title "<title>" --risk <risk>` to it so the worker can open the PR: `scripts/plan` finds plan tickets by ID, but a request is not in the plan. Return the ID, title, risk, brief, and any `needs-owner` recommendation to the dispatcher. The dispatcher starts the worker with that brief and later delegates review. A request takes a slot like any ticket, so it waits when three tickets are in flight.
