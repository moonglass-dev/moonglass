# Compose import and reconcile

This is the behaviour spec for Compose import and reconcile (ticket W6-4). It turns the supported field subset, change detection, the pre-confirm list and apply order into behaviour W6-4 can name acceptance tests against. The owner approved this spec on 2026-11-18.

W1-1 already turns a Compose project into container specs after `compose-go` has resolved it and checks each one (`docs/specs/container-policy.md`). W6-4 is the product path: detect change, show the list, apply through the normal deploy path.

## Supported fields

Only these fields are supported. Anything else is refused before apply:

- `image`
- `ports`
- `environment`
- named volumes only — bind mounts are rejected
- `command`
- `depends_on`
- `restart`
- `labels`
- `healthcheck`

## Change detection

Change is a hash of the supported fields only. Unsupported or refused fields never enter the hash. Unchanged services are never touched.

## Pre-confirm list

Before apply, W6-4 shows a list with create, update, unchanged or remove for every service in the project. The operator confirms that list. Circular `depends_on` is rejected; nothing is applied.

## Apply

Apply follows `depends_on` topological order. Changed services redeploy through the normal zero-downtime path (`docs/specs/release.md`), not stop-and-start. Removals send `STOP_APP` (`docs/specs/protocol.md`) and do not delete.
