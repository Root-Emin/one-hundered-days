# Releasing

A release is a promise. The version says how much of it a caller must read, the
notes say what to do about it, the build says which bytes are running, and the
deprecation policy says what will still work next quarter.

```
go run ./cmd/release next     # the version the commits imply
go run ./cmd/release notes    # the notes for that version
go run ./cmd/release verify -package ./cmd/api
```

## 1. The version number

| Bump | Means | The caller |
|---|---|---|
| PATCH | bug fixes | upgrades blind |
| MINOR | new features, nothing removed | upgrades blind |
| MAJOR | something they depend on changed or is gone | reads the notes |

Break the promise once and every consumer pins an exact version and reads every
diff — which costs them the entire benefit of versioning.

**The bump comes from the commits, not from memory.** `feat` → minor, `fix` /
`perf` / `revert` → patch, `feat!` or a `BREAKING CHANGE:` footer → major. A
commit marked breaking in March is still a major release in April, whether or
not anyone remembers it at tagging time.

**Below 1.0.0, a breaking change is a MINOR bump.** `0.y.z` is defined as
unstable, and Go modules treat every `0.x` as compatible with every other.
Shipping `1.0.0` is the act of promising stability — it should be deliberate,
not the side effect of a `!` in a commit message.

Ordering rules a string comparison gets wrong, all covered by tests:

```
v1.9.0      < v1.10.0        numeric, not lexical
v1.0.0-rc.1 < v1.0.0         a release outranks its own pre-releases
v1.0.0-rc.2 < v1.0.0-rc.10   numeric identifiers compare numerically
v1.0.0+a    = v1.0.0+b       build metadata is ignored
```

## 2. Release notes

Written for the person deciding whether to deploy at 4pm on a Thursday. They
need three things, in this order:

1. **does this break anything I depend on, and what do I do about it**
2. what is new
3. what was fixed

So breaking changes come first, each with the `BREAKING CHANGE:` footer's
migration text — including the paragraph after the blank line, which is usually
the half that says what to do. A breaking change with **no** migration note is
called out as a gap rather than rendered as if it were complete.

Refactors, tests, chores and CI changes are counted and omitted:

```
_2 internal change(s) (refactors, tests, tooling) are omitted; see the git log._
```

They are real work and invisible to a deployer; listing them buries the three
things that matter.

## 3. Reproducible builds

When an incident points at v1.4.2, you rebuild v1.4.2 and need the artifact
that is actually running. If the rebuild differs, you cannot tell whether the
difference is the bug.

```
go build -trimpath -buildvcs=false \
  -ldflags "-s -w -X <pkg>/internal/buildinfo.Version=v1.3.0 \
                   -X <pkg>/internal/buildinfo.Commit=e5f6a7b8c9d0 \
                   -X <pkg>/internal/buildinfo.BuildTime=2026-09-01T18:30:00Z" \
  -o dist/api ./cmd/api
```

Measured on this machine: two builds in **different** temporary directories
produced byte-identical binaries —
`a9b7f339a00a05f306eafde7c4ce24afd2de65bcb6cea81e366b39bc2998c33c`, 1,742,498
bytes. Different directories on purpose: building twice in one place hides a
path dependency that a CI runner would expose.

And the counter-example, also measured: the same build stamped with the
**build** time one second apart produced two different digests. A build
timestamp makes reproducibility impossible by construction — **stamp the commit
time.**

What breaks reproducibility, in the order it bites:

| Cause | Fix |
|---|---|
| absolute paths in panics and debug info | `-trimpath` |
| a build timestamp | stamp the **commit** time |
| VCS stamping (commit + dirty flag) | `-buildvcs=false`; a dirty tree is unreproducible by definition |
| the local C toolchain | `CGO_ENABLED=0` where possible |
| the Go version | pin it — a different compiler emits different code |
| floating dependency versions | `go.sum`, never a floating version |

Record the digest and the environment (`go1.26.5 darwin/arm64 CGO_ENABLED=0`)
next to the artifact. That is what turns "rebuild v1.4.2" into a procedure
rather than an archaeology project.

## 4. Deprecating safely

A deprecation is a promise with three parts. Leave any one out and "we
announced it" turns into an outage:

| Part | Why |
|---|---|
| **what replaces it** | a warning with no alternative cannot be acted on |
| **when it goes** | a date, not "eventually" — without one there is no reason to migrate today, so nobody does |
| **how to migrate** | the actual change, ideally a diff |

In the code:

```go
// FindProduct looks a product up by SKU.
//
// Deprecated: use catalog.Store.Get instead, which returns a typed error
// rather than a bare nil. This will be removed after 2026-12-01; see
// https://docs.example.com/migrations/products for the two-line change.
```

`internal/deprecation` scans for these markers and reports the ones missing a
replacement or a date — and the ones whose date has **passed**, which need a
decision rather than a quiet extension: either remove it, or admit it is not
going away and drop the marker that is lying to every caller.

At runtime, tell the client — most never read a changelog. RFC 8594:

```
Deprecation: true
Sunset: Tue, 01 Dec 2026 00:00:00 GMT
Link: <https://docs.example.com/migrations/products>; rel="deprecation"; type="text/html"
Link: <https://api.example.com/products/{sku}>; rel="successor-version"
```

A client library can act on those without anyone reading anything. The
accompanying log line fires **once per process**, not per request: a deprecated
endpoint under load would otherwise produce a line per call, which costs money
and gets the warning filtered out.

## The checklist

1. The commits carry their type, and breaking ones carry a migration note.
2. The version comes from the commits, not from someone's memory.
3. The notes lead with the breaking changes.
4. The tag is on the commit that was tested, and the tree is clean.
5. The build is reproducible: `-trimpath`, `-buildvcs=false`, commit time,
   pinned Go.
6. The digest is recorded with the artifact, so a rebuild can be checked.
7. Anything removed was deprecated first, with a date that has passed.
