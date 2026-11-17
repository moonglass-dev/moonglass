# Harness follow-ups

Nits and residual holes to fix later. Do not block product tickets on these. The model still must not need `go get`; the harness fetches ticket `dependencies:` itself.

## Open

- **depguard cannot yet confine compose-go and go-toml to `internal/policy` only.** A second strict allow-list with `files: "!**/internal/policy/**"` still ran the main rule on policy files and broke lint. Today's deny rule covers only `cmd/` and `internal/protocol/`. Tighten this when a later package is added, or when depguard file globs are confirmed to combine correctly.

- **`tests-immutable` treats a Skip added in the tests stage as a verify failure.** The gate scans the whole `BASE..HEAD` diff, so a legitimate `t.Skip` written with the tests (W1-1: `TestLoadFileRejectsUnsafeFile` skips the unreadable-file case when running as root) fails verify. The gate should compare later commits to the tests commit / `locked.txt`, not to main.

- **Size gate counts ticket, spec, and ADR docs, and 800 is tight for a core library ticket.** W1-1's non-test diff was 2,783 lines (already excluding `*_test.go`, `testdata/`, `go.sum` and `*.pb.go`). The cap was bypassed for this ticket only so product work could land. Consider excluding `docs/tickets/`, the ticket `spec`, and ADRs, or raising the cap for core tickets.

- **Pre-impl `go get` of an unused module leaves transitive `go.sum` entries missing.** `fetch_deps` adds the listed modules (often `// indirect` because nothing imports them yet) but not the checksums of their dependencies. Once the implementer imports the module, `GOFLAGS=-mod=readonly` fails with "missing go.sum entry" for transitives, and the model cannot run `go mod tidy`. Fetch should populate the full checksum graph of listed modules (for example by downloading the module graph, or by running a post-import tidy in the harness). Workaround used for W1-1: the executor ran `go mod tidy` after writing the imports.

- **`fetch_deps` with `GOTOOLCHAIN=local` fails when the installed `go` is older than `go.mod`.** The harness pins `GOTOOLCHAIN=local` so a listed module cannot download a newer toolchain or raise the `go` line. On a machine whose `/usr/local/go` is still 1.26.2, that fetch dies with `go.mod requires go >= 1.27.0 (running go 1.26.2; GOTOOLCHAIN=local)` even though the repo already declares `go 1.27.0` / `toolchain go1.27.4`. Fetch should use the already-declared toolchain, not a frozen older local. Workaround used for W1-1: put the `go1.27.4` toolchain binary first on `PATH`.

- **Unlisted `go.mod` require on impl retry (W0-8 review).** If a previous impl session commits an unlisted `require` in `go.mod` (allowed by the ticket's `allow` list) and the owner reruns `impl`, `go get` / `go mod download` still have network access and will fetch that module even though it is not in `dependencies:`. Offline tidy then succeeds from the cache. The tidy-`@latest` path is closed; this retry path is not. Only ticket `dependencies:` should be fetched.
- **Compound Bash still denied as a whole.** Write sessions that issue `cmd1; cmd2 | head` (or include `go version` in a chain) get one denial and may treat Bash as unavailable. Prompts now say "one command per call, retry on denial"; that is guidance, not enforcement.
- **`cat` / `rg` vs `Read` denies.** Inspection allows can read paths that `Read(...)` denies (documented in `docs/harness.md`).
- **Short-flag commit bundles.** `git commit -m msg -qan` (and similar unlisted bundles) can still skip hooks.
- **Commit messages containing flag-like substrings.** Messages with ` -n`, ` -F`, and similar can match commit denies and stall a stage.
- **Size gate counts ticket and spec docs.** A core ticket whose brief and spec are large can fail `MAX_NEW_LINES` (800) before product code is counted. Consider excluding `docs/tickets/` and the ticket's `spec`, or raising the cap for docs.
- **Single-command / retry guidance is prompt-only.** The harness does not enforce one command per Bash call.
- **Indirect requires until code exists.** A listed module that nothing imports yet stays `// indirect` after the pre-impl fetch; the model cannot run `go mod tidy`.

## Closed in recent tickets

- Letter-suffix ticket IDs (W0-6).
- Per-session allow-list override of protected-path Edit denies; `.claude/` and `scripts/ticket` stay guarded (W0-6).
- `plan_blocked` on `new_dependencies` already in the ticket or an accepted ADR; real ADR status lines (`- **Status:** Accepted`) (W0-6, W0-7).
- Normal `git add` / `git commit -q -m` forms; `gofmt` removed in favor of `make fmt` only (W0-7).
- Harness fetches versioned `dependencies:` before impl; `go version` / `go env` allowed; `go get` stays denied for models (W0-8).
