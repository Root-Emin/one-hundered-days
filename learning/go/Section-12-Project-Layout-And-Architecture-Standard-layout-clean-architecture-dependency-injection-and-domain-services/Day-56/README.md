# Day 56 — Standard Go Project Layout

A task service laid out the way most Go teams lay out a service. The point of
the day is not the features — it is that a new teammate can guess where
anything lives before opening a file.

## Structure

```
Day-56/
├── cmd/
│   └── api/
│       └── main.go          the binary: config, wiring, server lifecycle
└── internal/
    ├── config/              environment settings, read once at startup
    ├── domain/              business types, rules, errors, repository interface
    ├── service/             use cases; orchestrates domain + repository
    ├── repository/          storage implementation (in-memory here)
    └── httpapi/             HTTP transport: routing, DTOs, error mapping
```

| Package | Owns | Imports | Never imports |
|---|---|---|---|
| `domain` | Task, its invariants, domain errors, `TaskRepository` | stdlib only | anything in this service |
| `service` | Use cases, workflow rules, the injected `Clock` | `domain` | `httpapi`, `repository` |
| `repository` | Persistence, storage-specific errors | `domain` | `service`, `httpapi` |
| `httpapi` | Routes, JSON DTOs, status codes | `service`, `domain` | `repository` |
| `config` | Environment parsing | stdlib only | everything |
| `cmd/api` | Wiring and process lifecycle | all of the above | — |

Arrows point one way:

```
cmd/api ──► httpapi ──► service ──► domain ◄── repository
```

`domain` sits at the centre and depends on nothing. That is what lets the
business rules be tested without an HTTP server or a database, and what lets
the storage engine be replaced by editing one package plus one line of `main`.

## Why `cmd/` and `internal/`

- **`cmd/<name>/`** — one directory per binary. A repository that later grows a
  worker or a CLI adds `cmd/worker/`, and nobody has to reshuffle anything.
- **`internal/`** — enforced by the compiler, not by convention: no module
  outside this directory tree can import these packages. Everything is private
  until you deliberately move it out, which is the right default for a service.
- **No `pkg/`** here. `pkg/` only earns its place when other repositories
  actually import the code; adding it upfront is cargo cult.
- **No `utils/`, `helpers/`, `common/`.** A package named after what it *is*
  (`config`, `httpapi`) tells the reader something; a junk drawer named after
  what it *is not* collects unrelated code until it depends on everything.

## Thin `main`

`cmd/api/main.go` does exactly four things:

1. load configuration
2. construct the concrete implementations
3. wire them together through constructors
4. start the server and handle graceful shutdown

There is no business logic in it. `main()` itself is three lines; the real work
is in `run() error`, so every exit path returns an error and still runs its
deferred cleanup — `log.Fatal` in the middle of `main` skips defers.

## Running

```bash
go run ./cmd/api
PORT=9090 go run ./cmd/api
```

```bash
curl -XPOST localhost:8080/tasks -d '{"reference":"task-1","title":"Ship it","assignee":"ada"}'
curl localhost:8080/tasks
curl -XPOST localhost:8080/tasks/1/status -d '{"status":"doing"}'
curl localhost:8080/tasks/overdue
curl -XDELETE localhost:8080/tasks/1
```

## Tests

```bash
go test ./...
```

`internal/httpapi/handler_test.go` drives the whole stack through `httptest`,
wired exactly as `main` wires it. Because the layers are separated, the service
rules can also be tested with a fake repository and no HTTP at all — that is
Day 58.

## Onboarding notes

- Looking for a business rule? `internal/domain` first, then `internal/service`.
- Looking for a status code? `internal/httpapi`, `respondError`.
- Looking for SQL? `internal/repository` — and only there.
- Adding an endpoint? Route in `httpapi`, use case in `service`, rule in
  `domain`. If you find yourself adding a rule to `httpapi`, it belongs one
  layer down.
