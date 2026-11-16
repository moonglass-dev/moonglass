# ADR-0007: Releases and updates

- **Status:** Accepted
- **Date:** 2026-11-16

## Context

The agent has root-equivalent access to users' servers and updates itself. A bad release would reach every server at once (M§7), and a forged one would be a host compromise. The release signing key therefore becomes the most sensitive thing the project owns (S§2).

## Decision

1. **Signed releases.** Every binary, checksum file and SBOM is signed with a release key held offline on a hardware token. Builds come from CI with provenance attestations. The agent verifies the signature against a pinned public key before it installs anything (S§3.14).
2. **Two channels.** `canary` gets every release first: the test fleet, plus alpha users who opt in through `agent.toml`. `stable` gets a release after at least 72 hours on canary with no regressions in the permanent suites or the fleet.
3. **Staged rollout on hosted.** Stable agent updates go to 10%, then 50%, then 100% of connected servers over about 48 hours. The rollout halts automatically if updated agents fail to reconnect or their health reports degrade.
4. **Automatic revert.** The agent keeps the previous binary. If the new one has not reconnected and passed its self-check within five minutes, systemd starts the old one again.
5. **The proxy updates separately,** only when Caddy or our counter module changes (S§3.3, ADR-0001).
6. **One compatibility window, after launch only.** Because agents update in stages, the control plane speaks the current and the previous agent protocol version, and nothing older. This is the only compatibility code the product ever carries, and it is deleted one release later (B§8).
7. **Before launch there is no compatibility code at all.** There are no users to stay compatible with (B§8).

## Consequences

- A release reaches the fleet in steps, and a broken one reverts itself. G7 demonstrates both: an agent upgrade under load with zero failures, and a bad canary build that reverts (B§5).
- The signing key needs the owner present with the hardware token for every release.
- After launch, each control-plane release must keep speaking the previous agent protocol version until the release after it.
- Release signing and self-update are trusted core (`AGENTS.md`); W7-6 is a Core ticket.
- Railpack upgrades in v1 ride the same canary channel (S§3.5).

## Alternatives rejected

The plan lists no rejected alternatives for releases. These follow from what it does say:

- **One channel with no canary:** a bad release would reach every server at once (M§7).
- **A wider compatibility window:** the plan allows exactly one (B§8).
- **Agents that install without verifying a signature:** S§3.14 requires the check before anything is installed.

## Open questions

1. **Signing tool and signature format.** S§2 says "reuse signing tools" and names none. It must sign from a hardware token offline and be verifiable by the agent. A verifier that needs a new Go dependency needs its own ADR. Owner decision before W7-6.
2. **When signing starts.** M§2.1 says the first agent binary must already be signed; the build plan puts the pipeline at W7-6 and the public key "in the repository later" (B§10). Options: (a) W1-6's install script already verifies a signature, which pulls the key and the tool decision into week 1; (b) signing starts at W7-6 and earlier fleet builds are unsigned test builds. The plan documents disagree, so the owner picks.
3. **Agent and proxy version skew.** There is one binary on disk, so an agent self-update replaces the file the running proxy was started from. The plan does not say whether a new agent may talk to an older running proxy. Options: (a) any release that changes the Caddy config shape or the counter module includes the drained proxy upgrade in the same step, so the two never differ; (b) a second, agent-to-proxy compatibility rule, which B§8 currently rules out.
4. **Updates outside hosted.** The plan describes staged rollout "on hosted". It does not say where self-hosted agents fetch releases, or how `moonglass server` and the CLI update.
5. **Halt and self-check criteria.** "Fail to reconnect" and "health reports degrade" have no thresholds, and the content of the five-minute self-check is not defined.

## Links

- S§2 (Releases), S§3.3, S§3.5, S§3.14, S§7
- M§2.1, M§3 week 7, M§7
- B§5 (G7), B§6: W1-6, W7-6; B§8, B§10
- `docs/threat-model.md` §2, checklist items 6 and 7; ADR-0001; ADR-0004 (`[update]` in `agent.toml`)
