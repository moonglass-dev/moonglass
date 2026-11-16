# ADR-0008: Protobuf codegen tool

- Status: accepted
- Date: 2026-11-16

## Context

W0-3 introduces the agent ↔ control-plane protocol as a checked-in `.proto` file. Generated Go must be deterministic, reviewed as part of the ticket, and stale output must fail CI. The official Go protobuf runtime is `google.golang.org/protobuf`. A second tool, buf, can wrap `protoc` and add lint and breaking-change checks, but it is another toolchain and not the official compiler.

## Decision

Use the official `protoc` compiler and `protoc-gen-go` from `google.golang.org/protobuf`.

- `protoc` is pinned by version and sha256 and installed into a cache directory by `make proto-tools` (the official release zip, not a package manager).
- `protoc-gen-go` is built from the same module version pinned in `go.mod`.
- Generated Go is checked in under `internal/protocol/agentv1` and is never edited by hand.
- `make proto` is deterministic. `make proto-check` (part of `make ci`) regenerates and fails if `git diff` is not empty.

## Consequences

- Local and CI builds need `curl` and `unzip` to fetch `protoc` on a cache miss.
- Adding a message or field is a protocol change: update the `.proto` file, run `make proto`, and land the generated file in the same ticket.
- buf lint and breaking-change checks are not part of CI. They can be considered later without changing the generated-code contract.

## Alternatives rejected

- **buf as the driver.** Extra toolchain and not the official compiler. Official `protoc` plus a pinned `protoc-gen-go` already give deterministic checked-in output.
- **Generating at build time and not checking files in.** Reviewers would not see the generated code, and CI machines would have to generate it identically on every run without a committed baseline.
- **gRPC plugins (`protoc-gen-go-grpc`).** The agent protocol is a single WebSocket stream of envelopes, not gRPC.

## Open questions

None.

## Links

- `docs/specs/protocol.md`
- `docs/plan/stack.md` §3.1
- [Protocol Buffers Go](https://protobuf.dev/getting-started/gotutorial/)
