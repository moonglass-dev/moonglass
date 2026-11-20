No spec says how the deploy suite's real `moonglass proxy` serves the pushed `listen: [":443"]` once per test run, without root and with no fixed port.

`docs/specs/deploy-suite.md` says the suite drives `internal/deploy` and a real `moonglass proxy` against a real Engine, and puts load on the routes. `docs/specs/release.md` ("Proxy config", "The document") says `Proxy.Push` always writes the server `moonglass` with `listen` `[":443"]`, and the code matches (`internal/deploy/proxy.go`, `push`). There is no override. A test-run proxy on the host would therefore have to bind :443 itself. That fails for a non-root test user on Linux (CI runs as `runner`). It also breaks the rule in AGENTS.md and build plan §1 that tests take ports from `:0` and can run in two worktrees at once. `docs/specs/proxy.md` only covers the proxy's own tests, which push their own `127.0.0.1:<port>` listen. W2-7's `allowed_paths` do not reach `internal/deploy`.

The owner has to choose one of these, and `docs/specs/deploy-suite.md` has to record it before this ticket can name testable acceptance criteria:
1. `deploy.ProxyConfig` gets a listen address, `:443` in production and `127.0.0.1:0`-chosen in tests. This changes `docs/specs/release.md` "The document" and `internal/deploy`, so W2-7's `allowed_paths` must include `internal/deploy/` and `docs/specs/release.md`.
2. The suite runs `moonglass proxy` in a test container with its own network namespace, joined to `mg-<project>`, with 443 published on a `:0` host port and the admin socket shared through a temp-directory bind mount. The spec then has to say how that container's image is built and imported, and that it is labelled and removed per run.
3. Something else the owner names.

Secondary point for the same spec edit: say what "a rollback under 5 seconds" measures. For example, it could be from calling `Executor.Rollback` until it returns SUCCEEDED, or until the load generator first gets a 2xx answer from the restored release.

Blocked at status: todo
