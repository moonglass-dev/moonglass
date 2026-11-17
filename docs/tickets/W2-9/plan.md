---
verdict: ready
reason: ""
---

# Plan for W2-9

ADR-0004 decisions 10–12 and `docs/specs/container-policy.md` "Pass-through fields and agent labels (W2-9)" settle every behaviour; no contract, ADR, module or security decision is open. `internal/policy` and `testdata/escape/` are in `allowed_paths`; test files and `testdata/` do not count toward `max_diff_lines` (`tools/pipeline/verify.go` `countsTowardSize`), so the ~90 counted production lines fit the 200 cap.

## Files to change

- `internal/policy/types.go`
  - New type `LogConfig struct { Options map[string]string; Driver string }` with a doc comment.
  - `Spec` gains `Cmd []string`, `Env map[string]string`, `Labels map[string]string`, `AgentLabels map[string]string`, `Networks []string`, `RestartPolicy string`, `LogConfig LogConfig`, each with a one-line comment from the spec table (`Cmd`: exec argv, never joined or passed to a shell; `AgentLabels`: set only by agent code). Field order is whatever `govet fieldalignment` accepts (it runs on non-test code); run `make lint` and reorder as it reports.
  - `Spec.clone` deep-copies the new fields with `slices.Clone` (`Cmd`, `Networks`) and `maps.Clone` (`Env`, `Labels`, `AgentLabels`, `LogConfig.Options`). Both keep nil as nil and empty as empty, so `reflect.DeepEqual(Checked.Spec(), s)` holds.
  - `DeniedFields` gains, after the two build-entitlement rows: `{Field: "labels: dev.moonglass. namespace", Key: "agent_labels"}`.
- `internal/policy/check.go`
  - `const agentLabelPrefix = "dev.moonglass."`.
  - `specWellFormed` adds three rules, each returning a non-nil error (the existing caller turns any error into the single `Violation{Field: "spec"}`):
    - any `AgentLabels` key without `strings.HasPrefix(k, agentLabelPrefix)`;
    - `RestartPolicy` not in `"", "no", "on-failure", "unless-stopped"` (exact, case-sensitive, so `No`, `always`, `on-failure:3` fail);
    - `LogConfig.Driver == "" && len(LogConfig.Options) > 0`.
  - New `func reservedLabels(labels map[string]string) []Violation`: collects `Labels` keys with the prefix, sorts them, and returns one `Violation{Field: fmt.Sprintf("labels: %q (only the agent sets labels in the dev.moonglass. namespace; no block can allow)", key), Key: "agent_labels", Value: ""}` per key. Called in `check` right after the well-formedness return, with no reference to `matched`, so no block changes the result. Label values and `Env` are never read.
  - `Cmd`, `Env`, `Networks`, `RestartPolicy`, `LogConfig` are not judged; the existing `Checked{spec: s.clone()}` carries them unchanged.
- `internal/policy/reject.go`: **no code change.** An `agent_labels` violation has an empty `Value` (no snippet line) and a `Field` containing `no block can allow`, so `renderRejection`'s existing `allNever`/empty-`Value` rules print no `[[containers.allow]]` when it is the only kind of violation, exactly as for a host sysctl. When it is mixed with an allowable field, the snippet lists only that field. Nothing in `renderRejection` reads `Spec.Env` or `Spec.Labels`.
- `internal/policy/parse.go`
  - `rawAllow` gains `AgentLabels any \`toml:"agent_labels"\`` so `true`, `false`, a string or a list all decode instead of hitting the strict-decoder type error (position per `fieldalignment`).
  - In `compileBlock`, right after the `service` check and before any allow key is compiled: `if rb.AgentLabels != nil { return fail("agent_labels", "labels in the dev.moonglass. namespace are set only by the agent; agent_labels can never be allowed") }`. Rendered: `containers.allow[0]: agent_labels: … can never be allowed`. Placing it before `blockHasAllowKey` means a block whose only key is `agent_labels` gets this message, not "lists no allow key".
- `internal/policy/compose.go`: in `serviceToSpec`, `if len(svc.Labels) > 0 { s.Labels = maps.Clone(map[string]string(svc.Labels)) }`. `AgentLabels` is never assigned. Keeping nil for no labels keeps `TestComposeBenignProjectPasses` unchanged.
- `testdata/escape/compose/user-agent-label/compose.yaml`: one service `web`, `image: "@IMAGE@"`, `labels: { dev.moonglass.project: other }`; `expect`: `agent_labels`.
- `internal/policy/compose_test.go`: append `"user-agent-label",` as a new line in `requiredFixtures` (no existing line removed), plus `TestCheckComposeFillsLabelsOnly`.
- `internal/policy/check_test.go`: add the `deniedCases` entry `"labels: dev.moonglass. namespace": mut(func(_ layout, s *Spec) { s.Labels = map[string]string{"dev.moonglass.project": "x"} })` (required: `TestCheckDeniesEveryField` fatals on a `DeniedFields` row without one; its `hasField(err, "labels")` and keys `[agent_labels]` checks pass with the field above). The key is shorter than the longest existing key, so gofumpt does not realign other lines. Add the four Check tests below.
- `internal/policy/config_test.go`: `TestParseRejectsAgentLabelsAllowKey`.
- `internal/policy/reject_test.go`: `TestRejectionOmitsEnvironmentValues`.
- `testdata/tests.txt`: `make test-list`.
- `docs/specs/container-policy.md`: unchanged; the behaviour and the `agent_labels` deny row are already specified.

## Tests to write

