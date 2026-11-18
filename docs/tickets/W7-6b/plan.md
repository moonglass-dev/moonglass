---
verdict: ready
reason: ""
---

# Plan for W7-6b

Docs-only. One new file, `docs/specs/update.md`. `.md` and `docs/` do not count toward `max_diff_lines` (`tools/pipeline/verify.go` `countsTowardSize`), so the 50-line cap is not at risk. `tests: []`, so no tests and no `testdata/tests.txt` change.

## Files to change

- `docs/specs/update.md` (new). It follows the style of `docs/specs/install.md` and `connection.md`: short sections, tables, exact names, and a source cited for each rule (ADR-0007 dN, S§3.14, TM §n, or a spec). The opening paragraph says the spec turns ADR-0007 decisions 1 to 6, S§3.14 and threat-model checklist items 6 and 7 into behaviour for W7-6c to W7-6f, and that the owner approves it before merge. The sections below are numbered 1 to 10 to match Scope. Every number and name in this plan goes into the spec exactly as written here.

### 1. Discovery

- **Version format.** `vMAJOR.MINOR.PATCH`, ordered by semver. Each release has one version. Canary and stable do not have separate builds: a channel is a signed pointer to a version.
- **Channel pointer.** `https://moonglass.dev/releases/channels/<channel>` (`canary` or `stable`) and `<channel>.sig`, signed like every artifact. It has exactly four lines: `channel <channel>`, `version <vX.Y.Z>`, `linux-amd64 <64 hex sha256 of moonglass>`, `linux-arm64 <64 hex>`. Its maximum size is 1 KiB. Any other content is rejected. The pointer binds the channel, the version and the binary, so a signed binary from one version or channel cannot be served for another.
- **The control plane** (hosted and self-hosted) fetches and verifies both pointers at start and every 10 minutes, using the W7-6a verifier. It knows each agent's channel from a new Heartbeat field, `update_channel` (`STABLE` or `CANARY`, set by W7-6d), which the agent fills from `update.channel` in `agent.toml`. It sends `UpdateOffer` only to agents whose `agent_version` is older than their channel's pointer version. It sends at most one offer per server per version per hour, and it sends none after that server reports `PASSED` or `REVERTED` for that version.
  - **Hosted** (the server is started with `--release-org ORG_ID`, §8): offers follow the rollout state.
  - **Self-hosted** (no `--release-org`): every connected agent on the channel gets an offer at once, with no percentage rollout, no rows and no halt (ADR-0007 d5).
- **The agent** never installs a release it was not offered. It treats an offer as untrusted (TM §3 B3, ninth boundary). Before it downloads anything, it fetches its own channel's pointer from the fixed origin `https://moonglass.dev` (compiled in, never taken from a message) and verifies it. It accepts the offer only when all of these hold:
  - the offered version equals the pointer's version;
  - the version is strictly greater than its own;
  - the binary's SHA-256 is not in `releases/reverted`;
  - no trial is in progress.
  Otherwise it sends a `REJECTED` report with a reason. A hostile control plane can therefore only delay an update or offer the signed pointer's own release. It cannot name a URL, a path, a channel or an older version.
- The control plane never replaces itself (TM §8). The operator updates `moonglass server` and the CLI by running the signed install.

### 2. Artifact URL layout

- `https://moonglass.dev/releases/<version>/linux-<arch>/<artifact>` and `<artifact>.sig`, with `arch` `amd64` or `arm64`. The artifact set is install.md's set plus the two revert units (§4): `moonglass`, `SHA256SUMS`, `moonglass.sbom.json`, `moonglass-agent.service`, `moonglass-proxy.service`, `moonglass-agent-revert.service`, `moonglass-agent-revert.timer`.
- `SHA256SUMS` keeps the `sha256sum` format and covers every other artifact in that arch's directory.
- `https://moonglass.dev/releases/latest/linux-<arch>/` is a byte-identical copy of the directory of the version in the **stable** pointer. It is replaced on each promotion to stable. Canary never moves `latest`.
- The agent fetches only `moonglass`, `SHA256SUMS` and their `.sig` files. Unit files change only through the signed install.

### 3. Verification before install

The steps run in order. Each has a fixed reason code.

