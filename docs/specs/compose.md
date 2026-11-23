# Compose import and reconcile

This is the behaviour spec for Compose import and reconcile (W6-4, built as W6-4a, W6-4b, W6-4c and W6-4). It turns the supported field subset, change detection, the pre-confirm list and apply order into behaviour those tickets can name acceptance tests against.

## Which ticket builds what

| Ticket | Builds |
|---|---|
| W6-4a | `policy.ResolveCompose`: load, the supported subset, the refusals, the deny list, each service's settings (`docs/specs/container-policy.md`, "Import resolution") |
| W6-4b | `releases.compose_hash`, the `deploy` input, `services.list` showing it (`docs/specs/storage.md`, `docs/specs/registry.md`) |
| W6-4c | `internal/compose`: the hash, the list, the apply order, the refusals that need the environment's services |
| W6-4 | `moonglass compose apply` |
| LN-13 | The subset real files need, in this spec and `docs/specs/container-policy.md`, before W6-4a builds it |
| LN-14 | The dashboard's Compose screen and resolution on the agent |

The service settings on `Deploy` and their agent side are W6-1a to W6-1d (`docs/specs/managed-services.md`, "Which ticket builds what").

## Where it runs

A Compose file's `environment` values are secrets, and the control plane may never hold one in plaintext (`docs/specs/secrets.md`), so a file is resolved only where its values can be read (ADR-0004 decision 16):

- **In the CLI** (`moonglass compose apply`, below): the file never leaves the operator's machine.
- **On an agent of the environment**, for the dashboard (LN-14): a file from the connected repository is read on the agent, and a pasted file is encrypted to the environment key in the browser first. The agent returns the resolved settings without any environment value, and the dashboard shows the list below from them.

Either way the file is resolved with `policy.ResolveCompose`, its environment values are encrypted and set with `secrets.set`, and each service is applied through the existing operations: `services.create`, `deploy` and `services.delete`. Each call is authorised and audited by the registry like any other (`docs/specs/registry.md`). The agent checks each service's container spec, as built from its `Deploy`, against its own `agent.toml` with `policy.Check`, like every other container.

## The command

```
moonglass compose apply --project P --env E --server S [--file FILE]… [--env-file FILE] [--domain SERVICE=NAME]… [--update SERVICE]… [--yes]
```

A local command on `registry.LocalCommands`. Its flags follow the generated-CLI rules (`docs/specs/registry.md`, "Generated CLI").

- `--file` names the Compose files, in order, relative to the current directory. Absent, it is `compose.yaml`. Relative paths inside the files resolve against the first file's directory.
- `--env-file` is the only source of interpolation variables. The shell's environment and a `.env` file are never read, so an import gives the same result on every machine from the same files.
- `--server` is the server every created service runs on. Existing services stay where they are.
- `--domain SERVICE=NAME`, repeated, gives a service a domain. It needs that service to have a port.
- `--update SERVICE`, repeated, marks a service `update` even when its hash is unchanged, to roll out a changed environment value now.
- `--yes` applies without asking.

## Supported fields

Only these service fields are supported. Anything else is refused before anything is applied (`ResolveCompose`, steps 2 and 3):

- `image`, required. `build` is refused.
- `ports`: at most one entry, TCP. Its container port is the service's `port`. The published host port is never used: Moonglass publishes no ports and routes through the proxy, and the list says so.
- `environment`: each entry becomes an `env` secret of the environment (below).
- `volumes`: named volumes only. A bind mount, a tmpfs and an anonymous volume are refused, and so is a top-level volume with a driver, options, `external`, labels or a `name`.
- `command`: the argv, as the loader splits it.
- `depends_on`: apply order only.
- `restart`: `no`, `on-failure` or `unless-stopped`; absent is `unless-stopped`. `always` is refused, because it would restart a stopped (soft-deleted) service at boot.
- `labels`: user labels; one in the `dev.moonglass.` namespace is refused.
- `healthcheck`: the Docker health mode of the switch (`docs/specs/release.md`, "Health").

`networks` may name only networks the file declares, and every one maps onto the project's network, `mg-<project>`; services reach each other by service name (ADR-0004 decision 15; `docs/specs/release.md`, "Service names"). A network that is `external` or names another project's network is refused. Top-level keys other than `services`, `volumes`, `networks` and `x-` extensions are refused.

## Mapping

- A Compose service is the Moonglass service of the same name in `--project` and `--env`. Its name must be a Moonglass name (`docs/specs/storage.md`).
- **Compose-managed** means a service whose current release has a non-empty `compose_hash`. Only compose-managed services are updated or removed by an import. A service of that name that exists and is not compose-managed (a database from `databases create`, or one deployed by hand) stops the import with `service <name> exists and was not created by Compose import; rename the Compose service or delete the service first`.
- A named volume `v` is the managed volume `mg.<project>.<environment>.<v>` (`docs/specs/release.md`, "Volumes"). Services of one import that name the same volume share it, as in Compose.

## Environment values

Every Moonglass environment variable is an environment secret, delivered to every service of the environment (`docs/specs/secrets.md`). So:

