# Gaps

What is missing, written down honestly. A gap that is recorded is a decision;
a gap that is not is a surprise for whoever finds it.

Each entry says **why it is not done yet** and **what would trigger doing it** —
the second half is what stops the list from being a wish that never gets read.

## Product

| Gap | Why not yet | Trigger |
|---|---|---|
| No persistence | The MVP exists to exercise the process, and a store that owns a database cannot be tested without one. | The first time anything real depends on the data surviving a restart. |
| No pagination on `GET /tasks` | Returns everything; fine at hundreds. | ~10,000 tasks, or the first client that times out listing. |
| No task deletion | Nothing has needed it, and it raises a real question: a deleted task loses its history, the same way a backwards transition does. | A request with a reason attached — GDPR erasure would be one. |
| No authentication | It would sit behind a gateway. | The moment it is exposed without one. This needs a design, not a middleware. |

## Process and tooling

| Gap | Why not yet | Trigger |
|---|---|---|
| No `LICENSE` | This lives inside a larger repository whose licensing is its owner's call, not this directory's. | Extracting it into a repository of its own. |
| The audit checks that files **exist**, not that they are **true** | Truth is not machine-checkable. A README can score 100% and still be wrong. | Nothing; this is a permanent limit worth stating rather than a task. |
| `selfreview` cannot check unchecked errors | Deciding "was this error handled?" needs type information and data flow. `errcheck` and `staticcheck` already do it properly, and a half-implementation would produce false positives. | Never — run the real linters instead. That is what `make lint` is for. |
| The open size nits are deliberately unfixed | `DefaultChecks` (61 lines) and `routes` (62) are **tables**; splitting them would make them harder to read, not easier. `Review` (78), `Audit` (62) and the demo's own `demoAudit` (64) are walk functions whose steps do not want separate names. | A reviewer disagreeing. The number's job is to make the outlier visible, not to be obeyed. |
| No integration test against a running server | The handlers are thin and the domain is covered. | The first bug that lives in the wiring rather than in either half. |
| Coverage is not gated | A percentage target rewards testing the easy paths. Day 69 has a real coverage gate if one is wanted. | A drop that nobody noticed. |

## The honest summary

The **process** in this repository is in better shape than the **product**,
which is the correct order for a 100-day exercise and the wrong order for a
real service. If this became real, persistence and authentication come before
anything else on this page.

The `nice`-severity audit finding (no licence) is left open on purpose: a
report with zero findings is a report nobody reads carefully, and this one is
accurate.