1. Recreate `/var/lib/moonglass/releases/staging/` (mode 0700, owner `moonglass`) empty.
2. Check free space on that filesystem: at least 3 × 128 MiB, otherwise `DISK`.
3. Download over HTTPS with system roots and TLS ≥ 1.2. Redirects are not followed. The total budget is 10 minutes. Size caps: `moonglass` 128 MiB, `SHA256SUMS` 64 KiB, `.sig` 4 KiB. Failures are `DOWNLOAD` or `TOO_LARGE`.
4. Verify `SHA256SUMS.sig` and `moonglass.sig` with the W7-6a verifier: SSHSIG preamble, version 1, either of the two pinned keys, namespace `moonglass-release`, hash `sha512`, and flags 0x01 and 0x04 both set. Failure is `SIGNATURE`.
5. The SHA-256 of the staged `moonglass` must equal its `SHA256SUMS` line (`CHECKSUM`) and the pointer's `linux-<arch>` entry (`POINTER_MISMATCH`).
6. Only then: `mkdir releases/<sha256>` 0755, rename `staging/moonglass` to `releases/<sha256>/moonglass`, chmod 0755, and fsync the file and the directory (`WRITE`).

On any failure the agent empties `staging/`, leaves `current` and `previous` untouched, keeps running, and sends `VERIFY_FAILED` with the reason. Nothing executes a staged file.

### 4. Binary layout under `ProtectSystem=strict`

| Path | Type, mode | Owner | Written by |
|---|---|---|---|
| `/var/lib/moonglass` | dir 0755 | root | install |
| `/var/lib/moonglass/releases` | dir 0755 | moonglass:moonglass | install creates; agent writes |
| `releases/<sha256>/` and `releases/<sha256>/moonglass` | dir 0755, file 0755 | moonglass | agent (install for the first one) |
| `releases/current`, `releases/previous` | symlinks to `<sha256>/moonglass` (relative) | moonglass | agent, by symlink and rename; install |
| `releases/staging/` | dir 0700 | moonglass | agent |
| `releases/trial` | file 0600, JSON, `"format": 1` | moonglass | agent and the gate |
| `releases/reverted` | file 0600, one SHA-256 hex per line | moonglass | the gate |
| `/usr/local/bin/moonglass` | file 0755 | root | install only (CLI and `server`) |

- Staging lives inside `releases/`, so the final `rename` stays on one mount. A rename across systemd's per-path bind mounts fails with `EXDEV`.
- Only `current`, `previous` and the trial's binary exist. Other version directories are deleted when a trial passes.
- `moonglass-proxy` can read and execute `releases/*` but write none of it. It cannot read `trial`, `reverted` or `staging`, and it never reaches `/var/lib/moonglass/agent`.
- Changes W7-6e makes, each with its one-line reason:
  - Agent unit `ExecStart=/var/lib/moonglass/releases/current/moonglass agent connect …`: the unit runs the switchable binary.
  - Agent unit `ExecStartPre=/var/lib/moonglass/releases/previous/moonglass agent update-gate --releases /var/lib/moonglass/releases`: the last good binary decides before each start whether to revert.
  - Agent unit `ReadWritePaths=/var/lib/moonglass/releases`: the agent writes staging, version directories, symlinks and the trial. This is the only writable path added.
  - Agent unit `Wants=moonglass-agent-revert.timer`: the revert timer runs whenever the agent does.
  - Proxy unit `ExecStart=/var/lib/moonglass/releases/current/moonglass proxy …`: one binary on disk for both roles (ADR-0007 d6).
  - New `moonglass-agent-revert.service` and `.timer` (§5).
  - `scripts/install`:
    - The default `--from` becomes `https://moonglass.dev/releases/latest/linux-<arch>`, mapping `uname -m` `x86_64` to amd64 and `aarch64` to arm64; anything else is an error. Reason: artifacts are per arch.
    - `ARTIFACTS` gains the two revert units. Reason: they are signed like the others.
    - New `--state-dir DIR`, default `/var/lib/moonglass`. Reason: tests can check the layout in a temp directory.
    - Write `releases/<sha256>/moonglass`, set `previous` to the old `current` (or to the new binary on first install), set `current` to the new binary, and delete `trial`. Reason: the units run from `releases/`.
    - In host-setup mode only, `chown -R moonglass:moonglass releases`. Reason: the agent must own what it replaces.
  - No unit gains a capability, and `ProtectSystem=strict` stays.

### 5. The systemd revert

