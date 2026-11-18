---
verdict: ready
reason: ""
---

# Plan for W2-12

The agent (`moonglass`) and the proxy (`moonglass-proxy`) are different users, so the agent cannot reach today's `<data-dir>/proxy-admin.sock` (0600). ADR-0001 decision 3 (amended 2026-11-17) moves the guarded admin socket to `--admin-socket PATH`. The default is `/run/moonglass-proxy/admin.sock`, mode 0660, group `moonglass-proxy-admin`, in a 0750 systemd runtime directory. Caddy's inner socket, the certificates and the autosave file stay in the 0700 data dir. Nothing here needs a human decision:
- `LocalCommands` is not a registry operation.
- The ADR is already amended.
- Threat model B4 already describes the group.
- No new module is needed.

## Files to change

- `internal/proxy/proxy.go`
  - Delete `adminSocketName` and `AdminSocketPath`.
  - Add `const DefaultAdminSocket = "/run/moonglass-proxy/admin.sock"`. Its doc comment says this is the agent-facing guarded socket, in the proxy unit's `RuntimeDirectory`.
  - New signature `func Run(ctx context.Context, dataDir, adminSocket string) error`. The data-dir handling, the initial config and the autosave path stay as they are.
  - The `caddy.Load` error names `caddyAdminSocketPath(dataDir)`, the socket Caddy opens itself.
  - Run calls `listenAdmin(adminSocket)`, and the serve error names `adminSocket`.
  - The doc comment says the guarded API is served on `adminSocket` and Caddy's own API on `<dataDir>/proxy-caddy.sock`.
- `internal/proxy/guard.go`: `func listenAdmin(path string) (net.Listener, error)` does these steps in order:
  1. `os.Lstat(path)`.
     - If the path exists and is not `os.ModeSocket`, return `fmt.Errorf("admin socket path %s exists and is not a socket; remove it or pass another --admin-socket", path)`.
     - If it is a socket, `os.Remove` it (a stale socket, as today).
     - Return any other non-not-exist Lstat error, wrapped with the path.
  2. `net.Listen("unix", path)`. Error: `listen on admin socket %s: %w; check that its directory exists and is writable (the proxy unit's RuntimeDirectory creates /run/moonglass-proxy)`.
  3. `os.Chown(path, -1, os.Getegid())`. On error, close the listener and return `set group of admin socket %s to gid %d: %w`.
  4. `os.Chmod(path, 0o660)`, annotated `//nolint:gosec // G302: group members (the agent) must connect; the runtime dir is 0750.`. On error, close the listener and return `set admin socket %s to mode 0660: %w`.

  It never creates or chmods the parent directory. `guardedAdmin`, `checkConfig`, `AdminListenAddress`, `StorageDir` and `caddyAdminSocketPath` are unchanged.
- `cmd/moonglass/proxy.go`
  - Add `adminSocket := fs.String("admin-socket", proxy.DefaultAdminSocket, "Unix socket for the guarded admin API the agent uses")`.
  - The `--data-dir` help becomes "directory for Caddy's certificate store, its autosave file and its inner admin socket".
  - Usage check: `*dataDir == "" || *adminSocket == "" || fs.NArg() != 0` prints `usage` and exits 2. The `--data-dir` rules are unchanged. Refusing an empty `--admin-socket` keeps an empty path from reaching `net.Listen`.
  - Call `proxy.Run(ctx, *dataDir, *adminSocket)`.
  - The doc comment says it runs `moonglass proxy --data-dir <dir> [--admin-socket <path>]`.
- `internal/registry/routes.go`: the proxy entry becomes `{"proxy", "proxy --data-dir DIR [--admin-socket PATH]", "Run the proxy (Caddy) with its guarded admin API on a Unix socket", "the proxy data dir and its admin socket"}`. `cli.Usage()` reads this at runtime, so nothing is regenerated.
- `deploy/systemd/moonglass-proxy.service`
  - Add `Group=moonglass-proxy-admin` after `User=moonglass-proxy`.
  - Add `RuntimeDirectory=moonglass-proxy` and `RuntimeDirectoryMode=0750` after `StateDirectoryMode=0700`.
  - `ExecStart` is unchanged, so the default socket is used. Nothing else changes.
- `deploy/systemd/moonglass-agent.service`: `SupplementaryGroups=docker moonglass-proxy-admin`.
- `scripts/install`: inside `if [ "$host_setup" -eq 1 ]`, after the two `useradd` blocks and before `install -d ... /etc/moonglass`, add one line. It uses the literal group name, not a variable:
  `getent group moonglass-proxy-admin >/dev/null 2>&1 || groupadd --system moonglass-proxy-admin || die "cannot create group moonglass-proxy-admin"`
  No user is added to the group. Nothing else in the script changes.
