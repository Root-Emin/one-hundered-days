# Day 75 — Operable orders service

Section 15 capstone: the MVP with logs, metrics, traces and resilience wired
together, plus the two documents that make it operable —
[docs/DASHBOARD.md](docs/DASHBOARD.md) and [docs/RUNBOOK.md](docs/RUNBOOK.md).

## Running it

```bash
go run ./cmd/server           # :8080, text logs
go run ./cmd/loadgen          # in another terminal: traffic, failure, recovery
```

`loadgen` walks the service through four phases and prints what each one did:

```
=== HEALTHY BASELINE ===        created=40  failed=0    p95≈17ms   breaker=closed
=== SLOW DEPENDENCY ===         created=26  failed=14   p95≈809ms  breaker=closed
=== PAYMENTS FAILING HARD ===   created=12  unavailable=120        breaker=open
=== RECOVERY ===                created=24  unavailable=176        breaker=closed
```

and the server log shows the state machine doing its work:

```
circuit breaker state change  from=closed    to=open
circuit breaker state change  from=open      to=half-open
circuit breaker state change  from=half-open to=closed
```

## The three pillars, and what each one is for

| Pillar | Answers | Where |
|---|---|---|
| **Metrics** | *Is* something wrong? How many, how fast, how full? | `/metrics`, `internal/observability` |
| **Traces** | *Where* is it wrong — which span, which service? | `X-Trace-Id` on every response |
| **Logs** | *Why* is it wrong — the detail for one event | stdout, structured, with `trace_id` |

They only work together because they share identifiers. Every log line carries
the `trace_id` of its request; every response returns it; every metric is
labelled by a bounded route template.

```bash
# 1. metrics say the error rate jumped
curl -s localhost:8080/metrics | grep day75_http_requests_total

# 2. a failing request hands you a trace id
curl -si localhost:8080/orders -d '{"customer":"probe","cents":100}' | grep -i x-trace-id

# 3. the logs for that trace explain it
go run ./cmd/server 2>&1 | grep <trace_id>
```

## Failure injection

Watching telemetry under failure is the only way to know it works before the
incident:

```bash
curl -XPOST 'localhost:8080/debug/chaos?payment_failure_rate=100'   # break payments
curl -XPOST 'localhost:8080/debug/chaos?slow_rate=50'               # make it slow
curl -XPOST 'localhost:8080/debug/chaos?database_failure_rate=30'   # break the database
curl -XPOST 'localhost:8080/debug/chaos'                            # all back to zero
```

In a real service this lives behind an auth check and a feature flag — but it
does live there. "We have never seen what happens when the payment provider
goes down" is not a plan.

## Resilience, and what each layer does

```
breaker( retry( per-attempt timeout( call ) ) )
```

| Layer | Setting | Why |
|---|---|---|
| Per-attempt timeout | 300 ms | One slow call must not consume the whole retry budget |
| Retry | 3 attempts, 20–200 ms, full jitter | Absorbs transient failures; jitter avoids a synchronised retry storm |
| Breaker | opens at 50% of ≥5 calls in 10s, 3s cooldown, 1 probe | Stops calling a dead dependency, so *this* service survives |

Two behaviours worth understanding before you watch for them:

- **A short burst of failures after a healthy period does not open a
  rolling-window breaker.** The successes still in the window outweigh it.
  That is correct: a blip is not an outage.
- **Retries hide failures from the breaker.** At a 70% per-attempt failure
  rate, three attempts still succeed ~66% of the time, so the breaker stays
  closed. It opens when the failures survive the retries — which is exactly
  when it should.

## Health endpoints

| Endpoint | Question | Depends on |
|---|---|---|
| `/healthz` | Is this process alive? | Nothing — a dependency outage must not restart every pod |
| `/readyz` | Should it receive traffic? | The breaker: 503 while payments are unreachable |

Getting these the wrong way round is a classic incident amplifier: a liveness
probe that checks the database restarts the whole fleet during a database
blip.

## Status codes under failure

| Situation | Status | Reasoning |
|---|---|---|
| Invalid input | 422 | The caller's problem |
| Dependency failed | 502 | We are the gateway; upstream broke |
| Dependency timed out | 504 | Not a 500: nothing here is broken |
| Breaker open | 503 + `Retry-After` | The honest answer: not now, try shortly |

Only genuine internal faults are 5xx-with-no-explanation, which is what keeps
the error-rate alert meaningful.

## Tests

```bash
go test ./...
go test -race -count=1 ./...
```

`internal/httpapi/httpapi_test.go` asserts the things a dashboard and a runbook
depend on: the trace id is present and consistent, a scrape is not counted as
traffic, the breaker opens under a failing dependency and closes after
recovery, `/readyz` turns red while `/healthz` stays green, and every metric
series a dashboard panel queries actually exists.

That last one matters more than it sounds: a renamed metric breaks a dashboard
silently, and you find out during the incident it was built for.
