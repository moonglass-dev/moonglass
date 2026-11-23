# Secrets

How secrets are kept, set, delivered and revealed, per ADR-0003 as amended on 2026-11-17 by the weeks 2–3 preflight (owner decisions D13, D14, D15). The cryptography library is ADR-0015. The control-plane tables are `docs/specs/storage.md`; the operations are `docs/specs/registry.md`.

## Which ticket builds what

| Ticket | Builds | Needs step-up (W4-1) |
|---|---|---|
| W3-1 | `internal/keys`: age recipients and identities, the key record, wrap and unwrap; `moonglass keys init` writing the recovery key file | no |
| W3-2a | The four tables and their store methods (`docs/specs/storage.md`) | no |
| W3-2b | `keys.recovery.add` and `keys.recovery.list` | no |
| W3-2c | `keys init` registering the first recovery recipient, and the pins file with its recovery pins | no |
| W3-2d | `environments.key.create`, `secrets.set`, `secrets.list` and the environment's `public_key` | no |
| W3-2 | The `secrets set` and `environments key create` local commands: environment-key pins, the recovery check, creating an environment key wrapped to the pinned recovery recipients only, and encrypting each value | no |
| W4-8 | `secrets grant` for any server of an environment, and delivering secrets at deploy | yes |
| W4-9 (v1) | `secrets reveal` | yes |
| W3-3 (v1) | `RewrapEnvKey` and `secrets rotate` | yes |
| W4-5 (v1) | Browser-side encryption | no (setting a value) |

W4-9, W3-3 and W4-5 are v1 (the plan audit, `docs/review/plan-audit.md`, 2026-11-22). At launch the CLI is the only way to set a value, the owner's CLI grants every server, and no value is read back.

**Step-up is never replaced by holding a key** (ADR-0003 decision 11). Holding the recovery key is not proof that a human is present: on a machine where an AI agent works, that agent can read the key file or type into the terminal. The week 5 agent-safety story depends on this rule. So every grant, rotation, reveal and recipient change waits for W4-1. Until W4-8 merges, no agent holds an environment key, and deployed containers get no secrets.

## The key record

The plaintext of every wrapped environment key, to an agent or to a recovery recipient, is this JSON object, encrypted with age to that one recipient (owner decision D14):

```json
{"v": 1, "environment_id": "<32 hex>", "protected": false, "identity": "AGE-SECRET-KEY-1…"}
```

- `v` is 1. Any other value, an unknown field, a missing field, a duplicated field or trailing data after the object is an error.
- An agent decides whether an environment is protected from the record it unwrapped itself, never from a field in a request. The control plane holds only ciphertext and cannot change a record, because age authenticates its payload.
- A record whose `environment_id` differs from the environment it was delivered for is refused.
- From W4-2 (v1), each new record the CLI creates copies the environment's current flag. Until then every record says `"protected": false`. An existing record changes only when `secrets rotate` (W3-3) writes a new one. Until that write, the copy an agent already holds is unchanged, and the UI and the CLI say so.

## Kinds of secret

Owner decision D15. There are no plain environment variables: every one is a secret.

| Kind | Name | Value | Used by the agent |
|---|---|---|---|
| `env` | `[A-Z_][A-Z0-9_]*`, at most 128 bytes, not starting with `MOONGLASS_` | Any bytes, at most 32 KiB | As the container's environment variable of that name, in the app and its pre-deploy step |
| `registry` | A registry host, such as `ghcr.io` or `registry.example.com:5000`: a lowercase DNS name with an optional port | JSON `{"username": "…", "password": "…"}`, at most 4 KiB | Only to authenticate a pull from that host. **Never passed into a container's environment** |

Names are visible everywhere. Values are write-only: no operation returns a value or its ciphertext, and only `secrets reveal` (W4-9, v1) recovers one. No value appears in a log line, an error, a `CommandResult`, an audit record or a stream (ADR-0004 decision 12).

## Recovery key (W3-1, W3-2c)

