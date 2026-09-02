# Architecture

What the README should not drown in: the shape of the system, the rules that
hold it together, and the decisions someone will otherwise re-litigate every
six months.

## The shape

```
             HTTP
              │
   ┌──────────▼───────────┐
   │  internal/httpapi    │  transport: routing, status codes, JSON
   │  - Routes() table    │  knows about HTTP, knows nothing about storage
   └──────────┬───────────┘
              │  catalog.Product, catalog.Err*
   ┌──────────▼───────────┐
   │  internal/catalog    │  domain: products, stock, invariants
   │  - Store             │  knows nothing about HTTP
   └──────────────────────┘

   checked against, not used by:

   api/openapi.yaml  ◄── internal/contract ──►  httpapi.Routes()
   *.go doc comments ◄── internal/docslint
   CHANGELOG.md      ◄── internal/changelog
```

## The dependency rule

`catalog` must not import `httpapi`, `net/http`, or anything transport-shaped.

The test for it is simple: the domain package has to be usable from a gRPC
server, a CLI and a test with no HTTP in sight. The moment a `http.Request`
reaches it, every caller needs one — including the tests, which then need a
server to exercise a stock calculation.

The direction is one-way: transport depends on domain, never the reverse.

## Decisions

### Money is an integer count of the smallest currency unit

`PriceCent int64`, never `float64`. Binary floating point cannot represent
19.99, so three of them add to 59.969999999999999. Every currency question
("what about currencies with three decimals?") is answered by storing the unit
alongside the amount, never by switching to a float.

### Errors are sentinels, not strings

`catalog.ErrNotFound` and friends are part of the API; the message text is not.
Callers use `errors.Is`. This is what lets `httpapi.writeStoreError` map domain
failures onto status codes in one place, rather than every handler guessing.

The mapping lives in one function on purpose: scattered, the same failure
becomes a 400 on one endpoint and a 500 on another, and clients cannot tell
which errors are worth retrying.

### Insufficient stock is 409, not 400

The request was well formed; the *state* said no. A 400 tells the client to fix
its request, which will not help — the fix is to try again later or order less.
This distinction is what makes a client's retry logic possible.

### The route table is data

`httpapi.Routes()` returns a slice of `Route` rather than only calling
`mux.HandleFunc`. That is what lets `internal/contract` compare the server with
`api/openapi.yaml`. A route table that exists only as code can be read by humans
and by nothing else.

### The store is in memory, and persistence is the caller's problem

A store that also owns a database cannot be tested without one. When
persistence arrives it goes behind the same interface, and the tests here do
not change.

## What is deliberately absent

- **Authentication.** This service sits behind a gateway that does it. If that
  ever stops being true, it needs a design, not a middleware bolted on.
- **Pagination.** `GET /products` returns everything. Fine at hundreds of
  products, wrong at hundreds of thousands — the trigger to revisit is the
  catalog crossing about 10,000 items, not a slow-week refactor.
- **A cache.** The store is a map; there is nothing to cache yet. It becomes a
  question when the store is a database.

## Where the contract lives

`api/openapi.yaml` is the contract, and `internal/contract` proves the server
still honours it. Adding a route without a spec entry fails a test. Documenting
a status code the handler cannot return fails a test.

This matters because a drifted spec is *worse than no spec*: clients trust it,
generate code from it, and find out the difference in production.
