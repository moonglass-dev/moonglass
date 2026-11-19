---
verdict: ready
reason: ""
---

# Plan for FR1-3

This is a straight move of existing behaviour. No contract, ADR, module or spec behaviour changes. The four files keep their names, raw byte formats and 0600 modes. They are still written through a temp file and a rename, in the same order, and nothing is written before ACCEPTED. Only the package that owns them changes.

## Files to change

- `internal/keys/agentfiles.go` (new). Standard library only: `crypto/ecdh`, `crypto/ed25519`, `errors`, `fmt`, `io/fs`, `os`, `path/filepath`.
  - Unexported constants `fileSigning = "agent-signing.key"`, `fileAgree = "agent-agree.key"`, `filePin = "control-plane.pub"` and `fileFingerprint = "fingerprint"`, with a doc comment that points to `docs/specs/enrolment.md`.
  - `func writeAtomic(dir, name string, data []byte) error`. This is the body of today's `agent.writeFile`, moved with the same logic:
    - `os.CreateTemp(dir, name+".tmp-*")`, with a deferred `os.Remove(tmp.Name())`;
    - `tmp.Chmod(0o600)` before any `Write`;
    - then `Write`, `Close`, and `os.Rename` onto `filepath.Join(dir, name)`.

    Every error is `fmt.Errorf("write %s: %w", filepath.Join(dir, name), err)`. No fsync is added and the temp-file pattern is unchanged, as the ticket's out-of-scope list requires.
  - `func LoadAgentSigningKey(dataDir string) (ed25519.PrivateKey, error)`.
    - Reads with `os.ReadFile(filepath.Join(dataDir, fileSigning))` and keeps the `//nolint:gosec // G304` comment.
    - A read error returns `fmt.Errorf("read %s: %w", path, err)`, so `errors.Is(err, fs.ErrNotExist)` holds.
    - A length other than `ed25519.PrivateKeySize` returns `fmt.Errorf("%s is %d bytes, expected %d", path, n, ed25519.PrivateKeySize)`.
  - `func LoadAgentIdentity(dataDir string) (AgentIdentity, bool, error)`.
    - Calls `LoadAgentSigningKey`. If `errors.Is(err, fs.ErrNotExist)`, it returns `AgentIdentity{}, false, nil`. Any other error is returned.
    - Reads `agent-agree.key`; a read error returns `read %s: %w`.
    - If `ecdh.X25519().NewPrivateKey(agree)` fails, it returns `fmt.Errorf("%s is not an X25519 private key: %w", path, err)`.
    - Builds the identity exactly as today's `loadIdentity` does: `SigningPublic` from `sk.Public()` and `AgreePublic` from `priv.PublicKey().Bytes()`. Returns `true, nil`.
  - `func WriteAgentIdentity(dataDir string, id AgentIdentity) error`.
    - Before writing anything, rejects `len(id.SigningPrivate) != 64` or `len(id.AgreePrivate) != 32` with an error that names the file.
    - Then calls `writeAtomic(dataDir, fileSigning, id.SigningPrivate)`, then `writeAtomic(dataDir, fileAgree, id.AgreePrivate)`.
  - `func LoadControlPlanePin(dataDir string) (ed25519.PublicKey, error)`. A read error returns `read %s: %w`. A length other than `ed25519.PublicKeySize` returns `%s is %d bytes, expected %d`.
  - `func WriteControlPlanePin(dataDir string, pin ed25519.PublicKey) error`. A length other than 32 returns `fmt.Errorf("write %s: control-plane key is %d bytes, expected %d", path, …)` before any file call. Then it calls `writeAtomic(dataDir, filePin, pin)`.
  - `func WriteFingerprint(dataDir, fingerprint string) error`: `writeAtomic(dataDir, fileFingerprint, []byte(fingerprint+"\n"))`.
  - No error ever formats key bytes. Errors carry only paths, lengths, and wrapped OS or ecdh errors, and neither of those quotes the input bytes.
