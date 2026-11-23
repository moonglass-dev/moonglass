# Local command policy: `[commands]` and `[sources]`

**Status: Approved.** This refines the `agent.toml` schema and ADR-0004 decision 8, which are contracts (ADR-0004 decisions 13 and 14). The decisions are listed at the end.

What W5-3a and W5-3 build. `docs/specs/container-policy.md` loads and validates `[commands]` and `[sources]` (W1-1); this spec says what they mean and where the agent enforces them. Local policy beats remote command (threat model §8): nothing the control plane sends can widen what `agent.toml` allows.

## Which ticket builds what

| Ticket | Builds |
|---|---|
| W5-3a | Pure decisions in `internal/policy`: the source normal form, pattern validation at load, the empty-list rule, `CheckImageSource`, `CheckRepositorySource`, `Forbids` |
| W5-3 | Enforcement in the agent: the command-class table and its coverage test, the `REJECTED` answers, the source checks before any pull or clone, the `moonglass.local` rule and the repository label on builds |

W5-3 needs the command dispatcher (W2-10) and `BUILD` (W3-4), so it depends on both. W5-3a needs neither.

## Command classes

`[commands] forbid` takes the classes `delete` and `exec` (ADR-0004). Every command type the agent accepts has exactly one class. A command type is one the control plane sends to ask the agent to act: every server-to-agent `MessageType` except `HEARTBEAT_ACK` and `ERROR`.

| Class | Rule | Command types |
|---|---|---|
| `delete` | The type is sent by a registry operation that is `Destructive: Yes` (ADR-0006 decision 4) | `STOP_APP` and `REMOVE_APP` (W5-5a, `docs/specs/delete.md`). Later: a database restore that replaces data (W6-3) and any volume removal |
| `exec` | Runs a process in an existing container | None. Container exec is v1 (threat model §8) |
| none | Anything else | `DEPLOY`, `SWITCH_RELEASE`, `ROLLBACK`, `RUN_PRE_DEPLOY`, `BUILD`, `LOG_REQUEST` (W4-4); later `UPDATE_OFFER` (W7-6d) and the secret messages of W4-8, W3-3 and W4-9 |

- Deploy, rollback and pre-deploy steps are not destructive (ADR-0006), so they are not `delete`, even though a pre-deploy step can run an irreversible migration.
- **Forbidding a class with no command type yet is accepted and does nothing.** `forbid = ["exec"]` is valid today; it starts refusing the day a type in that class exists. No type is added to make it testable.
- **The table is code**, beside the agent's dispatcher (`internal/agent`), mapping each `agentv1.MessageType` to a `policy.Class`. `TestEveryCommandTypeHasAClass` walks every value of the generated `MessageType` enum: each must be on the fixed list of non-command types (`ENROLL_REQUEST`, `ENROLL_RESPONSE`, `HEARTBEAT`, `HEARTBEAT_ACK`, `ERROR`, `COMMAND_RESULT`, `LOG_CHUNK`, `USAGE` and later agent-to-server reports) or in the table. A ticket that adds a command type and forgets its class turns the test red.
- A `delete` or `exec` command type must be one the agent answers with a `CommandResult`. A future type that cannot is a protocol design error, caught in its spec.

### The answer to a forbidden command

The class check runs first, before the identity binding, the source check, the disk guard or any Engine call. A forbidden command is answered:

- `CommandResult` with outcome `REJECTED`;
- `detail` exactly `agent.toml on this server forbids <class> commands ([commands] forbid in /etc/moonglass/agent.toml); <TYPE> was not run`;
- `app` the app's state, unchanged.

The result is recorded like any other (`docs/specs/connection.md`, "Each request id runs once"), so a re-sent command gets the same answer. The control plane fails the call with that detail. Nothing about it is retried.

## Sources

`[sources]` has two keys. When a key is present, the agent runs only what matches one of its patterns; an absent key does not restrict that kind (`docs/specs/container-policy.md`). Patterns use Go `path.Match` syntax, where `*` never crosses a `/`.

