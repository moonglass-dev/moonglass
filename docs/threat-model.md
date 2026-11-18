# Moonglass threat model

_W0-2 · 2026-11-16 · Sources: `docs/plan/stack.md` (S§), `vision.md` (V§), `milestones.md` (M§), `build-plan.md` (B§) and `AGENTS.md`. Owner decisions of 2026-11-16 are recorded in the ADRs and folded in here._

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
| **User secrets** | Ciphertext in the control-plane database. Plaintext only on the user's servers, at run time | Encrypted to the environment public key. The key pair is generated only in the owner's CLI; the private half is stored only wrapped (S§3.11, ADR-0003). Write-only in the web UI. One reveal path, through the CLI: owner-only, step-up, refused to agent identities, audited |
| **Database backups** | The user's S3-compatible bucket | Dump → zstd → age to the environment key, with no temp files on disk (S§3.9). Readable only with the environment key or the owner recovery key. Bucket credentials are themselves an environment secret, so a restore needs only the recovery key |
| **Tokens**: API and coding-agent tokens, enrolment tokens, sessions, the bootstrap token, login links, identity links and step-up confirmations | Control-plane database | Every one is 256 bits from `crypto/rand`, written `mg_…`, stored only as SHA-256 in a lookup table and shown once (ADR-0005). API and agent tokens carry scope rows, an expiry and an owner (S§3.10). One-time tokens are marked used in the accepting transaction and fail on replay (B W1-3). Sessions are server-side, listable, revocable, bound to `__Host-` cookies and last 14 days. Links and tokens expire: login link, identity link and step-up 10 min, enrolment and bootstrap tokens 1 h. A step-up approves one request and is single use (`docs/specs/auth.md`) |
| **Release signing keys** | Two `ed25519-sk` keys, each on its own FIDO2 token, offline; PIN and touch per signature (S§3.14, ADR-0007) | Every artifact carries an OpenSSH `SSHSIG` signature, namespace `moonglass-release`. The install script (from W1-6) and the agent (from W7-6) verify against both pinned public keys before installing. Canary, staged rollout and automatic revert limit a bad release |
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
| **B1** client → control plane | Every call is a registry operation, or a route on the fixed non-operation list, with its authorisation declared beside it; authorisation and audit happen in the registry (S§3.12, ADR-0006). An operation declares its lowest role and `destructive`, with no default. **Three separate things each require a sign-in fresher than ten minutes**, checked on the server at approval: approving a protected deploy, approving a destructive operation, and changing secret recipients. A `destructive` flag is not a substitute for either of the other two. `secrets reveal` also requires it. On self-hosted the fresh sign-in is a new `server login-link`. A destructive call by an agent identity returns "pending approval" |
| **B2** control plane → database | Only the storage package writes SQL. Every row outside the lookup tables carries `org_id`; on Postgres each transaction sets `app.org_id` and RLS refuses other orgs' rows (S§3.7). The application connects as a role that does not own the tables and cannot bypass RLS; migrations run as a separate role, from numbered SQL files applied by `internal/store`. The only tables without `org_id` and RLS are the listed lookup tables, which hold a lookup key, IDs, an expiry and a revoked marker, nothing else (ADR-0002). The database never holds a readable secret, an unwrapped key or a server credential (S§4) |
| **B3** control plane ↔ agent | Outbound only, TLS 1.3, the control plane's public key pinned at enrolment, one Ed25519 identity per agent (S§2, S§3.1). The agent treats the control plane as able to be hostile: it accepts typed messages only and applies local policy and the sanitizer to each one. Malformed, oversized and out-of-order messages are rejected (S§7) |
| **B4** agent → proxy | Caddy's admin API on a Unix socket reachable only by the agent and the proxy, through the group `moonglass-proxy-admin` that only their units hold (ADR-0001 decision 3). A complete JSON config; no TCP admin port (S§3.3). The proxy has no Docker access |
| **B5** agent → Docker Engine | The socket is root-equivalent. Nothing reaches it without passing the sanitizer (S§3.2) |
| **B6** Docker Engine → containers | Containers run untrusted user code inside the default Docker sandbox plus the deny list in §6. They hold their environment's plaintext secrets |
| **B7** agent → bucket | Only age ciphertext leaves the server (S§3.9) |
| **B8** internet → proxy | Caddy parses all hostile HTTP and TLS input; we never fork or patch it (S rule 4, S§3.3) |

Release artifacts cross a ninth boundary: neither the install script nor the agent installs or runs anything whose own `.sig` does not verify against a pinned release key, with the user-presence and user-verification flags set (S§3.14, ADR-0007).

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

