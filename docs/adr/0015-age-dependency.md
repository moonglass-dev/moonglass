# ADR-0015: age dependency

- Status: accepted
- Date: 2026-11-17

## Context

W3-1 builds environment keys, the agent's age recipient, the owner recovery key, and wrap and unwrap (ADR-0003). Stack §2 picks "age (X25519) ... the reference Go library on servers and CLI", and rule 4 says cryptography is reused, never written. No ADR pins the module, and depguard does not allow it. Checked upstream on 2026-11-17: the newest release of `filippo.io/age` is v1.3.2 (2026-08-29).

## Decision

1. Pin `filippo.io/age v1.3.2` in `go.mod` and in the depguard allow list. W3-1 adds both.
2. Only `internal/keys` imports it. Every other package, the CLI included, calls `internal/keys`. depguard denies the module everywhere else.
3. Only X25519 recipients and identities are used (ADR-0003). Scrypt passphrase recipients, SSH recipients, plugins and post-quantum hybrid recipients are not used.
4. W3-1's test vectors must decrypt with the stock `age` command-line tool. CI installs it from the Ubuntu archive, and the test that uses it fails in CI when it is missing and skips locally when it is not installed.

## Consequences

- Upgrading age is a change to this ADR and to `go.mod`, in one ticket, with the stock-tool vectors re-run.
- The agent's existing raw X25519 key (`agent-agree.key`, `docs/specs/enrolment.md`) is encoded as an age recipient and identity by `internal/keys`. The file format on disk does not change.

## Alternatives rejected

- **Our own X25519 + ChaCha20-Poly1305 envelope.** Rule 4: cryptography is reused.
- **`age` plugins or SSH recipients.** More formats in trusted core, and nothing in ADR-0003 needs them.

## Open questions

None.

## Links

- `docs/plan/stack.md` §2, §3.11; ADR-0003
- `docs/specs/secrets.md`
- B§6: W3-1
