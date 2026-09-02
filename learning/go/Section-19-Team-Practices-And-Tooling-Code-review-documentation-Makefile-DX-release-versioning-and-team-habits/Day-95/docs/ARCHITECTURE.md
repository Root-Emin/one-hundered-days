# Architecture

## The shape

```
  cmd/api              HTTP: routing, status codes, JSON encoding
     │                 knows about HTTP, knows nothing about task rules
     ▼
  internal/tasks       the domain: three states, one invariant
                       knows nothing about HTTP

  the repository checking itself:

  internal/repoaudit   are the files a newcomer needs present, and do they
                       answer the question they exist to answer?
  internal/selfreview  the mechanical half of the review checklist, applied
                       to this repository's own source
```

## The dependency rule

`internal/tasks` must not import `net/http` or anything transport-shaped. The
test for it: a status transition has to be testable without starting a server,
and a gRPC front end has to be able to reuse the package unchanged.

## Decisions

### Tasks move forward only

`todo` → `doing` → `done`, never backwards, and never to the same state.

Reopening a task is a *new* task that links to the old one. A status field that
moves both ways loses the record of what actually happened: "this was done, then
undone, then done again" becomes indistinguishable from "this was done".

Advancing to the same state is rejected rather than ignored, because a repeated
advance is almost always a double submit, and silently accepting it hides the
bug in the caller.

### An invalid transition is 409, not 400

The request was well formed; the *state* said no. A 400 tells the client to fix
its request, which will not help — the fix is to look at the task's current
status. This is what makes a client's error handling possible.

### Error bodies have one shape

`{"error": "<code>", "message": "..."}` for every non-2xx. One shape means a
client writes one decoder; different shapes per endpoint means five, one of
which is wrong. `error` is a stable code to switch on; `message` is for humans
and changes freely.

The mapping from domain error to status code lives in one function
(`writeStoreError`). Scattered, the same failure becomes a 400 on one endpoint
and a 500 on another, and clients cannot tell which errors are worth retrying.

### Sentinel errors, not error strings

`tasks.ErrNotFound` and friends are part of the API; their message text is not.
Callers use `errors.Is`. A caller matching on strings breaks on the next
release, and would deserve to.

### In memory, no database

Persistence is not the point of this MVP, and a store that owns a database
cannot be tested without one. When it arrives it goes behind the same methods,
and the tests here do not change.

## What is deliberately absent

| Missing | Why, and when to revisit |
|---|---|
| Persistence | The MVP is a process exercise. Revisit the moment anything real depends on the data surviving a restart. |
| Authentication | It would sit behind a gateway. If that stops being true it needs a design, not a middleware bolted on. |
| Pagination | `GET /tasks` returns everything. Fine at hundreds; revisit at ~10,000 tasks. |
| Task deletion | Nothing has needed it. Adding it raises a question this design has an opinion about — a deleted task loses its history, the same way a backwards transition does. |

The last column is the part that saves time. "We chose not to paginate, revisit
at ~10,000" ends an argument that otherwise restarts every six months with a
new engineer.
