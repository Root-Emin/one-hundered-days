# Every command a contributor needs, in one place.
#
# The rule this file follows: if a step appears in the README as a shell
# command, it belongs here instead. Tribal knowledge that lives in someone's
# terminal history is knowledge the next person has to ask for.
#
# `make` on its own prints the help, because a Makefile whose targets you have
# to read the source to discover is not documentation.

MODULE     := example.com/onehundredday/Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-93
# Run make from THIS directory. Every path below is relative to it, so the
# targets work the same from a shell, an editor and CI.
DAY        := Section-19-Team-Practices-And-Tooling-Code-review-documentation-Makefile-DX-release-versioning-and-team-habits/Day-93
BINARY     := notes-api
DIST       := dist
DB         ?= notes.db
ADDR       ?= :8093
GO         ?= go
GOFLAGS    ?=
LINTER     := golangci-lint

# Pinned, so every contributor and CI install the same version. "latest" means
# a tool that changes under you, and a lint failure nobody can reproduce.
GOLANGCI_VERSION := v2.6.2

.DEFAULT_GOAL := help

.PHONY: help setup tools doctor run build test test-race cover lint fmt vet check ci migrate migrate-down migrate-status hooks clean

## help: print this help
help:
	@echo "$(BINARY) - available targets:"
	@echo
	@grep -hE '^## [a-zA-Z_-]+:' $(MAKEFILE_LIST) | sed 's/## //' | awk -F': ' '{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'
	@echo
	@echo "one-command setup:  make setup"

## setup: bootstrap a fresh clone - dependencies, tools, database
setup: tools
	$(GO) mod download
	$(MAKE) migrate
	@echo
	@echo "ready. 'make run' starts the service on $(ADDR)."

## tools: install the pinned developer tools
tools:
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)

## doctor: check that this machine can build and run the project
doctor:
	$(GO) run ./cmd/doctor

## run: start the service (applies migrations first)
run:
	NOTES_DB=$(DB) NOTES_ADDR=$(ADDR) $(GO) run ./cmd/api

## build: compile the binary into dist/
build:
	mkdir -p $(DIST)
	$(GO) build $(GOFLAGS) -trimpath -o $(DIST)/$(BINARY) ./cmd/api

## test: run the fast tests
test:
	$(GO) test $(GOFLAGS) -count=1 ./...

## test-race: run the tests under the race detector
test-race:
	$(GO) test $(GOFLAGS) -race -count=1 ./...

## cover: run the tests and report coverage
cover:
	$(GO) test $(GOFLAGS) -count=1 -coverprofile=coverage.out ./...
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

## check: everything CI runs, locally, with the same flags
check: fmt vet lint test-race

## ci: what the pipeline runs (no formatting, so a stray gofmt cannot pass CI)
ci: vet lint test-race cover

## migrate: apply pending migrations
migrate:
	$(GO) run ./cmd/migrate -db $(DB) up

## migrate-down: roll back the most recent migration
migrate-down:
	$(GO) run ./cmd/migrate -db $(DB) down

## migrate-status: show which migrations have been applied
migrate-status:
	$(GO) run ./cmd/migrate -db $(DB) status

## hooks: install the git hooks in .githooks
hooks:
	cd $(shell git rev-parse --show-toplevel) && git config core.hooksPath learning/go/$(DAY)/.githooks
	@echo "hooks installed; 'git config --unset core.hooksPath' removes them"

## clean: remove build output and the local database
clean:
	rm -rf $(DIST) coverage.out $(DB) $(DB)-wal $(DB)-shm
