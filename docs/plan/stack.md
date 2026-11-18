# Moonglass — Phase 3: Stack (revision 3)

_2026-11-15, revised 2026-11-16 after an outside review. Builds on `research.md` (R§n) and `vision.md` (V§n). Versions re-verified live 2026-11-15; sources at the end._

**What changed in revision 3.**
- **The proxy is a second process from day one.** `moonglass proxy` runs beside `moonglass agent`; both come from the same binary. This replaces Caddy embedded in the agent with a socket handover.
- **Hosted uses one Postgres database** with `org_id` isolation enforced by row-level security. Self-hosted keeps SQLite. This replaces per-org SQLite files plus Litestream.
- **A real secrets key design** (§3.11): per-environment keys, an owner recovery key, wrapping, granting and rotation. It also states plainly what browser-side encryption does and does not protect.
- **Container-spec sanitization** in the agent (§3.2). Without it, "a short, typed list of things" would not be true.
- **New in the MVP:** pre-deploy steps, disk-full handling, `moonglass import`, signed releases with a canary channel.
- **Moved to v1:** passkeys and TOTP, Railpack, zero-downtime Compose, metrics charts, managed subdomains.

Revision 2 decisions that still stand: Go core, Caddy, Svelte UI, code written by agents with you orchestrating.

Priorities, in your order: security, performance, footprint, UX. One constraint shapes almost every choice: **one person operates this, and agents write it.**

## Five rules the stack follows

1. **One binary, four roles.** `moonglass server`, `moonglass agent`, `moonglass proxy` and the CLI (including `moonglass import`) are the same static executable. A server runs the agent and the proxy as two systemd units. Self-hosting on one box adds the server role.
2. **The server side is authoritative for runtime.** Routing, certificates, health checks, auto-rollback and scheduled backups run on the user's server and keep working when the control plane is unreachable. The proxy keeps serving when the agent is restarting or upgrading. The control plane coordinates; it is never in the request path.
3. **One codebase for hosted and self-hosted.** The commercial module (approver rules, agent policy rules, SAML/SCIM, compliance exports, plus hosted-only billing, entitlements and abuse) lives in a separate repository and is compiled into the hosted build and the licensed self-hosted build. The open-source build contains no plan or licence checks (V§5.2, V§6).
4. **Reuse anything that parses hostile input or does cryptography. Build only what is the product.**
5. **Nothing that needs its own on-call.** No Redis, no message broker, no Kubernetes, no self-run database cluster. Hosted Postgres is a provider's managed service.

---

## 1. Why Go, stated once

- **The proxy is Go either way.** With Caddy serving every request, a Rust core buys no performance on the hot path.
- **Go keeps a single executable.** Caddy is designed to be embedded in Go programs. The proxy role embeds it, and the agent role does not load it.
- **Most of the domain is Go libraries:** Docker's client, Compose's parser, Caddy and its certificate engine, the reference implementation of age, the official MCP SDK. Rule 4 is usually "import it". The exception is Railpack (v1), which has no stable public Go API. We drive it through its CLI and BuildKit frontend.
- **You can read it.** With agents writing the code, a language you can skim and debug matters more than one with a stricter compiler.
- **Cost:** a garbage-collected runtime, now paid twice (two processes, roughly 8 MB of baseline each), and no "written in Rust" badge. Komodo and Temps already hold that badge (R§3).

---

## 2. The stack at a glance