- `internal/agent/enroll.go`: delete the file-name constant block, `loadIdentity` and `writeFile`, and the imports that become unused (`crypto/ecdh`, `io/fs`, `path/filepath`). In `Enroll`:
  - `id, saved, err := keys.LoadAgentIdentity(dataDir)`. On error, return `fmt.Errorf("enrol: %w: remove %s to start over", err, dataDir)`.
  - Key generation, the fingerprint, `MkdirAll(dataDir, 0o700)`, the writability probe and `post` stay as they are, in the same order.
  - After `post`: `if !saved { keys.WriteAgentIdentity(dataDir, id) }`, then `keys.WriteControlPlanePin(dataDir, ed25519.PublicKey(pin))`, then `keys.WriteFingerprint(dataDir, fingerprint)`. Each error is wrapped as `fmt.Errorf("enrol: %w", err)`. Today's write errors carry no advice, and that stays the same.
- `internal/agent/connect.go`: delete `loadConnectKeys`. Drop the `os` and `path/filepath` imports, and `crypto/ed25519` if it becomes unused. Add `io/fs`. At the top of `Connect`:
  - Call `priv, err := keys.LoadAgentSigningKey(dataDir)` and `pin, err := keys.LoadControlPlanePin(dataDir)`.
  - Pass each error through one small unexported helper, `func connectKeyErr(err error, dataDir string) error`:
    - when `errors.Is(err, fs.ErrNotExist)`, it returns ``fmt.Errorf("connect: %w: run `moonglass agent enroll` first", err)``;
    - otherwise it returns `fmt.Errorf("connect: %w: remove %s and enrol again", err, dataDir)`.
  - Connect still reads only these two files and writes nothing.
- `internal/agent/keyfiles_test.go` (new): `TestAgentPackageHoldsNoKeyFileIO`.
- `internal/keys/agentfiles_test.go` (new): the five `internal/keys` tests.
- `docs/specs/enrolment.md`: under "Agent identity (`internal/keys`, agent only)", add one sentence: "`internal/keys` is the only package that knows these four files: it reads them and writes them atomically, and `internal/agent` calls it." No other text changes.
- `testdata/tests.txt`: regenerate with `make test-list`.

Estimated counted size (non-test Go only): about 105 lines added in `internal/keys`, plus about 85 removed and 20 changed in `internal/agent`. That is about 230 lines, under the 350 cap.

## Tests to write

Every test uses `t.TempDir()` only, is table-driven where it has more than one case, and shows the expected and actual values on failure.

- `./internal/keys TestAgentIdentityFilesRoundTrip`:
  - `GenerateAgentIdentity`, `WriteAgentIdentity`, then `LoadAgentIdentity` gives `saved == true` and all four fields `bytes.Equal` to the generated ones.
  - `os.ReadFile` of `agent-signing.key` equals `SigningPrivate` (64 bytes), and of `agent-agree.key` equals `AgreePrivate` (32 bytes). Both have `Mode().Perm() == 0o600`.
  - Writing a second generated identity and loading again gives the second identity.
  - `filepath.Glob(dir/"*.tmp-*")` is empty.
- `./internal/keys TestLoadAgentIdentityAbsentDir`: an empty temp dir gives `saved == false`, `err == nil` and a zero identity, and `os.ReadDir` still returns zero entries.
- `./internal/keys TestLoadAgentIdentityRejectsMalformed`:
  - Table cases, each also asserting `saved == false`:
    - a valid 64-byte signing key and no agree file gives an error naming `agent-agree.key`;
    - a 63-byte signing key with a valid agree key gives an error naming `agent-signing.key`;
    - a valid signing key with a 31-byte agree key gives an error naming `agent-agree.key`;
    - a valid signing key with a 33-byte agree key gives an error naming `agent-agree.key`.
  - `LoadAgentSigningKey` on a 65-byte file gives an error naming `agent-signing.key`.
  - `LoadAgentSigningKey` on an empty dir gives `errors.Is(err, fs.ErrNotExist)` and an error naming `agent-signing.key`.
- `./internal/keys TestControlPlanePinFile`:
  - Write a random 32-byte key: loading it returns the same key, the file bytes equal it, and the mode is 0600.
  - A second key written replaces it.
  - Table of 31- and 33-byte writes: each fails, `control-plane.pub` stays byte-for-byte the second key, and no `*.tmp-*` file is left.
  - A 31-byte file written with `os.WriteFile` is rejected by `LoadControlPlanePin` with an error naming `control-plane.pub`.
  - An empty dir gives `errors.Is(err, fs.ErrNotExist)`.
