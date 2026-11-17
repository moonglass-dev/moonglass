# Harness follow-ups

Nits and residual holes to fix later. Do not block product tickets on these. The model still must not need `go get`; the harness fetches ticket `dependencies:` itself.

## Open

- **`pr` cannot recover when the PR already exists.** `cmd_pr` always runs `gh pr create`. If the branch already has a PR (a retry after `pr_blocked`, a manual create, or a previous successful create whose state write failed), create fails and the ticket stays `pr_blocked`. It should look up `gh pr list --head` and reuse the URL.

- **`review` cannot run from `verified`.** `cmd_review` only accepts `pr_open` or `review_blocked`. After a fix, `run_verify` sets `verified` and a re-review needs a manual state edit back to `pr_open`. The same is true if the ticket is in `verified` and the PR already exists: `run` calls `cmd_pr` again instead of review.

- **`tests-immutable` / locked tests can freeze a fixture bug.** W1-6's `signArtifact` helper called `ssh-keygen -Y sign` on a file that `signAll` had already signed. OpenSSH leaves the existing `.sig` in place and exits 0, so the "unknown key" and "wrong namespace" cases still had a valid signature and no correct install script could fail them. The implementer stopped rather than edit the locked tests. The owner added an `os.Remove` of the `.sig` before re-signing and updated `.ticket/W1-6/locked.txt`. The gate should let the owner relock a test with a recorded reason, or the tests prompt should warn that `ssh-keygen -Y sign` does not overwrite.

- **The 800-line size gate is too tight for W2-1, and it counts docs, SQL migrations, Makefile, `.golangci.yml`, and generated OpenAPI/CLI.** The implementer stopped after two attempts (1319 counted lines) rather than split the ticket. Hand-written non-test Go is about 921 lines; the rest is generated `openapi.json`, specs, ADRs, and migrations. The owner added a local, uncommitted hook that skips those files (like `*.pb.go`) and lets W2-1 pass under 1000 remaining Go lines. It must not be committed with a product ticket. Decide whether generated API surfaces and docs should be excluded for good, and whether Core tickets that add a store plus two surfaces get a higher cap, instead of splitting W2-1 after the fact.

- **`gate_red` still requires new tests to fail on the base commit for Light tickets.** Light has no tests-first stage, and W1-7 is a CI budget check of already-shipped processes, so the new test correctly passes on main. The owner added a local, uncommitted hook that skips `gate_red` for Light; it must not be committed with a product ticket. The gate should skip Light, or allow characterization tests.

- **The paths gate does not allow `_test.go` files named in `tests:` unless that path is also in `packages`.** W1-7 requires `./internal/proxy TestProxyAndAgentIdleUnderBudget`, but `internal/proxy` is not a product package of the ticket, so impl stopped (`impl_blocked`) until `internal/proxy/memory_test.go` was added to `packages`. The gate should treat a file that implements a named `tests:` entry as in-packages.

- **Allowing the `proto` or `internal/protocol` directory does not lift `Edit(**/*.proto)` / `Edit(**/*.pb.go)`.** `session_settings` only probes `$allow` and `$allow/x`, and `proto/x` is not a `.proto` file, so W2-2's impl was denied when editing `agent.proto`. Name the concrete files in `allow` (for example `proto/moonglass/agent/v1/agent.proto` and `internal/protocol/agentv1/agent.pb.go`). The probe could also try `$allow/x.proto` / `$allow/x.pb.go`.
- **Write sessions cannot run `make proto` / `make proto-tools` / `make proto-check`.** Those targets are not in the Bash allow list, and `proto-tools` would also need `curl` / `go install` (both denied). Tools are already cached under `~/.cache/moonglass`. A protocol ticket still cannot regenerate `*.pb.go` unless the owner adds those make targets for the session. The owner added a local, uncommitted hook in `scripts/ticket` for W2-2 only; it must not be committed with a product ticket.

- **The tests stage rejects `docs/tickets/<ID>.md` as a non-test file.** W2-2's tests session committed the ticket (it is in `packages` and is not on main yet) and `plan_blocked` with `tests stage changed non-test files`. The tests prompt should say not to add the ticket file, or the stage should allow `docs/tickets/<ID>.md`. The owner dropped the file from the tests commit and locked the tests by hand.
- **depguard cannot yet confine compose-go and go-toml to `internal/policy` only.** A second strict allow-list with `files: "!**/internal/policy/**"` still ran the main rule on policy files and broke lint. Today's deny rule covers only `cmd/` and `internal/protocol/`. Tighten this when a later package is added, or when depguard file globs are confirmed to combine correctly.

