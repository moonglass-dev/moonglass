# ADR-0004: Container-spec policy

- **Status:** Accepted
- **Date:** 2026-11-16

## Context

Docker socket access is root-equivalent (S§2). A typed protocol is not enough on its own: a control plane that can say "deploy this container" with `privileged: true` and a bind mount of `/` already has a root shell (S§3.2). The claim "a short, typed list of things" is true only if the agent refuses such specs (V§1).

## Decision

1. **Deny by default.** The agent checks every container spec it is about to create: apps, Compose services, one-click databases, pre-deploy steps, previews and backup jobs. The denied fields are the table in S§3.2, repeated in `docs/threat-model.md` §6:
   - `privileged: true`
   - bind mounts from host paths, other than named volumes and the agent-managed data directory
   - any mount of `docker.sock`, `containerd.sock` or the Docker data root, under any path or alias
   - `network_mode: host`, `pid: host`, `ipc: host`, `uts: host`, `userns_mode: host`, and `network_mode: container:<id>` across projects
   - `cap_add` (any), `devices`, `device_cgroup_rules`
   - `security_opt` that disables seccomp, AppArmor or SELinux
   - `sysctls` outside a short namespaced allow-list, a custom `cgroup_parent`, a non-default `runtime`
2. **BuildKit entitlements** `security.insecure` and `network.host` are denied the same way, at build time.
3. **Exceptions live only in `/etc/moonglass/agent.toml`** and are scoped, never global. The control plane cannot change that file.
4. **Reject, never strip.** A rejected spec fails the deploy with an error that names the field and shows the `agent.toml` lines that would allow it.
5. **Compose is checked after resolution:** after `compose-go` has resolved interpolation, `extends` and profiles.
6. **The same file holds command policy:** it can forbid classes of command outright, such as container exec and deletes, and restrict deploys to listed repositories or registries (S§3.1).
7. **The check and its escape suite are written in week 1, before any `Deploy` exists** (S§3.2, B W1-1).

**Draft `agent.toml`** — _Proposed_; W1-1 finalises it. Only the `[[containers.allow]]` block is from the plan (S§3.2).

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
image       = "prom/node-exporter"      # digest pinning supported
pid_host    = true
bind_mounts = ["/proc:/host/proc:ro", "/sys:/host/sys:ro"]
```

_Proposed_ rules for the file: an unknown key is an error and the agent does not start; the file is owned by root and not writable by the service user; enrolment state lives under `/var/lib/moonglass`, not here.

## Consequences

- A control plane under someone else's control cannot start a privileged or host-namespace container, mount the Docker socket or host paths, or read host files (S§4).
- Some legitimate tools, such as node exporters and backup agents, need an explicit allow entry written on the server (S§2).
- Every rejection must explain itself; sanitizer rejections are measured by field from launch (M§6).
- `internal/policy` is trusted core, and the escape suite is one of the five permanent suites (S§7).
- Not covered: a kernel or container-runtime escape, a malicious image acting inside its sandbox, an app leaking its own secrets (S§3.2).

## Alternatives rejected

- **Relying on the typed protocol alone:** the root shell above (S§3.2).
- **Silently stripping denied fields:** nothing is silently stripped; the deploy fails and says why (S§3.2).
- **Exceptions set from the control plane or UI:** local policy beats remote command (S§3.1).

## Open questions

1. **Allow-key vocabulary.** The plan shows `pid_host` and `bind_mounts`. W1-1 needs one key per deniable field. _Proposed:_ names mirror the denied field, and a block allows only what it lists.
2. **What an allow matches.** The example matches a project and an image. Undecided: whether a block can also name an environment or service, and whether `image` may be a tag or must be a digest. A tag is easier to write; a digest stops a repointed tag from inheriting the exception.
3. **The sysctl allow-list.** S§3.2 says "a short namespaced allow-list" without listing it. W1-1 needs the entries.
4. **The agent-managed data directory.** S§3.2 exempts it from the bind-mount rule but does not say which paths it covers or which containers may mount it.
5. **When a changed file takes effect:** on agent restart only, or on a reload. Restart is simpler and has one code path; reload avoids restarting the agent for a policy edit.
6. **Wording in `AGENTS.md`.** Its summary omits SELinux, `sysctls`, `cgroup_parent`, `runtime`, `device_cgroup_rules` and BuildKit entitlements. This ADR follows the fuller S§3.2 list; the owner may want the summary aligned.

## Links

- S§2, S§3.1, S§3.2, S§3.5, S§3.13, S§4, S§7, S§9; V§1; M§6
- B§6: W1-1, W2-3, W2-6, W3-4, W5-3, W6-4
- `docs/threat-model.md` §6 and checklist item 2
