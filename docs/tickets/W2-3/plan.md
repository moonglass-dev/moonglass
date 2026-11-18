---
verdict: ready
reason: ""
---

# Plan for W2-3

Every decision comes from `docs/specs/release.md` ("Executor: Deploy (W2-3)"), ADR-0014, ADR-0004 and `docs/specs/deploy-suite.md`. The plan needs no contract change, no ADR change and no module beyond the two ADR-0014 names. The client modules are already in the module cache at `client@v0.6.3` and `api@v1.57.0`.

## Files to change

Every non-test file below counts toward the 585-line cap; the target is about 540. Test files, `go.mod` and `go.sum` do not count.

- `go.mod`, `go.sum`: `go get github.com/moby/moby/client@v0.6.3 github.com/moby/moby/api@v1.57.0`, then `go mod tidy`. Both stay direct requires. Their transitive modules (otel, `containerd/errdefs`, `distribution/reference`, `go-connections`, image-spec) stay indirect and are never imported directly.
- `.golangci.yml`:
  - Add `github.com/moby/moby/client` and `github.com/moby/moby/api` to `depguard.rules.main.allow`, with a comment naming ADR-0014.
  - Add a `docker-deps` rule in the `age-deps` style: `list-mode: lax`, and `files` listing every package that exists today except `internal/deploy`: `**/cmd/**/*.go`, `**/internal/agent/**/*.go`, `**/internal/api/**/*.go`, `**/internal/cli/**/*.go`, `**/internal/installtest/**/*.go`, `**/internal/keys/**/*.go`, `**/internal/policy/**/*.go`, `**/internal/protocol/**/*.go`, `**/internal/proxy/**/*.go`, `**/internal/registry/**/*.go`, `**/internal/release/**/*.go`, `**/internal/server/**/*.go`, `**/internal/store/**/*.go`, `**/internal/update/**/*.go`, `**/tools/**/*.go`. Its `deny` is `pkg: github.com/moby/moby` with `desc: only internal/deploy may import the Docker client today (ADR-0014)`. The pkg is a prefix, so it covers both modules.
- `internal/deploy/engine.go`: package doc, plus the Engine.
  - `type Engine struct{ cli *client.Client }`.
  - `const defaultHost = "unix:///var/run/docker.sock"`, `minAPIVersion = "1.44"`, `pullTimeout = 15 * time.Minute`, `callTimeout = 60 * time.Second`.
  - `func checkHost(host string) error`: nil only for `unix://` followed by a non-empty path. Otherwise it returns `DOCKER_HOST %q is not a unix:// socket; Moonglass reaches the Docker Engine only through its Unix socket (ADR-0014)`.
  - `func checkAPIVersion(v string) error`: uses `client/pkg/versions.LessThan(v, "1.44")` and returns exactly `Docker Engine API <v> is older than 1.44 (Docker Engine 25); upgrade Docker Engine`.
  - `func Connect(ctx context.Context) (*Engine, error)`:
    1. Take the host from `os.Getenv("DOCKER_HOST")`, or `defaultHost` when that is empty.
    2. Run `checkHost` before anything else.
    3. Call `client.New(client.WithHost(host))`, and never `client.FromEnv`, so `DOCKER_API_VERSION` and `DOCKER_TLS_*` are not read. Version negotiation is the client's default.
    4. Call `cli.Ping(ctx, client.PingOptions{NegotiateAPIVersion: true})` under `callTimeout`.
    5. Run `checkAPIVersion(cli.ClientVersion())`.
    6. On any error after `New`, close the client and wrap the error with `connect to Docker Engine at <host>: …`.
  - `func (e *Engine) Close() error`.
  - `func call(ctx) (context.Context, context.CancelFunc)` applies `callTimeout`, and `pullCtx` applies `pullTimeout`. Every Engine call in the package goes through one of the two.
  - `func isNotFound(err error) bool`: `errors.As` against a local `interface{ NotFound() }`. This is the method set of the errdefs errors the client returns, so `containerd/errdefs` is never imported.
