# Linkr — Day 96: plan and walking skeleton

The capstone: a link shortener with click analytics, built over Days 96–100.
Each day carries the whole service forward in its own directory, so every day
is runnable on its own — the way a tagged release is.

## Today's deliverable

The service starts, answers its probes, and stops cleanly. No links yet.

```sh
go run ./cmd/linkr
curl localhost:8096/healthz
curl localhost:8096/readyz
```

```
INFO starting config="addr=:8096 metrics=127.0.0.1:9096 db=linkr.db ... rate_limit=120/min"
INFO listening addr=[::]:8096
INFO ready addr=:8096
INFO request request_id=9df6a6c76cdf21c5 method=GET path=/ status=200 duration=46µs
INFO draining grace=15s
INFO stopped
```

## Why a skeleton before features

The wiring is where the surprises are: signals, timeouts, shutdown ordering, a
readiness probe that lies. An hour on day one, or a lost day on day five.

Three things it already gets right, each of which is a bug in most first drafts:

- **Liveness ignores dependencies.** A `/healthz` that fails when the database
  is down gets the container killed, which does not fix the database and does
  lose the cache keeping redirects alive.
- **Readiness fails *before* the listener closes.** Traffic drains away while
  the process can still serve it; the alternative is connection resets for the
  last few requests.
- **Config is validated at startup.** A typo in a duration stops the process in
  the first second rather than producing a zero timeout — which means *no*
  timeout — that surfaces as a hang three hours later.

## Read next

| Document | What it answers |
|---|---|
| [docs/REQUIREMENTS.md](docs/REQUIREMENTS.md) | what it does, the endpoints, and the service levels with numbers |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | the components, the hot path, and every failure mode |
| [docs/MILESTONES.md](docs/MILESTONES.md) | the five days, and what gets cut if time runs out |
| [docs/adr/](docs/adr/) | four decisions: SQLite, random codes, async clicks, hashed API keys |

## What exists so far

```
cmd/linkr              flags, signals, lifecycle
internal/config        loaded from defaults ← environment ← flags, validated
internal/domain        Code, Link, Click - no I/O, no imports from this project
internal/httpserver    routing, middleware, probes, graceful shutdown
```

`internal/domain` is the part to read first. It answers "is this link
followable right now?" without a server and without a database, because that is
the question the whole service exists to answer.
