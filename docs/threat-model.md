# Moonglass threat model

_W0-2 · 2026-11-16 · Sources: `docs/plan/stack.md` (S§), `vision.md` (V§), `milestones.md` (M§), `build-plan.md` (B§) and `AGENTS.md`. Anything those do not state is marked _Proposed_._

This file is the reviewer's whole brief. Read it, then the diff, and look for the way the change breaks something below (S§7).

## 1. The system in brief

- One static binary, four roles: `moonglass server` (the control plane), `moonglass agent`, `moonglass proxy` and the CLI (S rule 1).
- The control plane coordinates and is never in the request path. Routing, certificates, health checks, auto-rollback and scheduled backups run on the user's server and keep working without it (S rule 2).
- The agent dials out. The control plane can ask only for the typed messages in the public `.proto` file, and the agent checks each one against local policy before acting (S§3.1, S§3.2).
- The control plane stores secrets as ciphertext and keys only as wrapped copies (S§3.11).
- Agents write the code and one person operates the product, so the checks in §9 are part of the design (S§7).

## 2. Assets

| Asset | Where it lives | What protects it |
|---|---|---|
| **Host root** on the user's server | Behind the Docker socket, which only the agent holds (S§3.3) | No protocol message carries a shell string (S§3.1). Every container spec passes the sanitizer (S§3.2). `agent.toml` is local policy the control plane cannot change. Agent and proxy are systemd services, not containers |
| **User secrets** | Ciphertext in the control-plane database. Plaintext only on the user's servers, at run time | Encrypted to the environment public key; the private half is stored only wrapped (S§3.11). Write-only in the web UI. One reveal path, through the CLI |
| **Database backups** | The user's S3-compatible bucket | Dump → zstd → age to the environment key, with no temp files on disk (S§3.9). Readable only with the environment key or the owner recovery key |
| **Tokens**: API and coding-agent tokens, enrolment tokens, sessions, the bootstrap token, login links | Control-plane database | API and agent tokens are opaque random values stored hashed, each with scope rows, an expiry and an owner (S§2, S§3.10). One-time tokens fail on replay (B W1-3). Sessions are server-side, listable, revocable and bound to `__Host-` cookies |
| **Release signing key** | Offline, on a hardware token (S§3.14) | Agents verify every release against a pinned public key before installing. Canary, staged rollout and automatic revert limit a bad release |
| **Audit trail** | Control-plane database | Hash-chained events written by the registry, never by handlers (B W2-1). `moonglass audit verify` checks the chain (B W5-5). Free export to the user's bucket on every plan (V§5.1) |

Also sensitive, and never held by the control plane: agent keys, the owner recovery key, certificate private keys (S§3.4) and the git deploy key (S§3.11). All are generated where they are used.

## 3. Trust boundaries

```
      people                                   coding agents
 +-----------+   +-----------+               +-----------------+
 |  Browser  |   |    CLI    |               | API / MCP token |
 +-----+-----+   +-----+-----+               +--------+--------+
       |               |                              |
       +---------------+---------- B1 ----------------+
                              |
                              v
 +-------------------------------------------+        +------------------+
 | CONTROL PLANE   moonglass server          |        | DATABASE         |
 | operation registry: authz, audit          +-- B2 ->| Postgres + RLS,  |
 | holds ciphertext, wrapped keys, hashes    |        | or SQLite        |
 +-------------------------------------------+        +------------------+
                              ^
                              | B3  agent dials out: WSS :443, typed protobuf
 = = = = = = = = = = = = = = =|= = = = = = = the user's server = = = = = = = =
                              |
 +----------------------------+----+           +--------------------------+
 | AGENT   moonglass agent         |           | PROXY   moonglass proxy  |
 | agent.toml policy, sanitizer,   +--- B4 --->| Caddy, certificate keys  |<-- B8
 | agent keys, SQLite              |   unix    | counters per release     |  :80/:443
 +-------+-----------------+-------+   socket  +------------+-------------+  public
         |                 |                                |
         | B5 docker.sock  | B7  S3 API                     | bridge IP
         v                 |                                v
 +----------------+        |                   +--------------------------+
 | DOCKER ENGINE  +--------|------ B6 -------->| CONTAINERS  user code,   |
 | root-equivalent|        |     sandbox       | plaintext secrets        |
 +----------------+        v                   +--------------------------+
                  +------------------+
                  | USER'S BUCKET    |
                  | age ciphertext   |
                  +------------------+
```

