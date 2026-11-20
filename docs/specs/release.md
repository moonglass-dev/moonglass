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
| `Abort(id)` | none | The executor gives up on a candidate: its health gate failed (W2-4b). |

## Transitions

- `Deploy`: legal with no candidate. Creates the candidate, `ready` if `hasPreDeploy` is false, otherwise `pending`. `Current` and `Previous` do not change.
- `PreDeployDone(id, true)`: legal for the `pending` candidate `id`. The candidate becomes `ready`.
- `PreDeployDone(id, false)`: legal for the `pending` candidate `id`. The deploy aborts: the candidate is cleared and `Current` and `Previous` are untouched, so the old release keeps serving. A new `Deploy` is then legal. `reversible` does not change this (it matters to auto-rollback, W4-3).
- `Switch(id)`: legal for the `ready` candidate `id`. The candidate becomes `Current`, the old `Current` becomes `Previous` (nil if there was none), and the candidate is cleared.
- `Abort(id)`: legal for the candidate `id` in either phase. The candidate is cleared; `Current` and `Previous` are untouched, so the old release keeps serving. A new `Deploy` is then legal. W2-4b adds it to `internal/release`.
- `Rollback()`: legal when `Previous` is set. `Previous` becomes `Current` and `Previous` is cleared; there is no roll-forward, so a new `Deploy` is required. A candidate, if any, and its phase are left untouched.

## Illegal transitions

Anything else returns an error wrapping `ErrIllegalTransition` and leaves the state unchanged. This includes: `Deploy` while a candidate exists (newest-wins supersession is W2-4), `Deploy` with an empty id or an id equal to the current or previous id, `Switch` with no candidate, a pending candidate or a different id, `Rollback` with no previous, and `PreDeployDone` with no candidate, a different id, or a candidate that is not `pending`.

The machine compares ids and rejects the empty one. Id and image formats are checked at the protocol boundary (`docs/specs/protocol.md`), not here.

## Executor: Deploy (W2-3)

`internal/deploy` runs each command against the Docker Engine through the client of ADR-0014, and keeps state in `docs/specs/agent-state.md`. Every result is a `CommandResult` (`docs/specs/protocol.md`).

### Order

1. **Binding.** If the `app_id` is bound to a different project, environment or service, answer `REJECTED`. Nothing else happens.
2. **Transition.** Apply `State.Deploy(release_id, image, has_pre_deploy)` to the stored state in memory. An illegal transition is `REJECTED`, and nothing is touched. (From W3-6 the disk guard can also refuse here: `docs/specs/disk.md`. From W6-1c the volume rule refuses here too: "Volumes" below.)
3. **Domains** (W2-4a). A domain of the `Deploy` that another app's current release routes is `REJECTED` with `domain <name> is already routed to app <app_id>`. "Another app's current release" means the release in that app's `current_release_id`; a candidate of another app does not hold its domains, and the app's own releases never conflict with it. This runs after the transition, so the refusal writes a `failed` row like any other after step 2.
4. **Image.** Resolve the image as under "Images" below. A failure is `FAILED`.
5. **Check.** Build the `policy.Spec` as under "The container" below and call `policy.Check`. A `Rejection` is `REJECTED` with the rendered rejection as `detail`. Nothing has been created.
6. **Network.** Make sure the project network exists (below), and from W6-1c every named volume the release mounts ("Volumes" below).
7. **Create** the container from the `Checked` value and nothing else.
8. **Start** it, unless `has_pre_deploy` is true; then it stays created until its pre-deploy step succeeds (W2-6). A started container must still be running when the start call returns, or the step is `FAILED`.
9. **Record.** In one transaction, write the binding (first deploy), the `releases` row with status `candidate`, and the new state. Answer `SUCCEEDED` with `image_id`, `repo_digest` and the app state.

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
| `Cmd` | Empty: the image's own command and entrypoint. From W6-1c, `Deploy.command` when it is set |
| `Env` | Empty until secret delivery (W4-8) |
| `Labels` | Empty. From W6-1c, `Deploy.labels` |
| `AgentLabels` | `dev.moonglass.managed=true`, `dev.moonglass.project`, `dev.moonglass.environment`, `dev.moonglass.service`, `dev.moonglass.app`, `dev.moonglass.release` |
| `Networks` | `mg-<project>` |
| `RestartPolicy` | `unless-stopped`. From W6-1c, `Deploy.restart` when it is set (`no` or `on-failure`) |
| `LogConfig` | driver `local`, `max-size=10m`, `max-file=3` (stack §3.8; the disk guard relies on this cap) |
| `Mounts` | None. From W6-1c, one `volume` mount per `Deploy.volumes` entry ("Volumes" below) |
| `Healthcheck` | None: the image's own. From W6-1c, `Deploy.healthcheck` when it is set |
| `NetworkAliases`, `DNSSearch` | None. From W6-1c, the alias `<service>.<environment>.internal` and the search domain `<environment>.internal` ("Service names" below) |
| everything else | zero: no bind mounts, no published ports, no capabilities |

