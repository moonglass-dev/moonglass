---
verdict: ready
reason: ""
---

# Plan for W2-8

Context: W2-8 adds deploy identity (`project`, `environment`, `service`), service settings (`port`, `health_path`, `domains`, `has_pre_deploy`) to `Deploy`, and the agent's typed answer `CommandResult` under `COMMAND_RESULT = 10`, with validation in `internal/protocol`. The owner approved the exact `.proto` contract on 2026-11-17 (ticket §9.1), so no contract decision is open. Only `.proto` and non-test Go count toward the 300-line cap (`tools/pipeline/verify.go` `countsTowardSize`); the estimate is about 50 proto lines and about 190 Go lines.

## Files to change

- `proto/moonglass/agent/v1/agent.proto`:
  - `MessageType` gains `COMMAND_RESULT = 10;` after `RUN_PRE_DEPLOY = 9;`.
  - `Deploy` gains `string project = 4; string environment = 5; string service = 6; uint32 port = 7; string health_path = 8; repeated string domains = 9; bool has_pre_deploy = 10;`. Its comment says: names select `agent.toml` blocks, `health_path` is an HTTP path inside the container, and no field is a host path or command.
  - New `message CommandResult { CommandOutcome outcome = 1; string detail = 2; AppState app = 3; string image_id = 4; string repo_digest = 5; }`. Its comment says: sent by the agent only, answers the command with the same `request_id`, and `detail` never carries a secret.
  - New `enum CommandOutcome { COMMAND_OUTCOME_UNSPECIFIED = 0; SUCCEEDED = 1; FAILED = 2; REJECTED = 3; }`.
  - New `message AppState { string current_release_id = 1; string previous_release_id = 2; string candidate_release_id = 3; ReleasePhase phase = 4; }`.
  - New `enum ReleasePhase { RELEASE_PHASE_UNSPECIFIED = 0; NONE = 1; PENDING = 2; READY = 3; }`. The values are package-scoped in proto3, so check that they do not collide with the names that already exist. `NONE`, `PENDING`, `READY`, `SUCCEEDED`, `FAILED` and `REJECTED` are all new.
- `internal/protocol/agentv1/agent.pb.go`: regenerated with `make proto` only.
- `internal/protocol/envelope.go`:
  - `validateType` adds `agentv1.MessageType_COMMAND_RESULT` to the accepted list. `typeOf` maps `*agentv1.CommandResult` to `COMMAND_RESULT`. `validatePayload` adds `case *agentv1.CommandResult: return validateCommandResult(m)`.
  - In `Open`, the top-level `len(msg.ProtoReflect().GetUnknown()) > 0` check is replaced by `rejectUnknown(msg.ProtoReflect())`. That is one new function, `func rejectUnknown(m protoreflect.Message) error`. It returns `ErrMalformed` naming the message's full name when `m.GetUnknown()` is non-empty. Then it calls `m.Range` and recurses into every populated field of message kind: singular fields, and each element of lists of messages. Maps are absent from the schema, so it handles no maps. The old check is deleted, which leaves one way to do it.
- `internal/protocol/deploy.go`:
  - Constants: `maxNameLen = 63`, `maxPort = 65535`, `maxHealthPathLen = 256`, `maxDomains = 16`, `maxDomainLen = 253`, `maxLabelLen = 63`, `maxDetailLen = 4096`, `sha256HexLen = 64`.
  - `validateImage(s string)` becomes `validateImage(field, s string)`, and the field name appears in its messages. The `Deploy` caller passes `"image"`, so existing message text is unchanged.
  - `func validateName(field, s string) error`: 1-63 bytes of `[a-z0-9-]`, and the first and last byte are not `-`.
  - `func validateServiceSettings(m *agentv1.Deploy) error`:
    - `port` must not exceed 65535.
    - A non-empty `health_path` needs `port != 0`, a length of 1-256, a leading `/`, and only `[A-Za-z0-9._~/-]`.
    - `domains` holds at most 16 entries, and any entry needs `port != 0`. Each entry passes `validateDomain(i, d)`. Duplicates are found with a `map[string]int` that records the first index.
  - `func validateDomain(i int, d string) error`: length 1-253; `strings.Split(d, ".")` gives at least 2 labels; each label is 1-63 bytes of `[a-z0-9-]`, and its first and last byte are not `-`. A trailing dot, `a..com` and `*` fail through the empty-label and charset rules. Messages name `domains[i]`.
  - `validateDeploy` keeps its current checks. It then runs `validateName` for `project`, `environment` and `service`, then `validateServiceSettings`.
  - `func validateCommandResult(m *agentv1.CommandResult) error`:
    - outcome is one of `SUCCEEDED`, `FAILED` or `REJECTED`. `UNSPECIFIED` and unknown numbers are rejected.
    - `detail` is at most 4096 bytes, and empty when the outcome is `SUCCEEDED`.
    - `app` is not nil, and `validateAppState` passes.
    - `image_id` is empty or passes `validateSHA256("image_id", s)`.
    - `repo_digest` is empty or passes `validateRepoDigest(s)`.
  - `func validateAppState(a *agentv1.AppState) error`: each of the three release ids is empty or passes `validateID("app.<field>", …)`. `phase` is one of `NONE`, `PENDING` or `READY`, and `UNSPECIFIED` and unknown values are rejected.
  - `func validateSHA256(field, s string) error`: `strings.CutPrefix(s, "sha256:")` must succeed, and the rest is exactly 64 bytes of `[0-9a-f]`.
  - `func validateRepoDigest(s string) error`: `validateImage("repo_digest", s)` passes; `i := strings.LastIndexByte(s, '@')` gives `i >= 1`, which means a non-empty repository; and `validateSHA256("repo_digest", s[i+1:])` passes.
  - Every error wraps `ErrMalformed` and names the field, the expected form, and the actual value or length. A value is quoted with `%q` only for names, domains and digests. `detail` is never echoed, only its length.
