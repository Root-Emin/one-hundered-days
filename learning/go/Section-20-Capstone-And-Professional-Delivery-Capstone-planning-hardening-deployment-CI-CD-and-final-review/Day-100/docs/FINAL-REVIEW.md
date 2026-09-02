# Final review — Linkr, and the hundred days

The last day of the course, spent the way the last day of a project should be:
reviewing what was built, saying honestly what is not there, and deciding what
comes next.

## The capstone, reviewed against its own requirements

Day 96 wrote the requirements down with numbers so that this review could be
something other than an opinion.

| Promised on Day 96 | Delivered | Evidence |
|---|---|---|
| Create a link with an API key | ✅ | `POST /api/links`, generated or custom code |
| Follow a short link | ✅ | `GET /{code}` → 302 |
| Clicks per day | ✅ | `GET /api/links/{code}/stats`, from the aggregate |
| List and deactivate | ✅ | `GET`/`DELETE /api/links/{code}` |
| Deactivated returns **410**, not 404 | ✅ | asserted in `httpserver` tests |
| Expiry | ✅ | validated at creation, enforced at follow |
| Referrer breakdown | ❌ | **cut, as planned** — the only "could" in the requirements |
| Redirect p95 < 5 ms | ✅ | **481 µs** measured |
| Redirect p99 < 20 ms | ✅ | **713 µs** measured |
| API p95 < 50 ms | ✅ | well inside; the API is not the hot path |
| A click already accepted is never lost | ✅ | 4,001 of 4,001 aggregated; outbox drains to 0 |
| Redirects survive the database being down | ⚠️ | **partially**: cached links keep working and `/readyz` fails correctly, but there is no test that closes the database mid-flight. It is in GAPS. |
| Ready in < 2 s including migrations | ✅ | measured at ~10 ms on an empty database |

Six of the seven stories, both latency targets, and the one that was cut was
the one designated to be cut on Day 96. The scope control worked because it was
decided in advance rather than at 11pm on Day 99.

### Code review of my own capstone

Using the Day 91 checklist, in cost order:

**Correctness.** The error paths are the tested ones: a failed reservation
changes nothing, a rolled-back migration leaves a known version, a failed
handler releases its claim. The boundary cases have tests — empty, expired,
expires-exactly-now, another owner's link, an unparseable code.

**Security.** Reviewed in full in [SECURITY.md](SECURITY.md). The one finding
with a deadline is the toolchain: Go 1.26.5 has five known standard-library
vulnerabilities, three reachable from this call graph, all fixed in 1.26.6. The
Dockerfile and CI already pin 1.26.6; the *local* toolchain is what needs the
upgrade.

**Tests.** 97% on the domain, 90%+ on cache, auth, rate limiting and the
worker, 62% overall — the shortfall is `cmd/` wiring and error branches that
need a broken database to reach. Every test asserts behaviour rather than
implementation, and each of them fails if its feature is reverted.

**Design.** The dependency rule holds: `internal/domain` imports nothing from
this project. The service defines its own interfaces, which is why its tests
run against a map. The one thing I would change: `service.Service` is becoming
a god object at ~200 lines, and if a second aggregate arrives it should split
into `linkService` and `statsService`.

**Readability.** Comments explain *why*, not *what* — the ones worth keeping
are the ones that record a decision someone would otherwise re-litigate: why
302 and not 301, why the click is written after the response, why invalidation
comes after the commit.

**Performance.** Measured, twice, and one real bottleneck found and fixed. The
optimization that was *not* made is worth noting too: the redirect cache is not
LRU, because tracking recency costs a write on every read, and for this
workload the popular codes are re-cached on their next request anyway.

## What the hundred days actually covered

| Days | What | What survived into the capstone |
|---|---|---|
| 1–20 | Fundamentals, types, functions, errors | typed domain errors, sentinel errors, `errors.Is` everywhere |
| 21–40 | Concurrency, HTTP, context, middleware | the middleware chain, graceful shutdown, context propagation |
| 41–50 | `database/sql`, migrations, repositories | embedded migrations, one transaction per unit of work |
| 51–60 | Auth, security, architecture | hashed keys, the dependency rule, ports the consumer defines |
| 61–70 | gRPC, testing, quality gates | integration tests through the real surface, table-driven tests |
| 71–80 | Observability, resilience, containers, CI | structured logs, RED metrics, probes, the distroless image |
| 81–90 | Caching, messaging, performance | the outbox, idempotent consumers, the cache, pprof, the load test |
| 91–95 | Review, docs, DX, releases | ADRs, the runbook, semantic versioning, reproducible builds |
| 96–100 | The capstone | all of it, in one service |

The through-line, if there is one: **almost nothing in the second half was a
new language feature.** It was the same Go, applied to the question "what
happens when this fails at 3am, and who finds out?"

## What I would tell someone starting Day 1

1. **Write the failing test first, and make it fail for the right reason.** A
   test that passes before the change is documentation, not a test. Days 41–50
   would have gone faster with this.
2. **Measure before optimising, and after.** The worker bottleneck was
   invisible until 5,000 requests hit it, and would have been invisible in
   production for hours.
3. **The interesting decisions are the ones where the obvious choice is wrong.**
   301 vs 302, 404 vs 403, delete-then-write vs write-then-delete. Those are
   what an ADR is for.
4. **Comments record decisions, not mechanics.** `// increment i` is noise;
   `// after the commit, because deleting first lets a concurrent reader
   repopulate from the old row` is the reason the next person does not "fix" it.
5. **Operability is a feature.** A service without probes, metrics and a
   runbook is a demo that happens to compile.

## Next

The honest ranking, most useful first:

1. **Kubernetes and real deployment.** Everything here stops at a container. The
   next questions — rolling updates, probes that a scheduler acts on, resource
   limits, a real rollback — are all things a compose file cannot teach.
2. **PostgreSQL and connection pooling under contention.** SQLite made the
   right trade for this service and hid a whole class of problem: pool
   exhaustion, lock contention, transaction isolation levels that actually
   differ.
3. **Distributed systems properly.** The outbox and idempotent consumers here
   are one machine's version. Consensus, partitioning and the failure modes of
   a broker under a network partition are the next layer.
4. **Profiling in anger.** Day 86 profiled a synthetic workload. The skill is
   reading a profile of something you did not write, under load you did not
   design.
5. **Reading other people's Go.** The standard library first — `net/http`'s
   server, `database/sql`'s pool. Both are in this project's call graph already,
   and both are better written than anything above.

## The one open item

Upgrade the local toolchain to **Go 1.26.6**. Five standard-library
vulnerabilities, three of them reachable from this code. Everything else on the
gaps page is a trade-off; this one is just work.
