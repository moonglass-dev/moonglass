# Install script and systemd units

`scripts/install` is a POSIX `sh` script. It installs the static `moonglass` binary and the units in `deploy/systemd/`. It is the trust boundary for first install (ADR-0007 decision 1; threat-model checklist items 6 and 7).

## Interface

```
install [--from DIR|URL] [--allowed-signers FILE] [--prefix DIR] [--unit-dir DIR]
```

- `--from` is where artifacts are fetched: a local directory (`cp`) or an `http(s)` URL (`curl`). Default: `https://moonglass.dev/releases/latest`.
- `--allowed-signers` replaces the pinned keys. It is a flag only, never an environment variable, and exists so tests can use throwaway keys. Without it the script uses the keys embedded in the script.
- `--prefix` (default `/usr/local`) receives `bin/moonglass`, mode 0755. `--unit-dir` (default `/etc/systemd/system`) receives the two units, mode 0644.
- With neither `--prefix` nor `--unit-dir` the script also does host setup (root only): creates the system users `moonglass` and `moonglass-proxy` and the system group `moonglass-proxy-admin` if absent (W2-12), creates `/etc/moonglass`, runs `systemctl daemon-reload`. A custom layout installs files only. The script never adds any user to the `docker` group and never touches `net.ipv4.ip_unprivileged_port_start`.

## Artifacts and verification

Artifacts: `moonglass`, `SHA256SUMS`, `moonglass.sbom.json`, `moonglass-agent.service`, `moonglass-proxy.service`. Each has `<artifact>.sig`.

1. Everything is fetched into a private `mktemp -d` staging directory, removed on exit.
2. Each artifact is checked with `ssh-keygen -Y verify -f <allowed_signers> -I moonglass-release -n moonglass-release -s <artifact>.sig < <artifact>` (OpenSSH 8.2 or later; the signature was made with hash `sha512`). A signature from either pinned key verifies.
3. The SHA-256 of `moonglass` must equal its entry in the signed `SHA256SUMS`.
4. Only after all of that is anything written under the prefix or unit dir. The binary is written to a temporary name and renamed into place.

Any failure exits non-zero, writes nothing, and prints `moonglass-install: <artifact>: <reason>` to stderr. A missing `.sig` says so; a bad, wrong-key, wrong-namespace or truncated artifact says the signature does not verify.

## Pinned keys

The primary and backup `ed25519-sk` public keys (created on FIDO2 tokens with `-O verify-required`) belong in `PINNED_SIGNERS` at the top of the script, one `allowed_signers` line each:

```
moonglass-release namespaces="moonglass-release" sk-ssh-ed25519@openssh.com AAAA...
```

They are not in the repository yet. The script contains two `OWNER-PASTE` lines in their place. While either remains, the script exits with an error saying the owner must paste the keys. No stand-in keys exist and no private key is in the repository.

## Units

The agent runs as the system user `moonglass` and the proxy as a distinct system user, `moonglass-proxy`, so the proxy cannot read the agent's keys under `/var/lib/moonglass/agent`. The installer creates both users if missing (`--system --no-create-home --shell /usr/sbin/nologin`); neither is added to the `docker` group. Both units set `NoNewPrivileges=true`, `ProtectSystem=strict`, `MemoryMax` set, and the usual kernel, home, tmp and namespace restrictions. State lives under `/var/lib/moonglass/{agent,proxy}` (mode 0700, created by systemd).

- Agent: `SupplementaryGroups=docker moonglass-proxy-admin` (from W2-12), empty `CapabilityBoundingSet`, no ambient capabilities, `MemoryMax=48M`, raised to `MemoryMax=512M` by W3-9 because the host's `git` and `docker buildx` run as the agent's children inside that cap (owner decision D19, `docs/specs/build.md`). The idle budget of W1-7 is unchanged. It runs `moonglass agent connect` and reads `MOONGLASS_URL` from `/etc/moonglass/agent.env`, which the operator creates (root-owned, not writable by the service user).
- Proxy: `Group=moonglass-proxy-admin` and `RuntimeDirectory=moonglass-proxy` with `RuntimeDirectoryMode=0750` (from W2-12), so the admin socket `/run/moonglass-proxy/admin.sock` is reachable by the agent and nobody else (ADR-0001 decision 3). No `docker` group, no Docker socket, no dependency on `docker.service`. `AmbientCapabilities=CAP_NET_BIND_SERVICE` (and only that, also its bounding set) lets the unprivileged user bind :80 and :443. `MemoryMax=64M`. It runs `moonglass proxy --data-dir /var/lib/moonglass/proxy`; the admin socket stays a Unix socket there (proxy spec).

From W3-9 the script also checks for `git` 2.30 or later and the Docker CLI's buildx plugin. Each missing one prints one line naming it and saying that git builds need it; the install continues, because deploys of images work without them (`docs/specs/build.md`, "Host requirements").

`moonglass-proxy-admin` has no members in `/etc/group`; only the two units hold it, the agent as a supplementary group. The script never adds a user to it.

## Out of scope

In-process verifier and self-update (W7-6), channels, rollout, installing Docker.
