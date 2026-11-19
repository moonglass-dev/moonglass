---
verdict: ready
reason: ""
---

# Plan for W2-11c

Prerequisite: the branch must contain W2-11h (`f23fbf9`). That change makes `store.ResolveTarget` return the project id when only the environment is missing. It is on `origin/main` but not yet on `p/W2-11c`, whose merge base is older than #93. Check with `git merge-base --is-ancestor f23fbf9 HEAD`. If that check fails, merge `origin/main` before writing code. `internal/store` is not in `allowed_paths`, so the worker must not touch it.

## Files to change

- `internal/registry/registry.go`
  - `Target` becomes `struct { Project, Environment string }` (names). Doc comment: both empty is an org-level call, which only a `*` row matches.
  - `Call` becomes `struct { OrgID, UserID, ProjectID, EnvironmentID string }` and still has no methods. Doc comment: `Invoke` resolves the ids.
  - New unexported type `resolved struct { project, environment string; projectKnown, environmentKnown bool }`.
  - New `func (r *Registry) resolve(ctx context.Context, orgID string, t Target) (res resolved, notFound error, err error)`:
    - `t == (Target{})`: return `resolved{projectKnown: true, environmentKnown: true}` with no store call. This is the org-level case and matches today's behaviour exactly.
    - Otherwise make exactly one `r.st.ResolveTarget(ctx, orgID, t.Project, t.Environment)` call. The condition is "target not empty", not "Project not empty". A target with only an environment therefore still goes through the store and comes back as an unknown project: `project "" not found` under a `*` row, denied otherwise. It is never treated as org-level.
    - `err == nil`: `resolved{pid, eid, true, true}`.
    - `errors.Is(err, store.ErrNotFound) && pid == ""`: `resolved{}` (both columns unknown), and `notFound = Refuse(ErrNotFound, fmt.Sprintf("project %q not found", t.Project))`.
    - `errors.Is(err, store.ErrNotFound)` with `pid != ""`: `resolved{project: pid, projectKnown: true}`, and `notFound = Refuse(ErrNotFound, fmt.Sprintf("environment %q not found in project %q", t.Environment, t.Project))`.
    - Any other error: `fmt.Errorf("resolve the target of %s: %w", operation, err)`.
  - `scopeAllows(rows []Scope, operation string, res resolved) bool` uses `column(pattern, id string, known bool) bool { return pattern == "*" || (known && pattern == id) }`. `matches` stays for the operation column.
  - `authorise` is replaced by straight-line code in `Invoke`, in this order:
    1. Role check, with the message unchanged. When denied: audit `denied` with `err.Error()` and return. No store read.
    2. `r.resolve(...)`. On a store error: audit outcome `error` with the fixed reason `the target could not be resolved` (no name, no wrapped text), return the wrapped error, and do not run the handler.
    3. Scope match against `res`. When no row matches: return the existing `ErrDenied` text `no scope row of the token covers %s on this target`, audit `denied`, and return. Known out-of-scope targets and unknown targets take this same path, so text, outcome and reason are identical.
    4. `notFound != nil`: audit outcome `error`, reason `not found`, return `notFound`, and do not run the handler.
    5. Destructive gate, unchanged (`ErrPendingApproval`, audited `denied`).
    6. Handler with `Call{OrgID: p.OrgID, UserID: p.UserID, ProjectID: res.project, EnvironmentID: res.environment}`. On a handler error: outcome `error`, reason `handlerReason(err)`, and the error is returned unchanged.
  - A small helper `func (r *Registry) deny(ctx context.Context, p Principal, operation string, err error) error` audits `denied` and returns err, or the audit error. Each of the three denial sites is then one line. `r.audit` stays the only `AppendAudit` caller, which `TestAppendAuditIsCalledOnlyByTheRegistry` checks.
  - `Invoke` doc comment: role check, then one target resolution, then scope, then not found, then the destructive gate; always exactly one audit event.
- `internal/registry/refuse.go` (new):
  - `ErrInvalidInput = errors.New("registry: invalid input")`, `ErrNotFound = errors.New("registry: not found")` and `ErrConflict = errors.New("registry: conflict")`. They are documented as the refusal kinds that `internal/api` maps to 400, 404 and 409.
  - `type refusal struct { kind error; message string }` with:
    - `func (e *refusal) Error() string { return e.message }`
    - `func (e *refusal) Is(target error) bool { return target == e.kind }`
    - No `Unwrap`, so it wraps nothing and matches only its kind.
  - `func Refuse(kind error, message string) error { return &refusal{kind: kind, message: message} }`. Doc comment: kind is one of the three; the message is for the caller, built from inputs and fixed text, never from another error's text.
  - `func handlerReason(err error) string` returns `invalid input`, `not found` or `conflict` by `errors.Is` against the three kinds, and `the handler returned an error` for anything else.
