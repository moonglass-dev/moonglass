# Agent self-update, release channels and staged rollout

This is the behaviour spec for agent self-update. It turns ADR-0007 decisions 1 to 6, `docs/plan/stack.md` §3.14 and `docs/threat-model.md` §9 checklist items 6 and 7 into behaviour that W7-6c to W7-6f can name acceptance tests against: W7-6c builds the release workflow (§9), W7-6d the messages (§1, §6), W7-6e the agent side, the units and the install script (§2 to §7), and W7-6f the channels and the rollout (§8). The owner approves this spec before it merges, because it fixes the shape of those changes. The signature verifier it names is W7-6a (`internal/update`).

Where ADR-0007 fixes a name, a number or a rule, this spec repeats it. Where it does not, the choice is listed in §10.

Every rule below cites its source: ADR-0007 decision `dN`, `docs/plan/stack.md` §`n`, `docs/threat-model.md` §`n` and checklist `cN`, or the spec behind it.

## 1. Discovery

A release is one version of the binary. Canary and stable do not have separate builds: a channel is a signed pointer to a version (ADR-0007 d2).

### Version

`vMAJOR.MINOR.PATCH`, ordered by semver. Each published release has exactly one version, and it appears unchanged in the pointer, the URL layout (§2) and every report (§6).

### Channel pointer

`https://moonglass.dev/releases/channels/<channel>` and `<channel>.sig`, with `<channel>` `canary` or `stable`. It is signed exactly like an artifact (`moonglass-release`, `sha512`, either pinned key, flags 0x01 and 0x04; ADR-0007 d1).

It has exactly four lines, in this order, each ending in one `\n`:

```
channel <channel>
version <vX.Y.Z>
linux-amd64 <64 lowercase hex sha256 of moonglass>
linux-arm64 <64 lowercase hex sha256 of moonglass>
```

Its maximum size is 1 KiB. Any other content, order, line, field or duplicate is rejected: the pointer is only accepted when all four lines are present, the channel line equals the channel it was fetched for, and the version and both hashes are well formed. The pointer binds the channel, the version and the two binaries, so a signed binary of one version or channel cannot be served for another.

### Polling

- **The control plane** (hosted and self-hosted) fetches and verifies both pointers at start and then every 10 minutes, with the W7-6a verifier. A pointer that does not verify is ignored: the control plane offers nothing for that channel until a later poll succeeds.
- It learns each agent's channel from a new `Heartbeat` field, `update_channel`, set by W7-6d. The values are `"STABLE"` or `"CANARY"` from `update.channel` in `agent.toml` (`docs/specs/container-policy.md`; ADR-0004). The field is empty when the agent predates W7-6d or `update.channel` is unconfigured, and such an agent is never sent an `UpdateOffer`. Any other non-empty value is rejected. The field carries no `UNSPECIFIED` value; the zero value is empty. The control plane cannot change that file (ADR-0004 d5).
- It sends an `UpdateOffer` only to an agent whose `agent_version` is older than its channel pointer's version, at most one offer per server per version per hour, and none for a version after that server reports `PASSED` or `REVERTED` for it. An offer's only field is `version` (§6), so it can name no URL, path, channel or older version.
- **Hosted** (the server is started with `--release-org ORG_ID`, §8): offers follow the rollout state of §8.
- **Self-hosted** (no `--release-org`): every connected agent on the channel is offered the pointer's version at once, with no percentage rollout, no rollout rows and no halt of ADR-0007 d3 (ADR-0007 d5). The criteria of §8 do not apply.
- **The control plane never replaces itself** (ADR-0007 d5, TM §8). The operator updates `moonglass server` and the CLI by running the signed install (`docs/specs/install.md`).

### What the agent does with an offer

The agent never installs a release it was not offered, and it treats every offer as untrusted (ADR-0007 d5; TM §3, the ninth boundary; TM B3). Before it downloads anything it fetches its own channel's pointer from the fixed origin `https://moonglass.dev` — compiled into the binary, never taken from a message — and verifies it. It accepts the offer only when every condition below holds, checked in this order:

| # | Condition | Reason when it does not hold |
|---|---|---|
| 1 | the offered version is strictly greater than the agent's own version | `NOT_NEWER` |
| 2 | the offered version equals the version line of the agent's channel pointer | `NOT_ON_CHANNEL` |
| 3 | the pointer verified and its `linux-<arch>` hash is not a line of `releases/reverted` | `POINTER`, or `REVERTED_BEFORE` for the hash |
| 4 | no trial is in progress (`releases/trial` has no state `trial`, §5) | `TRIAL_IN_PROGRESS` |

Otherwise it sends one `UpdateReport` with outcome `REJECTED`, that reason and an empty `sha256`, and downloads nothing.

A hostile control plane can therefore only delay an update or offer the release the signed pointer already names. It cannot get an unsigned binary installed, and it cannot name a URL, a path, a channel or an older version (TM §2, TM §5).

## 2. Artifact URL layout

