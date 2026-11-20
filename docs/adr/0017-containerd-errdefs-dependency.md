# ADR-0017: containerd/errdefs dependency

- Status: accepted
- Date: 2026-11-20

## Context

`github.com/moby/moby/client v0.6.3` (ADR-0014) returns typed HTTP 409 conflict errors that satisfy `errors.Is(err, errdefs.ErrConflict)` and `errdefs.IsConflict(err)` from `github.com/containerd/errdefs`. The error does not implement a `Conflict()` marker method (R-6 finding 2, FR6-2 plan). To recognise an image in use by its typed conflict error, without brittle text matching, `github.com/containerd/errdefs` must be a direct dependency. It is already in `go.mod` as an indirect module of the Docker client.

## Decision

1. Pin `github.com/containerd/errdefs v1.0.0`, the version already in `go.mod` as indirect, as a direct module. FR6-2 makes it direct when it adds the import.
2. Add `github.com/containerd/errdefs` to `depguard.rules.main.allow` in `.golangci.yml`.
3. An `errdefs-deps` depguard rule denies the module in every file outside `internal/deploy`.
4. `internal/deploy` recognises an image in use with `errdefs.IsConflict(err)` in `isConflict`. No error text is matched.

## Consequences

- Upgrading the module is a change to this ADR and to `go.mod`, in one ticket. Its version follows what the Docker client requires.
- The subpackage `github.com/containerd/errdefs/pkg` stays indirect; nothing imports it.

## Alternatives rejected

- **Matching the error text.** Breaks silently when the Engine rewords its message; R-6 finding 2.
- **A `Conflict()` marker interface.** The client's errors do not implement one.
- **Checking the HTTP status from the error.** The client does not expose it on this error.

## Open questions

None.

## Links

- ADR-0014
- `docs/reviews/R-6.md`, finding 2
- B§6: FR6-2
