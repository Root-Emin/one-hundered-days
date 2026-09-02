# Day 60 — Reading List: layered architecture

Section 12 capstone. The same MVP as earlier sections, reorganised into
domain / service / storage / transport packages, with the dependency direction
enforced by tests rather than by convention.

## Package diagram

Generated from the source — run it yourself:

```bash
go run ./cmd/archdiagram
```

```
cmd/api                      -> internal/service, internal/storage/sqlite, internal/transport/httpapi
cmd/archdiagram              (depends on nothing in this service)
internal/domain              (depends on nothing in this service)
internal/service             -> internal/domain
internal/storage/sqlite      -> internal/domain
internal/transport/httpapi   -> internal/domain, internal/service
```

Drawn as layers, with every arrow pointing inward:

```
            ┌───────────────────────────────┐
            │            cmd/api            │   wiring only
            └───────────────┬───────────────┘
                            │
        ┌───────────────────┼────────────────────┐
        ▼                                        ▼
┌──────────────────┐                  ┌────────────────────┐
│ transport/httpapi│                  │  storage/sqlite    │   outer: IO
│ routes, DTOs,    │                  │  SQL, driver,      │
│ status codes     │                  │  row mapping       │
└─────────┬────────┘                  └──────────┬─────────┘
          │                                      │
          ▼                                      │
   ┌─────────────┐                               │
   │   service   │  use cases                    │
   └──────┬──────┘                               │
          ▼                                      ▼
   ┌────────────────────────────────────────────────────┐
   │                      domain                         │
   │  Book, ISBN, Status, rules, errors, ports           │
   │  imports: standard library only                     │
   └─────────────────────────────────────────────────────┘
```

`storage` points at `domain` because it *implements* the ports the domain
declares. That inversion is the whole trick: the database plugs into the
business rules, not the other way round.

## Package responsibilities

| Package | Owns | May import |
|---|---|---|
| `internal/domain` | `Book`, `ISBN`, `Status`, invariants, `ErrValidation` / `ErrNotFound` / `ErrConflict`, and the `BookRepository`, `StatsReader`, `Clock` ports | standard library only |
| `internal/service` | Use cases (`AddBook`, `Start`, `RecordProgress`, `Progress`), input parsing into domain types | `domain` |
| `internal/storage/sqlite` | Every SQL statement, the driver import, row ↔ entity mapping | `domain` |
| `internal/transport/httpapi` | Routing, JSON DTOs, domain-error → status-code mapping | `domain`, `service` |
| `cmd/api` | Configuration, wiring, server lifecycle | everything |
| `cmd/archdiagram` | Prints and checks the package graph | nothing |
| `internal/arch` | Architecture tests (no production code) | — |

Rules, all enforced in `internal/arch/arch_test.go`:

1. **Dependencies point inward.** `cmd` → `transport`/`storage` → `service` → `domain`.
2. **The domain is pure.** No third-party import, no package of this service.
3. **Only `storage` knows the database.** The driver import appears in exactly
   one package.
4. **`transport` and `storage` do not know each other.** Two outer layers at
   the same level stay decoupled, so either can be replaced alone.
5. **No import cycles.**

Test files are exempt: like `cmd`, a test may wire the whole stack (the
`httpapi` tests really do run against SQLite).

Try breaking a rule and watch it fail:

```bash
printf 'package domain\n\nimport _ "net/http"\n' > internal/domain/violation.go
go test ./internal/arch    # fails
rm internal/domain/violation.go
```

## What moved, and why

This is a refactor: behaviour is unchanged from the earlier MVP, structure is not.

| Was | Now | Reason |
|---|---|---|
| Validation inside handlers | `domain.NewBook`, `domain.NewISBN` | The same rule applied to HTTP, a CLI, or an importer — for free |
| `percent_read` computed in the JSON layer | `Book.PercentRead()` | A business calculation belongs with the business type |
| `status` as a free string | `domain.Status` with `Valid()` | An invalid status cannot be constructed by accident |
| `time.Now()` in the service | `domain.Clock` port | "Finished at" is testable without waiting |
| SQL spread through handlers | `internal/storage/sqlite` | One package to audit, one package to replace |
| `if err.Error() == "..."` | `errors.Is` on domain sentinels | Error handling that survives a reworded message |

## Testing strategy

| Layer | Test | Speed | What it catches |
|---|---|---|---|
| `domain` | pure unit tests | instant | Broken invariants, bad state transitions |
| `service` | fakes for every port (`library_test.go`) | milliseconds | Use-case logic, error mapping, clock-dependent rules |
| `storage/sqlite` | real database per test in `t.TempDir()` | ~1s | SQL typos, constraints, round-trip fidelity |
| `transport/httpapi` | `httptest` over the real stack | ~1s | Status codes, DTO shape, end-to-end wiring |
| `arch` | source parsing | instant | Layering violations, import cycles |

```bash
go test ./...
go test -race -count=1 ./...
go test -run TestReadingWorkflow -v ./internal/service
```

## Running

```bash
go run ./cmd/api                        # :8080, in-memory database
DB_PATH=data/library.db go run ./cmd/api
go run ./cmd/archdiagram                # package graph + rule check
```

```bash
curl -XPOST localhost:8080/books \
  -d '{"isbn":"978-0-13-419044-0","title":"The Go Programming Language","author":"Donovan","pages":380}'
curl -XPOST localhost:8080/books/1/start
curl -XPOST localhost:8080/books/1/progress -d '{"page":190}'
curl localhost:8080/books?status=reading
curl localhost:8080/stats
```

## Adding a feature, without breaking the layering

Say "rate a book after finishing it":

1. `domain`: add `Rating` (a value object that rejects 0 and 6), a `Rate()`
   method that refuses unless the book is finished, and a test for both.
2. `service`: a `RateBook` use case — load, apply, save.
3. `storage`: one column, one migration, one line in `scanBook`.
4. `transport`: a route, a DTO field, no logic.

If step 4 starts wanting an `if`, the rule belongs in step 1.