### What `registries` is matched against

The image reference, in one normal form, without its tag or digest. The same normaliser serves the allow blocks (`internal/policy`), so there is one rule for image names:

1. Split off the digest (`@…`) and the tag (a `:` in the last path element).
2. If the first element contains `.` or `:`, or is `localhost`, it is the registry host. Otherwise the host is `docker.io` and is prefixed.
3. `index.docker.io` becomes `docker.io`. The host is lower-cased.
4. A Docker Hub name with one element gets `library/`.

| Reference | Normal form |
|---|---|
| `nginx:1.27` | `docker.io/library/nginx` |
| `acme/web@sha256:…` | `docker.io/acme/web` |
| `GHCR.io/acme/web:v2` | `ghcr.io/acme/web` |
| `localhost:5000/web` | `localhost:5000/web` |
| `ghcr.io/acme/team/web` | `ghcr.io/acme/team/web` (`ghcr.io/acme/*` does not match it; `ghcr.io/acme/*/*` does) |

An image with upper case in its normal form's path is rejected whatever the patterns, `moonglass.local` included: the Docker Engine reads a first element with upper case as the registry host and never accepts an upper-case path, so the normal form would name a host the Engine does not pull from.

**Patterns must be written in the normal form,** checked when `agent.toml` loads. A `registries` pattern that is not lower case, whose first element is not a literal host (it contains `*`, `?` or `[`, or has no `.` or `:` and is not `localhost`), that names `index.docker.io`, or that names `moonglass.local` is an error and the agent does not start. The message shows the normal form, such as `docker.io/library/nginx`. `docker.io/*` matches nothing an image can normalise to; official images need `docker.io/library/*`.

**`moonglass.local` images** are built on the server (`docs/specs/build.md`) and are never matched against `registries`:
- The agent never pulls a reference whose host is `moonglass.local`. One that is not in the local image store is `REJECTED`.
- Its path must be `moonglass.local/<project>/<service>`, with the project and service of the `DEPLOY` that names it. Any other is `REJECTED`.
- When `repositories` is present, the image's `dev.moonglass.repository` label (below) must match it. An image with no label is `REJECTED`.

### What `repositories` is matched against

`github.com/` followed by `Build.repository` (`<owner>/<repo>`, `docs/specs/protocol.md`), lower-cased: `Acme/Web` becomes `github.com/acme/web`. GitHub names are case-insensitive, so matching is too.

Patterns are checked at load: each is lower case, starts with the literal `github.com/` and has exactly three elements, so `github.com/acme/*` and `github.com/*/*` are valid and `github.com/acme` is an error that suggests `github.com/acme/*`.

**Every build is labelled.** Every `docker buildx build` argv carries `--label dev.moonglass.repository=github.com/<owner>/<repo>` (lower-cased, W5-3). It is an agent label (ADR-0004 decision 11), set from the validated `Build` fields.

### An empty list

A present, empty list allows nothing of that kind: `registries = []` makes a server that runs only its own builds, and `repositories = []` one that never builds.

### What is checked, and when

| Command | Checked against | When |
|---|---|---|
| `DEPLOY` | `registries` for `image`, or the `moonglass.local` rule | After the identity binding, before any Engine call: no pull, inspect or create happens for a rejected image |
| `BUILD` | `repositories` for `repository` | After the identity binding, before the clone |
| `SWITCH_RELEASE`, `ROLLBACK`, `RUN_PRE_DEPLOY` | Nothing | They act on a release whose image was admitted by its `DEPLOY` |

- The check is on the reference as sent, not on the digest it resolves to. A digest's repository is the reference's own, so resolving adds nothing.
- **A build's base images are not checked.** The `FROM` lines of a Dockerfile are the repository's choice, and parsing them (stages, `ARG` substitution) cannot be done reliably. `repositories` says which code is built, not which images it starts from (threat model §5, item 5).
- Later commands that name an image or a repository check every one, in the ticket that adds them. Compose import (W6-4) adds none: it sends one `DEPLOY` per service, so each image is checked as any `DEPLOY` is.

