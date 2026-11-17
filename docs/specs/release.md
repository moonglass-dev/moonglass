# Releases: state machine and executor

This is the behaviour spec for `internal/release`: the legal sequence of the deploy commands in `docs/specs/protocol.md` against one app's releases. The machine is pure. It has no Docker, no I/O and no timers. The executor sections below (W2-3, W2-4, W2-6) say what the agent does on the server for each command; they were specified by the weeks 2–3 preflight (owner decisions D4, D8, D9, D11).

Sources: `docs/plan/stack.md` §3.1, `docs/plan/build-plan.md` (week 2).

## State

One `State` per app:

| Field | Meaning |
|---|---|
| `Current` | The release serving traffic, or nil. |
| `Previous` | The release `Rollback` returns to, or nil. |
| `Candidate` | The release being deployed, or nil. |
| `Phase` | `none` exactly when there is no candidate; `pending` while the candidate waits for its pre-deploy outcome; `ready` when it can be switched. |

A release is an `ID` and an `Image`. Invariants: `Previous` is set only if `Current` is set; a release id is unique within an app. The zero `State` is an app with no releases.

## Events

Each event is a method that takes the state by value and returns the new state and an error. On error the returned state equals the input.

| Event | Protocol command | Notes |
|---|---|---|
| `Deploy(id, image, hasPreDeploy)` | `Deploy` | `hasPreDeploy` is a machine input, not a protocol field. |
| `PreDeployDone(id, ok)` | outcome of `RunPreDeploy` | Reported later by the executor (W2-6). |
| `Switch(id)` | `SwitchRelease` | |
| `Rollback()` | `Rollback` | Names the app only. |
| `Abort(id)` | none | The executor gives up on a candidate: its health gate failed (W2-4). |

## Transitions

- `Deploy`: legal with no candidate. Creates the candidate, `ready` if `hasPreDeploy` is false, otherwise `pending`. `Current` and `Previous` do not change.
- `PreDeployDone(id, true)`: legal for the `pending` candidate `id`. The candidate becomes `ready`.
- `PreDeployDone(id, false)`: legal for the `pending` candidate `id`. The deploy aborts: the candidate is cleared and `Current` and `Previous` are untouched, so the old release keeps serving. A new `Deploy` is then legal. `reversible` does not change this (it matters to auto-rollback, W4-3).
- `Switch(id)`: legal for the `ready` candidate `id`. The candidate becomes `Current`, the old `Current` becomes `Previous` (nil if there was none), and the candidate is cleared.
- `Abort(id)`: legal for the candidate `id` in either phase. The candidate is cleared; `Current` and `Previous` are untouched, so the old release keeps serving. A new `Deploy` is then legal. W2-4 adds it to `internal/release`.
- `Rollback()`: legal when `Previous` is set. `Previous` becomes `Current` and `Previous` is cleared; there is no roll-forward, so a new `Deploy` is required. A candidate, if any, and its phase are left untouched.

## Illegal transitions

Anything else returns an error wrapping `ErrIllegalTransition` and leaves the state unchanged. This includes: `Deploy` while a candidate exists (newest-wins supersession is W2-4), `Deploy` with an empty id or an id equal to the current or previous id, `Switch` with no candidate, a pending candidate or a different id, `Rollback` with no previous, and `PreDeployDone` with no candidate, a different id, or a candidate that is not `pending`.

The machine compares ids and rejects the empty one. Id and image formats are checked at the protocol boundary (`docs/specs/protocol.md`), not here.

## Executor: Deploy (W2-3)

`internal/deploy` runs each command against the Docker Engine through the client of ADR-0014, and keeps state in `docs/specs/agent-state.md`. Every result is a `CommandResult` (`docs/specs/protocol.md`).

### Order

1. **Binding.** If the `app_id` is bound to a different project, environment or service, answer `REJECTED`. Nothing else happens.
2. **Transition.** Apply `State.Deploy(release_id, image, has_pre_deploy)` to the stored state in memory. An illegal transition is `REJECTED`, and nothing is touched. (From W3-6 the disk guard can also refuse here: `docs/specs/disk.md`.)
3. **Image.** Resolve the image as under "Images" below. A failure is `FAILED`.
4. **Check.** Build the `policy.Spec` as under "The container" below and call `policy.Check`. A `Rejection` is `REJECTED` with the rendered rejection as `detail`. Nothing has been created.
5. **Network.** Make sure the project network exists (below).
6. **Create** the container from the `Checked` value and nothing else.
7. **Start** it, unless `has_pre_deploy` is true; then it stays created until its pre-deploy step succeeds (W2-6). A started container must still be running when the start call returns, or the step is `FAILED`.
8. **Record.** In one transaction, write the binding (first deploy), the `releases` row with status `candidate`, and the new state. Answer `SUCCEEDED` with `image_id`, `repo_digest` and the app state.

