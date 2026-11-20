---
verdict: ready
reason: ""
---

# Plan for FR5-2

The owner has already made the decision (update.md §10 item 31: absent means stable). This ticket is docs only. It needs no contract, ADR, module or security decision, because the wire rule, `internal/policy` and ADR-0004 all stay as they are.

The one mapping sentence, the same word for word in all three specs (written `MAP` below):

> `update.channel = "stable"` reports `STABLE`, `update.channel = "canary"` reports `CANARY`, and an absent key or an absent file reports `STABLE`.

## Files to change

- `docs/specs/update.md`
  - Line 3, opening paragraph: change "W7-6e the agent side, the units and the install script (§2 to §7)" to "W7-6e the agent side, the units and the install script (§2 to §7) and sets `Heartbeat.update_channel` from `agent.toml` (§1)". Nothing else changes in that paragraph.
  - §1 "Polling", the `update_channel` bullet (line 35), replaced by: "It learns each agent's channel from a new `Heartbeat` field, `update_channel`, which W7-6d added and validates and W7-6e sets from `update.channel` in `agent.toml` (`docs/specs/container-policy.md`; ADR-0004). The agent maps the file's lower-case value to the upper-case wire value: MAP The field is empty only from an agent built before W7-6e, and an agent that reports empty is never sent an `UpdateOffer`. Any other non-empty value is rejected. The field carries no `UNSPECIFIED` value; the zero value is empty. The control plane cannot change that file (ADR-0004 d5)."
  - §10: no change. Item 31 is already there.
- `docs/specs/protocol.md` "Heartbeat", the `update_channel` paragraph (line 65), replaced by: "W7-6d adds `update_channel` to `Heartbeat` and validates it; W7-6e sets it from `update.channel` in agent.toml. The agent maps the file's lower-case value to the upper-case wire value: MAP It is empty only from an agent built before W7-6e, and such an agent is never sent an `UpdateOffer`. Any other non-empty value is rejected: `stable`, `UNSPECIFIED` and whitespace are not accepted. The field carries no `UNSPECIFIED` value; the zero value is empty (`docs/specs/update.md` §1)."
- `docs/specs/container-policy.md` "agent.toml", the `update.channel` table row (line 327): "`stable` or `canary`. Absent means `stable`. The agent reports it in `Heartbeat.update_channel`: MAP (`docs/specs/update.md` §1)." The "An absent file is an empty configuration" bullet (line 338) already says "channel `stable`" and is left as it is, so it agrees with the absent key.

## Tests to write

- None. The ticket names no tests and changes no code. `make ci` must stay green, and `testdata/tests.txt` is unchanged.

## Edge cases

- Absent key and absent file: both report `STABLE` in all three specs, matching `internal/policy/parse.go:106` (`Config{Channel: "stable"}`) and `internal/policy/types.go:98`.
- Empty wire value: still legal on the wire (`internal/protocol/heartbeat.go` `validateUpdateChannel`). It now comes only from agents built before W7-6e, and such an agent is still never offered an update.
- Hostile or wrong values (`stable`, `UNSPECIFIED`, whitespace): the reject rule is kept in protocol.md word for word, and update.md keeps "Any other non-empty value is rejected".
- Field number 4 and "no `UNSPECIFIED` value": not touched (protocol.md line 238, agent.proto line 74).

## Risks

- `max_diff_lines: 25`: the plan rewrites four existing lines in place and adds none, so the diff is about 8 to 10 lines. Do not reflow nearby text.
- Phrases a reviewer will grep for: after the change, "byte for byte" and "set by W7-6d" must not appear in update.md or protocol.md near `update_channel`. The comment in `internal/protocol/heartbeat.go` about checking "byte for byte" describes the validator, is outside `allowed_paths`, and stays.
- Drift between the three copies: paste MAP exactly, and in review check that the three sentences match character for character.
- Threat model: the text keeps "The control plane cannot change that file (ADR-0004 d5)" and that an offer names no channel. It adds no claim that the reported channel is authenticated, and check 2 (`NOT_ON_CHANNEL`) stays the trust boundary.
- No other §10 item and no ADR edit, so the ticket stays within scope.
