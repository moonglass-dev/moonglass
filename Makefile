SHELL := /bin/bash

VERSION := $(shell git describe --tags --always --dirty)
COMMIT  := $(shell git rev-parse HEAD)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

PROTOC_VERSION := 36.2
PROTOC_CACHE := $(HOME)/.cache/moonglass/protoc-$(PROTOC_VERSION)
UNAME_S := $(shell uname -s)
UNAME_M := $(shell uname -m)

ifeq ($(UNAME_S),Darwin)
  ifeq ($(UNAME_M),arm64)
    PROTOC_ZIP := protoc-$(PROTOC_VERSION)-osx-aarch_64.zip
    PROTOC_SHA256 := 9cd98a532c5c5e0c4161314de0225de27e4c8a323917b6ea7b1b714d3ae23466
  else ifeq ($(UNAME_M),x86_64)
    PROTOC_ZIP := protoc-$(PROTOC_VERSION)-osx-x86_64.zip
    PROTOC_SHA256 := 228cc7add4616cc14ca5e80dee83209d44449a7aee95a914ae748fa374efb078
  endif
else ifeq ($(UNAME_S),Linux)
  ifeq ($(UNAME_M),x86_64)
    PROTOC_ZIP := protoc-$(PROTOC_VERSION)-linux-x86_64.zip
    PROTOC_SHA256 := 121f6c7afe1d4d0e3ea6aab9432038599250134cbf4474cb1167d2c7decd4278
  else ifeq ($(UNAME_M),aarch64)
    PROTOC_ZIP := protoc-$(PROTOC_VERSION)-linux-aarch_64.zip
    PROTOC_SHA256 := 8b8f18bd2b30346efbc698dd5a73dd7c805f3ef8380f6dfc95c768f3f1852f6a
  endif
endif

ifeq ($(origin PROTOC_ZIP),undefined)
$(error unsupported proto host: $(UNAME_S)/$(UNAME_M); expected Darwin arm64 or x86_64, or Linux x86_64 or aarch64)
endif

PROTOC := $(PROTOC_CACHE)/bin/protoc
PROTOC_GEN_GO := $(PROTOC_CACHE)/bin/protoc-gen-go
PROTO_SRC := proto/moonglass/agent/v1/agent.proto
PROTO_GO := internal/protocol/agentv1/agent.pb.go

GO_BUILD := CGO_ENABLED=0 go build -trimpath \
	-ldflags '-X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)'

# Prints every test as "package TestName", sorted.
LIST_TESTS := set -o pipefail; go test -list . ./... | awk '\
	/^(Test|Fuzz|Example)/ { names[n++] = $$1; next } \
	$$1 == "ok" { for (i = 0; i < n; i++) print $$2, names[i]; n = 0 }' | LC_ALL=C sort

.PHONY: build build-all fmt fmt-check lint test test-one test-integration memory-budget test-list check-test-list test-deploy generate proto proto-tools proto-check mod-check ci
.NOTPARALLEL:

build:
	$(GO_BUILD) -o bin/moonglass ./cmd/moonglass

build-all:
	@set -e; \
		GOOS=linux GOARCH=amd64 $(GO_BUILD) -o bin/moonglass-linux-amd64 ./cmd/moonglass & p1=$$!; \
		GOOS=linux GOARCH=arm64 $(GO_BUILD) -o bin/moonglass-linux-arm64 ./cmd/moonglass & p2=$$!; \
		wait $$p1; \
		wait $$p2

fmt:
	golangci-lint fmt

fmt-check:
	golangci-lint fmt --diff

lint:
	golangci-lint run

# Every test, uncached, shuffled, under the race detector (which needs cgo; the
# binaries built by build and build-all stay static). coverage.out feeds the
# changed-line coverage that scripts/verify records.
test:
	CGO_ENABLED=1 go test -count=1 -shuffle=on -race -coverprofile=coverage.out ./...

test-one:
	@test -n '$(PKG)' && test -n '$(NAME)' || { echo 'usage: make test-one PKG=./cmd/moonglass NAME=TestVersionPrintsVersionCommitAndDate' >&2; exit 2; }
	@printf '%s' '$(NAME)' | grep -Eq '^[A-Za-z0-9_]+$$' || { echo 'NAME must be a Go test identifier' >&2; exit 2; }
	@printf '%s' '$(PKG)' | grep -Eq '^\./[A-Za-z0-9_./-]*$$' || { echo 'PKG must be a relative package path starting with ./' >&2; exit 2; }
	go test -count=1 -run '^$(NAME)$$' '$(PKG)'

# The deploy-correctness suite alone (docs/specs/deploy-suite.md). make test runs
# it too, as part of ./...
test-deploy:
	CGO_ENABLED=1 go test -race -count=1 ./test/deploy

test-integration:
	@test -n '$(PKG)' || { echo 'usage: make test-integration PKG=./internal/deploy' >&2; exit 2; }
	@printf '%s' '$(PKG)' | grep -Eq '^\./[A-Za-z0-9_./-]*$$' || { echo 'PKG must be a relative package path starting with ./' >&2; exit 2; }
	go test -count=1 -tags integration '$(PKG)'

