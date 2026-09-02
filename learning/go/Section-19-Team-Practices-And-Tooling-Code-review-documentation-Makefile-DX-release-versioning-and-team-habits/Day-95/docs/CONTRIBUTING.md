# Contributing

## Setup

```sh
make setup     # tools and dependencies
make run       # the service on :8095
make help      # every target
```

There is nothing else. If a step is missing from that list, the list is wrong —
open a pull request against the Makefile.

## Before you push

```sh
make check     # fmt, vet, lint, race, audit - exactly what CI runs
```

Green locally means green in CI, because both run the same targets with the
same flags.

Optionally, `make hooks` installs a pre-commit hook that runs `gofmt`, `go vet`
and `go build` on the **staged files** — about a second. It deliberately does
not run the race detector: a hook that takes thirty seconds gets bypassed with
`--no-verify` within a week, and then it protects nothing.

## Tests

```sh
make test        # fast
make test-race   # the race detector
make cover       # total coverage
```

What a reviewer looks for, in order:

1. **Does a test fail if the change is reverted?** A test that passes either
   way is documentation, not a test.
2. **Is the failure mode tested**, not just the success? Most incidents are the
   untested branch.
3. **Are the boundaries covered** — empty, nil, zero, one, maximum?

`internal/tasks` is the domain, so its tests should read like the rules:
`TestCannotMoveBackwards`, `TestAdvancingTwiceIsRejected`.

## Commits and pull requests

Conventional Commits: `type(scope): imperative subject`. The subject completes
*"if applied, this commit will ___"*, which is how it reads in a revert and in a
generated changelog.

Fill in the pull request template. All four sections are load-bearing: **Why**,
**What**, **Test plan**, **Risk**. Each answers a question the reviewer would
otherwise ask — and asking costs a round trip measured in hours, while
answering costs five minutes while the change is still in your head.

Keep pull requests small. Under ~200 changed lines gets reviewed properly; past
~400 it gets skimmed, whatever anyone claims.

## Reviewing

`make review` runs the mechanical half of the checklist over the source:
package-level tests, function size, parameter count, context placement, leftover
TODOs. What it cannot decide — is this correct, is this the right design, does
this test assert anything real — is what your review is for.

## Changing the API

If a change is user-visible, add a `CHANGELOG.md` entry under `Unreleased`,
in the voice of someone deciding whether to upgrade. Breaking changes say what
to do, not just what changed.