- `docs/specs/protocol.md`:
  - Add `COMMAND_RESULT` to the `MessageType` list.
  - Delete "Until W2-8 merges, they are not on the wire.".
  - Under CommandResult, state that `app` is required, that the `ReleasePhase` and `CommandOutcome` zero values are rejected, and that `image_id` and `repo_digest` hex is lowercase.
  - State that `Open` rejects unknown fields at every nesting level.
- `testdata/tests.txt`: rewrite it with `make test-list`.

## Tests to write

Test files:

- `internal/protocol/deploy_test.go` (extended): the three Deploy tests.
- New `internal/protocol/command_result_test.go`: the three CommandResult tests.
- New `internal/protocol/open_fuzz_test.go`: the two fuzz targets.
- They reuse the existing `roundTrip`, `openRejected` and `assertNoShellStringFields` helpers, and `mustFrame` from `envelope_test.go`.

Tests:

- `./internal/protocol TestDeployIdentityRoundTrip`: runs `assertNoShellStringFields` on `Deploy`. A table of `Deploy`s round-trips, each `proto.Equal` to its input:
  - a full message with 63-byte names, 1-character names, `port` 1, a 256-byte `health_path`, 16 domains (one of them 253 bytes, one with a 63-byte label), and `has_pre_deploy` true;
  - the same with `port` 65535 and `has_pre_deploy` false;
  - one with `port` 0, no `health_path` and no domains.
- `./internal/protocol TestDeployRejectsBadIdentity`: covers `project`, `environment` and `service`, each with these values: `""`, 64 bytes, `"Web"`, `"a_b"`, `"a.b"`, `"a b"`, `"a\xffb"`, `"-a"` and `"a-"`. Each gets `ErrMalformed`, and `err.Error()` contains the field name.
- `./internal/protocol TestDeployRejectsBadServiceSettings`: a table of every case the ticket lists. Each starts from a valid base `Deploy` and changes one field:
  - port 65536 and `math.MaxUint32`;
  - `health_path` without a port, `"health"`, 257 bytes, and `?`, `%`, space, `#` and NUL;
  - domains without a port, 17 domains, a duplicate, `"localhost"`, `"*.example.com"`, `"example.com."`, `"Example.com"`, `""`, `"a..com"`, a 64-byte label, `"-a.com"`, `"a-.com"`, and a 254-byte domain.
- `./internal/protocol TestCommandResultRoundTrip`: runs `assertNoShellStringFields` on `CommandResult` and `AppState`. Round-trips under `COMMAND_RESULT`, each `proto.Equal` to its input:
  - `SUCCEEDED` with an empty detail, phase `READY`, all ids set, and valid digests;
  - `FAILED` with a 4096-byte detail, phase `PENDING`, only the candidate id set, and an empty `repo_digest`;
  - `REJECTED` with phase `NONE`, all ids empty, and empty digests.
- `./internal/protocol TestCommandResultRejectsInvalid`: `ErrMalformed` for every case the ticket lists:
  - outcome 0 and 99; a 4097-byte detail; a detail on `SUCCEEDED`; a nil `app`;
  - phase 0 and 99, and a release id `"a/b"`, tried in each of the three id fields;
  - `image_id` as 64 hex with no prefix, `sha256:` plus 63 hex, plus 65 hex, and with uppercase hex;
  - `repo_digest` as `"repo:tag"`, `"@sha256:<64hex>"`, uppercase hex, whitespace, a leading `-`, and 513 bytes.
