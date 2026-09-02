# Linkr — Day 97: the core features

The vertical slice: create a link, follow it, list and deactivate — through
auth, the service, the store and real SQLite.

## Run it

```sh
# a key, printed once
go run ./cmd/linkr -issue-key ada -db /tmp/linkr.db
#   owner:  ada
#   key id: 3f2a...
#   key:    lk_FhpBhs44j...
#   This key is shown once. Store it now; it cannot be recovered.

go run ./cmd/linkr -db /tmp/linkr.db -base-url http://127.0.0.1:8097 -addr 127.0.0.1:8097
```

```sh
KEY=lk_...

# create - the code is generated
curl -X POST :8097/api/links -H "Authorization: Bearer $KEY" \
     -d '{"target":"https://go.dev/doc/effective_go"}'
# {"code":"4VPUh4t","short_url":"http://127.0.0.1:8097/4VPUh4t",...}

# or choose one
curl -X POST :8097/api/links -H "Authorization: Bearer $KEY" \
     -d '{"target":"https://example.com","code":"golang"}'

# follow it - no credential, that is the point of a short link
curl -i :8097/golang            # 302 → https://example.com

# list, with click counts
curl :8097/api/links -H "Authorization: Bearer $KEY"

# deactivate
curl -X DELETE :8097/api/links/golang -H "Authorization: Bearer $KEY"   # 204
curl -i :8097/golang                                                    # 410 Gone
```

## What today added

```
internal/store     SQLite, embedded migrations, links + api_keys + clicks + outbox
internal/auth      API keys: crypto/rand, shown once, stored as SHA-256
internal/service   the use cases, with interfaces IT defines
internal/httpserver/api.go   the handlers, and one error→status mapping
cmd/linkr          -issue-key, migrations on startup, wiring
```

## Decisions visible in the code

| Choice | Why |
|---|---|
| **302, not 301** | A permanent redirect is cached by the browser forever: deactivating the link stops working for anyone who has visited, and the click is never counted again. |
| **The click is recorded after the response**, on a detached context | The request's context is cancelled the moment the client disconnects — and a user who closes the tab still clicked. A click that is not counted is a metrics problem; a redirect that waits on a write is a broken link. |
| **410 for deactivated and expired, 404 for unknown** | Gone tells a crawler to forget the URL; Not Found invites it back tomorrow. |
| **"Not yours" answers 404, not 403** | "This exists but belongs to someone else" is an enumeration oracle. |
| **The redirect route is outside the auth middleware** | Wrapping the whole mux would put a bearer token in front of every short link. |
| **`DisallowUnknownFields`** | A misspelled `expires_at` that is silently ignored is a very expensive silence. |
| **The key is hashed, never logged, never in a query string** | A query string reaches access logs, `Referer` headers and browser history. |

## Tests

```sh
go test -race ./...
```

- `internal/domain` — the rules, no I/O, microseconds
- `internal/service` — the use cases against a map, including the code-collision
  retry and its bound
- `internal/store` — real SQLite: migrations idempotent, expiry round-trips,
  a deactivated code cannot be reused
- `internal/httpserver` — the whole service through HTTP: auth, validation,
  302/404/410/409, and the click that lands after the response

Next: [Day 98](../Day-98) — the cache, rate limiting, metrics, and the worker
that turns the outbox into daily aggregates.
