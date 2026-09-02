# Releasing the task tracker

The full reasoning behind each step is in
[Day 94's RELEASING.md](../../Day-94/docs/RELEASING.md); this is the procedure
for *this* repository.

## Cutting v0.2.0

```sh
# 1. what do the commits say the version should be?
go run ../Day-94/cmd/release -repo ../../../.. next

# 2. the notes for it
go run ../Day-94/cmd/release -repo ../../../.. notes > /tmp/notes.md

# 3. everything CI runs, before the tag exists
make check

# 4. the repository itself
make audit

# 5. tag the commit that was tested, on a clean tree
git tag -a v0.2.0 -m "v0.2.0"
git push origin v0.2.0

# 6. prove the artifact can be rebuilt
go run ../Day-94/cmd/release verify -package ./cmd/api
```

Steps 1, 2 and 6 are Day 94's tooling; steps 3 and 4 are this repository's
Makefile. Nothing here is a judgement call except *when* to cut the release.

## What went into v0.2.0

Three user-visible changes, from `CHANGELOG.md`:

- **Added:** `POST /tasks/{id}/advance`, and `GET /tasks?status=`.
- **Changed (breaking for error-string parsers):** every non-2xx response now
  uses `{"error", "message"}`.
- **Fixed:** advancing a task twice returned `200` and did nothing; it now
  returns `409`.

That breaking change is why this is `0.2.0` and not `0.1.1`. Below 1.0.0 a
breaking change is a **minor** bump: `0.y.z` is defined as unstable, and Go
modules treat every `0.x` as compatible with every other. Reaching `1.0.0` is
the act of promising stability, and this API is not ready to promise it — see
[GAPS.md](GAPS.md).

## Verified

The tag command above is written out rather than run: this repository's history
belongs to its owner, and a tool that creates tags on someone's behalf is a
tool that will eventually create one nobody wanted. Everything around it —
`next`, `notes`, `verify`, `check`, `audit` — was run, and their output is in
the day's demo.