- `internal/registry/registry_test.go`: the owner allows W2-11c to edit this file (`ownerTestEdits` in `tools/pipeline/verify.go`).
  - `TestRoleAndScopeAreEnforced` keeps all twelve cases and its assertions.
  - `proj` becomes `registry.Target{Project: "web", Environment: "prod"}`.
  - Each subtest creates project `web` and environment `prod` in `e.st` (`CreateProject`, `CreateEnvironment`). Before building the principal, it rewrites the table's scope rows through `ids := map[string]string{"p1": project.ID, "e1": environment.ID}`. Patterns not in the map (`*`, `p2`, `e2`, `other.op`) stay as they are.
  - No other line of the file changes.
- `internal/registry/target_test.go` (new): the six named tests and one helper test, listed below. It also defines `countingStore struct { store.Store; calls int; err error }`, whose `ResolveTarget` counts calls and returns `err` when set, and otherwise delegates. Tests that need it build `registry.New(cs, func() time.Time { return t0 })` over `e.st`.
- `docs/specs/registry.md`, "Targets, authorisation and audit": add one bullet. A store failure while resolving the target is not a refusal: the handler does not run, the event has outcome `error` and reason `the target could not be resolved`, and the call is a 500. The spec leaves this case open; no existing sentence changes.
- `testdata/tests.txt`: regenerate with `make test-list`.
- Not changed:
  - `audit_test.go` and `token_test.go`: they only use `registry.Target{}`, which still compiles and still means org-level, and they are not owner-allowed edits.
  - `internal/api/api.go` and `internal/store/`.

## Tests to write

All tests are in `./internal/registry`, file `target_test.go`. They are table-driven with got/want messages, and each subtest uses its own `newEnv(t)`.

- `TestInvokeResolvesTargetAfterRoleCheck`: op `t.op` with MinRole member, and target `{web, prod}` (created).
  - Viewer with a `*` row: `ErrDenied`, `cs.calls == 0`, handler not run, one event with outcome `denied`.
  - Member with a `*` row: nil error, `cs.calls == 1`, handler ran once.
- `TestInvokeUnknownTargetInScopeIsNotFound`, three cases:
  - (a) project `ghost` under a `*` row: message `project "ghost" not found`.
  - (b) environment `ghost` in the known project `web` under a `*` row: message `environment "ghost" not found in project "web"`.
  - (c) the same as (b) under the row `{Project: web.ID, Environment: "*"}`. The known project's id matches the row; this needs W2-11h.
  - Each case checks: `errors.Is(err, registry.ErrNotFound)`, `!errors.Is(err, store.ErrNotFound)`, `err.Error()` equals the message, the handler did not run, and there is exactly one event with outcome `error` and reason `not found`.
- `TestInvokeUnknownTargetOutOfScopeIsDenied`:
  - Create projects `web` and `other`, and use the row `{Project: other.ID, Environment: "*", Operation: "*"}`.
  - Baseline: the known target `web` gives error B and event B.
  - Cases: unknown project `ghost`; unknown environment `ghost` in `web`.
  - Under the row `{Project: web.ID, Environment: prod.ID}`, compare environment `ghost` in `web` with the known `web/stage` (created). Both must get the same denial.
  - Each case checks: `errors.Is(err, ErrDenied)`, `err.Error() == B.Error()`, outcome `denied`, the same reason as the baseline, and the handler did not run.
- `TestInvokePassesResolvedIDsToHandler`: create `web` and `prod`, then compare the whole captured `Call` struct.
  - Project-level `{web, ""}` under the row `{web.ID, "*"}` gives `Call{OrgID, UserID, ProjectID: web.ID, EnvironmentID: ""}`.
  - Environment-level `{web, prod}` under the row `{web.ID, prod.ID}` gives both ids.
  - Org-level `Target{}` under `*` gives empty ids.
