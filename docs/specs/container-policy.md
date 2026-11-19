# Container-spec policy and `agent.toml`

This is the behaviour spec for the container-spec sanitizer and the `agent.toml` schema (ticket W1-1). It implements ADR-0004 and `docs/threat-model.md` §6, and changes neither. Where ADR-0004 fixes a name or a rule, this spec repeats it. Where it does not, the choice is made in "Choices made here" and may be amended by the owner.

Sources: ADR-0004, ADR-0009 (policy dependencies, written by W1-1), `docs/threat-model.md` §6, §7 and §9 (checklist item 2), `docs/plan/stack.md` §3.1 and §3.2.

The code is `internal/policy`, trusted core (threat model §7). The escape suite is `internal/policy` tests plus `testdata/escape/`, one of the five permanent suites. It is never red.

## What this ticket delivers

A library, not a feature. There is no `Deploy` yet.

- Load and validate `/etc/moonglass/agent.toml`.
- Check one container spec against the deny list and the allow blocks.
- Turn a Compose project into container specs after `compose-go` has resolved it, and check each one.
- Return a `Rejection` that says what failed and what would allow it.

Wiring this into the agent, mapping a `Rejection` to a protocol error code, the Deploy executor (W2-3), command-policy enforcement (W5-3a, W5-3; `docs/specs/local-policy.md`) and Compose reconcile (W6-4) are later tickets. They must call this package for every container, build and pre-deploy step they create, and must create only what `Check` returned.

## API

Names are fixed so later tickets can depend on them. Signatures are indicative; the types and behaviour are the contract.

```go
type Paths struct {
    MountsRoot        string   // /var/lib/moonglass/mounts
    DockerSockets     []string // /var/run/docker.sock, /run/docker.sock
    ContainerdSockets []string // /run/containerd/containerd.sock
    DockerRoot        string   // from the Engine, usually /var/lib/docker
}

type Env struct {
    Paths            Paths
    DefaultRuntime   string                          // from the Engine, usually "runc"
    ContainerProject func(ref string) (string, bool) // project label of a container, if known
}

func Parse(data []byte, env Env) (*Policy, error)         // agent.toml bytes
func LoadFile(path string, env Env, owner int) (*Policy, error)
func (p *Policy) Config() Config                          // update, commands, sources
func (p *Policy) Check(s Spec) (Checked, error)           // error is *Rejection
func (p *Policy) CheckCompose(ctx context.Context, in ComposeInput) ([]Checked, error)
```

`Checked` wraps a `Spec` and can be built only by `Check`. `Checked.Spec()` returns a deep copy. The Deploy executor builds Engine requests from a `Checked` and nothing else, so the spec that was checked is the spec that is sent (checklist item 2).

`Check` never modifies its input and never returns a changed spec: **reject, never strip.**

### Spec

The neutral description of one container or build the agent is about to create. Every creation path builds one: apps, Compose services, one-click databases, pre-deploy steps, previews, backup jobs and builds.

| Field | Meaning |
|---|---|
| `Kind` | `app`, `compose`, `database`, `pre_deploy`, `preview`, `backup`, `build`. Used in errors only. |
| `Project` | Project name. Required. Must be a valid path element (see "Choices made here"). |
| `Environment`, `Service` | Optional. Matched by a block's `environment` and `service`. |
| `Image` | The image reference to run. |
| `Privileged` | `privileged`. |
| `Mounts` | `[]Mount{Type, Source, Target, ReadOnly}`. `Type` is `bind`, `volume` or `tmpfs`. |
| `NetworkMode`, `PID`, `IPC`, `UTS`, `UserNS` | As Compose names them (`network_mode`, `pid`, `ipc`, `uts`, `userns_mode`). |
| `CapAdd`, `Devices`, `DeviceCgroupRules`, `SecurityOpt` | Lists, as Compose names them. |
| `Sysctls` | `map[string]string`. |
| `CgroupParent`, `Runtime` | Strings. |
| `BuildEntitlements` | BuildKit entitlements for a build. |
| `Cmd`, `Env`, `Labels`, `AgentLabels`, `Networks`, `RestartPolicy`, `LogConfig` | Pass-through fields (W2-9). See "Pass-through fields and agent labels". |

