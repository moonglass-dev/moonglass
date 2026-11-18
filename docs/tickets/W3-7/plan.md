---
verdict: ready
reason: ""
---

# Plan for W3-7

## Context

Last round stopped on two blockers, and both are now cleared:

- The impossible 256-byte `deploy_key` round-trip was replaced by a 253-byte case in commit ed3aaf2.
- The field numbers are owner-approved in `docs/specs/protocol.md` line 120.
- The `deploy_test.go` `11` → `15` edit is allowed in `docs/pipeline/children/{tests,build,approve}.md`, and `tools/pipeline/verify.go` `ownerTestEdits["W3-7"]` is on this branch after merge 4adf243.

Nothing left needs a human decision:

- No new module: only `encoding/base64` and `strings` from the standard library.
- No ADR change.
- No security question that the threat model and specs leave open.

## Files to change

- `proto/moonglass/agent/v1/agent.proto`:
  - Add `BUILD = 11;` to `MessageType`.
  - Add `message Build { string app_id = 1; string release_id = 2; string project = 3; string environment = 4; string service = 5; string repository = 6; string commit = 7; }`. Its comment says `repository` and `commit` are validated fields from which the agent builds the clone URL itself, never a URL, git option or shell string.
  - Extend `Heartbeat` with `DiskReport disk = 2; string deploy_key = 3;`. The comment says `disk` absent means not measured, and `deploy_key` is a public key, empty until it exists.
  - Add `message DiskReport { uint64 data_root_free_bytes = 1; uint64 data_root_total_bytes = 2; uint64 agent_dir_free_bytes = 3; uint64 agent_dir_total_bytes = 4; DiskLevel level = 5; }`.
  - Add `enum DiskLevel { DISK_LEVEL_UNSPECIFIED = 0; OK = 1; LOW = 2; CRITICAL = 3; }`.
  - `OK`, `LOW` and `CRITICAL` share the package-wide enum value scope with `NONE`, `PENDING`, `READY`, `ACCEPTED`, `INVALID`, `SUCCEEDED` and the others. None of them collides, so protoc accepts them.
- `internal/protocol/agentv1/agent.pb.go`: regenerated with `make proto` only.
- `internal/protocol/envelope.go`:
  - `validateType`: add `agentv1.MessageType_BUILD` to the accepted list.
  - `typeOf`: add `case *agentv1.Build: return agentv1.MessageType_BUILD, nil`.
  - `validatePayload`: add `case *agentv1.Build: return validateBuild(m)` and `case *agentv1.Heartbeat: return validateHeartbeat(m)`.
  - `Open` and `rejectUnknown` are unchanged. `rejectUnknown` already recurses into the set message field `disk`.
- `internal/protocol/build.go` (new):
  - Constants: `maxOwnerLen = 39`, `maxRepoLen = 100`, `commitHexLen = 40`.
  - `validateBuild(m *agentv1.Build) error`: `validateID` for `app_id` and `release_id`, then `validateName` for `project`, `environment` and `service` (both from `deploy.go`), then `validateRepository`, then `validateCommit`.
  - `validateRepository(s string) error`:
    - `parts := strings.Split(s, "/")` must have `len == 2`. Otherwise the error is `repository %q has %d '/', expected exactly one in <owner>/<repo>`.
    - Owner: length 1–39, every byte in `[A-Za-z0-9-]`, and `owner[0] != '-'`.
    - Repo: length 1–100, every byte in `[A-Za-z0-9._-]`, and `repo[0]` not `-` or `.`. This single first-byte rule also rejects `.` and `..`, so there is no separate check.
    - Every error starts `repository …`, names the part (`owner` or `repo`) and gives the expected form and the actual value or length. Non-printable bytes are reported with `%#x`, as `validateID` does.
  - `validateCommit(s string) error`: `len(s) == commitHexLen` and `strings.Trim(s, hexChars) == ""`. `hexChars` from `deploy.go` is lowercase only, so uppercase is rejected. The error starts `commit …` and gives the length or the value.