- `./internal/protocol TestCommandResultProtocolAbuse`:
  - a valid `CommandResult` with tag 99 appended, so the top level has an unknown field, gives `ErrMalformed`;
  - `AppState` bytes with tag 99 appended, written as field 3 of a valid result, give `ErrMalformed`;
  - a valid `Deploy` with tag 99 appended gives `ErrMalformed`;
  - `COMMAND_RESULT` opened as `Deploy`, and `DEPLOY` opened as `CommandResult`, give `ErrTypeMismatch`;
  - `Wrap(COMMAND_RESULT, …, &Deploy{})` gives `ErrTypeMismatch`;
  - an envelope with `COMMAND_RESULT` and `Error` set is rejected by `Marshal`, and by `Unmarshal` of `mustFrame(...)`;
  - a `CommandResult` with a `MaxEnvelopeSize`-byte detail, put through `Wrap` and then `Marshal`, gives `ErrTooLarge`.
- `./internal/protocol FuzzOpenDeploy`: seeds are a full valid message, a minimal one, `nil`, one with an unknown field, and an empty domain entry. The body:
  1. puts the bytes in a `DEPLOY` envelope and calls `Open`, returning on error;
  2. otherwise asserts `validateDeploy == nil`;
  3. runs `Wrap` and then `Open` again, both without error;
  4. checks `proto.Equal` with the first result.
- `./internal/protocol FuzzOpenCommandResult`: the same steps for `COMMAND_RESULT` and `CommandResult`. Seeds are a valid result for each outcome, plus a payload with an unknown field nested in `AppState`.

Existing tests, updated without removing any assertion:

- `TestDeployRoundTrip`: every literal gains `Project: "web", Environment: "prod", Service: "api"`, so each invalid case still fails only for its own reason.
- `TestDeployMessagesRejectUnknownType`: the unknown-type list `{UNSPECIFIED, 10, 99}` becomes `{UNSPECIFIED, 11, 99}`, and its `deploy` literal gains the names.
- `TestEnvelopeRoundTripEveryType` covers `COMMAND_RESULT` through the descriptor with no edit.

## Edge cases

- **Port above `uint16`**: 65536 and 2^32-1 → `TestDeployRejectsBadServiceSettings`.
- **Settings without a port** (a `health_path` or domains with port 0) → the same test.
- **Byte-level charset checks**: a non-ASCII byte in a name fails, and invalid UTF-8 from the wire fails the proto3 parse or the charset check → `TestDeployRejectsBadIdentity` and `FuzzOpenDeploy`.
- **Domain edge cases**: a trailing dot, an empty label, a wildcard, a single label, uppercase, a 64-byte label, a 254-byte total, a duplicate, and 17 entries → `TestDeployRejectsBadServiceSettings`.
- **Unknown fields**: top-level and nested `AppState`, through recursive `rejectUnknown` → `TestCommandResultProtocolAbuse` and both fuzz targets.
- **Enums out of range** (`outcome` and `phase` at 0 and 99) and a missing `app` → `TestCommandResultRejectsInvalid`.
- **Digest forms** (prefix, length, case, empty repository, image rule, 512-byte cap) → `TestCommandResultRejectsInvalid`.
- **Oversized result**: `ErrTooLarge` from `Marshal`. The existing 1 MiB cap on `Unmarshal` runs before parsing → `TestCommandResultProtocolAbuse`.
- **Error field on a non-`ERROR` type** → `TestCommandResultProtocolAbuse`.
- **No panics** on any payload bytes → both fuzz targets.

## Risks

- **Weakening existing tests**: required names would break or weaken `TestDeployRoundTrip`. The plan adds valid names to every literal and removes no assertion.
- **Stale generated code**: `make proto-check` fails if `agent.pb.go` is stale. Run `make proto` and commit its output, never hand edits.
- **Enum name collisions**: proto3 enum values share the package scope. If `protoc` reports a collision, the contract would need to change, so write `BLOCKED.md` and do not rename.
- **Lint**: `port` is compared as a `uint32`, so gosec G115 does not apply. `protoreflect` comes from the existing protobuf dependency, so `go.mod` does not change.
- **Diff cap**: about 240 counted lines against the cap of 300. Validators are plain loops, with no regex.
- **Security review**: `assertNoShellStringFields` runs on all three messages. `detail` is never echoed in errors.
- **Out of scope**: the identity binding, producing results, and direction enforcement stay with W2-3, W2-4 and W2-10. No code for them is added.

## Verification

- `make proto`, then `make test-one PKG=./internal/protocol NAME='TestDeploy|TestCommandResult|TestEnvelope'`.
- `go test ./internal/protocol -run '^$' -fuzz FuzzOpenDeploy -fuzztime 30s`, and the same for `FuzzOpenCommandResult`.
- `make test-list`, `make ci`, then `scripts/verify check W2-8`.
