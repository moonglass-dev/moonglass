---
verdict: ready
reason: ""
---

# Plan for W7-6c

No contract, ADR, module or open security decision is needed: §9 of `docs/specs/update.md` and `ci.yml` fix the actions and their pins, §9 step 4 fixes the permissions, and ADR-0007 d1, TM §2 and §9 "The secret pair" fix the rule of no secrets and no signing.

## Files to change

- `.github/workflows/release.yml` (new, about 90 lines; the only file counted toward `max_diff_lines: 180`). One job, `release`, `runs-on: ubuntu-latest`. The trigger is `on: push: tags: ['v[0-9]+.[0-9]+.[0-9]+']`. The workflow-level `permissions` are exactly `id-token: write`, `attestations: write`, `contents: write`. The job `env` is `VERSION: ${{ github.ref_name }}`. Steps, in order:
  1. `actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1` with `persist-credentials: false`.
  2. `actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0` with `go-version-file: go.mod`.
  3. `Build linux-amd64` with `env: GOARCH: amd64`. Its run is `CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o dist/amd64/moonglass ./cmd/moonglass`.
  4. `Build linux-arm64`: the same with `GOARCH: arm64` and `dist/arm64`.
  5. and 6. `SBOM linux-<arch>` with `anchore/sbom-action@66cbf4bc1f1c0d2edc94016e65bc221b6bb0ad6c # v0.24.3`. Its `with` is `file: dist/<arch>/moonglass`, `format: spdx-json`, `output-file: dist/<arch>/moonglass.sbom.json`, `upload-artifact: false`, `upload-release-assets: false`.
  7. and 8. `Package linux-<arch>` with `env: ARCH: <arch>`. Its run block is:
     ```
     cp deploy/systemd/moonglass-* "dist/$ARCH/"
     cd "dist/$ARCH"
     sha256sum moonglass moonglass.sbom.json moonglass-* > SHA256SUMS
     tar -cf "../moonglass-$VERSION-linux-$ARCH.tar" -- *
     ```
  9. `actions/attest-build-provenance@4d101475d8b20a2381f78447822ac1eab6504dd8 # v4.2.2`. Its `subject-path: |` lists `dist/amd64/moonglass`, `dist/amd64/moonglass.sbom.json`, `dist/arm64/moonglass` and `dist/arm64/moonglass.sbom.json`.
  10. `Upload to a draft release` with `env: GH_TOKEN: ${{ github.token }}` and `GH_REPO: ${{ github.repository }}`. Its run is `gh release create "$VERSION" --draft --verify-tag --title "$VERSION" "dist/moonglass-$VERSION-linux-amd64.tar" "dist/moonglass-$VERSION-linux-arm64.tar"`.

  The tag reaches shells only through `$VERSION`. The SBOM action's own uploads are off. `gh` is already on the runner, so no action is added for the release. In the repository root, `deploy/systemd/moonglass-*` matches only units, including W7-6e's revert units once they exist. Inside the set directory, `moonglass-*` matches only the copied units, so `SHA256SUMS` covers every other file.
- `internal/releasetest/release_test.go` (new, test-only package like `internal/installtest`, with a package comment and no runtime code). It uses only the standard library. Helpers:
  - `repoRoot(t)`: walks up to `go.mod`.
  - `readFile(t, rel)`.
  - `type step struct{ name, uses, run string; with, env map[string]string; line int }`.
  - `parseSteps(t, text) []step`: an indentation parser for the single `steps:` list. It reads inline or `|` block `run` values and one-level `with` and `env` maps. It fails the test on a second `steps:` or an unknown step key.
  - `blocks(text, key) [][]string`: the inline value or indented body of every `key:` line, used for `permissions:` and `on:`.
  - `usesLines(text) []string`.
  - `stepsByEnv(steps, key, value) []step`.
- `docs/specs/update.md` §9, "The workflow":
  - Step 1 changes `-X <version var>=<tag>` to `-X main.version=<tag>`.
  - Step 3 becomes: "It copies the units with the glob `deploy/systemd/moonglass-*`, then writes `SHA256SUMS` over the other artifacts of that arch's set."
  - Nothing else changes.
- `testdata/tests.txt`: `make test-list` adds the nine lines.

## Tests to write

All in `./internal/releasetest`. Every failure prints the expected value and the actual value or line (`want %q, got %q`, with a line number where there is one).

- `TestReleaseWorkflowTriggersOnVersionTagsOnly`:
  - `on:` holds exactly `push` → `tags` → the one pattern `v[0-9]+.[0-9]+.[0-9]+`, quotes optional.
  - `push` has no `branches`, `branches-ignore` or `paths`.
  - The file has no `pull_request`, `pull_request_target`, `schedule`, `workflow_run` or `workflow_dispatch` key.
- `TestReleaseWorkflowBuildsStaticBinariesPerArch`, a table over `amd64` and `arm64`:
  - Exactly one step has `env.GOARCH` set to that arch.
  - Its run holds `CGO_ENABLED=0`, `GOOS=linux`, `go build`, `-trimpath`, `-ldflags "-s -w -X main.version=$VERSION"`, `-o dist/<arch>/moonglass` and `./cmd/moonglass`.
  - The job `env` has `VERSION: ${{ github.ref_name }}`.
  - No run script sets `GOARCH=`.
