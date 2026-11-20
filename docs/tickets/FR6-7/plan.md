---
verdict: ready
reason: ""
---

# Plan for FR6-7

## Files to change

- `AGENTS.md`: replace line 12, the `internal/<package>/` row of the "Repo map" table, with exactly:

  ```
  | `internal/<package>/` | Product code, one package per area: `agent`, `api`, `cli`, `deploy`, `keys`, `policy`, `protocol`, `proxy`, `registry`, `release`, `server`, `store`, `update`, and `installtest` (tests for `scripts/install`) and `releasetest` (tests for `.github/workflows/release.yml`) |
  ```

  Nothing else in the file changes. The diff is one line removed and one added (2 lines, under `max_diff_lines: 3`).

## Tests to write

- None. The ticket names no tests (`tests: []`) and adds no code, so `testdata/tests.txt` is unchanged.

## Edge cases

- Completeness: `ls internal/` gives `agent api cli deploy installtest keys policy protocol proxy registry release releasetest server store update`. The new row names exactly these 15, no more. Checked by comparing the row against `ls internal/` before committing.
- `installtest` and `releasetest` stay outside the "product code" list and are described as tests for `scripts/install` and `.github/workflows/release.yml`. The second wording matches the package comment in `internal/releasetest/release_test.go:1`.
- Scope: `git diff main -- AGENTS.md` must show only line 12. The trusted-core list, Commands table and rules stay untouched.
- No banned words and no old product name are added. The new text only adds package names and a path.

## Risks

- AGENTS.md is a protected path. The ticket's `allowed_paths` names it, so the edit is allowed. Touching any other file would fail the allowed-paths gate.
- Markdown table breakage: the row keeps its two-cell `| … | … |` shape with no new pipe characters, so the table still renders.
- `make ci` does not read AGENTS.md, so it stays green. `scripts/verify check FR6-7` checks diff size and paths, and both pass with a 2-line diff to one allowed file.
