# Executor.Run's start-up Reconcile removes every dev.moonglass.managed container on the Engine that the suite's fresh agent DB does not know, and test/deploy cannot narrow it (Executor.scope is unexported and deploy.go is outside allowed_paths). Decide either to add an exported reconcile scope to internal/deploy (add internal/deploy/deploy.go and docs/specs/release.md to W2-7's allowed_paths) or to drop \"runs Executor.Run for the whole test\" from docs/specs/deploy-suite.md and W2-7.

Blocked at status: todo
