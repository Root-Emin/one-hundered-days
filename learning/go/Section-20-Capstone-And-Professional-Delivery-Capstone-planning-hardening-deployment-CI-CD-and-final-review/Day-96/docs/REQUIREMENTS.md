# Linkr — requirements

A link shortener with click analytics. Five days, one service, built in the
order that keeps it deployable at the end of every day.

## Why this scope

It is small enough to finish and wide enough to exercise everything the course
covered: a hot read path that must be fast, a write path that must be durable,
work that must happen out of band, and an operator who has to run it.

Depth over sprawl. Three endpoints done properly — cached, rate-limited,
observable, tested — is a better answer to "show me your work" than ten
half-endpoints.

## What it does

| # | Story | Priority |
|---|---|---|
| 1 | A client with an API key creates a short link for a URL | must |
| 2 | Anyone following a short link is redirected to the target | must |
| 3 | The owner sees how many times a link was followed, by day | must |
| 4 | The owner lists and deactivates their links | must |
| 5 | A deactivated link returns 410 Gone, not 404 | must |
| 6 | Links may carry an expiry | should |
| 7 | The owner sees referrer breakdown | could — cut first if time runs out |

## Endpoints

| Method | Path | Auth | Notes |
|---|---|---|---|
| `POST` | `/api/links` | API key | creates a code, or accepts a requested one |
| `GET` | `/api/links` | API key | the caller's links, newest first |
| `GET` | `/api/links/{code}` | API key | one link with its totals |
| `DELETE` | `/api/links/{code}` | API key | deactivates; the code is never reused |
| `GET` | `/api/links/{code}/stats` | API key | clicks per day |
| `GET` | `/{code}` | none | **the hot path**: 302 to the target |
| `GET` | `/healthz` | none | liveness: the process is up |
| `GET` | `/readyz` | none | readiness: dependencies are usable |
| `GET` | `/metrics` | none, bound to localhost | Prometheus |

## Service levels

These are targets with numbers, because "fast" is not a requirement and cannot
be tested.

| Property | Target | Measured by |
|---|---|---|
| Redirect latency | p95 < 5 ms, p99 < 20 ms (cache hit) | the load test on Day 99 |
| Redirect availability | the redirect works while the database is unavailable, for cached links | a test that closes the database |
| API latency | p95 < 50 ms | the load test |
| Click loss | at most the events in flight; a click already accepted is never lost | the outbox test |
| Startup | ready in < 2 s, including migrations | measured on Day 99 |

**The redirect is the endpoint that matters.** It is the one a human waits for,
it is 99% of the traffic, and it is the only one whose failure is visible to
someone who never signed up.

## Data

```
links        code (PK), owner, target_url, active, expires_at, created_at
clicks       id, code, occurred_at, referrer, user_agent_family
click_daily  code, day, count            (the aggregate the stats endpoint reads)
api_keys     id, owner, hash, created_at, last_used_at
outbox       id, event_id, type, payload, published_at, attempts
```

`click_daily` exists so the stats endpoint never scans `clicks`. Aggregation
happens in the worker, not in the request.

## Auth

API keys, not passwords: the clients are programs. The key is shown once at
creation and stored as a SHA-256 hash — a stolen database gives an attacker
hashes, not keys.

Every `/api/*` route requires a key. `/{code}` requires none, and must not: it
is a public redirect.

## Async

A click must not slow the redirect down, and must not be lost if the worker is
down. So the redirect writes an event to the outbox in the same transaction it
uses for nothing else — the redirect itself is a read — and a worker aggregates
into `click_daily`.

At-least-once delivery, idempotent consumer, keyed by event id. Day 84's
pattern, applied.

## Non-goals

Written down so they are decisions rather than omissions:

- **No custom domains.** One host, one namespace.
- **No user accounts or a UI.** API keys and curl.
- **No link editing.** A link's target is immutable; changing it silently
  changes what a shared link does, which is a phishing primitive.
- **No global rate limit tuning.** A fixed per-key limit, with the mechanism in
  place to change it.
- **No multi-region.** One instance, one database file.

## Definition of done

- [ ] Every endpoint above, with its tests
- [ ] Migrations run on startup, and roll back
- [ ] Redirect p95 measured, not assumed
- [ ] Structured logs, metrics, and a readiness probe that means something
- [ ] A Dockerfile that builds a non-root image, and a CI pipeline that runs
      what `make check` runs
- [ ] `docs/` a new engineer could start from
