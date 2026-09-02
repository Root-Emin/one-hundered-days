## Why

<!-- The motivation. A diff shows what changed; it can never show why. -->

## What

<!-- The summary, so a reviewer knows what to expect before reading a line. -->

## Test plan

<!-- How you verified it, and how a reviewer can. Specific commands, and a test
     that fails on the parent commit. "Tests pass" is not a test plan. -->

## Risk

<!-- What could break, who notices, and how it rolls back. -->

## Checklist

- [ ] `make check` passes
- [ ] A test fails if this change is reverted
- [ ] Errors are wrapped with context, and no secret reaches a log
- [ ] CHANGELOG.md updated if this is user-visible