```
https://moonglass.dev/releases/<version>/linux-<arch>/<artifact>
https://moonglass.dev/releases/<version>/linux-<arch>/<artifact>.sig
https://moonglass.dev/releases/latest/linux-<arch>/<artifact>
https://moonglass.dev/releases/latest/linux-<arch>/<artifact>.sig
https://moonglass.dev/releases/channels/<channel>
https://moonglass.dev/releases/channels/<channel>.sig
```

`<arch>` is `amd64` or `arm64`. `<version>` is the `vX.Y.Z` of §1.

| Artifact | Content |
|---|---|
| `moonglass` | the static binary (`CGO_ENABLED=0`, linux, that arch) |
| `SHA256SUMS` | the `sha256sum` format; one line per other artifact in that directory |
| `moonglass.sbom.json` | the SPDX JSON SBOM of §9 |
| `moonglass-agent.service` | the agent unit from `deploy/systemd/` |
| `moonglass-proxy.service` | the proxy unit from `deploy/systemd/` |
| `moonglass-agent-revert.service` | the revert unit of §5 |
| `moonglass-agent-revert.timer` | the revert timer of §5 |

The first five are the artifact set of `docs/specs/install.md` ("Artifacts and verification"); the two revert units join it because they are installed and signed the same way (§4).

- `releases/latest/linux-<arch>/` is a byte-identical copy of the directory of the version the **stable** pointer names. It is replaced on each promotion to stable. Canary never moves `latest`.
- `https://moonglass.dev/releases/latest` is what `scripts/install` uses when `--from` is not given. From W7-6e the default becomes `https://moonglass.dev/releases/latest/linux-<arch>`, with `uname -m` `x86_64` mapped to `amd64` and `aarch64` to `arm64` and any other value an error (§4). The install default therefore always resolves to the stable channel; a canary server is installed from an explicit `--from` directory.
- The agent fetches only `moonglass`, `SHA256SUMS` and their `.sig` files. Unit files change only through the signed install, so a unit is never replaced by the agent (W7-6e, §4).

## 3. Verification before install

The steps run in this order, and each failure has one fixed reason code. Nothing executes a staged file.

| # | Step | Reason on failure |
|---|---|---|
| 1 | Recreate `/var/lib/moonglass/releases/staging/` (mode 0700, owner `moonglass`) empty | `WRITE` |
| 2 | Require at least 3 × 128 MiB free on the filesystem holding it | `DISK` |
| 3 | Download over HTTPS with the system roots and TLS 1.2 or later, following no redirect, within a 10-minute budget. Size caps: `moonglass` 128 MiB, `SHA256SUMS` 64 KiB, each `.sig` 4 KiB | `DOWNLOAD`, or `TOO_LARGE` |
| 4 | Verify `SHA256SUMS.sig` and `moonglass.sig` with the W7-6a verifier: `SSHSIG` preamble, version 1, either of the two pinned keys, namespace `moonglass-release`, hash `sha512`, and flags 0x01 and 0x04 both set | `SIGNATURE` |
| 5 | The SHA-256 of the staged `moonglass` equals its `SHA256SUMS` line, and equals the pointer's `linux-<arch>` line | `CHECKSUM`, or `POINTER_MISMATCH` |
| 6 | `mkdir releases/<sha256>` (0755), rename `staging/moonglass` to `releases/<sha256>/moonglass`, `chmod 0755`, and fsync the file and its directory | `WRITE` |

On any failure the agent empties `staging/`, leaves `current` and `previous` untouched, keeps running, and sends one `UpdateReport` with outcome `VERIFY_FAILED` and that reason (§6). It does not retry the same version until the control plane offers it again (§1).

A binary that is signed but belongs to another version or arch fails step 5 with `POINTER_MISMATCH`, because the pointer names both hashes and the `SHA256SUMS` line names the artifact.

## 4. Binary layout under `ProtectSystem=strict`

Both units run with `ProtectSystem=strict`, so `/usr` is read-only and the binary a role runs must live somewhere its unit may execute from and the agent may replace. That is `/var/lib/moonglass/releases`, reached through two symlinks.

| Path | Type, mode | Owner | Written by |
|---|---|---|---|
| `/var/lib/moonglass` | dir 0755 | root | install |
| `/var/lib/moonglass/releases` | dir 0755 | `moonglass:moonglass` | install creates; the agent writes inside it |
| `releases/<sha256>/` | dir 0755 | `moonglass` | the agent (install for the first one) |
| `releases/<sha256>/moonglass` | file 0755 | `moonglass` | the agent (install for the first one) |
| `releases/current` | symlink to `<sha256>/moonglass`, relative | `moonglass` | the agent, by symlink-and-rename; install |
| `releases/previous` | symlink to `<sha256>/moonglass`, relative | `moonglass` | the agent, by symlink-and-rename; install |
| `releases/staging/` | dir 0700 | `moonglass` | the agent |
| `releases/trial` | file 0600, JSON, `"format": 1` | `moonglass` | the agent and the gate (§5, §6) |
| `releases/reverted` | file 0600, one SHA-256 hex per line | `moonglass` | the gate (§5) |
| `/usr/local/bin/moonglass` | file 0755 | root | install only; the CLI and `moonglass server` |
| `/var/lib/moonglass/agent` | dir 0700 | `moonglass` | the agent's enrolment and state files; unchanged |
| `/etc/moonglass/agent.toml` | file 0644 | root | the operator only; unchanged |