1. **A takeover of the running control plane can deploy its own image into an unprotected environment, and that image receives that environment's secrets.** The MVP defends against a stolen database plus a decryption credential, removes host root and keeps protected environments' keys out of reach. v1 closes the deploy gap for protected environments with agent-verified approvals (S§4). An org's first environment starts protected; later ones start unprotected until switched on (ADR-0006).
2. **Browser-side encryption trusts the JavaScript we serve.** The CLI path does not: it is a signed binary that pins each environment's public key and the org's recovery recipients on first use and stops if they change (S§3.11, ADR-0003 decision 9). The first use on a new machine trusts what the control plane returns, and the CLI prints it for the owner to check.
3. **Lose every server and the recovery key, and the secrets and backups are gone.** Nobody can decrypt them, including us (S§3.11).
4. **The install script is trusted through HTTPS from moonglass.dev.** Whoever controls that domain can serve a different script. Everything the script downloads is signature-checked; the script itself is only if the user verifies its `.sig` first (ADR-0007).

What browser-side encryption protects, and what it does not (S§3.11):

| Protects against | Does not protect against |
|---|---|
| A stolen copy of our database or its backups | A takeover of our **running** service, which can serve JavaScript that captures values as you type, or hand your browser a substitute environment key |
| Our database provider, our logs, anyone reading rows | The same takeover deploying its own image to an **unprotected** environment and receiving that environment's secrets |
| Secrets appearing in our error reports or support tooling | A compromised user server, which must hold plaintext to run your app |
| | An app that logs its own environment |

Sanitization does not cover a kernel bug or container-runtime escape, a malicious image doing what images can do inside their sandbox, or an app leaking its own secrets (S§3.2). Rotation does not un-leak: a removed server saw plaintext (S§3.11).

A log chunk is relayed and never stored; whoever can read that service's logs can see what the app printed.

## 6. Container-spec deny list

The agent checks **every** spec it is about to create: apps, Compose services, one-click databases, pre-deploy steps, previews, backup jobs and builds. It rejects the spec unless `agent.toml` allows the exception (S§3.2, ADR-0004).

| Denied by default | Why |
|---|---|
| `privileged: true` | Full host access |
| Bind mounts from host paths (anything other than named volumes and an allowed `data_dirs` path) | Host filesystem access |
| Any mount of `docker.sock`, `containerd.sock` or the Docker data root, under any path or alias | Root-equivalent control of the host |
| `network_mode: host`, `pid: host`, `ipc: host`, `uts: host`, `userns_mode: host`, `network_mode: container:<id>` across projects | Host or cross-project namespaces |
| `cap_add` (any), `devices`, `device_cgroup_rules` | Kernel attack surface and device access |
| `security_opt` that disables seccomp, AppArmor or SELinux | Removes the default confinement |
| `sysctls` other than `net.core.somaxconn` and `net.ipv4.ip_local_port_range`; a custom `cgroup_parent`; a non-default `runtime` | Escape routes and resource evasion. A sysctl that is not namespaced is never allowable |
| BuildKit entitlements `security.insecure` and `network.host` | The same escapes, at build time |

How exceptions are scoped:

- Only in `/etc/moonglass/agent.toml`, on the server. No protocol message, API operation or UI setting can add one.
- Exceptions are scoped, never global. A `[[containers.allow]]` block must name `project` and an `image` **digest**; a tag stops the agent. Optional `environment` and `service` only narrow it. Each allow key is named after the denied field, and a block allows only the fields it lists (ADR-0004).
- The agent-managed data directory is `/var/lib/moonglass/mounts/<project>/<name>`, created by the agent, holding no agent state, and mountable only through a block's `data_dirs` by that block's image. The path is resolved first; a symlink, `..` or a path outside `mounts/<project>/` is rejected.
- An unknown key or invalid block stops the agent. The file is owned by root and is not writable by the service user. It is read once, at start-up; a change takes effect on restart only. Enrolment state lives under `/var/lib/moonglass`, never in this file.
- A rejected spec fails the deploy with an error naming the field and showing the `agent.toml` lines that would allow it. Nothing is silently stripped.
- Labels in the `dev.moonglass.` namespace are set only by the agent; a user-supplied one is rejected and can never be allowed. Environment values never appear in a log, an error, a result or an audit record (ADR-0004 decisions 11 and 12).
- Compose files are checked after `compose-go` has resolved interpolation, `extends` and profiles.
- The escape suite covers every denied field, every alias of the Docker socket and every Compose trick that could smuggle one in. It is never red (S§7).

## 7. Trusted core

From `AGENTS.md`. Changes here get line-by-line review by the owner, and only one such ticket is in review at a time (B§3).

- `internal/protocol`
- `internal/policy`
- `internal/keys`
- `internal/auth`
- `internal/store`
- `internal/registry`
- `scripts/install`
- `deploy/systemd`
- release signing and self-update

## 8. Rules that hold everywhere