A `Spec` with an empty `Project`, a relative bind source or an unknown `Mount.Type` is rejected as malformed (a violation with field `spec`), not defaulted.

## The deny list

ADR-0004 decisions 1 and 2. Every row is denied unless a matching allow block allows it. `Key` is the allow key (see "Allow blocks").

| Denied | `Key` |
|---|---|
| `privileged: true` | `privileged` |
| A bind mount of a host path, other than a named volume or an allowed data directory | `bind_mounts` |
| A mount of an agent-managed data directory not named by a block, or by a block for another image or project | `data_dirs` |
| A mount of `docker.sock`, under any path or alias | `docker_socket` |
| A mount of `containerd.sock`, under any path or alias | `containerd_socket` |
| A mount of the Docker data root, under any path or alias | `docker_data_root` |
| `network_mode: host` | `network_host` |
| `pid: host` | `pid_host` |
| `ipc: host` | `ipc_host` |
| `uts: host` | `uts_host` |
| `userns_mode: host` | `userns_host` |
| `network_mode: container:<id>` where `<id>` is not a container of the same project | `network_container` |
| `cap_add`, any entry | `cap_add` |
| `devices`, any entry | `devices` |
| `device_cgroup_rules`, any entry | `device_cgroup_rules` |
| `security_opt` that disables seccomp, AppArmor or SELinux | `security_opt` |
| `sysctls` other than the two allow-listed ones | `sysctls` |
| `cgroup_parent`, any value | `cgroup_parent` |
| `runtime` other than the default runtime | `runtime` |
| BuildKit entitlement `security.insecure` | `build_entitlements` |
| BuildKit entitlement `network.host` | `build_entitlements` |
| A `Labels` key in the `dev.moonglass.` namespace (never allowable) | `agent_labels` |

`DeniedFields` is an exported, ordered list of these rows. The escape suite iterates it (see "Escape suite").

### Mount rules

Applied to every `bind` mount, in this order. A `volume` mount of a plain named volume and a `tmpfs` mount are never checked.

1. **Raw path.** The source must be absolute and must contain no `..` element. Otherwise it is rejected. It is never cleaned into something acceptable.
2. **Resolve.** Resolve symlinks on the longest existing prefix of the source and append the rest lexically. Also compare by file identity (`os.SameFile`) against each configured socket that exists, so a hard link or bind alias of a socket is caught.
3. **Protected.** A source is protected when its resolved path equals, is inside or is an ancestor of a path in `DockerSockets`, `ContainerdSockets` or `DockerRoot`, or when it is the same file as a socket, or when its final element is `docker.sock` or `containerd.sock`. A protected source is denied unless the block sets the matching key, and a key allows only a source that is exactly the socket, or (for `docker_data_root`) the data root or something inside it. An ancestor, such as `/`, `/var` or `/run`, is never allowed. `bind_mounts` never covers a protected source.
4. **Data directory.** A source equal to, inside or an ancestor of `MountsRoot` is governed only by `data_dirs` (below). `bind_mounts` never covers it.
5. **Other host path.** Allowed only when a matching block's `bind_mounts` lists an entry whose source equals the raw source (trailing slash ignored) and whose target equals the mount target. An entry with no mode, or `rw`, allows a read-write or read-only mount. An entry with `ro` allows a read-only mount only.

### Named volumes that are really bind mounts

A named volume exempt from the check must really be one. When Compose resolution finds a top-level volume with `driver: local` (or no driver) and `driver_opts` that bind a host path (`o` contains `bind`, or `type: none`, with a `device`), the service's mount is a `bind` mount of `device` and goes through the mount rules.

### Namespaces and `network_mode`