- Staging lives inside `releases/` so the final `rename` stays on one mount. A rename across systemd's per-path bind mounts fails with `EXDEV`.
- Only `current`, `previous` and the binary a trial is testing exist. Other version directories are deleted when a trial passes (§6), after a revert (§5) and by install (§4).
- **The proxy user cannot write any path the agent executes.** `releases/` and every version directory are mode 0755 owned by `moonglass`; `moonglass-proxy` is neither the owner nor in a group with write access, so it can read and execute `releases/*` and write none of it. It cannot read `trial`, `reverted` or `staging/` (0600, 0600, 0700, all owned by `moonglass`), and `/var/lib/moonglass/agent` (0700) stays closed to it (`docs/specs/install.md`).
- `ProtectSystem=strict` stays on both units. The only writable path this spec adds is `ReadWritePaths=/var/lib/moonglass/releases` on the agent unit. The proxy gets no writable path.
- **Neither unit gains a capability.** The agent keeps an empty `CapabilityBoundingSet` and no `AmbientCapabilities`; the proxy keeps `CAP_NET_BIND_SERVICE` only. The gate's `SIGKILL` of §5 needs no capability, because the agent and the revert unit run as the same uid.

### Changes W7-6e makes, each with its reason

| File | Change | Reason |
|---|---|---|
| `deploy/systemd/moonglass-agent.service` | `ExecStart=/var/lib/moonglass/releases/current/moonglass agent connect --data-dir /var/lib/moonglass/agent --url ${MOONGLASS_URL}` | the unit runs the switchable binary |
| `deploy/systemd/moonglass-agent.service` | `ExecStartPre=/var/lib/moonglass/releases/previous/moonglass agent update-gate --releases /var/lib/moonglass/releases` | the last good binary decides before each start whether to revert |
| `deploy/systemd/moonglass-agent.service` | `ReadWritePaths=/var/lib/moonglass/releases` | the agent writes staging, version directories, the symlinks and the trial; the only writable path added |
| `deploy/systemd/moonglass-agent.service` | `Wants=moonglass-agent-revert.timer` | the revert timer runs whenever the agent does |
| `deploy/systemd/moonglass-proxy.service` | `ExecStart=/var/lib/moonglass/releases/current/moonglass proxy --data-dir /var/lib/moonglass/proxy` | one binary on disk serves both roles (ADR-0007 d6) |
| new `deploy/systemd/moonglass-agent-revert.service` | the oneshot of §5 | the deadline kill must not depend on the agent it kills |
| new `deploy/systemd/moonglass-agent-revert.timer` | the timer of §5 | polls the deadline every 15 s |
| `scripts/install` | default `--from` becomes `https://moonglass.dev/releases/latest/linux-<arch>`, mapping `uname -m` as above | artifacts are per arch |
| `scripts/install` | `ARTIFACTS` gains `moonglass-agent-revert.service` and `moonglass-agent-revert.timer` | they are signed like the others |
| `scripts/install` | new `--state-dir DIR`, default `/var/lib/moonglass` | tests can check the layout in a temporary directory |
| `scripts/install` | write `releases/<sha256>/moonglass`, set `previous` to the old `current` (or to the new binary on first install), set `current` to the new binary, delete `trial` | the units run from `releases/` |
| `scripts/install` | host-setup mode only: `chown -R moonglass:moonglass releases` | the agent must own what it replaces |

## 5. The systemd revert

The old agent A installs B, then A switches and exits; systemd starts B; if B is not good, the gate decides at B's next start, and the revert timer makes sure there is a next start.

### The switch

1. A writes `releases/trial`: `request_id`, `version`, `sha256`, `previous_sha256`, `switched_at` (RFC3339 UTC), `deadline` (`switched_at` + 5 minutes), `starts` 0, `state` `trial`, and `proxy` (the revision and `started_at` the proxy reported, §7).
2. A points `previous` at its own binary and `current` at B's, each by writing a temporary symlink and renaming it over the old one.
3. A sends `UpdateReport` with outcome `SWITCHING` and the offer's `request_id`, waits up to 10 s for the next `HeartbeatAck`, and exits with status 75. `Restart=on-failure` starts B.

Exit 75 is deliberate for both restarts in this spec (A's hand-off and the proxy's restart, §7): it is non-zero, so `Restart=on-failure` restarts the unit, and it is distinct from every failure status the binary uses.

### The five-minute window

