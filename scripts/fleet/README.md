# Test fleet

Wipe and rebuild the three week-0 test-fleet VPSs (2 GB x86, 2 GB arm64, 4 GB x86) on Hetzner Cloud, and measure process RSS.

## Token

Export `HCLOUD_TOKEN` or `MOONGLASS_FLEET_TOKEN` in your own shell. The scripts read it at run time, never store it, and never print it. Do not put a token in `scripts/fleet/inventory` or any file in this repository. Agents in the ticket harness run with a scrubbed environment and must not be given the token.

## Inventory

Copy `inventory.example` to `inventory` (gitignored) and fill in the three Hetzner server ids, or export `FLEET_SERVER_X86_2GB`, `FLEET_SERVER_ARM64_2GB` and `FLEET_SERVER_X86_4GB`. Optional: `FLEET_IMAGE` (default `ubuntu-24.04`).

## Commands

```
scripts/fleet/wipe-rebuild --dry-run
scripts/fleet/wipe-rebuild
scripts/measure-memory --pid $$
scripts/measure-memory --cmd ./bin/moonglass
```

`--dry-run` prints the intended rebuilds and exits 0 even when the token or server ids are missing. A real run without a token or without all three server ids exits non-zero and does not call the API.
