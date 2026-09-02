# Quality targets. Everything CI runs is runnable here, with the same flags -
# a pipeline that cannot be reproduced locally is a pipeline nobody trusts.

GO       ?= go
LINT     ?= golangci-lint
COVERAGE ?= coverage.out

.PHONY: help
help:
	@echo "make test          fast tests (what you run on every save)"
	@echo "make test-race     fast tests with the race detector"
	@echo "make integration   + the tagged end-to-end suite"
	@echo "make flakes        run the suite 20x, shuffled, to hunt flaky tests"
	@echo "make quarantine    run the quarantined tests (expect failures)"
	@echo "make lint          golangci-lint with the checked-in config"
	@echo "make cover         coverage profile + per-function report"
	@echo "make cover-html    open the coverage report in a browser"
	@echo "make gate          enforce coverage-policy.json"
	@echo "make ci            everything CI runs, in the same order"

.PHONY: test
test:
	$(GO) test ./...

.PHONY: test-race
test-race:
	$(GO) test -race -count=1 ./...

.PHONY: integration
integration:
	$(GO) test -tags=integration -race -count=1 ./...

# -shuffle=on randomises test order, which exposes tests that depend on each
# other; -count=20 exposes timing flakes.
.PHONY: flakes
flakes:
	$(GO) test -count=20 -shuffle=on -race ./...

.PHONY: quarantine
quarantine:
	$(GO) test -tags=flaky -count=5 ./... || true

.PHONY: lint
lint:
	$(LINT) run --timeout=5m

.PHONY: cover
cover:
	$(GO) test -tags=integration -covermode=atomic -coverprofile=$(COVERAGE) ./...
	$(GO) tool cover -func=$(COVERAGE) | tail -1

.PHONY: cover-html
cover-html: cover
	$(GO) tool cover -html=$(COVERAGE)

.PHONY: gate
gate: cover
	$(GO) run ../Day-69/cmd/coveragegate -profile $(COVERAGE) -policy coverage-policy.json

.PHONY: ci
ci: lint test-race integration gate
	@echo "CI checks passed"

.PHONY: clean
clean:
	rm -f $(COVERAGE)