The window is measured from `switched_at`, the instant before the `current` rename, to `deadline`. It is a wall-clock deadline stored in `trial`, so it survives the restarts it measures (ADR-0007 d4).

### What counts as "not restarted"

The agent unit has started exactly once since the switch. The gate counts starts in `trial.starts`, and the self-check requires `starts == 1`. A restart is therefore any second start, whether it follows a crash, a failed exec or `systemctl restart`; systemd's own `NRestarts` is not read, because a unit that was restarted by a reboot or by an operator looks the same to the agent, and a counter in the trial file is observable by a test (ADR-0007 d4).

### `moonglass agent update-gate`, the `ExecStartPre` of the agent unit

It runs from `previous`, so the binary that decides is the last one that was good, never the one under trial.

| `trial` | Condition | Action |
|---|---|---|
| absent | — | exit 0 |
| state `trial` | `starts == 0` and now is before `deadline` | set `starts` to 1, exit 0 |
| state `trial` | `starts ≥ 1`, or now is at or after `deadline` | revert, then exit 0 |
| state `passed` | — | increment `starts`, exit 0 |
| state `reverted` | — | exit 0 |

Reverting, in this order:

1. point `current` at `previous`;
2. set state `reverted` with a reason: the one B recorded, otherwise `RESTARTED` when `starts ≥ 1`, otherwise `DEADLINE`;
3. delete B's version directory;
4. append B's SHA-256 to `releases/reverted`;
5. exit 0, so A starts.

### `moonglass-agent-revert.timer` and `.service`

The timer:

```
[Timer]
OnActiveSec=15s
OnUnitActiveSec=15s
AccuracySec=1s
```

So a hung B is killed at most 16 s after the deadline.

The service is `Type=oneshot`, `User=moonglass`, and runs

```
ExecStart=/var/lib/moonglass/releases/previous/moonglass agent revert-check --releases /var/lib/moonglass/releases
```

Its hardening is the agent unit's, with an empty `CapabilityBoundingSet`, `NoNewPrivileges=true`, no `ReadWritePaths`, `PrivateNetwork=true` and `MemoryMax=32M`. It writes nothing; it reads `trial` and kills.

When `trial` has state `trial` and the deadline has passed, `revert-check` sends `SIGKILL` to every PID listed in `/sys/fs/cgroup/system.slice/moonglass-agent.service/cgroup.procs`. Those processes run as the same uid, so no capability is needed. systemd restarts the agent, and the gate then reverts (§5). A B that is hung is therefore reverted no later than 5 min 16 s after the switch; a B that crashes or fails to exec is reverted on its next start, within the 5-minute window.

### When the previous binary is also bad

A is not in a trial, so there is no second revert and no older binary. A runs under the normal `Restart=on-failure`. The control plane sees the server as not reconnected (§8) and the fix is a new release.

### After a revert

A reports `REVERTED` with the reason on its first connection, then sets the trial aside: `trial` keeps state `reverted`, the `request_id` and the reason until the next offer or the first hour, so a re-sent offer with the same request id is answered from it (§6).

## 6. The self-check

B evaluates these conditions every 5 s from start until all four hold or the deadline passes. Each row is the ADR-0007 d4 condition as something a test observes; the reason is what `trial` records when it is unmet.

| # | Condition | Observed as | Reason |
|---|---|---|---|
| 1 | the new process is running and has not restarted | `trial.starts == 1` | `RESTARTED` |
| 2 | the enrolled connection to the control plane is back, with the pinned key | one `Heartbeat` and `HeartbeatAck` exchange completed on a connection whose server leaf key equals the `control-plane.pub` pin (`keys.ClientTLSConfig`) | `NOT_CONNECTED` |
| 3 | the running executable is the file whose `.sig` verified | SHA-256 of `/proc/self/exe` equals `trial.sha256` (TM §9 c6) | `EXE_MISMATCH` |
| 4 | the proxy condition of §7 | the §7 check for the release's revision | `PROXY` |

- **Pass.** B sets state `passed`, deletes every version directory other than `current` and `previous`, and sends `PASSED`.
- **Failure B detects.** B records the reason in `trial` and exits 1; the gate then reverts (§5). A condition 2 that is still unmet at the deadline is recorded as `NOT_CONNECTED` the same way.
- **The first hour.** Until `switched_at` + 1 h, B re-checks conditions 1, 3 and 4 every 60 s, and the gate keeps counting starts. The first failure sends one `CHECK_FAILED` with its reason. There is no revert after a pass. At `switched_at` + 1 h B deletes `trial`.

### Messages (W7-6d defines the `.proto`; these are its fields and rules)

`UpdateOffer`, type `UPDATE_OFFER`, server to agent. Its only field is `version` (§1). Its `request_id` is `u` followed by 32 lowercase hex characters from 128 random bits, the same shape as the other ids in `docs/specs/connection.md`.

`UpdateReport`, type `UPDATE_REPORT`, agent to server. Its `request_id` is the offer's, kept in `trial`.

