---
verdict: ready
reason: ""
---

# Plan for W3-1

Context: ADR-0003 and ADR-0015 are accepted and settle every decision here. `filippo.io/age v1.3.2` is named in full in ADR-0015 and is already in the module cache. It brings in `filippo.io/hpke` and `filippo.io/nistec` as indirect requirements, which the `dependencies` gate does not count. Nothing changes a contract: no proto, no registry operation, no `agent.toml` and no storage. `keys init` is a local command. Checked in age v1.3.2: `ParseX25519Recipient` accepts only HRP `age`, so it refuses `age1pq…` hybrids, `age1<plugin>…` plugins and SSH keys. `ParseX25519Identity` accepts only `AGE-SECRET-KEY-` and never echoes its input. age has no public constructor from a raw scalar or point, so `internal/keys` needs a small Bech32 encoder. It only encodes and has no decoder, and age's own parser checks every string it produces. `encoding/json/v2` still needs GOEXPERIMENT in Go 1.27, so the strict parser walks tokens with `encoding/json` v1.

## Files to change

- `go.mod`, `go.sum`: run `go get filippo.io/age@v1.3.2 && go mod tidy`. That gives one new direct `require filippo.io/age v1.3.2`; hpke and nistec come in only as `// indirect`. `golang.org/x/crypto v0.58.0` already meets age's v0.55.0.
- `internal/keys/bech32.go` (new, about 35 lines): `func bech32Encode(hrp string, data []byte) string`. It converts 8-bit data to 5-bit groups (keeping the accumulator masked), then writes `hrp + "1" + data + checksum` in lowercase using the BIP-173 polymod and hrpExpand. It has no length limit, the same as age. Callers uppercase the result for identities, the way age does. The package comment says it is an encoding only and that age's parser checks it.
- `internal/keys/age.go` (new). It and `record.go` are the only files outside tests that import `filippo.io/age`. Between them they call only the X25519 APIs plus `age.Encrypt` and `age.Decrypt`.
  - `type Recipient struct{ r *age.X25519Recipient }`, with `func (r Recipient) String() string` returning `age1…`.
  - `type Identity struct{ id *age.X25519Identity }`, with `func (i Identity) Recipient() Recipient` and `func (i Identity) Secret() string` returning `AGE-SECRET-KEY-1…`. Identity has no `String` method on purpose, so `%v` never prints the secret.
  - `func GenerateEnvironmentKey() (Identity, error)` calls `age.GenerateX25519Identity`, which reads `crypto/rand`.
  - `func AgreeRecipient(agreePublic []byte) (Recipient, error)` checks for 32 bytes, then returns `ParseRecipient(bech32Encode("age", agreePublic))`. A wrong length returns `agent agree public key is %d bytes, expected 32`.
  - `func AgreeIdentity(agreePrivate []byte) (Identity, error)` checks for 32 bytes, then returns `ParseIdentity(strings.ToUpper(bech32Encode("AGE-SECRET-KEY-", agreePrivate)))`. A wrong length returns `agent agree private key is %d bytes, expected 32`. age's `X25519(secret, Basepoint)` clamps the scalar exactly as `crypto/ecdh` does, so the derived recipient equals `AgreePublic`.
  - `func ParseRecipient(s string) (Recipient, error)` wraps `age.ParseX25519Recipient` as `not an age X25519 recipient (age1…): %w`. The recipient is public, so it may appear in the error.
  - `func ParseIdentity(s string) (Identity, error)` wraps `age.ParseX25519Identity` and returns the fixed error `not an age X25519 identity (AGE-SECRET-KEY-1…)`. It does not wrap age's error, because a Bech32 error can quote a character of the input.
  - `func CreateRecoveryKeyFile(path string) (Recipient, error)`:
    1. Generate an identity, then `os.OpenFile(path, O_WRONLY|O_CREATE|O_EXCL, 0o600)` with `//nolint:gosec // G304: the owner names the file with --out`.
    2. Write `Secret()+"\n"`, then `Sync`, then `Close`.
    3. If the write, sync or close fails, close and `os.Remove(path)`. The function created the file itself through O_EXCL, so removing it is safe.
    4. Errors: `create recovery key file %s: %w`. When the file exists, the error adds `keys init never overwrites a key: choose a new path` (checked with `errors.Is(err, fs.ErrExist)`).
