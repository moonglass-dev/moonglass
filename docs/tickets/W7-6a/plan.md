---
verdict: ready
reason: ""
---

# Plan for W7-6a

## Files to change

All new, all in `internal/update/`. Standard library only; `go.mod`, `go.sum`, `.golangci.yml` unchanged (`$gostd` is already allowed by depguard).

- `internal/update/verify.go` (the only production file, about 230 lines; tests do not count toward `max_diff_lines`):
  - Package doc: verifies OpenSSH `SSHSIG` release signatures from pinned `sk-ssh-ed25519@openssh.com` keys (ADR-0007 decision 1); embeds no key.
  - Unexported constants: `principal = "moonglass-release"`, `namespace = "moonglass-release"`, `namespacesOption = "namespaces=\"moonglass-release\""`, `keyType = "sk-ssh-ed25519@openssh.com"`, `hashAlg = "sha512"`, `magic = "SSHSIG"`, `version uint32 = 1`, `requiredFlags byte = 0x05`, `armorBegin = "-----BEGIN SSH SIGNATURE-----"`, `armorEnd = "-----END SSH SIGNATURE-----"`.
  - `var ErrBadSignature = errors.New("release signature rejected")`. Every rejection is `fmt.Errorf("update: <check> …: %w", …, ErrBadSignature)`, e.g. `update: signature namespace is %q, want %q: %w`. Errors never include message bytes; they may quote the (public) field that failed with `%q`.
  - `type Signer struct { blob []byte; key ed25519.PublicKey; application []byte }` (unexported fields; only `ParseSigner` makes one).
  - `func ParseSigner(line string) (Signer, error)`: `strings.Split(line, " ")` must give exactly 4 fields (so double spaces, tabs, a comment, extra options, `OWNER-PASTE …` and empty input fail on the count); field 0 `== principal`, field 1 `== namespacesOption`, field 2 `== keyType`, field 3 decoded with `base64.StdEncoding.Strict().DecodeString`; then `parseKeyBlob(blob)` (whose type check makes a blob of another type fail). Each failure names its field.
  - `type wire []byte` with methods that check length against bytes remaining before slicing, and never allocate:
    - `func (w *wire) take(n int, field string) ([]byte, error)` for fixed-size reads (magic, `uint32`, flags byte).
    - `func (w *wire) u32(field string) (uint32, error)`.
    - `func (w *wire) str(field string) ([]byte, error)`: reads the `uint32` length `n`, rejects `int64(n) > int64(len(rest))`, then slices with `n` directly (`rest[:n]`, `rest[n:]`), with no `4+n` arithmetic that could wrap at `0xFFFFFFFF` and no narrowing conversion for gosec G115.
    - `func (w wire) done(field string) error`: rejects leftover bytes ("trailing bytes after <field>").
  - `type keyBlob struct { raw, key, application []byte }`; `func parseKeyBlob(b []byte) (keyBlob, error)`: `str` type `== keyType`, `str` key with length exactly 32, `str` application, `done`.
  - `type sshsig struct { signer Signer; ed25519Sig []byte; flags byte; counter uint32 }`.
  - `func unarmor(armored []byte) ([]byte, error)`: strip one optional trailing `\n`; `bytes.Split` on `\n`; need at least 3 lines; first `== armorBegin`, last `== armorEnd`; every middle line non-empty and only `A–Z a–z 0–9 + / =` (so `\r`, spaces, `\t`, empty lines, text before BEGIN or after END fail); join the middle lines and decode with `base64.StdEncoding.Strict()`.
  - `func parseSignature(armored []byte, pinned []Signer) (sshsig, error)`: every check before the message hash, in the ticket's order, first failure returned:
    1. `unarmor`;
    2. outer blob: `take(6)` `== magic`; `u32` `== 1`; `str` public key blob; `str` namespace `== namespace`; `str` reserved empty; `str` hash `== hashAlg`; `str` signature; `done`;
    3. `parseKeyBlob` on the public key blob, then the pin match: the first `s` in `pinned` with `bytes.Equal(raw, s.blob)`; none (including nil or empty `pinned`) rejects with "signing key is not pinned";
    4. inner signature: `str` type `== keyType`; `str` Ed25519 signature of exactly 64 bytes; `take(1)` flags; `u32` counter; `done`;
    5. `flags != requiredFlags` rejects with `update: signature flags are 0x%02x, want 0x05 (user presence and user verification): %w`.
  - `func Verify(message io.Reader, armored []byte, pinned []Signer) error`:
    1. `parseSignature(armored, pinned)`;
    2. stream the message: `h := sha512.New(); io.Copy(h, message)`. A read error is returned as `fmt.Errorf("update: reading the signed file: %w", err)`, which is an I/O failure, not a rejection. It does not wrap `ErrBadSignature`, so a caller cannot mistake a disk error for a forged file, and it is never nil;
    3. `data = magic || string(namespace) || string("") || string(hashAlg) || string(h.Sum(nil))`, built by `appendString(b, s []byte) []byte` (its `uint32(len(s))` carries `//nolint:gosec // G115: s is at most 64 bytes`, the same pattern as `internal/protocol/envelope.go`);
    4. `app := sha256.Sum256(sig.signer.application)`, `d := sha256.Sum256(data)`, `msg = app || flags || be32(counter) || d`; `ed25519.Verify(sig.signer.key, msg, sig.ed25519Sig)` returning false rejects with "Ed25519 signature does not verify".
  - The message is hashed last, so a malformed or unpinned signature never reads the file.

