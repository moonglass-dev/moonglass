---
verdict: ready
reason: ""
---

# Plan for W2-11h

The spec (`docs/specs/storage.md`, "`ResolveTarget`") already gives the four-row table, so this ticket changes no contract and no ADR. The signature, the single `inOrg` transaction, the queries and the error text all stay as they are. The only change is what comes back on one error path.

## Files to change

- `internal/store/projects.go`, `(*SQLStore).ResolveTarget`: add a local `envMissing bool` next to `projectID, environmentID`. In the `sql.ErrNoRows` branch of the environment select, set `envMissing = true` before returning the existing wrapped `ErrNotFound`. After `inOrg`:
  ```go
  if err != nil {
      if envMissing {
          return projectID, "", err
      }
      return "", "", err
  }
  ```
  The flag is set only on that branch, and that branch returns straight away, so `inOrg` hands back the closure's own error unchanged: the deferred `Rollback` result is discarded and `Commit` never runs. Every other failure keeps returning two empty ids: an unknown project, a failed project select, a failed environment select, a failed begin or org bind, and a failed commit on the success path (where `envMissing` is false). `projectID` was scanned from a row read under the caller's org (the `org_id = $1` predicate on both backends, plus RLS on Postgres), so it can only be the caller's own project. Update the doc comment to `// ResolveTarget implements Store. When the project exists and the named environment does not, it returns the project id with ErrNotFound.` That is about 8 changed lines.
- `internal/store/store.go`, the `ResolveTarget` interface comment: replace the three comment lines with the four cases from the spec table:
  ```go
  // ResolveTarget resolves project and environment names to their ids in one
  // transaction bound to orgID. Both exist: both ids, or an empty environment
  // id when environment is empty, and nil. The project exists but the named
  // environment does not: the project id, an empty environment id and
  // ErrNotFound. The project is not the org's: two empty ids and ErrNotFound.
  // Any other failure: two empty ids and the wrapped error.
  ```
  The signature does not change. That is about 6 changed lines, for a total well under `max_diff_lines: 60`. Test files and docs do not count toward the cap.
- `docs/specs/storage.md`: no change. It already describes the built behaviour, names both tests and says W2-11h adds them.
- `testdata/tests.txt`: regenerate with `make test-list`.

## Tests to write

Write these first. Both fail before the code change, because `("web", "nope")` returns `("", "", ErrNotFound)`.

New file `internal/store/resolve_target_conformance_test.go`, package `store_test`, so no existing test file is edited:

- `./internal/store TestResolveTargetReportsKnownProjectSQLite`: `s, _ := newSQLite(t); runResolveTargetConformance(t, s)`.
- `./internal/store TestResolveTargetReportsKnownProjectPostgres`: `runResolveTargetConformance(t, newPGEnv(t, true).openApp(t))`. `newPGEnv` (`helpers_test.go`) already skips when `MOONGLASS_TEST_POSTGRES` is empty and fails under `CI`.
- `func runResolveTargetConformance(t *testing.T, s store.Store)`:
  - Setup:
    - `a := seedTarget(t, s)` gives org A with project `web` (`a.project.ID` is the id `CreateProject` returned) and environment `prod` (`a.env.ID`).
    - `b, _ := seedOrg(t, s)`, then `mustCreateProject(t, s, b, "b-only")`. Reuse the helpers in `projects_conformance_test.go`.
  - A table of `{name, project, environment, wantProject, wantEnv string; wantNotFound bool}`, with these rows asked as org A:
    - `web/prod` → `a.project.ID, a.env.ID`, nil
    - `web/""` → `a.project.ID, ""`, nil
    - `web/nope` → `a.project.ID, ""`, ErrNotFound
    - `nope/prod` → `"", ""`, ErrNotFound
    - `nope/""` → `"", ""`, ErrNotFound
    - `b-only/""` → `"", ""`, ErrNotFound
    - `b-only/prod` → `"", ""`, ErrNotFound
  - For each row, call `s.ResolveTarget(ctx, a.orgID, project, environment)`. Check the ids for equality, check `errors.Is(err, store.ErrNotFound)` when `wantNotFound`, and check `err == nil` otherwise. On any mismatch, fail with one message that shows the actual and expected triples: `ResolveTarget(%q, %q) = (%q, %q, %v), want (%q, %q, %s)`, where the last value is `nil` or `ErrNotFound`.
- `runProjectsConformance` and every other existing test stay as they are.

## Edge cases

- Unknown project with and without an environment: two empty ids and ErrNotFound (rows `nope/prod`, `nope/""`).
- A project name that exists only in another org, with and without an environment: two empty ids, so another org's id is never returned (rows `b-only/""`, `b-only/prod`). On Postgres this also exercises RLS, because the app role is used.
- Unknown environment in a known project: the project id, an empty environment id and ErrNotFound (row `web/nope`), with the error still matching `ErrNotFound` through `%w`.
- An empty environment name: the project only, with nil error (row `web/""`). This path is unchanged.
- An environment name that exists only in another org's project: the existing `runProjectsConformance` case (`web`, `b-only-env`) keeps asserting only ErrNotFound, and it still passes because the error is unchanged.
- Database, bind or commit failures: they return before `envMissing` is set, or with it false, so they give two empty ids. No new test is needed, because the spec's "any other failure" row is today's behaviour and the code path is untouched.

## Risks

- **Coverage gate on added lines.** The `envMissing` branch is covered by `web/nope`, and the empty-id branch by `nope/prod`, on SQLite in every run.
- **Callers that relied on empty ids together with an error.** Only `internal/store` calls `ResolveTarget` today (registry use arrives in W2-11c), and every caller checks `err` first, so nothing changes for them.
- **Review of trusted core (`internal/store`).** The plan keeps one transaction, one query per name and no new SQL. The returned id comes from a row read under the caller's org, which satisfies threat model §3 B2 and §9.5. The flag keeps the "project found" signal explicit, which is clearer than inferring it from a non-empty `projectID`. Inferring it would break if a future change set `projectID` before a later failure.
- **Banned words or placeholders.** None are needed. Comments describe the behaviour only.
- **Protected or locked test files.** Only a new `_test.go` file is added, so `removedTestLines` is not triggered.