- `internal/keys/record.go` (new):
  - `type KeyRecord struct { EnvironmentID string; Identity Identity; Protected bool }`. The field order satisfies fieldalignment.
  - `const maxRecordSize = 1024`.
  - `func validEnvironmentID(s string) bool`: exactly 32 characters, each in `0-9a-f`.
  - `func MarshalKeyRecord(r KeyRecord) ([]byte, error)`: refuses an invalid id or a zero Identity, then returns `fmt.Appendf(nil, "{\"v\":1,\"environment_id\":%q,\"protected\":%t,\"identity\":%q}", …)`. Both strings are validated ASCII from the hex or Bech32 alphabets, so `%q` gives the same bytes as JSON. A comment says so.
  - `func ParseKeyRecord(b []byte) (KeyRecord, error)` walks a `json.Decoder` one token at a time:
    - The first token must be `{`.
    - While `dec.More()`, it reads a key, refuses a duplicate key, and reads the value into `json.RawMessage`.
    - Each key accepts one form: `v` must be exactly the bytes `1`; `protected` must be exactly `true` or `false`; `environment_id` must unmarshal as a string and pass `validEnvironmentID`; `identity` must unmarshal as a string and pass `ParseIdentity`.
    - Any other key is `unknown field (allowed: v, environment_id, protected, identity)`. The error does not echo the key name, which comes from the input.
    - After the loop, the next token must be `}`, then `dec.Token()` must return `io.EOF`. Anything else is `trailing data after the key record`.
    - Each of the four fields must have been seen; a missing one gives `missing field %s`.
    - Every error starts `key record: …`. Errors never contain a value from the input; json syntax errors are wrapped as they are.
  - `func WrapKeyRecord(r KeyRecord, to Recipient) ([]byte, error)`: marshal, then `age.Encrypt(&buf, to.r)`, write and close. It returns the ciphertext.
  - `func UnwrapKeyRecord(ciphertext []byte, with Identity, environmentID string) (KeyRecord, error)`:
    1. Call `age.Decrypt(bytes.NewReader(ciphertext), with.id)`.
    2. Read with `io.ReadAll(io.LimitReader(r, maxRecordSize+1))`. Any read error fails before parsing: age authenticates every chunk, so a partial record is never returned. More than `maxRecordSize` bytes is refused.
    3. Call `ParseKeyRecord`. If `rec.EnvironmentID != environmentID`, refuse with `key record is for environment %s, not %s`.
    4. Errors: `unwrap key record: %w`. age's errors do not carry key text.
- `cmd/moonglass/keys.go` (new):
  - `func runKeysInit(args []string, stdout, stderr io.Writer) int`.
  - Parse the `--out` flag with `newFlags("keys init")`. A parse error, any positional argument, or an empty `--out` prints `usage` to stderr and returns 2.
  - Call `keys.CreateRecoveryKeyFile(*out)`. On error, write `moonglass: %v` to stderr and return 1.
  - On success, print to stdout:
    ```
    <age1…>
    Recovery key written to <FILE> (mode 0600). Keep it offline: anyone who can read it can decrypt what is wrapped to it.
    If every server in an environment is gone and no recovery key exists, nobody can decrypt its secrets or backups, including us.
    ```
    A print error returns 1.
  - The file does not import age.
- `cmd/moonglass/main.go`: add `"keys init": func(rest []string) int { return runKeysInit(rest, stdout, stderr) },` to the `local` map.
- `internal/registry/routes.go`: append `{"keys init", "keys init --out FILE", "Create the owner recovery key file and print its age recipient", "the new recovery key file named by --out"}` to `LocalCommands`.
- `.golangci.yml`:
  - Add `filippo.io/age` to `main.allow`.
  - Add a rule `age-deps` with `list-mode: lax`. Its `files` list, one entry per path in the existing style: `**/cmd/**/*.go` and `**/internal/{agent,api,cli,installtest,policy,protocol,proxy,registry,release,server,store}/**/*.go`. Write each path as its own entry, because depguard globs do not expand braces. It denies `filippo.io/age` with the description `only internal/keys may import age (ADR-0015)`.
  - A comment says the rule lists packages because depguard ignored file exclusions, as `policy-deps` notes.
- `.github/workflows/ci.yml`: add `- run: sudo apt-get update && sudo apt-get install -y age` before `- run: make ci`.
- `docs/specs/secrets.md`, "Recovery key" section:
  - The file holds exactly one line: the identity, then `\n`. `age -d -i FILE` reads it.
  - stdout gets exactly the three lines above, with the recipient on line 1. stderr is empty on success. The identity is never printed, logged or put in an error.
  - The command refuses to overwrite a file and creates nothing when the directory is missing.
  - An agent's recipient and identity are the age Bech32 encoding of `agent-agree.key`'s public and private halves, and the file on disk is unchanged.
  - Add the key record's strictness rules: a duplicated field or trailing data is an error.
