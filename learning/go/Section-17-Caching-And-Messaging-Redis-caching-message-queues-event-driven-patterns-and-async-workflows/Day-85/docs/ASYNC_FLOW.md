# Day 85 — Async flow: HTTP, DB, cache, messaging

The diagram exists so a teammate can find the race windows without reading
every handler. Each numbered arrow is a place where the system can be
interrupted, and the notes below say what happens if it is.

## Components

```
                   ┌────────────────────────────────────────────┐
                   │              cmd/api  (sync)               │
   client ────────▶│  GET  /products/{id}   cache-aside read    │
                   │  PUT  /products/{id}   write, invalidate   │
                   │  POST /orders          order + event, 202  │
                   └───────┬───────────────────────┬────────────┘
                           │ (1) read/write        │ (2) get/set/del
                           ▼                       ▼
                   ┌───────────────┐       ┌────────────────┐
                   │   SQLite DB   │       │  cache (memory │
                   │  products     │       │  or Redis)     │
                   │  orders       │       │  product:{id}  │
                   │  outbox       │       │  TTL 30s       │
                   │  receipts     │       └────────────────┘
                   │  processed_ev │
                   └───────┬───────┘
                           │ (3) poll unpublished, oldest first
                           ▼
                   ┌────────────────────────────────────────────┐
                   │            cmd/worker  (async)             │
                   │  queue.Relay  ──(4) publish──▶  queue.Bus  │
                   │                                    │       │
                   │                       (5) deliver  ▼       │
                   │                        worker.HandleOrder  │
                   │                        store.ProcessOnce   │
                   └───────────────────────┬────────────────────┘
                                           │ (6) claim + work, one tx
                                           ▼
                                   receipts, orders.status
```

## The write path, step by step

```
POST /orders
   │
   ├── BEGIN
   │     INSERT INTO orders   ...      ← the business fact
   │     INSERT INTO outbox   ...      ← the announcement of that fact
   │   COMMIT                          ← both, or neither
   │
   └── 202 Accepted {"status":"placed"}   the receipt does not exist yet

worker tick
   │
   ├── SELECT * FROM outbox WHERE published_at IS NULL ORDER BY id
   ├── publish  ─────────────────────▶  bus/NATS/Kafka
   ├── UPDATE outbox SET published_at   ← publish FIRST, mark SECOND
   │
   └── handler
         BEGIN
           INSERT INTO processed_events  ← the claim; UNIQUE decides the winner
           INSERT INTO receipts
           UPDATE orders SET status='confirmed'
         COMMIT
```

## Race windows

| # | Window | What can go wrong | What we do about it |
|---|--------|-------------------|---------------------|
| 1 | Between the DB commit and the cache delete on `PUT /products/{id}` | The process dies; the cache keeps the old price | The 30s TTL bounds the staleness. This is the TTL's real job — it is the backstop for a missed invalidation, not the invalidation strategy |
| 2 | Delete-then-write instead of write-then-delete | A concurrent reader misses, reads the **old** row, and repopulates the cache *after* the update commits — the stale value then survives a full TTL | `updateProduct` commits first and deletes second, so any repopulation after the delete reads the new row |
| 3 | Two concurrent `PUT`s both writing the new value into the cache | The slower write can land last and leave the **older** price cached | We `Delete` rather than `Set` on update; the next read repopulates from the source of truth |
| 4 | Between `publish` and `UPDATE outbox SET published_at` | The worker dies; the event is published but not marked | It is published again on the next tick → at-least-once → the consumer's claim makes the second one a no-op. The reverse order would lose the event entirely, and nothing recovers from that |
| 5 | Duplicate delivery from the broker | The receipt is written twice, the customer is charged twice | `store.ProcessOnce` inserts the claim **inside the same transaction** as the work. Check-then-act in two statements would still race; one transaction cannot |
| 6 | The handler fails halfway | A claim exists for work that never happened, so the redelivery is skipped | The rollback removes the claim with the work, so a redelivery retries cleanly |
| 7 | `processed_events` grows forever | The dedupe table becomes the biggest table in the database | Prune by age — with a retention **longer than the broker's maximum redelivery window**, or a late redelivery is processed twice |

## What the client sees

`POST /orders` returns `202 Accepted` with `"status": "placed"`, not `201` with
`"confirmed"`. The order is durable, but the receipt is not written yet. An
async system that reports work as finished before it is has just moved the lie
from the database to the API.

The client learns the outcome by re-reading `GET /orders/{id}` until the status
changes — polling here, a webhook or an SSE stream in a larger system.

## Where this stops being a toy

- **The bus is in-process.** Swapping `queue.Bus` for NATS or Kafka changes
  `internal/queue` and nothing else — that is why the `Publisher` interface is
  there.
- **The relay polls.** At scale, change-data-capture (Debezium reading the
  write-ahead log) removes the polling entirely, at the cost of more moving
  parts.
- **There is no dead letter queue here** (Day 84 has one). A handler that fails
  forever blocks the events behind it, because the relay deliberately stops at
  the first failure to preserve order.
- **The cache is per-process.** Two API replicas have two caches, and an
  invalidation on one does not reach the other — the moment you scale out, the
  cache has to move to Redis.
