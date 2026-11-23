# Disk guard

The agent keeps a small server from filling its disk (stack §3.13). W3-6 builds it in `internal/agent/disk`.

## What is watched

Two filesystems: the one holding the Docker data root (`DockerRootDir` from the Engine) and the one holding the agent's data directory. When they are the same filesystem, it is checked once. The guard checks every 30 seconds and right before every build and every deploy.

## Levels

For each filesystem, with `free` the bytes available to unprivileged users and `total` its size:

| Level | When | The agent |
|---|---|---|
| `OK` | otherwise | Nothing |
| `LOW` | `free` is below 15% of `total`, **or** below 5 GiB, whichever comes first | Refuses new builds, prunes (below), reports |
| `CRITICAL` | `free` is below 5% of `total` | Also refuses deploys, and deletes the reserve file |

A level returns to `OK` only once `free` is above both thresholds again, so it does not flap.

Refusals are `REJECTED` results (`docs/specs/protocol.md`) that give the level, the free space and what was pruned. Running apps keep running at every level; `SwitchRelease` and `Rollback` are never refused.

## Prune order

On entering `LOW`, and on every check while still there, until the level is `OK` or nothing is left to prune:

1. **Build cache**, with the Engine's build-cache prune, down to the cap below.
2. **Unused images** that are not one of the five kept releases of any app (`docs/specs/release.md`, "Keep five"), with the Engine's image remove, oldest first.

Never pruned: volumes, the images of any app's current and previous release, and any container. Docker's log files are not pruned by the agent: every container the agent creates uses the `local` log driver capped at 3 × 10 MB (`docs/specs/release.md`), so logs are bounded at creation.

**Build cache cap:** 2 GiB, applied after every build as well as on `LOW`.

## Reserve file

`<data-dir>/reserve`, 256 MiB of real blocks (written, not sparse), mode 0600. The agent creates it at start when the level is `OK`, deletes it on entering `CRITICAL` (giving SQLite and certificate renewal room), and recreates it once the level is back to `OK`.

## Reporting

Every `Heartbeat` carries the `disk` report (W3-6, `docs/specs/protocol.md`, W3-7). The field is optional on the wire, and the control plane treats an absent report as not measured. The control plane keeps the latest report per connected server in memory. Showing it (the servers screen) and alerting on it (notifications, W4-7) are later tickets. A change of level is also written to the agent's log.

## Tests

The free and total figures come from an injectable function, so tests can set any level without filling a disk. Pruning is tested against a real Engine: the test creates build cache and images under its own run's project, and asserts the prune order and that kept images survive.
