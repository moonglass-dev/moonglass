# One-click Postgres and Redis

This is the behaviour spec for managed Postgres and Redis (ticket W6-1). They are ordinary services created through the normal deploy path (`docs/specs/release.md`), not a separate runtime. The owner approved the first version of this spec on 2026-11-18 and the decisions below on 2026-11-19 (week 6).

## Which ticket builds what

| Ticket | Builds |
|---|---|
| W6-1a | The service settings on `Deploy`: `command`, `volumes`, `labels`, `restart`, `healthcheck` (`docs/specs/protocol.md`) |
| W6-1b | `Healthcheck`, `NetworkAliases` and `DNSSearch` in `policy.Spec` (`docs/specs/container-policy.md`) |
| W6-1c | The agent applying them: named volumes, the service's internal name, the Docker health mode, one release per volume, `REMOVE_APP` removing volumes (`docs/specs/release.md`, `docs/specs/delete.md`) |
| W6-1d | The settings stored on releases, the `deploy` inputs that set them, `services.restore` copying them (`docs/specs/storage.md`, `docs/specs/registry.md`) |
| W6-1 | `moonglass databases create` |

Nothing here is specific to databases below the CLI. The agent knows no database kinds: it applies settings, as it does for a Compose service (`docs/specs/compose.md`).

## The command

```
moonglass databases create --project P --env E --name N --server S --kind postgres|redis
```

A local command on `registry.LocalCommands` (ADR-0006 decision 7), because it makes a password and encrypts on the client and calls several operations. It is not an operation and has no authorisation of its own: each call it makes is authorised and audited by the registry like any other, so its lowest role is `member`, the highest `MinRole` among them, and it is not destructive. Its flags follow the generated-CLI rules (`docs/specs/registry.md`, "Generated CLI"); all five are required, and a `--kind` other than `postgres` or `redis` exits 2 before anything is sent.

It runs these steps in order and stops at the first that fails, with exit code 1 and the failing call's detail on stderr:

1. **Read.** `services.list` and `secrets.list` for the environment.
   - **Resume** when a service named `N` exists, both of the kind's secrets (below) exist, and `releases.list` shows the service has no current release: a previous run stopped after step 4. The command goes to step 5.
   - Otherwise, when either of the kind's secrets exists, it stops with `environment <E> already has <NAME>; Moonglass creates one <kind> database per environment`, and when a service named `N` exists, with `service <N> already exists`. Nothing has been changed.
2. **The service.** `services.create` with `project`, `environment`, `name` and `server`.
3. **The password.** 32 characters from `A`–`Z`, `a`–`z` and `0`–`9`, drawn from `crypto/rand` by rejection sampling, so every character is equally likely (about 190 bits). The alphabet needs no escaping in a URL or a shell. The password exists only in this process's memory, and is never logged or written to a file.
4. **The secrets.** Encrypted on the client to the environment's public key, exactly as `moonglass secrets set` does, including creating the environment key and checking the pins (`docs/specs/secrets.md`, "Setting a secret"), and stored with `secrets.set`, kind `env`:

   | Kind | Password secret | URL secret | URL |
   |---|---|---|---|
   | `postgres` | `POSTGRES_PASSWORD` | `DATABASE_URL` | `postgres://postgres:<password>@<N>.<E>.internal:5432/postgres` |
   | `redis` | `REDISCLI_AUTH` | `REDIS_URL` | `redis://:<password>@<N>.<E>.internal:6379/0` |

   Every service of the environment receives every `env` secret at deploy (W4-8), so each app finds `DATABASE_URL` or `REDIS_URL` with no further step. The host is the database's internal name (`docs/specs/release.md`, "Service names"), which resolves only inside the project's network.
5. **The deploy.** `deploy` with the kind's preset (below) and no `port`, `domain` or `pre_deploy`. It waits for the release's final status, as `deploy` does.
6. **The URL.** On a `succeeded` release, it prints one line to stdout, `DATABASE_URL=<url>` or `REDIS_URL=<url>`, and exits 0. This is the only time the URL is printed. Later, `moonglass secrets reveal` (W4-9) is the only way to read it.