`host` is compared case-insensitively. `network_mode: container:<id>` is allowed without a block only when `ContainerProject(<id>)` reports the same project. An unknown container is cross-project. `network_mode: service:<name>` is Compose's own form; after resolution it names a service of the same Compose project, and is allowed without a block when that service exists in the project. Every other value (`bridge`, `none`, a network name) is allowed.

### `security_opt`

An entry is denied when, ignoring case and treating `:` and `=` the same, it is `seccomp=unconfined`, `apparmor=unconfined`, `label=disable`, or `label=disabled`. Other entries (such as `no-new-privileges`) pass. See "Open questions" for custom profiles.

### Sysctls

ADR-0004 decision 3.

- `net.core.somaxconn` and `net.ipv4.ip_local_port_range` pass without a block.
- A **namespaced** sysctl is one named `net.*` or `fs.mqueue.*`, or exactly `kernel.msgmax`, `kernel.msgmnb`, `kernel.msgmni`, `kernel.sem`, `kernel.shmall`, `kernel.shmmax`, `kernel.shmmni` or `kernel.shm_rmid_forced`. A namespaced sysctl not on the allow-list needs a block listing it under `sysctls`.
- Any other sysctl (`kernel.core_pattern`, `vm.*`, `fs.suid_dumpable`, and so on) is **not namespaced and can never be allowed.** A block that lists one is invalid and stops the agent. A spec that sets one is rejected with an error that says no block can allow it.
- A `net.*` sysctl in a spec that shares the host network (`network_mode: host`, or `container:` of another project) and an IPC sysctl in a spec with `ipc: host` act on the host. They are treated as not namespaced and cannot be allowed.

### Runtime

A non-default runtime is any `Runtime` that is not empty and not `Env.DefaultRuntime`.

### Build entitlements

`security.insecure` and `network.host` are denied at build time the same way as the rows above. Compose `build.entitlements` maps to `BuildEntitlements`, and `build.privileged: true` is treated as `security.insecure`. A build has no digest image, so no block can currently match it (see "Open questions").

## Pass-through fields and agent labels (W2-9)

ADR-0004 decisions 10 to 12. The Engine request is built from `Checked` and nothing else, so every field the executor sends is in the `Spec`. The fields below are not judged by the deny list. `Check` copies them into `Checked` unchanged, except for the two label rules.

| Field | Type | Meaning |
|---|---|---|
| `Cmd` | `[]string` | Exec argv that replaces the image's command (a pre-deploy step). Empty means the image's own. Never joined, never passed to a shell. |
| `Env` | `map[string]string` | Environment variables for the container. |
| `Labels` | `map[string]string` | User-supplied labels (Compose `labels`). |
| `AgentLabels` | `map[string]string` | Labels the agent sets. Filled only by agent code, never from a protocol message or a Compose file. |
| `Networks` | `[]string` | Docker networks to attach. The executor sets the project network `mg-<Project>` (`docs/specs/release.md`). |
| `RestartPolicy` | `string` | `no`, `on-failure` or `unless-stopped`; empty is `no`. |
| `LogConfig` | `Driver string`, `Options map[string]string` | The log driver and its options. |

Rules:

- A key of `Labels` that starts with `dev.moonglass.` is a violation with key `agent_labels`. **No block can allow it**: a block that names `agent_labels` is invalid, like a sysctl that is not namespaced, and the rejection gives no snippet. The message says that only the agent sets labels in that namespace.
- Every key of `AgentLabels` must start with `dev.moonglass.`. Any other key makes the spec malformed (field `spec`).
- The Engine request's labels are `Labels` plus `AgentLabels`. They cannot collide, by the two rules above.
- `CheckCompose` fills `Labels` from each service's `labels` and never fills `AgentLabels`.
- **Environment values are never logged and never written into an error, a `Rejection`, a `Violation`, a command result or an audit record.** Names may be. A test sets a value that is also a field name elsewhere in the spec and asserts that it appears nowhere in the rejection text.
- `RestartPolicy` other than the three values, or a `LogConfig` with an empty `Driver` and non-empty `Options`, makes the spec malformed (field `spec`).