- **Switch.**
  1. The old agent (A) writes `trial`: `request_id`, `version`, `sha256`, `previous_sha256`, `switched_at` (RFC3339 UTC), `deadline` (`switched_at` + 5 min), `starts` 0, `state` `trial`, and `proxy` (the revision and `started_at` that the proxy reports, §7).
  2. A points `previous` at its own binary and `current` at B.
  3. A sends `SWITCHING`, waits up to 10 s for the next `HeartbeatAck`, and exits with status 75. `Restart=on-failure` starts B.
- **The five-minute window** is measured from `switched_at`, the instant before the `current` rename.
- **"Not restarted"** means the agent unit started exactly once since the switch. The gate counts starts in `trial.starts`, and the self-check requires `starts == 1`.
- **`moonglass agent update-gate`** (ExecStartPre, run from `previous`):
  - With no trial, it exits 0.
  - With state `trial`, `starts == 0` and the deadline not passed, it sets `starts = 1` and exits 0.
  - With state `trial` and either `starts ≥ 1` or the deadline passed, it reverts:
    1. point `current` at `previous`;
    2. set state `reverted` with a reason (the one B recorded, else `RESTARTED` or `DEADLINE`);
    3. delete B's directory;
    4. append B's SHA-256 to `reverted`;
    5. exit 0, so A starts.
  - With state `passed`, it increments `starts`.
- **`moonglass-agent-revert.timer`**: `OnActiveSec=15s`, `OnUnitActiveSec=15s`, `AccuracySec=1s`.
- **`moonglass-agent-revert.service`**: `Type=oneshot`, `User=moonglass`. It runs `/var/lib/moonglass/releases/previous/moonglass agent revert-check --releases /var/lib/moonglass/releases`. Hardening is the same as the agent's, with an empty `CapabilityBoundingSet`, `NoNewPrivileges=true`, no `ReadWritePaths`, `PrivateNetwork=true` and `MemoryMax=32M`.
  - When `trial` has state `trial` and the deadline has passed, it sends SIGKILL to every PID in `/sys/fs/cgroup/system.slice/moonglass-agent.service/cgroup.procs`. These are the same uid, so no capability is needed.
  - systemd then restarts the agent, and the gate reverts.
  - A hung B is reverted no later than 5 min 16 s after the switch. A B that crashes is reverted on its next start.
- **When the previous binary is also bad:** A is not in a trial, so there is no second revert and no older binary. A runs under the normal `Restart=on-failure`. The control plane sees it as not reconnected (§8), and the fix is a new release.
- A reports `REVERTED` with the reason on its first connection, then sets the trial aside (§6).

### 6. The self-check

B evaluates these conditions every 5 s from start until all four hold or the deadline passes.

1. `trial.starts == 1`.
2. A Heartbeat and HeartbeatAck exchange has completed on a connection whose server leaf key equals the `control-plane.pub` pin (`keys.ClientTLSConfig`).
3. The SHA-256 of `/proc/self/exe` equals `trial.sha256`.
4. The proxy condition of §7.

- **Pass.** B sets `state passed`, deletes version directories other than `current` and `previous`, and sends `PASSED`.
- **Failure B detects** (for example condition 3): B records the reason in `trial` and exits 1, and the gate reverts.
- **First hour.** Until `switched_at` + 1 h, B re-checks conditions 1, 3 and 4 every 60 s, and the gate keeps counting starts. The first failure sends one `CHECK_FAILED` with its reason. There is no revert after a pass. At one hour B deletes `trial`.
- **Message (for W7-6d).** `UpdateOffer`, type `UPDATE_OFFER`, sent from server to agent. Its only field is `version`. Its `request_id` is `u` plus 32 lowercase hex characters from 128 random bits.
- **Message (for W7-6d).** `UpdateReport`, type `UPDATE_REPORT`, sent from agent to server, with the offer's `request_id`, which is kept in `trial`.

  | Field | Rule |
  |---|---|
  | `version` | the semver above |
  | `sha256` | 64 lowercase hex characters, or empty for `REJECTED` |
  | `outcome` | `REJECTED`, `VERIFY_FAILED`, `SWITCHING`, `PASSED`, `REVERTED` or `CHECK_FAILED` |
  | `reason` | Enum: `NOT_NEWER`, `NOT_ON_CHANNEL`, `REVERTED_BEFORE`, `TRIAL_IN_PROGRESS`, `POINTER`, `DISK`, `DOWNLOAD`, `TOO_LARGE`, `SIGNATURE`, `CHECKSUM`, `POINTER_MISMATCH`, `WRITE`, `RESTARTED`, `NOT_CONNECTED`, `EXE_MISMATCH`, `PROXY`, `DEADLINE`. It is `UNSPECIFIED` only with `SWITCHING` or `PASSED`. |
  | `proxy_restarted` | bool |

  It has no free-text field, so no field can carry a host path, a command line, a shell string, a secret or key material.
