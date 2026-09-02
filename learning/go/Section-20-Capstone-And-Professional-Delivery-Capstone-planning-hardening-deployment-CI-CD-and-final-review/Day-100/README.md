# Linkr

A link shortener with click analytics. Written in Go, over five days, as the
capstone of a hundred-day course — and built the way a service you have to
operate is built rather than the way a demo is.

```sh
go run ./cmd/linkr -issue-key ada -db /tmp/linkr.db     # a key, printed once
go run ./cmd/linkr -db /tmp/linkr.db                     # the service

curl -X POST :8096/api/links -H "Authorization: Bearer $KEY" \
     -d '{"target":"https://go.dev/doc/effective_go"}'
# {"code":"4VPUh4t","short_url":"http://localhost:8096/4VPUh4t",...}

curl -i :8096/4VPUh4t                                    # 302, in ~170 µs
curl :8096/api/links/4VPUh4t/stats -H "Authorization: Bearer $KEY"
```

## Measured, not claimed

Every number here was produced by `cmd/loadsmoke` against the release binary in
its production configuration, on an Apple M4:

| | Promised | Measured |
|---|---|---|
| Redirect p95 | < 5 ms | **481 µs** |
| Redirect p99 | < 20 ms | **713 µs** |
| Throughput | — | **71,416 req/s** |
| Cache hit ratio | — | **99.8%** |
| Failed requests | 0 | **0** |
| Clicks lost | 0 once accepted | **0** — 4,001 of 4,001 aggregated |
| Graceful shutdown | drains, does not cut off | verified on `SIGTERM` |

Coverage: **97% domain, 92% auth, 97% rate limiter, 90% worker, 62% total.**
`go vet`, `golangci-lint` and `go test -race` all clean.

## What it does

| Method | Path | Auth | |
|---|---|---|---|
| `POST` | `/api/links` | API key | create; the code is generated or chosen |
| `GET` | `/api/links` | API key | the caller's links, with click totals |
| `GET` | `/api/links/{code}` | API key | one link |
| `DELETE` | `/api/links/{code}` | API key | deactivate; the code is never reused |
| `GET` | `/api/links/{code}/stats` | API key | clicks per day |
| `GET` | `/{code}` | **none** | the redirect — the hot path |
| `GET` | `/healthz`, `/readyz` | none | liveness, readiness |
| `GET` | `/metrics` | none, **loopback only** | Prometheus |

## How it is built

```
cmd/linkr            wiring: flags, signals, lifecycle
internal/httpserver  transport: routing, middleware, probes
internal/service     use cases: create, follow, list, stats
internal/store       SQLite, embedded migrations, the outbox
internal/domain      the rules — imports nothing from this project
internal/{cache,auth,metrics,ratelimit,worker}
```

**The dependency rule:** `internal/domain` knows nothing about HTTP or SQL. The
test for it — "is this link followable right now?" — must be answerable without
a server and without a database, because that is the question the whole service
exists to answer. It is also why the domain tests run in microseconds and cover
97% of it.

## The decisions worth defending

Each of these is a place where the obvious choice is wrong, and each is
recorded as an ADR or in the architecture:

| Decision | Why |
|---|---|
| **Random base62 codes, not sequential ids** ([ADR 0002](docs/adr/0002-codes.md)) | Sequential ids leak volume *and* everyone else's links — `/abc124` is the link made after `/abc123`, and enumerating the corpus costs one loop. Short links routinely protect documents whose only protection is an unguessable URL. |
| **Clicks aggregated asynchronously** ([ADR 0003](docs/adr/0003-async-clicks.md)) | A human waits for the redirect. Adding a counter update to a read adds the slowest thing in the request to the fastest — and a hot link would serialise every writer on one row. |
| **Hashed API keys, not JWTs** ([ADR 0004](docs/adr/0004-api-keys.md)) | A JWT buys stateless verification, which one service with one database does not need, and costs revocation — a leaked key must stop working *now*. |
| **SQLite** ([ADR 0001](docs/adr/0001-sqlite.md)) | The workload is point lookups by primary key. One file, no server, no pool to size — and every test runs against the real engine in milliseconds. |
| **302, not 301** | A permanent redirect is cached by the browser forever: deactivation stops working for anyone who has visited, and the click is never counted again. |
| **410 for gone, 404 for unknown** | Gone tells a crawler to forget the URL; Not Found invites it back tomorrow. |
| **"Not yours" answers 404** | "This exists but belongs to someone else" is an enumeration oracle. |
| **Only `http`/`https` targets** | A `javascript:` target turns every link into stored XSS **delivered from your domain**. The single most important validation in the service. |
| **The click is written *after* the response, on a detached context** | The request's context is cancelled when the client disconnects, and a user who closes the tab still clicked. |
| **Readiness fails *before* the listener closes** | Traffic drains away while the process can still serve it, instead of the last few requests getting a connection reset. |
| **Liveness ignores dependencies** | A `/healthz` that fails when the database is down gets the container killed — which does not fix the database and does lose the cache keeping redirects alive. |

## The bug the load test found

The redirect met its SLO immediately. The **worker** did not: it applied one
transaction per click, SQLite serialises writers, and it drained ~200
events/second while the redirect accepted 66,000. Six seconds after the load
stopped, 1,792 events were still pending.

The fix is one idea — **a counter increment is associative**. Group the batch by
`(code, day)` and `+1` five thousand times becomes `+5000` once; five thousand
transactions become one, with the events marked published in the same
transaction so the worker stays idempotent. Outbox after the same load: **0**.

Nothing about the redirect's numbers hinted at it. In production it would have
appeared as statistics that lag by hours.

## Operating it

- **[docs/RUNBOOK.md](docs/RUNBOOK.md)** — environment variables, the deploy
  sequence, what to watch afterwards, a symptom→cause table, backup and
  restore, and the part that is not simply "run the previous image": **rolling
  back across a migration**.
- **[docs/SECURITY.md](docs/SECURITY.md)** — authentication, authorization,
  input validation, secrets in logs, denial of service, exposure, and the
  `govulncheck` findings with the action they imply.
- **[docs/GAPS.md](docs/GAPS.md)** — what is missing, each with **why not yet**
  and **what would trigger it**. The trigger column is what stops a gaps list
  being a wish nobody reads.

## Delivery

Multi-stage Dockerfile → distroless, non-root, Go pinned to the **patched**
1.26.6, built reproducibly (`-trimpath -buildvcs=false`, commit-time stamping).

CI: a matrix over two Go versions and two operating systems, `go vet`,
`-race`, `golangci-lint`, **`govulncheck`**, a **smoke job that drives the real
binary** (create → redirect → metrics → `SIGTERM` drain), coverage and smoke
logs as artefacts, an image built always and pushed **only from `main`**, and
one summary check for branch protection.

The Dockerfile, the compose file and both workflows are **asserted by Go
tests** (`deploy/deploy_test.go`) — non-root, pinned bases, exec-form
entrypoint, a volume for the database, an unpublished metrics port, a grace
period that outlives the drain, timeouts on every job. Deployment configuration
is text, and text can be tested without a Docker daemon.

## Skills this demonstrates

HTTP APIs with `net/http` · SQLite and migrations · the outbox pattern and
idempotent consumers · caching with invalidation ordering · API-key
authentication · rate limiting · structured logging, Prometheus metrics and
meaningful probes · graceful shutdown · table-driven and integration testing ·
race detection · load testing against an SLO · containers, CI/CD, semantic
versioning and reproducible builds · and writing all of it down so someone else
can run it.

