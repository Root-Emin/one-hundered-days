# Linkr — architecture

## Components

```
                    ┌──────────────────────────────────────────┐
   public  ────────▶│  GET /{code}          the hot path        │
                    │    cache → store → 302                   │
                    │    click event → outbox (never blocks)    │
                    └──────────┬───────────────────────────────┘
                               │
   API key ────────▶┌──────────▼───────────────────────────────┐
                    │  /api/links…          the control plane   │
                    │    auth → rate limit → handler            │
                    └──────────┬───────────────────────────────┘
                               │
                    ┌──────────▼───────────┐    ┌──────────────┐
                    │   internal/store     │    │ internal/    │
                    │   SQLite + migrations│    │ cache (TTL)  │
                    │   links, clicks,     │    └──────────────┘
                    │   click_daily, outbox│
                    └──────────┬───────────┘
                               │ poll unpublished
                    ┌──────────▼───────────────────────────────┐
                    │  worker: aggregate clicks → click_daily   │
                    │  at-least-once, idempotent by event id    │
                    └──────────────────────────────────────────┘

   observability:  slog (JSON) · Prometheus /metrics · /healthz · /readyz
```

## Layers, and the one rule

```
cmd/linkr            wiring: flags, signals, lifecycle
internal/httpserver  transport: routing, status codes, middleware
internal/service     use cases: create a link, follow a link, read stats
internal/store       persistence: SQL, migrations, the outbox
internal/domain      the rules: what a code is, when a link is followable
```

**`internal/domain` imports nothing from this project.** No `net/http`, no
`database/sql`. The test for it: "is this link followable right now?" must be
answerable without a server and without a database, because that is the
question the whole service exists to answer.

Dependencies point inward. Transport knows about the service; the service knows
about ports it defines; the store implements them.

## The hot path, in detail

```
GET /abc123
  │
  ├─ cache.Get("link:abc123")          ~1µs, and the 99% case
  │    miss → store.Link(code)          ~200µs
  │           → cache.Set(ttl 60s)
  │
  ├─ link.Followable(now)?              domain rule, no I/O
  │    inactive → 410 Gone
  │    expired  → 410 Gone
  │    missing  → 404
  │
  ├─ 302 Location: <target>             the response ends here
  │
  └─ outbox.Append(click)               after the response is written
```

The click event is written **after** the redirect is sent, and its failure is
logged rather than returned: a click that is not counted is a metrics problem,
while a redirect that fails is a broken link.

## Decisions

Recorded as ADRs in [docs/adr/](adr/) — the short form, one page each:

| # | Decision |
|---|---|
| [0001](adr/0001-sqlite.md) | SQLite, not PostgreSQL |
| [0002](adr/0002-codes.md) | Random base62 codes, not sequential ids |
| [0003](adr/0003-async-clicks.md) | Clicks aggregated asynchronously |
| [0004](adr/0004-api-keys.md) | Hashed API keys, not sessions or JWTs |

## Failure modes, and what happens

| Failure | Behaviour |
|---|---|
| Database unavailable | Cached redirects keep working; `/readyz` fails; writes return 503 |
| Cache empty (restart) | Every redirect reads the database until it warms; latency rises, nothing breaks |
| Worker down | Clicks accumulate in the outbox; stats go stale; nothing is lost |
| Outbox full of failures | The relay stops at the first failure to preserve order; the queue depth metric is the alert |
| A link is followed a million times in a minute | Redirect is cached; the outbox absorbs the writes; `click_daily` catches up |

## What is deliberately absent

| Absent | Why | Revisit when |
|---|---|---|
| Horizontal scaling | One SQLite file; the redirect cache is per-process | The redirect rate exceeds one machine |
| A message broker | The outbox table is the queue; Day 83 has the NATS version | Another service needs the click stream |
| Custom domains | One namespace keeps code generation and routing simple | A customer asks and pays |
