# ADR-0001: The agent and the proxy are two processes

- **Status:** Accepted
- **Date:** 2026-11-16
- **Amended:** 2026-11-17, decision 3: who can reach the admin socket (owner decision D10, weeks 2–3 preflight)

## Context

The server side is authoritative for runtime: routing and certificates must keep working when the control plane is unreachable and when the agent is restarting or upgrading (S rule 2). The agent ships often; the proxy changes only when Caddy or our counter module changes (S§3.3). Revision 2 of the stack embedded Caddy in the agent and relied on a socket handover that had to be proven in week 1.

## Decision

1. `moonglass agent` and `moonglass proxy` are separate processes from the same static binary, each under its own systemd unit and memory limit (S rule 1, S§3.3).
2. The proxy embeds Caddy 2.11.9 as a library. The agent role never loads Caddy. We never fork or patch Caddy.
3. The agent pushes a complete JSON config to Caddy's admin API over a Unix socket. There is no TCP admin port, no Caddyfile, no labels and no templating. The agent (user `moonglass`) and the proxy (user `moonglass-proxy`) are different users (`docs/specs/install.md`), so access to the socket is by group:
   - The socket is `/run/moonglass-proxy/admin.sock`, mode 0660, in the systemd runtime directory `/run/moonglass-proxy`, mode 0750. Both are owned by `moonglass-proxy` with the group `moonglass-proxy-admin`.
   - `moonglass-proxy-admin` is a system group with no members in `/etc/group`. The proxy unit runs with it as its primary group, and the agent unit adds it as a supplementary group. No other user is given it.
   - The proxy's data directory, certificates and Caddy's own inner admin socket stay in `/var/lib/moonglass/proxy`, mode 0700, readable by the proxy only.
4. Our only Caddy extension is one small module that counts requests, status classes and latency per release. The agent reads those counters over the same socket (S§3.3, S§5.4).
5. Only the agent holds the Docker socket. The proxy reaches the network, containers by their bridge IP, and its own certificate store (S§3.3, S§3.6).
6. Caddy owns certificates end to end; private keys stay on the server (S§3.4).
7. A proxy upgrade is its own drained, announced operation, separate from agent upgrades. WebSockets get the same 30-second drain window as a deploy (S§5.3), then close (S§3.3, S§3.14).

## Consequences

- The agent can restart, crash or self-update while the proxy keeps serving. The week 1 demo upgrades the agent under load with zero failed requests (M§3).
- There are two processes to supervise, and the Go runtime baseline is paid twice, roughly 8 MB each (S§1).
- Memory budget: agent about 32 MB, proxy about 45 MB, 100 MB hard ceiling for both, measured in CI from week 1 (S§6). SPIKE-1 passes at proxy RSS ≤ 60 MB and combined RSS ≤ 100 MB at the 99th percentile (M§2.2). Caddy's 32 MB idle figure is an estimate until SPIKE-1 measures it.
- The proxy has less authority than the agent, which narrows what a proxy bug can reach.
- The deploy-correctness suite covers agent upgrades and proxy upgrades (S§7).

## Alternatives rejected

- **Caddy embedded in the agent with a socket handover** (revision 2): every agent upgrade would touch traffic.
- **Traefik:** CPU complaints, and label-driven config (S§3.3).
- **Pingora:** the best footprint, but a framework to assemble and own.
- **kamal-proxy:** built for switching, without Caddy's certificate engine.

## Open questions

Left open on purpose. SPIKE-1 closes all three by amending this ADR (B§6, M§2.2); nothing else decides them.

1. **Proxy upgrade mechanism:** systemd socket activation or `SO_REUSEPORT`. Whether Caddy 2.11.9 accepts inherited listeners cleanly is unverified (S§3.3). The spike picks exactly one and the other is never built. If neither upgrades under load with zero failed requests, a proxy upgrade is a short, announced, drained restart, and the docs say so.
2. **Measured memory** of each process, replacing the estimates above.
3. **Whether two proxy processes during a handover count against the 100 MB ceiling**, which matters only if the spike picks `SO_REUSEPORT`.

## Links

- S rules 1 and 2, S§1, S§3.3, S§3.4, S§3.6, S§6, S§9
- M§2.2 experiment 2, M§3 week 1
- B§6: SPIKE-1, W1-5, W1-6
- `docs/threat-model.md` §3, boundary B4