| Boundary | Rule when crossing it |
|---|---|
| **B1** client → control plane | Every call is a registry operation with a declared scope and `destructive` flag; authorisation and audit happen in the registry (S§3.12). A destructive call by an agent identity returns "pending approval". Approval needs a sign-in fresher than ten minutes (S§3.10) |
| **B2** control plane → database | Only the storage package writes SQL. Every row carries `org_id`; on Postgres each transaction sets `app.org_id` and RLS refuses other orgs' rows (S§3.7). The database never holds a readable secret, an unwrapped key or a server credential (S§4) |
| **B3** control plane ↔ agent | Outbound only, TLS 1.3, the control plane's public key pinned at enrolment, one Ed25519 identity per agent (S§2, S§3.1). The agent treats the control plane as able to be hostile: it accepts typed messages only and applies local policy and the sanitizer to each one. Malformed, oversized and out-of-order messages are rejected (S§7) |
| **B4** agent → proxy | Caddy's admin API on a Unix socket readable only by the Moonglass service user. A complete JSON config; no TCP admin port (S§3.3). The proxy has no Docker access |
| **B5** agent → Docker Engine | The socket is root-equivalent. Nothing reaches it without passing the sanitizer (S§3.2) |
| **B6** Docker Engine → containers | Containers run untrusted user code inside the default Docker sandbox plus the deny list in §6. They hold their environment's plaintext secrets |
| **B7** agent → bucket | Only age ciphertext leaves the server (S§3.9) |
| **B8** internet → proxy | Caddy parses all hostile HTTP and TLS input; we never fork or patch it (S rule 4, S§3.3) |

Release artifacts cross a ninth boundary: the agent installs nothing whose signature does not verify against its pinned key (S§3.14).

## 4. Attackers and what they get

From S§4. "Protected" means a protected environment.

| If an attacker gets… | Moonglass MVP | v1, protected environments |
|---|---|---|
| A copy of the control-plane database or its backups | Ciphertext secrets, wrapped environment keys they cannot unwrap, no server credentials, hashed tokens | Same |
| Full control of the running control plane | Can send any *typed* command: deploy an image or pre-deploy step to an unprotected environment (it runs in a sanitized container), stop services, soft-delete, enrol a rogue server into an unprotected environment. **Cannot** get a host shell, start a privileged or host-namespace container, mount the Docker socket or host paths, read host files, override `agent.toml`, or obtain a protected environment's key | Also cannot deploy, change sources, change secret recipients or delete in a protected environment without a signature from an approver's key, verified by the agent |
| A leaked coding-agent or API token | Only that token's project, environment and operations; destructive calls wait for a human with step-up | Same |
| A malicious image | Confined to its container by the default Docker sandbox plus sanitization; it can still use whatever that environment's secrets reach | Same |
| A container or kernel escape | Host compromise. True of every product in the category | Same |

## 5. Honest limits

Docs never claim more than this section allows (`AGENTS.md`), and never say "audited" before the external review in v1 (S§7).

1. **A takeover of the running control plane can deploy its own image into an unprotected environment, and that image receives that environment's secrets.** The MVP defends against a stolen database plus a decryption credential, removes host root and keeps protected environments' keys out of reach. v1 closes the deploy gap for protected environments with agent-verified approvals (S§4).
2. **Browser-side encryption trusts the JavaScript we serve.** The CLI path does not: it is a signed binary that pins each environment's public key on first use and warns if it changes (S§3.11).
3. **Lose every server and the recovery key, and the secrets and backups are gone.** Nobody can decrypt them, including us (S§3.11).

What browser-side encryption protects, and what it does not (S§3.11):

| Protects against | Does not protect against |
|---|---|
| A stolen copy of our database or its backups | A takeover of our **running** service, which can serve JavaScript that captures values as you type, or hand your browser a substitute environment key |
| Our database provider, our logs, anyone reading rows | The same takeover deploying its own image to an **unprotected** environment and receiving that environment's secrets |
| Secrets appearing in our error reports or support tooling | A compromised user server, which must hold plaintext to run your app |
| | An app that logs its own environment |

Sanitization does not cover a kernel bug or container-runtime escape, a malicious image doing what images can do inside their sandbox, or an app leaking its own secrets (S§3.2). Rotation does not un-leak: a removed server saw plaintext (S§3.11).

## 6. Container-spec deny list

The agent checks **every** spec it is about to create: apps, Compose services, one-click databases, pre-deploy steps, previews, backup jobs and builds. It rejects the spec unless `agent.toml` allows the exception (S§3.2, ADR-0004).

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

How exceptions are scoped:

- Only in `/etc/moonglass/agent.toml`, on the server. No protocol message, API operation or UI setting can add one.
- Exceptions are scoped, never global. In the plan's example a `[[containers.allow]]` block names a project and an image (digest pinning supported) and lists the fields it allows; the exact shape is a _Proposed_ draft in ADR-0004 until W1-1.
- A rejected spec fails the deploy with an error naming the field and showing the `agent.toml` lines that would allow it. Nothing is silently stripped.
- Compose files are checked after `compose-go` has resolved interpolation, `extends` and profiles.
- The escape suite covers every denied field, every alias of the Docker socket and every Compose trick that could smuggle one in. It is never red (S§7).