### Images

- **A digest reference** (`name@sha256:…`, with or without a tag) uses the local image when the Engine has it, and pulls otherwise.
- **A tag** is always pulled, so a stale local copy is never deployed.
- **`moonglass.local/<project>/<service>:<release_id>`** is the name of an image built on this server (`docs/specs/build.md`). It is never pulled; when it is missing the step fails with `image <ref> is not on this server; build it again`. Its `<project>` must be the app's own project, or the step is `REJECTED`, so one project cannot run another's build. Tests use the same name for the images they import (`docs/specs/deploy-suite.md`).
- After resolving, the executor inspects the image and records its ID and, for a pulled or digest reference, the repository digest it resolved to (the `RepoDigests` entry for that repository). For a tag this is the digest that actually ran, which `releases.list` shows (owner condition on D4).
- "Load" means only this: use an image already in the local store. The agent never loads an image archive, and no message carries one.
- A pull has a 15-minute limit. Other Engine calls have 60 seconds.

### The container

The `policy.Spec` (`docs/specs/container-policy.md`):

| Field | Value |
|---|---|
| `Kind` | `app` |
| `Project`, `Environment`, `Service` | From the binding |
| `Image` | `<repository>@<repo digest>` when there is a digest, otherwise the image ID. Both are immutable, so the checked image is the image that runs. |
| `Cmd` | Empty: the image's own command and entrypoint |
| `Env` | Empty until secret delivery (W4-8) |
| `Labels` | Empty |
| `AgentLabels` | `dev.moonglass.managed=true`, `dev.moonglass.project`, `dev.moonglass.environment`, `dev.moonglass.service`, `dev.moonglass.app`, `dev.moonglass.release` |
| `Networks` | `mg-<project>` |
| `RestartPolicy` | `unless-stopped` |
| `LogConfig` | driver `local`, `max-size=10m`, `max-file=3` (stack §3.8; the disk guard relies on this cap) |
| everything else | zero: no mounts, no published ports, no capabilities |

The container's name is `mg-<app_id>-<release_id>`. If a container with that name exists and carries `dev.moonglass.managed=true` with the same app and release, it is left over from an interrupted run: it is removed first. A container with that name and any other labels fails the step.

### The project network

One bridge network per project (stack §3.6), named `mg-<project>` and labelled `dev.moonglass.managed=true` and `dev.moonglass.project=<project>`. It is created when missing. A network of that name without those labels fails the step with `network mg-<project> exists and is not managed by Moonglass`. Networks are never removed by the executor.

### Failure

- Nothing that fails before step 6 creates anything. A container created in step 6 is force-removed when step 6 or 7 fails.
- A pulled image is left in the store; the disk guard prunes it (`docs/specs/disk.md`).
- On `FAILED` or `REJECTED` after step 2, a `releases` row is written with status `failed` and the `detail` as `error`, so the history shows it. The release state is unchanged, so the old release keeps serving.
- **Start-up reconcile.** When the agent starts, every container labelled `dev.moonglass.managed=true` whose release is not the current, previous or candidate release of its app is removed, and the stops due by `stop_after` are carried out.
- The Engine API version check and the Engine socket are ADR-0014.

## Executor: Switch, drain and rollback (W2-4)

### Health

- **With a `port` and a `health_path`:** an HTTP `GET http://<container IP on mg-<project>>:<port><health_path>` every second, 2 seconds per probe. A 2xx or 3xx is a pass.
- **With a `port` and no `health_path`:** a TCP connect to that address every second, 2 seconds per attempt.
- **Without a port:** healthy once the container has been running for 10 consecutive seconds with an unchanged restart count (owner condition on D9).
- Healthy means three passes in a row. The gate fails when 60 seconds pass without that, or when the container exits or restarts.

### SwitchRelease

1. Apply `State.Switch` in memory. Illegal is `REJECTED`.
2. Run the health gate on the candidate. On failure: apply `Abort`, stop and remove the candidate container, write its row as `aborted`, and answer `FAILED`. The old release keeps serving.
3. Push the complete proxy config (below) with the candidate routed. On failure, answer `FAILED`; the candidate stays `ready`, and the switch can be sent again.
4. In one transaction: commit the switch, set the new current row `current` and the old current `previous` with `stop_after` = now + 10 minutes, and set the old previous (if any) `retired` with `stop_after` = now + 30 seconds. Answer `SUCCEEDED`.
5. Afterwards, without blocking the result: at `stop_after` a `retired` container is stopped (SIGTERM, 30 seconds before SIGKILL) and removed; a `previous` container is stopped and kept.

