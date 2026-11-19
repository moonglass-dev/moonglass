---
verdict: ready
reason: ""
---

# Plan for W5-5a

The contract change is already approved: owner decision D62, with the field numbers fixed in `docs/specs/delete.md` "Protocol (W5-5a)" and `docs/specs/protocol.md` "Stop and remove an app (W5-5a)". The one edit to an existing test line is already allowed in `docs/pipeline/children/{tests,build,approve}.md` and in `tools/pipeline/verify.go`, under `"W5-5a": {"internal/protocol/deploy_test.go": true}`. No new module, no ADR, and no security decision is left open.

## Files to change

- `proto/moonglass/agent/v1/agent.proto`:
  - In `enum MessageType`, after `BUILD = 11;`, add `STOP_APP = 15;` and `REMOVE_APP = 16;`. Leave 12 to 14 unassigned and renumber nothing.
  - After `message Rollback`, add:
    ```proto
    // StopApp stops every container of an app and removes its routes, keeping its
    // containers, images, releases and volumes. A delete-class command answered with
    // one CommandResult. app_id is an identifier, never a host path or shell string.
    message StopApp {
      string app_id = 1;
    }

    // RemoveApp removes a stopped app's containers, unused images and agent state.
    // A delete-class command answered with one CommandResult. app_id is an
    // identifier, never a host path or shell string.
    message RemoveApp {
      string app_id = 1;
    }
    ```
- `internal/protocol/agentv1/agent.pb.go`: regenerate with `make proto`. Never edit it by hand.
- `internal/protocol/envelope.go`:
  - `validateType`: add `agentv1.MessageType_STOP_APP` and `agentv1.MessageType_REMOVE_APP` to the accepted case list, after `MessageType_BUILD`.
  - `typeOf`: add `case *agentv1.StopApp: return agentv1.MessageType_STOP_APP, nil` and `case *agentv1.RemoveApp: return agentv1.MessageType_REMOVE_APP, nil`.
  - `validatePayload`: add `case *agentv1.StopApp: return validateStopApp(m)` and `case *agentv1.RemoveApp: return validateRemoveApp(m)`.
- `internal/protocol/deploy.go`: put the two validators directly after `validateRollback`. They reuse the existing identifier rule and add no new one:
  ```go
  func validateStopApp(m *agentv1.StopApp) error { return validateID("app_id", m.GetAppId()) }
  func validateRemoveApp(m *agentv1.RemoveApp) error { return validateID("app_id", m.GetAppId()) }
  ```
  Use the same multi-line body style as `validateRollback`.
- `internal/protocol/deploy_test.go`: in `TestDeployMessagesRejectUnknownType`, change the list `{MESSAGE_TYPE_UNSPECIFIED, 15, 99}` to `{MESSAGE_TYPE_UNSPECIFIED, 2147483647, 99}`. This is the only existing line that changes, and the owner allowed it.
- `internal/protocol/stop_remove_test.go` (new file): the three tests and the fuzz target below. It reuses `roundTrip` and `openRejected` from `deploy_test.go`, and `assertNoShellStringFields` from `enroll_test.go`.
- `docs/specs/protocol.md`:
  - The `CommandResult` paragraph (line 108) ends "…, and from W3-7 `BUILD`, and from W5-5b `STOP_APP` and `REMOVE_APP`."
  - The "Stop and remove an app (W5-5a)" section gains one sentence: "In their `CommandResult`, `image_id` and `repo_digest` are empty."
  - The section's numbers and fields stay as they are.
- `testdata/tests.txt`: run `make test-list`.

Lines counted toward `max_diff_lines` (`countsTowardSize` skips tests, `.pb.go` files and docs): about 16 in the `.proto` file, 8 in `envelope.go` and 8 in `deploy.go`, so about 32 of the 80 allowed.

## Tests to write

All of these go in `internal/protocol/stop_remove_test.go`. Every test is `t.Parallel()` and table-driven. Each table covers both messages through a case slice like this:

```go
cases := []struct {
	name string
	mt   agentv1.MessageType
	new  func(id string) proto.Message
	out  func() proto.Message
}{
	{"StopApp", MessageType_STOP_APP, func(id string) proto.Message { return &agentv1.StopApp{AppId: id} }, func() proto.Message { return &agentv1.StopApp{} }},
	{"RemoveApp", MessageType_REMOVE_APP, ...},
}
```

- `./internal/protocol TestStopAndRemoveAppRoundTrip`:
  - For each message and each `app_id` in `"a"`, `strings.Repeat("a", 64)` and `"a.b_c-D9"`: run `roundTrip` (Wrap, Marshal, Unmarshal, Open, then `proto.Equal`).
  - Also call `Wrap` and `Marshal` directly, `Unmarshal` the bytes, and assert `int32(env.GetType())` is 15 for `STOP_APP` and 16 for `REMOVE_APP`, printing the expected and actual values on failure.
- `./internal/protocol TestStopAndRemoveAppRejectMalformed`:
  - For each message and each bad `app_id`, run `openRejected`, then assert that `strings.Contains(err.Error(), "app_id")`.
  - The bad values are: `""` (the zero message), 65 bytes, `"a/b"`, `"a b"`, `"a\x00b"`, `"a:b"`, `"a\xe9b"` (a non-ASCII byte) and `"../x"`.
  - `openRejected` returns no error, so this test inlines `Wrap` and `Open` to get the error message.