- `internal/deploy/container.go`: the only `ContainerCreate` in non-test code.
  - `func createOptions(c policy.Checked, name string) (client.ContainerCreateOptions, error)` reads `c.Spec()` and nothing else.
    - **Carried:** `Image`; `Cmd`; `Env` as sorted `K=V`; `Labels` merged with `AgentLabels` into a new map; `RestartPolicy` to `container.RestartPolicy{Name: …}`; `LogConfig` to `{Type, Config}`.
    - **Networks:** `Networks` becomes `HostConfig.NetworkMode = Networks[0]` and `NetworkingConfig.EndpointsConfig[n] = &network.EndpointSettings{}` for each network.
    - **Metadata only:** `Kind`, `Project`, `Environment` and `Service` are not Engine fields. They are carried as `AgentLabels`.
    - **Refused when set:** `Privileged`, `Mounts`, `NetworkMode`, `PID`, `IPC`, `UTS`, `UserNS`, `CapAdd`, `Devices`, `DeviceCgroupRules`, `SecurityOpt`, `Sysctls`, `CgroupParent`, `Runtime` and `BuildEntitlements`. A non-zero value of any of them returns `container spec field <Field> is set, but the executor does not send it to the Docker Engine; nothing was created`. The error names the field only, never a value. A table lists these fields, so one loop checks them all.
  - `func (e *Engine) create(ctx, c policy.Checked, name string) (string, error)`: builds `createOptions`, then calls `ContainerCreate` under `call`.
  - `func (e *Engine) clearName(ctx, name, appID, releaseID string) error`: calls `ContainerInspect(name)`. A not-found error returns nil. A result whose `Name` is not `"/"+name` is treated as not found, so an ID-prefix match is never mistaken for the container. When the container carries `dev.moonglass.managed=true` with the same app and release, it is force-removed. Any other container fails with `container <name> exists and is not a Moonglass container of release <release_id>; remove or rename it`.
  - `func (e *Engine) start(ctx, id string) error`: `ContainerStart`, then `ContainerInspect`, then `runningAfterStart(state)`.
  - `func runningAfterStart(s *container.State) error`: nil only when `Running && !Restarting`. Otherwise it returns `container exited right after start (status <s>, exit code <n>); see its logs`.
  - `func (e *Engine) remove(ctx, id string) error`: force remove under `call(context.WithoutCancel(ctx))`, so cleanup still runs after the command's context ends.
- `internal/deploy/image.go`: the spec's "Images".
  - `type resolved struct{ ID, RepoDigest string }`.
  - `func localProject(ref string) (project string, ok bool, err error)`: `ok` is true for a `moonglass.local/` prefix. The remainder must be `<project>/<service>:<release_id>` with three non-empty parts. Any other form is an error.
  - `func splitRef(ref string) (repo, digest string)`: same split rule as `policy`'s `splitImage`, where a tag is a colon in the last path element.
  - `func normalizeRepo(repo string) string`: a first element with no `.` or `:` that is not `localhost` gets `docker.io/`. A single element also gets `library/`.
  - `func (e *Engine) resolve(ctx, ref, project string) (resolved, error)`:
    - **`moonglass.local`:** a project other than `project` returns `rejected(fmt.Errorf("image %s belongs to project %s, not %s; a project runs only its own builds", …))`. Otherwise `ImageInspect`, where not-found returns `image <ref> is not on this server; build it again`. `RepoDigest` stays empty. Never pulled.
    - **Digest reference** (contains `@`): `ImageInspect`, and on not-found, `pull`.
    - **Tag:** always `pull`.
    - **Pull** (`func (e *Engine) pull(ctx, ref string) (digest string, err error)`): `ImagePull` under `pullCtx`. It ranges `JSONMessages`, keeps the `Digest: sha256:…` status, and fails on any message with an error.
    - **After a pull or for a digest reference:** `ImageInspect(ref)` for `ID`. Then pick the `RepoDigests` entry whose normalised repository equals `normalizeRepo(repo)` and whose digest equals the known digest (from the reference or the pull). No match fails with `image <ref> has no repository digest for <repo>; push it to its registry again`.
  - The reference is only ever an argument to the client. It never reaches a shell, and no archive is loaded.