The escape suite gains a fixture for a Compose service with a `dev.moonglass.project` label (rejected), and a table row for each pass-through field showing it is copied unchanged.

## Allow blocks

ADR-0004 decision 5. All exceptions live in `[[containers.allow]]` blocks in `/etc/moonglass/agent.toml`.

```toml
[[containers.allow]]
project     = "monitoring"                       # required
image       = "prom/node-exporter@sha256:<64 hex>"   # required, digest only
environment = "production"                       # optional, narrows
service     = "node-exporter"                    # optional, narrows
pid_host    = true                               # one allow key per denied field
bind_mounts = ["/proc:/host/proc:ro", "/sys:/host/sys:ro"]
```

### Matching

A block applies to a spec only when **every key it names matches**:

- `project` equals `Spec.Project`.
- `image` matches `Spec.Image` by repository and digest. Names are normalised so `prom/node-exporter` and `docker.io/prom/node-exporter` are the same repository, `library/` is implied for a one-element Docker Hub name, `localhost` is a registry host, and `index.docker.io` folds into `docker.io`. A spec image with no digest (a tag, or nothing) matches no block. A spec image `name:tag@sha256:…` matches on name and digest and ignores the tag, as the Engine does.
- `environment`, when named, equals `Spec.Environment`.
- `service`, when named, equals `Spec.Service`.

When several blocks apply, a field is allowed if any of them lists it. A block never allows a field it does not list: `pid_host = true` does not allow `ipc: host`.

### Allow keys

Each allow key is named after the denied field it allows (rows of the deny list). ADR-0004 fixes `pid_host`, `bind_mounts` and `data_dirs`. The rest follow the same rule and are listed under "Choices made here".

| Key | Type | Allows |
|---|---|---|
| `privileged`, `docker_socket`, `containerd_socket`, `docker_data_root`, `network_host`, `network_container`, `pid_host`, `ipc_host`, `uts_host`, `userns_host` | boolean, `true` only | The one denied value |
| `bind_mounts` | list of `"source:target[:ro\|rw]"` | Exactly those host bind mounts |
| `data_dirs` | list of names | The agent-managed directories `mounts/<project>/<name>` |
| `cap_add` | list of capability names | Exactly those capabilities. Names are upper-cased and lose a `CAP_` prefix. |
| `devices` | list of `"host[:container[:perms]]"` | Exactly those devices. Container defaults to host and perms to `rwm`; comparison is on the normalised form. |
| `device_cgroup_rules` | list of strings | Exactly those rules |
| `security_opt` | list of strings | Exactly those entries |
| `sysctls` | list of sysctl names | Those namespaced sysctls, any value |
| `cgroup_parent`, `runtime` | string | Exactly that value |
| `build_entitlements` | list from `security.insecure`, `network.host` | Those entitlements |

### Validation

An unknown key or an invalid block is an error and the agent does not start. A block is invalid when:

- `project` or `image` is missing or empty, or `environment` or `service` is present and empty.
- `project`, or any `data_dirs` name, is not a valid path element: empty, `.`, `..`, or containing `/`, a NUL or a control character.
- `image` is not `name@sha256:<64 lowercase hex>`. A tag, with or without a digest, is an error, and the message shows the digest form: `name@sha256:<digest>`.
- It lists no allow key (it would allow nothing).
- A boolean key is not `true`.
- A list is empty, has an empty or duplicate entry, or an entry that does not parse in the form above (a relative `bind_mounts` source or target, an unknown entitlement, a malformed capability).
- `sysctls` lists a name that is not namespaced.
- A `bind_mounts` source is protected, or inside or an ancestor of `MountsRoot` (mount rules 3 and 4). Those are governed by their own keys.

Every error names the block by its position (`containers.allow[2]`) and the key.

## The agent-managed data directory

ADR-0004 decision 4. The directory is `<MountsRoot>/<project>/<name>`. The agent creates and owns these paths. Agent state never lives under `MountsRoot`. This package only checks mounts of them; creating them is a later ticket.