- `./internal/keys TestFingerprintFile`:
  - Takes the fingerprint from `Fingerprint(id.SigningPublic)`.
  - After `WriteFingerprint`, the file content is exactly `fp+"\n"` and the mode is 0600.
  - `WriteFingerprint(filepath.Join(t.TempDir(), "missing"), fp)` fails and `err.Error()` contains `fingerprint`.
- `./internal/agent TestAgentPackageHoldsNoKeyFileIO`:
  - Reads `os.ReadDir(".")` and checks every regular `*.go` file that does not end in `_test.go`. It does not recurse, so subdirectories such as `state/` are skipped.
  - For each of `agent-signing.key`, `agent-agree.key`, `control-plane.pub` and `"fingerprint"` (with the quotes), it records `file: string` on a `bytes.Contains` match, then fails with a list of every match.
  - It also fails if it scanned no non-test `.go` file, so a wrong working directory cannot pass with nothing checked.

## Edge cases

- Signing key absent: the identity is not saved, the error is nil, and nothing is created (`TestLoadAgentIdentityAbsentDir`; existing `TestEnrollAcceptsAndPins`).
- Signing key present but agree key absent, wrong signing length, or wrong agree length: the error names the file, and `Enroll` adds "remove DIR to start over" (`TestLoadAgentIdentityRejectsMalformed`).
- Missing signing key or pin for `Connect`: `fs.ErrNotExist` is kept and the advice is "run `moonglass agent enroll` first". A wrong length gives "remove DIR and enrol again". `TestLoadAgentIdentityRejectsMalformed` and `TestControlPlanePinFile` cover the `errors.Is` behaviour the helper relies on.
- A wrong-length pin write is refused before any file call, the existing pin is unchanged, and no temp file is left (`TestControlPlanePinFile`). `post` already refuses a wrong-length key, so this is a second guard.
- A wrong-length identity write is refused before any file is touched. This is left to code review: no locked test names it, and a generated identity is always valid.
- A write into a directory that does not exist gives a `CreateTemp` error naming the file (`TestFingerprintFile`).
- The deferred `os.Remove` removes the temp file on every failure path. After a successful rename it does nothing (glob checks in `TestAgentIdentityFilesRoundTrip` and `TestControlPlanePinFile`).
- Replay and unwritable-dir behaviour are unchanged, because files are still written only after `post` returns ACCEPTED (existing `TestEnrollReplayFails` and `TestEnrollUnwritableDataDirDoesNotConsumeToken`).
- Hostile content: key bytes are never formatted into errors, and `ecdh` and `os` errors do not quote file contents.

## Risks

- Existing tests depend on the exact names and formats on disk (`enroll_test.go`, `writeAgent` in `connect_test.go`, `cmd/moonglass/enroll_test.go`). The plan moves the strings without changing them and edits no existing test.
- `TestAgentPackageHoldsNoKeyFileIO` would fail on a harmless `"fingerprint"` literal in `internal/agent`. There is none today (only `keys.Fingerprint` and the identifier `fingerprint`), and the new error text avoids it.
- Error text changes in `connect`: the advice is now chosen by `fs.ErrNotExist` rather than by call site, so a permission error now says "remove DIR and enrol again" instead of "run enroll first". No test checks either text, and both keep the `connect:` prefix.
- `LoadAgentIdentity` checks the signing-key length before it reads the agree key, because the ticket requires it to go through `LoadAgentSigningKey`. Today both files are read first. The only difference is which file the error names when both are bad.
- Lint: the reads keep the existing `//nolint:gosec` G304 comment. `make fmt` removes the unused imports in `internal/agent`. depguard is unaffected because `internal/keys` uses only the standard library, and `.golangci.yml` is untouched.
- Trusted core: `internal/keys` is high risk. The new file is small and a direct move, so the owner can compare it line by line with the deleted `writeFile`, `loadIdentity` and `loadConnectKeys`.
