# Step 5: review

Runs as Claude Opus in plan mode: you cannot write files. Your result goes in your final message. You did not write this code and have no access to the conversations that did.

Moonglass is a deploy platform whose server agent has root-equivalent access to users' machines. Review accordingly.

## Inputs

Read in full:

- `AGENTS.md`
- `docs/threat-model.md`, including its review checklist.
- `docs/tickets/<ID>.md` and `docs/tickets/<ID>/plan.md`
- Every spec and ADR in the ticket's `reads`.
- `docs/tickets/<ID>/check.json`: the mechanical gates, all green.
- The change: `git diff origin/main...HEAD -- . ':!docs/tickets/<ID>/'` and `git log --oneline origin/main..HEAD`
- `docs/tickets/<ID>/review.json`, if it exists: the previous round. Check that each of its blockers and majors is fixed, and carry forward any that are not.
- `docs/tickets/<ID>/merge.json`, if it exists: a merge failure that a repair round handled.

## Do

Report only:

- correctness bugs;
- threat-model violations (use the checklist);
- acceptance criteria with no test, or a test that does not prove its criterion;
- tests that were deleted, weakened, skipped or hard-coded;
- fallback paths, dead code, or code for later tickets;
- behaviour that disagrees with the spec, or a spec not updated for changed behaviour.

Severity:

- `blockers`: a security property from the threat model is broken, or data can be lost or corrupted.
- `majors`: any other correctness bug, missing test for a criterion, spec disagreement, or fallback or dead code.
- `minors`: anything real but harmless. Minors never block a merge.

For each finding give the file, the line, what breaks, and a concrete failing scenario. No style comments. Do not propose new abstractions or features. If you find nothing, say so with an empty list.

## Output

One summary line, then:

````
FILE: docs/tickets/<ID>/review.json
`````text
{
  "round": <your round number>,
  "verdict": "approve",
  "blockers": [],
  "majors": [],
  "minors": [
    { "file": "internal/deploy/run.go", "line": 42, "issue": "what breaks", "scenario": "how it fails" }
  ]
}
`````
````

`verdict` is `changes` when `blockers` or `majors` is not empty, and `approve` otherwise. Use exactly these five keys; the scripts add the reviewed commit. `round` must be the round in your task.