**Draining.** Caddy reloads gracefully: requests in flight on the old route finish on it. The proxy's `http` app sets `grace_period` to 30 seconds, so WebSockets on the old route get the 30-second drain window of stack §5.3 and ADR-0001 decision 7, then close.

**Known trade-off, accepted by the owner (D11):** the previous release keeps running, unrouted, for 10 minutes after each switch, so that a rollback is a route push and finishes in under 5 seconds (the G2 gate). For those 10 minutes the server runs two copies of the app and pays for both in memory. On a 2 GB server, an app that uses more than about half the free memory cannot be deployed without risk until that window ends.

### Rollback

1. Apply `State.Rollback` in memory. Illegal is `REJECTED`.
2. If the previous container is running, push the config with it routed. It finishes in under 5 seconds (G2).
3. If it is stopped, start it, run the health gate, then push. This takes as long as the app takes to start.
4. If it is gone, answer `FAILED` with `the previous release's container is gone; deploy again`.
5. In one transaction: commit, set the restored release `current`, and the release rolled back from `retired` with `stop_after` = now + 30 seconds. Answer `SUCCEEDED`.

### Proxy config

- `internal/deploy` never imports `internal/proxy`, which links Caddy (ADR-0011). The `admin` and `storage` values every pushed config must repeat (`docs/specs/proxy.md`) are read from the proxy's running config over the admin socket (`GET /config/admin`, `GET /config/storage`).
- The agent builds the complete Caddy JSON from every app's current release that has domains, and pushes it with `POST /load` over the admin socket (`--proxy-socket`, ADR-0001 decision 3). A route is the release's domains, with the handlers `moonglass_counter` (`release` = the release id) and `reverse_proxy` to `<container IP>:<port>`.
- Certificates: `--tls-issuer acme` (the default, Let's Encrypt production), `acme-staging` (the fleet), or `internal` (tests and CI, as in `docs/specs/proxy.md`).
- A domain that is already routed to another app is `REJECTED` at `Deploy`.
- The agent pushes the config again at start and whenever the proxy's admin socket comes back after a proxy restart.

### Keep five

Per app, the current, the previous and the three newest retired releases keep their images: five in all (stack §3.5). Retired containers beyond the previous one are removed after their drain. When a release falls out of the five, its image is removed (without force; an image still in use is left for the disk guard). The rows follow `docs/specs/agent-state.md`, "Retention".

### One deploy per app

The control plane runs at most one deploy per service and queues at most one more; a newer request replaces the queued one (`docs/specs/registry.md`, `deploy`). The agent does not queue: a `Deploy` while a candidate exists stays illegal, and is `REJECTED`.

## Executor: pre-deploy steps (W2-6)

- A `Deploy` with `has_pre_deploy` makes a `pending` candidate whose container is created but not started.
- `RunPreDeploy` runs one one-off container. Its `policy.Spec` is the candidate's, except: `Kind` `pre_deploy`, `Cmd` = `args`, `RestartPolicy` `no`, and `AgentLabels` add `dev.moonglass.role=pre_deploy`. It is named `mg-<app_id>-<release_id>-pre`, joins the project network, and runs from the candidate's image ID. It gets the environment's secrets from W4-8 on; until then, none.
- The step has a 15-minute limit; over it, the container is killed and the step fails.
- Exit code 0: apply `PreDeployDone(id, true)`, start the candidate container, and answer `SUCCEEDED`. Any other exit code, a kill or an Engine error: apply `PreDeployDone(id, false)`, remove the candidate container, write the row as `aborted`, and answer `FAILED` with `pre-deploy step exited with code <n>; its output is in the logs of container mg-<app_id>-<release_id>-pre`. The old release keeps serving.
- The step's output is never put in the result, which the control plane stores: an app can print its own environment. The one-off container is kept, stopped, so its logs stay readable, until its release is retired.
- `reversible` is stored in `pre_deploy_reversible` for auto-rollback (W4-3). It does not change these steps.

## Tests that need Docker

`AGENTS.md`, "Testing rules", and `docs/specs/deploy-suite.md`. Each test uses a project name unique to its run, so everything it creates (containers, `mg-<project>` networks) carries that `dev.moonglass.project` label and is removed by it afterwards.