- `moonglass keys init --out FILE` generates one age X25519 identity and writes it to `FILE` with mode 0600, created exclusively. The file holds exactly one line: the identity, then `\n`. Stock `age -d -i FILE` reads it.
- The command prints exactly three lines to stdout: the recipient (`age1…`) first; then where the key was written, that its mode is 0600 and to keep it offline; then the warning from ADR-0003 that losing every server and this key loses the secrets. stderr is empty on success. The identity is never printed, logged or put in an error.
- It refuses to overwrite an existing file and creates nothing when the directory is missing.
- The agent's recipient and identity are the age Bech32 encoding of the public and private halves of `agent-agree.key`. The file on disk is unchanged.
- From W3-2c, `keys init` reads `MOONGLASS_URL` and `MOONGLASS_TOKEN` like the generated runner (`docs/specs/registry.md`, "Generation"). It checks both before it creates anything: either one empty, or a `MOONGLASS_URL` the runner would refuse, exits 2 with a message naming the variable, and no file is written. It also reads the pins file first, so a pins file it cannot parse exits 1 before the key file exists.
- Once the file is written and flushed to disk, it calls `keys.recovery.add` with the recipient. That succeeds only while the org has no recovery recipient and no environment key (ADR-0003 decision 12); the owner accepted the risk that someone holding an owner token could register a key first. On success it sets the recovery pin for that control plane to exactly that recipient, replacing any recovery pin it held, and prints the three lines. Its environment pins are not touched.
- **When registration fails** (owner decision D23), the file is kept, nothing is printed to stdout, and the command exits 1. stderr holds `moonglass: keys init: registering the recovery key failed: <detail>`, then the lines the generated runner would print for the call's `errors`, then `The key file <FILE> is kept. Retry with: moonglass keys recovery add --recipient <age1…>`. The recipient is a public key. The file is never written after registering, so the org can never hold a registered recovery key that nobody has.
- **When the pin cannot be written** after registration succeeded, the command exits 1 with nothing on stdout and `moonglass: keys init: the recovery key is registered, but <pins path> could not be written: <error>. The next secrets set pins the recovery set it lists.` on stderr. The key file is kept.
- `keys init` is a local command (`registry.LocalCommands`) that calls one operation. From W3-2c its `Touches` names the key file, `keys.recovery.add` and the pins file.

## The pins file (W3-2c, W3-2)

Owner decision of 2026-11-20, from ADR-0003 decision 9 and D22. The CLI keeps `$XDG_CONFIG_HOME/moonglass/pins.json`, or `~/.config/moonglass/pins.json` when `XDG_CONFIG_HOME` is empty. The owner edits it by hand to accept a changed key, so its layout is part of the CLI's interface:

```json
{
  "v": 1,
  "control_planes": {
    "https://cp.example.com": {
      "recovery": ["age1…"],
      "environments": {"<environment id, 32 hex>": "age1…"}
    }
  }
}
```

- **The key** of `control_planes` is `MOONGLASS_URL` normalised: the scheme and host lowercased, `:443` dropped for `https` and `:80` for `http`, and every trailing `/` removed from the path. A `MOONGLASS_URL` with user info, a query or a fragment exits 2 with a message naming the variable, before anything is read or sent.
- **`recovery`** is absent until the first recovery pin, then the pinned recipients, sorted, without repeats. An empty list is a pin of no recipients and is never written by the CLI.
- **`environments`** maps an environment id to the public key pinned for it.
- `v` is 1. Any other value, an unknown or duplicated field, a field of the wrong type, or trailing data is an error that names the file and says to fix or delete it. A missing file reads as `{"v": 1, "control_planes": {}}`.
- Writes replace the whole file: a temporary file in the same directory with mode 0600, synced, then renamed over it. The directory is created with mode 0700 when missing. The CLI reads the file, checks, and writes it within one command; two commands run at once may lose one pin, which the next run records again.
- **Printing.** Recording a pin prints one line to stderr: `moonglass: pinned recovery recipient <age1…> for <key>` for each recipient of a new recovery pin, and `moonglass: pinned environment key <age1…> for <project>/<environment>` for a new environment pin. A pin that matches prints nothing.

**Stop messages.** A mismatch exits 1, sends nothing more, writes nothing, and prints to stderr:

- Environment key: `moonglass: <subcommand>: the control plane at <key> gives environment <project>/<environment> (<id>) the public key <new>, but this CLI pinned <old>. If you did not replace that key, stop: the control plane may be taken over. To accept the new key, remove control_planes["<key>"].environments["<id>"] from <pins path>.`
- Recovery set: `moonglass: <subcommand>: the control plane at <key> lists the recovery recipients <new>, but this CLI pinned <old>. If you did not change them, stop: the control plane may be taken over. To accept the new set, remove control_planes["<key>"].recovery from <pins path>.` Each set is its recipients sorted and joined with `, `, or `none`.

## Setting a secret (W3-2)

`moonglass secrets set --project P --env E [--registry] NAME` reads the value from stdin, never from an argument, so it is not in shell history. The value is the bytes read up to end of file; no newline is trimmed, so `printf %s "$VALUE" |` sets the value without one. A value over its kind's limit exits 1 with `moonglass: secrets set: the value is over <limit>`, where `<limit>` is `32 KiB` or `4 KiB`, and sends nothing. With `--registry` the value must be a JSON object with exactly the string fields `username` and `password`, or the command exits 1 with `moonglass: secrets set: a registry value must be {"username": "…", "password": "…"}`; the value is never quoted.