- `internal/protocol/heartbeat.go` (new):
  - Constants: `maxDeployKeyLen = 256`, `deployKeyPrefix = "ssh-ed25519 "`, `deployKeySuffix = " moonglass-deploy"`, `base64Chars = "ABC…Zabc…z0123456789+/="`.
  - `validateHeartbeat(m *agentv1.Heartbeat) error`: if `m.GetDisk() != nil`, return any error from `validateDiskReport`. If `m.GetDeployKey() != ""`, return `validateDeployKey`. `agent_version` gets no rule.
  - `validateDiskReport(d *agentv1.DiskReport) error`:
    - Free must not exceed total for both pairs. The error is `disk.data_root_free_bytes %d exceeds disk.data_root_total_bytes %d`, and the same for `agent_dir`.
    - Then switch on `level`: `OK`, `LOW` and `CRITICAL` pass. `UNSPECIFIED` gives `disk.level unspecified, expected OK, LOW or CRITICAL`, and any other value gives `disk.level %d, expected …`.
  - `validateDeployKey(s string) error`, checked in this order, each error starting `deploy_key`:
    1. `len(s) > 256`: reject, giving the length.
    2. `strings.CutPrefix(s, deployKeyPrefix)` fails: reject (wrong key type or a leading space).
    3. `strings.CutSuffix(rest, deployKeySuffix)` fails: reject (missing or other comment, trailing newline).
    4. `b64 == ""`: reject.
    5. `strings.Trim(b64, base64Chars) != ""`: reject. This catches extra spaces, `-`, `_`, NUL, CR and LF. It must run before decoding, because Go's base64 decoder skips `\r` and `\n` even in `Strict` mode.
    6. `base64.StdEncoding.Strict().DecodeString(b64)` fails: reject. This covers unpadded input, a length that is not a multiple of 4, `=` in the wrong place, and non-zero padding bits.
- `internal/protocol/deploy_test.go`: the one owner-approved edit, `11` → `15` in the type list of `TestDeployMessagesRejectUnknownType`. No other existing test line changes.
- `docs/specs/protocol.md`:
  - Line 25: add `BUILD` to the `MessageType` values list.
  - In "Builds and server reports (W3-7)":
    - `DISK_LEVEL_UNSPECIFIED` (0) and unknown levels are rejected.
    - Free ≤ total for each pair is the only rule relating the numbers. Zero totals are allowed when free is zero. Nothing ties `level` to the byte counts, because W3-6 hysteresis means the level cannot be derived from one sample.
    - The exact `deploy_key` grammar: `ssh-ed25519`, one space, non-empty padded standard base64 (RFC 4648 §4, strict, no CR or LF), one space, `moonglass-deploy`, at most 256 bytes. The protocol checks the shape only; whether the base64 decodes to an Ed25519 wire key is W3-8 in `internal/keys`.
    - Opening a payload rejects unknown fields inside `DiskReport`.
  - Leave the approval sentence unchanged.

Expected counted diff (Go outside tests, `.pb.go`, `.proto` and docs): about 130 lines, under `max_diff_lines: 240`.

## Tests to write

`internal/protocol/build_test.go` (new):

- `validBuild() *agentv1.Build`: a helper returning `app-1`, `rel-1`, `web`, `prod`, `api`, `octo-org/app.repo_1`, and `strings.Repeat("a", 40)`.
- `./internal/protocol TestBuildRoundTrip`: `assertNoShellStringFields` on the `Build` descriptor, then a table through the existing `roundTrip(t, MessageType_BUILD, msg, &agentv1.Build{})` helper. Cases:
  - all fields set;
  - 64-byte `app_id` and `release_id`;
  - 63-byte names and 1-byte names;
  - 39-byte owner and 100-byte repo;
  - owner `a` with repo `b`;
  - repo `a.b_c-d`;
  - owner `a-b`;
  - an all-digit commit.
