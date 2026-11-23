# Soft delete, restore and purge

The registry operations, the protocol messages, the tables and the agent-state change below are contracts (ADR-0006 decision 9, ADR-0002, `docs/specs/protocol.md`). The decisions are listed at the end.

A deleted thing can be brought back for 7 days, then it is purged. Nobody, a coding agent included, removes data in one step: deleting needs admin and, for an agent, a person's approval (W5-2); purging early needs a person with step-up and is never an agent's. Sources: V§4 ("soft delete" is free everywhere), M§3 week 5, R§3 (Railway's 48-hour soft delete after an agent deleted a production volume).

## Which ticket builds what

| Ticket | Builds |
|---|---|
| W5-5a | `STOP_APP` and `REMOVE_APP` in the protocol |
| W5-5b | The agent running them, and what a stopped app means on the server |
| W5-5c | `services.deleted_at`, `services.purge_detail`, the `purge_due` lookup table (and the audit columns of `docs/specs/audit.md`) |
| W5-5d | `services.delete`, `services.restore`, `services.list` with `deleted` |
| W5-5e | `services.purge` and the retention purge |
| W5-7 | Delete, restore and purge on the service screens (`docs/specs/ui.md`) |
| W5-8a | `deleted_at` on `environments` and `projects`, and the store methods that purge an environment's secrets and keys |
| W5-8 | The delete, restore and purge operations for environments and projects |

## What can be deleted

**Services** (W5-5), including the databases of W6-1, which are services. **Environments and projects** (W5-8), only when they hold nothing live: an environment with a live service, or a project with a live environment, is refused with 409 naming the first one. Nothing cascades, so each delete, restore and purge acts on exactly one row and says so in the audit trail.

Releases, rollbacks, secrets, servers, agent identities and audit events are not soft-deleted. Releases and rollbacks go when their service is purged; secrets and keys when their environment is purged.

## Retention

Seven days, fixed. A deleted row records `deleted_at`; it is purged at `deleted_at` + 7 days by the retention purge, or earlier by a purge call. There is no setting for it.

## A deleted service

On the control plane:
- It is hidden. `services.list` leaves it out unless called with `deleted: true`, which lists only deleted services with `deleted_at`, `purge_after` and `purge_detail`.
- Every operation and route that finds a service by name answers 404 `service <name> not found`, as for an unknown name: `deploy`, `rollback`, `releases.list`, the log and usage streams. `ResolveTarget` and the store's service lookups leave deleted rows out, so there is one rule in one place.
- Its name stays taken. `services.create` with that name is 409 `a deleted service named <name> exists until <purge_after>; restore it or purge it first`.
- Token scope rows that name its environment keep their ids and match again after a restore.

On its server (W5-5b):
- Every container of the app is stopped, and its routes are removed from the proxy, so it serves nothing.
- Its containers, images, agent-state rows and volumes are kept, so a restore loses nothing.
- Auto-rollback (W4-3) ignores it, the start-up reconcile neither starts nor removes its containers, and Docker's `unless-stopped` keeps it stopped across a reboot.

## Operations (W5-5d, W5-5e)

| Ticket | Name | Method, path | `MinRole` | Destructive | StepUp | `AgentCallable` | Target |
|---|---|---|---|---|---|---|---|
| W5-5d | `services.delete` | `POST /v1/services/delete` | admin | yes | no | yes | project, environment |
| W5-5d | `services.restore` | `POST /v1/services/restore` | member | no | no | yes | project, environment |
| W5-5e | `services.purge` | `POST /v1/services/purge` | admin | yes | yes | no | project, environment |

`services.list` gains the input `deleted` (bool, false when absent). Each takes `project`, `environment` and `service`. Delete resolves among live services; restore and purge resolve among deleted ones. Each adds its 8 authz-matrix lines.

**Why these flags.** Soft delete stops a service, and ADR-0006 decision 4 calls stopping a service destructive: it needs admin, and from an agent it waits for a person's approval (W5-2). Purging is irreversible, so it also needs a step-up, which makes it a person's alone. Restoring starts what was stopped and removes nothing, so it is a member's change.

**`services.delete`:**
1. Refused with 409 `service <name> has a deploy in progress; wait for it to finish` while a release of the service is `queued` or `running`, or a rollback is `running`.
2. A service with no release at all is deleted on the control plane alone: no command is sent, and its server need not be online.
3. Otherwise its server must be connected (`server <id> is offline`, as for `deploy`). The control plane sends `STOP_APP` and waits for the result.
4. On `SUCCEEDED`, one transaction sets `deleted_at` and writes the `purge_due` row. On `REJECTED` or `FAILED` the service stays live and the call fails with the agent's `detail`; a server whose `agent.toml` forbids `delete` answers this way (`docs/specs/local-policy.md`).
5. It returns `service`, `deleted_at` and `purge_after`.

If the transaction fails after the agent stopped the app, the service is live but stopped; deleting it again finishes the job, because `STOP_APP` on a stopped app succeeds.

**`services.restore`:**
1. Refused with 409 when its environment or project is deleted: restore that first.
2. A service with no current release is restored on the control plane alone.
3. Otherwise the control plane mints a new release that runs the current release's image again, and drives `DEPLOY` then `SWITCH_RELEASE` as `deploy` does (`docs/specs/registry.md`). The image is the current release's `repo_digest`, or for a git release its `moonglass.local/<project>/<service>:<release_id>` reference; port, health path and domains are the current release's. No pre-deploy step runs: a restore never repeats a migration. From W6-1d the new release also copies the current release's `command`, `volumes`, `labels`, `restart` and `healthcheck`, and from W6-4b its `compose_hash`, so a restored database gets its volume back (`docs/specs/storage.md`). A restore of a service with a volume works because the stopped app runs no container (`docs/specs/release.md`, "One release per volume").
4. When the new release succeeds, one transaction clears `deleted_at` and deletes the `purge_due` row. Otherwise the service stays deleted, and the call fails with the release's `detail`.
5. It returns the service and the new release.

**`services.purge`:**
1. Only a deleted service can be purged; a live one is 409 `delete service <name> first`.
2. A service with no release at all is purged on the control plane alone.
3. Otherwise its server must be connected. The control plane sends `REMOVE_APP` and waits.
4. On `SUCCEEDED`, one transaction deletes the service, its releases, its rollbacks and its `purge_due` row. The audit trail keeps every event. On failure the service stays deleted, `purge_detail` records the agent's `detail`, and the call fails with it.

## The retention purge (W5-5e)

`moonglass server` runs `Registry.PurgeExpired` once an hour. It is not an operation and has no route.

- It reads the `purge_due` rows whose `expires_at` has passed. That lookup needs no org, like a token hash.
- For each, in that row's org, it purges as `services.purge` does from step 2 (W5-8: and the environment and project rules below). Step-up does not apply: the 7-day retention is the policy being carried out, and no person is asking.
- A service whose server is offline is skipped, with `purge_detail` set to `server <id> is offline`, and tried again the next hour. No audit event is written for a skipped purge.
- Every purge it attempts writes one audit event from the registry, with `principal_kind` `system`, empty `user_id`, `agent_id`, `token_id`, `session_id` and `step_up_id`, the purge operation's name, the target ids, and outcome `allowed` or `error` (`docs/specs/audit.md`).

A server that is gone for good keeps its deleted services until it is re-enrolled (W7-2) or removed by a later ticket.

## Protocol (W5-5a)

Two commands, server to agent. Each payload has one field, `app_id`, an identifier as for `Deploy`. No field is a host path or a shell string. Both are `delete` class (`docs/specs/local-policy.md`). The numbers are fixed: `STOP_APP` = 15, `StopApp.app_id` = 1; `REMOVE_APP` = 16, `RemoveApp.app_id` = 1. Later tickets that add messages (W4-8, W3-3, W4-9, W7-6d) take numbers after these.

Each is answered with one `CommandResult` (`docs/specs/protocol.md`). Result timeouts (`docs/specs/connection.md`): `STOP_APP` 5 minutes, `REMOVE_APP` 10 minutes.

## The agent (W5-5b)

**`STOP_APP`:**
- Aborts and removes a candidate, if one is in flight (`Abort` in `release.State`).
- Stops every remaining container of the app (SIGTERM, then SIGKILL after 30 seconds), removes the app's routes from the proxy config, and records `stopped_at` on the app's row in agent state (`docs/specs/agent-state.md` gains the column).
- Keeps every container, image, release row and volume.
- Answers `SUCCEEDED` with the app's state. An app already stopped, or one the agent does not know, also answers `SUCCEEDED`: nothing is left to stop.

**While an app is stopped:** `ROLLBACK` is `REJECTED` with `app <id> is stopped; restore it first`. A `DEPLOY` runs as usual, and the `SWITCH_RELEASE` that makes it current clears `stopped_at`: that is how a restore starts the app again. Auto-rollback and the start-up reconcile leave it alone.

**`REMOVE_APP`:**
- `REJECTED` with `app <id> is not stopped; stop it first` unless the app is stopped. A control plane able to send one command can send both, so this guards against mistakes, not against a hostile control plane; only `forbid = ["delete"]` stops that.
- Removes every container of the app, the images no other app's kept release uses, and the app's agent-state rows and identity binding. Its command records age out after 7 days as usual (`docs/specs/connection.md`).
- Does not remove `data_dirs`. From W6-1c it removes named volumes as below; until then it removes none.
- **Named volumes** (W6-1c). After the containers, it removes each volume `mg.<project>.<environment>.<name>` that a release row of the app names in `volumes`, unless a kept release of another app names it too: two services of one environment may share a volume ("Volumes" in `docs/specs/release.md`), and a shared one stays until the last of them is removed. A volume that is already gone is not an error. A volume the Engine refuses to remove, because a container outside Moonglass uses it, fails the command with `volume <name> is in use by a container Moonglass does not manage`, and the app's state rows are kept so a retried purge can finish. The volumes are read from agent state before its rows are deleted.
- Answers `SUCCEEDED` with an empty app state. An app the agent does not know also answers `SUCCEEDED`, so a retried purge finishes.

## Environments and projects (W5-8)

Control-plane rows only: once their services are deleted, nothing of theirs runs on a server.

| Ticket | Name | Method, path | `MinRole` | Destructive | StepUp | `AgentCallable` | Target |
|---|---|---|---|---|---|---|---|
| W5-8 | `environments.delete` | `POST /v1/environments/delete` | admin | yes | no | yes | project, environment |
| W5-8 | `environments.restore` | `POST /v1/environments/restore` | member | no | no | yes | project, environment |
| W5-8 | `environments.purge` | `POST /v1/environments/purge` | admin | yes | yes | no | project, environment |
| W5-8 | `projects.delete` | `POST /v1/projects/delete` | admin | yes | no | yes | project |
| W5-8 | `projects.restore` | `POST /v1/projects/restore` | member | no | no | yes | project |
| W5-8 | `projects.purge` | `POST /v1/projects/purge` | admin | yes | yes | no | project |

`environments.list` and `projects.list` gain `deleted`, as `services.list` does.

- **Delete** refuses an environment with a live service, or a project with a live environment (409). Otherwise it sets `deleted_at` and writes `purge_due`. The environment's secrets and keys are kept.
- **Restore** refuses an environment whose project is deleted (409). It clears `deleted_at` and deletes `purge_due`. It does not restore children.
- **Purge** refuses while a deleted child is not yet purged (409 `purge or restore its services first`). Purging an environment deletes, in one transaction, its secrets, its wrapped key copies, its environment key and its row. Purging a project deletes its row. After that the secrets cannot be recovered by anyone, the recovery key included, because their key is gone. From W6-2, backups in the user's bucket are encrypted to that key too, so they also become unreadable; W6-2 adds that warning to the purge.

The retention purge handles them the same way. A child is always deleted no later than its parent, so with one retention period it falls due first.

## Storage

**W5-5c:**
- `services` gains `deleted_at` (empty when live) and `purge_detail` (empty unless the last purge attempt failed).
- `purge_due`, a lookup table (ADR-0002 decision 9): `id` (the deleted row's id, the lookup key), `org_id`, `expires_at`. It holds nothing else. It exists so the hourly purge can find due rows without knowing their orgs.
- Store methods: mark a service deleted and write `purge_due`; clear both; list deleted services; list due `purge_due` rows; delete a service with its releases, rollbacks and `purge_due` row in one transaction; set `purge_detail`. Every existing service lookup and list, and `ResolveTarget`, leave deleted services out.

**W5-8a:**
- `environments` and `projects` gain `deleted_at`. `ResolveTarget`, the lists and the lookups leave deleted ones out.
- Store methods: as for services, plus purging an environment with its secrets and keys in one transaction.

Conformance covers: a deleted service is gone from every list and lookup and from `ResolveTarget`, and its name stays taken; restoring brings back the same id; purging removes its releases and rollbacks and frees the name; `purge_due` rows are written, listed when due and removed; a second org sees none of it. W5-8a adds the same for environments and projects, and that purging an environment leaves no secret, key or wrapped copy behind.

## Named tests

- W5-5a: `TestStopAndRemoveAppRoundTrip`, `TestStopAndRemoveAppRejectMalformed`, and a fuzz target for opening both payloads.
- W5-5b: `TestStopAppStopsAndUnroutes`, `TestStopAppIsIdempotent`, `TestDeployRestoresStoppedApp`, `TestRemoveAppNeedsStoppedApp`, `TestRemoveAppRemovesContainersImagesAndState`, `TestStoppedAppIsLeftAloneByReconcileAndAutoRollback`, against a real Engine.
- W5-5c: `TestSoftDeleteConformanceSQLite`.
- W5-5d: `TestServiceDeleteStopsAndHides`, `TestServiceDeleteRefusedDuringDeploy`, `TestServiceRestoreRedeploysWithoutPreDeploy`, `TestDeletedServiceNameIsTaken`, `TestDeleteRejectedByAgentPolicyLeavesServiceLive`.
- W5-5e: `TestServicePurgeNeedsStepUp`, `TestRetentionPurgeAfterSevenDays`, `TestRetentionPurgeSkipsOfflineServer`, `TestRetentionPurgeIsAuditedAsSystem`.
- W5-8: `TestEnvironmentDeleteNeedsNoLiveServices`, `TestEnvironmentPurgeRemovesSecretsAndKeys`, `TestProjectRestoreDoesNotRestoreChildren`.

## Not in W5-5 or W5-8

Deleting servers, secrets, releases or agent identities. Removing named volumes (W6-1c). A configurable retention period. Deleting a service whose server is gone for good.

## Decisions

Each line is a choice no ADR or earlier spec fixed.

- **D54** Services are soft-deleted in W5-5; environments and projects in W5-8, only when empty, with no cascade. Recommended: yes.
- **D55** Retention is 7 days, fixed. Recommended: 7 days. Railway chose 48 hours; 7 covers a weekend and costs only disk for stopped containers.
- **D56** A deleted service is stopped and unrouted on its server, hidden and 404 on the control plane, and its name stays taken. Recommended: yes.
- **D57** Delete and purge are destructive (admin; an agent's delete waits for approval); purge also needs step-up, so no agent can purge; restore is a member's, not destructive. Recommended: yes.
- **D58** A restore redeploys the current release's pinned image as a new release, with no pre-deploy step. Recommended: yes; it reuses `DEPLOY` and `SWITCH_RELEASE`, so only stop and remove are new messages.
- **D59** Delete needs the server online and no deploy in flight; a service that never had a release is handled on the control plane alone. Recommended: yes.
- **D60** Purge removes containers, unshared images, agent state and the service's control-plane rows; named volumes wait for W6-1. Recommended: yes.
- **D61** The control plane purges due rows hourly as a `system` principal, skipping offline servers. Recommended: yes.
- **D62** `STOP_APP` and `REMOVE_APP` are new protocol messages, numbers 15 and 16, both `delete` class, so `forbid = ["delete"]` refuses soft delete too. Recommended: yes.
- **D63** `REMOVE_APP` is refused for an app that is not stopped. Recommended: yes, as a guard against mistakes only.
- **D64** Purging an environment deletes its key and every wrapped copy, the recovery copy included. Recommended: yes; purge means gone. Keeping the recovery copy would keep old backups readable, but leaves key material for an environment that is gone.