- `docs/specs/proxy.md`
  - "Process and binary": the command is `moonglass proxy --data-dir <dir> [--admin-socket <path>]`. A missing or empty `--data-dir`, or an empty `--admin-socket`, prints the usage and exits 2.
  - "Admin API": describe the new layout as current and drop "Until W2-12 … From W2-12". The agent-facing socket is `--admin-socket PATH`, default `/run/moonglass-proxy/admin.sock`, mode 0660. Its group is the proxy's effective group, which is `moonglass-proxy-admin` under the unit. It sits in the 0750 runtime directory that systemd creates. The proxy refuses to start if the path exists and is not a socket. Tests pass a temp path.
  - Guard paragraph: "the agent-facing socket (`--admin-socket`) is a guard in front of Caddy's own admin socket `<data-dir>/proxy-caddy.sock` (0600, data dir 0700)".
- `docs/specs/install.md`
  - Drop the "(W2-12)" and "(from W2-12)" markers.
  - The end of the proxy-unit bullet becomes "its data dir `/var/lib/moonglass/proxy` (0700) keeps the certificates and Caddy's inner admin socket (proxy spec)".
- `docs/specs/memory.md` step 3: `moonglass proxy --data-dir <temp> --admin-socket <temp>`.
- `testdata/tests.txt`: run `make test-list`.

## Tests to write

The owner allowance (2026-11-17) covers only `internal/proxy/proxy_test.go` and `internal/proxy/memory_test.go`. Every other new test goes in a new file, so no existing line of any other test file changes.

Changes to existing tests:

- `internal/proxy/proxy_test.go`: split `startProxy` into two helpers:
  - `tempProxyDir(t) string` makes `os.MkdirTemp("/tmp", "mgproxy")` and removes it at cleanup.
  - `runProxy(t, dataDir, sock string) *http.Client` holds the existing goroutine, cleanup, client and wait loop. It runs `proxy.Run(ctx, dataDir, sock)` and dials `sock`.

  `startProxy(t) (dataDir, sock string, admin *http.Client)` uses both, with `dataDir = dir + "/data"` and `sock = dir + "/admin.sock"`. That socket is outside the data dir, short, and unique per run. Existing callers take `dataDir, _, admin` or `_, _, admin`. Every test starts and waits for the proxy the same way.
- `TestAdminAPIOnUnixSocket` stats `sock`. It asserts a Unix socket with `perm == 0o660` exactly, which replaces the 0600-or-tighter check as Scope says. It keeps the data-dir 0700 check and the `GET /config/` 200 check.
- `internal/proxy/memory_test.go`, `TestProxyAndAgentIdleUnderBudget`:
  - Set `adminSock := filepath.Join(tmp, "admin.sock")`.
  - Start the proxy with `"proxy", "--data-dir", filepath.Join(tmp, "proxy"), "--admin-socket", adminSock`.
  - The wait loop stats `adminSock`.

New tests in `internal/proxy/admin_socket_test.go` (package `proxy_test`):

- `./internal/proxy TestAdminSocketAtGivenPath`: calls `dataDir, sock, admin := startProxy(t)`. It asserts:
  - `sock` is not under `dataDir`.
  - `GET http://localhost/config/` over `sock` returns 200.
  - `os.Stat(filepath.Join(dataDir, "proxy-admin.sock"))` returns `fs.ErrNotExist`.
- `./internal/proxy TestAdminSocketModeAndGroup`:
  - Call `dir := tempProxyDir(t)`.
  - If `os.Getgroups()` holds a gid other than `os.Getegid()`, chown `dir` to that gid and chmod it `02750`. With the setgid bit, a new file on Linux would inherit that other group, so the group check cannot pass without the `Chown`.
  - Call `runProxy(t, dir+"/data", dir+"/admin.sock")`.
  - Assert the socket has `ModeSocket`, `perm == 0o660` and `Sys().(*syscall.Stat_t).Gid == uint32(os.Getegid())`. Failure messages show the expected and actual values.
- `./internal/proxy TestCaddyAdminSocketStaysPrivate`: calls `dataDir, sock, admin := startProxy(t)`. It asserts:
  - `<dataDir>/proxy-caddy.sock` is a socket with `perm&0o077 == 0`.
  - `dataDir` has `perm&0o077 == 0`.
  - `POST /load` with the correct storage and `admin.listen` set to `"unix/"+sock` returns exactly 400. So does `"unix/"+sock+"|0660"`.
  - `POST /load` with `admin.listen = proxy.AdminListenAddress(dataDir)` and the correct storage returns 200, checked with `push`.
- `./internal/proxy TestAdminSocketRefusesNonSocketPath` (helper test):
  - Call `dir := tempProxyDir(t)`, with `dataDir := dir+"/data"` and `sock := dir+"/admin.sock"`.
  - Write a regular file with known content at `sock`.
  - Call `proxy.Run(ctx, dataDir, sock)` synchronously.
  - Expect a non-nil error that contains `sock` and "not a socket", and expect the file to still exist with the same content.

New tests in `cmd/moonglass/proxy_test.go` (package `main`):

- `./cmd/moonglass TestProxyAdminSocketDefault`:
  - Assert `proxy.DefaultAdminSocket == "/run/moonglass-proxy/admin.sock"`.
  - `run([]string{"proxy", "--help"}, &stdout, &stderr)` returns 2.
  - stderr contains `-admin-socket string` and `(default "` + `proxy.DefaultAdminSocket` + `")`. Go's flag help prints one dash; `--admin-socket` parses the same way.
