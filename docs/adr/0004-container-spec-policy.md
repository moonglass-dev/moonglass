# ADR-0004: Container-spec policy

- **Status:** Accepted
- **Date:** 2026-11-16
- **Amended:** 2026-11-17, new decisions 10 to 12 (owner decision D8, weeks 2–3 preflight)
- **Amended:** 2026-11-18, new decisions 13 and 14 (week 5 preflight, owner decisions D43 to D52, `docs/specs/local-policy.md`)
- **Amended:** 2026-11-19, new decisions 15 and 16 (week 6 owner decisions, `docs/specs/compose.md`, `docs/specs/container-policy.md`)

## Context

Docker socket access is root-equivalent (S§2). A typed protocol is not enough on its own: a control plane that can say "deploy this container" with `privileged: true` and a bind mount of `/` already has a root shell (S§3.2). The claim "a short, typed list of things" is true only if the agent refuses such specs (V§1).

## Decision

1. **Deny by default.** The agent checks every container spec it is about to create: apps, Compose services, one-click databases, pre-deploy steps, previews and backup jobs. The denied fields are the table in S§3.2, repeated in `docs/threat-model.md` §6:
   - `privileged: true`
   - bind mounts from host paths, other than named volumes and the agent-managed data directory (decision 4)
   - any mount of `docker.sock`, `containerd.sock` or the Docker data root, under any path or alias
   - `network_mode: host`, `pid: host`, `ipc: host`, `uts: host`, `userns_mode: host`, and `network_mode: container:<id>` across projects
   - `cap_add` (any), `devices`, `device_cgroup_rules`
   - `security_opt` that disables seccomp, AppArmor or SELinux
   - `sysctls` outside the allow-list (decision 3), a custom `cgroup_parent`, a non-default `runtime`
2. **BuildKit entitlements** `security.insecure` and `network.host` are denied the same way, at build time.
3. **The sysctl allow-list** passes without a block: `net.core.somaxconn` (listen backlog; Redis and busy servers) and `net.ipv4.ip_local_port_range` (outbound ports for connection-heavy apps). Both are scoped to the container's network namespace. Any other namespaced sysctl needs a block. A sysctl that is not namespaced can never be allowed.
4. **The agent-managed data directory** is `/var/lib/moonglass/mounts/<project>/<name>`. The agent creates and owns these paths; agent state never lives under `mounts/`. A container mounts one only when a block names it under `data_dirs` and the container's image matches that block. The path is resolved first: a symlink, a `..` or a path outside `mounts/<project>/` is rejected.
5. **Exceptions live only in `/etc/moonglass/agent.toml`** and are scoped, never global. The control plane cannot change that file.
   - A `[[containers.allow]]` block must name `project` and `image`. It may also name `environment` and `service`, which only narrow it. It applies only when every key it names matches.
   - `image` is a digest (`name@sha256:…`). A tag stops the agent, with an error showing the digest form.
   - Each allow key is named after the denied field it allows, and a block allows only the fields it lists.
   - An unknown key or an invalid block is an error and the agent does not start.
   - The file is owned by root and is not writable by the service user. Enrolment state lives under `/var/lib/moonglass`, not in this file.
   - The agent reads the file once, at start-up. A change takes effect on agent restart only; there is no reload.
6. **Reject, never strip.** A rejected spec fails the deploy with an error that names the field, shows the `agent.toml` lines that would allow it, and says to restart the agent after editing.
7. **Compose is checked after resolution:** after `compose-go` has resolved interpolation, `extends` and profiles.
8. **The same file holds command policy:** it can forbid classes of command outright, such as container exec and deletes, and restrict deploys to listed repositories or registries (S§3.1).
9. **The check and its escape suite are written in week 1, before any `Deploy` exists** (S§3.2, B W1-1). The suite has a fixture for every denied field, each sysctl entry, a host sysctl, a tag in place of a digest, the agent's own state, a symlink out of `mounts/` and another project's subtree.
10. **The checked spec is the whole request** (2026-11-17). The fields the deny list does not judge (command, environment, labels, networks, restart policy, log configuration) are carried in the spec too and pass through unchanged, so the Engine request is built from the checked value and nothing else (`docs/specs/container-policy.md`).
11. **Only the agent sets Moonglass labels** (2026-11-17). Labels in the `dev.moonglass.` namespace mark what the agent manages, so the agent and its clean-up trust them. A user-supplied label in that namespace is rejected, and no block can allow it. The agent sets its own labels through a separate field that no protocol message or Compose file reaches.
12. **Environment values stay out of every record** (2026-11-17). Environment variable values are never logged and never written into an error, a rejection, a command result or an audit record. Names may be.
13. **Command classes are fixed per command type** (2026-11-18). Every command type the agent accepts has one class. A type sent by a destructive registry operation is `delete`; one that runs a process in an existing container is `exec`; the rest have none. A forbidden class is answered `REJECTED` before anything else is checked. Forbidding a class that has no command type yet is valid and does nothing. A test fails when a command type has no class (`docs/specs/local-policy.md`).
14. **Sources are matched in one normal form** (2026-11-18). `registries` matches the image's repository without tag or digest, with the default registry `docker.io` and `library/` filled in; `repositories` matches `github.com/<owner>/<repo>`, lower-cased. Patterns must be written in that form, with a literal host, or the agent does not start. A present, empty list allows nothing of its kind. The check runs on `DEPLOY` and `BUILD` before any pull or clone. Images built on the server are never pulled, and are checked through the repository label the agent puts on each build. A build's base images are not checked (`docs/specs/local-policy.md`).