When step 5 fails after step 4 ran, the command still prints the URL line, because the secrets now hold that password, then the release's detail on stderr, and exits 1. Running the same command again resumes at step 5 (step 1). A resumed run prints no URL: it says on stderr `the URL was printed when the database was created; moonglass secrets reveal shows it` and exits 0 on success.

## Presets

Sent as the `deploy` inputs of `docs/specs/registry.md` ("Service settings on `deploy`"). The images are tags, so each deploy pulls them and `releases.list` records the digest that ran (`docs/specs/release.md`, "Images").

| | Postgres | Redis |
|---|---|---|
| `image` | `postgres:18` | `redis:7` |
| `volume` | `<N>-data:/var/lib/postgresql` (postgres:18 keeps its data under that path) | `<N>-data:/data` |
| `command` | none: the image's own | `sh`, `-c`, `exec redis-server --requirepass "$REDISCLI_AUTH" --appendonly yes` |
| `healthcheck` | `CMD`, `pg_isready`, `-h`, `127.0.0.1`, `-U`, `postgres` | `CMD`, `redis-cli`, `-h`, `127.0.0.1`, `ping` |
| healthcheck timing | interval 2000 ms, timeout 2000 ms, retries 3, start period 30000 ms, start interval 1000 ms | the same |
| `restart` | empty: `unless-stopped` | empty: `unless-stopped` |

- Postgres reads `POSTGRES_PASSWORD` when it first initialises its data directory, and never again. `pg_isready -h 127.0.0.1` passes only once the server listens on TCP, after the image's first-start initialisation has finished.
- Redis has no password variable of its own. Its command is a fixed string in the CLI, with no input in it; the container's own shell expands `$REDISCLI_AUTH` from the container's environment, so the password is not in the container's configuration. `redis-cli` reads `REDISCLI_AUTH` itself, so the health check authenticates. No host shell runs anything (`docs/threat-model.md` §8).
- The healthcheck gives the Docker health mode of the switch (`docs/specs/release.md`, "Health").
- The volume is the managed volume `mg.<P>.<E>.<N>-data` (`docs/specs/release.md`, "Volumes"). A redeploy reuses it.

## Running, stopping and removing

- A database mounts a named volume, so the agent never runs two of its releases at once and never rolls it back (`docs/specs/release.md`, "One release per volume"). A `deploy` to a running database is refused by the agent, so the MVP has no way to change a database's image or settings after it is created. `services.restore` is the one redeploy it gets, with the same image digest and settings, while it is stopped.
- `services.delete` stops it and keeps its volume; `services.restore` brings it back with its volume and settings; `services.purge` removes its containers and its volume (`docs/specs/delete.md`).
- Changing the password is not supported: `POSTGRES_PASSWORD` is read only at first start, so a new secret value would not reach the data directory.

## Security

- The control plane never holds the password in plaintext: it is made and encrypted in the CLI and travels only as ciphertext (`docs/specs/secrets.md`). The URL on stdout is the one plaintext copy outside the server.
- Any service of the environment can read the database's secrets, by design: they are the environment's. Within the project's network another environment's container can reach the database by its full name, so the password, not the network, is what keeps environments apart (`docs/specs/release.md`, "Service names").
- A takeover of the control plane can deploy its own image into an unprotected environment, mount that environment's volumes and receive its secrets (`docs/threat-model.md` §5, limit 1). Volumes are named from the agent's binding, so no `Deploy` reaches another environment's or project's volume.

## Tests (W6-1)

`internal/cli`, against an `httptest` control plane: `TestDatabasesCreateCallsInOrder`, `TestDatabasesCreatePresets` (both kinds, every input exactly), `TestDatabasePasswordAlphabetAndLength`, `TestDatabasesCreateSecretsAreCiphertext` (each `secrets.set` value decrypts with the test environment key to the password and the URL), `TestDatabasesCreatePrintsURLOnce`, `TestDatabasesCreateRefusesSecondDatabase`, `TestDatabasesCreateResumesFailedDeploy`.

## Out of scope

No backups (W6-2). No replication or pooling. No automatic major upgrades. No other kinds.
