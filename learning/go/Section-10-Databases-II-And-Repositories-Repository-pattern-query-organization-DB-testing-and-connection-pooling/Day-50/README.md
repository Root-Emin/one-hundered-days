# Day 50 — Shop API: the data layer, reviewed

Section 10 capstone. A small order-taking service whose persistence is fully
isolated behind repository interfaces, with organized SQL, versioned
migrations, tuned pooling and integration tests.

## Layout

| File | Responsibility |
|---|---|
| `migrations/` | Versioned schema, `up` and `down` for every change |
| `queries.go` | **Every** SQL statement, named after its operation |
| `repository.go` | Interface implementations; driver errors → domain errors |
| `database.go` | Pool settings, migration runner, transaction manager |
| `domain.go` | Types, validation, repository interfaces |
| `service.go` | Business logic and units of work |
| `api.go` | HTTP routing, DTO mapping, status codes |
| `main.go` | Composition root |

Dependency direction is one-way: `api → service → domain ← repository`.
`domain.go` imports no database, HTTP or JSON package, which is the mechanical
check that the layering is real.

## Schema

```
customers ──< orders ──< order_items >── products
```

| Table | Key columns | Indexes |
|---|---|---|
| `customers` | `id`, `email`, `name`, `created_at` | `idx_customers_email` (unique) |
| `products` | `id`, `sku`, `name`, `price_cents`, `stock` | `idx_products_sku` (unique) |
| `orders` | `id`, `customer_id`, `status`, `total_cents` | `idx_orders_customer (customer_id, created_at DESC)` |
| `order_items` | `id`, `order_id`, `product_id`, `quantity`, `unit_cents` | `idx_order_items_order` |

Conventions:

- **Money is integer minor units** (`*_cents`). No floats anywhere near a price.
- **Every table has `created_at`**, for auditing and for stable ordering.
- **Indexes exist for shipped queries only** — each one matches a statement in
  `queries.go`.
- **Constraints are in the database**: unique email/SKU, `stock >= 0`,
  `quantity > 0`, foreign keys with `ON DELETE CASCADE`. The application
  validates for good error messages; the database is what actually guarantees.

## Migrations

Migrations are embedded in the binary (`//go:embed migrations/*.sql`), so a
deployed build can never drift from the SQL it was compiled with. Each one
runs inside its own transaction together with its `schema_migrations` row.

```bash
go run . migrate up       # apply everything pending
go run . migrate status   # what is applied, what is pending
go run . migrate down     # revert the newest applied migration
```

Adding a migration:

1. Create `migrations/000N_name.up.sql` **and** `000N_name.down.sql`
   (a missing half is rejected at startup).
2. Prefer additive changes: new nullable column or column with a default,
   backfill, then tighten. That keeps a rolling deploy safe.
3. Run `go run . migrate up`, then `go run . migrate down` once to prove the
   revert works before opening the PR.

## Connection pool

Set in `DefaultDBConfig` (`database.go`), overridable per environment:

| Setting | Env var | Default | Why |
|---|---|---|---|
| `SetMaxOpenConns` | `DB_MAX_OPEN_CONNS` | 10 | Bounded, so a traffic spike queues locally instead of exhausting the database |
| `SetMaxIdleConns` | `DB_MAX_IDLE_CONNS` | 10 | Equal to max open: a lower value closes connections it is about to need |
| `SetConnMaxLifetime` | `DB_CONN_MAX_LIFETIME` | 30m | Connections rotate, surviving failovers and server-side idle timeouts |
| `SetConnMaxIdleTime` | `DB_CONN_MAX_IDLE_TIME` | 5m | Idle capacity is released when traffic drops |

Sizing rule for Postgres: `max_open × number_of_instances` must stay
comfortably below `max_connections`, leaving headroom for migrations, admin
sessions and background jobs. Watch `db.Stats().WaitCount` — connection waits
show up as latency long before they show up as errors (Day 49).

The pool is closed on every exit path, including migration commands: a process
that leaves connections open makes deploys hang.

## Transactions

`SQLTxManager.WithinTx` hands the callback a `Repositories` set bound to one
`*sql.Tx`, so the service composes a unit of work without ever seeing a
`*sql.Tx` itself.

- `PlaceOrder`: reserve stock for every line, insert the order and its items —
  all or nothing. Prices are read from the catalog **inside** the transaction,
  never taken from the request body.
- `CancelOrder`: return stock and set the status together.
- Lock and serialization conflicts (SQLITE_BUSY, Postgres `40001`/`40P01`) are
  retried up to 8 times with exponential backoff. Business errors are not
  retried.

## Running

```bash
go run . migrate up
go run . serve                         # PORT=8080 by default
go run . demo                          # scripted end-to-end run, in-memory
```

```bash
curl -XPOST localhost:8080/customers -d '{"email":"ada@example.com","name":"Ada"}'
curl -XPOST localhost:8080/products  -d '{"sku":"kb-01","name":"Keyboard","price_cents":12900,"stock":5}'
curl -XPOST localhost:8080/orders    -d '{"customer_id":1,"lines":[{"product_id":1,"quantity":2}]}'
curl localhost:8080/customers/1/orders
curl -XPOST localhost:8080/orders/1/cancel
```

## Tests

```bash
go test ./...                 # everything
go test -race -count=1 ./...  # what CI runs
go test -run TestOrderRepository -v ./...
```

- Each test opens **its own SQLite file** in `t.TempDir()` and runs the real
  migrations, so tests are isolated and safe under `t.Parallel()`.
- `repository_test.go` covers happy **and** not-found paths for every
  repository method, plus constraint violations that a fake could never catch.
- `TestConcurrentOrdersCannotOversell` runs 20 buyers against 10 units and
  requires exactly 10 winners — the test that justifies putting the stock rule
  in `UPDATE ... WHERE stock >= ?` instead of an `if` in Go.
- `api_test.go` covers the HTTP contract and asserts that no response body
  ever leaks database detail.

No test needs a running server, a container, or a fixture file. `go test ./...`
on a clean checkout is the whole setup.
