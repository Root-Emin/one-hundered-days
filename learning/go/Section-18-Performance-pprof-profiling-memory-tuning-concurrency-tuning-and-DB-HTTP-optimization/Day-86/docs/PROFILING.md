# Day 86 — A profiling session, start to finish

Everything below is a real measurement from this machine (Apple M4, 10 cores,
Go 1.26.5, `go run ./Day-86`). Your numbers will differ; the shape will not.

## 1. Where pprof lives

`net/http/pprof` registers `/debug/pprof/*` on `http.DefaultServeMux` **as an
import side effect**. If the service also serves on `DefaultServeMux`, the
profiler is now public: anyone can dump the heap (an information leak) or start
a 30-second CPU profile on the production box (a denial of service).

So `internal/profiling` registers the handlers on a mux of its own, and
`cmd/server` binds it to `127.0.0.1` on a separate port. You reach it through
an SSH tunnel or `kubectl port-forward`. That friction is the feature.

```
go run ./cmd/server -addr :8086 -pprof 127.0.0.1:6060

go tool pprof -http=: 'http://127.0.0.1:6060/debug/pprof/profile?seconds=15'
go tool pprof -top http://127.0.0.1:6060/debug/pprof/heap
go tool pprof -top -cum -focus='Day-86/internal' cpu.pprof
```

## 2. Profile under load, not at rest

An idle server profiles as `runtime.usleep` and `kevent`. The capture has to
overlap with the traffic, which is why `main.go` starts the load generator
first, sleeps half a second for a steady state, and only then pulls a 4-second
CPU profile.

## 3. Reading the CPU profile

The unfiltered flat top of a 4-second capture at ~950 req/s:

```
      flat  flat%   sum%        cum   cum%
     3.58s  23.14% 23.14%     3.58s 23.14%  runtime.usleep
     2.91s  18.81% 41.95%     2.91s 18.81%  runtime.pthread_cond_wait
     1.69s  10.92% 52.88%     1.69s 10.92%  runtime.madvise
     1.57s  10.15% 63.03%     1.57s 10.15%  runtime.pthread_cond_signal
```

None of that is the bottleneck — it is ten cores parking idle threads. This is
the single most common way a first profile gets misread.

Narrow to your own code and sort by cumulative time, so the *caller responsible*
for the work rises above the leaves doing it:

```
go tool pprof -top -cum -focus='Day-86/internal' cpu-slow.pprof

      flat  flat%        cum   cum%
         0     0%      2.48s 16.07%  service.(*Service).renderReport
     0.01s 0.065%      2.23s 14.45%  report.RenderSlow          ← the hot function
     0.07s  0.45%      1.14s  7.39%  runtime.mallocgc
         0     0%      0.70s  4.54%  runtime.concatstring2      ← `output += ...`
         0     0%      0.53s  3.43%  regexp.MustCompile         ← recompiled per item
```

Two lines name the two bugs. `concatstring2` is string concatenation in a loop;
`regexp.MustCompile` is a pattern being rebuilt once per line item.

## 4. Reading the heap profile

```
go tool pprof -top -cum heap-slow.pprof

Type: inuse_space
      flat  flat%        cum   cum%
    2.05MB 27.02%     3.08MB 40.60%  report.RenderSlow
         0     0%      3.08MB 40.60%  service.(*Service).renderReport
    2.51MB 33.07%     2.51MB 33.07%  runtime.mallocgc
```

`inuse_space` (the default) is what is live right now; `-sample_index=alloc_space`
is everything ever allocated, which is what you want when the problem is GC
pressure rather than retention. `FetchProfile` requests `?gc=1` so a collection
runs first — otherwise uncollected garbage reads like a leak.

## 5. The fix

Four mechanical changes in `report.RenderFast`, all named by the profile:

| Slow | Fast | Why |
|------|------|-----|
| `output += fmt.Sprintf(...)` in a loop | `strings.Builder` with `Grow` | `+=` allocates a new string and copies everything written so far — O(n²) copying |
| `regexp.MustCompile` inside the loop | compiled once at package level (and here, replaced by a byte loop) | compilation is microseconds each, ×n items × every request |
| `fmt.Sprintf("%d.%02d", …)` | `strconv.AppendInt` into a reused buffer | no format parsing, no interface boxing, no per-call allocation |
| `map[string]bool` rebuilt per item | a reused slice + linear scan | for 1–3 categories, hashing costs more than comparing |

## 6. The evidence

Benchmarks (`go test -run XXX -bench Render -benchmem ./internal/report`):

| Benchmark | ns/op | B/op | allocs/op |
|-----------|-------|------|-----------|
| `RenderSlow100` | 143,992 | 615,361 | 2,963 |
| `RenderFast100` | **15,617** | **28,984** | **207** |
| `RenderSlow1000` | 4,395,647 | 47,507,370 | 29,940 |
| `RenderFast1000` | **197,000** | **261,641** | **2,007** |

**9.2× faster at 100 items, 22× at 1000** — and the gap widens with size, which
is the signature of removing an O(n²): 47 MB of allocation per call becomes
262 KB, a 181× reduction.

End to end through HTTP, same load, same wall clock:

| mode | requests | mean | p99 | throughput |
|------|----------|------|-----|------------|
| slow | 5,747 | 6.261 ms | 11.456 ms | 958 req/s |
| fast | 54,228 | **664 µs** | **1.323 ms** | **9,037 req/s** |

**9.4× the throughput, p99 down 8.7×.**

## 7. What makes this trustworthy

- `TestFastMatchesSlow` asserts the two implementations produce byte-identical
  output. "Faster" and "wrong" are indistinguishable without it — and it earned
  its keep immediately: the hand-written SKU normaliser dropped the leading `-`
  that the regexp produced for `"---leading"`, and the test caught it.
- The workload is seeded (`report.Sample(n, 42)`), so two profiles taken an hour
  apart are comparable.
- The benchmark, the profile and the end-to-end latency all agree. Any one of
  them alone is a claim; together they are evidence.

## 8. Things worth knowing that this demo does not need

- `-benchtime=200x` here keeps the run short. A real comparison uses
  `-count=10` and `benchstat old.txt new.txt`, which reports whether the
  difference survives the noise.
- `-sample_index=alloc_objects` finds allocation *count* problems (GC pressure);
  `inuse_space` finds retention problems (leaks). Different questions.
- Block and mutex profiles are off by default because they cost something.
  `profiling.EnableBlockAndMutexProfiles` turns them on at rate 1 — fine while
  hunting a specific contention problem, not something to leave running.
- `go tool pprof -http=:` opens the flame graph in a browser. The `-top` text
  used here is what fits in a terminal and a document.
- Profile-guided optimization (`go build -pgo=cpu.pprof`) feeds a real profile
  back to the compiler for better inlining. It typically buys a few percent —
  worth having *after* the O(n²) is gone, never instead.
