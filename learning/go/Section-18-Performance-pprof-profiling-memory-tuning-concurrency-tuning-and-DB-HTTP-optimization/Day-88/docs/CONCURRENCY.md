# Day 88 — Concurrency tuning

Measured on this machine: Apple M4, `GOMAXPROCS=10`, Go 1.26.5, via
`go run ./Day-88`.

## 1. How many workers? Measure, don't guess

The same pool, the same 240 jobs, two different kinds of work:

**CPU-bound** (hashing, no waiting):

| workers | duration | jobs/sec | vs 1 worker |
|---|---|---|---|
| 1 | 70 ms | 3,428 | 1.0× |
| 2 | 25 ms | 9,512 | 2.8× |
| 5 | 11 ms | 21,624 | 6.3× |
| **10 (GOMAXPROCS)** | 7 ms | 36,766 | **10.7×** |
| 20 | 6 ms | 38,508 | 11.2× |
| 40 | 6 ms | 38,483 | 11.2× |
| 160 | 6 ms | 37,622 | 11.0× — *slower than 40* |

**I/O-bound** (4 ms of waiting per job):

| workers | duration | jobs/sec | vs 1 worker |
|---|---|---|---|
| 1 | 1.074 s | 223 | 1.0× |
| 10 (GOMAXPROCS) | 108 ms | 2,219 | 9.9× |
| 20 | 54 ms | 4,436 | 19.9× |
| 40 | 27 ms | 8,784 | 39.3× |
| 160 | 10 ms | 25,208 | **112.8×** |

The two curves have nothing to do with each other:

- CPU-bound work **plateaus at GOMAXPROCS**. Past that, goroutines are not
  running in parallel — they are taking turns on the same cores, and the extra
  scheduling makes 160 workers slightly *worse* than 40.
- I/O-bound work **keeps scaling**, because a goroutine blocked on a socket
  occupies no core. Its ceiling is set by whatever it is calling — a connection
  pool, a rate limit — not by your CPU count.

Which is why "one goroutine per item" is not a strategy: it is unbounded
concurrency against a bounded dependency, and the outage lands over there.

## 2. Goroutine leaks

A leaked goroutine costs a live stack (8 KB and growing), everything that stack
references, and one more entry for the scheduler to walk. One leak per request
is thousands per hour.

**The classic**, and the one that actually happens:

```go
result := make(chan string)      // unbuffered — the bug
go func() { result <- expensive() }()

select {
case v := <-result: return v
case <-ctx.Done(): return ctx.Err()   // caller leaves; the send blocks forever
}
```

Measured: 50 timed-out calls → **+50 goroutines, permanently**.

The fix is one character — `make(chan string, 1)` — so the send always
completes whether anyone is listening or not. Measured: 50 timed-out calls →
back to baseline.

The other two in `internal/leak`:

| Leak | Fix |
|---|---|
| `range` over a channel the producer never closes | the **sender** closes, with `defer close(out)` |
| a worker looping on a ticker with no `ctx.Done()` | `select` on `ctx.Done()`, and `defer ticker.Stop()` |

## 3. Finding them: the goroutine profile

`runtime.NumGoroutine()` is the cheapest leak detector there is, and worth
exporting as a metric — a count that climbs and never comes back down *is* a
leak, no other symptom needed.

When it climbs, `/debug/pprof/goroutine?debug=1` says which stack is
accumulating:

```
   50 goroutines  internal/leak.LeakyRequest.func1
                  internal/leak/leak.go:42
    1 goroutine   internal/leak.Profile
                  internal/leak/leak.go:222
```

Fifty goroutines sharing one stack, with a file and a line. The frame that
matters is the first one in your own code — runtime frames are never the answer.

In tests, `leak.Settle(before, timeout)` polls until the count comes back down.
Polling, not sleeping: goroutines exit asynchronously, so an assertion made
immediately after a `cancel()` is a flaky test.

## 4. errgroup

`errgroup.Group` is a `WaitGroup` that also carries the first error and cancels
the rest. By hand that is a WaitGroup, a buffered error channel, a
`context.WithCancel`, and the discipline to get all three right every time.

| | measured |
|---|---|
| `SetLimit(4)` over 40 tasks | peak concurrency **4**, wall 56 ms |
| first error cancels siblings | started 4, finished 0, **cancelled-by-sibling 3**, wall **21 ms** instead of the 2 s the slow tasks asked for |
| `Wait()` | returns the **first** non-nil error, not a slice |

`errgroup.WithContext` is the part people skip. Without it, a group whose first
task failed keeps three other tasks burning CPU for a result that is already
lost.

## 5. The rules

- Size the pool by what the work **waits on**, not by how much work there is.
- CPU-bound: `GOMAXPROCS`. I/O-bound: measure, then bound it by the
  dependency's limits, not yours.
- Every send and every receive in a worker gets a `select` on `ctx.Done()`.
- The **sender** closes a channel, exactly once. A `range` over a channel
  nobody closes is a leak by construction.
- Buffer a result channel with capacity 1 when the caller might time out.
- Return a `done` channel from a background worker, so shutdown can *wait* for
  it rather than hope.
- Assert the goroutine count in tests. `internal/leak` tests both that the
  fixed version cleans up **and** that the broken version leaks — otherwise the
  passing test proves nothing.
