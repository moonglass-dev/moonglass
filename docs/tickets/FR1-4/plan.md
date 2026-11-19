---
verdict: ready
reason: ""
---

# Plan for FR1-4

## Files to change

- `internal/policy/parse.go`:
  - New unexported `func checkAgentTOML(file string, fi fs.FileInfo, owner int) error`. It runs four checks, in this order and once each:
    1. `fi.Mode()&fs.ModeSymlink != 0` → `fmt.Errorf("%s is a symlink; replace it with a regular file", file)`.
    2. `!fi.Mode().IsRegular()` → `fmt.Errorf("%s is not a regular file; replace it with a regular file", file)`.
    3. Mode, one comparison: `if mode := fi.Mode().Perm(); mode&0o022 != 0 || mode&0o400 == 0` → `fmt.Errorf("%s has unsafe mode %#o: it must be readable by its owner and not writable by group or others; fix with chmod 0600", file, mode)`.
    4. Owner, one comparison, through `statUID`: `uid, ok := statUID(fi)`; `!ok` → `fmt.Errorf("%s: cannot read the file owner, so it is refused; check the file system reports a uid", file)`; `uid != owner` → `fmt.Errorf("%s has the wrong owner (uid %d, want %d); fix with chown", file, uid, owner)`.
  - `LoadFile(file string, env Env, owner int) (*Policy, error)` becomes:
    - `os.Lstat(file)`. `errors.Is(err, fs.ErrNotExist)` → `Parse(nil, env)`. Any other error → `fmt.Errorf("stat %s: %w", file, err)`.
    - `checkAgentTOML(file, fi, owner)`. Its error is returned unchanged.
    - `os.Open(filepath.Clean(file))` → `fmt.Errorf("open %s: %w", file, err)`.
    - `io.ReadAll` → `fmt.Errorf("read %s: %w", file, err)`.
    - `Parse(data, env)` error → `fmt.Errorf("%s: %w", file, err)`.
  - Delete:
    - the `interface{ Uid() uint32 }` assertion and its `st`/`ok` variables;
    - the `_ = ok` line;
    - the repeated mode checks: the second `mode&0o022` check and the `mode&0o400` check;
    - the `uidOK &&` short-circuit;
    - `fileUID` and its callers.
  - The `LoadFile` doc comment states the rules:
    - a regular file, not a symlink;
    - readable by its owner, not writable by group or others;
    - owned by `owner`, and a file whose owner can't be read is refused;
    - every error names the path.
  - Imports: `io/fs` stays (used by `checkAgentTOML`); `errors` stays.
- `internal/policy/stat_unix.go`: no change. `statUID` is the one way to read the uid, and `checkAgentTOML` calls it directly.
- `docs/specs/container-policy.md`, "The file":
  - The first bullet says the file must be a regular file (not a symlink), readable by its owner, not writable by group or others, and owned by `owner` (the agent passes `0`). A file whose owner uid can't be read is refused. Each check runs once, and its error says to fix `chmod` or `chown`.
  - Add a bullet: every error `LoadFile` returns names the file's path, and errors never include file contents.
- `testdata/tests.txt`: regenerated with `make test-list`.

## Tests to write

All tests go in a new file, `internal/policy/load_test.go`. Existing test files on main are locked, so `config_test.go` is not touched. Shared helpers in that file:
- `fakeInfo struct { mode fs.FileMode; sys any }` implements `fs.FileInfo`: `Name` returns `"agent.toml"`, `Size` is 0, `ModTime` is the zero time, `IsDir` comes from the mode, and `Sys` returns `sys`.
- `stat(uid int) *syscall.Stat_t` returns `&syscall.Stat_t{Uid: uint32(uid)}`.
- The fake path is `"/etc/moonglass/agent.toml"`.

- `./internal/policy TestCheckAgentTOMLRefusesUnknownOwner`: a table over two `sys` values, `nil` and a test type `uidOnly struct{ uid uint32 }` with a `Uid() uint32` method (the shape the deleted assertion accepted). Each uses mode `0o600`, with `owner` in {0, 1000, the uid that `uidOnly` reports}. Every case returns an error containing the path and `owner`.
- `./internal/policy TestCheckAgentTOMLMode`: a table over fake regular files with `sys = stat(owner)`:
  - `0o600`, `0o400`, `0o640`, `0o644` → nil;
  - `0o620`, `0o602`, `0o622`, `0o666`, `0o200`, `0o000`, `0o004`, `0o044` → an error containing the path and `chmod`.
  These use only fake `fs.FileInfo` values, so nothing depends on the test's uid and no case skips under root.
