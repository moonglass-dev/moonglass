# Moonglass — Stack

_Builds on `research.md` (R§n) and `vision.md` (V§n). Versions verified 2026-11-15; sources at the end._

Priorities, in order: security, performance, footprint, UX. One constraint shapes almost every choice: **agents write this, and a small team operates it.** The Go core, Caddy, the Svelte dashboard and code written by agents with a human orchestrating are fixed.

## Five rules the stack follows

1. **One binary, four roles.** `moonglass server`, `moonglass agent`, `moonglass proxy` and the CLI (including `moonglass import`) are the same static executable. A server runs the agent and the proxy as two systemd units. Self-hosting on one box adds the server role.
2. **The server side is authoritative for runtime.** Routing, certificates, health checks, auto-rollback and scheduled backups run on the user's server and keep working when the control plane is unreachable. The proxy keeps serving when the agent is restarting or upgrading. The control plane coordinates; it is never in the request path.
3. **One codebase for every control plane.** Every feature is in the open core. The Business module (SAML, SCIM, SSO enforcement, audit export) lives in its own repository and is compiled into the licensed build through extension points (V1-1); Cloud runs the same binary in managed instance mode (V1-2). The open-source build contains no plan or licence checks (V§5.1, V§6).
4. **Reuse anything that parses hostile input or does cryptography. Build only what is the product.**
5. **Nothing that needs its own on-call.** No Redis, no message broker, no Kubernetes, no database server at all: the control plane is SQLite.

---

## 1. Why Go, stated once

- **The proxy is Go either way.** With Caddy serving every request, a Rust core buys no performance on the hot path.
- **Go keeps a single executable.** Caddy is designed to be embedded in Go programs. The proxy role embeds it, and the agent role does not load it.
- **Most of the domain is Go libraries:** Docker's client, Compose's parser, Caddy and its certificate engine, the reference implementation of age, the official MCP SDK. Rule 4 is usually "import it". The exception is Railpack (LN-12), which has no stable public Go API. We drive it through its CLI and BuildKit frontend.
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
| Builds | Dockerfile or a prebuilt image through BuildKit; a build-in-CI template; **Railpack** for zero-config builds, via its pinned CLI and BuildKit frontend (LN-12) | Reuse | Railpack has no stable Go API, so we drive its CLI |
| Compose | `compose-go`, the official parser, then our own reconciler through the Engine API | Reuse the parser; **build** the reconciler | We own Compose runtime semantics for the subset we support |
| Networking | One Docker bridge network per project; a cross-server WireGuard mesh (V1-15) | Reuse kernel WireGuard | The mesh is the last networking piece to land |
| Control-plane storage | **SQLite** (pure-Go driver), with `org_id` on every row. A Cloud control plane is one instance per account, not a shared database (§3.7) | Reuse (`modernc.org/sqlite`) | Cloud is many small instances, each upgraded and backed up on its own |
| Agent storage | SQLite on the server | Reuse | None worth noting |
| Logs and metrics | Docker's `local` log driver, streamed and searched by time range on demand; cgroup v2 samples rolled up into SQLite for charts | **Build** the sampler | No log index: a text filter scans a time range on the server |
| Backups | Native dump tools → zstd → age, encrypted to the **environment key** → any S3-compatible bucket; volumes through restic (V1-13) | Reuse | restic is a second executable, run on demand |
| Secrets | age (X25519): envelope keys per environment, an owner recovery key, wrapping and rotation (§3.11). The reference Go library on servers and CLI; the same author's TypeScript implementation in the browser | Reuse the crypto; **build** key distribution | Losing every server *and* the recovery key loses the secrets, by design |
| API | Go's standard HTTP server with Huma generating OpenAPI from Go types; one operation registry feeds REST, CLI and MCP | Reuse | Discipline: no operation exists outside the registry |
| MCP | Official Go SDK v1.7+, spec 2026-07-28, stateless Streamable HTTP, OAuth 2.1 | Reuse | None; Go is a Tier 1 SDK |
| Auth | GitHub and Google sign-in through the operator's OAuth apps, OpenID Connect, passkeys (`go-webauthn`) and TOTP; one-time login links printed on the box for the first sign-in and emergencies; invitations; server-side sessions; step-up for everything that matters (ADR-0005) | Reuse libraries; **build** the flows | Until passkeys land, a second factor depends on the provider |
| API and agent tokens | Opaque random tokens, stored hashed, with scope rows | **Build** (small) | No offline verification or delegation yet |
| Web UI | **Svelte 5 with SvelteKit in static mode**, Tailwind, Bits UI primitives; shipped inside the binary | Reuse | Smaller component ecosystem than React |
| Billing | Stripe Checkout, the customer portal and Stripe Tax, in the Cloud service's repository (`docs/specs/billing.md`) | Reuse | None in the open core |
| Releases | Signed artifacts, a **canary** and a **stable** channel, agent self-update with automatic revert, Update from the dashboard, and staged rollout on Cloud (§3.14) | Reuse signing tools; **build** the channel logic | The signing key is the most sensitive thing the project owns |

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
- The check has its own permanent test suite (§7), written before any `Deploy` existed (W1-1).
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
- The agent never hands sockets to the proxy, so an agent upgrade never touches traffic.

