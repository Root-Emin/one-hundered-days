# Everything CI does, runnable locally with the same flags.
#
# A pipeline that cannot be reproduced on a laptop is a pipeline people learn
# to distrust.

MODULE      := example.com/onehundredday/Section-16-Containers-And-CI-CD-Docker-multi-stage-builds-GitHub-Actions-and-Go-CI-pipelines/Day-80
BINARY      := api
DIST        := dist
IMAGE       ?= day80-api
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_TIME  ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS     := -s -w \
  -X $(MODULE)/internal/buildinfo.Version=$(VERSION) \
  -X $(MODULE)/internal/buildinfo.Commit=$(COMMIT) \
  -X $(MODULE)/internal/buildinfo.BuildTime=$(BUILD_TIME)
PLATFORMS   := linux/amd64 linux/arm64 darwin/arm64

.DEFAULT_GOAL := help

.PHONY: help
help:
	@echo "make run            run locally"
	@echo "make build          build for this machine"
	@echo "make release        build every platform into $(DIST)/ with checksums"
	@echo "make test           go test -race"
	@echo "make lint           gofmt, go vet, golangci-lint"
	@echo "make ci             everything the pipeline runs"
	@echo "make docker-build   build the container image"
	@echo "make docker-run     run the image"
	@echo "make compose-up     the whole stack (api + prometheus)"
	@echo "make tag VERSION=v0.1.0   create and push a release tag"

.PHONY: run
run:
	go run ./cmd/api

.PHONY: build
build:
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o $(DIST)/$(BINARY) ./cmd/api
	@echo "built $(DIST)/$(BINARY) version=$(VERSION) commit=$(COMMIT)"

# The same binaries CI attaches to a release, produced the same way - so a
# developer can reproduce exactly what shipped.
.PHONY: release
release: clean
	@mkdir -p $(DIST)
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		echo "building $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath \
			-ldflags="$(LDFLAGS)" \
			-o $(DIST)/$(BINARY)-$$os-$$arch ./cmd/api || exit 1; \
	done
	@cd $(DIST) && shasum -a 256 * > checksums.txt 2>/dev/null || sha256sum * > checksums.txt
	@ls -lh $(DIST)

.PHONY: test
test:
	go test -race -shuffle=on -count=1 ./...

.PHONY: lint
lint:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then echo "not gofmt'd:"; echo "$$unformatted"; exit 1; fi
	go vet ./...
	@command -v golangci-lint >/dev/null 2>&1 && golangci-lint run --timeout=5m || \
		echo "golangci-lint not installed; skipping"

.PHONY: ci
ci: lint test build
	@echo "CI checks passed"

.PHONY: docker-build
docker-build:
	cd ../.. && docker build \
		-f Section-16-Containers-And-CI-CD-Docker-multi-stage-builds-GitHub-Actions-and-Go-CI-pipelines/Day-80/Dockerfile \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_TIME=$(BUILD_TIME) \
		-t $(IMAGE):$(VERSION) \
		-t $(IMAGE):latest .
	@docker images $(IMAGE)

.PHONY: docker-run
docker-run:
	docker run --rm -p 8080:8080 \
		-e LOG_FORMAT=text \
		--name day80-api \
		$(IMAGE):$(VERSION)

.PHONY: compose-up
compose-up:
	cd ../.. && docker compose -f Section-16-Containers-And-CI-CD-Docker-multi-stage-builds-GitHub-Actions-and-Go-CI-pipelines/Day-80/docker-compose.yml up --build

.PHONY: compose-down
compose-down:
	cd ../.. && docker compose -f Section-16-Containers-And-CI-CD-Docker-multi-stage-builds-GitHub-Actions-and-Go-CI-pipelines/Day-80/docker-compose.yml down -v

# Tagging is the release trigger: pushing a v* tag starts the release workflow.
.PHONY: tag
tag:
	@if [ "$(VERSION)" = "dev" ]; then echo "set VERSION, e.g. make tag VERSION=v0.1.0"; exit 1; fi
	@echo "tagging $(VERSION)"
	git tag -a $(VERSION) -m "release $(VERSION)"
	@echo "now: git push origin $(VERSION)"

.PHONY: clean
clean:
	rm -rf $(DIST) coverage.out
