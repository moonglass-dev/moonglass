---
verdict: ready
reason: ""
---

# Plan for FR4-4

## Files to change

- `tools/pipeline/verify.go`: in `func bannedWordsSkip(f string) bool`, delete the line `f == "ui/svelte.config.js" ||` (line 762). The other clauses stay as they are, and so does the doc comment above the function (it already names only `ui/vite.config.ts` and `ui/scaffold_test.go`).
- `tools/pipeline/parse_test.go`: in `TestBannedWordsSkip`, change `"ui/svelte.config.js": true,` to `"ui/svelte.config.js": false,` (line 153). Keep the column alignment the same so gofumpt makes no other change.

## Tests to write

- None are new. `./tools/pipeline TestBannedWordsSkip` is the existing test, and it now asserts `bannedWordsSkip("ui/svelte.config.js") == false`. Its other rows keep their values, including `"docs/tickets/T-1/svelte.config.js": true`, which is exempt because it is under `docs/`. `testdata/tests.txt` stays unchanged.

## Edge cases

- `ui/svelte.config.js`, the path itself, is no longer exempt. The changed row covers it.
- `docs/tickets/T-1/svelte.config.js` is still exempt through the `docs/` prefix. Its existing `true` row covers it.
- `ui/vite.config.ts` and `ui/scaffold_test.go` keep their exemptions. Their existing `true` rows cover them.
- `scripts/svelte.config.js` and `ui/svelte.config.ts` stay not exempt. Their existing `false` rows cover them.

## Risks

- Diff size: the change is one deleted line and one changed line, which fits `max_diff_lines: 2`. Edit nothing else, and don't let a reformat touch the map alignment, because the key length does not change.
- Allowed paths: `tools/pipeline/` is a protected path, but the ticket's `allowed_paths` names both files.
- Coverage: `ui/svelte.config.js` does not exist in the repo (ADR-0016 decision 3), so dropping its exemption can't make any current diff fail the banned-words gate.
- Verification: `go test ./tools/pipeline -run TestBannedWordsSkip`, `grep -n 'svelte.config.js' tools/pipeline/verify.go` (empty), `make ci`, `scripts/verify check FR4-4`.
