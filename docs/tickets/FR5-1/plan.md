---
verdict: ready
reason: ""
---

# Plan for FR5-1

Context: R-5 finding 1. `SwitchRelease` and `Rollback` route a release through `Proxy.PushRouted` without the cross-app domain check that `Deploy` runs, so two apps can push `terminal` routes for one host. The fix moves the one domain rule into a plain function and runs it inside the `PushRouted` lock before anything is posted. This needs no contract change, ADR or new module: `rejectedError` already exists and maps to `REJECTED`, and `docs/specs/release.md` already defines the rule at Deploy. The fix only extends that rule.

## Files to change

- `internal/deploy/deploy.go`
  - Add the one rule:
    ```go
    // domainConflict is the one rule for who owns a domain: another app's
    // current release in current holds its domains, and appID's own releases
    // never conflict. The first of domains, in the given order, that another
    // app routes is a refusal naming that app.
    func domainConflict(current []state.CurrentRelease, appID string, domains []string) error
    ```
    The body is the owner-map logic moved verbatim out of `checkDomains`. The first owner wins in `app_id` order, the order `CurrentReleases` returns. The function returns `rejected(fmt.Errorf("domain %s is already routed to app %s", domain, other))`.
  - `checkDomains(ctx, appID, domains)` keeps its early return for no domains, keeps reading `x.db.CurrentReleases(ctx)`, and returns `domainConflict(current, appID, domains)`. `Deploy` is unchanged. It still calls `checkDomains` at step 3 with the Deploy's domain order.
  - Add `func outcome(err error) agentv1.CommandOutcome`, which returns `REJECTED` when `errors.As(err, &rejectedError{})` matches and `FAILED` otherwise. `fail` uses it in place of its inline `errors.As`, so there is one mapping from refusal to outcome.
- `internal/deploy/proxy.go`
  - In `push`, right after `current, err := p.db.CurrentReleases(ctx)` and before `withRouted`: when `routed != nil`, `if err := domainConflict(current, routed.Record.AppID, routed.Record.Domains); err != nil { return err }`. The check runs under `p.mu`, which `PushRouted` holds. No route is built and no `POST /load` is sent. `p.last` is not touched.
  - `PushRouted` is unchanged in code. Its existing `if err := p.push(...); err != nil { return err }` already skips `commit`. Update its doc comment to say that a routed domain another app's current release routes is refused before posting, and that `commit` is not run.
- `internal/deploy/switch.go`: the `PushRouted` error branch becomes `return result(outcome(err), err.Error(), st)`. There is no abort and no row write, so the candidate stays `ready`/`candidate` and its container keeps running.
- `internal/deploy/rollback.go`: same one-line change on the `PushRouted` error branch. Nothing is written, and the previous container stays in place.
- `docs/specs/release.md`
  - "SwitchRelease" step 3: inside the `PushRouted` lock and before anything is posted, a candidate domain that another app's current release routes is `REJECTED` with `domain <name> is already routed to app <app_id>`. This is the same check as "Order" step 3. Nothing is posted or committed, the candidate stays `ready` with its row `candidate` and its container running, and the answer carries the stored state. Any other push failure stays `FAILED`.
  - "Rollback" step 4: the same refusal and detail inside the push lock, with nothing posted or committed and the stored state unchanged. The previous release's container stays in place as the app's previous release, as after a failed gate.
  - "Proxy config":
    - Replace the bullet "A domain that another app's current release routes is `REJECTED` at `Deploy`" with: it is refused at `Deploy`, `SwitchRelease` and `Rollback` by one check (`domainConflict`), and `PushRouted` refuses before posting.
    - In the "Pushing" bullet, add that a refused `PushRouted` posts nothing, never runs `commit`, and leaves the last pushed document as it was.

## Tests to write

All five go in a new file, `internal/deploy/domain_owner_test.go`, so no existing test line changes. They use a real Engine through `testEngine`, `testProject` and `newSwitchExecutor` (a `fakeAdmin`), with `x.healthLimit = 20 * time.Second`. They reuse `deployDomains`, `requireDeployOutcome`, `switchMsg`, `rollbackMsg`, `storedApp`, `requireStoredState`, `requireAppState`, `storedRow`/`storedTimes`, `requireRunning`, `singleContainerID`, `pushedRoutes`, `expectedRoute`, `containerIPOn` and `makeCurrent`. Exact-detail checks compare `res.GetDetail()` with `==`.

- `./internal/deploy TestSwitchReleaseRejectsDomainRoutedByAnotherApp`
  - Setup: A and B each deploy `r1` with `[x.example]`, and both deploys are `SUCCEEDED`. Switching A is `SUCCEEDED`.
  - Before B's switch, snapshot B's stored state and `fake.loadCount()`. B's switch is `REJECTED` with detail `domain x.example is already routed to app <A>`.
  - After the refusal:
    - The load count is unchanged.
    - B's stored state equals the snapshot (candidate r1, phase ready, no current), and the result's app state equals it too.
    - `storedRow(B, r1).Status == "candidate"`.
    - `requireRunning(B r1)`.
    - `pushedRoutes(last load)` is exactly `[expectedRoute([x.example], r1, A's dial)]`.
