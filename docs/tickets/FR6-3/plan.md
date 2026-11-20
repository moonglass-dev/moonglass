---
verdict: ready
reason: ""
---

# Plan for FR6-3

## Files to change

- `docs/plan/build-plan.md`: replace only the `**W7-6e**` line (line 217). The new line is exactly:

  ```
  - **W7-6e** Agent self-update and revert — download into a private staging directory, verify with the W7-6a verifier and the signed SHA256SUMS, keep the previous binary and switch; the systemd revert after five minutes without a passing self-check; the self-check of ADR-0007 decision 4: running and not restarted, the pinned connection back, the running executable's SHA-256 equal to the verified file, the proxy check passing; the agent sets `Heartbeat.update_channel` from `update.channel` in the agent.toml that `agent connect` loaded with `policy.LoadFile`, passed from cmd/moonglass into internal/agent: `stable` reports `STABLE`, `canary` reports `CANARY`, and an absent key or an absent file reports `STABLE` (`docs/specs/update.md`, `docs/specs/install.md`) · depends_on: W7-6a, W7-6b, W7-6d, W2-10b · risk: high · paths: internal/update/, internal/agent/, cmd/moonglass/, deploy/systemd/, scripts/install, internal/installtest/, docs/specs/install.md
  ```

  The existing intent text stays word for word; one clause is added before the spec references. `depends_on` appends W2-10b; `paths` inserts `cmd/moonglass/` after `internal/agent/`; `risk: high` is kept. The diff is one removed and one added line (within `max_diff_lines: 3`).

## Tests to write

- None: the ticket names no tests and changes no code. `testdata/tests.txt` is unchanged.

## Edge cases

- Parser: `tools/pipeline/ticket.go` matches `^- \*\*([^*]+)\*\* (.+?) — (.+)$` and splits on ` · `. The added clause contains no ` · ` and no second ` — `, and its backticks hold no `*`, so the line still parses. Check with `scripts/ticket-status`, which must list W7-6e with W2-10b.
- Cycle: W2-10b depends on W2-10a, W2-4c and W2-6 (all week 2), none of which reach a W7 ticket, so adding the edge creates no cycle.
- Mapping values match `docs/specs/update.md` §1 line 35 and `docs/specs/protocol.md` "Heartbeat" line 65 word for word (`stable`/`STABLE`, `canary`/`CANARY`, absent key or absent file → `STABLE`). `internal/policy` already supports it: `Config.Channel` defaults to `"stable"` and `LoadFile` returns `Parse(nil, env)` for a missing file, so no schema change is implied.

## Risks

- Touching any other build-plan line, or reflowing W7-6e: avoided by an exact single-line replacement; confirm with `git diff main -- docs/plan/build-plan.md` showing one `-` and one `+` line.
- Contract change concern: none. The agent.toml schema, the `.proto` field and the mapping are already final (FR5-2, update.md §10 item 31); this only records them in the plan.
- `make ci` is unaffected by a docs line but is run to satisfy the definition of done.