**Proxy upgrades without dropped requests:** the preferred path is systemd socket activation, so the listening sockets outlive the process. Whether Caddy 2.11.9 accepts inherited listeners cleanly is **unverified** (Caddy's network-address docs list TCP, UDP and Unix sockets). The week 0 spike tests it against the one alternative, where the new proxy process binds alongside the old one with `SO_REUSEPORT` and the old one drains and exits. **The spike picks exactly one; the other is never built.** Long-lived connections (WebSockets) get a drain window, then close.

**Rejected:** Traefik (CPU complaints in R§3, and label-driven config is the "magic" users dislike); Pingora (best footprint, but a framework to assemble and own); kamal-proxy (purpose-built for switching, without Caddy's certificate engine).

### 3.4 Certificates and managed subdomains

- CertMagic renews on ARI rather than a timer. That matters because Let's Encrypt's default lifetime drops to 64 days in February 2027 and 45 days in February 2028.
- Keys are generated and kept on the server. The control plane never holds a certificate private key.
- Preview domains (LN-16) use a wildcard DNS record the user points at the server, with an HTTP-01 certificate issued on demand for each preview host. Wildcard certificates use DNS-01; Cloudflare first, more providers through `libdns` (V1-22).
- **Managed subdomains** are for Cloud accounts (V1-4). Getting them right needs time we cannot shorten:
  - A **separate registrable user-content domain**, not a subdomain of moonglass.dev, for cookie and phishing isolation (V§8).
  - A **Public Suffix List** entry. The PSL has no SLA. Merged requests often land in one to two weeks, but some sit for months. It explicitly rejects entries whose purpose is to get around Let's Encrypt limits, so we apply on the legitimate ground: browser cookie and security isolation between customers.
  - **Let's Encrypt's limit is 50 certificates per registered domain per week.** Without a PSL entry, every customer subdomain shares that limit. We separately request a rate-limit adjustment from Let's Encrypt as a hosting provider.
  - **A shared wildcard certificate is not an option**: its private key would have to sit on every customer's server.
- Custom domains always work and are unlimited.

### 3.5 Builds

- **Dockerfile and prebuilt image,** both through BuildKit driven by `docker buildx`, from the first git build (W3-4).
- **First-class "build in CI" path:** a GitHub Actions template that builds, pushes to GHCR and calls `moonglass deploy --image <digest>`. Zero RAM on the server, which matters on a 2 GB VPS.
- **Build on the target server by default, one build at a time**, with the sanitization rules applied to BuildKit entitlements.
- **Rollback without a registry.** Keep the last five release images in the server's local store. This avoids Dokploy's requirement for an external registry (R§3).
- **Cap the build cache** (Coolify has an open issue for Railpack's BuildKit cache growing by 20 GB between cleanups), and stop builds before the disk fills (§3.13).
- **Never auto-build pull requests from forks.**
- **Railpack** (v0.43.0 at the last check, pre-1.0, frequent releases) for zero-config builds (LN-12). We use `railpack prepare` plus its BuildKit frontend from a pinned, checksum-verified CLI version, not an imported Go library, because it has no stable public API. We upgrade it deliberately, behind the canary channel. `build = "auto"` uses the repository's Dockerfile when there is one and Railpack otherwise.
- **Compose builds** a service's `build` from the connected repository on the server, through the same path (LN-13).

### 3.6 Networking

- One bridge network per project; nothing crosses projects unless declared. Dokploy has open issues about a shared network causing cross-project DNS collisions.
- The proxy reaches containers by their bridge IP; apps publish no host ports unless the project asks.
- **Private networking between servers** (V1-15): a WireGuard mesh using the kernel module, keys generated on each server, the control plane distributing only public keys and endpoints.

### 3.7 Storage

**SQLite only** (ADR-0002).

- It is one binary with no database server to run, upgrade or secure. The pure-Go driver keeps the binary static.
- **One storage interface** (`internal/store`, with `store/sqlite`). Nothing outside it writes SQL.
- **`org_id` on every row** outside the listed lookup tables, and every store method filters on it. A control plane may hold several orgs, but they all belong to one customer: the operator of a self-hosted box, or one Cloud account. So a missing org filter can show one of a customer's orgs to that same customer, never another customer's data. The conformance suite's second-org checks stay.
- **Control-plane backup:** a scheduled consistent snapshot (`VACUUM INTO`), age-encrypted to the owner's recovery key, uploaded to the user's bucket. A control plane moves with `moonglass export` and re-enrolment (W7-2, V§6), or by restoring its backup on another box.
- **A Cloud control plane is one instance per account** on the same binary and SQLite, run by us (V1-2). There is no shared multi-tenant database, so there is no row-level security to keep in step and no second backend.

**Rejected:** a shared Postgres with row-level security for many accounts (a second backend for every store ticket, and one policy bug away from every account); per-org SQLite files with Litestream (too much custom operations work); a distributed SQLite with consensus (a cluster to operate); Turso (too young to hold a control plane).

### 3.8 Logs and metrics

- Containers log through Docker's `local` driver with rotation and a size cap. The agent streams and greps on request. Nothing is shipped to us.
- The agent samples CPU, memory, network and disk from cgroup v2 files. The runtime panel shows **current** usage from the first dashboard (W4-4); 10-second points rolled up to one minute and one hour in agent state feed the **charts** (LN-19). The collection exists from the start because disk-full handling and auto-rollback need it.
- Proxy counters per release feed auto-rollback and the request charts.
- Later: optional OpenTelemetry export.
- **Rejected:** bundling a log or metrics database. Even the light ones cost more idle memory than the whole agent.

### 3.9 Backups and restore

- **Databases:** run the engine's own dump tool inside the container (a sanitized one-off), stream through zstd and age, upload to the user's bucket. No temp files on disk.
- **Encrypted to the environment key, not to the server's key** (§3.11). A dead server must not take its backups with it. Restoring onto a new server needs that server granted the environment key, or the owner's recovery key.
- **Restore is a first-class button.** The G6 demo restores onto a *different* server using only the recovery key.
- **Volumes** (V1-13): restic, bundled and run on demand, so it costs disk, not idle memory, with a scheduled **restore drill**: restore into a scratch container, run a check query, report. This answers the top day-2 complaint about Coolify (R§3, R§10).
- **Later:** point-in-time recovery for Postgres.

### 3.10 Auth and sessions

- **Sign-in with GitHub and Google** through OAuth apps the operator configures, **OpenID Connect** for any provider (V1-7), and **passkeys and TOTP** for everyone, free (V1-6). No passwords, and email is never a sign-in method (ADR-0005, `docs/specs/auth.md`). GitHub requires 2FA for most active developer accounts, so a GitHub sign-in is usually two-factor; passkeys make a second factor available to everyone.
- **The first owner** is created on the box with a one-time bootstrap token or owner link printed in the terminal; there is never an open registration window. Dokploy had a pre-authentication admin takeover, and Coolify has since restricted its first-admin registration to a short window after start (R§3). On Cloud, sign-up creates the first owner. `moonglass server login-link` prints a one-time link on the box for emergencies, such as a provider being down or no OAuth app set up yet.
- **Members join by invitation** (LN-3): a one-time link for a role, emailed through the operator's SMTP when it is set.
- **Step-up for anything that matters.** A change to secret recipients, a reveal, minting a token, a purge, membership and protection changes, and an approval each require a sign-in fresher than ten minutes, then an Approve on a page that shows the exact request (`docs/specs/auth.md`). That page is out of band from the coding agent, which is the point.
- Server-side sessions, listable and revocable, bound to `__Host-` cookies.
- **Business module:** SAML, SSO enforcement and SCIM (V§5.1).

**Tokens:** random, stored hashed, each with rows naming project, environment and allowed operations, an expiry and an owner. Every token belongs to a human or a named agent identity, and the audit trail records which. **Rejected for now:** capability tokens with offline verification. They are elegant, but they add a policy language users must learn.

### 3.11 Secrets and keys

The question the reviewer asked: if the control plane cannot decrypt secrets, how does a new or replacement server get them? The answer is envelope encryption with an owner recovery key.

**Keys**

| Key | Where it lives | Purpose |
|---|---|---|
| **Agent key** (X25519 + Ed25519) | Generated on each server at enrolment; never leaves it | Identity, and the recipient for wrapped environment keys |
| **Environment key** (an age X25519 key pair per environment) | Public half: control plane, browser, CLI. Private half: **only ever stored wrapped** | Secret values and database backups are encrypted to it |
| **Owner recovery key** (age identity) | Generated in the browser at first run or by `moonglass keys init`, and saved by the owner as a file or printed code. Up to two owners can hold one | Recovering, granting and rotating without a surviving server |

The recovery key exists before the first secret is saved. The dashboard asks once a quarter for a recovery-key check: the browser or the CLI proves it can unwrap a test value, without revealing anything. Every key operation below has a dashboard flow and a CLI command (ADR-0003).

**Wrapping**
- Each secret value is encrypted to the environment **public** key. The browser and CLI need nothing else to set a value, which is why values are write-only.
- The environment private key is stored as wrapped copies, one per authorised agent and one per recovery key. The control plane holds only those wrapped copies.
- Database backups are encrypted to the environment public key. Exports carry ciphertext plus wrapped keys, and `age` can read them once the owner unwraps the environment key with the recovery key.

**Adding a server to an environment**
- **With the recovery key** (W4-8, AL-3): in the dashboard, with the key file read into the browser, or with `moonglass secrets grant --env prod --server <name>`, with step-up. The browser or CLI unwraps the environment key and shows the new server's key fingerprint. The owner checks it against the one the server's enrolment printed, and only then is the key wrapped to it.
- **Without the recovery key** (W3-3): in an unprotected environment, an online agent re-wraps the key to the new agent (`RewrapEnvKey`) after a person confirms the fingerprint with step-up. Agents refuse that for a protected environment.

**Replacing a server when every server in the environment is gone:** the owner grants it with the recovery key. **If every server is gone and no recovery key exists, the secrets and the database backups cannot be decrypted by anyone, including us.** The dashboard says this at first run, on the pricing page and in the docs, in plain words.

**Rotation** (W3-3, LN-11)
- **Removing a server, or suspecting one:** rotate from Settings → Keys or with `moonglass secrets rotate --env <name>`. It creates a new environment key, re-encrypts every value (done by an online agent in the environment, or by the owner's browser or CLI with the recovery key), re-wraps to the remaining recipients and deletes the old wrapped copies. Future backups use the new key, and old backups stay readable through the recovery key.
- **Rotation does not un-leak.** A removed server saw plaintext, so the dashboard lists which values to rotate at their source. Moonglass rotates the values it generated itself (one-click database passwords) on a schedule (V1-11).
- **Agent key rotation:** the agent generates a new key and re-wraps the environment keys it holds to itself. On demand, and every 90 days (V1-11).
- **Recovery key rotation:** in the browser or the CLI, with the old recovery key.

**Reveal** (W4-9, LN-11): one path, in the dashboard or with `moonglass secrets reveal`: the agent re-encrypts the value to a one-time key made by the browser or the CLI. The recovery key is only for granting, rotating and recovering, never for everyday reads.

**Git access** uses a deploy key generated on the server. The GitHub App's private key (W4-10) is the only repository credential the control plane holds, with the fewest permissions push-to-deploy, deploy keys, statuses and preview comments need (ADR-0003 decision 10).

**What browser-side encryption protects, and what it does not.** The dashboard sets, reveals and rotates values and handles keys in the browser (W4-5, AL-2, LN-11, ADR-0018).

| Protects against | Does not protect against |
|---|---|
| A stolen copy of our database or its backups: the Railway-style breach (R§3) | A takeover of our **running** service, which can serve JavaScript that captures values as you type, or hand your browser a substitute environment key |
| Our storage on Cloud, our logs, anyone reading rows | The same takeover deploying its own image to an **unprotected** environment and receiving that environment's secrets |
| Secrets appearing in our error reports or support tooling | A compromised user server, which must hold plaintext to run your app |
| | An app that logs its own environment |

The CLI is the stronger path. It is a signed binary, it pins each environment's public key on first use and warns if it changes, and it can do everything the dashboard can. **In one sentence for the docs:** our database never holds a readable secret; if the running control plane were taken over, it could deploy its own image to an unprotected environment and receive that environment's secrets, which protected environments prevent.

### 3.12 API, CLI and MCP from one source

- Every operation is declared once with its inputs, outputs, required scope and a `destructive` flag. REST routes, CLI subcommands and MCP tools are generated from that list. "MCP for everything" then costs nothing extra, and nothing is reachable by a coding agent without also being scoped and audited.
- MCP spec 2026-07-28 is stateless over plain HTTP, and the official Go SDK supports it from v1.7 (W5-4).
- A destructive operation called by an agent identity, and any deploy to a protected environment, returns "pending approval" with a link. A person approves it from the inline diff in the dashboard with step-up, or with `moonglass approve` (W5-2, LN-7). Enforcement is on the server, not a flag the caller sets (R§5).
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
- **Two channels.** `canary` gets every release first: the test fleet plus people who opt in through `agent.toml`. `stable` gets it after at least 72 hours on canary with no regressions in the permanent suites or the fleet.
- **Self-hosted**, a control plane offers each channel's release to every agent on that channel at once (`docs/specs/update.md` §1), and the operator upgrades the control plane itself with Update in the dashboard or `moonglass update` (AL-5).
- **Staged rollout on Cloud** (V1-3): stable agent updates go to 10%, then 50%, then 100% of connected servers over about 48 hours. The rollout halts automatically if updated agents fail to reconnect or their health reports degrade.
- **Automatic revert:** the agent keeps the previous binary. If the new one has not reconnected and passed its self-check within five minutes, systemd starts the old one again.
- **The proxy updates separately**, only when Caddy or our module changes (§3.3).

### 3.15 Import and export

Import is the first hour for people moving from somewhere else (V§3):
- **Compose:** a `compose.yaml` becomes a project's services (LN-13, LN-14, W6-4). Services are classified and checked against the sanitization rules, and anything unsupported is reported before anything is applied, not after.
- **Coolify** (W7-3): reads projects, applications, environment variables, domains and Compose stacks through Coolify's API with a token the user provides. Values are encrypted to the new environment key on the user's side before upload, so no plaintext reaches the control plane. Database contents move through a backup and restore, not live. Coverage of Coolify's API is checked against real instances from the alpha.
- **Dokploy** (V1-21), the same way.
- **Export** (W7-2): `moonglass export` and the Export button write a control plane's state with ciphertext and wrapped keys only, for an import on Cloud or another box.

### 3.16 Web UI

**Choice: Svelte 5, SvelteKit in static mode, Tailwind, Bits UI.** Compiled to static files and embedded in the binary.

- Compiled output, small downloads, fast interaction.
- The UI costs the server nothing either way; it ships as static files.
- A typed API client is generated from the OpenAPI description, so the dashboard cannot drift from the API, and every operation the CLI has, the dashboard has.
- Live updates over server-sent events.
- The layout, flows and tokens are `docs/specs/ui.md`.

| Alternative | Verdict |
|---|---|
| Solid | Slightly smaller and faster; much smaller component ecosystem |
| React | Largest ecosystem and strongest agent familiarity; heavier |
| Server-rendered pages with light scripting | Lightest, but browser-side encryption and live deploy views fit it poorly |
| Compile-to-WASM UI frameworks | Large downloads, immature for an app this size |

---

## 4. Threat model in one table

| If an attacker gets… | SSH-from-panel design (incumbents) | Moonglass | Moonglass, protected environment |
|---|---|---|---|
| A copy of the control-plane database or its backups | Whatever the panel stores to reach servers and run apps | Ciphertext secrets, wrapped environment keys they cannot unwrap, no server credentials, hashed tokens | Same |
| Full control of the running control plane | A root shell on every connected server | Can send any *typed* command: deploy an image or pre-deploy step to any environment (it runs inside a sanitized container), stop services, soft-delete, enrol a server into an unprotected environment through `RewrapEnvKey`, and serve altered JavaScript to the dashboard. **Cannot** get a host shell, start a privileged or host-namespace container, mount the Docker socket or host paths, read host files, or override `agent.toml` | Also cannot get the environment key to a new server, because agents refuse `RewrapEnvKey` there. With agent-verified approvals (V1-9), cannot deploy there without an approver's passkey signature, verified by the agent |
| A leaked coding-agent or API token | Often everything (R§3, Railway) | Only that token's project, environment and operations; its destructive calls wait for a person with step-up | Its deploys wait for a person too |
| A malicious image | Host compromise | Confined to its container by the default Docker sandbox plus sanitization; it can still use whatever that environment's secrets reach | Same |
| A container or kernel escape | Host compromise | Host compromise. True of every product in the category | Same |

Three honest limits:

- **A takeover of the running control plane can deploy its own image into an unprotected environment, and that image receives that environment's secrets.** Moonglass defends against the breach that hit Railway (stolen records plus a decryption credential, R§3) and removes host root. Protected environments keep their keys from new servers, and agent-verified approvals (V1-9) close their deploy gap.
- **Browser-side encryption trusts the JavaScript we serve** (§3.11). The CLI path does not.
- **Lose every server and the recovery key, and the secrets and backups are gone.** That is the price of a control plane that cannot read them.

---

## 5. The tensions from `vision.md`, resolved

**1. Ciphertext-only control plane vs a complete dashboard.** Envelope keys and a recovery key (§3.11), with every key operation in the browser and in the CLI. Values are write-only, like GitHub Actions secrets; reveal re-encrypts one value to a one-time key in the browser or the CLI. Builds, previews and pre-deploy steps decrypt on the user's server. Secrets export as standard age files.

**2. Web terminal vs "a short, typed list".**
- No host shell, ever. It is not in the protocol.
- The container terminal (V1-10) is in the dashboard and the CLI: human-only, with step-up, audited, unavailable to agent identities, and switched off by `forbid = ["exec"]` in `agent.toml`.

**3. Zero-downtime: exactly which shapes get it.** This list goes in the docs word for word.

| Shape | Behaviour | Built by |
|---|---|---|
| Single-service app (image, Dockerfile or git) behind the proxy, with a health check, no fixed `container_name`, no published host port, no single-writer volume | **Zero failed HTTP requests** across a deploy. In-flight requests finish; WebSockets get a drain window (default 30 s), then close and reconnect | The alpha |
| Same, but with a single-writer volume (an app writing its own SQLite file, say) | Stop-then-start, with a brief gap, which is inherent. The dashboard says so before you confirm | The alpha |
| App with a fixed `container_name` or a published host port | Stop-then-start, flagged in the dashboard | The alpha |
| Compose: services you did not change | **Never touched.** Coolify stops every container on every Compose deploy (R§3, #3767) | The launch |
| Compose: services you changed | **Through the zero-downtime path** for services with no named volume, no published host port and no fixed name. A service with a named volume (typically a database) stops and then starts, with a brief gap, and the dashboard lists it before you confirm. It is never refused for having a volume (`docs/specs/compose.md`, LN-13) | The launch |
| Non-HTTP services (TCP, UDP) | Stop-then-start | The alpha |

Compose parsing uses the official library, so interpolation, profiles and `extends` behave as users expect. Unsupported fields produce an explicit error; nothing is silently dropped. The classifier runs over 30 popular Compose files (W6-5) to check how many real files the supported subset accepts.

**4. Auto-rollback defaults: shadow mode first.**
- Watch for ten minutes after the switch. The previous release stays available.
- **On by default:** roll back if the new containers restart twice, or the health check fails for 30 seconds. These mean the deploy failed; nobody disputes them.
- **Error-rate trigger, shadow mode by default:** if 5xx responses exceed 5% **and** three times the previous release's rate, over a two-minute window with at least 50 requests, Moonglass notifies you with the numbers ("this would have rolled back"), and you mark the call right or wrong. One switch per app turns it into a real rollback. This gives us the false-positive rate before anyone's healthy deploy is reverted (M§6).
- Low-traffic apps use only the first two triggers.
- Never auto-roll-back twice in a row; stop and notify.
- **Deploys that ran an irreversible pre-deploy step do not auto-roll-back** unless that step is marked reversible (§3.13).
- All thresholds are per-app settings.

**5. Cost of a Cloud control plane.**
A Cloud control plane is one `moonglass server` process with its SQLite file, per account, including trials. Estimate: tens of megabytes of memory each, so hundreds of accounts fit on one small machine, plus object storage for their encrypted backups. Payment fees and support time, not compute, are the real costs, which is why yearly billing costs ten months (V§5.3).

---

## 6. Memory budget: agent and proxy

Targets, validated by the 48-hour spike (SPIKE-1) and measured in CI on every change:

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

No person reads every line, and this product holds deploy rights to production. The stack therefore includes its own checks:

- **Tests are the specification.** Each ticket's acceptance tests are written before the code and run in CI against real Docker on real Linux.
- **Five permanent suites,** never allowed to go red:
  - *Deploy correctness:* a load generator runs through deploys, rollbacks, agent upgrades and proxy upgrades, and fails on a single dropped request for the shapes promised in §5.3.
  - *Authorisation matrix:* every operation × every token scope × every role, asserting allow or deny.
  - *Container escape:* every denied field in §3.2, every alias of the Docker socket, every Compose trick (interpolation, `extends`, anchors) that could smuggle one in. Each must be rejected.
  - *Protocol abuse:* malformed, oversized and out-of-order agent messages, plus Go's built-in fuzzing on every parser.
  - *Storage conformance:* the same fixtures and queries against SQLite, including a second org that must see none of the first's rows through any store method.
- **Restore in CI** on every change from W6-3, including a restore onto a fresh server with only the recovery key.
- **A reviewer agent with a different brief.** Every change is reviewed by a second agent whose only instructions are the threat model in §4 and "find the way this breaks it".
- **Static checks in CI:** strict linting, the race detector, vulnerability scanning of dependencies.
- **Small trusted core.** Protocol handling, sanitization, policy enforcement, token checks and key handling live in a few packages with the strictest review and the fewest dependencies.
- **A paid external review of the protocol, sanitizer, key design and auth** (V1-12). Until then, the docs describe the design and its limits; they never claim "audited".

---

## 8. What gets written, and what does not

**Build (this is the product):**
- The agent protocol, local policy enforcement and container-spec sanitization
- The reconciler: desired state → containers, including the Compose classifier
- Release switching, draining and auto-rollback on top of Caddy, including shadow mode
- Pre-deploy steps and disk-full handling
- Environment keys, wrapping, granting, rotation and the recovery key
- The operation registry that generates REST, the CLI, the dashboard's client and MCP
- Scoped tokens, step-up, approvals and the audit trail
- Backup orchestration and restore
- Release channels, agent self-update with revert, Update from the dashboard and staged rollout on Cloud
- The dashboard
- Compose, previews, templates and `moonglass import` for Coolify and Dokploy

**Reuse (do not be tempted):**
- Caddy and CertMagic for everything HTTP and TLS
- Docker and BuildKit for running and building; Railpack's CLI for zero-config builds
- Compose's official parser
- age (Go and TypeScript), OAuth libraries and WebAuthn for all cryptography and sign-in
- restic for volume snapshots
- Stripe for billing, in the Cloud service only
- SQLite through the pure-Go driver

---

## 9. Decisions recorded

| Decision | Outcome |
|---|---|
| Core language | Go |
| Proxy | Caddy 2.11.9, embedded in `moonglass proxy`, a separate process (ADR-0001) |
| Control-plane database | SQLite only; a Cloud control plane is one instance per account (ADR-0002) |
| Secrets | Envelope keys per environment, an owner recovery key, every key operation in the browser and the CLI (ADR-0003, ADR-0018) |
| Container specs | Deny-by-default sanitization in the agent; exceptions only in `agent.toml` (ADR-0004) |
| Auto-rollback | Crash-loop and health triggers on; error-rate trigger in shadow mode until enabled |
| Auth | GitHub, Google, OpenID Connect, passkeys and TOTP; login links on the box for the first sign-in and emergencies; invitations; step-up (ADR-0005) |
| Builds | Dockerfile, image, Railpack and Compose |
| UI | Svelte 5 + SvelteKit (static), dashboard first (ADR-0016, `docs/specs/ui.md`) |
| Who writes the code | Agents, with a human orchestrating; guardrails in §7 |

**Open (stack):**
1. **Proxy upgrade mechanism:** systemd socket activation or `SO_REUSEPORT` handover, decided by SPIKE-1 (ADR-0001).

---

## Sources

Re-verified live 2026-11-15:
- Caddy 2.11.9: [Go package docs](https://pkg.go.dev/github.com/caddyserver/caddy/v2) · [feature list](https://caddyserver.com/features) · [network address conventions](https://caddyserver.com/docs/conventions#network-addresses)
- MCP: [2026-07-28 specification release](https://blog.modelcontextprotocol.io/posts/2026-07-28/) · official Go SDK v1.7+ (stateless Streamable HTTP)
- Railpack v0.43.0: [releases](https://github.com/railwayapp/railpack/releases) (used via CLI and BuildKit frontend; no stable Go API)
- Public Suffix List: [guidelines](https://github.com/publicsuffix/list/wiki/Guidelines) · Let's Encrypt: [rate limits](https://letsencrypt.org/docs/rate-limits/)
- Coolify [#12442](https://github.com/coollabsio/coolify/issues/12442) (MCP deploy bypassed the queue) · [discussion #3767](https://github.com/coollabsio/coolify/discussions/3767) (Compose deploys stop all containers)

Checked 2026-11-15:
- SQLite in Go: [pure-Go driver](https://pkg.go.dev/modernc.org/sqlite@v1.60.1) · [driver benchmarks](https://github.com/cvilsmeier/go-sqlite-bench)
- Railpack and Nixpacks: [comparison with release history](https://www.bitdoze.com/nixpacks-vs-railpack) · [Coolify's Railpack docs](https://next.coolify.io/docs/applications/builds/railpack) · [Coolify cache-growth issue](https://github.com/coollabsio/coolify/issues/12542)
- Let's Encrypt: [45-day announcement](https://letsencrypt.org/2025/12/02/from-90-to-45) · [timeline and profiles](https://www.youngju.dev/blog/2026-07-17-certificate-lifetime-47-days-sc081v3-acme-ari.en)
- Docker Engine: [v29 release notes](https://docs.docker.com/engine/release-notes/29/) · [v29 announcement](https://www.docker.com/blog/docker-engine-version-29/)
- Pingora (rejected): [repository](https://github.com/cloudflare/pingora)
- Dokploy network issue: [#6028](https://github.com/Dokploy/dokploy/issues/6028)
- Repository health (stars, last push, archived flag), GitHub search API: Caddy, CertMagic, compose-go, Docker/Moby, the MCP Go SDK, age and its TypeScript implementation, go-webauthn, Huma, coder/websocket, protobuf-go, SvelteKit, Svelte, Bits UI, the node-graph library, restic, Railpack, kamal-proxy, Turso. All active. Two small dependencies have been quiet for a year or more (the TOTP library and the WireGuard control library); both are small and stable and not needed until v1.

Not verified:
- Caddy's idle memory in its own process (the 32 MB estimate is measured by SPIKE-1).
- Whether Caddy accepts systemd-activated listeners cleanly (SPIKE-1).
- Exact current versions of Svelte, SvelteKit and Tailwind.
- How much of Coolify's API the importer can rely on (checked against real instances from the alpha, W7-3).
