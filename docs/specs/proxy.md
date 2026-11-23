# Proxy process

W1-5 embeds Caddy 2.11.9 as the Moonglass proxy process (`moonglass proxy`), per ADR-0001. SPIKE-1 decides the open questions of ADR-0001; the defaults below answer them without deciding them, and the ADR is used as written. The Caddy dependency is ADR-0011.

## Process and binary

`moonglass proxy --data-dir <dir> [--admin-socket <path>]` is a subcommand of the same static binary. It runs until SIGINT or SIGTERM, then stops Caddy. A missing or empty `--data-dir`, or an empty `--admin-socket`, prints the usage and exits 2. The code is `internal/proxy`; `cmd/moonglass` calls `proxy.Run` and does not import Caddy. depguard denies Caddy from `cmd/`, `internal/agent`, `internal/keys`, `internal/protocol` and `internal/policy`. CGO is disabled. We do not fork or patch Caddy.

The proxy links only the Caddy packages it serves with: the http app with `reverse_proxy`, the tls and pki apps, file-system storage and logging. It never touches Docker.

## Admin API

Caddy's admin API is bound to a Unix socket. The agent-facing socket is `--admin-socket PATH`, default `/run/moonglass-proxy/admin.sock`, mode 0660, group `moonglass-proxy-admin`, in the systemd runtime directory `/run/moonglass-proxy`, mode 0750; tests pass a temp path. The proxy refuses to start when the path exists and is not a socket, and removes a stale socket there. Caddy's own inner socket is `<data-dir>/proxy-caddy.sock`, mode 0600, in a data dir of mode 0700. There is no TCP admin port. Configuration is a complete JSON document pushed to that API (`POST /load`). No Caddyfile, no labels, no templating.

On start the proxy loads a minimal config: the admin socket, file-system storage under `<data-dir>/caddy`, and log level WARN. It also keeps Caddy's autosave file at `<data-dir>/caddy-autosave.json`, not in the user's home. Every config pushed afterwards is complete and must repeat `admin` (exactly `{"listen": proxy.AdminListenAddress}`) and `storage` (exactly the file_system module at `proxy.StorageDir`). The proxy enforces this: the agent-facing socket (`--admin-socket`) is a guard in front of Caddy's own admin socket `<data-dir>/proxy-caddy.sock` (0600, data dir 0700). It forwards GET and HEAD, accepts only `POST /load` with a config that passes the check (otherwise 400 with the reason), and refuses every other mutation (403).

Certificates and their private keys stay in `<data-dir>/caddy`, owned by Caddy. The config the agent pushes is specified in `docs/specs/release.md`, "Proxy config".

## Counter module

One Caddy handler, `moonglass_counter`, in `internal/proxy`. Put it first in a route's `handle` list:

```json
{"handler": "moonglass_counter", "release": "rel_123"}
```

`release` is required. For each release the module counts requests, responses by status class (`1xx` to `5xx`) and latency: the total in nanoseconds and a histogram. A request that ends in a Caddy handler error is counted with that error's status (500 when it has none). The counts live in the process, so a config reload keeps them and a proxy restart resets them.

The counts are read over the same admin socket: `GET /moonglass/counters` returns

```json
{
  "releases": {"rel_123": {"requests": 1, "status": {"1xx": 0, "2xx": 1, "3xx": 0, "4xx": 0, "5xx": 0},
                           "latency_nanos_total": 1234567, "latency_buckets": [1, 0, 0, 0, 0, 0, 0, 0, 0]}},
  "latency_bounds_ms": [5, 10, 25, 50, 100, 250, 500, 1000]
}
```

`latency_buckets` are not cumulative: bucket `i` holds requests that took at most `latency_bounds_ms[i]` ms and more than the previous bound; the last bucket holds slower requests. This is enough for later auto-rollback (W4-3) to consume; this ticket builds no rollback.

## One HTTPS route

With a pushed config, the proxy serves one route over HTTPS. Locally and in tests, a `tls` automation policy with the `internal` issuer signs the certificate (no public ACME, no network to Let's Encrypt), and the `pki` app sets `install_trust: false` so Caddy never touches the system trust store. A fleet / production config path uses ACME staging (a documented config choice, not a live ACME call in CI). Tests speak HTTPS to a localhost port and trust only the local CA root, which `GET /pki/ca/local` returns over the admin socket. A `GET` of the route returns success, and the counter records the request.

The upstream for that test route is a localhost test handler; tests pull no Docker images and start no app containers.

## Defaults for ADR-0001 open questions (do not close the ADR)

- **Upgrade mechanism:** out of scope. No systemd socket activation and no `SO_REUSEPORT`. Until SPIKE-1 picks one, a proxy upgrade is a short, announced, drained restart, as ADR-0011 records.
- **Measured memory:** out of scope (W1-7). No long-running memory jobs and no new CI images.
- **Two processes during handover vs the 100 MB ceiling:** not applicable until a handover is implemented.

## Out of scope

Agent upgrades, draining integration with deploy (W2-4), install units (W1-6), memory budget CI (W1-7), ACME production, Docker. Enrolment and the control plane (W1-3).