A rejected source is answered `REJECTED` with `detail`:
- `agent.toml on this server allows images only from [sources] registries; <normal form> matches none of them. Add a pattern to [sources] registries in /etc/moonglass/agent.toml and restart the agent.`
- `agent.toml on this server allows builds only from [sources] repositories; github.com/<owner>/<repo> matches none of them. Add a pattern to [sources] repositories in /etc/moonglass/agent.toml and restart the agent.`

The detail names the rejected source and never lists the patterns.

## API (W5-3a)

In `internal/policy`, beside `Check`:

```go
type Class uint8 // ClassNone, ClassDelete, ClassExec

func (p *Policy) Forbids(c Class) bool                  // false for ClassNone
func (p *Policy) CheckImageSource(ref string) error     // *SourceRejection, or nil
func (p *Policy) CheckRepositorySource(repo string) error
```

`CheckImageSource` returns nil for a `moonglass.local` reference; the agent applies the `moonglass.local` rule itself, since only it can look at the image store (ADR-0014). The functions read only the parsed configuration: no Engine, no file, no network.

## Named tests

- W5-3a: `TestImageSourceNormalForm` (every row of the normal-form table), `TestImageSourceMatching`, `TestRepositorySourceMatching`, `TestSourcePatternsValidatedAtLoad`, `TestEmptySourceListAllowsNothing`, `TestForbidsClasses`.
- W5-3: `TestEveryCommandTypeHasAClass`; `TestForbiddenClassIsRejected` (a synthetic `delete`-class type in the test table); `TestDeployFromUnlistedRegistryIsRejectedBeforePull` and `TestBuildFromUnlistedRepositoryIsRejectedBeforeClone`, against a real Engine, asserting that nothing was pulled, created or cloned; `TestMoonglassLocalImageIsNeverPulled`; `TestRejectedSourceResultIsStored`.

## Not in W5-3

Reporting the local policy to the control plane, so the UI can explain a refusal before it happens. Base-image checks. Exec, which is v1.

## Decisions

Each line is a choice no ADR or spec fixed.

- **D43** A command type is `delete` when a destructive registry operation sends it; `STOP_APP` and `REMOVE_APP` are `delete`, and no other current type is. So `forbid = ["delete"]` refuses soft delete as well as purge (`docs/specs/delete.md`).
- **D44** Forbidding `exec` is valid and does nothing until exec exists (v1); no stand-in type is added.
- **D45** A forbidden command is answered `REJECTED` with the fixed detail above, checked before anything else.
- **D46** `registries` matches the normalised repository without tag or digest, with `docker.io` and `library/` filled in, `localhost` as a host and `index.docker.io` folded. The allow blocks share the normaliser, so `localhost/x` there means `localhost/x`.
- **D47** Patterns must be in the normal form with a literal host, or the agent does not start; a pattern that silently matches nothing is worse than a refusal to start.
- **D48** `repositories` matches `github.com/<owner>/<repo>`, lower-cased; patterns start with `github.com/` and have three elements.
- **D49** A build is checked against `repositories` only; Dockerfile base images are not checked, and the threat model says so.
- **D50** Sources are checked on `DEPLOY` and `BUILD` before any pull or clone, against the reference as sent; `SWITCH_RELEASE`, `ROLLBACK` and `RUN_PRE_DEPLOY` are not re-checked.
- **D51** `moonglass.local` images are never pulled, must belong to the deploying service, and are checked through the `dev.moonglass.repository` label each build carries. Without the label, an image built while `repositories` was wider could still be deployed.
- **D52** A present, empty `[sources]` list allows nothing of its kind; otherwise there is no way to say "build only" or "images only".
- **D53** W5-3a (pure decisions) depends on W1-1; W5-3 (enforcement) depends on W5-3a, W2-10 and W3-4.
