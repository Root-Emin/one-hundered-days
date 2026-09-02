# Every command a contributor needs. Run make from this directory.
#
# If a step appears in the README as a shell command, it belongs here instead:
# tribal knowledge that lives in someone's terminal history is knowledge the
# next person has to ask for.

BINARY  := tasks-api
DIST    := dist
ADDR    ?= :8095
GO      ?= go
LINTER  := golangci-lint

# Pinned: "latest" means a tool that changes under you, and a lint failure
# nobody else can reproduce.
GOLANGCI_VERSION := v2.6.2

.DEFAULT_GOAL := help

.PHONY: help setup tools run build test test-race cover lint fmt vet check ci audit review release-notes hooks clean

## help: print this help
help:
	@echo "$(BINARY) - available targets:"
	@echo
	@grep -hE '^## [a-zA-Z_-]+:' $(MAKEFILE_LIST) | sed 's/## //' | awk -F': ' '{printf "  %-14s %s\n", $$1, $$2}'
	@echo
	@echo "one-command setup:  make setup"

## setup: bootstrap a fresh clone
setup: tools
	$(GO) mod download
	@echo
	@echo "ready. 'make run' starts the service on $(ADDR)."

## tools: install the pinned developer tools
tools:
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)

## run: start the service
run:
	TASKS_ADDR=$(ADDR) $(GO) run ./cmd/api

## build: compile the binary into dist/
build:
	mkdir -p $(DIST)
	$(GO) build -trimpath -o $(DIST)/$(BINARY) ./cmd/api

## test: run the fast tests
test:
	$(GO) test -count=1 ./...

## test-race: run the tests under the race detector
test-race:
	$(GO) test -race -count=1 ./...

## cover: run the tests and report total coverage
cover:
	$(GO) test -count=1 -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out | tail -1

## lint: run golangci-lint
lint:
	@command -v $(LINTER) >/dev/null 2>&1 || { echo "$(LINTER) is not installed; run 'make tools'"; exit 1; }
	$(LINTER) run ./...

## fmt: format the code
fmt:
	gofmt -w -s .

## vet: run go vet
vet:
	$(GO) vet ./...

## audit: check the repository's own documentation and conventions
audit:
	$(GO) run ./cmd/repocheck

## review: run the review checklist over this repository's source
review:
	$(GO) run ./cmd/repocheck -review-only

## release-notes: the notes for the next release, from the commits
release-notes:
	@echo "see Day-94: go run ../Day-94/cmd/release -repo ../../../.. notes"

## check: everything CI runs, locally, with the same flags
check: fmt vet lint test-race audit

## ci: what the pipeline runs (no formatting, so a stray gofmt cannot pass CI)
ci: vet lint test-race cover audit

## hooks: install the git hooks in .githooks
hooks:
	cd $(shell git rev-parse --show-toplevel) && git config core.hooksPath learning/go/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-95/.githooks
	@echo "hooks installed; 'git config --unset core.hooksPath' removes them"

## clean: remove build output
clean:
	rm -rf $(DIST) coverage.out