- `docs/specs/registry.md` line 97: add `keys init` to the `registry.LocalCommands` list.
- `internal/keys/testdata/` (test-only vectors):
  - `recovery.txt`: one test-only identity line.
  - `record.json`: the exact bytes from marshalling a record whose identity is a test-only environment key.
  - `record.age`: `record.json` wrapped to `recovery.txt`.
  - `agent-agree.key`: 32 raw bytes, test-only.
  - `agent-record.age`: `record.json` wrapped to `AgreeRecipient` of the public half of that key.
  - All of these are written by `go test ./internal/keys -run TestKeyVectorsDecryptWithStockAge -update-vectors`, a flag that exists only in `vectors_test.go`.
- `testdata/tests.txt`: regenerate with `make test-list`.

## Tests to write

Write all of these first.

- `./internal/keys TestGenerateEnvironmentKey`: two keys have different `Secret()` and `Recipient().String()`. `ParseIdentity(k.Secret())` succeeds, and the parsed identity's recipient equals `k.Recipient().String()`, which starts with `age1`.
- `./internal/keys TestAgentAgreeKeyAsAgeRecipient`:
  - With `GenerateAgentIdentity()`, `AgreeRecipient(AgreePublic).String()` equals `AgreeIdentity(AgreePrivate).Recipient().String()`.
  - A record wrapped to that recipient unwraps with that identity to an equal record.
  - A table of 0-, 31- and 33-byte inputs, for both the public and the private function: each returns an error containing `expected 32`.
- `./internal/keys TestOnlyX25519RecipientsAndIdentitiesParse`: a table run against `ParseRecipient` and `ParseIdentity`. Every refused string except the corrupted ones has a valid checksum (built in the test with `bech32Encode`), so the refusal is by type.
  - Accepted: a generated recipient and identity.
  - Refused: an `ssh-ed25519 AAAA…` key; an `ssh-rsa AAAA…` key; a plugin recipient `bech32Encode("age1yubikey", 33 bytes)`; a plugin identity `ToUpper(bech32Encode("AGE-PLUGIN-YUBIKEY-", 32 bytes))`; a hybrid recipient `bech32Encode("age1pq", 1216 bytes)`; `""`; a valid recipient with its last character changed; a valid identity with its last character changed.
  - The identity errors do not contain the input.
  - No age hybrid, plugin or SSH API is called.
- `./internal/keys TestKeyRecordRoundTrip`: for `protected` false and true, `MarshalKeyRecord` equals `{"v":1,"environment_id":"<id>","protected":<b>,"identity":"<secret>"}` byte for byte, and `ParseKeyRecord` gives back an equal record. A test helper `recordsEqual` compares the id, `Protected` and `Identity.Secret()`.
- `./internal/keys TestKeyRecordRejectsMalformed`: a table refusing each of these. Every error is non-nil and never contains `AGE-SECRET-KEY` or the input's identity.
  - `v` set to 0, 2 or `1.0`.
  - Each of the four fields missing.
  - An unknown field.
  - A duplicated `v` field and a duplicated `identity` field.
  - Trailing `{}`, and trailing `x`.
  - `environment_id` that is uppercase hex, 31 characters, 33 characters, or non-hex.
  - `identity` that is a recipient, or a corrupted identity.
  - `protected` set to `"false"`, `0` or `null`.
  - Not an object: an array, and an empty input.
- `./internal/keys TestWrapUnwrapKeyRecord`, with every error checked to never contain the identity text:
  - A wrap and unwrap round-trips to an equal record.
  - Unwrapping with another identity fails.
  - A table flipping one byte in the header, the payload and the last byte: each fails.
  - A record wrapped for environment A and unwrapped as environment B fails with an error containing both ids.
- `./internal/keys TestKeyVectorsDecryptWithStockAge`:
  - Helper `stockAge(t)`: `exec.LookPath("age")`. If age is missing, `t.Fatal` when `CI` is set and `t.Skip` otherwise.
  - With the `-update-vectors` flag, rewrite `testdata/`.
  - For each checked-in vector, run `age -d -i <identity file> <ciphertext>`; stdout must equal `record.json`. The agent vector's identity file is `AgreeIdentity(agent-agree.key).Secret()` written to `t.TempDir()`.
  - `UnwrapKeyRecord` on each vector gives a record equal to `ParseKeyRecord(record.json)`.
  - Repeat with freshly generated keys and records: one to a new recovery identity and one to a new `GenerateAgentIdentity` recipient, written under `t.TempDir()`.
