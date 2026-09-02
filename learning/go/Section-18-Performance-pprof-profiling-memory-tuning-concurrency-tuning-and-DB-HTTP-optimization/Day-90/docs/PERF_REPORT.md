# Day 90 — Performance report: `GET /dashboard`

One complete optimization cycle on the MVP, with the numbers that justified
each step. Measured by `go run ./Day-90` on an Apple M4 (10 cores), Go 1.26.5.

**Setup.** 600 customers, 7,200 orders, page size 50, 240 requests from 8
concurrent clients. The database is SQLite in-process with a **simulated 0.3 ms
round trip per query** — without it, N+1 costs microseconds and looks free right
up until production, where the database is a network hop away.

## The bottleneck

Baseline `v1`: **51 queries per request**, p95 **49.7 ms**, 204 req/s.

The CPU profile, focused on our own packages:

```
      flat  flat%        cum   cum%
         0     0%      1.49s 29.98%  internal/api.(*API).dashboard
         0     0%      1.44s 28.97%  internal/store.(*Store).DashboardNPlusOne
         0     0%      1.32s 26.56%  database/sql.withLock
```

Every `flat` is **zero**. The handler is not computing — it is *waiting*, and
waiting never appears in a CPU profile. The number that explains the latency is
not in the profile at all: 51 round trips × 0.3 ms = **15 ms of pure waiting**,
before the database does a single useful thing.

This is the day's main lesson. A CPU profile answers "where is the CPU going?"
When the answer is "nowhere", the question was wrong, and the query count is
what to look at.

The heap profile *does* name its callers — `json.Encoder.Encode` →
`bytes.growSlice` — which is where round 4 came from.

## Four rounds, measured after each

| version | queries/req | p50 | p95 | p99 | req/s |
|---|---|---|---|---|---|
| v1 — N+1, no index | 51 | 37.2 ms | 49.7 ms | 52.6 ms | 204 |
| v2 — N+1, **indexed** | 51 | 18.3 ms | 18.9 ms | 19.1 ms | 436 |
| v3 — **single JOIN** | **1** | 2.00 ms | 2.70 ms | 2.97 ms | 3,797 |
| v4 — JOIN + **preallocated** | 1 | 1.87 ms | 2.64 ms | 2.83 ms | 4,078 |

**p95: 49.7 ms → 2.64 ms (18.9×). Throughput: 204 → 4,078 req/s (20×).**

Round by round:

- **Round 2 — the index: 2.4×.** A migration; not one line of Go changed. The
  plan went from `SCAN orders` to
  `SEARCH orders USING INDEX idx_orders_customer (customer_id=?)`.
- **Round 3 — killing the N+1: another 6.8×.** This was the real bottleneck.
  51 round trips became 1.
- **Round 4 — preallocation: 1.00× end to end**, which is *noise*. A load test
  cannot resolve a change this small. The benchmark can:
  `BenchmarkDashboardV3` 1,933 allocs/op → `BenchmarkDashboardV4` **1,868**,
  stable across three runs. Use the tool whose resolution matches the change.

Measuring after **each** round is what makes this table possible. Ship all four
together and you cannot tell which one paid — and the temptation is always to
credit the clever change rather than the boring one.

## The optimization that was reverted

Round 4 originally also marshalled the response into a `sync.Pool` buffer and
wrote it in one call. It sounded right. The benchmark disagreed:

| | B/op | allocs/op |
|---|---|---|
| plain `json.NewEncoder(w).Encode` | 63,843 | 1,933 |
| pooled buffer + `json.Marshal` | **84,438** | 1,870 |

`json.Marshal` already builds the whole document in its own internal pool, so
copying that into a second pooled buffer added **a full extra copy of the
response** — 20 KB per request more, for 63 fewer allocations. It was removed;
the note lives in `api.write`'s doc comment so nobody re-adds it.

This is what "optimize with evidence, not instinct" costs and buys. The change
was plausible, the measurement said no, and the measurement wins.

## Regression guards

Three layers, from tightest to loosest:

1. **Query count — exact, and asserted in unit tests.**
   `TestHotPathIssuesOneQueryPerRequest` fails if `v3`/`v4` ever issue more than
   one query. It does not vary with the machine, the load or the scheduler, so
   it can be asserted *exactly* — an N+1 sneaking back fails in CI in
   milliseconds. `TestBaselineIssuesNPlusOneQueries` asserts the old code
   *does* issue `limit+1`, so the guard is known to be testing something real.
2. **Query plan.** `TestIndexIsUsedByTheHotQuery` asserts `EXPLAIN` produces
   `SEARCH`, not `SCAN`. An index dropped in a migration fails here.
3. **`perf.Budget` — latency and throughput, set at ~3× the measured value.**

```
budget: p95 < 8ms, queries/request <= 2, throughput > 1301 req/s
v4 passes.

the same budget applied to v1:
  p95 49.739ms exceeds 7.853ms
  51.0 queries per request exceeds 2.0
  204 req/s is below 1301
```

The thresholds are deliberately generous. The gate exists to catch
order-of-magnitude regressions — an N+1 restored, an index dropped — not a 5%
drift. **A gate that fails on noise gets disabled within a month, and then it
protects nothing.**

## Correctness first

`TestAllVersionsReturnTheSamePage` compares all four implementations row by row,
including the computed totals. Without it, "faster" and "wrong" are
indistinguishable from the benchmark's point of view.

## What to carry forward

1. Load test with a fixed request **count**, not a fixed duration — comparing
   two versions needs the same amount of work.
2. Profile **under load**, and read `-cum -focus=<your package>`; the flat top
   of a Go profile is the scheduler.
3. When every `flat` is zero, the service is waiting. Count round trips.
4. Fix one thing per round and re-measure.
5. Report **p95/p99**, never the mean — the mean hides the tail, and the tail is
   what users feel.
6. Guard the exact thing (query count) tightly and the noisy thing (latency)
   loosely.
