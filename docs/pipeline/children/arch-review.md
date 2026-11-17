# Review tickets: whole-repository drift review

Runs as a fresh Claude Opus in plan mode for each `kind: review` ticket (the drift reviews R-n, and W8-R). You cannot write files. Your result goes in your final message. You have no memory of earlier reviews except their reports.

## Inputs

Read in full:

- `AGENTS.md`
- `docs/tickets/<ID>.md`: its intent line says what this review focuses on. W8-R is an adversarial review of the trusted core against the threat model.
- `docs/plan/` (all of it), `docs/adr/`, `docs/specs/`, `docs/threat-model.md`
- Earlier reports in `docs/reviews/`, so you do not file a finding twice.
- The code: every package under `cmd/`, `internal/`, `scripts/`, `deploy/` and `proto/`.
- `docs/tickets/<ID>/check.json`, when your task lists it: your last round's report failed a gate. Fix it.

## Do

Compare the code on this branch (it starts from `main`) with the documents. Facts only, each with `file:line` evidence:

- code and documents that disagree;
- code with no ticket behind it;
- fallback, legacy or compatibility paths, and dead code;
- dependencies not named in an accepted ADR;
- threat-model properties the code does not enforce, or enforces without a test;
- package boundaries that are crossed, and logic duplicated across packages.

Do not fix anything, and do not file style findings.

## Output

One summary line, then the report:

````
FILE: docs/reviews/<ID>.md
`````text
# <ID>: <title>

<date, the commit reviewed (git rev-parse HEAD), and the scope>

## Findings

### 1. <one line>

- Evidence: `path/to/file.go:123`, and the document and section it disagrees with.
- Impact: what goes wrong.
- Fix ticket: FR1-1 (or "none: documentation only, fixed by …")
`````
````

Then one fix ticket line per finding that needs a code or document change, appended to the build plan:

````
APPEND: docs/plan/build-plan.md
`````text
- **FR1-1** <title> — <the fix, one line> · depends_on: — · risk: <low|standard|high> · paths: <paths>
`````
````

The ID is `F`, this ticket's ID without its dash, a dash and a number from 1: `FR1-1` for R-1, `FW8R-1` for W8-R. `risk` follows build plan §1: `high` for anything in the trusted core. If there are no findings, the report says so and there is no `APPEND` block.