| Layer | Choice | Build or reuse | Main tradeoff |
|---|---|---|---|
| Language and runtime | Go, cgo disabled, fully static x86_64 and arm64 binaries; memory limit set explicitly per role | Reuse | GC baseline in each process |
| Agent ↔ control plane | One outbound WebSocket over TLS 1.3 on port 443; protobuf messages; each agent has its own Ed25519 identity | **Build** the protocol; reuse Go's TLS, `coder/websocket`, protobuf | WebSocket passes every firewall and CDN; costs gRPC's ready-made tooling |
| Container runtime | Docker Engine 25+ through its Unix socket, with Docker's own Go client | Reuse | Socket access is root-equivalent, so the agent sanitizes every spec it sends (§3.2) |
| Container-spec sanitization | Deny-by-default check on every container, build and pre-deploy step; exceptions only in `/etc/moonglass/agent.toml` | **Build** (small, critical) | Some legitimate tools (node exporters, backup agents) need an explicit allow entry |
| Reverse proxy | **Caddy 2.11.9 embedded in `moonglass proxy`, a separate process**, plus one small Caddy module of ours for per-release counters | Reuse Caddy; **build** the module and the switch/drain logic | Two processes to supervise; proxy upgrades still need care (§3.3) |
| TLS and certificates | Caddy's CertMagic: automatic HTTPS, ARI-driven renewal, HTTP/3, DNS-01 through `libdns` providers | Reuse | None worth noting |
| Builds | Dockerfile or a prebuilt image through BuildKit; a build-in-CI template. **Railpack in v1**, via its CLI and BuildKit frontend | Reuse | No zero-config builds at launch |
| Compose | `compose-go`, the official parser, then our own reconciler through the Engine API | Reuse the parser; **build** the reconciler | We own Compose runtime semantics for the subset we support |
| Networking | One Docker bridge network per project. Cross-server WireGuard mesh in v1 | Reuse kernel WireGuard | No private networking between servers at launch |
| Control-plane storage | **Hosted: one managed Postgres** with `org_id` on every row and row-level security. **Self-hosted: SQLite** (pure-Go driver). One storage interface, one conformance suite for both | Reuse (`pgx`, `modernc.org/sqlite`) | Two backends to keep in step (§3.7) |
| Agent storage | SQLite on the server | Reuse | None worth noting |
| Logs and metrics | Docker's `local` log driver, streamed on demand; cgroup v2 samples rolled up into SQLite. Charts in v1 | **Build** the sampler | No full-text log search, no charts at launch |
| Backups | Native dump tools → zstd → age, encrypted to the **environment key** → any S3-compatible bucket; volumes through restic in v1 | Reuse | restic is a second executable, run on demand |
| Secrets | age (X25519): envelope keys per environment, an owner recovery key, wrapping and rotation (§3.11). The reference Go library on servers and CLI; the same author's TypeScript implementation in the browser | Reuse the crypto; **build** key distribution | Losing every server *and* the recovery key loses the secrets, by design |
| API | Go's standard HTTP server with Huma generating OpenAPI from Go types; one operation registry feeds REST, CLI and MCP | Reuse | Discipline: no operation exists outside the registry |
| MCP | Official Go SDK v1.7+, spec 2026-07-28, stateless Streamable HTTP, OAuth 2.1 | Reuse | None; Go is a Tier 1 SDK |
| Auth | **MVP:** GitHub and Google sign-in (email links dropped, ADR-0005 amended 2026-11-18), one-time login links printed on a self-hosted box, server-side sessions, step-up for approvals. **v1:** passkeys (`go-webauthn`), TOTP, generic OIDC | Reuse libraries; **build** the flows | MFA depends on GitHub until v1 |
| API and agent tokens | Opaque random tokens, stored hashed, with scope rows | **Build** (small) | No offline verification or delegation yet |
| Web UI | **Svelte 5 with SvelteKit in static mode**, Tailwind, Bits UI primitives; shipped inside the binary | Reuse | Smaller component ecosystem than React |
| Billing | Stripe, in the commercial module | Reuse | Sales tax is yours unless you use a merchant of record |
| Releases | Signed artifacts, a **canary** and a **stable** channel, staged agent self-update with automatic revert (§3.14) | Reuse signing tools; **build** the channel logic | The signing key becomes the most sensitive thing you own |

---

## 3. Layer by layer

### 3.1 Agent ↔ control plane