- `TestRefusalKindsAndAuditReasons`: a table over `ErrInvalidInput`/`invalid input`, `ErrNotFound`/`not found` and `ErrConflict`/`conflict`.
  - The handler returns `registry.Refuse(kind, "msg for "+name)`.
  - Assert `errors.Is(err, kind)`, `!errors.Is` for the other two kinds, `err.Error() == msg` and `errors.Unwrap(err) == nil`.
  - The event has outcome `error` and the listed reason, and the reason does not contain the message.
- `TestUnmappedStoreErrorIsNotARefusal`: a table over `store.ErrNotFound`, `store.ErrDuplicate` and `store.ErrInvalidName`. The handler returns each as `fmt.Errorf("create project %q: %w", "web", sentinel)`.
  - Assert the returned error matches the store sentinel and none of the three refusal kinds.
  - The event has outcome `error` and reason `the handler returned an error`.
- Helper `TestInvokeResolveStoreFailureIsNotARefusal`: `countingStore{err: errors.New("disk on fire")}`, target `{web, ""}`, a `*` row.
  - Assert the error wraps the injected error and matches none of the refusal kinds or `ErrDenied`.
  - The handler did not run, and there is one event with outcome `error` and reason `the target could not be resolved`. The reason does not contain `disk on fire`.

After the tests: run `make test-list`, `make ci` and `scripts/verify check W2-11c`.

## Edge cases

- Role denial with a named target: no store read (`TestInvokeResolvesTargetAfterRoleCheck`).
- Org-level call: no store read, and it matches only `*` (or an empty pattern) as today. Covered by the org-level cases of `TestRoleAndScopeAreEnforced` and `TestInvokePassesResolvedIDsToHandler`.
- Unknown project with a scoped token: the same denial as a known out-of-scope project, so the token cannot probe for names (`TestInvokeUnknownTargetOutOfScopeIsDenied`, threat model §9.4).
- Unknown environment in a known project:
  - Under a row for that project with a specific environment, it is denied with the same text as a known other environment (`TestInvokeUnknownTargetOutOfScopeIsDenied`).
  - Under a row with `*` for the environment, it is not found (`TestInvokeUnknownTargetInScopeIsNotFound` case c).
- A project name that exists only in another org: `ResolveTarget` runs in `p.OrgID` and returns two empty ids, so it is an unknown project. W2-11h's store conformance tests cover this; the registry never passes another org's id.
- A target with only an environment is resolved as the unknown project `""`, never as org-level, because of the `t == Target{}` condition.
- Hostile names (quotes, newlines): messages use `%q`, so a name cannot break a message. Audit reasons are fixed strings and never carry a name.
- Store failure during resolution: the error is wrapped, is not a refusal, and gets a fixed audit reason (`TestInvokeResolveStoreFailureIsNotARefusal`).
- Audit write failure: unchanged; the audit error is returned (existing tests).
- Agent destructive call on an unknown in-scope target: not found is checked before the destructive gate, so no pending approval is created for a target that does not exist.
- A handler that wraps a refusal with `fmt.Errorf("...: %w", ...)` still gets the refusal's reason. A store sentinel never gets one (`TestUnmappedStoreErrorIsNotARefusal`).

## Risks

- Branch missing W2-11h: case (c) and the known-project denial cases fail. Mitigation: the prerequisite check above, and no change to `internal/store`.
- `tests-kept` gate: only `registry_test.go` is an owner-allowed edit for W2-11c. All new tests go in the new `target_test.go`, and `audit_test.go` and `token_test.go` stay byte-identical.
- `TestAppendAuditIsCalledOnlyByTheRegistry`: `refuse.go` must not call `AppendAudit`; all auditing goes through `r.audit`.
- Size limit of 160 counted lines (tests and docs excluded): `registry.go` changes by about 60 net lines and `refuse.go` is about 35, well under the limit.
- The spec bullet for the resolve-failure reason could read as a behaviour change. It is one added bullet for a case the spec does not cover. It keeps the rule that no message carries the text of a wrapped error, and the fixed text is visible to the reviewer.
- Banned words: none of TODO, FIXME, XXX, HACK, fallback or legacy in code or comments. Avoid "fall back" wording in the org-level comment.
- `errors.Is` semantics: `refusal` is used as a pointer, so its `Is` method is reached. A value-receiver mismatch would make kinds never match, and `TestRefusalKindsAndAuditReasons` would catch it.
- Lint: the refusal type is unexported, and the new sentinels follow the existing `Err…` naming in the file.
