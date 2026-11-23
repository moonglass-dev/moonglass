# Moonglass

Every app on your servers, in one calm place.

Moonglass is an open-source deploy platform for your own servers. It is being built so that git pushes deploy without dropping a request, databases restore with one click, every pull request gets a preview, a coding agent's deploys wait for your approval, and the control plane never SSHes into your servers. Agent and proxy run in under 100 MB. Self-hosted Moonglass is free, with every feature, for any number of people and servers.

**Status: pre-alpha, not usable yet.** This repository holds the plan and the first plumbing. Nothing above works today.

The source is at [moonglass-dev/moonglass](https://github.com/moonglass-dev/moonglass). The module path is `moonglass.dev/moonglass`.

## Build and test

You need Go and golangci-lint. `go.mod` names the Go version, and `.github/workflows/ci.yml` names the golangci-lint version.

```
make build   # bin/moonglass
make test    # every test
make ci      # everything CI runs
```

## Where things are

- [`docs/plan/`](docs/plan/): what is being built, and in what order: the alpha, the launch and v1.
- [`docs/specs/pricing.md`](docs/specs/pricing.md): Free, Cloud and Business.
- [`AGENTS.md`](AGENTS.md): how work is done in this repository.
- [`LICENSE`](LICENSE): Apache-2.0.