- `internal/deploy/network.go`: `func (e *Engine) ensureNetwork(ctx, project string) error` with name `mg-<project>`.
  - `NetworkInspect(name)`. Not-found means `NetworkCreate(name, {Driver: "bridge", Labels: managed + project})`.
  - A found network whose `Name` is not the name, or which lacks either label, fails with `network mg-<project> exists and is not managed by Moonglass`.
  - Networks are never removed.
- `internal/deploy/deploy.go`: the executor.
  - `type Executor struct{ engine *Engine; policy *policy.Policy; db *state.DB }`.
  - `func New(engine *Engine, p *policy.Policy, db *state.DB) *Executor`.
  - `type rejectedError struct{ error }`, built by `func rejected(err error) error`. `outcome(err)` returns `REJECTED` when `errors.As` finds one, and `FAILED` otherwise.
  - `func (x *Executor) Deploy(ctx context.Context, d *agentv1.Deploy) *agentv1.CommandResult`:
    1. **Binding.** `x.db.App(ctx, app)`. A read error is `FAILED` with zero state and no row. When the app is known and the stored binding differs, the result is `REJECTED` with `app <id> is bound to project %q, environment %q, service %q; deploy it with that binding or use a new app id`, the stored state, and no row.
    2. **Transition.** `next, err := st.Deploy(release, image, has_pre_deploy)`. An error is `REJECTED` with the stored state and no row.
    3. Build `rec := state.Record{AppID, ReleaseID, ImageRef: image, Port, HealthPath, Domains, HasPreDeploy}`.
    4. Call `x.prepare(ctx, d, &rec)`, which runs steps 3–7:
       - resolve, then fill `rec.ImageID` and `rec.RepoDigest`;
       - `appSpec(d, img)`, then `x.policy.Check`;
       - `ensureNetwork`;
       - `clearName`;
       - `create`, then fill `rec.ContainerID`;
       - unless `has_pre_deploy`, `start`. A start failure force-removes the container.
    5. **Record.** On success, `x.db.RecordCandidate(ctx, binding, rec, next, time.Now())`. If that fails, the container is force-removed. Step 8 failure is not in the spec, so `docs/specs/release.md` gets one line about it (below).
    6. **Any error after step 2:** clear `rec.ContainerID`, then `x.db.RecordFailed(ctx, rec, detail, time.Now())`. The answer is `outcome(err)` with `detail` and the stored state. If `RecordFailed` itself fails, `detail` adds `; recording the failure also failed: <err>`.
    7. **Success:** `SUCCEEDED` with `ImageId`, `RepoDigest` and `appState(next)`.
  - `func appSpec(d *agentv1.Deploy, image string) policy.Spec` follows the spec's table exactly:
    - `Kind` `app`; `Project`, `Environment` and `Service` from `d`, which equal the binding after step 1;
    - `Image` set to `RepoDigest`, or to `ID` when there is no digest;
    - `Cmd`, `Env` and `Labels` nil;
    - `AgentLabels` holds the six `dev.moonglass.*` keys;
    - `Networks` is `[mg-<project>]`;
    - `RestartPolicy` `unless-stopped`;
    - `LogConfig{Driver: "local", Options: {"max-size": "10m", "max-file": "3"}}`.
  - `func appState(s release.State) *agentv1.AppState`: maps the IDs and the phase (`none`, `pending` and `ready` to `NONE`, `PENDING` and `READY`, with an `exhaustive`-complete switch).
  - `func result(outcome, detail string, s release.State) *agentv1.CommandResult`: `detail` is cut to 4096 bytes at a UTF-8 boundary, which keeps a long policy `Rejection` valid under `validateCommandResult`.
  - Label key constants: `labelManaged`, `labelProject`, `labelEnvironment`, `labelService`, `labelApp`, `labelRelease`.
