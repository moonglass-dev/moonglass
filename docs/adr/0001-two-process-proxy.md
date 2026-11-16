# ADR-0001: The agent and the proxy are two processes

- **Status:** Accepted
- **Date:** 2026-11-16

## Context

The server side is authoritative for runtime: routing and certificates must keep working when the control plane is unreachable and when the agent is restarting or upgrading (S rule 2). The agent ships often; the proxy changes only when Caddy or our counter module changes (S§3.3). Revision 2 of the stack embedded Caddy in the agent and relied on a socket handover that had to be proven in week 1.

## Decision

1. `moonglass agent` and `moonglass proxy` are separate processes from the same static binary, each under its own systemd unit and memory limit (S rule 1, S§3.3).
2. The proxy embeds Caddy 2.11.9 as a library. The agent role never loads Caddy. We never fork or patch Caddy.
3. The agent pushes a complete JSON config to Caddy's admin API, bound to a Unix socket readable only by the Moonglass service user. There is no TCP admin port, no Caddyfile, no labels and no templating.
4. Our only Caddy extension is one small module that counts requests, status classes and latency per release. The agent reads those counters over the same socket (S§3.3, S§5.4).
5. Only the agent holds the Docker socket. The proxy reaches the network, containers by their bridge IP, and its own certificate store (S§3.3, S§3.6).
6. Caddy owns certificates end to end; private keys stay on the server (S§3.4).
7. A proxy upgrade is its own drained, announced operation, separate from agent upgrades. Long-lived connections get a drain window, then close (S§3.3, S§3.14).

## Consequences

- The agent can restart, crash or self-update while the proxy keeps serving. The week 1 demo upgrades the agent under load with zero failed requests (M§3).
- There are two processes to supervise, and the Go runtime baseline is paid twice, roughly 8 MB each (S§1).
- Memory budget: agent about 32 MB, proxy about 45 MB, 100 MB hard ceiling for both, measured in CI from week 1 (S§6).
- The proxy has less authority than the agent, which narrows what a proxy bug can reach.
- The deploy-correctness suite covers agent upgrades and proxy upgrades (S§7).

## Alternatives rejected

- **Caddy embedded in the agent with a socket handover** (revision 2): every agent upgrade would touch traffic.
- **Traefik:** CPU complaints, and label-driven config (S§3.3).
- **Pingora:** the best footprint, but a framework to assemble and own.
- **kamal-proxy:** built for switching, without Caddy's certificate engine.

## Open questions

1. **Proxy upgrade mechanism.** Decided by SPIKE-1 (B§6, M§2.2). The spike picks exactly one candidate; this ADR is then amended to name it, and the other is never built.
   - **Candidate A, systemd socket activation:** the listening sockets outlive the process. Whether Caddy 2.11.9 accepts inherited listeners cleanly is unverified (S§3.3). It would add socket units to `deploy/systemd`, which is trusted core.
   - **Candidate B, `SO_REUSEPORT`:** the new proxy process binds alongside the old one, which drains and exits. Two proxy processes run at once during the handover, so their combined memory counts against the ceiling for that window (_Proposed_ reading; the spike measures it).
   - M§2.2 names a third result: if neither candidate upgrades under load with zero failed requests, no handover is built, a proxy upgrade is a short, announced, drained restart, and the docs say so. That result is also recorded here by amendment.
2. **Drain window for a proxy upgrade.** S§5.3 sets 30 seconds as the default for WebSockets during a deploy. The plan does not say whether a proxy upgrade uses the same value.
3. **Proxy idle memory.** The 32 MB for Caddy is an estimate until SPIKE-1 measures it (S§6, "Not verified").

## Links

- S rules 1 and 2, S§1, S§3.3, S§3.4, S§3.6, S§6, S§9
- M§2.2 experiment 2, M§3 week 1
- B§6: SPIKE-1, W1-5, W1-6
- `docs/threat-model.md` §3, boundary B4
