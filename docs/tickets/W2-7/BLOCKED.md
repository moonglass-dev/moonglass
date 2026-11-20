# test/deploy must import github.com/moby/moby/client, which ADR-0014 decision 2 and the .golangci.yml docker-deps rule forbid outside internal/deploy, internal/build and internal/agent/disk; owner must amend ADR-0014 and add .golangci.yml to W2-7's allowed_paths, or rescope how the suite reaches the Engine

Blocked at status: todo