- `./internal/protocol TestBuildRejectsInvalid`: a table of `{name, field, mutate func(*agentv1.Build)}`. Each case wraps the mutated `validBuild()`, opens it, and checks `errors.Is(err, ErrMalformed)` and `strings.Contains(err.Error(), field)`. Cases:
  - `app_id` and `release_id`: empty, 65 bytes, contains `/`.
  - `project`, `environment`, `service`: empty, 64 bytes, uppercase, contains `_`, leading `-`.
  - `repository`:
    - empty, no `/`, two `/`, `/b`, `a/`;
    - owner of 40 bytes, owner `-a`, owner containing `_`, owner containing `.`;
    - repo of 101 bytes, `.`, `..`, `.a`, `-a`;
    - containing `:`, `@`, a space, `\x00`, `é`;
    - `https://github.com/a/b`, `github.com/a/b`, `git@github.com:a/b`.
  - `commit`: empty, 39 and 41 characters, uppercase hex, contains `g`, `main`, and `"-" + 39 hex`.
- `./internal/protocol TestBuildProtocolAbuse`, with subtests:
  - An unknown field (tag 99) appended to a marshaled `Build` fails `Open` with `ErrMalformed`.
  - Opening a `BUILD` envelope as `Deploy`, and a `DEPLOY` envelope (`validDeployMessage()`) as `Build`, both give `ErrTypeMismatch`.
  - `Wrap(BUILD, &Deploy{})` and `Wrap(DEPLOY, &Build{})` both give `ErrTypeMismatch`.
  - A `BUILD` envelope with `Error` set gives `ErrMalformed` from both `Marshal` and `Unmarshal(mustFrame(...))`.
  - `assertNoShellStringFields(Build)`.
- `./internal/protocol FuzzOpenBuild`:
  - Seeds: `validBuild()`, a maximum-boundary `Build`, nil, a payload with an unknown field, and a `Build` with the repository `git@github.com:a/b`.
  - Body, following `FuzzOpenDeploy`:
    - `Open` either succeeds or returns an error that `errors.Is` `ErrMalformed`.
    - If it succeeds, `validateBuild` passes.
    - Re-wrap, `Open` again, and the result is `proto.Equal` to the first.

`internal/protocol/heartbeat_reports_test.go` (new):

- `deployKeyLine(t, pub []byte) string`: builds `uint32be(11) + "ssh-ed25519" + uint32be(32) + pub`, base64-encodes it with `StdEncoding`, and wraps it as `ssh-ed25519 <b64> moonglass-deploy`. It uses `encoding/binary` and `encoding/base64` from the standard library.
- `deployKeyOfB64Len(n int) string`: `ssh-ed25519 ` + `base64.StdEncoding.EncodeToString(make([]byte, n/4*3))` + ` moonglass-deploy`.
- `./internal/protocol TestHeartbeatReportsRoundTrip`: `roundTrip` under `HEARTBEAT` for each case:
  - no reports, with a check that `GetDisk() == nil` after the round trip;
  - a disk report at each of `OK`, `LOW` and `CRITICAL`;
  - free equal to total;
  - every byte count zero;
  - `math.MaxUint64` free and total;
  - a key from `deployKeyLine` with a 32-byte key (`ed25519.NewKeyFromSeed` or fixed bytes);
  - `deployKeyOfB64Len(224)`, with an assertion that `len == 253`;
  - a disk report and a key together.
- `./internal/protocol TestHeartbeatRejectsBadDiskReport`: a table that runs `openRejected` plus a field-name check. Cases:
  - `data_root` free = total + 1;
  - `agent_dir` free = total + 1;
  - free 1 with total 0, for each pair;
  - `level` `UNSPECIFIED`;
  - `level` 4;
  - `level` `math.MaxInt32`;
  - `&DiskReport{}`.