- **Outbound only.** The agent dials `wss://` on 443. The server needs no inbound port except 80 and 443 for its own apps. Komodo moved to this model in v2 (R§3).
- **Typed commands.** Every message is a protobuf type: `Deploy`, `SwitchRelease`, `Rollback`, `RunPreDeploy`, `StopService`, `RunBackup`, `Restore`, `UpdateRoutes`, `RewrapEnvKey`, and so on. No message carries a shell string for the host. The `.proto` file is the public, auditable list of everything the control plane can ask a server to do.
- **Enrolment.** `moonglass agent enroll <one-time token>` generates the agent's keys on the server, pins the control plane's public key, registers, and **prints the agent's key fingerprint** in the terminal. The secrets flow uses that fingerprint (§3.11). Re-enrolling to another control plane is the portability path (V§6).
- **Local policy beats remote command.** `/etc/moonglass/agent.toml` on the server can forbid classes of command outright: no container exec, no deletes, deploys only from listed repositories or registries. It also holds the container-spec exceptions (§3.2). The control plane cannot change it.
- **Rejected:** gRPC (more middleboxes mangle it, and we need only one long-lived stream); QUIC (UDP is filtered on enough networks to cause support load); SSH from the control plane (the design behind the incumbents' worst CVEs, R§3).

### 3.2 Container runtime and container-spec sanitization

- Docker Engine through the socket. Current line is 29.x; new installs use the containerd image store by default, and nftables support is opt-in. Test against 25 through 29.
- The agent and proxy run as systemd services from the static binary, not as containers. They survive Docker restarts and have nothing to bootstrap.
- Podman's Docker-compatible socket should work later; not a launch target.
- **Rejected:** talking to containerd directly (users lose the `docker` CLI they know); any custom runtime.

**Sanitization.** A typed protocol is not enough on its own. A control plane that can say "deploy this container" with `privileged: true` and a bind mount of `/` already has a root shell. So the agent checks **every** container spec it is about to create: apps, Compose services, one-click databases, pre-deploy steps, previews and backup jobs. It rejects the spec unless `agent.toml` explicitly allows the exception:

| Denied by default | Why |
|---|---|
| `privileged: true` | Full host access |
| Bind mounts from host paths (anything other than named volumes and the agent-managed data directory) | Host filesystem access |
| Any mount of `docker.sock`, `containerd.sock` or the Docker data root, under any path or alias | Root-equivalent control of the host |
| `network_mode: host`, `pid: host`, `ipc: host`, `uts: host`, `userns_mode: host`, `network_mode: container:<id>` across projects | Host or cross-project namespaces |
| `cap_add` (any), `devices`, `device_cgroup_rules` | Kernel attack surface and device access |
| `security_opt` that disables seccomp, AppArmor or SELinux | Removes the default confinement |
| `sysctls` outside a short namespaced allow-list, a custom `cgroup_parent`, a non-default `runtime` | Escape routes and resource evasion |
| BuildKit entitlements `security.insecure` and `network.host` | The same escapes, at build time |

Exceptions are scoped, never global:

```toml
# /etc/moonglass/agent.toml
[[containers.allow]]
project = "monitoring"
image   = "prom/node-exporter"      # digest pinning supported
pid_host = true
bind_mounts = ["/proc:/host/proc:ro", "/sys:/host/sys:ro"]
```

- A rejected spec fails the deploy with an error that names the field and shows the `agent.toml` lines that would allow it. Nothing is silently stripped.
- Compose files are checked after `compose-go` has resolved interpolation, `extends` and profiles, so an escape cannot hide in a variable.
- The check has its own permanent test suite (§7). It is written in week 1, before any `Deploy` exists.
- **What sanitization does not cover:** a kernel bug or container-runtime escape, a malicious image doing what images can do inside their sandbox, and an app leaking its own secrets. The threat model says so (§4).

### 3.3 Reverse proxy: Caddy, as its own process

- **`moonglass proxy` embeds Caddy as a library**, under its own systemd unit and memory limit. The agent role never loads Caddy.
- **Agent → proxy over a Unix socket.** The agent pushes a complete JSON config to Caddy's admin API, bound to a Unix socket readable only by the Moonglass service user. No TCP admin port, no Caddyfile, no labels, no templating. Caddy reloads gracefully without dropping connections.
- **We never fork or patch Caddy.** Our only extension is one small module in the proxy that counts requests, status classes and latency per release. The agent reads those counters over the same socket for auto-rollback (§5.4).
- **Caddy owns certificates end to end.** Issuance, storage, renewal. We do not write ACME code.

**Why two processes from day one:**
- **Agent upgrades never touch traffic.** The agent ships often; it can restart, crash or self-update while the proxy keeps serving.
- **Proxy upgrades are rare**, needed only when Caddy or our counter module changes. They are a separate, drained, announced operation.
- **The proxy has less authority.** It reaches the network and its own certificate store; only the agent holds the Docker socket.
- This removes the socket-handover design that revision 2 had to prove in week 1.

**Proxy upgrades without dropped requests:** the preferred path is systemd socket activation, so the listening sockets outlive the process. Whether Caddy 2.11.9 accepts inherited listeners cleanly is **unverified** (Caddy's network-address docs list TCP, UDP and Unix sockets). The week 0 spike tests it against the one alternative, where the new proxy process binds alongside the old one with `SO_REUSEPORT` and the old one drains and exits. **The spike picks exactly one; the other is never built.** Long-lived connections (WebSockets) get a drain window, then close.

**Rejected:** Traefik (CPU complaints in R§3, and label-driven config is the "magic" users dislike); Pingora (best footprint, but a framework to assemble and own); kamal-proxy (purpose-built for switching, without Caddy's certificate engine).

### 3.4 Certificates and managed subdomains

- CertMagic renews on ARI rather than a timer. That matters because Let's Encrypt's default lifetime drops to 64 days in February 2027 and 45 days in February 2028.
- Keys are generated and kept on the server. The control plane never holds a certificate private key.
- Wildcards use DNS-01; Cloudflare first, more providers through `libdns` in v1.
- **Managed subdomains move to v1** and are not on the Free plan (V§5.1). Getting them right needs time we cannot shorten:
  - A **separate registrable user-content domain**, not a subdomain of moonglass.dev, for cookie and phishing isolation (V§8, open decision 2).
  - A **Public Suffix List** entry. The PSL has no SLA. Merged requests often land in one to two weeks, but some sit for months. It explicitly rejects entries whose purpose is to get around Let's Encrypt limits, so we apply on the legitimate ground: browser cookie and security isolation between customers.
  - **Let's Encrypt's limit is 50 certificates per registered domain per week.** Without a PSL entry, every customer subdomain shares that limit. We separately request a rate-limit adjustment from Let's Encrypt as a hosting provider.
  - **A shared wildcard certificate is not an option**: its private key would have to sit on every customer's server.
- Custom domains always work and are unlimited.

### 3.5 Builds

- **MVP:** Dockerfile and prebuilt image, both through BuildKit driven by `docker buildx`. The docs ship short Dockerfiles for the common stacks; coding agents write the rest well.
- **First-class "build in CI" path:** a GitHub Actions template that builds, pushes to GHCR and calls `moonglass deploy --image <digest>`. Zero RAM on the server, which matters on a 2 GB VPS.
- **Build on the target server by default, one build at a time**, with the sanitization rules applied to BuildKit entitlements.
- **Rollback without a registry.** Keep the last five release images in the server's local store. This avoids Dokploy's requirement for an external registry (R§3).
- **Cap the build cache** (Coolify has an open issue for Railpack's BuildKit cache growing by 20 GB between cleanups), and stop builds before the disk fills (§3.13).
- **Never auto-build pull requests from forks.**
- **v1: Railpack** (v0.43.0 today, pre-1.0, frequent releases) for zero-config builds. We use `railpack prepare` plus its BuildKit frontend from a pinned, signature-checked CLI version, not an imported Go library, because it has no stable public API. We upgrade it deliberately, behind the canary channel.

### 3.6 Networking

- One bridge network per project; nothing crosses projects unless declared. Dokploy has open issues about a shared network causing cross-project DNS collisions.
- The proxy reaches containers by their bridge IP; apps publish no host ports unless the project asks.
- **v1:** WireGuard mesh between a user's servers using the kernel module, keys generated on each server, the control plane distributing only public keys and endpoints.

### 3.7 Storage

**Hosted: one managed Postgres. Self-hosted: SQLite.**

Why the hosted side changed:
- **Thousands of per-org SQLite files plus Litestream** means thousands of replication streams, file-level restores and a routing table. That is too much custom operations work for one person.
- **Row-level security is a second lock on tenant isolation.** Every table carries `org_id`. Each request sets `app.org_id` for its transaction, and RLS policies refuse rows from any other org. A missing `WHERE` clause in agent-written code then returns nothing instead of another customer's data. For a security product written by agents, that is worth a second backend.
- **A managed provider gives standby, failover and point-in-time recovery** without our own on-call (rule 5).
- **Control-plane nodes become stateless.** Agents can connect to any node. Commands are routed to the node holding an agent's connection through Postgres `LISTEN/NOTIFY`, so there is still no broker. Scaling out is adding a node, not sharding.

Why self-hosted stays SQLite:
- It is one binary with no database server to run, upgrade or secure. The pure-Go driver keeps the binary static.
- The `org_id` columns exist in SQLite too, so the schema is the same shape everywhere.
- Control-plane backup: a scheduled consistent snapshot (`VACUUM INTO`), age-encrypted to the owner's recovery key, uploaded to the user's bucket.

Keeping two backends honest:
- **One storage interface**, two implementations (`store/pg`, `store/sqlite`). Nothing outside it writes SQL.
- **One conformance suite** runs against both on every change (§7). It covers the same fixtures, the same queries and the same migrations. A CI check confirms that the two schemas describe the same tables and columns.
- **SQL kept to the common subset.** Postgres-only features are allowed only inside the storage package (RLS policies, `LISTEN/NOTIFY`).
- **Export is logical, not a file copy:** a versioned bundle that either backend can import (V§6).

**Rejected:** per-org SQLite on hosted (above); Postgres for self-hosted (breaks the single-binary install); a distributed SQLite with consensus (a cluster to operate); Turso (too young to hold a control plane).

### 3.8 Logs and metrics

- Containers log through Docker's `local` driver with rotation and a size cap. The agent streams and greps on request. Nothing is shipped to us.
- The agent samples CPU, memory, network and disk from cgroup v2 files. At launch the UI shows **current** usage; 10-second points rolled up to one minute and one hour in SQLite feed the **charts in v1**. The collection exists from day one because disk-full handling and auto-rollback need it.
- Proxy counters per release feed auto-rollback now and the charts later.
- Later: optional OpenTelemetry export.
- **Rejected:** bundling a log or metrics database. Even the light ones cost more idle memory than the whole agent.

### 3.9 Backups and restore

- **Databases:** run the engine's own dump tool inside the container (a sanitized one-off), stream through zstd and age, upload to the user's bucket. No temp files on disk.
- **Encrypted to the environment key, not to the server's key** (§3.11). A dead server must not take its backups with it. Restoring onto a new server needs that server granted the environment key, or the owner's recovery key.
- **Restore is a first-class button.** The week 6 demo restores onto a *different* server using only the recovery key.
- **Volumes (v1):** restic, bundled and run on demand, so it costs disk, not idle memory. Also in v1, a scheduled **restore drill**: restore into a scratch container, run a check query, report. This answers the top day-2 complaint about Coolify (R§3, R§10).
- **Later:** point-in-time recovery for Postgres.

### 3.10 Auth and sessions

**MVP**
- **Hosted:** GitHub and Google sign-in (ADR-0005 as amended on 2026-11-18; email magic links are not in the MVP). No passwords. GitHub requires 2FA for most active developer accounts, so a GitHub sign-in is usually two-factor.
- **Self-hosted:** the first admin is created with a one-time bootstrap token printed in the terminal; there is never an open registration window. Dokploy had a pre-authentication admin takeover, and Coolify has since restricted its first-admin registration to a short window after start (R§3). Day-to-day sign-in uses `moonglass server login-link`, which prints a one-time link on the box. Access to the server is the credential, so it needs no SMTP or OAuth app. GitHub sign-in reuses the GitHub App created for git sources. Self-hosted has no email sign-in in the MVP, so there is no SMTP to configure.
- **Step-up for anything that matters.** Approving a protected deploy, a destructive operation or a change to secret recipients requires a sign-in fresher than ten minutes: a new GitHub or Google round trip, then an Approve on a page that shows the exact request (`docs/specs/auth.md`). That page is out of band from the coding agent, which is the point.
- Server-side sessions, listable and revocable, bound to `__Host-` cookies.

**v1:** passkeys and TOTP for everyone, free. Then approvals can require a real second factor. Generic OIDC sign-in follows.

**Commercial module:** SAML and SCIM (Enterprise).

**Tokens:** random, stored hashed, each with rows naming project, environment and allowed operations, an expiry and an owner. Every token belongs to a human or a named agent identity, and the audit trail records which. **Rejected for now:** capability tokens with offline verification. They are elegant, but they add a policy language users must learn.

### 3.11 Secrets and keys

The question the reviewer asked: if the control plane cannot decrypt secrets, how does a new or replacement server get them? The answer is envelope encryption with an owner recovery key.

**Keys**

| Key | Where it lives | Purpose |
|---|---|---|
| **Agent key** (X25519 + Ed25519) | Generated on each server at enrolment; never leaves it | Identity, and the recipient for wrapped environment keys |
| **Environment key** (an age X25519 key pair per environment) | Public half: control plane, browser, CLI. Private half: **only ever stored wrapped** | Secret values and database backups are encrypted to it |
| **Owner recovery key** (age identity) | Generated by `moonglass keys init` in the CLI and saved by the owner as a file or printed code. Up to two owners can hold one | Recovering, granting and rotating without a surviving server |

`moonglass keys init` is required before the first secret is saved. The UI asks once a quarter for a recovery-key check: the CLI proves it can unwrap a test value, without revealing anything.

**Wrapping**
- Each secret value is encrypted to the environment **public** key. The browser and CLI need nothing else, which is why values are write-only in the web UI.
- The environment private key is stored as wrapped copies, one per authorised agent and one per recovery key. The control plane holds only those wrapped copies.
- Database backups are encrypted to the environment public key. Exports carry ciphertext plus wrapped keys, and `age` can read them once the owner unwraps the environment key with the recovery key.

**Adding a server to an environment**
- **Unprotected environment:** the control plane asks an online agent in that environment to re-wrap the environment key to the new agent's public key (`RewrapEnvKey`). This happens after a human confirms the enrolment in the UI with step-up.
- **Protected environment:** agents refuse `RewrapEnvKey`. The owner runs `moonglass secrets grant --env prod --server <name>`. The CLI unwraps the environment key with the recovery key and shows the new server's key fingerprint. The owner checks it against the one `moonglass agent enroll` printed on that server, and only then does the CLI wrap the key. In v1, agent-verified approver signatures turn this into a UI flow.

**Replacing a server when every server in the environment is gone:** the owner runs `moonglass secrets grant` with the recovery key. **If every server is gone and no recovery key exists, the secrets and the database backups cannot be decrypted by anyone, including us.** The UI says this at `keys init`, on the pricing page and in the docs, in plain words.

**Rotation**
- **Removing a server, or suspecting one:** `moonglass secrets rotate --env <name>`. It creates a new environment key, re-encrypts every value (done by an online agent in the environment, or by the owner's CLI), re-wraps to the remaining recipients and deletes the old wrapped copies. Future backups use the new key, and old backups stay readable through the recovery key.
- **Rotation does not un-leak.** A removed server saw plaintext, so the UI lists which values to rotate at their source. In v1, Moonglass rotates the values it generated itself (one-click database passwords) automatically.
- **Agent key rotation:** the agent generates a new key and re-wraps the environment keys it holds to itself. On demand at launch; scheduled every 90 days in v1.
- **Recovery key rotation:** in the CLI, with the old recovery key or an online agent.

**Reveal:** `moonglass secrets reveal` has one path: the agent re-encrypts the value to a one-time CLI key. The recovery key is only for granting, rotating and recovering, never for everyday reads.

**Git access** uses a deploy key generated on the server, so the control plane holds no repository credentials after setup.

**What browser-side encryption protects, and what it does not**

| Protects against | Does not protect against |
|---|---|
| A stolen copy of our database or its backups: the Railway-style breach (R§3) | A takeover of our **running** service, which can serve JavaScript that captures values as you type, or hand your browser a substitute environment key |
| Our database provider, our logs, anyone reading rows | The same takeover deploying its own image to an **unprotected** environment and receiving that environment's secrets |
| Secrets appearing in our error reports or support tooling | A compromised user server, which must hold plaintext to run your app |
| | An app that logs its own environment |

The CLI is the stronger path. It is a signed binary, it pins each environment's public key on first use and warns if it changes, and it can do everything the web UI can. **In one sentence for the docs:** our database never holds a readable secret; if our running service were taken over, protected environments stay sealed and unprotected ones do not.

### 3.12 API, CLI and MCP from one source

- Every operation is declared once with its inputs, outputs, required scope and a `destructive` flag. REST routes, CLI subcommands and MCP tools are generated from that list. "MCP for everything" then costs nothing extra, and nothing is reachable by a coding agent without also being scoped and audited.
- MCP spec 2026-07-28 is stateless over plain HTTP, and the official Go SDK supports it from v1.7. That suits stateless control-plane nodes.
- A destructive operation called by an agent identity returns "pending approval" with a link. The human approves in the UI with step-up, or with `moonglass approve`. Enforcement is on the server, not a flag the caller sets (R§5).
- Only one deploy per app runs at a time; a newer one supersedes a queued older one. Coolify shipped a bug where its MCP deploy tool skipped that queue and took an app down (#12442, R§10).

### 3.13 The deploy pipeline: pre-deploy steps and a full disk

**Pre-deploy steps** (Dokploy #110, 43 votes, R§10)
- Declared in `moonglass.toml` as an argument list, for example `pre_deploy = ["bin/migrate"]`. They run in a one-off, sanitized container from the **new** image with the environment's secrets, after the build and before the switch.
- A failing step aborts the deploy, and the old release keeps serving.
- Steps are treated as **irreversible** by default, which turns off auto-rollback for that deploy (§5.4), unless marked `reversible = true`.
- This is user code inside the container sandbox, the same trust as the image itself. It adds no host command to the protocol.

**Disk-full handling.** Small VPSs fill up with images, build cache and logs, and a full disk breaks Docker, SQLite and certificate renewal together.
- The agent watches free space on the Docker data root and its own data directory.
- **Below 15% or 5 GB free:** refuse new builds, then prune in order: build cache, unused images beyond the last five releases, rotated logs. Alert.
- **Below 5%:** refuse deploys too. Running apps keep running.
- **Never pruned automatically:** volumes, and the images for the current and previous release.
- The agent keeps a 256 MB reserve file it can delete to give SQLite and certificate renewal room to recover.

### 3.14 Releases and updates

- **Signed releases.** Every binary, checksum file and SBOM is signed with a release key held offline on a hardware token. Builds come from CI with provenance attestations. The agent verifies the signature against a pinned public key before it installs anything.
- **Two channels.** `canary` gets every release first: your test fleet plus alpha users who opt in through `agent.toml`. `stable` gets it after at least 72 hours on canary with no regressions in the permanent suites or the fleet.
- **Staged rollout on hosted:** stable agent updates go to 10%, then 50%, then 100% of connected servers over about 48 hours. The rollout halts automatically if updated agents fail to reconnect or their health reports degrade.
- **Automatic revert:** the agent keeps the previous binary. If the new one has not reconnected and passed its self-check within five minutes, systemd starts the old one again.
- **The proxy updates separately**, only when Caddy or our module changes (§3.3).

### 3.15 Import

`moonglass import` is the first hour for people moving from somewhere else (V§3):
- **Compose:** a `compose.yaml` becomes a project. Services are classified and checked against the sanitization rules, and anything unsupported is reported before import, not after.
- **Coolify:** reads projects, applications, environment variables, domains and Compose stacks through Coolify's API with a token the user provides. The CLI encrypts the environment variables to the new environment key **locally** before upload, so no plaintext reaches us. Database contents move through a backup and restore, not live. The exact coverage of Coolify's API is to be confirmed during the week 0 migration interviews.
- **v1:** Dokploy, and whatever the interviews say people are leaving.

### 3.16 Web UI

**Choice: Svelte 5, SvelteKit in static mode, Tailwind, Bits UI.** Compiled to static files and embedded in the binary.

- Compiled output, small downloads, fast interaction.
- The UI costs the server nothing either way; it ships as static files.
- A typed API client is generated from the OpenAPI description, so the UI cannot drift from the API.
- Live updates over server-sent events.
- The canvas view wanted after v1 is available: the leading node-graph library ships a Svelte version.

| Alternative | Verdict |
|---|---|
| Solid | Slightly smaller and faster; much smaller component ecosystem |
| React | Largest ecosystem and strongest agent familiarity; heavier |
| Server-rendered pages with light scripting | Lightest, but browser-side encryption and live deploy views fit it poorly |
| Compile-to-WASM UI frameworks | Large downloads, immature for an app this size |

---

## 4. Threat model: what is true at each stage

| If an attacker gets… | SSH-from-panel design (incumbents) | Moonglass MVP | Moonglass v1, protected environments |
|---|---|---|---|
| A copy of the control-plane database or its backups | Whatever the panel stores to reach servers and run apps | Ciphertext secrets, wrapped environment keys they cannot unwrap, no server credentials, hashed tokens | Same |
| Full control of the running control plane | A root shell on every connected server | Can send any *typed* command: deploy an image or pre-deploy step to an unprotected environment (it runs inside a sanitized container), stop services, soft-delete, enrol a rogue server into an unprotected environment. **Cannot** get a host shell, start a privileged or host-namespace container, mount the Docker socket or host paths, read host files, override `agent.toml`, or obtain a protected environment's key | Also cannot deploy, change sources, change secret recipients or delete in a protected environment without a signature from an approver's key, verified by the agent |
| A leaked coding-agent or API token | Often everything (R§3, Railway) | Only that token's project, environment and operations; destructive calls wait for a human with step-up | Same |
| A malicious image | Host compromise | Confined to its container by the default Docker sandbox plus sanitization; it can still use whatever that environment's secrets reach | Same |
| A container or kernel escape | Host compromise | Host compromise. True of every product in the category | Same |

Three honest limits:

- **A takeover of the running control plane can deploy its own image into an unprotected environment, and that image receives that environment's secrets.** The MVP defends against the breach that hit Railway (stolen records plus a decryption credential, R§3), removes host root, and keeps protected environments' keys out of reach. v1 closes the deploy gap for protected environments with agent-verified approvals.
- **Browser-side encryption trusts the JavaScript we serve** (§3.11). The CLI path does not.
- **Lose every server and the recovery key, and the secrets and backups are gone.** That is the price of a control plane that cannot read them.

---

## 5. The tensions from `vision.md`, resolved

**1. Ciphertext-only control plane vs a usable UI.** Envelope keys and a recovery key (§3.11). Values are write-only in the web UI, like GitHub Actions secrets; reveal goes through the CLI. Builds, previews and pre-deploy steps decrypt on the user's server. Secrets export as standard age files.

**2. Web terminal vs "a short, typed list".**
- No host shell, ever. It is not in the protocol.
- Container exec is not in the MVP. In v1 it is CLI-first, human-only, requires a fresh second factor, is audited, is unavailable to agent identities, and can be switched off in `agent.toml`.

**3. Zero-downtime: exactly which shapes get it.** This list goes in the docs word for word.

| Shape | At launch | v1 |
|---|---|---|
| Single-service app (image, Dockerfile or git) behind the proxy, with a health check, no fixed `container_name`, no published host port, no single-writer volume | **Zero failed HTTP requests** across a deploy. In-flight requests finish; WebSockets get a drain window (default 30 s), then close and reconnect | Same |
| Same, but with a single-writer volume (an app writing its own SQLite file, say) | Stop-then-start, with a brief gap. The UI says so before you confirm | Same; it is inherent |
| App with a fixed `container_name` or a published host port | Stop-then-start, flagged in the UI | Same |
| Compose: services you did not change | **Never touched.** Coolify stops every container on every Compose deploy (R§3, #3767) | Same |
| Compose: services you changed | Restarted in place, with a brief gap for those services; the UI lists them before you confirm | **Blue/green** for services classified *swappable* (no published host port, no fixed name, no single-writer volume). *Pinned* services (typically databases) restart in place only when their own definition changed |
| Non-HTTP services (TCP, UDP) | Stop-then-start | Same |

Compose parsing uses the official library, so interpolation, profiles and `extends` behave as users expect. Unsupported fields produce an explicit error; nothing is silently dropped. Week 6 runs the swappable/pinned classifier over 30 popular Compose files to size the v1 work.

**4. Auto-rollback defaults: shadow mode first.**
- Watch for ten minutes after the switch. The previous release stays available.
- **On by default:** roll back if the new containers restart twice, or the health check fails for 30 seconds. These mean the deploy failed; nobody disputes them.
- **Error-rate trigger, shadow mode by default:** if 5xx responses exceed 5% **and** three times the previous release's rate, over a two-minute window with at least 50 requests, Moonglass notifies you with the numbers ("this would have rolled back"), and you mark the call right or wrong. One switch per app turns it into a real rollback. This gives us the false-positive rate before anyone's healthy deploy is reverted (M§6).
- Low-traffic apps use only the first two triggers.
- Never auto-roll-back twice in a row; stop and notify.
- **Deploys that ran an irreversible pre-deploy step do not auto-roll-back** unless that step is marked reversible (§3.13).
- All thresholds are per-app settings.

**5. Cost of a free organisation.**
My estimate, at ~30,000 active free orgs (the upper scenario in V§5.6), ~1.5 servers each:

| Load | Size |
|---|---|
| Agent connections held open | ~45,000, across two or three stateless nodes |
| Memory for those, at 50–200 KB each | 2.2–9 GB in total |
| Heartbeats, every 30 seconds | ~1,500 per second, not written to the database |
| Probes at five-minute intervals | ~300 per second |
| Audit data kept for 14 days | ~12 GB in Postgres |

That is two or three mid-sized VMs plus managed Postgres with a standby and object storage: roughly $400–900 a month, or **about one to three cents per active org**. Compute will not sink the free tier; abuse handling, email deliverability and your support time are the real costs.

---

## 6. Memory budget: agent and proxy

Targets, validated by the 48-hour spike in week 0 and measured in CI from week 1:

| Process | Component | Budget |
|---|---|---|
| `moonglass agent` | Go runtime and control-plane connection | 12 MB |
| | Docker client, reconciler and sanitizer state | 6 MB |
| | SQLite cache | 6 MB |
| | Metrics sampler and buffers | 4 MB |
| | Slack | 4 MB |
| | **Agent total** | **~32 MB** |
| `moonglass proxy` | Go runtime | 8 MB |
| | Caddy, idle, ten routes, plus our counter module | 32 MB |
| | Slack | 5 MB |
| | **Proxy total** | **~45 MB** |
| **Both** | | **~77 MB target, 100 MB hard ceiling** |

For scale: community reports put Coolify's idle footprint at 500 MB–1.2 GB and Dokploy's at 300–400 MB, and Temps needs 4 GB (R§3). We publish our number with a script that reproduces it, in the benchmark repository (M§5).

---

## 7. Guardrails for agent-written code

You will not read every line, and this product holds deploy rights to production. The stack therefore includes its own checks:

- **Tests are the specification.** Each week's acceptance tests are written before the code and run in CI against real Docker on real Linux.
- **Five permanent suites,** never allowed to go red:
  - *Deploy correctness:* a load generator runs through deploys, rollbacks, agent upgrades and proxy upgrades, and fails on a single dropped request for the shapes promised in §5.3.
  - *Authorisation matrix:* every operation × every token scope × every role, asserting allow or deny.
  - *Container escape:* every denied field in §3.2, every alias of the Docker socket, every Compose trick (interpolation, `extends`, anchors) that could smuggle one in. Each must be rejected.
  - *Protocol abuse:* malformed, oversized and out-of-order agent messages, plus Go's built-in fuzzing on every parser.
  - *Storage conformance:* the same fixtures and queries against Postgres (with RLS on, and with a deliberately missing `org_id` filter that must return nothing) and against SQLite.
- **Restore in CI** on every change from week 6, including a restore onto a fresh server with only the recovery key.
- **A reviewer agent with a different brief.** Every change is reviewed by a second agent whose only instructions are the threat model in §4 and "find the way this breaks it".
- **Static checks in CI:** strict linting, the race detector, vulnerability scanning of dependencies.
- **Small trusted core.** Protocol handling, sanitization, policy enforcement, token checks and key handling live in a few packages with the strictest review and the fewest dependencies.
- **A paid external review of the protocol, sanitizer, key design and auth before v1.** Until then, the docs describe the design and its limits; they never claim "audited".

---

## 8. What gets written, and what does not

**Build (this is the product):**
- The agent protocol, local policy enforcement and container-spec sanitization
- The reconciler: desired state → containers, including the Compose classifier
- Release switching, draining and auto-rollback on top of Caddy, including shadow mode
- Pre-deploy steps and disk-full handling
- Environment keys, wrapping, granting, rotation and the recovery key
- The operation registry that generates REST, CLI and MCP
- Approvals with step-up, scoped tokens, audit trail
- Backup orchestration and restore
- Release channels, staged rollout and agent self-update with revert
- `moonglass import` for Compose and Coolify
- The UI

**Reuse (do not be tempted):**
- Caddy and CertMagic for everything HTTP and TLS
- Docker and BuildKit for running and building; Railpack's CLI in v1
- Compose's official parser
- age, OAuth libraries and (v1) WebAuthn for all cryptography and sign-in
- restic for volume snapshots (v1)
- Postgres through `pgx` on hosted, SQLite through the pure-Go driver on self-hosted

---

## 9. Decisions recorded

| Decision | Outcome |
|---|---|
| Core language | Go |
| Proxy | Caddy 2.11.9, embedded in `moonglass proxy`, a **separate process from day one** (2026-11-16) |
| Control-plane database | **Hosted: one managed Postgres with RLS on `org_id`. Self-hosted: SQLite.** One interface, one conformance suite (2026-11-16) |
| Secrets | Envelope keys per environment, owner recovery key required, write-only in the web UI, reveal through the CLI (2026-11-16) |
| Container specs | Deny-by-default sanitization in the agent; exceptions only in `agent.toml` (2026-11-16) |
| Auto-rollback | Crash-loop and health triggers on; error-rate trigger in shadow mode until enabled (2026-11-16) |
| MVP auth | GitHub, Google, self-hosted login links, step-up; passkeys and TOTP in v1 (2026-11-16; Google replaced email links 2026-11-18) |
| Builds | Dockerfile and image at launch; Railpack via CLI in v1 (2026-11-16) |
| UI | Svelte 5 + SvelteKit (static) |
| Who writes the code | Agents, with you orchestrating; guardrails in §7 |

**Open (stack):**
1. **Managed Postgres provider and region** for hosted. It needs PITR, a standby, and `LISTEN/NOTIFY` support (rules out some poolers in transaction mode). Decide by week 4.
2. **Proxy upgrade mechanism:** systemd socket activation or `SO_REUSEPORT` handover, decided by the week 0 spike.

---

## Sources

Re-verified live 2026-11-15:
- Caddy 2.11.9: [Go package docs](https://pkg.go.dev/github.com/caddyserver/caddy/v2) · [feature list](https://caddyserver.com/features) · [network address conventions](https://caddyserver.com/docs/conventions#network-addresses)
- MCP: [2026-07-28 specification release](https://blog.modelcontextprotocol.io/posts/2026-07-28/) · official Go SDK v1.7+ (stateless Streamable HTTP)
- Railpack v0.43.0: [releases](https://github.com/railwayapp/railpack/releases) (used via CLI and BuildKit frontend; no stable Go API)
- Public Suffix List: [guidelines](https://github.com/publicsuffix/list/wiki/Guidelines) · Let's Encrypt: [rate limits](https://letsencrypt.org/docs/rate-limits/)
- Coolify [#12442](https://github.com/coollabsio/coolify/issues/12442) (MCP deploy bypassed the queue) · [discussion #3767](https://github.com/coollabsio/coolify/discussions/3767) (Compose deploys stop all containers)

Checked 2026-11-15 in revision 2:
- SQLite in Go: [pure-Go driver](https://pkg.go.dev/modernc.org/sqlite@v1.60.1) · [driver benchmarks](https://github.com/cvilsmeier/go-sqlite-bench)
- Railpack and Nixpacks: [comparison with release history](https://www.bitdoze.com/nixpacks-vs-railpack) · [Coolify's Railpack docs](https://next.coolify.io/docs/applications/builds/railpack) · [Coolify cache-growth issue](https://github.com/coollabsio/coolify/issues/12542)
- Let's Encrypt: [45-day announcement](https://letsencrypt.org/2025/12/02/from-90-to-45) · [timeline and profiles](https://www.youngju.dev/blog/2026-07-17-certificate-lifetime-47-days-sc081v3-acme-ari.en)
- Docker Engine: [v29 release notes](https://docs.docker.com/engine/release-notes/29/) · [v29 announcement](https://www.docker.com/blog/docker-engine-version-29/)
- Pingora (rejected): [repository](https://github.com/cloudflare/pingora)
- Dokploy network issue: [#6028](https://github.com/Dokploy/dokploy/issues/6028)
- Repository health (stars, last push, archived flag), GitHub search API: Caddy, CertMagic, compose-go, Docker/Moby, the MCP Go SDK, age and its TypeScript implementation, go-webauthn, Huma, coder/websocket, protobuf-go, pgx, SvelteKit, Svelte, Bits UI, the node-graph library, restic, Railpack, kamal-proxy, Turso. All active. Two small dependencies have been quiet for a year or more (the TOTP library and the WireGuard control library); both are small and stable and not needed until v1.

Not verified:
- Caddy's idle memory in its own process (my 32 MB is an estimate for the week 0 spike).
- Whether Caddy accepts systemd-activated listeners cleanly (week 0 spike).
- Exact current versions of Svelte, SvelteKit and Tailwind.
- How much of Coolify's API the importer can rely on (week 0 interviews).
