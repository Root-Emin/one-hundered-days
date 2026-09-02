# Task tracker

A small task service, used here as the subject of a repository that is set up
the way a team maintains one: documented, reviewable, releasable.

## What it is

Tasks move `todo` → `doing` → `done`, and never backwards. Reopening is a new
task that links to the old one, so the record of what actually happened
survives — a status that can move both ways loses it.

HTTP, JSON, in memory. No database yet; see
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for why, and what would change.

## Quick start

```sh
make setup          # tools and dependencies
make run            # the service on :8095
make check          # fmt, vet, lint, race - what CI runs
make help           # every target
```

```sh
curl -X POST localhost:8095/tasks -d '{"title":"write the release notes"}'
curl -X POST localhost:8095/tasks/1/advance -d '{"status":"doing"}'
curl localhost:8095/tasks
```

## How it works

```
  cmd/api            HTTP: routing, status codes, JSON
     │
     ▼
  internal/tasks     the domain: statuses, transitions, the one invariant
```

`internal/tasks` knows nothing about HTTP. That is what lets a transition be
tested without a server, and what would let a gRPC front end reuse it unchanged.

Three tools in this repository check the repository itself:

| Tool | Checks |
|---|---|
| `cmd/repocheck` | the files a newcomer needs, and a review checklist over the source |
| Day 91's `prcheck` | commit messages, PR size, PR description |
| Day 94's `release` | the version the commits imply, the notes, a reproducible build |

## Documentation

- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — the layers, the decisions, and
  what is deliberately absent
- [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md) — setup, tests, and what must
  pass before you push
- [CHANGELOG.md](CHANGELOG.md) — what changed, per release
- [docs/GAPS.md](docs/GAPS.md) — what is still missing, written down honestly

## Status

`v0.2.0`. Pre-1.0: the HTTP shapes may still change, and a breaking change
bumps the minor version. See [docs/RELEASING.md](docs/RELEASING.md).