| Field | Rule |
|---|---|
| `version` | the `vX.Y.Z` of §1 |
| `sha256` | 64 lowercase hex characters, or empty for `REJECTED` |
| `outcome` | `REJECTED`, `VERIFY_FAILED`, `SWITCHING`, `PASSED`, `REVERTED` or `CHECK_FAILED` |
| `reason` | `NOT_NEWER`, `NOT_ON_CHANNEL`, `REVERTED_BEFORE`, `TRIAL_IN_PROGRESS`, `POINTER`, `DISK`, `DOWNLOAD`, `TOO_LARGE`, `SIGNATURE`, `CHECKSUM`, `POINTER_MISMATCH`, `WRITE`, `RESTARTED`, `NOT_CONNECTED`, `EXE_MISMATCH`, `PROXY`, `DEADLINE`; `UNSPECIFIED` only with `SWITCHING` or `PASSED` |
| `proxy_restarted` | `true` when this release restarted the proxy (§7), otherwise `false`; always `false` with `REJECTED` and `VERIFY_FAILED` |

`UNSPECIFIED` and unknown values are rejected when the payload is opened, as in `docs/specs/protocol.md`. Each reason belongs to one outcome: `NOT_NEWER`, `NOT_ON_CHANNEL`, `REVERTED_BEFORE`, `TRIAL_IN_PROGRESS` and `POINTER` to `REJECTED`; `DISK`, `DOWNLOAD`, `TOO_LARGE`, `SIGNATURE`, `CHECKSUM`, `POINTER_MISMATCH` and `WRITE` to `VERIFY_FAILED`; `RESTARTED`, `NOT_CONNECTED`, `EXE_MISMATCH`, `PROXY` and `DEADLINE` to `REVERTED` and `CHECK_FAILED`.

The report has no free-text field, so no field can carry a host path, a command line, a shell string, a secret or key material (TM §9 c1). Neither message carries one either.

### Delivery

- `SWITCHING` is sent on the connection A already holds, before A exits.
- A `REVERTED`, `PASSED` or `CHECK_FAILED` report is held in `trial` until a `HeartbeatAck` arrives after it on the same connection, and is re-sent on each reconnect until then.
- The server ignores a duplicate report for a request id it already has a final report for, and does not close the connection. This differs from `CommandResult`, which closes the connection on a duplicate (`docs/specs/connection.md`), because a report is re-sent until acknowledged and the two sides may cross.
- A re-sent offer with a request id the agent has a report for gets the stored report back, unchanged ("Each request id runs once").
- The agent deletes `trial` at `switched_at` + 1 h, or when a new offer with a different request id arrives, whichever is first.

## 7. The drained proxy upgrade

A release changes the proxy when Caddy, our counter module, the admin guard or the config shape the agent pushes changes (ADR-0007 d6, ADR-0001, `docs/specs/proxy.md`).

### The proxy mark

`internal/proxy` has the integer constant `proxy.Revision`. It is raised in the same change as any change to the Caddy version, the counter module, the admin guard or the config shape the agent pushes, and it lives in the signed binary, so no extra artifact is needed. A release changes the proxy when B's `proxy.Revision` differs from the revision the running proxy reports. An agent that finds no proxy running skips the proxy condition (condition 4 passes) and pushes its config as usual.

### Two guard endpoints

Both are answered by the admin guard in `internal/proxy`, beside `GET /moonglass/counters`, and both are reachable only through the `moonglass-proxy-admin` socket (ADR-0001 d3, TM B4). Nothing else about the socket changes.

| Endpoint | Response |
|---|---|
| `GET /moonglass/version` | `{"revision":N,"sha256":"<64 hex of /proc/self/exe>","started_at":"<RFC3339Nano>"}` |
| `POST /moonglass/restart` | `202` with an empty body |

`POST /moonglass/restart` stops Caddy with a 30-second grace period for in-flight requests and WebSockets (ADR-0001 d7), then exits 75 (§5). systemd restarts the proxy from `current`. Connections are refused for the gap between the old process exiting and the new one listening; that is the "announced" part of the ADR-0001 d7 restart, and the report says so with `proxy_restarted`.

### Order when the revision differs

1. B pushes no config.
2. B calls `POST /moonglass/restart`.
3. B polls `GET /moonglass/version` until `revision` equals its own and `sha256` equals `trial.sha256`, waiting at most 2 minutes.
4. B pushes the complete config it builds from agent state.
5. B reads `GET /config/` back.

### Same config shape

The agent and the running proxy are never on different config shapes, because no config is pushed across a revision mismatch. This holds after a revert too: A finds a proxy running with B's revision and, when its own differs, runs the same order before it pushes.

### The proxy self-check condition (condition 4 of §6)

- **The revision is the same:** `started_at` on `GET /moonglass/version` equals `trial.proxy.started_at`, and `GET /config/` equals the config B builds from agent state.
- **The revision differs:** the restart completed with B's revision and B's SHA-256, and `GET /config/` equals what B pushed in step 4.

