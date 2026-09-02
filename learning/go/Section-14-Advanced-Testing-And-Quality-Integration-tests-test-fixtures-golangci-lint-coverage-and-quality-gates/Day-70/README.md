# Day 70 — Todo API: the quality suite

Section 14 capstone. The same small service as the rest of the section, with
the testing and quality tooling a team would actually run.

```bash
make help        # every target below
make ci          # exactly what the pipeline runs
```

## Which tests exist, and when to run them

| Suite | Command | Runtime | What it protects |
|---|---|---|---|
| Unit | `make test` | < 1s | Service rules: validation, ownership, the clock-dependent branches |
| HTTP | `make test` | < 1s | Auth middleware, status codes, DTO shape (`httptest` handler, no socket) |
| Integration | `make integration` | ~2s | Real server, real client, real concurrency (`-tags=integration`) |
| Flake hunt | `make flakes` | ~20s | Order dependence and timing flakes (`-count=20 -shuffle=on -race`) |
| Quarantine | `make quarantine` | ~1s | Known-flaky tests, excluded from CI (`-tags=flaky`) |

**Run `make test` on every save. Run `make ci` before you push.**

The split is by *build tag*, not by directory:

```go
//go:build integration
```

as the first line of a file, followed by a blank line. `go test ./...` skips
it; `go test -tags=integration ./...` includes it. The fast suite stays fast,
and nothing is hidden from CI.

## Linting

`.golangci.yml` is checked in, so a developer's run and CI's run apply the same
rules:

```bash
make lint                # golangci-lint run --timeout=5m
golangci-lint run --fix  # apply the fixes it can make itself
```

Enabled: `govet`, `staticcheck`, `errcheck`, `ineffassign`, `unused`,
`bodyclose`, `rowserrcheck`, `noctx`, `errorlint`, `copyloopvar`, `misspell`.
Day 68 explains what each one catches.

Two rules about suppression:

- Fix the finding. `//nolint` needs a reason on the same line, and reviewers
  should push back on it.
- Anything disabled in `.golangci.yml` carries a written reason. This repo
  disables `ST1000` (per-day packages do not need package comments) and
  `fieldalignment` (it fights readability outside hot paths).

## Coverage

```bash
make cover        # profile + the total
make cover-html   # the visual report: red lines are untested
make gate         # enforce coverage-policy.json, exit 1 on a miss
```

Thresholds (`coverage-policy.json`), chosen by blast radius rather than by
round numbers:

| Package | Minimum | Why |
|---|---|---|
| `internal/todo` | 80% | Ownership and validation live here |
| `internal/httpapi` | 70% | Auth middleware and status mapping |
| Overall (gated packages) | 70% | Ratchet target, raised deliberately |
| `cmd/api` | ignored | Wiring; the integration suite exercises it |
| `internal/testsupport` | ignored | Test helpers, covered by being used |

Coverage measures **statements executed**, not branches and not assertions. It
is good at two things: finding untested branches, and finding dead code. It
cannot tell you whether a test asserts anything — see Day 69.

## Flaky tests

The policy on this project, in order of preference:

1. **Fix the code.** `Service.List` used to sort by `CreatedAt` alone. Two
   tasks created in the same tick tied, and Go's randomised map iteration
   picked the order — the test passed locally and failed in CI about one run in
   three. The fix was a tie-break on ID, and
   `TestListOrderIsDeterministic` (50 iterations) now guards it.
2. **Fix the test**, when the code is right and the assertion was too strict.
3. **Quarantine it**, with an owner and an issue number, so it stops eroding
   trust while somebody works on it — `internal/todo/quarantine_test.go`,
   behind `//go:build flaky`.

Never: add a retry, add a sleep, or delete the assertion. All three keep the
bug and remove the warning.

To reproduce the quarantined flake:

```bash
make quarantine     # go test -tags=flaky -count=5 ./...
```

It fails perhaps two runs in three, because it sleeps and hopes rather than
waiting for a signal. The fix is written in the comment above it.

A quarantine is a promise: one sprint, then fixed or deleted.

## CI

`.github/workflows/quality.yml` runs, in order:

1. `gofmt -l .` — formatting is not a review conversation
2. `golangci-lint`
3. `govulncheck` — known vulnerabilities in reachable code (Day 54)
4. `go test -race` — unit
5. `go test -tags=integration -race` — integration
6. coverage + the gate

Every step is a `make` target, so a red pipeline can be reproduced locally in
one command. A pipeline that cannot be reproduced is a pipeline people learn to
ignore.

## Running the service

```bash
go run ./cmd/api      # :8080; tokens ada-token and alan-token

curl -XPOST localhost:8080/tasks -H 'Authorization: Bearer ada-token' \
  -d '{"title":"write the README"}'
curl localhost:8080/tasks -H 'Authorization: Bearer ada-token'
curl -XPOST localhost:8080/tasks/1/complete -H 'Authorization: Bearer ada-token'
curl localhost:8080/tasks/overdue -H 'Authorization: Bearer ada-token'
```
