---
verdict: needs-human
reason: "test/deploy must import github.com/moby/moby/client, which ADR-0014 decision 2 and the .golangci.yml docker-deps rule forbid outside internal/deploy, internal/build and internal/agent/disk; owner must amend ADR-0014 and add .golangci.yml to W2-7's allowed_paths, or rescope how the suite reaches the Engine"
---

# Plan for W2-7

## What I found

- The ticket's Scope says the `test/deploy` helpers build and import images and clean up containers, networks and images "through `github.com/moby/moby/client` (already a dependency, ADR-0014; no new module)".
- ADR-0014 decision 2: "Only `internal/deploy`, `internal/build` and `internal/agent/disk` may import either module. depguard denies them everywhere else." The ADR index (`docs/adr/README.md`) says the same.
- The `.golangci.yml` rule `docker-deps` covers `$all` except those three directories, test files included, and denies `github.com/moby/moby`. So `make lint`, and with it `make ci`, fails as soon as a file in `test/deploy` imports the client.
- `.golangci.yml` is a protected path and is not in W2-7's `allowed_paths`. Changing ADR-0014 needs the owner's approval (AGENTS.md, "Contracts … and ADRs change only after the owner approves").
- Today no Go file outside the three allowed directories imports `moby/moby` (`git grep`), so the repo sets no precedent.

## Ways around it that I rejected

- **Exported helpers in `internal/deploy`** (image import, label cleanup) called from `test/deploy`: only `proxy.go` and `proxy_test.go` are allowed in that package. Exported test-only API in product code is also the kind of hook the engineering rules forbid.
- **Standard-library HTTP to the Engine socket from `test/deploy`**: with no client import, lint passes. But it contradicts the ticket's explicit "through `github.com/moby/moby/client`" and ADR-0014's rejected alternative ("The standard library over the socket … we would own the Engine types"). That changes what the ticket asks for, so it is not mine to decide.
- **Putting the suite under `internal/deploy`**: the ticket and `docs/specs/deploy-suite.md` both name the package `test/deploy`.

## Decision needed

The owner picks one:

1. Amend ADR-0014 decision 2 so `test/deploy` (test files only) may import `github.com/moby/moby/client` and `/api`. Then add `.golangci.yml` to W2-7's `allowed_paths` so the `docker-deps` rule can gain `!**/test/deploy/**`. This is the smallest change and keeps the ticket as written.
2. Rescope W2-7 so `test/deploy` reaches the Engine through the standard library over the Unix socket, and record that in `docs/specs/deploy-suite.md`. The suite would then use image import (`POST /images/create?fromSrc=-`) and list and remove containers, networks and images by label filter.

The rest of the ticket needs no further decisions:
- `ProxyConfig.Listen` and its two tests in `internal/deploy`
- the test image and `TestMain`
- the host proxy and the load generator
- `make test-deploy`
- the Engine 25–29 matrix job in `ci.yml`

Once the owner decides, round 2 can plan the whole ticket.