- `./internal/protocol TestHeartbeatRejectsBadDeployKey`: a table that runs `openRejected` plus a check that the error contains `deploy_key`. Cases:
  - `deployKeyOfB64Len(228)` (257 bytes);
  - an `ssh-rsa` prefix;
  - no comment;
  - the comment `other`;
  - two spaces after the type, and two spaces before the comment;
  - a trailing `\n`;
  - a leading space;
  - empty base64;
  - base64 with `-`, and base64 with `_`;
  - unpadded `AAAAAAA`;
  - non-zero padding bits `AB==`;
  - a NUL inside the base64;
  - `\n` inside the base64, which shows the alphabet check runs before decoding.
- `./internal/protocol TestHeartbeatReportsProtocolAbuse`, with subtests:
  - An unknown tag 99 appended to a heartbeat payload is rejected.
  - A `DiskReport` with tag 99 appended, embedded as field 2 using `protowire.AppendTag(…, 2, BytesType)`, is rejected with `ErrMalformed`.
  - Opening `HEARTBEAT` as `Build` and `BUILD` as `Heartbeat` both give `ErrTypeMismatch`.
  - `assertNoShellStringFields` for `Heartbeat` and `DiskReport`.
- `./internal/protocol FuzzOpenHeartbeat`:
  - Seeds: a heartbeat with no reports, one with a valid disk report and key, nil, and one with an unknown field inside `DiskReport`.
  - Body: the same as `FuzzOpenBuild`. It also asserts that an accepted payload has `GetDisk() == nil`, or has free ≤ total for both pairs and a level in {OK, LOW, CRITICAL}.

Then run `make test-list` to update `testdata/tests.txt`.

## Edge cases

- **A valid line exactly at the 256-byte cap cannot exist** (29 fixed bytes plus base64 in multiples of 4). The cap is still enforced first. Covered by the 257-byte reject and the 253-byte accept.
- **Go base64 ignores CR and LF**, even in Strict mode. The alphabet check runs before decoding, so a newline inside the base64 is rejected (`TestHeartbeatRejectsBadDeployKey`, newline-in-base64 case).
- **Repository injection:** a scheme, host, `:`, `@`, whitespace, NUL, non-ASCII bytes, a leading `-`, `.`/`..` and an extra `/` are all rejected by the alphabet, first-byte and split rules (`TestBuildRejectsInvalid`). A leading `-` on the commit is rejected by the hex rule.
- **Disk report present but empty:** level 0 is rejected. Disk report absent is valid and stays nil (both round-trip and reject tests cover this).
- **uint64 extremes:** `MaxUint64` free equal to total is accepted. Free above total is rejected with no arithmetic, so nothing can overflow.
- **Unknown nested fields in `DiskReport`:** rejected by the existing `rejectUnknown` recursion (abuse test and fuzz seed).
- **Unknown enum numbers:** proto3 open enums decode 4 and 2^31−1 as values, and the `default` switch branch rejects them.
- **Type 11 becomes known:** the approved `11` → `15` edit keeps `TestDeployMessagesRejectUnknownType` testing an unknown type. `TestEnvelopeRoundTripEveryType` picks up `BUILD` from the descriptor.

## Risks

- **`proto-check` drift.** The worker must run `make proto` with the pinned toolchain from ADR-0008 and commit `agent.pb.go` unedited.
- **Tests-kept gate.** Only the approved line in `deploy_test.go` changes. New tests go in new files, so no other existing test line is touched.
- **Error-message field check.** Every validator error must contain the field name exactly as the tests use it (`app_id`, `release_id`, `project`, `environment`, `service`, `repository`, `commit`, `disk.data_root_free_bytes`, `disk.agent_dir_free_bytes`, `disk.level`, `deploy_key`). A field-only disk case asserts `disk.` plus the pair name.
- **Banned-words lint.** No "fallback" or "legacy" appears in comments. The `commit` field name does not match `assertNoShellStringFields` (`cmd` is not a substring of it).
- **Scope creep.** There is no direction check, no Ed25519 wire parse, no level computation and no build logic. Those belong to W3-4, W3-6 and W3-8.