### No handover

This is the short, announced, drained restart of `docs/specs/proxy.md`. ADR-0001 open question 1 stays open, there is no second proxy process, and no socket activation is built.

## 8. Rollout state on the control plane

This section is hosted only: it runs when `moonglass server` is started with `--release-org ORG_ID`. Every row carries `org_id` set to that org, under RLS (ADR-0002 d1; TM B2), so no lookup table is added. Without `--release-org` the server runs the self-hosted behaviour of §1 and stores no rollout rows.

### Stage membership

A server is in a stage when

```
uint64(first 8 bytes of SHA-256(version ‖ server_id)) mod 100 < stage_percent
```

where `‖` is concatenation of the `vX.Y.Z` bytes and the 32 hex characters of the `server_id`, and `server_id` is the `servers.id` of `docs/specs/storage.md`. Membership is stable across restarts, needs no row per selection, and connected servers are tracked in memory. W7-6f may use the salt only as written here; changing it reselects the fleet.

### Tables

Named as `docs/specs/storage.md` names its tables; all three are row-level tables with `org_id`.

- `rollout_criteria` — `org_id`, `channel` (unique per org), `min_agents`, `min_percent`, `reconnect_window_seconds`, `check_window_seconds`, `stage_interval_seconds`, `canary_soak_seconds`, `stage_percents` (JSON array). At start the server inserts these rows for each channel that has none:

  | `channel` | `min_agents` | `min_percent` | `reconnect_window_seconds` | `check_window_seconds` | `stage_interval_seconds` | `canary_soak_seconds` | `stage_percents` |
  |---|---|---|---|---|---|---|---|
  | `stable` | 2 | 1 | 600 | 3600 | 86400 | 259200 | `[10,50,100]` |
  | `canary` | 2 | 1 | 600 | 3600 | 86400 | 259200 | `[100]` |

  Those inserted defaults are the only constants. Every threshold and window is read from the rows (ADR-0007 d3), so a change is data.
- `rollouts` — `org_id`, `id`, `channel`, `version`, `state`, `stage_percent`, `stage_started_at`, `started_at`, `halted_at`, `halt_reason`, `finished_at`; unique `(org_id, channel, version)`.
- `agent_updates` — `org_id`, `rollout_id`, `server_id`, `request_id`, `stage_percent`, `offered_at`, `outcome` (`offered`, or a lowercase report outcome), `reason`, `switched_at`, `reconnected_at`, `reported_at`; unique `(org_id, rollout_id, server_id)`.

### States

| From | To | When |
|---|---|---|
| — | `waiting` | a stable rollout is created, until the canary rollout of the same version has run for `canary_soak_seconds` (72 h) and is not `halted` or `withdrawn` |
| — | `rolling` at 100 | a canary rollout is created |
| `waiting` | `rolling` at 10 | the soak condition above holds |
| `rolling` | the next stage | `stage_interval_seconds` (24 h) after `stage_started_at` with no halt: 10 to 50 to 100 |
| `rolling` | `halted` | a halt criterion below is met |
| `halted` | `rolling`, same stage | `rollouts.resume`; `stage_started_at` is reset to now, so the count restarts in that stage |
| any | `withdrawn` | `rollouts.withdraw`; final |
| `rolling`, `halted` | `superseded` | a newer version's rollout starts on that channel |

A halted rollout sends no offers and stays halted until it is resumed or withdrawn (ADR-0007 d3). A halted or withdrawn canary rollout keeps the stable rollout of that version in `waiting`.

### Updated, and failed

- **Updated in the current stage:** `agent_updates` rows of this rollout whose `stage_percent` is the rollout's current `stage_percent` and whose `switched_at` is at or after `stage_started_at`.
- **Failed:** among those rows, any of: `outcome` is `reverted`; no `reconnected_at` within `reconnect_window_seconds` of `switched_at` (10 minutes); or `outcome` `check_failed` reported within `check_window_seconds` of `switched_at` (the first hour). Each agent counts once, whatever its number of failures.
- **Halt rule:** the rollout halts when `failed ≥ min_agents` and `failed × 100 ≥ min_percent × updated` (at least 2 agents, and at least 1%), computed in integers. It is checked on every report and once a minute. The values come from `rollout_criteria` (ADR-0007 d3). The same arithmetic on the canary rollout keeps the stable rollout in `waiting`.

### Operations

Each is declared in the registry next to its handler (ADR-0006 d3), added by W7-6f, and regenerates the OpenAPI file and the CLI. The target is the release org, which is an org-level target, so only a `*` scope row matches (`docs/specs/registry.md`).

| Name | Method, path | `MinRole` | Destructive |
|---|---|---|---|
| `rollouts.list` | `GET /v1/rollouts` | viewer | no |
| `rollouts.resume` | `POST /v1/rollouts/resume` | member | no |
| `rollouts.withdraw` | `POST /v1/rollouts/withdraw` | admin | yes |