- `internal/deploy/reconcile.go`:
  - `func keepContainer(labels map[string]string, s release.State) bool`: false when the app or release label is empty. Otherwise true only when the release label equals the ID of `Current`, `Previous` or `Candidate`.
  - `func (x *Executor) Reconcile(ctx context.Context) error { return x.reconcile(ctx) }`.
  - `func (x *Executor) reconcile(ctx context.Context, scope ...string) error`:
    - `ContainerList{All: true, Filters: label=dev.moonglass.managed=true plus each scope label}`.
    - For each container, `x.db.App` on its app label (once per app; a missing label gives zero state). When `!keepContainer`, `remove`.
    - It keeps going past a single failure and returns `errors.Join` of every error, each naming its container.
    - Unmanaged containers are never listed, so never touched. `stop_after` handling is W2-4.
- `docs/specs/release.md`, "Failure": change "when step 6 or 7 fails" to "when step 6, 7 or 8 fails". There is no other change.

## Tests to write

All Docker tests are in package `deploy`, as internal tests, so they can call `reconcile` and `keepContainer`.

- `internal/deploy/helpers_test.go`:
  - **`testEngine(t)`:** `Connect`. On error, it calls `t.Skip` unless `CI` is set, and `t.Fatal` when it is.
  - **`testProject(t, e)`:** `t` plus 12 random hex characters. Its `t.Cleanup` force-removes every container and network with label `dev.moonglass.project=<p>`, every network name the test registered, and every image the test registered.
  - **`importImage(t, e, ref string, entrypoint []string) string`:** `go build` of `./testdata/server` with `CGO_ENABLED=0 GOOS=linux GOARCH=runtime.GOARCH`, packed with `archive/tar` as `/app`, then `ImageImport` with `Changes: ENTRYPOINT [...]`, draining the stream. It returns the image ID.
  - **`newExecutor(t, e)`:** `state.Open(ctx, t.TempDir())` and `policy.Parse(nil, policy.Env{})`. It returns the executor and the data directory.
  - **`releaseRow(t, dataDir, app, rel)`:** `sql.Open("sqlite", …)` with `SetMaxOpenConns(1)`. The driver is already registered by `internal/agent/state`, so the test does not import modernc. It returns `status`, `image_id`, `repo_digest`, `container_id`, `error` and a found flag.
  - **`deployMsg(project, app, rel, image string)`.**
  - **`managedSpec(...)`:** fixture containers go through `policy.Check` and `e.create`, so they use the package's one create path.
- `internal/deploy/testdata/server/main.go`: a standard-library HTTP server on `:8080` answering 200.
- `./internal/deploy TestDeployImportedImageStartsCandidate`: a first deploy of an imported `moonglass.local/<p>/web:r1` gives:
  - **Result:** `SUCCEEDED`, `image_id` = the imported ID, `repo_digest` empty, candidate `r1`, phase `READY`.
  - **Network:** `mg-<p>` exists with both labels.
  - **Container inspect:** running; `Config.Labels` exactly the six agent labels with their values; restart `unless-stopped`; log `local` with `max-size=10m` and `max-file=3`; `NetworkSettings.Networks` keys exactly `[mg-<p>]`; `Image` = the ID; no `Mounts`, `PortBindings` or `CapAdd`; `Privileged` false.
  - **State:** `db.App` returns the binding and the state.
  - **Row:** `candidate` with the image ID and container ID.
- `./internal/deploy TestDeployWithPreDeployLeavesContainerCreated`: `has_pre_deploy` gives `SUCCEEDED`, phase `PENDING`, and container `State.Status == "created"`.
- `./internal/deploy TestDeployRejectsBeforeActing` (table), each row with its own project or projects:
  - (a) A second deploy of a bound app naming another project, environment or service: `REJECTED`, the detail contains the stored triple, and there is no `releases` row for the new release.
  - (b) A deploy while a candidate exists: `REJECTED`, no row.
  - (c) `moonglass.local/<other project>/web:r1`: `REJECTED`, with a `failed` row whose `error` equals the detail.
  - Every row: the counts of containers and networks with the project labels, and with `mg-<other>`, are unchanged, and `db.App` is unchanged.
