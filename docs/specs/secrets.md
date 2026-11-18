# Secrets

How secrets are kept, set, delivered and revealed, per ADR-0003 as amended on 2026-11-17 by the weeks 2–3 preflight (owner decisions D13, D14, D15). The cryptography library is ADR-0015. The control-plane tables are `docs/specs/storage.md`; the operations are `docs/specs/registry.md`.

## Which ticket builds what

| Ticket | Builds | Needs step-up (W4-1) |
|---|---|---|
| W3-1 | `internal/keys`: age recipients and identities, the key record, wrap and unwrap; `moonglass keys init` writing the recovery key file | no |
| W3-2 | `keys init` registering the first recovery recipient, creating an environment key wrapped to the recovery key only, `secrets set` and `secrets list` | no |
| W4-8 | Granting the first server (the first agent-wrapped copy) and delivering secrets at deploy | yes |
| W4-9 | `secrets reveal` | yes |
| W3-3 | `RewrapEnvKey`, `secrets grant` and `secrets rotate` | yes |
| W4-5 | Browser-side encryption | no (setting a value) |

**Step-up is never replaced by holding a key** (ADR-0003 decision 11). Holding the recovery key is not proof that a human is present: on a machine where an AI agent works, that agent can read the key file or type into the terminal. The week 5 agent-safety story depends on this rule. So every grant, rotation, reveal and recipient change waits for W4-1. Until W4-8 merges, no agent holds an environment key, and deployed containers get no secrets.

## The key record

The plaintext of every wrapped environment key, to an agent or to a recovery recipient, is this JSON object, encrypted with age to that one recipient (owner decision D14):

```json
{"v": 1, "environment_id": "<32 hex>", "protected": false, "identity": "AGE-SECRET-KEY-1…"}
```

- `v` is 1. Any other value, an unknown field, a missing field, a duplicated field or trailing data after the object is an error.
- An agent decides whether an environment is protected from the record it unwrapped itself, never from a field in a request. The control plane holds only ciphertext and cannot change a record, because age authenticates its payload.
- A record whose `environment_id` differs from the environment it was delivered for is refused.
- From W4-2, each new record the CLI creates copies the environment's current flag. An existing record changes only when `secrets rotate` (W3-3) writes a new one. Until that write, the copy an agent already holds is unchanged, and the UI and the CLI say so.

## Kinds of secret

Owner decision D15. There are no plain environment variables: every one is a secret.

| Kind | Name | Value | Used by the agent |
|---|---|---|---|
| `env` | `[A-Z_][A-Z0-9_]*`, at most 128 bytes, not starting with `MOONGLASS_` | Any bytes, at most 32 KiB | As the container's environment variable of that name, in the app and its pre-deploy step |
| `registry` | A registry host, such as `ghcr.io` or `registry.example.com:5000`: a lowercase DNS name with an optional port | JSON `{"username": "…", "password": "…"}`, at most 4 KiB | Only to authenticate a pull from that host. **Never passed into a container's environment** |

Names are visible everywhere. Values are write-only: no operation returns a value or its ciphertext, and only `secrets reveal` (W4-9) recovers one. No value appears in a log line, an error, a `CommandResult`, an audit record or a stream (ADR-0004 decision 12).

## Recovery key (W3-1, W3-2)

- `moonglass keys init --out FILE` generates one age X25519 identity and writes it to `FILE` with mode 0600, created exclusively. The file holds exactly one line: the identity, then `\n`. Stock `age -d -i FILE` reads it.
- The command prints exactly three lines to stdout: the recipient (`age1…`) first; then where the key was written, that its mode is 0600 and to keep it offline; then the warning from ADR-0003 that losing every server and this key loses the secrets. stderr is empty on success. The identity is never printed, logged or put in an error.
- It refuses to overwrite an existing file and creates nothing when the directory is missing.
- The agent's recipient and identity are the age Bech32 encoding of the public and private halves of `agent-agree.key`. The file on disk is unchanged.
- From W3-2 it then calls `keys.recovery.add` with the recipient. That succeeds only while the org has no recovery recipient and no environment key (ADR-0003 decision 12); the owner accepted the risk that someone holding an owner token could register a key first.
- `keys init` is a local command (`registry.LocalCommands`) that calls one operation.

## Setting a secret (W3-2)

`moonglass secrets set --project P --env E [--registry] NAME` reads the value from stdin, never from an argument, so it is not in shell history.

1. The CLI fetches the environment with `environments.list`. If it has no key yet, the CLI generates an age X25519 key pair, wraps the key record to every recovery recipient on record, and calls `environments.key.create`. The private half never leaves the CLI process unwrapped.
2. **Pinning** (ADR-0003 decision 9). The CLI keeps `$XDG_CONFIG_HOME/moonglass/pins.json` (default `~/.config/moonglass/pins.json`, mode 0600), mapping control-plane URL and environment id to the environment public key. The first use records it. A different key later stops the command and prints both keys; the owner removes the entry by hand to accept the new one.
3. The CLI encrypts the value with age to the environment public key and calls `secrets.set`. The control plane stores the ciphertext as it came.
4. The control plane checks only that the ciphertext is an age file within the size limit for its kind. It cannot check the recipient, and does not try.

`moonglass secrets list --project P --env E` prints kinds, names and times.

## Delivery (W4-8)

- **First server.** `moonglass secrets grant --env E --server S` for the first server: the owner's CLI unwraps the record with the recovery key, shows the server's fingerprint for the owner to compare with the one `agent enroll` printed, wraps the record to the agent's recipient, and uploads it. It is a recipient change, with step-up.
- **At deploy,** `Deploy` gains `env_key` (this agent's wrapped record) and `secrets` (repeated `kind`, `name`, `ciphertext`). Together they must fit the 1 MiB envelope. The agent unwraps the record in memory, decrypts each value, puts `env` values into `policy.Spec.Env` for the app and its pre-deploy step, and keeps `registry` values for the pull only.
- Plaintext, unwrapped keys and records are never written to disk, to agent state, or to a log. A value that does not decrypt fails the step with `secret <name> could not be decrypted`.

## Reveal (W4-9)

Unchanged from ADR-0003 decision 8: the CLI sends a one-time X25519 recipient; an online agent in the environment re-encrypts the value to it; the control plane relays only ciphertext. Owner-only, step-up, refused for any token owned by an agent identity, audited.

## Grant and rotate (W3-3)

ADR-0003 decisions 5 and 6, with step-up. An agent refuses `RewrapEnvKey` when its own record says `protected: true`. An unprotected rotation by an agent wraps only to recipients already stored for that environment.
