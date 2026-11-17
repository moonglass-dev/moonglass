# ADR-0009: Policy dependencies

- Status: accepted
- Date: 2026-11-16

## Context

W1-1 adds `internal/policy`, which must parse `/etc/moonglass/agent.toml` strictly and check a Compose project only after interpolation, `extends`, `include`, YAML merge keys and profiles have been resolved. The repository had not yet imported a TOML library or a Compose parser. A new module requires an ADR and a depguard entry. The combined agent and proxy idle-memory budget is 100 MB (G1, `docs/plan/build-plan.md`).

## Decision

Pin these two modules, and import them only from `internal/policy`:

- `github.com/compose-spec/compose-go/v2 v2.17.0` — the official Compose parser named by ADR-0004 decision 7 and stack §2. It resolves interpolation, `extends`, `include` and profiles, so an escape cannot hide in a variable or an inactive profile.
- `github.com/pelletier/go-toml/v2 v2.4.3` — TOML parsing with `Decoder.DisallowUnknownFields` and line and column on errors. Strictness is a decoder setting, not a second pass over metadata.

Both versions are recorded in `go.mod` and in the depguard allow list in `.golangci.yml`. No other new direct module is added. Transitive modules pulled by compose-go stay indirect.

## Consequences

- `make build-all` (`CGO_ENABLED=0`, linux/amd64 and linux/arm64) is unchanged at 2,362,757 and 2,348,801 bytes respectively. `cmd/moonglass` does not yet import `internal/policy` (wiring is a later ticket), so the release binaries do not link the new libraries today. The build remains a static ELF.
- A process that has loaded a policy and run `Check` (measured from a test that calls `Parse` and `Check`, after GC) used 915,232 bytes of heap (`HeapAlloc`) and 13,830,408 bytes `MemStats.Sys`. That is well under the 100 MB combined idle budget, with ample headroom once the agent begins loading this package.
- compose-go v2.17.0 declares `go 1.26.11`. The module's transitives did not require cgo.

## Alternatives rejected

- **A hand-rolled Compose parser.** Would have to reimplement interpolation, `extends`, `include`, anchors and profiles correctly; a gap is an escape. The official loader is the one ADR-0004 names.
- **`BurntSushi/toml`.** Strict unknown-key errors and positions would need a second pass over metadata. `go-toml/v2` provides both on the decoder.
- **`yaml` plus custom resolution.** Reimplements what compose-go already does, and would still leave TOML for `agent.toml` unsolved.

## Open questions

None.

## Links

- `docs/specs/container-policy.md`
- `docs/adr/0004-container-spec-policy.md`
- `docs/plan/stack.md` §2, §3.1, §3.2
- B§6: W1-1; G1 in `docs/plan/build-plan.md`