The container's name is `mg-<app_id>-<release_id>`. If a container with that name exists and carries `dev.moonglass.managed=true` with the same app and release, it is left over from an interrupted run: it is removed first. A container with that name and any other labels fails the step.

### The project network

One bridge network per project (stack §3.6), named `mg-<project>` and labelled `dev.moonglass.managed=true` and `dev.moonglass.project=<project>`. It is created when missing. A network of that name without those labels fails the step with `network mg-<project> exists and is not managed by Moonglass`. Networks are never removed by the executor.

### Failure

- Nothing that fails before step 7 creates anything, except the project network and a named volume, which are kept. A container created in step 7 is force-removed when step 7, 8 or 9 fails.
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
- **With a healthcheck** (W6-1c): when the release's `Deploy.healthcheck` is set and its test is not `NONE`, this mode replaces the three above, whatever `port` and `health_path` say. The agent inspects the container every second. The gate passes the first time the Engine reports its health as `healthy`, and fails when the Engine reports `unhealthy`, when the container exits or restarts, or when 60 seconds pass. The Engine runs the check inside the container, with the timing the `Deploy` gave; the agent never execs into a container. A test of `NONE` turns off the image's own check, and the gate uses the three modes above.

### SwitchRelease

1. Apply `State.Switch` in memory. Illegal is `REJECTED`.
2. Run the health gate on the candidate. On failure: apply `Abort`, remove the candidate container, write its row as `aborted` with the gate's detail as `error` and an empty `container_id`, and answer `FAILED` with the aborted state. The old release keeps serving.
3. Push the complete proxy config (below) with the candidate routed in place of the app's stored current release, through `Proxy.PushRouted`. Inside that push's lock and before anything is posted, a candidate domain that another app's current release routes is `REJECTED` with `domain <name> is already routed to app <app_id>`, the same check as "Order" step 3. Nothing is posted or committed, the candidate stays `ready` with its row `candidate` and its container running, and the answer carries the stored state. Any other push failure is `FAILED`; the candidate stays `ready`, and the switch can be sent again.
4. Commit the switch inside that push's lock, in one transaction: the new current row `current` with `switched_at` = now, the old current `previous` with `stop_after` = now + 10 minutes, and the old previous (if any) `retired` with `stop_after` = now + 30 seconds. Answer `SUCCEEDED` with the new state. A commit that fails is `FAILED` with the stored state unchanged, and the last pushed document is cleared, so `Run` restores the stored routes.
5. Afterwards, without blocking the result: a stop, `SwitchRelease` and `Rollback` of one app hold that app's lock, so a stop never runs between a rollback reading the previous container as running and its commit. At `stop_after` a `retired` container is stopped (SIGTERM, 30 seconds before SIGKILL) and removed, and the release's pre-deploy step container `mg-<app_id>-<release_id>-pre` goes with it; a `previous` container is stopped and kept, so a rollback inside its window only starts it. A stop re-reads its row under the lock and skips a row a switch or rollback changed, so a restored release is never stopped. Every stop or removal is preceded by the check that the container carries `dev.moonglass.managed=true` with the same app and release; a container without those labels is left running, and the stop fails with an error naming the container, the app and the release. A container the Engine no longer has counts as stopped and removed, and a release with an empty `container_id` is recorded the same way. The carried-out stop is written in one transaction that clears `stop_after`, and for a `retired` row also `container_id` (`docs/specs/agent-state.md`, "Rules").

**Draining.** Caddy reloads gracefully: requests in flight on the old route finish on it. The proxy's `http` app sets `grace_period` to 30 seconds, so WebSockets on the old route get the 30-second drain window of stack §5.3 and ADR-0001 decision 7, then close.

**Known trade-off, accepted by the owner (D11):** the previous release keeps running, unrouted, for 10 minutes after each switch, so that a rollback is a route push and finishes in under 5 seconds (the G2 gate). For those 10 minutes the server runs two copies of the app and pays for both in memory. On a 2 GB server, an app that uses more than about half the free memory cannot be deployed without risk until that window ends.

### Rollback

