# Enrolment

W1-3 implements agent enrolment: a one-time token, agent identity keys generated on the agent machine, the fingerprint printed, the control-plane key pinned, and the real `moonglass server` and `moonglass agent enroll` subcommands. It is faithful to ADR-0003 (agent identity only), ADR-0005 (token format and one-use), the Enroll types in `docs/specs/protocol.md`, and threat-model boundary B3. Transport is HTTP for this ticket; outbound WSS, reconnect and heartbeat scheduling are W1-4.

## Tokens

Enrolment tokens are ADR-0005 bearer secrets: 256 bits from `crypto/rand`, written as `mg_` plus 52 characters of lowercase unpadded base32. They last one hour and are one use. The store keeps only `store.HashToken` of the full text (hex SHA-256). Shown once, at creation.

The token secret is not a field of `EnrollRequest`. The agent sends it as `Authorization: Bearer <token>` on the HTTP request. `token_id` in the protobuf is the hex SHA-256 of that same bearer (not the secret). The server hashes the bearer itself, looks it up with `UseEnrolmentToken`, and rejects the request if `token_id` does not match that hash. A second use of the same token returns `TOKEN_USED` and does not register a server. A missing, malformed, expired or unknown token returns `TOKEN_INVALID`.

`moonglass server` can issue a token for an org (the server package, and a CLI hook used by tests). Expiry is one hour from issue.

## Agent identity (`internal/keys`, agent only)

At enrol the agent generates, on the machine, one Ed25519 signing key and one X25519 key whose public half is the `agent_age_recipient` field (raw 32-byte public keys). Private keys never leave the machine and are not sent to the control plane. They are written under the agent `--data-dir` (default `/var/lib/moonglass/agent` when running as a service; tests use a temp dir). Standard library `crypto/ed25519` and `crypto/ecdh` only; this ticket does not add the `age` module (W3-1).

The fingerprint printed on stdout after a successful enrol is the lowercase hex SHA-256 of the raw Ed25519 public key (64 hex characters). It is also written under the data dir. The secrets flow (later) compares against this value.

`EnrollRequest.agent_signing_public_key` and `agent_age_recipient` must be exactly 32 bytes. Any other length is a malformed request: the server answers HTTP 400 with a plain-text error, and does not call `UseEnrolmentToken`, so the token is not consumed. An unparsable or oversize body or a wrong envelope type is also HTTP 400.

The agent stores its files under the data dir, all mode 0600 (directory 0700): `agent-signing.key` (raw 64-byte Ed25519 private key), `agent-agree.key` (raw 32-byte X25519 private key), `control-plane.pub` (the pin) and `fingerprint` (hex plus newline). Files are written atomically and only after an `ACCEPTED` response with a valid key. A data dir that already holds keys reuses them; a failed or replayed enrol leaves every file untouched. The agent maps `TOKEN_USED` and `TOKEN_INVALID` to `agent.ErrTokenUsed` and `agent.ErrTokenInvalid`.

## Control-plane key

The server has an Ed25519 identity, generated into its `--data-dir` on first start and reused after that. `EnrollResponse.control_plane_public_key` is that raw 32-byte public key. The agent writes it to its data dir as the pin. A later successful enrol to a different control-plane key replaces the pin (re-enrol to another control plane is the portability path). An `ACCEPTED` response with a missing or wrong-length control-plane key is an error and must not pin or print a fingerprint.

## HTTP endpoint

`moonglass server` listens on `--listen` (tests use `httptest` or a localhost port). `POST /v1/enroll` accepts one length-prefixed protocol envelope (`internal/protocol`) whose payload is an `EnrollRequest`, and replies with one `EnrollResponse` envelope. No WebSocket in this ticket. TLS is not required on loopback tests; production TLS and `wss` are W1-4.

On `ACCEPTED`, the server calls `store.AddServer` with the Ed25519 public key encoded as lowercase hex (the `PublicKey` string the store already uses). The store cannot use a token and add a server in one transaction (the schema is fixed), so the server calls `UseEnrolmentToken` first and then `AddServer`. A duplicate public key, or a store failure after the token is spent, answers `TOKEN_INVALID` and the token stays used; issue a fresh token to retry. This approximates ADR-0005's "marked used when accepted": the gap exists only for a duplicate key or store failure. A store failure answers HTTP 500; the server writes no logs from this package, so the token cannot leak through them.

The token travels in an `Authorization` header over plain HTTP in this ticket. That is for loopback tests and the walking skeleton only; B3 transport protection arrives with TLS in W1-4. The token is a positional argument, so it is visible in shell history and `ps` on the agent machine; it is never printed or logged, and transport errors have it redacted.

## Commands

- `moonglass server --data-dir DIR --listen ADDR` — opens SQLite at `DIR/server.db`, migrates, loads or creates the server identity, listens. If the store has no orgs, it creates one so a first token can be issued. A `--print-enroll-token` flag issues one token for that org, prints the bearer once to stdout, and continues serving. This is a walking-skeleton hook, not hosted bootstrap (W7-1 / ADR-0005).
- Owner decision D27: `moonglass server --print-owner-token NAME` mints one owner token (`docs/specs/registry.md`, "Tokens") named `NAME`, expiring 90 days after issue, for the org's owner, prints the token once to stdout, and continues serving. When the org has no owner yet it first mints a user through the external identity `local:owner` and gives it the owner role; an org that already has an owner gets no second owner. An empty `NAME` (`--print-owner-token ""` or `--print-owner-token=`) prints usage to stderr and exits 2, like a missing `--url`. With both `--print-enroll-token` and `--print-owner-token` the enrolment token line comes first. The token goes to stdout only, so the owner runs the hook from a shell and not under the systemd unit, whose stdout is the journal. It is the same walking-skeleton hook as `--print-enroll-token`, and it is how the owner gets a token for the CLI and for CI until sign-in (W4-1). Until scoped tokens (W5-1) every token is a full owner token.
- `moonglass agent enroll --data-dir DIR --url URL TOKEN` — generates keys if this data dir has none, POSTs the enroll, pins the control-plane key, prints the fingerprint to stdout. The token is a positional argument or the last argument. It must not be logged. A replay must fail and must not overwrite keys or the pin.

`moonglass version` stays as it is. Existing `cmd/moonglass` tests must keep passing. The usage text grows to list `server` and `agent enroll`; `TestUsageOnMissingOrUnknownCommand` compares against the `usage` constant and is unchanged. A missing token or `--url` prints usage and exits 2; an enrol failure prints the error and exits 1.

## Out of scope

Outbound WSS, reconnect, heartbeat scheduling (W1-4). Environment keys, recovery key, `age` wrap/unwrap (W3-1). Bootstrap token and first-admin registration window (ADR-0005, later). GitHub sign-in and login links (W4-1). Proxy (W1-5). Changing W1-2 store schema.