- `./internal/deploy TestDeployFailuresLeaveStateUnchanged` (table). Each row answers `FAILED`, has a `failed` row with `error` equal to the detail, leaves `db.App` known=false (first deploy), and leaves no container named `mg-<app>-<rel>`, except in (d):
  - (a) A missing `moonglass.local` image gives the exact message.
  - (b) A test-made unlabelled network `mg-<p>`, registered for cleanup, gives the exact message.
  - (c) An image whose entrypoint is `/missing` (an exec that fails at once): the start fails and the container is removed.
  - (d) A container `mg-<app>-<rel>` with only the project agent label: the detail names it and it still exists.
- `./internal/deploy TestDeployReplacesLeftoverContainer`: a created managed container with the same app and release labels is replaced. The deploy is `SUCCEEDED`, the new container ID differs, and the old ID is gone.
- `./internal/deploy TestDeployResolvesRegistryImages`:
  - **Setup:** `registry:2@sha256:<digest>` is pulled and run by the test, with a raw `ContainerCreate`, project label, `5000/tcp` published on `127.0.0.1` with a random port. The worker pins the digest Docker Hub serves for `registry:2` (read with `docker buildx imagetools inspect registry:2`) as a constant. Image A is imported, tagged `localhost:<port>/<p>/web:v1` and pushed; the digest comes from the push stream's aux. Then a different image B is tagged with the same tag locally.
  - (a) Deploying the tag gives `SUCCEEDED`. `repo_digest` = `localhost:<port>/<p>/web@<digest>` and equals the row. `image_id` equals `ImageInspect(repo@digest).ID` and differs from B's ID. The container's `Image` equals `image_id`.
  - (b) After the registry container is removed, a new app deploys `localhost:<port>/<p>/web@<digest>` with `SUCCEEDED` and the same `repo_digest`, so no pull happened.
- `./internal/deploy TestReconcileKeepsReferencedReleases` (pure table over `keepContainer`): current, previous and candidate are kept. Another release, the zero state, a missing app label and a missing release label are removed.
- `./internal/deploy TestReconcileRemovesUnreferencedContainers`:
  - **Setup:** deploy app A at `r1`. Create, through `policy.Check` and `e.create`: a managed container for A at `r0`, a managed container for app `ghost`, and a container with only the project agent label (unmanaged).
  - **Run:** `x.reconcile(ctx, "dev.moonglass.project=<p>")`. The scope keeps the test from touching other runs' containers (two worktrees).
  - **Assert:** the first two are gone, and `r1`'s container and the unmanaged one remain.
- `./internal/deploy TestEngineAPIVersionFloor` (pure, `checkAPIVersion`): `1.40` and `1.43` give the exact message; `1.44`, `1.47` and `1.57` give nil.
- `./internal/deploy TestConnectRequiresUnixSocket`: for each of `tcp://127.0.0.1:2375`, `ssh://host`, `npipe:////./pipe/docker_engine` and `http://localhost`, `t.Setenv("DOCKER_HOST", v)` and `Connect` returns exactly the `checkHost` message containing `v`. That message is produced before `client.New`, so no network call happens. `checkHost("unix:///tmp/docker.sock")` is nil, and `checkHost("unix://")` is refused.
- Helper tests:
  - `TestCreateOptionsCarriesCheckedSpec`: the app spec goes through `Check`, then `createOptions`, and the options equal the expected Config, HostConfig and NetworkingConfig field for field.
  - `TestCreateOptionsRefusesUncarriedFields`: Checked specs that pass an empty policy but set an uncarried field (a named volume mount, `SecurityOpt` `no-new-privileges`, `Sysctls` `net.core.somaxconn`, `NetworkMode` `bridge`, `Runtime` equal to the default) each fail with the field-only message, and a set `Env` value never appears in the error.
  - `TestRunningAfterStart`: running passes; exited and restarting fail.
  - `TestResolveRefParsing`: `localProject`, `splitRef` and `normalizeRepo` (Hub familiar names, `localhost:5000`, a registry port versus a tag).