15. **A container joins only its own project's network** (2026-11-19). No protocol message names a network: the agent attaches every container to `mg-<project>` and nothing else. Compose import refuses `networks`, so a Compose service cannot name another project's network, and no allow key exists for one. This closes open question 8 of `docs/specs/container-policy.md`. Services reach each other by the alias the agent sets from its binding (`docs/specs/release.md`, "Service names").
16. **Compose is resolved in the CLI, and each service is checked on the agent** (2026-11-19). A Compose file's environment values are secrets, which the control plane may never hold in plaintext, so the file cannot travel to the agent. The CLI resolves it with `compose-go` through `internal/policy` (`policy.ResolveCompose`: interpolation, `extends`, `include` and profiles, as decision 7 requires), refuses everything outside the supported subset, and runs `CheckCompose` with the empty policy before anything is applied. Each service then reaches the agent as an ordinary `Deploy`, and the agent checks the spec it builds from that `Deploy` with `Check` against its own `agent.toml`. The agent's check is the one that counts; the CLI's only stops a file early.

**`agent.toml` schema,** implemented by W1-1. `[update] channel` is `stable` or `canary`. `[commands] forbid` takes the classes `delete` (any command that carries out a destructive operation) and `exec` (container exec, v1). `[sources]` lists glob patterns; when present, deploys come only from them. Any other key or value stops the agent.

```toml
# /etc/moonglass/agent.toml
[update]
channel = "stable"                      # "canary" opts this server in (S§3.14)
[commands]
forbid = ["delete"]                     # classes the control plane may not ask for (S§3.1)
[sources]                               # when present, deploys come only from these (S§3.1)
repositories = ["github.com/acme/*"]
registries   = ["ghcr.io/acme/*"]

[[containers.allow]]                    # one scoped exception per block (S§3.2)
project     = "monitoring"
image       = "prom/node-exporter@sha256:<digest>"
pid_host    = true
bind_mounts = ["/proc:/host/proc:ro", "/sys:/host/sys:ro"]
```

## Consequences

- A control plane under someone else's control cannot start a privileged or host-namespace container, mount the Docker socket or host paths, or read host files (S§4).
- Some legitimate tools, such as node exporters and backup agents, need an explicit allow entry written on the server (S§2).
- Every rejection must explain itself; sanitizer rejections are measured by field from launch (M§6).
- `internal/policy` is trusted core, and the escape suite is one of the five permanent suites (S§7).
- Not covered: a kernel or container-runtime escape, a malicious image acting inside its sandbox, an app leaking its own secrets (S§3.2).

## Alternatives rejected

- **Adding agent-owned fields after the check:** the request sent would no longer be the spec that was checked (threat-model checklist item 2).

- **Relying on the typed protocol alone:** the root shell above (S§3.2).
- **Silently stripping denied fields:** nothing is silently stripped; the deploy fails and says why (S§3.2).
- **Exceptions set from the control plane or UI:** local policy beats remote command (S§3.1).
- **Tags in allow blocks:** a repointed tag would inherit the exception.
- **A data directory any container in the project may mount:** a compromised control plane could have any image read or write host files.
- **Reloading the file while running:** a second load path, and a rule for deploys in flight.

## Links

- S§2, S§3.1, S§3.2, S§3.5, S§3.13, S§4, S§7, S§9; V§1; M§6
- B§6: W1-1, W2-3, W2-6, W3-4, W5-3a, W5-3, W6-1b, W6-4a, W6-4
- `docs/threat-model.md` §6 and checklist item 2