- `internal/update/verify_test.go`: all named tests and the helpers below.
- `testdata/tests.txt`: regenerated with `make test-list`.

## Tests to write

Helpers in `verify_test.go`, standard library only, keys from `ed25519.GenerateKey(rand.Reader)` in each test; nothing on disk:

- `type testKey struct { pub ed25519.PublicKey; priv ed25519.PrivateKey; app string }`, `newTestKey(t, app string) testKey` (app `ssh:`).
- `func keyBlob(keyType string, pub []byte, app string, extra []byte) []byte`, `func (k testKey) line() string` (the pinned `allowed_signers` form), `func (k testKey) signer(t) Signer` (via `ParseSigner`).
- `type sigFields struct { preamble string; version uint32; keyBlob []byte; namespace, reserved, hash string; innerType string; flags byte; counter uint32; innerExtra []byte }`, `validFields(k) sigFields`.
- `func (f sigFields) sign(k testKey, app string, msg []byte) []byte`: the Ed25519 signature over `SHA-256(app) || flags || counter || SHA-256(data)`, using the case's own namespace, reserved and hash fields, so each case is validly signed over its own fields.
- `func (f sigFields) blob(sig []byte) []byte`: writes the outer blob field by field.
- `func armor(blob []byte, width int, trailingNewline bool) []byte` (width 70 as `ssh-keygen` writes it, or 0 for one line).
- `func wantBad(t, err error, substr string)`: fails unless `errors.Is(err, ErrBadSignature)` and the message contains `substr`, printing want and got.
- `func malformedSignatures(t testing.TB, k testKey) [][]byte`: the malformed inputs, shared by the encoding test and the fuzz seeds.

Named tests:

- `./internal/update TestVerifyAcceptsEitherPinnedKey`: two keys, each signing its own message. Each verifies against `[a, b]` and `[b, a]`, armored at width 70 and on one line, with and without the trailing newline (a table of 2×2×2×2 cases). All return nil.
- `./internal/update TestVerifyRejectsUnpinnedKey`: table: key c not in `[a, b]`; `pinned` empty; `pinned` nil; a key with a's Ed25519 bytes but app `ssh:other`, signed by a's private key over `ssh:other`. Each `wantBad(…, "not pinned")`.
- `./internal/update TestVerifyRejectsWrongEnvelope`: table of `sigFields` mutations, each signed with `sign` after the mutation: preamble `SSHSIH` ("preamble"); version 0 and 2 ("version"); namespace `file`, `""`, `moonglass-release2` ("namespace"); reserved `x` ("reserved"); hash `sha256`, `SHA512` ("hash"); outer key blob type `ssh-ed25519` ("key type"); inner type `ssh-ed25519` ("signature type"). Pinned is the key's real signer.
- `./internal/update TestVerifyRequiresExactFlags`: table over flags: `0x05` returns nil; `0x00, 0x01, 0x04, 0x07, 0x25, 0x45, 0x85, 0xFF` each `wantBad(…, "flags")`. Each case is signed with its own flags.
- `./internal/update TestVerifyRejectsTamperedSignature`: from one valid signature:
  - the message with one byte changed, one byte appended, or empty;
  - flags changed after signing (signed with 0x07, encoded with 0x05, so the flags check passes and only the Ed25519 check can reject);
  - counter changed after signing (signed with 7, encoded with 8);
  - one bit of the Ed25519 signature flipped;
  - a signature made over `SHA-256("ssh:other")` with the real key blob.

  Each `wantBad(…, "does not verify")`.
- `./internal/update TestVerifyRejectsMalformedEncoding`: none panics, and each `wantBad(…, "")`:
  - empty input; BEGIN missing or misspelled; END missing or misspelled;
  - `"x\n"` before BEGIN; `"x"` after END; two trailing newlines; CRLF line ends;
  - invalid base64: `!`, bad padding, a space inside a line, an empty line between lines;
  - the valid blob truncated at every offset `0…len-1`; one byte appended;
  - the public-key length prefix set to `len(rest)+1` and to `0xFFFFFFFF`;
  - a 31- and a 33-byte public key; a 63- and a 65-byte Ed25519 signature;
  - one trailing byte in the key blob; one trailing byte in the inner signature.
