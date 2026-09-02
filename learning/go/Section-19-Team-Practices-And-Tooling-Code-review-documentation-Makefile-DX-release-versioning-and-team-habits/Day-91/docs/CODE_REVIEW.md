# Code review and git workflow

Four habits, each built as something a machine can check — because advice that
cannot be checked is advice that gets skipped on a Friday afternoon.

| Habit | Enforced by | Runs as |
|---|---|---|
| Short-lived branches, small PRs | `internal/changeset` | `cmd/prcheck` |
| PR descriptions that answer the reviewer's questions | `internal/prlint` | `cmd/prcheck -desc pr.md` |
| A review checklist in cost order | `internal/review` | read by humans |
| Feedback about code, not people | `internal/review.CheckTone` | read by humans |

```
go run ./cmd/prcheck                        # against the current branch
go run ./cmd/prcheck -desc pr.md -strict    # as a CI step
```

## 1. Trunk-based habits

Branch from `main`, live there for a day or two, merge back. The reason is not
ceremony — it is that **review quality collapses with size**:

| Reviewable lines | What actually happens |
|---|---|
| ≤ 200 | reviewed properly in one sitting |
| 200–400 | still fine, attention starts to thin |
| 400–1000 | skimmed; defect detection drops sharply |
| > 1000 | approved, not reviewed |

A 900-line pull request does not get twice the scrutiny of a 450-line one. It
gets *less*. `changeset.Review()` blocks past 1000 lines and warns past 400.

Generated files (`*.pb.go`, `go.sum`, `gen/`, `vendor/`) are excluded from the
count. Counting a 9,000-line protobuf regeneration as a huge review teaches
people to ignore the warning, and then it protects nothing.

Two other things the size check looks for:

- **Go code changed, no `_test.go` changed** → blocking. Not a law, a question:
  what proves this works, and what would catch it breaking?
- **Risky paths** → warning, so the right reviewer gets picked rather than the
  next one in the rotation: migrations (they run once, against production
  data), `go.mod` (a new module is code you now ship and must patch), anything
  named auth/token/crypto (needs the threat model), and `.github/workflows/` or
  `Dockerfile` (they run with credentials nobody else has).

## 2. Commit messages

Conventional Commits — `type(scope): subject` — because Day 94 derives the
changelog and the version bump from the history. A convention a machine reads
is a convention that pays for itself.

The subject is **imperative**: "add", not "added". It completes the sentence
*"if applied, this commit will ___"*, which is how it reads in a revert, a
cherry-pick and a generated changelog. 72 characters maximum; the body wraps at
72 because `git log` does not wrap for you.

```
fix(store): close rows on the error path        ok
wip                                             says nothing
Added retry logic to the client.                past tense, trailing period
```

## 3. PR descriptions

Four required sections, each answering a question the reviewer would otherwise
have to ask — and asking costs a round trip measured in hours, while answering
costs five minutes while the change is still in your head.

| Section | Question |
|---|---|
| **Why** | The motivation. A diff shows what changed; it can never show why. |
| **What** | The summary, so a reviewer knows what to expect before reading. |
| **Test plan** | How you verified it, and how a reviewer can. Specific commands. |
| **Risk** | What could break, who notices, how it rolls back. |

`prlint` blocks on a missing section and warns on a placeholder one — because
`## Test plan` followed by `n/a` satisfies a naive check and tells the reviewer
nothing. "Tests pass" is not a test plan; **"`TestConcurrentDeliveriesProcessOnce`
fails on the parent commit"** is.

## 4. The review checklist, in cost order

The order is the useful part. A reviewer who starts with naming runs out of
attention before reaching the concurrency bug — and the author gets three rounds
of style comments on code that does not work.

1. **Correctness** — the error path, the boundaries (empty/nil/zero/one/max),
   what happens if it runs twice or concurrently.
2. **Security** — is the input attacker-controlled and validated at the
   boundary; does any secret reach a log, an error or a response.
3. **Tests** — does a test fail if this change is reverted; is the *failure*
   mode tested.
4. **Design** — is this the smallest change that solves the problem; what does
   the next feature on top of it look like.
5. **Readability** — does a newcomer understand *why*; are the names still
   accurate after the code moved.
6. **Performance** — is there a measurement, or is this a guess.

## 5. Giving feedback

Every poor review comment has the same shape: it comments on the **person or
their judgement** instead of the code. The fix is always one of three moves —
describe the code, ask about the intent, or state the consequence.

| Instead of | Write |
|---|---|
| "This is wrong." | "blocking: [correctness] this returns nil when orders is empty, and the caller dereferences it on line 40 — a panic on the first customer with no orders." |
| "Why did you use a mutex here?" | "question: [design] what does the mutex protect? If it is only the counter, `atomic.Int64` would drop the lock — but I may be missing an invariant." |
| "Just use a map." | "nit: [performance] a map would make the lookup O(1) — though with n<10 the slice is likely faster. Fine either way." |
| "Missing tests." | "blocking: [tests] the retry path (client.go:88) has no test — if I invert that condition, does anything fail?" |

Three rules that do most of the work:

- **Mark severity explicitly.** `blocking:` means "I will not approve this";
  `nit:` means "optional, merge without it if you disagree"; `question:` means
  "I might be wrong". `nit:` is a protocol, not decoration — teams that use it
  consistently argue less, because the author knows which comments are
  negotiable.
- **Leave room to be wrong.** You are, about a third of the time. "What am I
  missing?" costs nothing and prevents an argument.
- **Say the consequence, not the verdict.** "This is bad" carries no
  information the author can act on. "This leaks a connection per error"
  carries all of it.

## 6. Receiving feedback

- A comment on your code is not a comment on you. This is easy to write and
  hard to feel at 6pm, which is why the norms above exist on the *reviewer's*
  side too.
- Answer every comment, even with "done" or "leaving as is because X". An
  unanswered comment reads as ignored.
- Disagreement is fine and cheap: say why, once. If it is still contested after
  one exchange, that is a conversation, not a thread — the async medium has run
  out of bandwidth.
- "Good catch" costs nothing.

## What none of this can do

`prcheck` cannot decide whether the code is **correct** — which is the entire
point of review. It cannot tell a blunt-but-kind comment from a cruel one,
cannot know whether a test plan is honest, and cannot judge whether a design
survives the next feature.

That is exactly why the mechanical half is worth automating. Every minute a
reviewer spends noting a missing test plan is a minute not spent on the
concurrency bug that only a human will find.