- `TestReleaseWorkflowWritesSPDXSBOM`, per arch:
  - Exactly one `anchore/sbom-action` step has `file: dist/<arch>/moonglass`, `format: spdx-json` and `output-file: dist/<arch>/moonglass.sbom.json`.
  - That step has `upload-artifact: false` and `upload-release-assets: false`.
- `TestReleaseWorkflowPackagesArtifactSet`, per arch:
  - Exactly one step has `env.ARCH` set to that arch, and its run holds the `cp deploy/systemd/moonglass-*`, `sha256sum moonglass moonglass.sbom.json moonglass-* > SHA256SUMS` and `tar -cf "../moonglass-$VERSION-linux-$ARCH.tar" -- *` lines, in that order.
  - No line in the file names a single unit (`moonglass-agent.service`, `moonglass-proxy.service` or any `deploy/systemd/<file>`).
  - No other line writes `SHA256SUMS`.
- `TestReleaseWorkflowAttestsBinariesAndSBOM`:
  - There is exactly one `actions/attest-build-provenance` step.
  - Its `subject-path` lines are exactly the four `dist/{amd64,arm64}/{moonglass,moonglass.sbom.json}` paths.
  - It comes after both SBOM and both package steps, and before the `gh release` step.
- `TestReleaseWorkflowUploadsToDraftRelease`:
  - Exactly one run holds `gh release`, and it is `gh release create "$VERSION" --draft …`.
  - Its file arguments are exactly the two tars.
  - The file has none of `--latest`, `--draft=false`, `gh release edit`, `gh release upload`, `gh api`, `softprops/` or `upload-release-assets: true`.
- `TestReleaseWorkflowPinsEveryAction`:
  - Every `uses:` matches `^[a-z0-9-]+/[a-z0-9-]+@[0-9a-f]{40} # v[0-9]\S*$`.
  - The set of `uses:` values is exactly checkout and setup-go as read from `.github/workflows/ci.yml`, plus the two §9 constants for sbom-action and attest-build-provenance.
  - Checkout has `persist-credentials: false`, and setup-go has `go-version-file: go.mod`.
- `TestReleaseWorkflowGrantsOnlyReleasePermissions`:
  - The file has at least one `permissions:` block, and none has an inline value, which rules out `write-all`, `read-all` and `{}`.
  - Every entry is `id-token: write`, `attestations: write` or `contents: write`.
  - Together the blocks grant all three.
- `TestReleaseWorkflowHoldsNoSecretOrSigningStep`:
  - The file has no `secrets.`, no `environment:` key, no `ssh-keygen`, no `.sig`, no `\baws\b` and no `workflow_dispatch`.
  - No step `run` contains `${{`.

## Edge cases

- **Ref-name injection:** `${{` inside a `run` fails the no-secret test. The build test checks that the version comes only from the job `env` mapping.
- **Wider triggers** (`branches`, `pull_request_target`, dispatch) fail the trigger test.
- **Unpinned actions:** a floating, extra or third-party action, or a checkout or setup-go pin that has drifted from `ci.yml`, fails the pin test.
- **Wider permissions:** `write-all`, `packages: write` or a job-level permissions block fails the permissions test, because every block is checked.
- **Extra uploads:** a published or latest release, an extra upload, or the SBOM action uploading its own asset fails the upload test or the SBOM test.
- **Units listed one by one** would miss W7-6e's revert units, and fail the packaging test.
- **Attestation gaps:** attesting after the upload, or attesting only one arch, fails the attest test.
- **Missing file or unexpected YAML shape:** a missing workflow file fails `readFile` with the path. An unknown step key or a second `steps:` fails `parseSteps`. Every per-arch table entry fails when it finds zero matching steps, so no check passes with nothing to check.

## Risks

- **Brittle text tests:** the exact workflow text is fixed above, and the parser fails loudly on any other shape, so a reformat breaks visibly rather than silently.
- **Banned words:** the test strings `ssh-keygen`, `.sig`, `aws` and `workflow_dispatch` are not on the banned list. Keep TODO, FIXME, XXX, HACK, fallback and legacy out of comments.
- **No runtime check:** nothing in CI ever runs the workflow. It runs only on a tag, and attestations need a public repository, which is the owner's step (§9).
  - The plan uses only the documented inputs of the pinned versions: sbom-action `file`, `format`, `output-file`, `upload-artifact` and `upload-release-assets`, and attest-build-provenance `subject-path` as a multi-line list.
  - `gh release create --draft --verify-tag` runs with the job token under `contents: write`.
- **Lint:** use `t.Helper()`, check every error, and compile regexps at package level.
- **Size:** only `release.yml` counts toward the cap (about 90 of 180 lines). `countsTowardSize` in `tools/pipeline/verify.go` excludes test files, `docs/` and `.md` files.
- **Scope:** no publish job, no install-script change and no revert units (W7-6g, W7-6e). Nothing changes outside `allowed_paths`, apart from `testdata/tests.txt`.
