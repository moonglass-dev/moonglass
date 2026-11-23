---
name: drift
description: The weekly Moonglass drift check - tick merged tickets in the plan, fix doc drift, and file fix tickets for code drift, all in one PR. Use when scripts/plan says the drift check is due.
---

# Drift check

You run once a week, or after 15 merges, in your own worktree. You produce one PR titled `drift: week of <YYYY-MM-DD>`. Never ask questions.

## Steps

1. `git fetch -q origin main && git switch -c p/drift origin/main`. If that name exists, use `git switch -C p/drift origin/main`.
2. `scripts/plan tick`. It ticks the box of every ticket merged on `main` in `docs/plan/build-plan.md`.
3. Find the last drift commit: `last=$(git log --first-parent --format=%H --grep='^drift: ' -1 origin/main)`. List what merged since: `git log --first-parent --format='%h %s' $last..origin/main`. When there is no earlier drift commit, take the last 30 merges.
4. For each merged change, read `git show --stat <hash>`, and the diff of any file under `internal/`, `cmd/`, `proto/` or `deploy/`. Check it against the spec it names, the ADRs, and the docs map and repo map in `AGENTS.md`. Look only for drift:
   - a spec, ADR or the `AGENTS.md` repo map or docs map that does not match the code;
   - two implementations of one thing, dead code, or text matching on errors;
   - a rule in `AGENTS.md` that the code breaks.
5. Fix docs-only drift directly in this PR: specs, the maps in `AGENTS.md`, and stale references. Never change an ADR's decision; file a ticket for that instead.
6. Code drift becomes fix tickets. Append them to the end of the "Fixes and requests" list in `docs/plan/build-plan.md` §3, in its line format, with the ID `FD<n>-<k>`. `<n>` is one more than the highest `FD` number already in that list, and `<k>` counts from 1. Each line names the evidence (`file:line`) and the risk (`high` for trusted-core paths). One ticket per finding, with no duplicate of an open ticket.
7. Run `make ci`. Commit as `Moonglass Team <team@moonglass.dev>` with no trailers (`git -c user.name='Moonglass Team' -c user.email='team@moonglass.dev' commit -q -am 'drift: week of <date>'`). Write a PR body with `## Ticked` (the IDs), `## Doc fixes` and `## New tickets`, each finding with its evidence. Then run `scripts/plan pr drift --title 'week of <YYYY-MM-DD>' --risk low --body-file <file>`.
8. Final message: the PR URL and the counts (ticked, doc fixes, new tickets), in three lines at most.