- `./internal/deploy TestRollbackRejectsDomainRoutedByAnotherApp`
  - Setup: A deploys and switches `r1 [x.example]`, then `r2 [y.example]`. B deploys and switches `r1 [x.example]`, and both are `SUCCEEDED`. Deploy allows B because A's current r2 holds only `y.example`.
  - A's rollback is `REJECTED` with detail `domain x.example is already routed to app <B>`.
  - After the refusal:
    - The load count is unchanged.
    - A's stored and result state are current r2, previous r1.
    - The `storedTimes` status of r1 is `previous` and of r2 is `current`.
    - `singleContainerID(A r1)` exists.
- `./internal/deploy TestRollbackStoppedPreviousRejectedForDomain`
  - Same setup, then `ContainerStop` A's r1 before the rollback, so the rollback starts r1 and gates it.
  - The answer is `REJECTED` with the same detail.
  - A's state and rows are unchanged, the load count is unchanged, and `singleContainerID(A r1)` still exists.
- `./internal/deploy TestSwitchReleaseKeepsOwnDomains`
  - Setup: A uses `testAppID(p, "a")` and B uses `testAppID(p, "b")`, so A sorts first. A deploys and switches `r1 [x.example]`. B deploys and switches `r1 [z.example]`. A deploys `r2 [x.example, z2.example]`.
  - A's switch is `SUCCEEDED`.
  - The last pushed routes are `[expectedRoute([x.example z2.example], r2, A r2 dial), expectedRoute([z.example], r1, B r1 dial)]`.
- `./internal/deploy TestProxyPushRoutedRejectsDomainBeforePosting`
  - Setup:
    - A deploys `r1 [x.example]`, and `makeCurrent` makes it A's current release.
    - `x.proxy.Push(ctx)` succeeds. Snapshot `fake.loadCount()` and a copy of `*x.proxy.last.Load()`.
    - Build B's routed release by hand: `state.CurrentRelease{Binding: {p.name, testEnvironment, testService}, Record: {AppID: B, ReleaseID: "r1", Port: 8080, Domains: [x.example]}}`. Deploy would refuse B, so it cannot come from there. The check runs before any route is built, so it needs no container.
  - Call `PushRouted` with a commit closure that sets `committed = true`.
  - Expect:
    - `err.Error() == "domain x.example is already routed to app <A>"`.
    - `errors.As(err, &rejectedError{})` matches.
    - `committed == false`.
    - The load count is unchanged.
    - `bytes.Equal(*x.proxy.last.Load(), snapshot)`.
- Run `make test-list` to add the five names to `testdata/tests.txt`.

## Edge cases

- The app's own current release never conflicts. It is skipped by app id, which is how a switch or rollback replaces its own route. `TestSwitchReleaseKeepsOwnDomains` covers this, and so do the existing switch and rollback tests that reuse one domain.
- A domain list with a conflict that is not first in order: the first conflict in the stored order is named. The existing Deploy case `[y, x]` covers this through the shared function.
- Another app's candidate holds no domain, so both candidates deploy. `TestSwitchReleaseRejectsDomainRoutedByAnotherApp` setup and the existing Deploy case cover this.
- A routed release with no domains skips the check. The existing switch and rollback tests with no domain cover this.
- `Run`/`Push` (routed nil) never runs the check. Existing proxy tests stay unchanged.
- A refused `PushRouted` never runs `commit`, never sets or clears `last`, and posts nothing. `TestProxyPushRoutedRejectsDomainBeforePosting` covers this.
- A non-refusal push error is still `FAILED`. The existing `TestSwitchReleaseRefusalsAndPushFailure` and `TestRollbackRefusalsAndPushFailure` (a 500 from `/load`) cover this.
- A stopped previous release is started and gated, then refused. It is left in place, as after a failed gate. `TestRollbackStoppedPreviousRejectedForDomain` covers this.
- Concurrency: the check reads `CurrentReleases` inside `p.mu`, and every switch, rollback and `Run` push holds that lock, so another app cannot commit between the check and the post.
- Hostile input (§3 B3): the rule reads only agent state and the stored release record, never the message. The detail names only the domain and the app id (§9.3).

## Risks

- `tests-kept`/`tests-locked`: the new tests go in a new file, and no existing test line is edited or removed.
- Diff cap of 150 counted lines: the non-test Go change is about 30 lines. Docs and tests do not count.
- Behaviour drift at Deploy: the owner-map code moves unchanged, and `checkDomains` keeps its early return and read, so `TestDeployRejectsDomainRoutedByAnotherApp` stays green.
- Banned words: none of TODO, FIXME, XXX, HACK, fallback or legacy appear in added Go lines.
- Shared image refs: both apps deploying `r1` in one project import the same `moonglass.local/<p>/web:r1`. The existing Deploy tests already do this, and the project-ownership check passes because the project is the same.
- Gate time: each switch runs the 3-probe gate, about 3 s. With up to four switches per test and `healthLimit` at 20 s, each test stays well within limits.
- Verification: `make test-one PKG=./internal/deploy NAME=<each test>`, `make test-list`, `make ci`, then `scripts/verify check FR5-1`.
