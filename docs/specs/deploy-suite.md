# Docker in tests, and the deploy-correctness suite

How tests reach a real Docker Engine, and what W2-7 builds. Specified by the weeks 2–3 preflight (owner decision D12). Sources: `AGENTS.md` "Testing rules", stack §3.2 and §7, M§7.

## Tests that need Docker

- A test that needs the Engine uses `DOCKER_HOST`, or `/var/run/docker.sock` when that is unset. When no Engine answers, the test **skips**, except when `CI` is set, in which case it **fails**: CI must provide Docker. This is the same rule as Postgres in `docs/specs/storage.md`.
- Each test uses a project name unique to its run (`t` plus 12 random lowercase hex characters), so every container and `mg-<project>` network it creates carries that `dev.moonglass.project` label. The test removes everything with that label when it ends, pass or fail. Two worktrees can run the suite at once.
- **Test images are imported, not pulled.** A test builds a small static Go HTTP server (`CGO_ENABLED=0`) into a root filesystem tar and imports it with the Engine's image import, under `moonglass.local/<test project>/<service>:<release_id>`. That works on Engine 25 to 29 and needs no registry and no builder.
- **Pull and registry tests** run a local registry, `registry:2` pinned by digest, on a `:0` port, with basic auth when the test needs it. It is the only image pulled from Docker Hub.

## The deploy-correctness suite (W2-7)

- Package `test/deploy`. It drives `internal/deploy` and a real `moonglass proxy` against a real Engine.
- **The load generator** is our own Go code (standard library), sending steady requests (default 50 per second) through the proxy and counting every request that does not get a 2xx response. One failed request fails the test.
- The suite runs ten consecutive deploys of the zero-downtime shape (stack §5.3) under load, then a rollback, and asserts zero failed requests and a rollback under 5 seconds.
- `make test-deploy` runs only this suite. `make test` runs it too.

## The Docker matrix (W2-7)

- A CI job per Engine major, 25 to 29, on `ubuntu-22.04`. Docker 25 is published only for Ubuntu 22.04 (`jammy`), not 24.04 (`noble`), at download.docker.com (checked 2026-11-17).
- Each job installs `docker-ce`, `docker-ce-cli`, `containerd.io` and `docker-buildx-plugin` at an exact version from Docker's apt repository, pinned in `ci.yml` (the newest patch of that major when W2-7 is built), then runs `make test-deploy`.
- The main `ci` job keeps running `make ci` on `ubuntu-latest` with its own Docker.
- **Owner action after W2-7 merges:** add the matrix job for Engine 29 as a required check in branch protection. The pipeline cannot change branch protection.
- When GitHub retires `ubuntu-22.04` runners, the Engine 25 job needs a new home. That is a plan decision, not a quiet removal.
