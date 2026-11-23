# ADR-0003: Secrets, envelope keys and the recovery key

- **Status:** Accepted
- **Date:** 2026-11-16

## Context

The control plane must never hold a readable secret (V§1). A new or replacement server still has to receive secrets, and a dead server must not take its backups with it (S§3.9, S§3.11). Every key operation must be doable in the dashboard as well as the CLI (V§1, dashboard first). All cryptography is reused: age (X25519), with the reference Go library on servers and in the CLI (ADR-0015) and the same author's TypeScript implementation in the browser (ADR-0018).

## Decision

**Keys** (S§3.11)

| Key | Where it lives | Purpose |
|---|---|---|
| Agent key (X25519 + Ed25519) | Generated on each server at enrolment; never leaves it | Identity, and the recipient for wrapped environment keys |
| Environment key (an age X25519 pair per environment) | Public half: control plane, browser, CLI. Private half: only ever stored wrapped | Secret values and database backups are encrypted to it |
| Owner recovery key (one age identity) | Generated in the browser at first run or by `moonglass keys init` in the CLI. The owner saves it as a file and can print the same text; there is no second format. Up to two owners can hold one | Recovering, granting and rotating without a surviving server |

1. **The recovery key exists before the first secret is saved.** The dashboard's first run creates it in the browser, offers it as a file and checks the saved copy before going on; `moonglass keys init` does the same in the CLI. Once a quarter the dashboard asks for a recovery-key check: the browser or the CLI proves it can unwrap a test value without revealing anything.
2. **Environment key pairs are generated on the owner's side,** in the browser or the CLI, the first time a secret is set in an environment that has no key yet. The private half is wrapped to every recovery recipient on record and, when the owner picks servers at that moment, to those servers' agents after their fingerprints are shown; then the public half and the wrapped copies are uploaded. Wrapping to a server is a grant (decision 5) and needs step-up. The private half is held only in memory and is never sent anywhere unwrapped.
3. **Wrapping.** Each value is encrypted to the environment public key. The environment private key exists only as wrapped copies, one per authorised agent and one per recovery key. The control plane holds only ciphertext and wrapped copies.
4. **Backups and exports** are encrypted to the environment public key. Exports carry ciphertext plus wrapped keys and are readable with stock `age` once the owner unwraps the environment key. **Bucket credentials are an environment secret**, encrypted to the environment key like any other, so a restore onto a new server needs only the recovery key (S§3.9).
5. **Grant.** Every grant is a recipient change: owner-only, with step-up (ADR-0006).
   - *With the recovery key:* the owner's CLI, or the dashboard with the recovery key file read into the browser, unwraps the record, shows the new server's fingerprint for the owner to compare with the one its enrolment printed, wraps the record to that agent and uploads it (W4-8, AL-3). This works for any environment and needs no server online.
   - *Unprotected environment, without the recovery key:* after the owner confirms the new server's fingerprint in the dashboard or the CLI, the control plane asks an online agent of that environment to re-wrap the key to the new agent (`RewrapEnvKey`, W3-3).
   - *Protected environment:* agents refuse `RewrapEnvKey`, so only the recovery-key path grants.
   - *How an agent knows an environment is protected.* The plaintext of every wrapped copy is a key record, not a bare key: the environment ID, a `protected` flag and the environment's age identity (`docs/specs/secrets.md`). The agent decides from the record it unwrapped itself, never from a flag in the request. The control plane cannot change a record without the key, because the age payload is authenticated. Turning protection off is safe. Turning it on takes effect for existing copies only through `secrets rotate`, which writes new records; until then the old copies say unprotected, and the dashboard and the CLI say so.
6. **Rotate** (W3-3): from the dashboard's Settings → Keys or `moonglass secrets rotate --env <name>`. It creates a new environment key, re-encrypts every value and re-wraps, with one executor per case. It is a recipient change: owner-only, with step-up.

   | Case | Executor | Rule |
   |---|---|---|
   | Protected environment | The owner's browser or CLI, with the recovery key | Unwraps with the recovery key; re-wraps to recipients the owner confirms by fingerprint |
   | Unprotected environment | An online agent in it | Re-wraps only to recipients already stored for that environment, never to one named in the request |
   | Recovery key | The owner's browser or CLI, with the old recovery key | Unwraps every environment key, current and retained, and wraps each to the new recovery key |

   Rotation deletes the agent-wrapped copies of the old environment key and keeps its recovery-wrapped copy, so old backups stay readable through the recovery key. Rotation does not un-leak, so the dashboard lists the values to rotate at their source.