A mount whose source is under `MountsRoot` is allowed only when a matching block names the directory in `data_dirs`: the block's project equals the directory's `<project>` and equals `Spec.Project`, and `<name>` is listed. The source may be that directory or a path inside it. The path is resolved first:

- a symlink anywhere between `MountsRoot` and the final element (inclusive) is rejected, even when it resolves back inside;
- `..` is rejected by the raw-path rule;
- a resolved path outside `MountsRoot/<project>/<name>` is rejected. That covers another project's subtree, the parent `MountsRoot/<project>`, `MountsRoot` itself, and any agent state.

Agent state outside `MountsRoot` (`/var/lib/moonglass/…` other than `mounts/`) has no data-directory form; mounting it is a plain host bind mount that needs a `bind_mounts` entry (see "Open questions").

## Compose

ADR-0004 decision 7. Compose input is checked after `compose-go` has resolved it.

```go
type ComposeInput struct {
    Project     string
    Environment string
    WorkDir     string            // project source directory; relative paths resolve here
    Files       []string          // compose file paths, in order
    Env         map[string]string // the only source of interpolation variables
}
```

Rules for `CheckCompose`:

- The loader runs with the project's own files and `Env`. The agent's own process environment is never read, so a variable cannot carry a value the deploy did not supply.
- Interpolation, `extends`, `include`, YAML anchors and merge keys, and `x-` extension fields are resolved by the loader. The check sees only the resolved result.
- **Every profile is enabled.** A service behind `profiles:` is checked like any other, so a profile switched on later cannot smuggle in a denied field.
- Every service becomes a `Spec` (`Kind` `compose`, `Service` the service name, `Project` and `Environment` from the input). Relative bind sources are made absolute against `WorkDir`. Named volumes are resolved as described under "Named volumes that are really bind mounts".
- **All services are checked and all violations are returned together**, so a fix does not take one deploy per field.
- A loader error (unreadable file, unset required variable, invalid YAML, an unsupported or unknown field the loader rejects) is returned as an error. Nothing is checked or returned in that case.

## `agent.toml`

ADR-0004 decisions 5 and 8, and its schema block. The file is parsed strictly: any key not in this schema is an error.

```toml
[update]
channel = "stable"                      # "stable" or "canary"
[commands]
forbid = ["delete"]                     # "delete", "exec"
[sources]
repositories = ["github.com/acme/*"]
registries   = ["ghcr.io/acme/*"]

[[containers.allow]]
# see "Allow blocks"
```

| Key | Rule |
|---|---|
| `update.channel` | `stable` or `canary`. Absent means `stable`. |
| `commands.forbid` | List of `delete` and `exec`. Any other class, or a duplicate, is an error. Absent means nothing is forbidden. Which command types are in each class: `docs/specs/local-policy.md` (W5-3). |
| `sources.repositories`, `sources.registries` | Lists of glob patterns in Go `path.Match` syntax, written in the normal form (`docs/specs/local-policy.md`): a `repositories` pattern is lower case `github.com/<owner>/<repo>`, and a `registries` pattern names a literal host and a path, never `index.docker.io` or `moonglass.local`. A pattern with bad syntax, an empty or duplicate entry, or one not written in the normal form is an error. A present, empty list allows nothing of that kind; an absent key does not restrict it. |
| `containers.allow` | Array of tables, see "Allow blocks". |

`Parse` and `LoadFile` return the parsed `Config` and the compiled allow blocks. They only load and validate `[commands]` and `[sources]`. What they mean, how a source is matched and where the agent enforces them is `docs/specs/local-policy.md`. A list in the `Config` is nil when its key is absent and empty when the key is present and lists nothing; the two mean different things (`docs/specs/local-policy.md`, "An empty list").

### The file