- `make test-list` regenerates `testdata/tests.txt`.

## Edge cases

- Identity mismatch, an illegal transition and a state read error: no row, nothing touched (`TestDeployRejectsBeforeActing` a, b).
- A cross-project `moonglass.local` image: `REJECTED` after step 2 with a failed row (row c). A malformed `moonglass.local` name fails at step 3 (`TestResolveRefParsing`).
- A policy `Rejection`: `REJECTED`. The detail is the rendered rejection, cut to 4096 bytes. It contains no env value, because `Env` is empty and the policy never renders values.
- An uncarried checked field fails before create (`TestCreateOptionsRefusesUncarriedFields`).
- An unlabelled network, or one found by an ID-prefix match: fails (failures b).
- A same-name foreign container is kept and named (failures d). A leftover managed container is replaced (`TestDeployReplacesLeftoverContainer`).
- A start failure, or exited/restarting at inspect, force-removes (failures c, `TestRunningAfterStart`). The command context being cancelled still allows cleanup (`context.WithoutCancel`).
- A step 8 failure (for example, a reused release ID already recorded as failed) force-removes the container and answers `FAILED`. `RecordFailed` then also refuses the duplicate, and the detail says so. A retry must use a new release ID, as the state package's error already says.
- A tag with a stale local copy is still pulled (`TestDeployResolvesRegistryImages` a). A local digest reference is not pulled (b). A pull stream error or a missing `RepoDigests` match fails.
- A non-unix `DOCKER_HOST` and an API below 1.44 are refused (the two Engine tests).
- Reconcile skips unmanaged containers by its filter, and removes managed ones with no app label, an unknown app or an unreferenced release (both reconcile tests).

## Risks

- **Size cap 585.** Keep one helper per concern and table-driven field checks. golangci and release.md add about 25 lines. If the count passes the cap, cut prose comments before code.
- **`govet` fieldalignment** applies to non-test structs. Order fields pointer-first, or run `fieldalignment -fix` before `make ci`.
- **depguard.** Prefix `github.com/moby/moby` in `docker-deps`. `containerd/errdefs`, `distribution/reference` and `go-connections` are never imported directly, so the strict `main` list needs only the two ADR names. `client/pkg/versions` is inside the allowed client module.
- **`go mod tidy`** may raise indirect versions shared with Caddy (otel, `x/sys`). That is allowed by ADR-0014 "Consequences". `make build-all` with `CGO_ENABLED=0` must still pass. `cmd/` does not import `internal/deploy`, so the binary and the idle-memory budget do not change.
- **Two worktrees at once.** Everything is per-project labelled, images are per-run names, the registry publishes a random port, and `reconcile` takes a project scope in the test. The exported `Reconcile` has no scope, and the named test drives the same function. Reviewers should see `reconcile(ctx, scope...)` as the one implementation, not a test hook with its own behaviour.
- **Test-only raw `ContainerCreate`.** The registry fixture uses one, because `policy.Spec` has no published ports. Every other fixture container goes through `policy.Check` and `e.create`. Non-test code has exactly one `ContainerCreate`, in `container.go`.
- **Start race.** A process that exits at once can still look running at inspect time. Failure row (c) therefore uses an entrypoint that cannot exec, which makes `ContainerStart` fail deterministically. The running-after-start rule itself is covered by `TestRunningAfterStart`. With `unless-stopped`, an exit right after start shows as `Restarting`, which the rule also fails.
- **Engine 29 containerd image store.** Image IDs may be manifest digests. Tests compare against `ImageInspect` results, never a precomputed config digest. `ImagePush` may need `RegistryAuth` set to base64 `{}` on older Engines; the test sets it.
- **Docker Desktop on macOS.** Test images are built for `runtime.GOARCH`. A `localhost:<port>` registry is reachable by the daemon in the VM, and localhost registries are insecure-allowed by default.
- **Secrets.** `Env` is never set, error text names fields only, and the package has no logger. `TestCreateOptionsRefusesUncarriedFields` asserts that an env value is absent from the error.
