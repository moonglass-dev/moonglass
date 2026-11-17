# ADR-0014: Docker Engine client

- Status: accepted
- Date: 2026-11-17

## Context

W2-3 is the first ticket to talk to the Docker Engine. Stack §1 and §2 choose "Docker Engine 25+ through its Unix socket, with Docker's own Go client". Stack §3.2 and M§7 require Engine 25 through 29 to work. No ADR has pinned a client module, and depguard allows none. A new module requires an ADR and a depguard entry.

Docker's client now ships as its own modules in the moby repository: `github.com/moby/moby/client` and the API types in `github.com/moby/moby/api`. The monolithic `github.com/docker/docker` module is no longer where the client is developed. Checked upstream on 2026-11-17 (module proxy and the `client/v0.6.3` and `api/v1.57.0` tags of `github.com/moby/moby`).

## Decision

1. Pin `github.com/moby/moby/client v0.6.3` and `github.com/moby/moby/api v1.57.0` in `go.mod` and in the depguard allow list in `.golangci.yml`. The ticket that first imports them (W2-3) adds both, and nothing before it does.
2. Only `internal/deploy`, `internal/build` and `internal/agent/disk` may import either module. depguard denies them everywhere else, including `cmd/`, `internal/agent` outside `disk`, `internal/proxy`, `internal/policy`, `internal/protocol`, `internal/keys`, `internal/store` and `internal/server`.
3. **API versions.** The client negotiates the API version with the Engine (its default). Moonglass requires at least API **1.44**, which is Docker Engine 25. On start the agent pings the Engine. When the negotiated version is below 1.44, the agent exits with: `Docker Engine API <v> is older than 1.44 (Docker Engine 25); upgrade Docker Engine`. The client's own floor (1.40) is not relied on. The client negotiates up to its maximum (1.57), which covers Engine 29.
4. The agent reaches the Engine only through the Unix socket from `DOCKER_HOST`, or `/var/run/docker.sock` when that is unset. No TCP Engine endpoint is supported.
5. Every Engine request that creates a container or starts a build is built from a `policy.Checked` value (`docs/specs/container-policy.md`). This ADR adds no path around the sanitizer.

## Consequences

- One Engine client serves the executor, builds and the disk guard.
- The client brings its own transitive modules (OpenTelemetry HTTP instrumentation, `distribution/reference`, `go-connections`, the OCI image spec). They stay indirect. The idle-memory test (W1-7) measures the agent once it links the client. If the agent passes 32 MB, the ticket says so and the budget is revisited in the plan, not in the test.
- `CGO_ENABLED=0` holds: the modules are pure Go.
- Upgrading the client is a change to this ADR and to `go.mod`, in one ticket, re-checked against Engine 25 and 29 in the W2-7 matrix.

## Alternatives rejected

- **`github.com/docker/docker` v28.** The client has moved out of it. Pinning it would freeze the agent on an old API ceiling.
- **The standard library over the socket.** No new dependency, but we would own the Engine types for images, networks, containers, builds and pruning, against stack §1's choice.
- **containerd directly.** Users lose the `docker` CLI they know (stack §3.2).

## Open questions

None.

## Links

- `docs/plan/stack.md` §1, §2, §3.2; `docs/plan/milestones.md` §7
- `docs/specs/release.md` (executor), `docs/specs/build.md`, `docs/specs/disk.md`
- `docs/threat-model.md` boundary B5
- B§6: W2-3, W3-4, W3-6
