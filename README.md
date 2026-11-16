# Moonglass

Calm deploys on your own servers.

Moonglass is an open-source deploy platform for your own servers. It is being built so that deploys don't drop requests, restores are one click, there is nothing to SSH into, and it runs in under 100 MB.

**Status: pre-alpha, not usable yet.** This repository holds the plan and the first plumbing. Nothing above works today.

The code is private at [moonglass-dev/moonglass](https://github.com/moonglass-dev/moonglass) until it moves to a GitHub organization. The module path stays `moonglass.dev/moonglass`.

## Build and test

You need Go and golangci-lint. `go.mod` names the Go version, and `.github/workflows/ci.yml` names the golangci-lint version.

```
make build   # bin/moonglass
make test    # every test
make ci      # everything CI runs
```

## Where things are

- [`docs/plan/`](docs/plan/): what is being built, and in what order.
- [`AGENTS.md`](AGENTS.md): how work is done in this repository.
- [`LICENSE`](LICENSE): Apache-2.0.
