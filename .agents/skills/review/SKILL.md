---
name: review
description: Review a Moonglass ticket PR before it merges. The dispatcher delegates this to Opus (claudeAgent / claude-opus-5-5) with the diff and the relevant spec section, then acts on the verdict. Opus does not merge or enable auto-merge.
---

# Review a PR

The dispatcher delegates this skill to Opus as a child (`delegate_task`, `claudeAgent` / `claude-opus-5-5`) and passes the diff (`git diff origin/main...$sha`), the named spec section, the brief, and the PR body. The dispatcher does not review the diff itself. Opus reviews for correctness and the rules, not taste, and returns a verdict. The dispatcher acts on it.

## Read

1. `git fetch -q origin p/<ID>` and record the head: `sha=$(git rev-parse FETCH_HEAD)`.
2. `git diff --stat origin/main...$sha`, then the test diff first: `git diff origin/main...$sha -- '*_test.go'`. Then the rest, a package at a time. Open a whole file only when the diff is not enough to judge a change. Read the spec section the dispatcher attached.
3. The PR body (`gh pr view <n> --json body -q .body`), for the decisions recorded there.

## Check

- **Brief:** every named acceptance test exists and asserts the behaviour (not a tautology, not only "no error"), and the code does what the brief says, nothing more.
- **Tests:** table-driven where there are cases; no skip outside the Docker and Postgres rules; no deleted or weakened test; `testdata/tests.txt` matches.
- **Rules (`AGENTS.md`):** one way to do each thing; old paths deleted; errors wrapped and actionable; no new dependency; generated code regenerated, not edited; no banned words; specs updated when behaviour changed.
- **Security**, for any change to `risk: high` paths, the network, the filesystem, Docker or auth: the deny list in `AGENTS.md` and `docs/threat-model.md` holds; no secret or token is logged or echoed; no shell string crosses the protocol; `agent.toml` stays local policy; input is validated where it enters.
- **Decisions:** every default in the PR body is one you would have made. If one needs the owner (a contract, an ADR, a dependency, a security trade-off), return `needs-owner`.

## Verdict

Return a verdict to the dispatcher. Do not merge, enable auto-merge, start a worker, or commit a fix yourself.

- **Approve:** return `approve` at `$sha`, with comment text `Opus review: approved at <sha8>. <one or two lines on what was checked>`. The dispatcher posts that comment and runs `scripts/plan merge <ID> --sha $sha`. Auto-merge squashes the PR when `ci` passes. A push after review changes the head, so auto-merge refuses it and the PR needs another review.
- **Changes:** return `changes` and one fix brief (`dispatch`, "The brief"), each finding as `file:line — what is wrong — what to do`. The dispatcher sends that brief to a worker.
- **Needs owner:** return `needs-owner` with the proposal, the options and your recommendation. The dispatcher parks the PR (`.agents/skills/dispatch/SKILL.md`, "Needs owner").
- **A second round still wrong:** return `escalate` and a fix brief. The dispatcher gives the ticket to the Opus worker row.

Nits that are not defects go in the approval comment, not a fix round.
