---
verdict: ready
reason: ""
---

# Plan for FR6-8

## Files to change

- `internal/deploy/switch_test.go`, `TestHealthGateHTTPPath` only: directly after the existing line 281 `x.healthLimit = 6 * time.Second`, add
  ```go
  if tc.wantPass {
  	x.healthLimit = 20 * time.Second
  }
  ```
  Three added lines, none removed. Nothing else changes: the cases, the `t.Parallel()` calls, `deployGate`, the error check for passing cases and the five assertions for failing cases all stay as they are. Failing cases keep the 6-second limit, so `elapsed >= x.healthLimit` and `elapsed <= x.healthLimit+3*time.Second` still check against 6s.

## Tests to write

- None added (the ticket's `tests: []`). `./internal/deploy TestHealthGateHTTPPath` is the existing test that gets changed: `200 passes` and `302 passes without following the redirect` run under a 20s limit and still fail on any error from `healthGate`. `500 fails at the limit` and `the stored path is probed, not another one` run under 6s with all five assertions unchanged. `testdata/tests.txt` does not change.

## Edge cases

- The passing cases now have the same 20s limit as the passing gates in `domain_owner_test.go` (lines 26, 87, 135, 189). That leaves about 17s for a fresh container to answer under `-race` and parallel load.
- The failing cases' timing bounds do not change, so a gate that fails slowly still gets caught.
- The new limit is set before `deployGate`/`healthGate` and inside the subtest's own executor `x`, so there is no shared state between the parallel subtests.

## Risks

- `removedTestLines` in `tools/pipeline/verify.go`: the plan only adds lines, so `git diff --numstat origin/main` shows 0 removed lines.
- `max_diff_lines: 10`: the diff is 3 lines.
- gofumpt/lint: a plain `if` block with tab indentation, so it passes `make fmt-check`.
- No production code, contract, spec or ADR changes. `docs/specs/release.md` "Health" stays accurate.