- `./internal/policy TestCheckCopiesPassThroughFields`: table, one row per field on `baseSpec()`: `Cmd {"migrate","--up"}`, `Env {"A":"1"}`, `Labels {"app.tier":"web"}`, `AgentLabels {"dev.moonglass.project":"monitoring","dev.moonglass.managed":"true"}`, `Networks {"mg-monitoring"}`, `RestartPolicy` `""`, `no`, `on-failure`, `unless-stopped`, `LogConfig{Driver:"json-file"}`, `LogConfig{Driver:"json-file", Options:{"max-size":"10m"}}`. Each: `Check` returns nil error and `reflect.DeepEqual(c.Spec(), s)`, failure message prints got/want. Then one row with all fields set: mutate the input's `Cmd[0]`, `Env`, `Labels`, `AgentLabels`, `Networks[0]`, `LogConfig.Options` after `Check`, and mutate the result of one `Spec()` call the same way; a later `Spec()` still equals a pristine copy built by a constructor func.
- `./internal/policy TestCheckRejectsUserLabelInAgentNamespace`: rows `dev.moonglass.project` and `dev.moonglass.managed`, each with no policy and with `blockTOML("pid_host = true")` (matches project and image): keys are `[agent_labels]`; `Error()` contains the label key and `only the agent sets labels in the dev.moonglass. namespace`, and not `[[containers.allow]]`. Passing rows: `moonglass.dev/x` and `app.tier` labels, `Spec()` DeepEqual to input.
- `./internal/policy TestCheckRejectsAgentLabelOutsideNamespace`: `AgentLabels` `{"app.tier":"web"}` and `{"dev.moonglassx":"1"}`: `r.Violations` has length 1 with `Field == "spec"` and `Key == ""`.
- `./internal/policy TestCheckRejectsMalformedRestartPolicyAndLogConfig`: malformed rows `RestartPolicy` `always`, `on-failure:3`, `No`, and `LogConfig{Options:{"max-size":"1m"}}` → keys `["-"]`, field `spec` (via `runCheckCases`); passing rows `LogConfig{Driver:"local"}` and `LogConfig{Driver:"local", Options:{"max-size":"1m"}}`.
- `./internal/policy TestParseRejectsAgentLabelsAllowKey`: `Parse` of `blockTOML("pid_host = true\nagent_labels = true")` and of `blockTOML("pid_host = true\nagent_labels = [\"dev.moonglass.project\"]")`: error non-nil and contains `containers.allow[0]`, `agent_labels`, and `can never be allowed`; returned policy is nil.
- `./internal/policy TestRejectionOmitsEnvironmentValues`: spec with `Labels{"app.tier":"web","dev.moonglass.project":"x"}`, `Env{"TIER_KEY":"app.tier","DB_PASSWORD":"s3cr3t-pw-9"}`; and the same `Env` with `RestartPolicy "always"`. Both rejected; for each of `app.tier` and `s3cr3t-pw-9`, it is absent from `Error()` and from every `Violation`'s `Field`, `Key`, `Value`.
- `./internal/policy TestCheckComposeFillsLabelsOnly`: two compose files via `writeCompose`, map form `labels: {app.tier: web, team: core}` and list form `labels: ["app.tier=web", "team=core"]`: one `Checked`, `Spec().Labels` DeepEqual `{"app.tier":"web","team":"core"}`, `len(Spec().AgentLabels) == 0`.
- Existing `TestComposeEscapes` runs `user-agent-label` (keys exactly `[agent_labels]`); `TestEscapeSuiteCoversEveryDeniedField` and `TestCheckDeniesEveryField` cover the new row.

## Edge cases

- Several reserved labels: one violation each, keys sorted before appending so `Violations` order is deterministic (map iteration) — `TestCheckRejectsUserLabelInAgentNamespace` checks the keys.
- Reserved label plus an allowable field (`pid: host`): the snippet holds only `pid_host`; the label line has no `Value`. Covered by the `pid_host` block row (passes the block, still rejected for the label).
- Hostile label key with a newline or quote: the key is rendered with `%q`, so it cannot inject lines into the error.
- `dev.moonglassx`, `moonglass.dev/x`, `Dev.Moonglass.x`: prefix match is exact and case-sensitive, matching Docker's case-sensitive label keys and the agent's own exact-match filters; the first two are covered by tests.
- Malformed spec returns before any other check, so a malformed `RestartPolicy` never renders any spec content; covered by the second half of `TestRejectionOmitsEnvironmentValues`.
- `agent_labels = false` or a string in a block: `any` decodes it and the same "can never be allowed" error fires (no boolean-only special case).
- Empty `RestartPolicy` stays empty and nil maps stay nil in `Checked` (no defaulting) — the empty-policy row of `TestCheckCopiesPassThroughFields`.
- Compose with no `labels`: `Labels` stays nil — `TestComposeBenignProjectPasses` unchanged.

## Risks

- `fieldalignment` on `Spec`, `LogConfig`, `rawAllow`: run `make lint` and use the order it reports.
- `tests-kept` gate: only add lines to existing test files (append to `requiredFixtures`, add a `deniedCases` entry); never rewrite an existing line. Check `git diff` for `-` lines in `_test.go` before finishing.
- `Env` field name shadows nothing at compile time (field vs type namespace), but `Spec.Env` must never be passed to `fmt` in package code; reviewer can grep `\.Env` in `check.go`/`reject.go` to confirm only `p.env` appears.
- Snippet suppression relies on the existing `no block can allow` text in `renderRejection`; the label `Field` must keep that phrase, and the test asserting no `[[containers.allow]]` locks it.
- compose-go `svc.Labels` is `types.Labels`; convert with `map[string]string(...)` before `maps.Clone` so the `Spec` field type is exact for `reflect.DeepEqual`.
- Banned words: none of TODO/FIXME/XXX/HACK/fallback/legacy in added lines.
- Verify with `make test-one PKG=./internal/policy NAME=<each test>`, `make test-list`, `make ci`, `scripts/verify check W2-9`.
