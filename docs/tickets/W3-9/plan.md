---
verdict: ready
reason: ""
---

# Plan for W3-9

## Files to change

- `deploy/systemd/moonglass-agent.service`: line `MemoryMax=48M` becomes `MemoryMax=512M`. Nothing else changes.
- `scripts/install` (about 20 counted lines; the cap is 80 and the unit adds 2):
  - Next to `die`, add `warn()`. It prints `moonglass-install: %s\n` to stderr and returns 0.
  - `git_version_ok()`:
    ```sh
    git_version_ok() {
    	command -v git >/dev/null 2>&1 || return 1
    	git_out="$(git --version 2>/dev/null)" || return 1
    	printf '%s\n' "$git_out" | awk 'NR == 1 {
    		ok = $1 == "git" && $2 == "version" && $3 ~ /^[0-9]+\.[0-9]+/
    		split($3, v, ".")
    		ok = ok && (v[1] + 0 > 2 || (v[1] + 0 == 2 && v[2] + 0 >= 30))
    	} END { exit !ok }'
    }
    ```
    The output is captured first so git's own exit status counts, since POSIX sh has no `pipefail`. awk only compares the text; nothing evaluates it.
  - `buildx_ok()`: `command -v docker >/dev/null 2>&1 && docker buildx version >/dev/null 2>&1`.
  - After the `host_setup` block and before the final `installed` printf, so it runs after every verify and write in every layout:
    ```sh
    # Build prerequisites (docs/specs/build.md "Host requirements"): a warning, never a failure.
    git_version_ok || warn "git 2.30 or later not found; git builds need it, image deploys work without it"
    buildx_ok || warn "docker buildx plugin not found; git builds need it, image deploys work without it"
    ```
    Both are `||` lists, so `set -e` cannot abort on them and the exit status stays 0. The buildx line has no `git 2.30`, and the git line has no `buildx`.
- `docs/specs/install.md`:
  - The agent bullet says `MemoryMax=512M`, because the host's `git` and `docker buildx` run as the agent's children inside that cap (D19). The W1-7 idle budget is unchanged. Drop "48M, raised by W3-9".
  - Rewrite the prerequisite paragraph. In every layout, after everything is verified and installed, the script runs `git --version` and `docker buildx version`. One `moonglass-install:` line, naming the prerequisite and saying git builds need it, is printed in either case:
    - git is missing, fails, cannot be parsed, or is below 2.30;
    - buildx is missing or fails.

    The check never fails the install and never changes the exit status.
- `internal/installtest/build_prereqs_test.go` (new): the five tests and the helpers below.
- `testdata/tests.txt`: `make test-list` adds the five names.

## Tests to write

Helpers in `build_prereqs_test.go`:
- `writeShim(t, dir, name, body string)`: writes `#!/bin/sh\n` plus the body, mode 0755.
- Shim bodies as constants:
  - `gitOK`: `echo 'git version 2.40.0'`.
  - `exit127`: `exit 127`.
  - `dockerBuildxOK`: exits 0 only for the arguments `buildx version`, otherwise exits 1.
  - `dockerBuildxFails`: a message to stderr, then `exit 1`.

  The version cases use `printf '%s\n' '<output>'`; no table value contains a single quote.
- `installWithPrereqs(t, gitBody, dockerBody string) string`:
  - Steps: a new key, `newEnv`, `signAll`, `e.shimDir = t.TempDir()` with `git` and `docker` shims, then `runInstall`.
  - It fails the test unless the exit is 0, `e.dest()` matches the fixture binary and both units exist in `e.unitDir`.
  - It returns stderr.
- `linesWith(stderr string, subs ...string) []string`: the stderr lines that contain every substring.

Tests:
- `./internal/installtest TestAgentUnitMemoryMaxFitsBuilds`: `parseUnit` gives `Service.MemoryMax` == `["512M"]` for the agent unit and `["64M"]` for the proxy unit. Failure messages print got and want.
- `./internal/installtest TestInstallWarnsWhenGitMissing`: git `exit127`, docker `dockerBuildxOK`. Exactly one line contains `git 2.30` and `git builds`; no line contains `buildx`.
- `./internal/installtest TestInstallWarnsWhenGitTooOld`: a table with docker `dockerBuildxOK` in every case, each exiting 0 with the files installed:
  - `git version 2.29.9`, `git version 1.9.5` and `git version garbage` each want one git line.
  - `git version 2.30.0`, `git version 2.39.3 (Apple Git-146)` and `git version 3.0.1` each want none.
- `./internal/installtest TestInstallWarnsWhenBuildxMissing`: a table with docker `dockerBuildxFails` and docker `exit127`, git `gitOK`. Exactly one line contains `buildx` and `git builds`; no line contains `git 2.30`.
- `./internal/installtest TestInstallQuietWhenBuildPrereqsPresent`:
  - Subtest "present": `gitOK` and `dockerBuildxOK` give no line containing `git builds`. The shims also append their arguments to a log, and the test asserts git ran only as `--version` and docker only as `buildx version`.
  - Subtest "both missing": `exit127` for both gives exactly two `git builds` lines, one containing `git 2.30` and one containing `buildx`.

The existing tests stay unchanged and must pass.

## Edge cases

- **git not on `PATH`:** `command -v` fails and the script warns. The real git may be on `PATH` during tests, so "missing" is a shim exiting 127 placed ahead of it (`TestInstallWarnsWhenGitMissing`, the "both missing" subtest). Both cases return 1 from `git_version_ok`.
- **git exits non-zero or prints nothing:** the command substitution fails, or awk sees no line and `ok` stays unset, so awk runs `exit 1` and the script warns (GitMissing).
- **Unparsable or extra text** (`garbage`, `(Apple Git-146)`): the regex looks only at `$3`, and major then minor are compared as numbers. So `2.29` against `2.30` and `1.9` against `2.30` are numeric compares, not string compares (GitTooOld table).
- **Hostile output:** it is never executed, never `eval`ed and never put into a command; it only goes to awk as data.
- **docker missing or exiting 127, or the buildx plugin missing (exit 1):** one buildx line (BuildxMissing table).
- **Verification failure:** the script dies before the check, so it still exits non-zero with nothing installed (existing `TestInstallRejectsBadSignatureWritesNothing` and `TestInstallVerifiesSignatureBeforeWriting`).
- **Exit status:** the warnings are `||` lists placed after every `die`, so a missing prerequisite never makes the exit non-zero. Every new test asserts exit 0.

## Risks

- **`set -e` aborting on a failed check:** cannot happen. Both checks run inside `||` lists, and awk's status is the function's last command.
- **awk portability** (BWK awk on macOS, mawk or gawk on Linux CI): the script uses only `~` with ERE `+`, `split`, `NR`, `END` and `exit`, all POSIX.
- **Existing tests on a developer machine without Docker** will now see warning lines. None of them asserts that stderr is otherwise empty, so they pass unchanged.
- **Threat model §9 item 6:** no root step is added. The check runs after verification, and in the custom layout it runs without root. In the system layout it runs only `git --version` and `docker buildx version` as root, which the ticket allows.
- **Threat model §9 item 7:** only the agent's `MemoryMax` changes, and it stays set. The proxy keeps 64M.
- **Banned words:** the comment and the messages avoid "fallback" and the other banned words. After the spec edit, `grep -n 'MemoryMax=48M' deploy docs/specs` must find nothing.
- **Size gate:** only `scripts/install` and the unit count, since tests and docs are excluded. That is about 22 lines against a cap of 80.
