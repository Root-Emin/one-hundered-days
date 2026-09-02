# Linkr — Day 99: deployment and CI/CD

The image, the pipeline, the runbook, and a production-like run.

## What was actually run

Docker's daemon is not running on this machine, so this is what is verified and
what is not — stated rather than implied:

| | Status |
|---|---|
| A **release binary** built with the production flags (`CGO_ENABLED=0 -trimpath -buildvcs=false`, commit-time stamping) | ✅ 12 MB, `linkr -version` prints `v1.0.0 (commit 3d60c3c…, built from commit time 2026-09-01T14:59:34+03:00)` |
| A **production-like run**: `LINKR_ENV=production`, JSON logs, loopback metrics, real SQLite volume path | ✅ started, migrated, served |
| **Smoke test** against it: create, redirect, stats, metrics | ✅ |
| **Load test** against it: 4,000 requests, 16 workers | ✅ **p95 481 µs**, 71,416 req/s, 0 failed — the SLO is 5 ms |
| **Graceful shutdown** on `SIGTERM` | ✅ `draining → stopped → click worker stopped → port released` |
| The **Dockerfile, compose file and both workflows** | ✅ parsed and asserted by `deploy/deploy_test.go` |
| `docker build` / `docker compose up` | ⚠️ **not run** — no daemon here. The commands are in the runbook and in CI. |
| Pushing to a registry, rolling back by digest | ⚠️ **not run** — needs a registry and credentials. |

The metrics listener failing to bind (port taken by a leftover process) showed
up in that run as an `ERROR` line while the service kept serving — which is the
behaviour it was written for: **losing metrics is degraded, not down.**

## The image

```dockerfile
FROM golang:1.26.6-alpine AS build      # pinned to the PATCHED version
...
FROM gcr.io/distroless/static-debian12:nonroot
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/linkr"]     # exec form, so SIGTERM reaches the process
```

| Choice | Why |
|---|---|
| **Two stages** | The shipped image has no compiler, no source, no module cache, no shell — a remote code execution needs something to execute. |
| **`1.26.6`, not `1.26`** | Day 98's `govulncheck` found five stdlib vulnerabilities fixed in 1.26.6. A floating tag is how a rebuild silently changes which ones you have. |
| **distroless `:nonroot`** | ~2 MB, and a container escape lands as nobody rather than as uid 0. |
| **Exec-form ENTRYPOINT** | The shell form makes the process a child of `/bin/sh`, which does not forward `SIGTERM` — every deploy becomes a 30-second wait then `SIGKILL`. |
| **`CGO_ENABLED=0 -trimpath -buildvcs=false`, commit time** | The build is reproducible (Day 94): the same source gives the same bytes. |
| **Deps layer before source** | `go.mod` changes rarely; the download layer is a cache hit on nearly every build. |
| **Config by environment** | The same image runs in staging and production. An image that differs between them is not the thing you tested. |

## The pipeline

`ci.yml` — five jobs, each with a timeout, narrowed permissions, and pinned
actions:

```
test ─┐
lint ─┼─→ image   (built always, pushed ONLY from main)
vuln ─┘
deploy-checks     (the artefact tests, no daemon needed)
```

`release.yml` — on a `v*` tag: **verify again** (a tag can be moved), then
cross-compile three platforms with checksums, then build and push the image
with semver tags.

The rules that matter more than the YAML:

- **The image is not built until the code is known good** — `needs: [test, lint,
  vulnerabilities]`. Building first wastes minutes on every failing PR.
- **A fork's pull request cannot push an image.** That is a supply-chain
  compromise with a green checkmark.
- **CI runs `govulncheck`** — the check that found Day 98's five findings.
- Every job has `timeout-minutes`: a hung job runs for six hours and bills for
  all of them.

## Deployment configuration is tested

`deploy/deploy_test.go` parses the artefacts and asserts what matters, without
a Docker daemon:

- non-root, pinned bases, exec-form entrypoint, reproducible build flags
- the database on a **volume** (a container's filesystem is discarded on
  restart — without this every deploy is a data loss incident)
- `stop_grace_period` (45s) **outlives** the shutdown timeout (15s) plus the
  drain delay
- the **metrics port is not published**, and `LINKR_METRICS_ADDR` is loopback
- read-only rootfs, `no-new-privileges`, `cap_drop: ALL`
- every workflow job has a timeout, permissions and pinned actions
- the runbook answers the operator's questions

Two of those tests failed when first written — the compose file relied on the
Dockerfile's default metrics address, and the release workflow set
`CGO_ENABLED` in an `env:` block the test did not read. Both are fixed.

## The runbook

[docs/RUNBOOK.md](docs/RUNBOOK.md): every environment variable, the deploy
sequence, what to watch for one cycle afterwards, a symptom→cause table, backup
and restore.

The section worth reading twice is **rolling back**, because it is the one that
is not simply "run the previous image":

- Roll back by **digest, not tag** — a tag can be moved.
- **No migration** → roll back freely.
- **Additive migration** (new table, nullable column) → roll back freely; the
  old binary ignores what it does not know about. Every Linkr migration so far
  is additive, deliberately.
- **Destructive migration** → do not roll the schema back under a running
  service. `migrate down` is for a maintenance window, not an incident.

Having a rollback script is not the same as it being safe to run at 3am.

Next: [Day 100](../Day-100) — the final review.