- `resume` and `withdraw` take `channel` and `version`.
- `resume` works only from `halted`; from any other state it is refused with `rollout <channel> <version> is <state>, not halted`. It sets `rolling` and resets `stage_started_at` to now.
- `withdraw` is final and irreversible, which is why it is `admin` and destructive. It sets `withdrawn` and `finished_at`.
- **Effect on agents already updated: none.** They keep the release, because agents never downgrade (§1). The fix is a newer release.
- On a server without `--release-org` every one of these is refused with `this control plane does not run staged rollouts`.

## 9. Release workflow and signing hand-off

### The workflow

`.github/workflows/release.yml`, built by W7-6c, triggered by `on: push: tags: ['v[0-9]+.[0-9]+.[0-9]+']`.

1. For `GOARCH` `amd64` and `arm64` it runs `CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w -X <version var>=<tag>"`.
2. It writes `moonglass.sbom.json` (SPDX JSON) with `anchore/sbom-action@66cbf4bc1f1c0d2edc94016e65bc221b6bb0ad6c # v0.24.3`.
3. It writes `SHA256SUMS` over the artifacts of that arch's set and copies the units from `deploy/systemd/`.
4. It attests each binary and the SBOM with `actions/attest-build-provenance@4d101475d8b20a2381f78447822ac1eab6504dd8 # v4.2.2`, with permissions `id-token: write`, `attestations: write` and `contents: write` (ADR-0007 d1, S§3.14).
5. It uploads `moonglass-<version>-linux-<arch>.tar` (unsigned) to a **draft** GitHub release for the tag.

CI never holds a signing key. Signing stays offline on the owner's token (ADR-0007 d1; TM §2).

### Signing

The owner runs `gh release download <tag>`, extracts the archives, and signs **each** artifact and the canary pointer with

```
ssh-keygen -Y sign -f <sk key handle> -n moonglass-release -O hashalg=sha512 <file>
```

Each signature needs the token's PIN and a touch, and the owner signs with OpenSSH from Homebrew (ADR-0007 consequences). The owner then uploads `moonglass-<version>-linux-<arch>.sigs.tar` and the signed pointer with `gh release upload`.

### Publish

A `workflow_dispatch` job in the same file takes the inputs `version` and `channel`.

1. It downloads the signed set from the draft release.
2. For each arch it runs `scripts/install --from <dir> --prefix <tmp> --unit-dir <tmp> --state-dir <tmp>`, the same pinned-key check a user runs, and it checks the pointer with `ssh-keygen -Y verify`.
3. It uploads to the release origin in this order: `releases/<version>/…` for both arches, then, for stable, `releases/latest/…`, then `releases/channels/<channel>` and its `.sig` last, so a pointer never names a version that is not there yet.

The origin's upload credential is a repository secret. It is not a signing key.

### Release origin (owner decision, 2026-11-18)

This closes §10 items 24 and 25.

**The origin.** One Cloudflare R2 bucket, `moonglass-releases`, served at `https://moonglass.dev/releases/`.

- An object's key is its URL path without the leading `/`, so `https://moonglass.dev/releases/v1.2.3/linux-amd64/moonglass` is the object `releases/v1.2.3/linux-amd64/moonglass`. Nothing rewrites the path.
- The bucket is connected to the R2 custom domain `releases-origin.moonglass.dev`. An origin rule in the `moonglass.dev` zone, for URI paths starting with `/releases/`, overrides the DNS record and rewrites the `Host` header to that hostname. Every other path of `moonglass.dev`, such as the Go import path, is served as before.
- **No redirects.** A request under `/releases/` is answered `200` with the object, or `404`, and never with a `3xx`. No redirect rule, page rule or trailing-slash rule matches `/releases/*`. The agent and `scripts/install` follow no redirect (§3), so a redirect would fail every download rather than be followed.
- The `r2.dev` development URL stays disabled. Nothing else writes to the bucket.

**The upload step.** The publish job uploads with the `aws` CLI that GitHub's Ubuntu runners already have, so no action and no new tool is added:

```
aws s3 cp <file> s3://moonglass-releases/<key> \
  --endpoint-url https://<account id>.r2.cloudflarestorage.com \
  --content-type <type> --cache-control <policy>
```

- One `aws s3 cp` per object, in the order of "Publish" step 3. No `aws s3 sync`, which uploads in an order of its own.
- `AWS_DEFAULT_REGION` is `auto`. The account id comes from the repository variable `R2_ACCOUNT_ID`; it is not a secret.
- Content types: `application/octet-stream` for `moonglass`, `application/json` for `moonglass.sbom.json`, and `text/plain; charset=utf-8` for everything else.

**The secret pair.** `R2_RELEASES_ACCESS_KEY_ID` and `R2_RELEASES_SECRET_ACCESS_KEY`: an R2 API token with Object Read & Write on `moonglass-releases` only, and no other bucket or account permission.

