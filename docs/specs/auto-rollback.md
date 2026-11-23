# Auto-rollback

What the agent watches after a switch, when it rolls a release back on its own, what it reports, and what the control plane stores and lets people change. Stack §5.4 gives the intent; this spec turns it into rules. W4-3a to W4-3f build it (`docs/plan/build-plan.md`, Launch 1).

Auto-rollback runs on the server and keeps working while the control plane is unreachable (stack rule 2). The control plane only receives reports, stores them, and holds the per-service settings it sends with each `Deploy`.

## Settings

Each service has these settings. The control plane stores them on the service (`docs/specs/storage.md`), changes them with `services.auto-rollback.set` (below), and sends them in every `DEPLOY` as `Deploy.auto_rollback` (`docs/specs/protocol.md`, "Auto-rollback"). The agent stores them on the release row (`docs/specs/agent-state.md`), so a release is always watched with the settings it was deployed with. **A change takes effect at the next deploy.**

| Setting | Default | Range | Meaning |
|---|---|---|---|
| `off` | false | | True turns off every trigger for the service. |
| `error_rate_enforce` | false | | False is shadow mode: the error-rate trigger only reports. True makes it roll back. This is "one switch per app" of stack §5.4. |
| `restart_limit` | 2 | 1 to 10 | Restarts of the new container that trigger a rollback. |
| `health_fail_seconds` | 30 | 10 to 300 | Seconds of unbroken health-check failure that trigger a rollback. |
| `error_rate_percent` | 5 | 1 to 100 | The 5xx share, in percent, that the new release must exceed. |
| `error_rate_multiple` | 3 | 1 to 100 | How many times the previous release's 5xx share the new release must exceed. |
| `error_rate_min_requests` | 50 | 10 to 100000 | Requests in the window below which the error-rate trigger does not judge (stack §5.4, "low-traffic apps"). |

The watch window (10 minutes) and the error-rate window (2 minutes) are fixed: the window is the time the previous release stays running (`docs/specs/release.md`, D11), and a rollback after it would need a start and a health gate instead of a route push.

A `Deploy` without `auto_rollback` means the defaults. The agent writes the defaults onto the release row, so a stored release always has all seven values.

## What is watched

The **watch** of a release starts when its switch commits (`switched_at`, `docs/specs/agent-state.md`) and ends at the first of:

- 10 minutes after `switched_at`;
- a trigger that fires, whatever its action, except a shadow-mode report (below);
- another switch, rollback, stop or removal of the app.

A release is watched only when all of these hold at the switch: its settings do not have `off`; the app has a `previous` release whose `container_id` is set; the app is not stopped (`docs/specs/delete.md`); and neither the release nor the previous release mounts a named volume (`docs/specs/release.md`, "Volumes"), because `Rollback` refuses such an app. A release restored by a rollback is never watched: a rollback leaves no previous release.

**After an agent restart** the agent resumes the watch of every app whose current release has `switched_at` less than 10 minutes ago and meets the conditions above. The health clock and the error-rate samples start again from empty, and the previous release's rate counts as not measured (below).

## Triggers

The agent checks the watched release's container every second.

- **Restarts.** The Engine's `RestartCount` for the container is at least `restart_limit`. The Engine resets that count to 0 when the container is started, and the health gate fails on any restart before the switch (`docs/specs/release.md`, "Health"), so the count at the switch is 0. A container whose state is `exited` or `dead` triggers too, whatever its count: it is not serving and its restart policy did not bring it back.
- **Health.** Probes in the release's health mode (`docs/specs/release.md`, "Health"): the HTTP or TCP probe every second with 2 seconds per probe, or, with a `healthcheck`, the Engine's reported health every second. The trigger fires when no probe has passed for `health_fail_seconds`, counted from the first failing probe after the last pass. With a healthcheck, a probe passes while the Engine reports `healthy` or `starting`. A release with no port and no healthcheck has no health trigger: the restarts trigger already covers it.
- **Error rate.** Below. Never for an app whose settings have `off`.

At most one trigger fires per watch for restarts or health. The error-rate trigger in shadow mode reports once per watch and the watch goes on, so a later restart or health trigger can still roll back.

### Error rate

