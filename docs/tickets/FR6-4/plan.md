---
verdict: ready
reason: ""
---

# Plan for FR6-4

## Files to change

- `.golangci.yml`, `linters.settings.depguard.rules.proxy-deps`: replace the comment with "Caddy is imported only by internal/proxy (ADR-0011). cmd/ calls proxy.Run. Every file outside internal/proxy is covered, so new packages are too." Replace `files` with exactly `"$all"`, `"!**/internal/proxy/**"`. Keep `list-mode: lax`. The deny entry stays `pkg: github.com/caddyserver/caddy/v2`, and its `desc` becomes `only internal/proxy may import Caddy (ADR-0011)`.
- `.golangci.yml`, `websocket-deps`: the comment becomes "coder/websocket is imported only by internal/agent and internal/server (ADR-0012). cmd/ calls into those packages. Every file outside those two directories is covered, so new packages are too." `files` becomes exactly `"$all"`, `"!**/internal/agent/**"`, `"!**/internal/server/**"`. Keep `list-mode: lax`. The deny entry `pkg: github.com/coder/websocket` stays, and its `desc` becomes `only internal/agent and internal/server may import coder/websocket (ADR-0012)`.
- `.golangci.yml`, `deploy-deps`: delete the `github.com/caddyserver/caddy/v2` deny entry (both lines). The comment becomes "internal/deploy never imports internal/proxy, which links Caddy (docs/specs/release.md "Proxy config", ADR-0011). The agent pushes proxy config over the admin socket. proxy-deps denies Caddy itself." The `moonglass.dev/moonglass/internal/proxy` deny entry and its desc stay unchanged.
- No other file changes and no Go code. The `main` allow list is untouched. The diff should be about 30 lines, under the 60-line limit.

## Tests to write

- None. The ticket's `tests` list is empty and the change is lint configuration, so `testdata/tests.txt` is unchanged.
- Verification instead of tests:
  - `make lint` and `make ci` are green. A grep confirms every current importer is inside the excluded directories: `internal/proxy/proxy.go`, `internal/proxy/counter.go`, `internal/agent/connect.go`, `internal/agent/connect_test.go`, `internal/server/agent.go` and `internal/server/agent_test.go`.
  - Reviewer check, not committed: add `import _ "github.com/coder/websocket"` to a file in `internal/deploy` or `internal/cli`, and `import _ "github.com/caddyserver/caddy/v2"` to a file in `internal/server` or `internal/api`. `make lint` must fail with the new desc text. Revert afterwards.
  - `grep -n 'caddyserver/caddy/v2\|coder/websocket' .golangci.yml` shows only the `main` allow entries plus one deny for each module, in `proxy-deps` and `websocket-deps`.

## Edge cases

- Exclusions too wide: only the directories ADR-0011 and ADR-0012 name are excluded. `!**/internal/agent/**` also covers subpackages such as `internal/agent/disk` and `internal/agent/state`, which is correct because ADR-0012 allows the whole `internal/agent` tree. There is no `cmd/` exclusion, so `cmd/` stays denied as both ADRs require.
- Test files: `$all` includes `_test.go` files, so tests outside the allowed directories are denied too (threat-model §3 B3, B4/B8). The only test importers (`connect_test.go`, `agent_test.go`) are inside the exclusions.
- Glob syntax: `$all` and `!**/<dir>/**` match the form that `huma-deps`, `age-deps`, `pgx-deps` and `policy-deps` already use with the pinned golangci-lint v2. That shows the negation works with this version.
- Duplicate denial: after Caddy is removed from `deploy-deps`, `proxy-deps` (`$all`) still covers `internal/deploy`, and each boundary is enforced by one rule.

## Risks

- If a package outside the allowed directories already imported either module, lint would turn red. The grep shows none, so `make lint` stays green.
- Changing the desc strings breaks nothing, because no test or script matches them.
- Scope: only `.golangci.yml` (a protected path named in `allowed_paths`) and the ticket's own docs change. ADR-0011, ADR-0012 and `docs/specs/proxy.md` keep their directory lists, which stay true; editing them is out of scope.
- No contract, ADR, module or open security decision is involved, so the verdict is ready.