- No host shell, ever. Container exec is not in the MVP (S§5.2).
- Local policy beats remote command (S§3.1).
- Enforcement is on the server, never a flag the caller sets (S§3.12).
- The recovery key is for granting, rotating and recovering, never for everyday reads (S§3.11).
- No plan or licence check in the agent or in the open-source build (V§6).
- A sign-in never attaches itself to an existing user by email address; linking two sign-in methods is explicit, with step-up, except the first owner's first identity linked on the box (ADR-0005).
- The control plane never replaces itself. Self-hosted operators update `moonglass server` and the CLI by running the signed install (ADR-0007).

## 9. Reviewer checklist

Ask every question of every diff. Report a finding with file:line, what breaks and a concrete failing scenario (B§7, T4).

**1. New protocol fields or messages**
- Does the diff add or change anything in a `.proto` file? Was the contract change approved first (B§8)?
- Can any field carry a shell string, a host command line, a host path, or text the agent hands to a host program?
- Does the agent check size, order and type before acting? Is there a protocol-abuse test, and a fuzz target for each new parser?
- Could the message return plaintext or an unwrapped key to the control plane? Do agents still refuse `RewrapEnvKey` for protected environments, deciding from the key record they unwrapped themselves (`docs/specs/secrets.md`)?
- Can a re-sent command run twice? Each request id runs once (`docs/specs/connection.md`).

**2. Policy or sanitizer bypass**
- Is there any path to the Docker Engine that creates a container, build or exec without passing `internal/policy`?
- Is the spec that was checked the exact spec that is sent, after Compose resolution, with nothing changed in between, including its command, environment, labels and networks?
- Is a denied field rejected with an error, rather than stripped or defaulted?
- Can anything except a human editing the file change `agent.toml`, or widen what it allows? Does an unknown key still stop the agent? Does any block allow a field it does not list, match by tag, or reach a path outside its project's `mounts/` subtree? Is the file read anywhere but at start-up?
- Does a new deny rule or allow key come with escape-suite fixtures?

**3. Secret plaintext**
- Can a secret value, token, key or recovery key reach a log line, an error string, an audit event, a live stream, an API response, the database or a temp file?
- Does the control plane ever receive a plaintext value or an unwrapped environment key?
- Does any grant, rotation, reveal or recipient change skip step-up because the caller holds the recovery key (ADR-0003 decision 11)? Does anything put key material in the agent database (ADR-0010)?
- Is `secrets reveal` still the only read path, re-encrypting to a one-time CLI key? Is it still owner-only, behind step-up, and refused to agent identities?
- Is an environment key pair ever generated or unwrapped outside the owner's CLI or an agent? Can an unprotected-environment rotation wrap to a recipient named in the request rather than one already stored? Does rotation keep the old key's recovery-wrapped copy and delete its agent-wrapped copies?
- Are bucket credentials stored anywhere but as an environment secret?

**4. Token scope holes**
- Is every new token stored hashed, with scope rows, an expiry and an owner?
- Does a one-time token fail on replay?
- Is every new bearer secret 256 bits, `mg_`-prefixed, stored only as SHA-256 and shown once?
- Can a token act outside its scope rows, or above its owning human's role? Can an agent identity approve its own request?
- Is step-up freshness checked on the server at the moment of approval? Do lifetimes match ADR-0005? Is each step-up bound to one user, credential, operation, target and input digest, and used once? Can a sign-in attach to an existing user by email address?

**5. Authz-matrix rows**
- Is every new operation declared in the registry with a scope, a lowest role that follows the role rule, and an explicit `destructive` flag? Is anything that deletes, irreversibly replaces or stops a service, release, volume or stored data marked destructive?
- Does a protected deploy or a recipient change still pass its own gate, rather than relying on `destructive`?
- Does the authz matrix have a row for it against every token scope and every role, asserting allow or deny?
- Does a new HTTP route or CLI command appear on neither the registry nor its fixed list beside it? Any authorisation in middleware? Were the generated files regenerated, not edited?
- Does every store call run inside the caller's org? Does the conformance suite cover the new query on both backends?
- Does the application's database role still lack table ownership and `BYPASSRLS`? Is any migration run by the application role?
- Is a new table without `org_id` on the lookup-table list? Does a lookup table gain any other column? Is a lookup row ever returned to a caller instead of only setting the org?

**6. Root steps in install scripts, and release verification**
- Is each step that runs as root listed, necessary and as narrow as it can be?
- Does the script verify every downloaded artifact's own `.sig` with `ssh-keygen -Y verify` against the pinned keys, namespace `moonglass-release`, before it runs or installs it? Does a failure stop with nothing installed?
- Is Docker group access given to the agent only (B W1-6)?
- Does the agent's verifier use only the standard library, require a pinned key, the namespace, `sha512` and the 0x01 and 0x04 flags, and reject anything else? Does the self-check compare the running executable's SHA-256 with the verified file?

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