- **Delivery.** A `REVERTED`, `PASSED` or `CHECK_FAILED` report is held in `trial` until a `HeartbeatAck` arrives after it on the same connection. It is resent on each reconnect until then. The server ignores a duplicate report for a request id it already has a final report for, and does not close the connection. This differs from `CommandResult` on purpose, and the spec says so.
- A re-sent offer with a known request id gets the stored report back ("Each request id runs once").

### 7. The drained proxy upgrade

- **The proxy mark.** `internal/proxy` has the integer constant `proxy.Revision`. It is raised in the same change as any change to the Caddy version, the counter module, the admin guard or the config shape the agent pushes. It lives in the signed binary, so no extra artifact is needed. A release changes the proxy when B's `proxy.Revision` differs from the revision the running proxy reports.
- **Two guard endpoints**, reachable only through the `moonglass-proxy-admin` socket (TM B4; nothing else changes there):
  - `GET /moonglass/version` returns `{"revision":N,"sha256":"<hex of /proc/self/exe>","started_at":"<RFC3339Nano>"}`.
  - `POST /moonglass/restart` returns 202. The proxy then stops Caddy with a 30 s grace period for in-flight requests and WebSockets (ADR-0001 d7) and exits 75. systemd restarts it from `current`.
- **Order when the revision differs:**
  1. B pushes no config.
  2. B calls `POST /moonglass/restart`.
  3. B polls `GET /moonglass/version` until `revision` equals its own and `sha256` equals `trial.sha256`, waiting at most 2 min.
  4. B pushes the complete config it builds from agent state.
  5. B reads `GET /config/` back.
- **Same config shape.** The agent and the running proxy are never on different config shapes, because no config is pushed across a revision mismatch. This also holds after a revert, where A restarts a proxy that has B's revision.
- **The proxy self-check condition.**
  - When the revision is the same: `started_at` equals `trial.proxy.started_at`, and `GET /config/` equals the config B builds from state.
  - When the revision differs: the restart completed with B's revision and SHA-256, and `GET /config/` equals what B pushed.
- **No handover.** This is the short, announced, drained restart of `docs/specs/proxy.md`. ADR-0001 open question 1 stays open, and there is no second proxy process. "Announced" means `proxy_restarted` is set in the report, and the docs say connections are refused for the restart gap.

### 8. Rollout state on the control plane

- **Hosted only.** It runs only with `moonglass server --release-org ORG_ID`. Every row carries `org_id` set to that org, with RLS (ADR-0002 d1, TM B2), so no lookup table is added.
- **Stage membership.** A server is in a stage when `uint64(first 8 bytes of SHA-256(version ‖ server_id)) mod 100 < stage_percent`. Membership is stable across restarts and needs no row per selection. Connected servers are tracked in memory.
- **Tables**, named in the style of `docs/specs/storage.md`:
  - `rollout_criteria`: `org_id`, `channel` (unique per org), `min_agents` (2), `min_percent` (1), `reconnect_window_seconds` (600), `check_window_seconds` (3600), `stage_interval_seconds` (86400), `canary_soak_seconds` (259200), `stage_percents` (JSON `[10,50,100]`; canary `[100]`). At start the server inserts the ADR values when a channel has no row. Those inserted defaults are the only constants. All logic reads the rows.
  - `rollouts`: `org_id`, `id`, `channel`, `version`, `state`, `stage_percent`, `stage_started_at`, `started_at`, `halted_at`, `halt_reason`, `finished_at`. Unique on `(org_id, channel, version)`.
  - `agent_updates`: `org_id`, `rollout_id`, `server_id`, `request_id`, `stage_percent`, `offered_at`, `outcome` (`offered` or a lowercase report outcome), `reason`, `switched_at`, `reconnected_at`, `reported_at`. Unique on `(org_id, rollout_id, server_id)`.
- **States:**
  - A stable rollout starts as `waiting` until the canary rollout of the same version has been running for 72 h (`canary_soak_seconds`) and is not `halted` or `withdrawn`. It then becomes `rolling` at stage 10.
  - A canary rollout starts `rolling` at 100.
  - From `rolling`, after `stage_interval_seconds` without a halt, the rollout moves to the next stage: 10 to 50 to 100, 24 h apart.
  - `rolling` becomes `halted` on the halt criteria.
  - `halted` becomes `rolling` on resume.
  - Any state becomes `withdrawn` on withdraw.
  - `rolling` or `halted` becomes `superseded` when a newer version's rollout starts on that channel.
