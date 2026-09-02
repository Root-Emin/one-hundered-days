<!--
Four sections, four questions a reviewer would otherwise have to ask you.
Asking costs a round trip measured in hours; answering costs five minutes.

cmd/prcheck reads this file, so a missing section fails before review starts.
-->

## Why

<!-- The motivation. A diff shows what changed; it can never show why.
     Link the issue, describe the bug, or state the requirement. -->

## What

<!-- A summary of the change, so the reviewer knows what to expect before
     reading a single line. Note anything deliberately left out. -->

## Test plan

<!-- How you verified this, and how a reviewer can. Be specific:

     - go test ./internal/store/... -run TestOrdersForCustomer
     - ran the demo against a seeded database, confirmed X
     - added TestFoo, which fails on the previous commit

     "Tests pass" is not a test plan. -->

## Risk

<!-- What could break, who would notice, and how it gets rolled back.
     "Low risk: this only touches the demo command" is a fine answer.
     "None" usually means the question was not considered. -->

## Checklist

- [ ] The commits are small and their messages say what applying them does
- [ ] Tests fail if this change is reverted
- [ ] Errors are wrapped with context, and no secret reaches a log
- [ ] `make check` passes locally (fmt, vet, lint, test, race)