- `./internal/update TestParseSignerAcceptsOnlyPinnedForm`: table. The valid line parses, and its signer verifies a signature from that key. Each of these is rejected with `ErrBadSignature`:
  - principal `release`;
  - the option missing (3 fields), or `namespaces="file"`;
  - an extra option `cert-authority` (5 fields), or a trailing comment;
  - key type `ssh-ed25519` or `sk-ecdsa-sha2-nistp256@openssh.com`;
  - line type `sk-ssh-ed25519@openssh.com` with a blob of type `ssh-ed25519`;
  - invalid base64; a blob with one trailing byte; a 31-byte key;
  - `OWNER-PASTE primary release public key here`; `""`; a double space between fields.
- `./internal/update FuzzParseSignature`: one `GenerateKey` signer is made before `f.Fuzz`. Seeds are the valid armored signature plus every input from `malformedSignatures`. The body checks two things:
  - `parseSignature(data, []Signer{signer})` never panics, and any error it returns wraps `ErrBadSignature`;
  - `Verify(bytes.NewReader(msg), data, []Signer{signer})` returns nil or an error wrapping `ErrBadSignature`.

## Edge cases

- Length prefix beyond the input, including `0xFFFFFFFF`: `str` compares as `int64` before slicing and slices by `uint32` without arithmetic. Covered by the `len+1` and `0xFFFFFFFF` cases and the truncation sweep in `TestVerifyRejectsMalformedEncoding`, and by `FuzzParseSignature`.
- Truncation at any offset, and trailing bytes at each level (outer blob, key blob, inner signature): `done` runs after each. Covered by the truncation sweep and the trailing-byte cases.
- Wrong key or signature length (31/33, 63/65): exact length checks run before use, so `ed25519.Verify` never sees a bad length. `TestVerifyRejectsMalformedEncoding`.
- Same Ed25519 key with a different application: the comparison is byte for byte over the whole key blob. `TestVerifyRejectsUnpinnedKey`.
- Flags with extra bits (0x07, 0x25, 0x45, 0x85, 0xFF) or missing bits: exact equality. `TestVerifyRequiresExactFlags`.
- Flags or counter changed after signing: both are inside the signed message. `TestVerifyRejectsTamperedSignature`.
- Armor variations (CR, whitespace, text outside the markers, a second trailing newline, an empty body): `unarmor` rejects them; `TestVerifyRejectsMalformedEncoding`. Width 70 and one line are both accepted; `TestVerifyAcceptsEitherPinnedKey`.
- Non-canonical base64 (non-zero padding bits): `Strict()` rejects it.
- `pinned` nil or empty: rejected, never accepted by default. `TestVerifyRejectsUnpinnedKey`.
- Huge message: hashed by streaming with `io.Copy`, never buffered, and read last.
- Message reader error: returned with context, never nil.
- Hostile `allowed_signers` line (comment, extra option, tab, double space, `OWNER-PASTE`): only exactly 4 single-space fields parse. `TestParseSignerAcceptsOnlyPinnedForm`.

## Risks

- Stricter than `ssh-keygen -Y verify`, never looser: the signed data and the U2F wrapping follow `PROTOCOL.sshsig` and `PROTOCOL.u2f` exactly. That means the raw 6-byte `SSHSIG` magic in the signed data, the `SHA-512` of the message as a `string`, and `SHA-256(application) || flags || counter || SHA-256(data)`. Every extra rule (exact flags, empty reserved, no comment, a 4-field line) only rejects more. No `ssh-keygen` interop test is added, because it would need a FIDO token. The test helper builds blobs exactly as the spec states, and W7-6e can add a vector from the owner's real key.
- gosec G115 on integer conversions: reading uses `int64` widening and `uint32` slice indices, with no narrowing. The one `uint32(len(s))` in `appendString` has a `//nolint:gosec` that states the bound.
- Error strings could leak the message: only public field values from the signature are quoted, and tests assert on field names only.
- Banned words: none of TODO, FIXME, XXX, HACK, fallback or legacy appear in the code or comments.
- `testdata/tests.txt` drift would fail `check-test-list`: run `make test-list` after the tests exist.
- Race and shuffle under `make test`: tests share no state, and each makes its own keys.
- No key is committed: keys come from `crypto/rand` at test time, and no fuzz corpus under `testdata/fuzz` is committed (only the seeds added in code).
- Size: production code stays under the 330-line cap; test files are not counted.
