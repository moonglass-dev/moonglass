---
name: review
description: Review a Moonglass ticket PR before it merges. Use after a worker reports a PR. Read the diff, not whole files, then approve and turn on auto-merge, or send one fix brief.
---

# Review a PR

Opus reviews every diff before it merges. The aim is correctness and the rules, not taste: request changes only for a real defect.

## Read

1. `git fetch -q origin p/<ID>` and record the head: `sha=$(git rev-parse FETCH_HEAD)`.
2. `git diff --stat origin/main...$sha`, then the test diff first: `git diff origin/main...$sha -- '*_test.go'`. Then the rest, a package at a time. Open a whole file only when the diff is not enough to judge a change.
3. The PR body (`gh pr view <n> --json body -q .body`), for the decisions the worker made.

## Check

- **Brief:** every named acceptance test exists and asserts the behaviour (not a tautology, not only "no error"), and the code does what the brief says, nothing more.
- **Tests:** table-driven where there are cases; no skip outside the Docker and Postgres rules; no deleted or weakened test; `testdata/tests.txt` matches.
- **Rules (`AGENTS.md`):** one way to do each thing; old paths deleted; errors wrapped and actionable; no new dependency; generated code regenerated, not edited; no banned words; specs updated when behaviour changed.
- **Security**, for any change to `risk: high` paths, the network, the filesystem, Docker or auth: the deny list in `AGENTS.md` and `docs/threat-model.md` holds; no secret or token is logged or echoed; no shell string crosses the protocol; `agent.toml` stays local policy; input is validated where it enters.
- **Decisions:** every default in the PR body is one you would have made. If one needs the owner (a contract, an ADR, a dependency, a security trade-off), park the PR (`.agents/skills/dispatch/SKILL.md`, "Needs owner").

## Decide

- **Approve:** post one comment, `gh pr comment <n> --body "Opus review: approved at <sha8>. <one or two lines on what was checked>"`, then `scripts/plan merge <ID> --sha $sha`. Auto-merge squashes the PR when `ci` passes. A push after review changes the head, so auto-merge refuses it and the PR needs another review.
- **Changes:** one fix brief to a worker (`dispatch`, "The brief"), each finding as `file:line — what is wrong — what to do`. Fix a one-line slip yourself instead only when it is faster than writing it up; your commit follows the same identity rule.
- **A second round still wrong:** give the ticket to the Opus row in `dispatch`, or do the fix in a worktree yourself.

Nits that are not defects go in the approval comment, not a fix round.