- Each `environment` entry is encrypted in the CLI to the environment's public key and set with `secrets.set`, kind `env`, exactly as `moonglass secrets set` does, including creating the environment key and checking the pins.
- Two services that give one name different values stop the import: `environment variable <NAME> has different values in services <a> and <b>; Moonglass environment variables belong to the environment`. Values are compared in the CLI only and never printed.
- Every apply sets every entry again. The CLI cannot read a value back, so it cannot tell whether one changed. A changed value reaches a service at that service's next deploy; `--update SERVICE` redeploys it now.
- Environment values are not part of the hash, and neither are names: every service gets every secret, so neither changes one service's container.

## Change detection

The hash of a service is SHA-256, as lowercase hex, of this canonical JSON, with keys in this order, no whitespace, lists in the order given:

```json
{"v":1,"image":"…","port":0,"domains":[…sorted],"command":[…],"volumes":[{"name":"…","target":"…","read_only":false}…sorted by target],"labels":{…keys sorted},"restart":"…","healthcheck":null}
```

`healthcheck` is `null` or `{"test":[…],"interval_ms":…,"timeout_ms":…,"start_period_ms":…,"start_interval_ms":…,"retries":…}`. These are exactly the settings the import sends in `deploy`. `depends_on`, environment names and values, and every refused field never enter it. The image is hashed as written, so a tag that moved to a new image is `unchanged`; use a digest to pin one.

The hash is sent as `deploy`'s `compose_hash` and stored on the release (W6-4b). `services.list` returns the current release's hash. So a failed deploy, a rollback or a restore leaves the hash of whatever release is current, and the next import compares against what actually runs.

## The list

Before anything is changed, the CLI reads `services.list` and `secrets.list` and prints one line per service, in apply order, then the removals and the secrets:

```
create     web     image ghcr.io/acme/web@sha256:…  port 3000 (not published)  domain app.example.com
update     api     image ghcr.io/acme/api:2.1
unchanged  db
remove     worker  stopped and hidden; restorable for 7 days
secrets    set API_KEY, DATABASE_URL (replaces), LOG_LEVEL (replaces)
```

- `create`: in the file, no service of that name. `update`: compose-managed, and the hash differs, or named by `--update`. `unchanged`: compose-managed with the same hash. `remove`: compose-managed and not in the file.
- **A changed service with a named volume is refused**, and nothing is applied: `service <name> mounts a named volume and changed; Moonglass never runs two releases on one volume (docs/specs/release.md): revert the change`. The agent would refuse that deploy anyway ("One release per volume"); the import says so before anything moves. A new service with a volume is created normally. Naming a volume service in `--update` is refused the same way.
- A `depends_on` cycle is refused by `ResolveCompose`. Nothing is applied.
- With nothing to create, update, remove or set, it prints `nothing to apply` and exits 0.

Then it asks `Apply these changes? [y/N] ` on stderr and reads one line from stdin. Only `y` or `yes` applies. Anything else, or end of input, prints `nothing applied` and exits 1. `--yes` skips the question. The question is for the operator's benefit, not a security control: authorisation is each operation's, and a removal from an agent identity waits for a person's approval like any `services.delete` (W5-2).

The dashboard's Compose screen (LN-14) shows the same list before anything changes; it is the "tell people before" surface of `docs/specs/ui.md` for Compose.

## Apply

In this order, stopping at the first failure:

1. **Secrets**, as above.
2. **Creates and updates**, in `depends_on` order: a service after everything it depends on, and services with no order between them by name. A create is `services.create`, then `deploy`; an update is `deploy`. Each `deploy` carries the service's settings, its domains and its hash, and returns when the release is final, so a service is healthy before the services that depend on it are deployed. A changed service redeploys through the normal zero-downtime path (`docs/specs/release.md`), never by stop-and-start. Unchanged services are never touched: no call names them.
3. **Removals**, by name, each a `services.delete` (`docs/specs/delete.md`): `STOP_APP` stops it and hides it, and it can be restored for 7 days. Nothing is purged.

A failure prints what was applied, what failed with its detail, and what was not reached, and exits 1. Running the import again continues: what succeeded is now `unchanged`.

## Tests

- W6-4a: the `ResolveCompose` tests in `docs/specs/container-policy.md`.
- W6-4b `internal/store`, `internal/registry` and `internal/server`: `TestComposeHashConformanceSQLite`, `TestDeployRecordsComposeHash`, `TestDeployRefusesMalformedComposeHash`, `TestServicesListShowsCurrentComposeHash`, `TestRestoreKeepsComposeHash`.
- W6-4c `internal/compose`: `TestServiceHashIsCanonical` (field order, sorting, a golden hash), `TestServiceHashIgnoresEnvironmentAndDependsOn`, `TestPlanMarksCreateUpdateUnchangedRemove`, `TestPlanRefusesServiceNotFromCompose`, `TestPlanRefusesChangedVolumeService`, `TestPlanRefusesConflictingEnvironmentValues`, `TestApplyOrderFollowsDependsOn`, `TestPlanListText`.
- W6-4 `internal/cli`, against an `httptest` control plane: `TestComposeApplyShowsListBeforeAnyCall`, `TestComposeApplyNeedsConfirmation`, `TestComposeApplyNeverTouchesUnchanged`, `TestComposeApplyOrderAndStopsAtFirstFailure`, `TestComposeApplyRemovalIsSoftDelete`, `TestComposeApplySetsEncryptedSecrets`, `TestComposeApplyUpdateFlagRedeploys`, `TestComposeApplyReadsOnlyEnvFile`.