- The agent reads `GET /moonglass/counters` over the proxy admin socket (`docs/specs/proxy.md`) every 10 seconds, for its whole life, not only during a watch. It keeps, in memory, the samples of the last 2 minutes for every release the proxy reports. A counter that is lower than in the previous sample means the proxy restarted: every kept sample is discarded.
- **The window** of a release is the difference between its newest sample and its oldest kept sample that is at least 2 minutes older; while no sample is that old, there is no window and the trigger does not judge. `requests` and `5xx` are the differences of those counters.
- **The previous release's rate** is taken when the watch starts: the window of the release that was current until the switch, as the agent's samples hold it at that moment. It is **measured** when that window exists and has at least `error_rate_min_requests` requests; otherwise it is not measured and counts as 0.
- **Fires** when the window has at least `error_rate_min_requests` requests, and `5xx × 100 > requests × error_rate_percent`, and `5xx × previous requests > previous 5xx × requests × error_rate_multiple` (integer arithmetic; with the previous rate not measured or 0 the second test is just `5xx > 0`).
- In shadow mode (`error_rate_enforce` false) it reports with the action `shadow` and does not roll back. Enforced, it acts like the other triggers.

## What happens when a trigger fires

In this order:

1. **Held.** When the app's `auto_rollback_hold` is set (below), nothing is rolled back. The action is `held`.
2. **Irreversible pre-deploy step.** When the release has `has_pre_deploy` and not `pre_deploy_reversible`, nothing is rolled back (stack §5.4, §3.13). The action is `skipped`.
3. Otherwise the agent rolls back through the executor's one rollback path (`docs/specs/release.md`, "Rollback"), under the app's lock, exactly as a `ROLLBACK` command, except that its commit transaction also writes the event and sets the hold (`docs/specs/agent-state.md`). `SUCCEEDED` is the action `rolled_back`. `FAILED` or `REJECTED` is the action `failed` with the executor's `detail`; the stored state is then unchanged, and the hold is not set.

Shadow-mode reports skip these steps: they never roll back, so they are never held or skipped.

**Never twice in a row.** `rolled_back` sets the app's `auto_rollback_hold`. The hold is cleared when a later watch of that app ends at its 10 minutes with no trigger that fired (a shadow report does not count as fired). Nothing else clears it: not a new deploy, not a manual rollback. While it is set, a trigger is reported as `held` and the release keeps serving; a person can still send `rollback`.

## Events and reports

Every fired trigger, with any action, is one **event**, written in agent state (`auto_rollback_events`, `docs/specs/agent-state.md`) and reported to the control plane with `AUTO_ROLLBACK_REPORT` (`docs/specs/protocol.md`). The report carries the numbers: the restart count, the seconds of health failure, and the window and previous counts of the error-rate trigger.

- The event's id is `a` followed by 32 lowercase hex characters from 128 random bits, minted by the agent. It is the report's request id.
- The agent sends a report as soon as its event is written, when it is connected, and after every connect it sends every unacknowledged report, oldest first. The control plane answers each with `AUTO_ROLLBACK_ACK` with the same request id, after storing it. The agent then marks the event reported. A report the control plane already holds is acknowledged again and not stored twice.
- Reported events are deleted 30 days after they were reported. Unreported events are kept.

## The control plane

- **Receiving** (`docs/specs/connection.md`). The server stores a report only for a service of the sending server's org whose `server_id` is that server. A report for any other `app_id` closes the connection, like any other bad frame. For `rolled_back`, the service's `current_release_id` and `previous_release_id` are updated from the report's `app` state in the same transaction, with the write that copies a `CommandResult`'s app state (`docs/specs/storage.md`).
- **Storage**: the service's settings columns and the `auto_rollback_reports` table (`docs/specs/storage.md`, "Auto-rollback").
- **Operations** (`docs/specs/registry.md`, "Auto-rollback"): `services.auto-rollback.set`, `auto-rollbacks.list` and `auto-rollbacks.mark`. A person marks any report `right` or `wrong`; marking again replaces the mark. The marks give the false-positive rate of the defaults (M§6).
- **Notifications** for reports are W4-7.

## Tests

- `internal/rollback` holds the trigger logic as plain functions over observations (container state and restart count, probe results, counter samples, the clock), with no Docker, no socket and no timers, so every rule above is unit-tested with table-driven tests.
- The watcher's wiring runs against a real Engine (`docs/specs/deploy-suite.md`): a release that crash-loops is rolled back within 2 minutes of its switch (gate G5).