- `./internal/protocol TestStopAndRemoveAppProtocolAbuse`. Subtests:
  - Unknown field: for each message, marshal a valid payload, append tag 99 (varint) with value 1, build `Envelope{Version, Type: mt, RequestId: "r", Payload: payload}`, and expect `Open` to return `ErrMalformed`.
  - Open mismatches: a `STOP_APP` envelope opened as `&RemoveApp{}` and as `&Rollback{}`, and a `REMOVE_APP` envelope opened as `&StopApp{}`, each return `ErrTypeMismatch`.
  - Wrap mismatches: `Wrap(STOP_APP, "r", &RemoveApp{AppId: "a"})`, `Wrap(REMOVE_APP, "r", &StopApp{AppId: "a"})` and `Wrap(STOP_APP, "r", &Rollback{AppId: "a"})` each return `ErrTypeMismatch`.
  - Error carried on a command type: for each type, `env := Wrap(...)` with a valid message, then set `env.Error = &Error{Code: INVALID}`; `Marshal(env)` returns `ErrMalformed`.
    - For `Unmarshal`, build the bytes by hand: `proto.MarshalOptions{Deterministic: true}.Marshal(env)`, then `frameBody(body)` (a package-internal function).
    - `Unmarshal` of those bytes returns `ErrMalformed`.
  - Field shape: for each descriptor, the field count is 1, field 1 is named `app_id`, its kind is `StringKind`, and `assertNoShellStringFields` passes.
- `./internal/protocol FuzzOpenStopAndRemoveApp`:
  - Seeds:
    - a marshalled valid `StopApp{AppId: "app-1"}`, which as wire bytes equals a valid `RemoveApp`;
    - `app_id` of 64 bytes;
    - `nil`;
    - the valid payload plus unknown tag 99;
    - `app_id: "a/b"`.
  - Body: for each `(mt, out)` pair, `(STOP_APP, &StopApp{})` and `(REMOVE_APP, &RemoveApp{})`, build an envelope from the input bytes and call `Open`.
    - An error must satisfy `errors.Is(err, ErrMalformed)`.
    - On success, `validateID("app_id", first.GetAppId())` returns nil. Then Wrap again, Open again into a fresh message, and require `proto.Equal` to the first result.
  - The fuzz body stays plain: one helper closure taking `mt` and a `func() proto.Message`, so that `GetAppId` is reached through a small `interface{ GetAppId() string }` type assertion.
- `TestEnvelopeRoundTripEveryType` (an existing test, unchanged) picks up both new types through the descriptor.

## Edge cases

- Empty or zero message: the `""` row in RejectMalformed, and the `nil` fuzz seed.
- Length limits: the 1-byte and 64-byte rows in RoundTrip, the 65-byte row in RejectMalformed, and the 64-byte fuzz seed.
- Path or traversal input (`/`, `../x`), plus `:`, a space, NUL and non-ASCII: rows in RejectMalformed. `validateID` reports the first bad byte and names `app_id`.
- Unknown fields: the `rejectUnknown` path in `Open`, covered by the ProtocolAbuse subtest and a fuzz seed.
- Type confusion between `StopApp`, `RemoveApp` and `Rollback`, which have the same wire shape: `typeOf` and the `env.Type` check return `ErrTypeMismatch`. Covered by the ProtocolAbuse Open and Wrap mismatch subtests.
- An `error` set on a non-`ERROR` type: `validate` rejects it in both `Marshal` and `Unmarshal`. Covered by a ProtocolAbuse subtest.
- Type 15 was the "unknown" sample in `TestDeployMessagesRejectUnknownType`. Changing it to 2147483647 (the maximum `int32` enum value, which no ticket assigns) keeps that test checking an unknown type.

## Risks

- **Existing test edit.** The only changed line is the number in the unknown-type list. The `removedTestLines` gate allows `deploy_test.go` for W5-5a only, so any other existing line that changes fails verify. New tests go in a new file, so no other test file is touched.
- **`proto-check` drift.** Run `make proto`, which uses the pinned protoc in `PROTOC_CACHE`, and commit its output exactly. Never edit `agent.pb.go` by hand.
- **Banned words.** Proto comments and test strings avoid TODO, FIXME, XXX, HACK, fallback and legacy. "Delete-class" is fine.
- **Name clash.** Helpers in `stop_remove_test.go` must not reuse names from other `_test.go` files in the package. Reuse `roundTrip`, `openRejected` and `assertNoShellStringFields`; don't redefine them.
- **Lint.** `exhaustive` is satisfied because both `switch` statements in `validateType` list the new enum values. `gofumpt` is applied by `make fmt`.
- **Scope creep.** No agent handling, no command class entry and no control-plane sender: those are W5-5b, W5-5d and W5-5e. Nothing outside `internal/protocol`, `proto/` and `docs/specs/protocol.md` changes, apart from `testdata/tests.txt`.
- **Verification.**
  - `make test-one PKG=./internal/protocol NAME='TestStopAndRemoveApp|TestDeployMessagesRejectUnknownType|TestEnvelopeRoundTripEveryType'`
  - `go test ./internal/protocol -run '^$' -fuzz FuzzOpenStopAndRemoveApp -fuzztime 30s`
  - `make test-list`, `make ci`, then `scripts/verify check W5-5a`.