## 7. Trusted core

From `AGENTS.md`. Changes here get line-by-line review by the owner, and only one such ticket is in review at a time (B§3).

- `internal/protocol`
- `internal/policy`
- `internal/keys`
- `internal/auth`
- `internal/store`
- `scripts/install`
- `deploy/systemd`
- release signing and self-update

## 8. Rules that hold everywhere

- No host shell, ever. Container exec is not in the MVP (S§5.2).
- Local policy beats remote command (S§3.1).
- Enforcement is on the server, never a flag the caller sets (S§3.12).
- The recovery key is for granting, rotating and recovering, never for everyday reads (S§3.11).
- No plan or licence check in the agent or in the open-source build (V§6).

## 9. Reviewer checklist

Ask every question of every diff. Report a finding with file:line, what breaks and a concrete failing scenario (B§7, T4).

**1. New protocol fields or messages**
- Does the diff add or change anything in a `.proto` file? Was the contract change approved first (B§8)?
- Can any field carry a shell string, a host command line, a host path, or text the agent hands to a host program?
- Does the agent check size, order and type before acting? Is there a protocol-abuse test, and a fuzz target for each new parser?
- Could the message return plaintext or an unwrapped key to the control plane? Do agents still refuse `RewrapEnvKey` for protected environments?

**2. Policy or sanitizer bypass**
- Is there any path to the Docker Engine that creates a container, build or exec without passing `internal/policy`?
- Is the spec that was checked the exact spec that is sent, after Compose resolution, with nothing changed in between?
- Is a denied field rejected with an error, rather than stripped or defaulted?
- Can anything except a human editing the file change `agent.toml`, or widen what it allows?
- Does a new deny rule or allow key come with escape-suite fixtures?

**3. Secret plaintext**
- Can a secret value, token, key or recovery key reach a log line, an error string, an audit event, a live stream, an API response, the database or a temp file?
- Does the control plane ever receive a plaintext value or an unwrapped environment key?
- Is `secrets reveal` still the only read path, re-encrypting to a one-time CLI key?

**4. Token scope holes**
- Is every new token stored hashed, with scope rows, an expiry and an owner?
- Does a one-time token fail on replay?
- Can a token act outside its project, environment or operations? Can an agent identity approve its own request?
- Is step-up freshness checked on the server at the moment of approval?

**5. Authz-matrix rows**
- Is every new operation declared in the registry with a scope and a `destructive` flag?
- Does the authz matrix have a row for it against every token scope and every role, asserting allow or deny?
- Is there any HTTP route, CLI command or MCP tool that reaches behaviour outside the registry? Any authorisation in middleware?
- Does every store call run inside the caller's org? Does the conformance suite cover the new query on both backends?

**6. Root steps in install scripts**
- Is each step that runs as root listed, necessary and as narrow as it can be?
- Is everything downloaded verified against the release signature before it runs? (_Proposed_ for the install script; S§3.14 states it for the agent.)
- Is Docker group access given to the agent only (B W1-6)?

**7. systemd unit privileges**
- Does each unit run as the service user with its own memory limit?
- Does the proxy unit have any access to the Docker socket? Who can read the admin socket?
- Does the diff relax hardening, add a capability or widen a writable path? Why is that needed?

**8. Fallback or dead code**
- Is there a second way to do something, a compatibility path, an unused branch, or code for a later ticket?
- If a design changed, was the old path deleted in the same change?

**9. Tests weakened or removed**
- Does `testdata/tests.txt` lose a line? Is each loss explained?
- Is any test skipped, loosened, hard-coded to pass, or switched from real Docker to a mock?
- Does every acceptance test named in the ticket exist and assert what the ticket says?

**Also:** a dependency added without an ADR; a doc or UI string that claims more than §5 allows.

## Open questions

1. **Which environments are protected by default?** Limit 1 applies to every unprotected environment, and the plan does not say what a new environment starts as. Options: (a) unprotected until the owner switches it on, which is simplest but leaves production exposed to limit 1 until someone acts; (b) the first-deploy wizard asks, which costs one question. The Free plan includes one protected environment (V§5.3), so "all protected" is not available on hosted.
2. **Where do bucket credentials live?** The plan says backups go to the user's bucket but not where its access key is stored. Options: (a) as an environment secret, encrypted to the environment key like any other; (b) only in a file on the server. Option (a) lets a replacement server restore without re-entering it; option (b) keeps it out of the database entirely.
3. **Registry authorisation and the trusted core.** B§3 reviews "registry authorisation" at Core level, but the `AGENTS.md` list in §7 has no path for it. Adding `internal/registry` to `AGENTS.md` is a one-line change for the owner to make or decline.