- `./cmd/moonglass TestProxyRejectsEmptyAdminSocket` (helper test): `run([]string{"proxy", "--data-dir", "/tmp/x", "--admin-socket", ""}, …)` returns 2, stderr equals `usage`, and stdout is empty.

New tests in `internal/installtest/proxy_admin_test.go` (package `installtest`). They reuse `parseUnit`, `unitPaths`, `fields`, `containsString`, `repoRoot`, `newKey`, `newEnv`, `signAll` and `runInstall`.

- `./internal/installtest TestProxyUnitOwnsAdminSocketDir`: the proxy unit has:
  - `Service.User == [moonglass-proxy]`
  - `Service.Group == [moonglass-proxy-admin]`
  - `Service.RuntimeDirectory == [moonglass-proxy]`
  - `Service.RuntimeDirectoryMode == [0750]`
  - no `Service.SupplementaryGroups`

  In `fields(Service.ExecStart)`, if `--admin-socket` appears (as a separate value or as `--admin-socket=VALUE`), the value must equal `"/run/" + RuntimeDirectory + "/admin.sock"`. Otherwise the flag is absent and the default applies.
- `./internal/installtest TestOnlyAgentJoinsProxyAdminGroup`:
  - The agent unit's `fields(SupplementaryGroups)` contains both `docker` and `moonglass-proxy-admin`.
  - The proxy unit has `Group == [moonglass-proxy-admin]`, and no other proxy key has a value containing `moonglass-proxy-admin`.
  - `(?m)^[^#\n]*\b(usermod|gpasswd|adduser|useradd)\b[^\n]*moonglass-proxy-admin` matches nothing in `scripts/install`.
- `./internal/installtest TestInstallCreatesProxyAdminGroup`:
  - Find the root-check line in `scripts/install` (it contains `id -u` and `die`) and the `systemctl daemon-reload` line, and require the root check to come first.
  - Among the non-comment lines strictly between them, exactly one must match `^\s*getent group moonglass-proxy-admin\b.*\|\|\s*groupadd --system moonglass-proxy-admin\s*\|\|\s*die\b`.
  - Then run a custom-layout install with `newEnv`, `signAll` and a shim dir that holds executable `groupadd` and `getent` scripts, both appending to a log. `runInstall` exits 0 without root, and the log file does not exist.

## Edge cases

- **Path is a regular file or directory:** the proxy refuses to start and leaves the file alone (`TestAdminSocketRefusesNonSocketPath`).
- **Stale socket from an earlier run:** the `Lstat` socket branch removes it, as today, before `net.Listen`.
- **Parent directory missing:** the `net.Listen` error names the path and points at the RuntimeDirectory. Run stops Caddy first, as it already does when the listener fails.
- **Parent directory setgid with another group:** `Chown` still sets the effective group (`TestAdminSocketModeAndGroup`).
- **Pushed config that points Caddy's admin at the agent-facing socket:** `checkConfig` returns 400 (`TestCaddyAdminSocketStaysPrivate`). TCP and storage moves are still refused (`TestPushedConfigCannotOpenTCPAdmin`, `TestNoTCPAdminPort`).
- **Empty `--admin-socket`:** prints the usage and exits 2 (`TestProxyRejectsEmptyAdminSocket`).
- **Unix socket path limit:** test paths look like `/tmp/mgproxyNNN/admin.sock` and `/tmp/mgmemNNN/admin.sock`, well under about 100 bytes, and unique per run.
- **Install without root in a custom layout:** it never reaches `groupadd` (`TestInstallCreatesProxyAdminGroup`). A second run is safe because of the `getent` guard.

## Risks

- **`tests-kept` gate:** only `proxy_test.go` and `memory_test.go` change existing lines. `main_test.go` and `install_test.go` stay untouched. `TestUsageOnMissingOrUnknownCommand` compares against the `usage` variable, so the new synopsis does not break it.
- **Gid check passing by accident:** on Linux with no second group, the check could pass without `Chown`. The setgid-parent setup makes it fail without `Chown` on GitHub runners, whose user has extra groups. On macOS, `/tmp` group inheritance makes it fail without `Chown`.
- **gosec G302 on 0660:** annotated with a `nolint` and its reason, as the existing data-dir chmod is.
- **Agent under `ProtectSystem=strict`:** connecting to a socket on a read-only mount is allowed, because EROFS applies only to regular files, directories and symlinks. The agent already reaches `docker.sock` the same way. The proxy can write `/run/moonglass-proxy` only through `RuntimeDirectory`.
- **Umask window between Listen and Chmod:** the socket is never wider than 0777&^umask, and it sits in a 0750 directory, so no other user can reach it.
- **Banned words:** none in added lines, test comments included.
- **Size:** about 50 counted lines, under the 150 cap. Tests and docs are not counted.
- **Caddy is process-global:** the new tests run `proxy.Run` one at a time, with no `t.Parallel`.
- **Old names:** `AdminSocketPath` is gone. The literal `proxy-admin.sock` appears only in `TestAdminSocketAtGivenPath`, which needs it to check the old file is absent.
