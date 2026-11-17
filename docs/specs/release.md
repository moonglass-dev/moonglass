# Release state machine

This is the behaviour spec for `internal/release`: the legal sequence of the deploy commands in `docs/specs/protocol.md` against one app's releases. The machine is pure. It has no Docker, no I/O and no timers. Executing a command is W2-3 onward.

Sources: `docs/plan/stack.md` §3.1, `docs/plan/build-plan.md` (week 2).

## State

One `State` per app:

| Field | Meaning |
|---|---|
| `Current` | The release serving traffic, or nil. |
| `Previous` | The release `Rollback` returns to, or nil. |
| `Candidate` | The release being deployed, or nil. |
| `Phase` | `none` exactly when there is no candidate; `pending` while the candidate waits for its pre-deploy outcome; `ready` when it can be switched. |

A release is an `ID` and an `Image`. Invariants: `Previous` is set only if `Current` is set; a release id is unique within an app. The zero `State` is an app with no releases.

## Events

Each event is a method that takes the state by value and returns the new state and an error. On error the returned state equals the input.

| Event | Protocol command | Notes |
|---|---|---|
| `Deploy(id, image, hasPreDeploy)` | `Deploy` | `hasPreDeploy` is a machine input, not a protocol field. |
| `PreDeployDone(id, ok)` | outcome of `RunPreDeploy` | Reported later by the executor (W2-6). |
| `Switch(id)` | `SwitchRelease` | |
| `Rollback()` | `Rollback` | Names the app only. |

## Transitions

- `Deploy`: legal with no candidate. Creates the candidate, `ready` if `hasPreDeploy` is false, otherwise `pending`. `Current` and `Previous` do not change.
- `PreDeployDone(id, true)`: legal for the `pending` candidate `id`. The candidate becomes `ready`.
- `PreDeployDone(id, false)`: legal for the `pending` candidate `id`. The deploy aborts: the candidate is cleared and `Current` and `Previous` are untouched, so the old release keeps serving. A new `Deploy` is then legal. `reversible` does not change this (it matters to auto-rollback, W4-3).
- `Switch(id)`: legal for the `ready` candidate `id`. The candidate becomes `Current`, the old `Current` becomes `Previous` (nil if there was none), and the candidate is cleared.
- `Rollback()`: legal when `Previous` is set. `Previous` becomes `Current` and `Previous` is cleared; there is no roll-forward, so a new `Deploy` is required. A candidate, if any, and its phase are left untouched.

## Illegal transitions

Anything else returns an error wrapping `ErrIllegalTransition` and leaves the state unchanged. This includes: `Deploy` while a candidate exists (newest-wins supersession is W2-4), `Deploy` with an empty id or an id equal to the current or previous id, `Switch` with no candidate, a pending candidate or a different id, `Rollback` with no previous, and `PreDeployDone` with no candidate, a different id, or a candidate that is not `pending`.

The machine compares ids and rejects the empty one. Id and image formats are checked at the protocol boundary (`docs/specs/protocol.md`), not here.