- `LoadFile` requires a regular file, not a symlink, readable by its owner and not writable by group or others. It must be owned by `owner` (the agent passes `0`). Each check runs once; a file whose owner uid cannot be read is refused, so the owner comparison is never skipped. Otherwise it is an error that says to fix `chmod` or `chown`.
- Every error `LoadFile` returns names the file's path, and never includes the file's contents.
- **An absent file is an empty configuration:** no exceptions, nothing forbidden, no source restrictions, channel `stable`. A present file that cannot be read, parsed or validated is an error and the agent does not start.
- The file is read once. There is no reload, watcher or signal handler. A change takes effect on agent restart.
- Enrolment state and keys are not in this file and this package never reads or writes `/var/lib/moonglass`.
- Nothing in the protocol, the API or the UI reaches this package's configuration. `Parse` and `LoadFile` are the only constructors.

## Rejection

A rejected spec fails the deploy. `Check` returns `*Rejection`, which holds every `Violation` found for the spec and renders to an error like this:

```
container policy rejected service "node-exporter" (project "monitoring"):
  pid: host is not allowed
  volumes: bind mount of /proc is not allowed
To allow these, add to /etc/moonglass/agent.toml:

  [[containers.allow]]
  project     = "monitoring"
  image       = "prom/node-exporter@sha256:<digest>"
  pid_host    = true
  bind_mounts = ["/proc:/host/proc:ro"]

Then restart the agent. The file is read at start-up only.
```

- A `Violation` has `Field` (the Compose-style field, with the offending value), `Key` (the allow key) and `Value` (what to write).
- The snippet always names `project` and `image`. The image is the spec's digest form when it has one, and `<name>@sha256:<digest>` otherwise. It never names `environment` or `service`; a narrower block is the operator's choice.
- Several violations of one spec are merged into one snippet.
- For a protected mount, a symlink, a `..` or a path outside `mounts/<project>/` the message says exactly that and gives the `data_dirs` or `bind_mounts` form only where one could apply. For a sysctl that is not namespaced, it says no block can allow it and gives no snippet.
- Errors contain field names, paths, image references, sysctl names and environment variable names. They never contain environment values.
- `CheckCompose` returns one `*Rejection` listing every failing service.

## Escape suite

ADR-0004 decision 9. The suite is written before any `Deploy` exists.

### Layout

```
testdata/escape/compose/<case>/
    compose.yaml           # plus any files it extends or includes
    agent.toml             # optional; absent means an empty policy
    env                    # optional KEY=VALUE lines, the ComposeInput.Env
    expect                 # "ok", or the Violation.Key of every violation, one per line
```

The test substitutes the tokens `@MOUNTS@`, `@DOCKER_SOCK@`, `@CONTAINERD_SOCK@` and `@DOCKER_ROOT@` in `agent.toml`, `env` and `expect` with paths it creates under `t.TempDir()`, and sets the same as `Paths`. Fixtures that need symlinks, hard links or a symlinked mounts directory create them in test code first; git does not store them portably.

### Required coverage

Every fixture has a table row or a directory. The suite covers at least:

- every row of the deny list, once as a plain field and once with an allow block that names a different field (not allowed);
- every allow key, once allowing its own field;
- each sysctl on the allow-list; a namespaced sysctl not on it (rejected, then allowed by a block); a host sysctl (rejected, and a block that lists it is invalid);
- a tag, and a tag with a digest, in place of a digest, in a block (agent does not start) and in a spec (no block matches);
- a block that matches by project only, by image only, by wrong environment, by wrong service;
- `data_dirs`: allowed for its image and project; rejected for another image, another project's subtree, the project directory itself, `MountsRoot` itself, agent state, a symlink inside `mounts/`, a symlinked `mounts/` directory and a `..`;
- the Docker socket by every alias: `/var/run/docker.sock`, `/run/docker.sock`, a symlink to it, a `..` path to it, a parent directory (`/`, `/var/run`, `/run`), a hard link or bind alias, the short and long volume syntax, an interpolated source, and a named volume whose `driver_opts` bind it;
- the containerd socket and the Docker data root by the same aliases;
- `privileged` hidden behind each of: `${VAR:-true}` interpolation, `extends` (one level and chained), `include`, a YAML anchor and merge key, a `profiles:` service, and a value from the supplied `Env` only;
- an `extends` or interpolation that turns a benign service into one that mounts the socket;
- `network_mode: container:<id>` in the same and another project, `service:<name>`;
- build entitlements and `build.privileged`;
- a benign project (named volume, tmpfs, allow-listed sysctls, `no-new-privileges`, an allowed same-project network mode) that passes untouched.