- **`tests-immutable` treats a Skip added in the tests stage as a verify failure.** The gate scans the whole `BASE..HEAD` diff, so a legitimate `t.Skip` written with the tests (W1-1: `TestLoadFileRejectsUnsafeFile` skips the unreadable-file case when running as root) fails verify. The gate should compare later commits to the tests commit / `locked.txt`, not to main.

- **Size gate counts ticket, spec, and ADR docs, and 800 is tight for a core library ticket.** W1-1's non-test diff was 2,783 lines (already excluding `*_test.go`, `testdata/`, `go.sum` and `*.pb.go`). The cap was bypassed for this ticket only so product work could land. Consider excluding `docs/tickets/`, the ticket `spec`, and ADRs, or raising the cap for core tickets.

- **Unlisted `go.mod` require on impl retry (W0-8 review).** If a previous impl session commits an unlisted `require` in `go.mod` (allowed by the ticket's `allow` list) and the owner reruns `impl`, `go get` / `go mod download` still have network access and will fetch that module even though it is not in `dependencies:`. Offline tidy then succeeds from the cache. The tidy-`@latest` path is closed; this retry path is not. Only ticket `dependencies:` should be fetched.
- **Compound Bash still denied as a whole.** Write sessions that issue `cmd1; cmd2 | head` (or include `go version` in a chain) get one denial and may treat Bash as unavailable. Prompts now say "one command per call, retry on denial"; that is guidance, not enforcement.
- **`cat` / `rg` vs `Read` denies.** Inspection allows can read paths that `Read(...)` denies (documented in `docs/harness.md`).
- **Short-flag commit bundles.** `git commit -m msg -qan` (and similar unlisted bundles) can still skip hooks.
- **Commit messages containing flag-like substrings.** Messages with ` -n`, ` -F`, and similar can match commit denies and stall a stage.
- **Size gate counts ticket and spec docs.** A core ticket whose brief and spec are large can fail `MAX_NEW_LINES` (800) before product code is counted. Consider excluding `docs/tickets/` and the ticket's `spec`, or raising the cap for docs.
- **Single-command / retry guidance is prompt-only.** The harness does not enforce one command per Bash call.
- **Indirect requires until code exists.** A listed module that nothing imports yet stays `// indirect` after the pre-impl fetch; the model cannot run `go mod tidy`.

- **The tests stage does not lint.** W1-3's locked tests had two `errcheck` findings (`defer res.Body.Close()`), so the impl stage could not pass `make ci` without editing locked files. The owner fixed them and re-locked. `gate_red` could run `make lint` on the test files before locking.
- **The fix round cannot add a regression test to a locked file.** W1-3's fix appended a test to a locked `_test.go` and tripped tests-immutable. Additive changes to locked files in the fix stage could be allowed (no removed lines, named tests unchanged).
- **`fetch_deps` misses zip hashes for modules a new dependency bumps.** W1-5: Caddy raised `go-viper/mapstructure/v2` and `santhosh-tekuri/jsonschema/v6`, which `internal/policy` already imports; only their `/go.mod` hashes were recorded, so `go vet ./...` failed. The owner ran `go mod download` for them. Fetch could run `go list -deps ./...` over the whole module, not only the stub.

## Closed in recent tickets

- The size gate no longer counts `go.mod`, like `go.sum`; W1-5 added about 155 generated `// indirect` lines for Caddy and failed the gate at 802 lines.
- The words gate no longer scans `go.mod` and `go.sum`; module paths such as `golang.org/x/crypto/x509roots/fallback` (a Caddy requirement) are not our words.
- Letter-suffix ticket IDs (W0-6).
- Per-session allow-list override of protected-path Edit denies; `.claude/` and `scripts/ticket` stay guarded (W0-6).
- `plan_blocked` on `new_dependencies` already in the ticket or an accepted ADR; real ADR status lines (`- **Status:** Accepted`) (W0-6, W0-7).
- Normal `git add` / `git commit -q -m` forms; `gofmt` removed in favor of `make fmt` only (W0-7).
- Harness fetches versioned `dependencies:` before impl; `go version` / `go env` allowed; `go get` stays denied for models (W0-8).
- `fetch_deps` uses the declared `toolchain` (go1.27.4) when the installed `go` is older than `go.mod`, and still fails if a listed module would change the `go` or `toolchain` lines.
- `fetch_deps` writes a temporary import stub so transitive `go.sum` entries are present before the impl session; the stub is removed and tidy is not rerun after that, so the checksums stay.
- Offline `go mod tidy` after the stub also walked test files of listed modules (so it needed test-only deps such as `github.com/jackc/puddle/v2` only after a non-root package was imported, and it failed GOPROXY=off) and treated missing local packages from a tests-first tree as a fetch failure. Fetch now records checksums with `go get module/...` plus `go list -deps` of the stub, and rejects unlisted imports with an offline `go list` instead of tidy.
