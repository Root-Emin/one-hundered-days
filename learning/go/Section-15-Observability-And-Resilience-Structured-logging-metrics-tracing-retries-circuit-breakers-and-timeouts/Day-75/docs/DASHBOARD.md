# Dashboard sketch

Panels in the order an engineer reads them during an incident. The rule is
that the top row answers "is something wrong?" in five seconds, and each row
below narrows down "where".

All queries assume the `day75_` namespace and a 5-minute rate window.

## Row 1 — Is something wrong? (the RED metrics)

| Panel | Query | Alert |
|---|---|---|
| **Request rate** (by route) | `sum by (route) (rate(day75_http_requests_total[5m]))` | Traffic dropping to zero is as suspicious as a spike |
| **Error rate** (% of requests) | `sum(rate(day75_http_requests_total{status=~"5xx"}[5m])) / sum(rate(day75_http_requests_total[5m]))` | Page at > 5% for 5 minutes |
| **Latency p50 / p95 / p99** | `histogram_quantile(0.95, sum by (le, route) (rate(day75_http_request_duration_seconds_bucket[5m])))` | Warn when p95 > 500 ms for 10 minutes |
| **Saturation** | `day75_http_requests_in_flight` | Warn when it approaches the worker/connection limit |

Show the error rate as a **ratio**, not a count: 50 errors per second means
nothing without the denominator.

## Row 2 — Which dependency?

| Panel | Query | Reads as |
|---|---|---|
| **Dependency call rate by outcome** | `sum by (dependency, outcome) (rate(day75_dependency_calls_total[5m]))` | `success`, `error`, `breaker_open` side by side |
| **Dependency latency p95** | `histogram_quantile(0.95, sum by (le, dependency) (rate(day75_dependency_duration_seconds_bucket[5m])))` | Which dependency got slow, and when |
| **Circuit breaker state** | `day75_dependency_breaker_state` | 0 closed, 1 half-open, 2 open — as a state timeline, not a number |
| **Retry rate** | `rate(day75_dependency_retries_total[5m])` | Rising retries are the earliest warning: they precede the errors |

Retries are the leading indicator. A dependency usually gets *retried* before
it gets *broken*, so this panel moves first.

## Row 3 — What is the business impact?

| Panel | Query |
|---|---|
| **Orders by outcome** | `sum by (outcome) (rate(day75_orders_processed_total[5m]))` |
| **Payment failure ratio** | `rate(day75_orders_processed_total{outcome="payment_failed"}[5m]) / sum(rate(day75_orders_processed_total[5m]))` |

Technical panels tell you the system is unhealthy. This row tells you whether
customers are feeling it — which is what decides whether to page someone at
3am.

## Row 4 — Is the process itself healthy?

| Panel | Query |
|---|---|
| Goroutines | `go_goroutines` — a monotonic climb is a leak |
| Heap | `go_memstats_heap_inuse_bytes` |
| GC pause | `rate(go_gc_duration_seconds_sum[5m])` |
| Open file descriptors | `process_open_fds` — approaching the limit means dropped connections |

These come free from the Go and process collectors, and they answer "is it the
service or its dependency?" faster than anything else.

## Alerts worth having

Alert on **symptoms**, not causes — page on the error ratio, not on CPU.

| Alert | Condition | Severity |
|---|---|---|
| High error rate | 5xx ratio > 5% for 5m | page |
| Latency regression | p95 > 1s for 10m | page |
| Breaker open | `day75_dependency_breaker_state == 2` for 2m | page |
| Retry storm | retry rate > 20% of request rate for 10m | ticket |
| Traffic disappeared | request rate == 0 for 5m during business hours | page |
| Saturation | in-flight > 80% of capacity for 5m | ticket |

Every alert needs a runbook entry, or it becomes noise people learn to ignore.

## What is deliberately NOT on the dashboard

- **Per-user or per-order panels.** That is what traces and logs are for; as a
  metric label it would explode cardinality (Day 72).
- **Averages.** An average latency of 200 ms hides that 1% of users wait 8
  seconds. Percentiles or nothing.
- **Twenty panels of everything.** A dashboard nobody can read in an incident
  is decoration. Four rows, top to bottom.