- `./internal/policy TestCheckAgentTOMLOwner`: owner 1000. Uid 1000 → nil. Uid 1001 → an error containing the path, `chown`, `1001` and `1000`.
- `./internal/policy TestCheckAgentTOMLFileType`: `fs.ModeSymlink|0o777` → path and `symlink`. `fs.ModeDir|0o755` and `fs.ModeNamedPipe|0o600` → path and `not a regular file`. Each has `sys = stat(owner)`, so only the type check can fail.
- `./internal/policy TestLoadFileErrorsNamePath`: a table of setups under `t.TempDir()`, each returning `(path, owner)`:
  - the parent is a regular file: `<tmp>/plain/agent.toml` where `<tmp>/plain` is a file. This case also asserts `errors.Is(err, syscall.ENOTDIR)`;
  - a symlink to a safe file;
  - a directory;
  - a `0o620` file;
  - a `0o600` file loaded with `os.Getuid()+1`;
  - a `0o600` file holding `[update` (invalid TOML);
  - a `0o600` file holding `[update]\nbogus = 1` (unknown key).
  Every case checks `err != nil` and `strings.Contains(err.Error(), path)`, and the failure message shows the path and the error. Last, an absent `<tmp>/agent.toml` loads with `Config()` equal to `Config{Channel: "stable"}`.
- `./internal/policy TestLoadFileLoadsSafeFile`: for modes `0o600`, `0o400` and `0o644`:
  - write `[update]\nchannel = "stable"\n\n[sources]\nregistries = ["ghcr.io/acme/*"]\n` with `os.WriteFile` at `0o600`, then `os.Chmod` to the mode;
  - load it with `os.Getuid()`;
  - compare `Config()` by `reflect.DeepEqual` with `Config{Channel: "stable", Registries: []string{"ghcr.io/acme/*"}}`.
- Unchanged and still passing: `TestLoadFileAbsentIsEmptyConfig` and `TestLoadFileRejectsUnsafeFile`. Their hints `symlink`, `chmod` and `chown` appear in the new messages.

## Edge cases

- Owner uid can't be read (`Sys()` is nil, or any type other than `*syscall.Stat_t`, including one with a `Uid()` method): refused. `TestCheckAgentTOMLRefusesUnknownOwner`.
- Modes the owner can't read (`0o200`, `0o000`, `0o004`, `0o044`): refused by the one mode check, whether or not the caller is root. `TestCheckAgentTOMLMode`.
- Group or other write, in any combination: refused. `TestCheckAgentTOMLMode`.
- A symlink is caught before the regular-file check, so its error says `symlink` rather than `not a regular file`. `TestCheckAgentTOMLFileType`, `TestLoadFileErrorsNamePath`.
- Named pipe: refused before it is opened, so `LoadFile` never blocks on a FIFO. `TestCheckAgentTOMLFileType`.
- `Lstat` errors other than not-exist (such as ENOTDIR) are wrapped with `%w` and keep the errno. `TestLoadFileErrorsNamePath`.
- An absent file still loads as the empty configuration. `errors.Is(err, fs.ErrNotExist)` replaces `os.IsNotExist`, matching the rest of the package. `TestLoadFileErrorsNamePath`, `TestLoadFileAbsentIsEmptyConfig`.
- Parse errors keep their text (field name, `containers.allow[i]`) after the path prefix, and never show more of the file than `Parse` already reports.

## Risks

- Locked test files: the new tests go in `load_test.go`, and `config_test.go` is not edited. FR1-4 has no owner exception to edit existing tests.
- Size cap of 150: about 40 lines change in `parse.go` and 4 in the spec. Test lines don't count toward the cap.
- Portability: `syscall.Stat_t.Uid` is `uint32` on Linux and macOS. `stat_unix.go` already limits the package to unix, so `load_test.go` needs no build tag of its own. `lstat` through a regular file returns `ENOTDIR` on both systems.
- Root in CI: the mode tests use fake `fs.FileInfo` values, so they don't depend on the uid. The real-file tests use `os.Getuid()` and `os.Getuid()+1`, which also work as root.
- Lint: no unused parameters or results remain, octal literals use `0o`, and there are no banned words.
- There is a gap between the `Lstat` check and the `Open`. This is out of scope: the ticket fixes the checks on the `Lstat` result, and `/etc/moonglass` is owned by root, so the service user can't swap the file (threat model §6). It is not raised as a new decision.
- Error text: the existing tests match `symlink`, `chmod` and `chown`, and the new messages contain each one where expected, so those tests pass unchanged.