1. The CLI fetches the environment with `environments.list`. If it has no key yet, it fetches the recovery recipients with `keys.recovery.list` and checks them against its pins (step 2). An empty list with no recovery pin stops the command with `moonglass: secrets set: this org has no recovery key: run moonglass keys init --out FILE first` (ADR-0003 decision 1). The CLI then generates an age X25519 key pair, wraps the key record to every recovery recipient on record, and calls `environments.key.create`. The private half never leaves the CLI process unwrapped.
2. **Pinning** (ADR-0003 decision 9), in the pins file above. Per control-plane URL it holds each environment id's public key and the set of recovery recipients.
   - **Environment keys.** The first use records the key, whether the CLI created it or found it in `environments.list`. A different key later stops the command with the stop message above; the owner removes the entry by hand to accept the new one.
   - **Recovery recipients** (owner decision D22). `keys init` records the recipient it registered. A CLI with no recovery pin for that URL records the set `keys.recovery.list` returns and prints each recipient. A different set later stops the command with the stop message above, including a pinned set the control plane now lists as empty; the owner removes the entry by hand to accept the new one. This stops a taken-over control plane from naming its own recipient and receiving the new environment key.
3. The CLI encrypts the value with age to the environment public key and calls `secrets.set` with `kind` `env`, or `registry` with `--registry`. The control plane stores the ciphertext as it came.
4. The control plane checks only that the ciphertext is an age file within the size limit for its kind. It cannot check the recipient, and does not try.
5. **Output.** On success stdout holds exactly the `secrets.set` response body, as the generated runner prints it, and the command exits 0. Pin lines, and `moonglass: created the key of environment <project>/<environment>` when it created one, go to stderr. A refused call prints as the generated runner does and exits 1.

`moonglass environments key create --project P --env E` runs step 1 and its pin checks alone, for an environment with no key yet: it fetches the environment, stops with `moonglass: environments key create: environment <project>/<environment> already has a key` when it has one, checks the recovery recipients against their pins, creates the key, wraps the record to each pinned recipient and records the environment pin. stdout holds exactly the `environments.key.create` response body. `secrets set` does the same work inline. The `wrapped` input is `docs/specs/registry.md` ("Secrets operations (W3-2b, W3-2d)").

`moonglass secrets list --project P --env E` prints kinds, names and times. It is generated.

## Delivery (W4-8)

- **Each server.** `moonglass secrets grant --env E --server S` for any server of the environment, the first or a later one: the owner's CLI unwraps the record with the recovery key, shows the server's fingerprint for the owner to compare with the one `agent enroll` printed, wraps the record to the agent's recipient, and uploads it. It is a recipient change, with step-up.
- **At deploy,** `Deploy` gains `env_key` (this agent's wrapped record) and `secrets` (repeated `kind`, `name`, `ciphertext`). Together they must fit the 1 MiB envelope. The agent unwraps the record in memory, decrypts each value, puts `env` values into `policy.Spec.Env` for the app and its pre-deploy step, and keeps `registry` values for the pull only.
- Plaintext, unwrapped keys and records are never written to disk, to agent state, or to a log. A value that does not decrypt fails the step with `secret <name> could not be decrypted`.

## Secrets set by other commands (W6-1, W6-4)

Owner decision of 2026-11-19 (week 6). `moonglass databases create` and `moonglass compose apply` set `env` secrets the same way `secrets set` does: in the CLI, encrypted to the environment's public key after the pin checks, with `secrets.set`. Neither sends a plaintext value to the control plane.

- `databases create` makes the password in the CLI (32 characters from `[A-Za-z0-9]`, `crypto/rand`, rejection sampling) and sets two secrets per database: the password under the name its image reads (`POSTGRES_PASSWORD` or `REDISCLI_AUTH`) and the connection URL (`DATABASE_URL` or `REDIS_URL`). It prints the URL once (`docs/specs/managed-services.md`).
- `compose apply` sets each Compose `environment` entry as a secret of that name (`docs/specs/compose.md`, "Environment values").
- Every `env` secret reaches every service of the environment at deploy (W4-8). A secret is never scoped to one service.

The delivery fields are `Deploy.env_key` = 11 and `Deploy.secrets` = 12 (`docs/specs/protocol.md`).

## Reveal (W4-9, v1)

Unchanged from ADR-0003 decision 8: the CLI sends a one-time X25519 recipient; an online agent in the environment re-encrypts the value to it; the control plane relays only ciphertext. Owner-only, step-up, refused for any token owned by an agent identity, audited.

## `RewrapEnvKey` and rotate (W3-3, v1)

ADR-0003 decisions 5 and 6, with step-up. The CLI grant above stays the way to add a server. An agent refuses `RewrapEnvKey` when its own record says `protected: true`. An unprotected rotation by an agent wraps only to recipients already stored for that environment.
