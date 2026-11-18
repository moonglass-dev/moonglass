# Memory budget

W1-7 enforces the G1 gate from stack §6 in CI: `moonglass agent` and `moonglass proxy` together stay at or under 100 MiB of idle resident memory. SPIKE-1 is deferred and ADR-0001 is unchanged; this spec adds no new public number.

## Ceiling

The hard ceiling is **102400 KiB** (100 MiB) for the sum of the two processes' RSS. The 77 MB figure in the stack is a target, not the fail line. Raising the ceiling is a decision for the plan, not a change to the test.

## What is measured

`TestProxyAndAgentIdleUnderBudget` in `internal/proxy`:

1. Builds the static binary (`CGO_ENABLED=0`, no `-race`) into a temp dir, so the sample is the shipped build.
2. Starts a control plane in the test process on `127.0.0.1:0` (enrolment over plain HTTP, agent endpoint over TLS 1.3 WSS) and enrols an agent into a temp data dir.
3. Starts `moonglass proxy --data-dir <temp> --admin-socket <temp>` and `moonglass agent connect --data-dir <temp> --url https://<loopback>` as real processes.
4. Waits until the agent has reached the control plane and the proxy admin socket exists, then lets both sit idle for 3 seconds. Nothing is deployed and no requests are made.
5. Samples each process once with `scripts/measure-memory --pid PID` and fails if proxy plus agent is greater than 102400 KiB. The failure message names both values.

Every port is `:0` and every directory is temp, so two worktrees can run the check at once. It needs no Docker, no Postgres and no non-loopback network, and reads no cloud token. The enrolment token is never printed.

## The script

`scripts/measure-memory --pid PID` prints `rss_kib <KiB> pid <PID>`. It reads `VmRSS` from `/proc/<pid>/status` on Linux and `ps -o rss=` on macOS, and exits non-zero on other platforms or when the process is gone.

## CI

`make memory-budget` runs the test alone. It also runs under `make test`, a step of `make ci`, which GitHub Actions runs on `ubuntu-latest`.

## Not covered

48-hour growth, p99 under load and proxy-upgrade memory are SPIKE-1.