### Meta test

`TestEscapeSuiteCoversEveryDeniedField` fails when a row of `DeniedFields` has no table row in `TestCheckDeniesEveryField` and no fixture whose `expect` names its key. Adding a denied field without a test turns the suite red.

## Choices made here

ADR-0004 leaves these open. They are the narrowest reading of it, and each is covered by a test.

1. The allow keys other than `pid_host`, `bind_mounts` and `data_dirs`, and their value forms.
2. `data_dirs` lists names, not paths. The project comes from the block.
3. Boolean keys accept only `true`. List and string keys are exact matches.
4. A block with no allow key is invalid.
5. When several blocks match, their allowed fields are united.
6. Docker socket, containerd socket and data root each have their own key and are allowed only for the exact path (the data root, or inside it), never an ancestor.
7. A `name:tag@sha256:…` image is an error in a block, even though it holds a digest.
8. An absent `agent.toml` is the empty configuration.
9. Unknown `[commands]` values, duplicates, empty entries and an empty `forbid` list are errors. The `[sources]` rules are in `docs/specs/local-policy.md`: a pattern not in the normal form is an error, and a present, empty list allows nothing.
10. A project or data-directory name is a path element.
11. `network_mode: service:<name>` is allowed within the same Compose project.

## Open questions

Each needs an owner decision before a ticket builds on it. None blocks W1-1; W1-1 builds the narrower behaviour.

1. **Builds and allow blocks.** A build has no digest image, so a `build_entitlements` block can never match and builds with those entitlements are always rejected. ADR-0004 says entitlements are denied "the same way", which implies an allow path. A later ticket must define the build identity (for example the digest of the base image) and amend ADR-0004.
2. **Agent state.** `bind_mounts` can name `/var/lib/moonglass` or `/etc/moonglass`, which would expose enrolment keys and `agent.toml` to a container. ADR-0004 does not forbid it. Should these be never allowable, like a host sysctl?
3. **`security_opt` beyond "disables".** Custom seccomp or AppArmor profiles, `systempaths=unconfined` and `label=type:…` can weaken confinement without "disabling" it. Only the ADR's wording is enforced.
4. **Docker fields outside the ADR table**, such as `cgroup: host`, `gpus` and non-`local` volume drivers, are not checked. Adding them needs an ADR amendment.
5. **Loader file reads.** `extends`, `include` and `env_file` can name files outside `WorkDir`. They are read by the loader but never mounted. Whether to bound them is a later decision.

6. **`pid: container:<id>` and `ipc: container:<id>`.** ADR-0004 decision 1 names `pid: host`, `ipc: host`, and only `network_mode: container:<id>` as a cross-project check. A spec that joins another project's PID or IPC namespace is therefore not denied by this ticket. Whether those forms should be checked the same way as `network_mode` is a later decision, and would amend the ADR.
7. **`network_mode: service:<name>` inherits the target's network namespace.** The narrower behaviour allows it when the named service is in the same Compose project, with no further check of whether that service itself uses the host network or a cross-project `container:` mode. A later ticket may want to reject or require a block when the joined service would not itself pass without one.

8. **Cross-project `Networks`.** Nothing in the protocol sets `Networks`; the executor attaches only the project's own network. A Compose service that names another project's network (W6-4) is not checked by W2-9. Whether to deny it is a decision for W6-4, with an ADR-0004 amendment.

## Not covered

A kernel or container-runtime escape, a malicious image acting inside its sandbox, and an app leaking its own secrets (ADR-0004, threat model §5).