# Fails when idle agent + proxy RSS is above the 100 MiB ceiling (docs/specs/memory.md).
# make test runs the same test; this target runs it alone.
memory-budget:
	go test -count=1 -run '^TestProxyAndAgentIdleUnderBudget$$' ./internal/proxy

test-list:
	$(LIST_TESTS) > testdata/tests.txt

check-test-list:
	@$(LIST_TESTS) | diff -u testdata/tests.txt - || { echo 'testdata/tests.txt is out of date: run "make test-list" and commit the result' >&2; exit 1; }

# Rewrites internal/api/openapi.json and internal/cli/generated.go from the registry.
# make test (and so make ci) fails when either file is stale.
generate:
	go test -count=1 ./internal/api -run '^TestGeneratedOpenAPIIsCurrent$$' -update
	go test -count=1 ./internal/cli -run '^TestGeneratedCLIIsCurrent$$' -update

proto-tools: $(PROTOC) $(PROTOC_GEN_GO)

$(PROTOC):
	@mkdir -p "$(PROTOC_CACHE)"
	@set -euo pipefail; \
		tmp=$$(mktemp -d); \
		trap 'rm -rf "$$tmp"' EXIT; \
		curl -sSL -o "$$tmp/$(PROTOC_ZIP)" "https://github.com/protocolbuffers/protobuf/releases/download/v$(PROTOC_VERSION)/$(PROTOC_ZIP)"; \
		if command -v shasum >/dev/null 2>&1; then \
			echo "$(PROTOC_SHA256)  $$tmp/$(PROTOC_ZIP)" | shasum -a 256 -c -; \
		else \
			echo "$(PROTOC_SHA256)  $$tmp/$(PROTOC_ZIP)" | sha256sum -c -; \
		fi; \
		unzip -q -o "$$tmp/$(PROTOC_ZIP)" -d "$(PROTOC_CACHE)"
	@chmod +x "$(PROTOC)"

$(PROTOC_GEN_GO): go.mod go.sum
	@mkdir -p "$(PROTOC_CACHE)/bin"
	GOBIN="$(PROTOC_CACHE)/bin" go install google.golang.org/protobuf/cmd/protoc-gen-go@$$(go list -m -f '{{.Version}}' google.golang.org/protobuf)

proto: proto-tools
	$(PROTOC) --plugin=protoc-gen-go=$(PROTOC_GEN_GO) \
		--go_out=. --go_opt=module=moonglass.dev/moonglass \
		$(PROTO_SRC)

proto-check: proto
	@git diff --exit-code -- $(PROTO_GO) || { echo "$(PROTO_GO) is stale: run make proto and commit the result" >&2; exit 1; }
	@genver=$$(go list -m -f '{{.Version}}' google.golang.org/protobuf); \
		grep -F "protoc-gen-go $$genver" $(PROTO_GO) >/dev/null || { echo "$(PROTO_GO) was not generated by protoc-gen-go $$genver from go.mod" >&2; exit 1; }

# Fails when go.mod or go.sum differs from what "go mod tidy" writes.
mod-check:
	@go mod tidy -diff || { echo 'go.mod or go.sum is not tidy: run "go mod tidy" and commit the result' >&2; exit 1; }

# Runs each step quietly. A failing step prints its failed test names and its last 60 lines.
# The first phase runs proto-check, fmt-check and mod-check; proto-check rewrites
# generated Go, so it finishes before the compilers start.
# lint, test, check-test-list and build-all then run together: on a cold build
# cache those three compilers are most of the Actions time, and adding them
# up is what made some runs take about 12 minutes.
ci:
	@set -eu; \
	dir=$$(mktemp -d); \
	trap 'rm -rf "$$dir"' EXIT; \
	run() { \
		name="$$1"; \
		out="$$dir/$$name.out"; \
		if $(MAKE) --no-print-directory "$$name" >"$$out" 2>&1; then \
			printf 'ok   %s\n' "$$name" > "$$dir/$$name.status"; \
		else \
			{ \
				printf 'FAIL %s\n' "$$name"; \
				grep -Eo -- '--- FAIL: [^ ]+' "$$out" | sort -u | head -n 20 || true; \
				tail -n 60 "$$out"; \
			} > "$$dir/$$name.status"; \
			return 1; \
		fi; \
	}; \
	run proto-check & p1=$$!; \
	run fmt-check & p2=$$!; \
	run mod-check & p3=$$!; \
	rc=0; \
	wait $$p1 || rc=1; \
	wait $$p2 || rc=1; \
	wait $$p3 || rc=1; \
	cat "$$dir/proto-check.status" "$$dir/fmt-check.status" "$$dir/mod-check.status"; \
	[ "$$rc" -eq 0 ]; \
	run lint & p1=$$!; \
	run test & p2=$$!; \
	run check-test-list & p3=$$!; \
	run build-all & p4=$$!; \
	rc=0; \
	wait $$p1 || rc=1; \
	wait $$p2 || rc=1; \
	wait $$p3 || rc=1; \
	wait $$p4 || rc=1; \
	cat "$$dir/lint.status" "$$dir/test.status" "$$dir/check-test-list.status" "$$dir/build-all.status"; \
	exit $$rc