- **Updated in the current stage:** rows with the rollout's current `stage_percent` and `switched_at ≥ stage_started_at`.
- **Failed:** among those rows, any of:
  - outcome `reverted`;
  - no `reconnected_at` within `reconnect_window_seconds` of `switched_at`;
  - `check_failed` reported within `check_window_seconds` of `switched_at`.
  Each agent counts once.
- **Halt rule.** The rollout halts when `failed ≥ min_agents` and `failed × 100 ≥ min_percent × updated`. It is checked on each report and each minute. A halted rollout sends no offers and stays halted until it is resumed or withdrawn. A halted or withdrawn canary rollout keeps the stable rollout of that version in `waiting`.
- **Operations** (`docs/specs/registry.md` table form). The target is the release org, so only a `*` scope row matches.

  | Name | Method, path | `MinRole` | Destructive |
  |---|---|---|---|
  | `rollouts.list` | `GET /v1/rollouts` | viewer | no |
  | `rollouts.resume` | `POST /v1/rollouts/resume` | member | no |
  | `rollouts.withdraw` | `POST /v1/rollouts/withdraw` | admin | yes |

  - `resume` and `withdraw` take `channel` and `version`.
  - `resume` works only from `halted`. It sets `rolling` and resets `stage_started_at` to now, so the count restarts in the same stage.
  - `withdraw` is final.
  - Effect on agents already updated: none. They keep the release, because agents never downgrade (§1). The fix is a newer release.
  - On a server without `--release-org`, the call is refused with `this control plane does not run staged rollouts`.

### 9. Release workflow and signing hand-off

- **Workflow.** `.github/workflows/release.yml`, triggered by `on: push: tags: ['v[0-9]+.[0-9]+.[0-9]+']`.
  - For `GOARCH` amd64 and arm64, it runs `CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w -X <version var>=<tag>"`.
  - It writes the SBOM `moonglass.sbom.json` (SPDX JSON) with `anchore/sbom-action@66cbf4bc1f1c0d2edc94016e65bc221b6bb0ad6c # v0.24.3`.
  - It writes `SHA256SUMS` and copies the units from `deploy/systemd/`.
  - It attests each binary and SBOM with `actions/attest-build-provenance@4d101475d8b20a2381f78447822ac1eab6504dd8 # v4.2.2`, with permissions `id-token: write`, `attestations: write` and `contents: write`.
  - It uploads `moonglass-<version>-linux-<arch>.tar` (unsigned) to a **draft** GitHub release for the tag.
  - CI never holds a signing key.
- **Signing.** The owner runs `gh release download <tag>`, extracts the archives, and signs each artifact and the canary pointer with `ssh-keygen -Y sign -f <sk key handle> -n moonglass-release -O hashalg=sha512 <file>`. Each signature needs a PIN and a touch. The owner uses Homebrew OpenSSH (ADR-0007 consequences) and uploads `moonglass-<version>-linux-<arch>.sigs.tar` and the pointer with `gh release upload`.
- **Publish.** A `workflow_dispatch` job in the same file takes the inputs `version` and `channel`.
  1. It downloads the signed set.
  2. For each arch it runs `scripts/install --from <dir> --prefix <tmp> --unit-dir <tmp> --state-dir <tmp>`, the same pinned-key check users run, and it checks the pointer with `ssh-keygen -Y verify`.
  3. It uploads to the release origin in this order: `releases/<version>/…`, then for stable `releases/latest/…`, then `releases/channels/<channel>` and its `.sig` last, so a pointer never names a missing version.
  - The origin's upload credential is a repository secret. It is not a signing key.
- **Promotion to stable.** The owner signs a stable pointer and runs the same job with `channel=stable`. The hosted control plane still keeps the stable rollout `waiting` until §8 allows it.

### 10. Owner decisions

One line each:

