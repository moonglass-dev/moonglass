SHELL := /bin/bash

VERSION := $(shell git describe --tags --always --dirty)
COMMIT  := $(shell git rev-parse HEAD)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

GO_BUILD := CGO_ENABLED=0 go build -trimpath \
	-ldflags '-X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)'

# Prints every test as "package TestName", sorted.
LIST_TESTS := set -o pipefail; go test -list . ./... | awk '\
	/^(Test|Fuzz|Example)/ { names[n++] = $$1; next } \
	$$1 == "ok" { for (i = 0; i < n; i++) print $$2, names[i]; n = 0 }' | LC_ALL=C sort

.PHONY: build build-all fmt fmt-check lint test test-one test-integration test-harness race test-list check-test-list ci
.NOTPARALLEL:

build:
	$(GO_BUILD) -o bin/moonglass ./cmd/moonglass

build-all:
	GOOS=linux GOARCH=amd64 $(GO_BUILD) -o bin/moonglass-linux-amd64 ./cmd/moonglass
	GOOS=linux GOARCH=arm64 $(GO_BUILD) -o bin/moonglass-linux-arm64 ./cmd/moonglass

fmt:
	golangci-lint fmt

fmt-check:
	golangci-lint fmt --diff

lint:
	golangci-lint run

test:
	go test -count=1 -shuffle=on ./...

test-one:
	@test -n '$(PKG)' && test -n '$(NAME)' || { echo 'usage: make test-one PKG=./cmd/moonglass NAME=TestVersionPrintsVersionCommitAndDate' >&2; exit 2; }
	@printf '%s' '$(NAME)' | grep -Eq '^[A-Za-z0-9_]+$$' || { echo 'NAME must be a Go test identifier' >&2; exit 2; }
	@printf '%s' '$(PKG)' | grep -Eq '^\./[A-Za-z0-9_./-]*$$' || { echo 'PKG must be a relative package path starting with ./' >&2; exit 2; }
	go test -count=1 -run '^$(NAME)$$' '$(PKG)'

test-harness:
	bash scripts/test-ticket

test-integration:
	@test -n '$(PKG)' || { echo 'usage: make test-integration PKG=./internal/deploy' >&2; exit 2; }
	@printf '%s' '$(PKG)' | grep -Eq '^\./[A-Za-z0-9_./-]*$$' || { echo 'PKG must be a relative package path starting with ./' >&2; exit 2; }
	go test -count=1 -tags integration '$(PKG)'

# -race needs cgo; the binaries built by build and build-all stay static.
race:
	CGO_ENABLED=1 go test -count=1 -shuffle=on -race ./...

test-list:
	$(LIST_TESTS) > testdata/tests.txt

check-test-list:
	@$(LIST_TESTS) | diff -u testdata/tests.txt - || { echo 'testdata/tests.txt is out of date: run "make test-list" and commit the result' >&2; exit 1; }

# Runs each step quietly. A failing step prints its failed test names and its last 60 lines.
ci:
	@for step in fmt-check lint test race check-test-list build-all; do \
		out=$$(mktemp); \
		if $(MAKE) --no-print-directory $$step >"$$out" 2>&1; then \
			echo "ok   $$step"; rm -f "$$out"; \
		else \
			echo "FAIL $$step"; \
			grep -Eo -- '--- FAIL: [^ ]+' "$$out" | sort -u | head -n 20; \
			tail -n 60 "$$out"; rm -f "$$out"; exit 1; \
		fi; \
	done
