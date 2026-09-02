# Day 87 — Memory and allocation tuning

Every number here is measured on this machine (Apple M4, 10 cores, Go 1.26.5)
by `go run ./Day-87` and `go test -bench . -benchmem ./internal/encode`.

## The order to apply fixes in

1. **Preallocate** — `make([]byte, 0, n)` when you can estimate `n`
2. **Build, don't concatenate** — `append` / `strings.Builder` instead of `s += …`
3. **Let the caller own the buffer** — `AppendX(dst []byte, …) []byte`
4. **`sync.Pool`** — last, only where a profile shows churn you cannot remove

Steps 1–3 are local, obvious and always safe. A pool is shared mutable state
with a reset you can forget; it goes last, with a benchmark behind it.

## What each step is worth

Encoding 100 log records, all five producing byte-identical output:

| implementation | ns/op | B/op | allocs/op |
|---|---|---|---|
| `EncodeNaive` — `fmt.Sprintf` + `s += …` | 68,046 | 469,345 | **1,317** |
| `AppendEncode(nil, …)` — no size estimate | 16,904 | 39,656 | 17 |
| `EncodePrealloc` — sized buffer | 15,314 | 8,192 | **1** |
| `BuildString` — `strings.Builder` + `Grow` | 15,636 | 8,192 | 1 |
| `AppendEncode` into a reused buffer | 14,955 | 0 | **0** |
| `Encoder.WriteTo` — `sync.Pool` | 14,990 | 0 | **0** |
| `Encoder.WriteTo`, `RunParallel` | 2,418 | 0 | 0 |

Reading it:

- **1,317 → 17** is `fmt` and `+=` going away. `+=` allocates a new string and
  copies everything written so far, so n lines cost O(n²) copying.
- **17 → 1** is the size estimate. `append` grows by doubling, so an unsized
  buffer allocates log₂(n) times and copies on each growth.
- **1 → 0** is handing buffer ownership to the caller. This is the shape the
  standard library uses (`time.AppendFormat`, `strconv.AppendInt`) precisely
  because it lets the caller decide where memory comes from.

## What allocations cost the collector

2,000 encodes of the same batch:

| | total allocated | GC cycles | wall |
|---|---|---|---|
| naive | 895.2 MB | 282 | 131 ms |
| pooled | 0.0 MB | 0 | 30 ms |

Go's collector is fast, but every cycle is CPU taken away from serving
requests. "Allocations cost CPU" means this table, not a hand-wave.

## Where `sync.Pool` does *not* pay

One small record:

| | ns/op | allocs/op |
|---|---|---|
| no pool | 151.3 | 1 |
| pooled | 158.8 | 1 |

The pool is **slower**. For a small buffer, allocation is a pointer bump into
the per-P cache, while the pool costs an interface conversion, atomics and a
per-P lookup. *"Use `sync.Pool`" is not advice — it is a hypothesis you
benchmark.*

## The four rules that make pooling safe

`internal/bufpool` encodes each of them, and each has a test:

1. **Reset on the way out, in `Get`.** Never trust the caller. A buffer handed
   out with the previous request's bytes still in it puts one user's data in
   another user's response — a security incident wearing a performance bug's
   clothes.
2. **Drop oversized buffers.** `MaxRetained` is 64 KiB. Without it, one 10 MB
   request permanently raises the memory floor of every pooled buffer.
3. **Pool a `*[]byte`, not a `[]byte`.** Putting a slice into an `any`
   allocates a header on every `Put` — the exact cost the pool exists to avoid.
   Staticcheck flags this as SA6002.
4. **Never return a pooled buffer**, or anything pointing into one. `WriteTo`
   keeps the buffer inside the function; `Encode` copies before returning, and
   the copy is the honest price of a `[]byte`-returning API on top of a pool.

A pool is also **not a cache**: its contents are cleared at every GC cycle.

## Escape analysis: why the allocation happened at all

```
go build -gcflags='-m' ./internal/escape
```

Three pieces of received wisdom that measurement contradicts — all asserted in
`escape_test.go` with `testing.AllocsPerRun`:

| Folklore | Measured |
|---|---|
| "returning a pointer allocates" | `NewPointEscapes(1,2)` discarded: **0 allocs**. Escape analysis runs *after* inlining, per call site. Store the result in a package-level variable and it becomes 1. |
| "`make` with a runtime size heap-allocates" | `SumHeapSlice(64)`: **0 allocs**. The compiler emits a size check and uses the stack when it fits. `SumHeapSlice(1<<20)`: 1 alloc. |
| "`fmt.Sprintf` allocates more than `strconv`" | Both: 2 allocs. The difference is CPU — 37.7 ns vs 19.3 ns — from format parsing and reflection. |

`-m` reports what the compiler decided *at the definition*. It is a hint about
where to look, not the answer. The answer is `allocs/op`.

## Keeping the win

`TestAllocationBudget` and `TestAppendEncodeIntoAWarmBufferDoesNotAllocate` use
`testing.AllocsPerRun` to assert budgets. A refactor that quietly reintroduces
a per-record allocation fails in CI, rather than six months later in a profile.

That is the difference between tuning something once and keeping it tuned.