7. **Agent key rotation** is on demand: the agent makes a new key and re-wraps the environment keys it holds to itself. Scheduled rotation every 90 days is V1-11.
8. **One reveal path** (W4-9): the agent re-encrypts the value to a one-time key made by the browser or the CLI, and the control plane relays only ciphertext. Reveal is owner-only, requires step-up, and is refused for any token owned by an agent identity, whatever its scope. Every reveal is audited. The recovery key is never used for everyday reads.
9. **The CLI pins each environment's public key** on first use and warns if it changes. It also pins the org's recovery recipients: `keys init` records the recipient it registers, and a CLI with no pin records the set it first fetches and prints it. A different set later stops the command. Otherwise a taken-over control plane could name its own recipient as the recovery key and receive every new environment key. The dashboard shows each environment's key fingerprint and the recovery recipients, and cannot pin against the control plane that serves it (decision 14).
10. **Git access** uses a deploy key generated on the server. The GitHub App's private key (W4-10) is the only repository credential the control plane holds; its permissions are the fewest that push-to-deploy, deploy keys, commit statuses and preview comments need (`docs/specs/build.md`), and the threat model states what a takeover of the control plane gets from it.
11. **Step-up is not replaced by holding a key.** Grant, rotate, reveal and every other recipient change need the sign-in fresher than ten minutes of ADR-0005 decision 3, even when the owner's browser or CLI also holds the recovery key. Holding the recovery key is not proof that a human is present: on a machine where a coding agent works, that agent can read the key file or type into the terminal. These operations therefore depend on the step-up primitive (W4-1c).
12. **The first recovery recipient.** First run or `moonglass keys init` uploads the recovery key's public half. While the org has no recovery recipient and no environment key exists, registering one needs the owner role and no step-up, and it is audited. Adding a second recovery recipient or replacing one is a recipient change, with step-up. Known risk: someone holding an owner token could register their own recovery key before the real owner does.
13. **Kinds of secret.** Every environment variable is a secret: its name is visible and its value is write-only. There are no plain environment variables. Registry credentials are a separate kind, keyed by registry host. The agent uses them only to pull from that host and never passes them into a container's environment. Both kinds are encrypted to the environment key in the same way (`docs/specs/secrets.md`).
14. **Browser-side keys trust the JavaScript we serve.** In the dashboard, keys are generated, unwrapped and used in the browser's memory, the recovery key file is read locally and never uploaded, and the age module loads only on screens that handle keys, under the CSP of `docs/specs/ui.md` §11. A taken-over control plane could serve altered JavaScript; the CLI is the stronger path, and the dashboard says so wherever a key is handled (`docs/threat-model.md` §5).

**What browser-side encryption does and does not protect** is the table in `docs/threat-model.md` §5, taken from S§3.11. In one sentence: our database never holds a readable secret; if the running control plane were taken over, it could deploy its own image to an unprotected environment and receive that environment's secrets, which protected environments prevent.

## Consequences

- A stolen database or backup yields ciphertext and wrapped keys only (S§4).
- **If every server in an environment is gone and no recovery key exists, nobody can decrypt its secrets or backups, including us.** The dashboard says so at first run, on the pricing page and in the docs.
- Every key operation has a dashboard flow and a CLI command, from the same registry operations.
- A takeover of the running control plane can still serve altered JavaScript or a substitute environment key to a browser, and can enrol a server into an unprotected environment through `RewrapEnvKey`.
- `internal/keys` and the dashboard's key module are trusted core. Test vectors must be readable by the stock `age` tool (B W3-1), and the browser's output by the Go library.

## Alternatives rejected

- **Encrypting backups to the server's key:** a dead server would take its backups with it (S§3.9).
- **A control plane that can decrypt:** a stolen database plus one credential exposes everything (S§4).
- **Reading secrets with the recovery key day to day:** that key is only for granting, rotating and recovering (S§3.11).
- **Key operations only in the CLI:** the dashboard would not be complete, and most people would never set a secret (V§1).
- **Generating environment keys on the first agent:** a server would be needed before the first secret, chosen by the control plane.
- **An agent rotating a protected environment:** it would re-wrap to recipients the control plane names, which the `RewrapEnvKey` refusal exists to stop.
- **Reveal by agent identities:** a leaked token could pull plaintext with no human involved.
- **Bucket credentials only in a file on the server:** they would die with the server, and restore would need more than the recovery key.

## Links

- S§2, S§3.9, S§3.11, S§4, S§5.1, S§9; V§1, V§6
- B§3: W1-3, W3-1, W3-2, W4-8, W4-5, AL-2, AL-3, W3-3, W4-9, LN-11, W6-2, V1-11
- `docs/threat-model.md` §2, §5; ADR-0005 (step-up); ADR-0015; ADR-0018
- `docs/specs/secrets.md`
