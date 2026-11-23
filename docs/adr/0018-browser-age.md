# ADR-0018: age in the browser

- Status: accepted
- Date: 2026-11-23

## Context

The dashboard sets, reveals and rotates secrets, creates the recovery key and environment keys, and grants servers, all in the browser (ADR-0003 decisions 2, 5, 6, 8 and 14). That needs age's X25519 recipients and identities in TypeScript, byte-compatible with `filippo.io/age` (ADR-0015). A new module requires an ADR, and the dashboard's dependencies follow ADR-0016: exact pins, `bun.lock`, and a pin at least seven days old.

## Decision

1. Pin the npm package `age-encryption` at `0.3.1` exactly in `ui/package.json`, with `bun.lock` committed. It is the TypeScript implementation by age's author (`github.com/FiloSottile/typage`), BSD-3-Clause, published 2026-08-28. Its dependencies (`@noble/ciphers`, `@noble/curves`, `@noble/hashes`, `@noble/post-quantum`, `@scure/base`) are pinned by `bun.lock`.
2. **One importer.** Only the dashboard's key module, `ui/src/lib/keys/`, imports it. A lint rule in the UI build refuses an import anywhere else.
3. **Loaded only where keys are handled.** The key module is a separate chunk, loaded by the screens that set, reveal or rotate a secret, create or check a key, or grant a server (`docs/specs/ui.md` §10). It does not count toward the initial JavaScript budget.
4. **X25519 only,** as on the Go side (ADR-0015 decision 2). Post-quantum and passphrase recipients are not used.
5. **Test vectors cross both ways.** CI decrypts in Go what the browser module encrypts, and in the browser module what `filippo.io/age` encrypts, with the vectors of W3-1.

## Consequences

- The browser and the CLI produce the same ciphertext format, so the agent and stock `age` read either.
- The key module is trusted core: it is `risk: high`, and Opus reviews every change to it (B§1).
- The embedded build grows by the module's size, inside the 1 MB budget of `docs/specs/ui.md` §10.

## Alternatives rejected

- **Key operations only in the CLI:** the dashboard would not be complete (V§1).
- **Compiling the Go age library to WebAssembly:** several times larger, and a second build toolchain.
- **Web Crypto alone:** it has X25519 in current browsers but no age format, so we would write our own age implementation in trusted core.

## Links

- ADR-0003, ADR-0015, ADR-0016
- `docs/specs/secrets.md`, `docs/specs/ui.md` §10 and §11
- B§3: W4-5, AL-2, AL-3, LN-11