- `./internal/keys FuzzParseKeyRecord`:
  - Seeds: a valid record, both values of `protected`, and every input from the malformed table.
  - Property: no panic, and no error contains `AGE-SECRET-KEY`. On success, `MarshalKeyRecord` then `ParseKeyRecord` gives an equal record.
- `./cmd/moonglass TestKeysInitWritesRecoveryKeyFile`:
  - `run({"keys","init","--out",<tmp>/recovery.txt})` exits 0 and stderr is empty.
  - The file mode is `0600`. The file content is one line, which `keys.ParseIdentity` accepts.
  - stdout line 1 equals that identity's `Recipient().String()`. stdout contains the exact ADR-0003 sentence `If every server in an environment is gone and no recovery key exists, nobody can decrypt its secrets or backups, including us.`
  - Neither stdout nor stderr contains the identity.
  - When `age` is on PATH, `age -e -r <recipient>` of `probe`, piped to `age -d -i FILE`, gives back `probe`. When it is not, `t.Log` the skip, or `t.Fatal` under `CI`. The test itself never skips.
- `./cmd/moonglass TestKeysInitFailures`, a table where stdout is always empty:
  - An existing file, created 0644 holding `keep\n`: exit 1, the contents and mode are unchanged, and stderr names the path.
  - `keys init` alone: exit 2, stderr equals `usage`.
  - `keys init --out F extra`: exit 2, stderr equals `usage`.
  - `--out <tmp>/missing/recovery.txt`: exit 1, stderr contains the path, and `<tmp>/missing` still does not exist.
- The existing `./internal/cli TestEverySubcommandIsGeneratedOrLocal` and the `cmd/moonglass` usage tests keep passing unchanged.

## Edge cases

- Wrong-length raw agent keys: `TestAgentAgreeKeyAsAgeRecipient`.
- Non-X25519 formats, including strings with valid checksums: `TestOnlyX25519RecipientsAndIdentitiesParse`.
- A lowercase `age-secret-key-1…` identity is refused, because age's HRP check is case-sensitive. Add it as one more row in the same table.
- Duplicate keys hidden by JSON escaping (`"\u0076"` is `v`): `Token` returns the unescaped key, so the duplicate check sees it. Add a row to `TestKeyRecordRejectsMalformed`.
- An oversized plaintext (more than 1024 bytes) is refused before parsing. Add a row to `TestWrapUnwrapKeyRecord` that wraps 2 KiB of raw bytes with `age.Encrypt` in the test.
- Tampered ciphertext and the wrong identity: `TestWrapUnwrapKeyRecord`. Environment mismatch: the same test.
- An existing file is never overwritten and a missing directory is never created: `TestKeysInitFailures`. A partial write removes the file it created (code review; this cannot be provoked without fault injection).
- Secret leakage: every error path in record parsing and identity parsing has a fixed message, checked by the malformed table, the wrap tests, the fuzz target and the keys-init tests.
- umask can only remove permission bits from 0600, never add them; the CI test asserts 0600 under the default umask.

## Risks

- **Size cap (400 counted lines).** The estimate is bech32 about 35, age.go about 100, record.go about 110, cmd keys.go about 30, plus about 30 across main.go, routes.go, `.golangci.yml`, ci.yml and go.mod: about 305 in total. Tests, testdata, docs and `go.sum` do not count. Keep comments short.
- **`named-tests` skips locally without age.** `TestKeyVectorsDecryptWithStockAge` skips when `age` is missing and `CI` is unset, and the gate fails on a skip. Install age on the machine that runs `scripts/verify` (`brew install age` on macOS) before `scripts/verify check W3-1`.
- **depguard globs.** Brace patterns are not expanded, so list each package path separately. Confirm the rule works by temporarily adding an age import in `cmd/moonglass` and checking that `make lint` fails, then revert.
- **fieldalignment** (`govet enable-all`): field order in `KeyRecord` is string, then pointer struct, then bool. `Recipient` and `Identity` each have a single field.
- **gosec G304** on `OpenFile`: a nolint with a reason, in the existing style.
- **Downloading modules.** `go mod tidy` must fetch `filippo.io/hpke` and `filippo.io/nistec`. They are indirect, so the dependencies gate ignores them, but `go.sum` must include them.
- **Trusted core review.** No hand-written cipher or KDF; Bech32 encodes only and is never trusted to decode; the age imports are confined to `age.go`, `record.go` and `internal/keys` tests. There is no `String()` on `Identity`.
- **Generated code.** `keys init` is local, so `make generate` output is unchanged.