- They are secrets of the GitHub environment `release`, not of the whole repository, and only the publish job names that environment. The build, attest and draft-release jobs of W7-6c reference no secret.
- The upload step alone maps them to `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` in its `env`. No step prints them.
- They can overwrite release objects, but they cannot sign: an object they write that is not signed by a pinned key fails verification on every agent and in `scripts/install` (§3; TM §2).

**Cache-Control.**

| Objects | `Cache-Control` | Why |
|---|---|---|
| `releases/<version>/…` | `public, max-age=31536000, immutable` | A version's files never change |
| `releases/latest/…` | `public, max-age=60` | Replaced on each promotion to stable |
| `releases/channels/<channel>` and `.sig` | `public, max-age=60` | The pointer moves with each release |

A `latest` directory read during a promotion can mix two versions for up to 60 seconds. The install then fails its checksum or signature check and is rerun; it never installs a mixed set.

**Attestations.** The repository `github.com/moonglass-dev/moonglass` stays private until the first release tag is cut. It is made public before that tag is pushed, because `actions/attest-build-provenance` on a private repository needs GitHub Enterprise Cloud. W7-6c and W7-6g are built and merged while the repository is private; their workflows run only on a tag or a dispatch. Making it public is the owner's step, done by hand, not by any ticket.

### Promotion to stable

The owner signs a stable pointer and runs the same job with `channel=stable`. The hosted control plane still keeps the stable rollout in `waiting` until §8 allows it.

## 10. Owner decisions

Every choice below is one no ADR or existing spec already fixes. One line each, so each can be approved or changed before merge.

**Final, 2026-11-18.** The product name Moonglass, the signing namespace `moonglass-release` and the release origin host `moonglass.dev` are final. Nothing in W7-6c, W7-6d or W7-6e waits on a rename, and those tickets may proceed with the names this spec gives.

1. Signed channel pointers with four lines, binding the version and the two binary SHA-256s.
2. Agents refuse any version that is not strictly newer; there is no downgrade.
3. Every control plane polls the pointers every 10 minutes; offers are limited to one per server per version per hour.
4. `--release-org` selects hosted rollout, and its absence means self-hosted offers to every connected agent on the channel.
5. `Heartbeat` gains `update_channel`, and `UPDATE_OFFER` and `UPDATE_REPORT` get the fields of §6.
6. The per-arch URL layout, `latest` as a copy of stable, and the install default `--from` per arch.
7. The artifact set grows by the two revert units.
8. The binary layout under `/var/lib/moonglass/releases`, symlink switching, the agent-owned binaries, and `/usr/local/bin/moonglass` kept for the CLI and `server` only.
9. The 128 MiB, 64 KiB, 4 KiB and 1 KiB caps, the 10-minute download budget and the disk check.
10. Exit status 75 for both deliberate restarts.
11. The revert design: the gate as `ExecStartPre` from `previous`, the timer every 15 s, `SIGKILL` through `cgroup.procs` (which assumes cgroup v2), and a revert that lands up to 16 s after five minutes.
12. "Not restarted" is defined as `starts == 1`, counted by the gate.
13. The first-hour re-check every 60 s as the meaning of "failed self-check in their first hour".
14. Report delivery confirmed by the next `HeartbeatAck`, with duplicates ignored rather than closing the connection.
15. `proxy.Revision`, plus `GET /moonglass/version` and `POST /moonglass/restart` on the admin guard.
16. Hash-bucket stage membership (approximately 10%, 50% and 100% of connected servers), with the exact hash of §8.
17. Rollout rows in the release org rather than global tables, and the criteria row defaults.
18. `rollouts.list` added, and the roles member and admin per the ADR-0006 rule, not owner.
19. Resume restarts the stage clock.
20. Withdraw has no effect on agents already updated.
21. The SBOM tool and format (syft, SPDX JSON).
22. The draft GitHub release hand-off, with the tar names of §9.
23. The publish job verifying with `scripts/install` and the upload order.
24. The release origin behind `moonglass.dev/releases/` and its upload credential: decided 2026-11-18, the R2 bucket, the `aws s3 cp` upload and the secret pair of §9 "Release origin".
25. Attestations on a private repository need GitHub Enterprise Cloud, or a public repository, before W7-6c can run: decided 2026-11-18, the repository is made public before the first release tag is pushed (§9 "Release origin").
26. W7-6e's `allowed_paths` need `cmd/moonglass/`, `internal/registry/` (`LocalCommands` gains `agent update-gate` and `agent revert-check`) and `internal/proxy/`.
27. The offer checks run in the order of §1, so an older version is `NOT_NEWER` and another channel's version is `NOT_ON_CHANNEL`.
28. `trial` is one JSON file with `"format": 1`, deleted at the first hour or on the next offer, and a re-sent offer is answered from it rather than from agent state.
29. `NOT_CONNECTED` is recorded by the new agent when the pinned connection is still missing at the deadline; `DEADLINE` is the gate's reason when the new agent could not record one.
30. An agent that finds no proxy running passes the proxy condition and pushes its config; no error is raised.