1. Signed channel pointers with four lines, binding the version and the binary SHA-256s.
2. Agents refuse any version that is not strictly newer; there is no downgrade.
3. Every control plane polls the pointers every 10 minutes; offers are limited to one per server per version per hour.
4. `--release-org` selects hosted rollout, and its absence means self-hosted offers to all.
5. Heartbeat gains `update_channel`, and `UPDATE_OFFER` and `UPDATE_REPORT` get the fields above.
6. The per-arch URL layout, `latest` as a copy of stable, and the install default `--from` per arch.
7. The artifact set grows by the two revert units.
8. The binary layout under `/var/lib/moonglass/releases`, symlink switching, the agent-owned binaries, and `/usr/local/bin/moonglass` kept for the CLI and server only.
9. The 128 MiB, 64 KiB, 4 KiB and 1 KiB caps, the 10-minute download budget and the disk check.
10. Exit status 75 for both deliberate restarts.
11. The revert design: the gate as ExecStartPre from `previous`, the timer every 15 s, SIGKILL through `cgroup.procs` (which assumes cgroup v2), and a revert that lands up to 16 s after five minutes.
12. "Not restarted" is defined as `starts == 1`, counted by the gate.
13. The first-hour re-check every 60 s as the meaning of "failed self-check in their first hour".
14. Report delivery confirmed by the next `HeartbeatAck`, with duplicates ignored rather than closing the connection.
15. `proxy.Revision`, plus `GET /moonglass/version` and `POST /moonglass/restart` on the admin guard.
16. Hash-bucket stage membership (approximately 10%, 50% and 100% of connected servers).
17. Rollout rows in the release org rather than global tables, and the criteria row defaults.
18. `rollouts.list` added, and the roles member and admin per the ADR-0006 rule, not owner.
19. Resume restarts the stage clock.
20. Withdraw has no effect on agents already updated.
21. The SBOM tool and format (syft, SPDX JSON).
22. The draft GitHub release hand-off, with tar names.
23. The publish job verifying with `scripts/install` and the upload order.
24. The release origin behind `moonglass.dev/releases/` and its upload credential, which no existing decision names.
25. Attestations on a private repository need GitHub Enterprise Cloud, or a public repository, before W7-6c can run.
26. W7-6e's `allowed_paths` need `cmd/moonglass/`, `internal/registry/` (`LocalCommands` gains `agent update-gate` and `agent revert-check`) and `internal/proxy/`.

## Tests to write

- None. The ticket lists `tests: []` and changes docs only. Later tickets name acceptance tests against this spec.

## Edge cases

- **A hostile offer.** An older version, another channel's version, a reverted SHA-256, or an offer during a trial gets `REJECTED` with a reason, and nothing is downloaded (§1).
- **A tampered, missing, truncated or wrong-key `.sig`, or a wrong namespace, hash or flags:** `SIGNATURE`, and nothing is switched (§3).
- **A mix-and-match binary** (signed, but from another version or arch): `POINTER_MISMATCH` (§3).
- **An oversize download or a full disk:** `TOO_LARGE` or `DISK` (§3).
- **B crashes at start, B is hung, B fails exec:** the gate reverts, the timer kills, the gate counts starts (§5).
- **The previous binary is also bad:** no second revert (§5).
- **A lost report:** resent until a `HeartbeatAck` follows it; duplicates are ignored (§6).
- **A release that changes the proxy, and a revert across a proxy change:** §7.
- **Halt arithmetic at small fleets:** at least 2 agents, and the ≥ 1% product form uses integers (§8).
- **Control-plane restart:** state is in rows and buckets are deterministic (§8).

## Risks

- **Review rejects a choice.** Mitigation: every choice not fixed by an ADR is in §10. Fixed values are copied exactly: `moonglass-release`, `sha512`, 0x01 and 0x04, two keys, 5 minutes, 10/50/100, 24 h, 72 h, 2 and 1%, 10 min, 1 h, halted until resumed or withdrawn.
- **Words that look like placeholders.** The spec must contain no "TBD", "fallback", "audited" or open questions. In particular, the wording of the proxy-restart default must not use the banned word, even though docs are exempt.
- **Writable paths and capabilities.** The only writable path added is `ReadWritePaths=/var/lib/moonglass/releases` on the agent. No capability is added, and the proxy has no write access to anything executed (TM checklist 7).
- **Attestations on a private repository** may be unavailable on the owner's plan. This is flagged as owner decision 25, not hidden.
- **W7-6e's paths** do not cover the subcommands or the proxy endpoints. This is flagged as owner decision 26 so the build plan can be amended before W7-6e is claimed.
- **The worker changes another file.** It must change only `docs/specs/update.md`. install.md's "Out of scope" line is left for W7-6e.