1. Read the app's stored state. An unknown app is `REJECTED` with `app <app_id> has no release on this server; deploy it first`, and nothing else is read or touched. Apply `State.Rollback` in memory; illegal is `REJECTED` with the machine's error. From W6-1c, an app whose current or previous release mounts a named volume is also `REJECTED`, with `app <app_id> mounts a volume; Moonglass does not roll back a service with a volume`, before anything is touched ("Volumes" below).
2. Read the previous release's record. A missing record is `FAILED`. A record with an empty `container_id`, or a container the Engine does not find, is `FAILED` with `the previous release's container is gone; deploy again`; nothing is pushed and the stored state is unchanged.
3. If the container is running, it is neither started nor gated, so the rollback finishes in under 5 seconds (G2). If it is stopped, it is started with the executor's one start path and then the health gate runs on it with the app's project. A failed start or gate is `FAILED` with its detail and the stored state unchanged: nothing is pushed, nothing is committed, and the container is left in place as the app's previous release.
4. The complete proxy config is pushed with the previous release routed in place of the app's stored current release, through `Proxy.PushRouted`, with the commit inside that push's lock. Inside that lock and before anything is posted, a domain of the restored release that another app's current release routes is `REJECTED` with `domain <name> is already routed to app <app_id>`, the same check as "Order" step 3: nothing is posted or committed, the stored state is unchanged, and the previous release's container stays in place as the app's previous release, as after a failed gate. Any other push or commit failure is `FAILED` with the stored state unchanged, and the rollback can then be sent again.
5. The commit is one transaction: the restored release becomes `current` with `switched_at` = now and `stop_after` cleared, so no stop is due on the serving release; the release rolled back from becomes `retired` with `stop_after` = now + 30 seconds; the candidate row, the app's candidate and its phase are untouched. Answer `SUCCEEDED` with the new state.

Auto-rollback (W4-3c) rolls back through these same steps, under the same lock; only its commit also writes the event and the hold (`docs/specs/auto-rollback.md`, `docs/specs/agent-state.md`).

### Proxy config

- `internal/deploy` never imports `internal/proxy`, which links Caddy (ADR-0011). The `admin` and `storage` values every pushed config must repeat (`docs/specs/proxy.md`) are read from the proxy's running config over the admin socket (`GET /config/admin`, `GET /config/storage`).
- The agent builds the complete Caddy JSON from every app's current release that has domains, and pushes it with `POST /load` over the admin socket (`--proxy-socket`, ADR-0001 decision 3). A route is the release's domains, with the handlers `moonglass_counter` (`release` = the release id) and `reverse_proxy` to `<container IP>:<port>`.
- Certificates: `--tls-issuer acme` (the default, Let's Encrypt production), `acme-staging` (the fleet), or `internal` (tests and CI, as in `docs/specs/proxy.md`).
- A domain that another app's current release routes is refused at `Deploy`, at `SwitchRelease` and at `Rollback` by one check (`domainConflict`), and `PushRouted` refuses it before posting.
- **One pusher** (W2-4a). `deploy.NewProxy(engine, db, deploy.ProxyConfig{Socket, Issuer, Listen, OnError})` returns the agent's one `*deploy.Proxy`. `OnError`, optional, receives a push that failed during `Run`. The executor's pusher is that same `*deploy.Proxy`, never a second one, so the switch pushes through the same pusher as `Run` (W2-4b).
- **The document.** `Proxy.Push(ctx)` reads `admin` and `storage` over the admin socket and repeats them exactly, sets `logging` to `{"logs": {"default": {"level": "WARN"}}}` (the level the proxy starts with), and builds `apps` from every app's current release that has domains, in `app_id` order. `apps.http` always carries `grace_period` `"30s"`. When at least one route exists it also carries the server `moonglass` with `listen` `[":443"]` (or `[<Listen>]`, below) and one route per routed app in `app_id` order, each with the release's domains, in stored order, as its `host` match, the `moonglass_counter` handler with the release id, the `reverse_proxy` handler dialling `<container IP>:<port>` on `mg-<project>`, and `terminal` `true`. With no routed app there is no server, so nothing listens on :443; Caddy's automatic HTTPS adds the :80 redirect (the proxy unit binds :80 and :443, `docs/specs/install.md`). A routed current release whose container is gone or has no address on `mg-<project>` is an error naming the app, the release and the container, and nothing is posted.
- **Listen** (W2-7, owner decision of 2026-11-20). `ProxyConfig.Listen` is empty in production, and the document is exactly as above. Only the deploy-correctness suite sets it, to `127.0.0.1:<port>` (`docs/specs/deploy-suite.md`), because a test user cannot bind :443 or :80 and tests take their ports from `:0`. When it is set, the server `moonglass` has `listen` `[<Listen>]` and also `automatic_https` `{"disable_redirects": true}`, so Caddy adds no :80 redirect listener; everything else in the document is unchanged. A `Listen` that is not `host:port` with a numeric port makes every push an error naming the value, before anything is read or posted, as an unknown issuer does. `moonglass agent connect` never sets it.
- **Certificates.** `apps.tls` is `{"automation": {"policies": [{"issuers": [<issuer>]}]}}`, where the issuer is `{"module": "acme"}` for `acme`, `{"module": "acme", "ca": "https://acme-staging-v02.api.letsencrypt.org/directory"}` for `acme-staging`, and `{"module": "internal"}` for `internal`. Only `internal` also sets `apps.pki` to `{"certificate_authorities": {"local": {"install_trust": false}}}`. Any other issuer is an error naming the value and the three accepted ones, before anything is read or posted.
- **Pushing.** Pushes run one at a time; each records the document it posted as the last pushed, and only when `/load` answers 2xx, so a failed push is never the last pushed. A non-2xx answer is an error carrying the method, path, status and the bounded body. A refused `PushRouted` posts nothing, never runs `commit`, and leaves the last pushed document as it was. `Proxy.PushRouted(ctx, routed, commit)` (W2-4b) builds the same complete document with one app's route taken from the given release record instead of its stored current release, then runs `commit` while it still holds the push lock, so a `Run` push cannot read the old current release in between. A failed `commit` clears the last pushed document and returns the commit's error.
- **Run.** `Proxy.Run(ctx)` pushes once at start, then every second `GET`s `/config/` over the admin socket; when that answers and the running config, decoded as JSON, is not equal to the last pushed one decoded the same way, it pushes again. A probe that fails is not an error: the proxy is restarting, and the next probe tries again. `Run` returns only when `ctx` ends. A push that fails during `Run` is reported through `OnError` and retried at the next probe; it never stops `Run`.
- `moonglass agent connect` runs `Proxy.Run` for its whole life (W2-4c, `docs/specs/connection.md`). The switch and rollback push through the same `Proxy` (W2-4b, W2-4d).

### Service settings, volumes and names (W6-1c)

Owner decision of 2026-11-19 (week 6). `Deploy` carries a service's settings (`docs/specs/protocol.md`, "Service settings"). The executor copies each into the `policy.Spec` as "The container" says, records them on the release row (`docs/specs/agent-state.md`), and builds the Engine request from the `Checked` value only. A field the `Deploy` leaves empty keeps the value in "The container".

**Volumes.** Each `Deploy.volumes` entry is a `volume` mount of the Docker volume `mg.<project>.<environment>.<name>`, at its `target`, read-only when `read_only` is set. `<project>` and `<environment>` come from the binding, so a `Deploy` can name only its own environment's volumes. Project and environment names hold no `.`, so the name is never ambiguous. Two apps of one environment that name the same volume share it, as two services of one Compose project do.

- In step 6 of "Order", a missing volume is created with the `local` driver, no driver options, and the labels `dev.moonglass.managed=true`, `dev.moonglass.project`, `dev.moonglass.environment` and `dev.moonglass.volume=<name>`. An existing volume of that name is used only when it carries those four labels with these values, the `local` driver and no driver options. Anything else fails the step with `volume mg.<project>.<environment>.<name> exists and is not managed by Moonglass`.
- A redeploy reuses the volume. Volumes are never removed by a deploy, the start-up reconcile, retention or the disk guard. Only `REMOVE_APP` removes one (`docs/specs/delete.md`).
- A `volume` mount of a plain named volume is never judged by the deny list (`docs/specs/container-policy.md`). That holds here because the agent created the volume itself, with no options that bind a host path.

**One release per volume.** Two containers must never write one volume, so an app that mounts a named volume never runs two releases at once:
- A `Deploy` whose release mounts a named volume, or for an app whose current release mounts one, is `REJECTED` in step 2 with `app <app_id> mounts a volume and is running; Moonglass never runs two releases on one volume`, unless the app has no current release or is stopped (W5-5b). A stopped app's containers are all stopped, so the restore path of `docs/specs/delete.md` still works.
- `Rollback` of such an app is `REJECTED` (above), and auto-rollback (W4-3) leaves it alone, as it leaves a stopped app alone.
- So a database or a Compose service with a volume is never redeployed while it runs. The MVP has no in-place change for one: `services.restore` of the stopped service is its only redeploy, with the same image digest and settings (`docs/specs/delete.md`). The CLI paths that create such services say so (`docs/specs/managed-services.md`, `docs/specs/compose.md`).

**Service names.** Every app container joins `mg-<project>` with the network alias `<service>.<environment>.internal`, and gets the DNS search domain `<environment>.internal`. So an app reaches another service of its environment as `<service>`, or as `<service>.<environment>.internal` from anywhere in the project, and never reaches another environment's service by its bare name. `.internal` is reserved for private use and never delegated, so a name that does not resolve here is not looked up on the internet under a real domain. A pre-deploy step's container gets neither the alias nor the search domain.

**Known trade-off, accepted by the owner (2026-11-19):** the alias names every running container of the service. A candidate in its health gate and the previous release in its 10-minute window (D11) answer on it too, so another service that calls this one directly can reach the old or the new release during a deploy. Traffic through the proxy moves at the switch. A service with a named volume never has two releases running, so a database's name always reaches its one container.

### Keep five

Per app, the current, the previous and the three newest retired releases keep their images: five in all (stack §3.5). Newest means `created_at`, then the order the rows were written, so a tie keeps the later row. A retired row outside those three is deleted only once its stop has run, so no container is orphaned. Retired containers beyond the previous one are removed after their drain. When a release falls out of the five, its image is removed by ID, never by a reference from a message, and without force; an image the Engine still uses, or no longer has, is left for the disk guard. The rows follow `docs/specs/agent-state.md`, "Retention".

### Run

`Executor.Run(ctx, onError)` reconciles and applies retention at start, then carries out the stops due every second and applies retention every hour, until `ctx` ends. A step that fails goes to `onError`, when the caller passed one, and is retried at its next turn; it never stops `Run`. Stops and removals keep the 60-second Engine call limit, and a stop's own wait is 30 seconds. `moonglass agent connect` runs `Executor.Run` for its whole life from W2-10b.

### One deploy per app

The control plane runs at most one deploy per service and queues at most one more; a newer request replaces the queued one (`docs/specs/registry.md`, `deploy`). The agent does not queue: a `Deploy` while a candidate exists stays illegal, and is `REJECTED`.

## Executor: pre-deploy steps (W2-6)

- A `Deploy` with `has_pre_deploy` makes a `pending` candidate whose container is created but not started.
- `RunPreDeploy` runs one one-off container. Its `policy.Spec` is the candidate's, except: `Kind` `pre_deploy`, `Cmd` = `args`, `RestartPolicy` `no`, and `AgentLabels` add `dev.moonglass.role=pre_deploy`. It is named `mg-<app_id>-<release_id>-pre`, joins the project network, and runs from the candidate's image ID. It gets the environment's secrets from W4-8 on; until then, none.
- The step has a 15-minute limit; over it, the container is killed and the step fails.
- Exit code 0: apply `PreDeployDone(id, true)`, start the candidate container, and answer `SUCCEEDED`. Any other exit code, a kill or an Engine error: apply `PreDeployDone(id, false)`, remove the candidate container, write the row as `aborted`, and answer `FAILED` with `pre-deploy step exited with code <n>; its output is in the logs of container mg-<app_id>-<release_id>-pre`. The old release keeps serving.
- The step's output is never put in the result, which the control plane stores: an app can print its own environment. The one-off container is kept, stopped, so its logs stay readable, until its release is retired.
- `reversible` is stored in `pre_deploy_reversible` for auto-rollback (W4-3). It does not change these steps.
- **Order.** An unknown app is `REJECTED` before anything is read. The transition is checked next, so a release that is not the pending candidate, a candidate deployed without `has_pre_deploy` and a second `RunPreDeploy` are `REJECTED` with the stored state unchanged. Only then are the candidate's record read and the step's spec checked.
- **Policy rejection.** A `Rejection` of the step's spec is `REJECTED` with the rendered rejection as `detail`: nothing is created and no state or row is written.
- **Leftover step container.** A container already at `mg-<app_id>-<release_id>-pre` follows the candidate's leftover rule: one carrying `dev.moonglass.managed=true` with the same app and release is removed first, and any other container fails the step and is left in place.
- **Candidate start.** A candidate that fails to start after exit 0 aborts like an Engine error: the candidate is removed, the row is `aborted` and the answer is `FAILED`.
- Every Engine call other than the wait keeps the 60-second limit; the wait holds the step's 15-minute limit and kills the container over it.

## Tests that need Docker

`AGENTS.md`, "Testing rules", and `docs/specs/deploy-suite.md`. Each test uses a project name unique to its run, so everything it creates (containers, `mg-<project>` networks) carries that `dev.moonglass.project` label and is removed by it afterwards.
