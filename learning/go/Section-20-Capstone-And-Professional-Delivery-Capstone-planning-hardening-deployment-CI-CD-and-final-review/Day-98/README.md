# Linkr — Day 98: hardening and observability

Features without operability are demos. Today: a cache, a rate limiter,
metrics, the click worker, a security review — and a load test that found a
real bottleneck.

## Run it

```sh
go run ./cmd/linkr -issue-key ada -db /tmp/linkr.db
go run ./cmd/linkr -db /tmp/linkr.db -addr 127.0.0.1:8098 \
    -metrics-addr 127.0.0.1:9098 -base-url http://127.0.0.1:8098

curl 127.0.0.1:9098/metrics          # private listener, localhost only
curl :8098/api/links/golang/stats -H "Authorization: Bearer $KEY"
```

## The load test found something

`cmd/loadsmoke`, 5,000 requests over 16 workers against one hot code:

| | p50 | p95 | p99 | throughput | outbox after |
|---|---|---|---|---|---|
| before | 180 µs | 505 µs | 878 µs | 66,855 req/s | **1,792 pending** |
| after | 160 µs | **475 µs** | 722 µs | 72,855 req/s | **0** |

The redirect met its SLO immediately — **p95 475 µs against a 5 ms promise**,
with a 99.8% cache hit ratio. The *worker* did not: it applied one transaction
per click, and SQLite serialises writers, so it drained ~200 events/second
while the redirect accepted 66,000.

The fix is one idea: **a counter increment is associative.** Group the batch by
`(code, day)` and `+1` five thousand times becomes `+5000` once — five thousand
transactions become one. The events are marked published in the same
transaction, so the worker stays idempotent.

That is what a smoke test is for. Nothing about the redirect's numbers hinted
at it, and it would have shown up in production as stats that lag by hours.

## What today added

```
internal/cache       TTL cache with negative entries and bounded memory
internal/metrics     Prometheus: RED + cache hit ratio + outbox depth
internal/ratelimit   per-key token bucket, 429 with Retry-After
internal/worker      the outbox → click_daily aggregator, batched
cmd/loadsmoke        the smoke test, with the SLO as an exit code
docs/SECURITY.md     the review, including govulncheck's findings
docs/GAPS.md         what is missing, with triggers
```

## Decisions visible in the code

| Choice | Why |
|---|---|
| **Negative cache entries** | A crawler hammering an unknown code would otherwise reach the database every time — a cheap denial of service. |
| **Invalidate *after* the commit** | Deleting first leaves a window where a concurrent reader repopulates from the old row, and the deactivated link keeps working for a full TTL. |
| **`RouteTemplate` before every metric label** | `/abc123` and `/xyz789` both become `/{code}`. A million links must be one series, not a million. |
| **Histogram buckets from 0.5 ms** | The Prometheus defaults start at 5 ms, which would put every good redirect in the first bucket and make the p95 unmeasurable. |
| **Series initialised at zero** | A dashboard querying a never-incremented metric shows "no data", which looks exactly like "the service is down". |
| **Rate limit keyed by owner, after auth** | Keying by IP punishes everyone behind one NAT and does nothing to a client with a thousand addresses. |
| **`/metrics` on a private listener** | It hands an attacker your traffic shape, error rate and deployment size. `config.Validate` refuses a public one in production. |
| **The worker drains until empty, bounded** | One batch per tick is how it fell 1,792 events behind; an unbounded loop is a shutdown that never happens. |

## Security review

`docs/SECURITY.md` covers authentication, authorization, input validation,
secrets in logs, denial of service, exposure and the dependency scan.

`govulncheck` found **five standard-library vulnerabilities, all fixed in Go
1.26.6** — three of them reachable from this service's call graph. The action is
a toolchain upgrade, which is why Day 99 pins the Go version in the Dockerfile
and runs the scan in CI.

Next: [Day 99](../Day-99) — the image, the pipeline, and the release.
