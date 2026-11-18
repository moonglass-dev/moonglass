---
verdict: ready
reason: ""
---

# Plan for FR1-9

## Files to change

- `go.mod`, `go.sum`: replace both with the output of `go mod tidy` under the pinned toolchain (`go1.27.4`, from `toolchain go1.27.4`). Do not edit them by hand. A dry run (`go mod tidy -diff`) on this branch gives:
  - Two modules move into the direct block: `github.com/danielgtaylor/huma/v2 v2.40.0` and `github.com/jackc/pgx/v5 v5.13.0`. `modernc.org/sqlite v1.62.0` is already direct on this branch. After the change the direct block holds exactly age, caddy/v2, coder/websocket, compose-go/v2, huma/v2, pgx/v5, go-toml/v2, protobuf and modernc sqlite, all at their current versions.
  - 59 unused requirements are removed, including BurntSushi/toml, gin, fiber v2 and v3, echo v4 and v5, chi/v5, gorilla/mux and bunrouter.
  - One indirect line is added: `github.com/google/btree v1.1.3 // indirect`. Its only v1.1.3 requirer, `certificate-transparency-go`, leaves the graph, so tidy pins it to keep the selected version. This is a transitive requirement, not a new dependency, and the `dependencies` gate skips `// indirect` lines. No version of any module in the build list changes.
  - `go.sum` gains about 126 lines and loses about 143. The `size` gate does not count `go.sum` or `go.mod`.
- `Makefile`:
  - New target, placed after `proto-check`:
    ```make
    # Fails when go.mod or go.sum differs from what "go mod tidy" writes.
    mod-check:
    	@go mod tidy -diff || { echo 'go.mod or go.sum is not tidy: run "go mod tidy" and commit the result' >&2; exit 1; }
    ```
    `-diff` writes nothing, so the target is read-only and safe to run in parallel with the other steps.
  - Add `mod-check` to `.PHONY`.
  - In `ci`, the first phase becomes `run proto-check & p1`, `run fmt-check & p2` and `run mod-check & p3`. Add `wait $$p3 || rc=1`, and extend the `cat` to `"$$dir/proto-check.status" "$$dir/fmt-check.status" "$$dir/mod-check.status"`, which prints `ok   mod-check`. Update the comment above `ci` to say that the first phase runs proto-check, fmt-check and mod-check.
- `AGENTS.md`: the `make ci` row becomes "The one gate: `fmt-check`, `mod-check`, `lint`, `proto-check`, `test`, `check-test-list`, `build-all`".

No Go source, `.github/workflows/ci.yml`, or `testdata/tests.txt` changes.

## Tests to write

- None. The ticket names no tests (`tests: []`). The gate itself is the check:
  - `go mod tidy -diff` exits 0 and prints nothing.
  - `make mod-check` exits 0.
  - Negative check, done by hand and then reverted: add `require github.com/gorilla/mux v1.8.1` to `go.mod`. `make mod-check` then exits non-zero and prints the message to stderr.
  - `make ci` is green and prints `ok   mod-check`.
  - `make check-test-list` still passes and `testdata/tests.txt` is unchanged.

## Edge cases

- Stray or unused requirement: `go mod tidy -diff` prints the diff and exits 1. The target then prints the fixed message and exits 1, and `ci` shows `FAIL mod-check` with the tail of the output. Covered by the negative check above.
- Missing `go.sum` entries: tidy reports them as a diff, and the same failure path applies.
- Cold module cache in CI: `go mod tidy -diff` may download modules. CI already has network access for `go build` and `go test`, so this adds no new requirement.
- Running in parallel with `proto-check`: `proto-check` can run `go install` against the module cache, and `mod-check` only reads `go.mod` and `go.sum`. Both are safe to run at the same time.

## Risks

- A wrong toolchain could produce a different tidy output. Run `go mod tidy` with go1.27.4. CI reads it through `go-version-file: go.mod`. Check `go version` before committing.
- Version drift: compare `go list -m all` before and after. The only difference should be the removed modules, with no version changes. The ticket forbids upgrades.
- `dependencies` gate: the newly added non-indirect lines are huma/v2 (ADR-0013) and pgx/v5 (ADR-0010). Both ADRs are accepted, so the gate passes.
- `words` gate: `go.mod` and `go.sum` are exempt, and the new Makefile lines contain no banned words.
- `size` gate: only the Makefile counts, about 8 lines against a cap of 120.
- `paths` gate: the change touches only `go.mod`, `go.sum`, `Makefile` and `AGENTS.md`, all of which are in `allowed_paths`.
