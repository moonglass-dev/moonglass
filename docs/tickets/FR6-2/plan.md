---
verdict: ready
reason: ""
---

# Plan for FR6-2

## Files to change

- `internal/deploy/stop.go`:
  - Imports: delete `"strings"` (its only use in the file is `isConflict`). Add `"github.com/containerd/errdefs"` to the third-party group beside `github.com/moby/moby/client`, sorted (containerd before moby).
  - `func isConflict(err error) bool` body becomes `return errdefs.IsConflict(err)`. The doc comment says it matches only the Docker client's typed HTTP 409 conflict error (ADR-0017), never error text.
  - `removeImage` is unchanged: `isNotFound(err) || isConflict(err)` gives nil, anything else gives `fmt.Errorf("remove image %s: %w", id, err)`.
  - About 6 counted lines, under the cap of 18 (`tools/pipeline/verify.go` `countsTowardSize` leaves out `_test.go`, `go.mod`, `go.sum` and `testdata/tests.txt`).
- `go.mod`: `github.com/containerd/errdefs v1.0.0` moves from the indirect block into the first (direct) `require` block, in sorted position after `github.com/compose-spec/compose-go/v2`, with no `// indirect`. `github.com/containerd/errdefs/pkg v0.3.0 // indirect` stays where it is. Run `go mod tidy` after the import is added. It should make exactly this move. `go.sum` already has both `v1.0.0` hashes (lines 120–121), so it should not change. If tidy changes anything else, stop and block.
- `internal/deploy/conflict_test.go` (new): the three named tests below.
- `testdata/tests.txt`: rewritten with `make test-list`.

The spec does not change: docs/specs/release.md "Keep five" already describes this behaviour.

## Tests to write

All three go in `internal/deploy/conflict_test.go`, `package deploy`. They reuse `testEngine`, `testProject`, `registerImage`, `importImage`, `createFixture`, `fixtureSpec`, `emptyPolicy` and `testAppID` from `helpers_test.go`, and `stopImagePresent` from `retain_test.go`.

- `./internal/deploy TestIsConflictMatchesTypedErrorOnly`: a table of `{name string; err error; want bool}`, with no Engine needed:
  - `errdefs.ErrConflict` → true
  - `fmt.Errorf("remove image sha256:abc: %w", errdefs.ErrConflict)` → true
  - `errors.New("image conflict: unable to delete")` → false
  - `errdefs.ErrNotFound` → false
  - `fmt.Errorf("conflict while removing: %w", errdefs.ErrUnavailable)` → false

  On failure it reports `t.Errorf("isConflict(%q) = %v, want %v", tc.err, got, tc.want)`, which shows the input error, the actual value and the expected value.
- `./internal/deploy TestRemoveImageInUseIsNotAnError`: uses a real Engine.
  1. Set `p := testProject(t, e)` and `ref := "moonglass.local/"+p.name+"/web:inuse"`, call `p.registerImage(ref)`, then `id := importImage(t, e, ref, []string{"/app"})`.
  2. Create a holder: `createFixture(t, e, emptyPolicy(t, policy.Env{}), fixtureSpec(p, testAppID(p, "inuse"), "inuse", ref, true), "mg-"+p.name+"-inuse")`. It is labelled with the project, so the project cleanup removes it before the image.
  3. `_, err := e.cli.ImageRemove(ctx, id, client.ImageRemoveOptions{})`. Require `err != nil` and `errdefs.IsConflict(err)`. The failure message shows `err` and says the test wants the client's typed 409 conflict.
  4. `e.removeImage(ctx, id)` must return nil. The failure message shows the error.
  5. `stopImagePresent(t, e, id)` must be true.
- `./internal/deploy TestRemoveImageReportsOtherFailures`: uses a real Engine.
  1. Import an image as `moonglass.local/<p>/web:other` and register it.
  2. Build `ctx, cancel := context.WithCancel(context.Background())` and call `cancel()` straight away.
  3. Call `err := e.removeImage(ctx, id)`. Require `err != nil`, `strings.Contains(err.Error(), "remove image "+id)` and `!errdefs.IsConflict(err)`. Each failure message shows the actual error and what was wanted.
  4. With a background context, `stopImagePresent(t, e, id)` must be true.
- `TestRetainRemovesImagesOutsideFive` is not changed. It must still pass, because its holder container's conflict is the typed 409.

## Edge cases

- **Text that contains "conflict" but is not the typed error**: false. Covered by the `errors.New` row and the wrapped `ErrUnavailable` row of `TestIsConflictMatchesTypedErrorOnly`.
- **A wrapped typed conflict**: true through `errors.Is`. Covered by the wrapped row.
- **Not found**: `errdefs.ErrNotFound` is not a conflict (table row). `removeImage` still treats not found as nil through `isNotFound`, which is out of scope and unchanged. `TestRetainRemovesImagesOutsideFive` covers its r3 case.
- **A cancelled context or an Engine failure**: the error reaches the caller as `remove image <id>: …` and nothing is removed. Covered by `TestRemoveImageReportsOtherFailures`. The text `context canceled` matches neither `isNotFound` pattern ("not found", "No such ").
- **A real Engine 409**: moby client v0.6.3 maps 409 through `containerd/errdefs/pkg/errhttp` to `errdefs.ErrConflict`, so `errdefs.IsConflict` is true. Step 3 of `TestRemoveImageInUseIsNotAnError` asserts this directly, so a client upgrade that broke the mapping would fail loudly.
- **No Engine**: `testEngine` skips locally and fails under `CI`.
- **Worktree isolation**: the project name is random per run. The image and the holder container are registered with the project, so cleanup removes them.

## Risks

- **depguard**: the `errdefs-deps` rule excludes `**/internal/deploy/**`, so both `stop.go` and the test file may import it, and `main.allow` already lists it. `.golangci.yml` does not change.
- **`dependencies` gate**: the module is not new. It was already in `go.mod` and is named in full in ADR-0017.
- **`go mod tidy` drift**: with Go 1.27, tidy might reorder or regroup other lines. Check `git diff go.mod`. Only the one line may move, and `go.sum` must stay unchanged. Otherwise block.
- **Lint**: `gofumpt`/`goimports` import grouping in `stop.go` and `conflict_test.go`. Run `make fmt`.
- **Banned words**: none in the added lines. Do not write "fallback" in comments.
- **Size cap**: only `stop.go` counts, at about 6 lines.
- **Test order with `-shuffle`**: each test has its own project and images, so there is no shared state.
