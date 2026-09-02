# Linkr — milestones

Five days, and something deployable at the end of each. The order is chosen so
that if a day is lost, what exists still works.

Each day lives in its own directory (`Day-99` … `Day-100`) and carries the
whole service forward, so every day is runnable on its own — the way a tagged
release is.

## Day 96 — plan and walking skeleton

**Deliverable:** the service starts, answers `/healthz` and `/readyz`, and
shuts down cleanly. No links yet.

- [x] Requirements, with numbers for the service levels
- [x] Architecture and four ADRs
- [x] `internal/domain`: `Link`, `Code`, followability, validation — no I/O
- [x] `internal/config`: environment + flags, validated at startup
- [x] `internal/httpserver`: routing, middleware chain, graceful shutdown
- [x] Tests for all of it

Why a walking skeleton first: the wiring is where the surprises are — signals,
timeouts, config, shutdown. Finding them on day one is cheap; finding them on
day five is a lost day.

## Day 97 — the core features ✅

**Deliverable:** create a link, follow it, list them. The product works.

- [x] Migrations and the store: `links`, `api_keys`, `clicks`, `outbox`
- [x] API-key auth middleware
- [x] `POST /api/links`, `GET /api/links`, `GET /api/links/{code}`, `DELETE`
- [x] `GET /{code}` — the redirect, uncached for now
- [x] Integration tests through the real HTTP surface

## Day 98 — hardening and observability ✅

**Deliverable:** it survives being used, and you can see what it is doing.

- [x] The redirect cache — p95 **475 µs** against a 5 ms promise, 99.8% hit ratio
- [x] Per-key rate limiting, 429 with `Retry-After`
- [x] Structured logs with a request id; no secrets in them
- [x] Prometheus metrics: rate, errors, duration, cache hit ratio, outbox depth
- [x] `/readyz` that actually checks the database
- [x] The click worker: outbox → `click_daily`, idempotent **and batched** —
      the load test caught it draining 200/s against 66,000/s
- [x] `GET /api/links/{code}/stats`
- [x] Security review (`docs/SECURITY.md`) and `govulncheck`

## Day 99 — deployment and CI/CD ✅

**Deliverable:** an image someone else can run, built by a pipeline.

- [x] Multi-stage Dockerfile: distroless, non-root, Go pinned to the **patched**
      1.26.6, reproducible flags
- [x] `docker-compose.yml` for a production-like stack (volume, read-only
      rootfs, dropped capabilities, unpublished metrics port)
- [x] CI: fmt, vet, race, lint, **govulncheck**, image (pushed only from main),
      and the deployment-artefact tests
- [x] Release workflow: tag → verify again → cross-compile → checksums → image
- [x] `docs/RUNBOOK.md`: variables, deploy, verify, roll back, restore
- [x] A production-like run of the release binary: **p95 481 µs**, 71,416 req/s,
      graceful shutdown verified on SIGTERM
- [ ] `docker build` itself — no daemon on this machine; the artefacts are
      asserted by `deploy/deploy_test.go` instead

## Day 100 — final review

**Deliverable:** the story, told honestly.

- Self-review with the Day 91 checklist and Day 95's tools
- The gaps, with triggers
- What the 100 days actually covered, and what they did not
- A README a stranger can start from

## Scope control

Two rules, decided now so they are not negotiated at 11pm on Day 99:

1. **Story 7 (referrer breakdown) is cut first.** It is the only "could".
2. **If a day runs long, the tests are not what gets dropped.** The feature is.
   A half-finished feature with tests is a milestone; a finished feature
   without them is a liability.
