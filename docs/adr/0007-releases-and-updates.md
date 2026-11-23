# ADR-0007: Releases and updates

- **Status:** Accepted
- **Date:** 2026-11-16

## Context

The agent has root-equivalent access to users' servers and updates itself. A bad release would reach every server at once (M§7), and a forged one would be a host compromise. The release signing key is therefore the most sensitive thing the project owns (S§2). Upgrades must also be easy: one click in the dashboard, with a revert when they go wrong (V§1).

## Decision

1. **Signed releases, from W1-6.** Every published binary, checksum file and SBOM carries its own signature, `<artifact>.sig` (S§3.14). Builds come from CI with provenance attestations (W7-6c).
   - **Tool and format:** OpenSSH signatures, `ssh-keygen -Y sign -n moonglass-release`, with hash `sha512`. The release key is an `ed25519-sk` key created on a FIDO2 token with `-O verify-required`, so each signature needs the token's PIN and a touch. Signing is offline, on the owner's machine.
   - **Two pinned keys:** a primary token and a backup token kept apart. Both public keys are committed to the repository before W1-6 and embedded in the install script and the binary. A signature from either verifies.
   - **The install script (W1-6)** verifies each artifact it downloads with `ssh-keygen -Y verify` (OpenSSH 8.2 or later) against the pinned `allowed_signers`, before anything runs or is installed. A failure installs nothing and exits with an error that says why.
   - **The agent (W7-6a, W7-6e)** verifies the same format before it installs anything, with a verifier on the Go standard library only. It checks the `SSHSIG` preamble, version 1, a pinned key, the namespace and `sha512`, then the Ed25519 signature over the application hash, flags, counter and message hash, and requires the user-presence (0x01) and user-verification (0x04) flags. No new dependency.
2. **Two channels.** `canary` gets every release first: the test fleet, plus people who opt in through `agent.toml`. `stable` gets a release after at least 72 hours on canary with no regressions in the permanent suites or the fleet.
3. **Staged rollout on Cloud** (V1-3). Stable agent updates to a Cloud fleet go to 10% of its connected servers, then 50% after 24 hours, then 100% after another 24. The rollout halts when, among agents updated in the current stage, at least 2 agents and at least 1% have either reverted, not reconnected within 10 minutes, or reported a failed self-check in their first hour. A halted rollout stays halted until the owner resumes it or withdraws the release. The same criteria on canary block promotion to stable.
4. **Automatic revert.** The agent keeps the previous binary. If the new one has not passed its self-check within five minutes, systemd starts the old one again. The self-check passes only when all hold:
   - the new agent process is running and has not restarted;
   - its enrolled connection to the control plane is back, with the pinned control-plane key;
   - the SHA-256 of the running executable equals the file whose `.sig` verified against a pinned release key;
   - when the release did not change the proxy, the proxy process has not restarted and its admin socket returns the config the agent last pushed. When it did, the drained proxy upgrade has completed and the same check passes.
5. **Self-hosted** agents download the same signed artifacts for the channel in `agent.toml`, with no percentage rollout; verification and revert are the same. **The operator upgrades the control plane when they choose:** the dashboard shows a newer release on the channel, and Update (or `moonglass update`) upgrades the box's server, agent and proxy through the same download, verification, previous-binary and systemd revert as the agent (AL-5). Nothing upgrades the control plane without that click or command.
6. **The proxy updates separately,** only when Caddy or our counter module changes (S§3.3, ADR-0001). There is one binary on disk, so a release that changes the Caddy config shape or the counter module includes the drained proxy upgrade in the same step; the agent and the running proxy never differ in either. There is no agent-to-proxy compatibility rule.
7. **One compatibility window, after launch only.** Because agents update in stages, the control plane speaks the current and the previous agent protocol version, and nothing older. This is the only compatibility code the product ever carries, and it is deleted one release later (B§4).
8. **Before launch there is no compatibility code at all.** There are no users to stay compatible with (B§4).

## Consequences

- A release reaches the fleet in steps, and a broken one reverts itself. G4 demonstrates both, with an Update from the dashboard: an agent upgrade under load with zero failures, and a bad canary build that reverts (B§2).
- Every release needs the owner present with a token, one touch per artifact. The stock macOS `ssh-keygen` lacks FIDO support, so the owner signs with OpenSSH from Homebrew.
- Losing one token does not strand the fleet; the backup key still verifies. Replacing a pinned key is itself a signed release.
- The install script is trusted through HTTPS from moonglass.dev only. The docs show how to verify the script's own `.sig` before running it.
- After launch, each control-plane release must keep speaking the previous agent protocol version until the release after it.
- Release signing and self-update are trusted core (`AGENTS.md`); the W7-6 tickets and AL-5 are `high`.
- Railpack upgrades ride the same canary channel (S§3.5, LN-12).

## Alternatives rejected

The plan lists no rejected alternatives for releases. These follow from what it does say:

- **One channel with no canary:** a bad release would reach every server at once (M§7).
- **A wider compatibility window:** the plan allows exactly one (B§4).
- **Agents that install without verifying a signature:** S§3.14 requires the check before anything is installed.
- **Unsigned builds until W7-6:** M§2.1 requires the first agent binary to be signed.
- **minisign:** cannot sign from a hardware token.
- **GPG on a token:** verifying needs `ProtonMail/go-crypto`, a large parser in trusted core.
- **cosign / Sigstore:** keyless signing needs a network service; verifying adds a large dependency tree.
- **Raw PIV ECDSA:** a format of our own, and `openssl` is not on every minimal server.
- **One pinned key:** a lost token would leave every server unable to update until reinstalled by hand.
- **A control plane that updates itself unasked:** an operator would not see or choose when the server changes.
- **Percentage rollout on self-hosted:** one operator's fleet is the whole fleet; the canary channel is the stage.

## Links

- S§2 (Releases), S§3.3, S§3.5, S§3.14, S§7
- M§2.1, M§3, M§7
- B§2 (G4), B§3: W1-6, W7-6a to W7-6g, AL-5, V1-3; B§4
- `docs/threat-model.md` §2, §3, §5, checklist items 6 and 7; ADR-0001; ADR-0004 (`[update]` in `agent.toml`)
- OpenSSH `PROTOCOL.sshsig` and `PROTOCOL.u2f`
