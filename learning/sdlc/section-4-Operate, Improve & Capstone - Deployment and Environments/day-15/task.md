# Day 15 — Operate, Improve & Capstone: Retrospectives and Continuous Improvement

**Ongoing product:** *Vardiya* — a shift-scheduling and timesheet app for cafés and restaurants with 5–20 employees.
**Retro scope:** The MVP pilot — Sprints 1–5, three pilot cafés, two full month-end payroll exports (March and April 2026).
**Team:** me (product/dev), Burak (backend), Selin (frontend/design).
**Personas involved:** Deniz (business owner), Ece (part-time barista), Levent (external accountant).

---

## Task 1 — Retro Format: Went Well / Improve / Actions

**Format used:** a solo retro, timeboxed to 45 minutes, run against evidence rather than memory — the sprint board, the bug list, the pilot demo notes, and the two payroll exports we shipped. The rule I set for myself: no item goes on the list unless I can point to something that actually happened.

### ✅ Went well

| # | What went well | Evidence |
|---|---|---|
| 1 | **The thin vertical slice on payroll came first.** We produced an end-to-end timesheet from one café's real previous-month data in week 2. | It surfaced a wrong overtime boundary (weekly 45h counted per calendar week vs. per contract week) while it cost us 2 days. At month end it would have cost a wrong payroll run. |
| 2 | **Specification by example paid for itself twice.** The 15-scenario input→output table became the test suite verbatim. | 11 of the 14 payroll unit tests were generated straight from that table; no separate test-design effort was needed. |
| 3 | **Rule IDs (PAY-01, PAY-02, …) made a rule change cheap.** | When the night-shift window changed from 22:00–06:00 to 20:00–06:00, one search found the rule doc, the function, and the 3 tests. Change took under an hour. |
| 4 | **Shipping US-02 (employee shift view) early bought adoption.** | By week 4, 31 of 38 pilot employees had opened the app at least twice a week — that made the swap feature land on an audience that already existed. |
| 5 | **Levent (the accountant) reviewed the CSV before we built the exporter.** | The March export was accepted on the first attempt. Zero rework on the format that carries the product's money value. |

### ⚠️ Could improve

| # | What didn't go well | Cost |
|---|---|---|
| 1 | **Notification failures were invisible to us.** The third-party provider silently dropped a batch on 18 April; we heard about it from Deniz, not from a monitor. | 2 shifts uncovered; a trust hit with the customer that a $0 alerting rule would have prevented. |
| 2 | **The concurrent-takeover case (US-03 / AC-5) escaped to production.** It was written as an acceptance criterion but never had an automated test. | Two employees were both told they had the same shift. Found by the customer — a textbook escaped defect. |
| 3 | **Pilot feedback lived in WhatsApp and in my head.** | Reconstructing this retro took 40 minutes of scrolling. At least 3 requests from Deniz were never entered anywhere, and one resurfaced as a "you never did this" conversation. |
| 4 | **L-sized stories were consistently underestimated.** PAY-7 (accountant's column layout) took roughly 3× the estimate. | Sprint 4 spilled; the night-shift rules (PAY-4) slipped out of the MVP under time pressure rather than by decision. |
| 5 | **"Done" was never defined for feedback.** A demo would end with nods and no artifact. | We repeatedly discussed the same three ideas across sprints without ever deciding on them. |

### 🎯 Actions

Five observations, three actions. Anything I can't assign an owner and a date to stays an observation, not an action — otherwise the list is just a nicer form of complaining.

| ID | Action | Owner | Due | Done when |
|---|---|---|---|---|
| **ACT-1** | Add a regression test for every acceptance criterion that describes a **race or concurrency** case, starting with US-03/AC-5. | Burak | 28 Aug 2026 | AC-5 test is in CI and fails on a deliberately reverted fix. |
| **ACT-2** | Instrument notification delivery and alert on failure. | Burak | 28 Aug 2026 | See ACT-2 detail in Task 2. |
| **ACT-3** | Single intake channel + weekly triage for all pilot feedback. | me | 22 Aug 2026 | See ACT-3 detail in Task 2. |
| **ACT-4** | Re-estimate any story sized L by splitting it into at most M-sized slices before it enters a sprint. No L enters a sprint again. | me | Sprint 6 planning, 24 Aug 2026 | Sprint 6 board contains zero L-sized items. |

---

## Task 2 — Actionable Outcomes: turning 2 complaints into improvement actions

A complaint describes a feeling. An action names a change, an owner, a date, and a way to tell whether it worked. Here are the two loudest complaints from the retro, converted.

### Complaint 1 → ACT-2

> **The complaint, as it was said:** *"Notifications are unreliable and we always hear about it from the customer."*

| | |
|---|---|
| **What's underneath it** | We depend on a third-party notification service, we have no visibility into delivery, and the failure path is silent — a dropped notification looks identical to a notification nobody opened. |
| **The action** | Log every notification send with its provider response. Compute a rolling 1-hour delivery-failure rate. Alert to the team channel when it exceeds 2%. Add a fallback: if a shift-swap notification isn't delivered within 10 minutes, surface an in-app banner on next open, and flag the shift as "employee not confirmed" on Deniz's schedule screen. |
| **Owner** | Burak |
| **Due** | 28 Aug 2026 (end of Sprint 6) |
| **How we'll know it worked** | For the next 30 days: (a) we detect every delivery incident before the customer reports it — target 100% of incidents detected internally first; (b) zero uncovered shifts attributed to an unseen notification. |
| **What it is not** | It is not "switch providers." We don't yet know whether the provider or our integration is the cause — measuring comes before replacing. |

### Complaint 2 → ACT-3

> **The complaint, as it was said:** *"Feedback just evaporates — Deniz tells me something at the café and nothing ever happens."*

| | |
|---|---|
| **What's underneath it** | There is no intake point. Feedback arrives through four channels (in-person, WhatsApp, demo calls, support email) and lands in none of them permanently. Nothing is refused and nothing is promised, so both sides end up frustrated. |
| **The action** | One intake: a `feedback` label in the issue tracker, with a 4-field template (who said it, what they were trying to do, what happened instead, date). Everything — including hallway remarks — gets written there within 24 hours, by whoever heard it. Every Monday, a 30-minute triage: each item is dispositioned as *backlog story / SRS change request / bug / won't do*, and the person who raised it gets a one-line reply either way. |
| **Owner** | me |
| **Due** | Template live by 22 Aug 2026; first triage on 24 Aug 2026 |
| **How we'll know it worked** | After 4 weeks: 100% of items have a disposition (nothing sits untouched), median time from "said" to "logged" is under 24 hours, and zero items from the last month exist only in WhatsApp. |
| **What it is not** | It is not "we'll build everything Deniz asks for." *Won't do* is a valid outcome — as long as it's said out loud. |

---

## Task 3 — Measure Once: one process metric for next month

**The metric I'm choosing: escaped defects per release.**

| | |
|---|---|
| **Definition** | Any defect found in production, after a release, by anyone other than the team — customer, employee, accountant, or monitoring. Counted against the release that introduced it. |
| **Why this one** | Our whole risk profile is that *this product's errors are silent*. A wrong payroll number doesn't crash anything; it just gets paid. The concurrent-takeover bug and the notification drop were both discovered by the customer. Escaped defects measure exactly the thing that erodes trust in a product whose main output is a number someone else acts on. |
| **How it's measured** | Manually, from the issue tracker's `escaped` label. No new tooling. Roughly 5 minutes per release — if a metric needs a dashboard project to exist, it won't survive a 3-person team. |
| **Baseline** | Pilot period: 7 escaped defects across 5 releases ≈ **1.4 per release**. Two of them were severity-high (payroll- or notification-related). |
| **Target for next month** | Under 1.0 per release, and **zero** high-severity escapes in the payroll module. |
| **Review date** | 21 Sep 2026, in the Sprint 8 retro. |
| **Runner-up, and why I dropped it** | **Cycle time.** It's a good metric, but our problem right now isn't speed — the pilot shipped roughly on schedule. Optimising cycle time while escaped defects are the actual pain would just make us produce wrong answers faster. I'll revisit it once escapes are stable. |

**Guardrails I'm setting for myself:**
- **One metric, not five.** Five metrics means no metric — nobody looks at a dashboard nobody owns.
- **The metric is a thermometer, not a target to game.** If escaped defects hit zero because we stopped labelling them, the number is worthless. The label is applied at triage, not by the person who wrote the bug.
- **Trend over value.** One release with 3 escapes doesn't mean anything. Four weeks of direction does.
- **A metric with no review date is decoration.** 21 Sep is on the calendar.

---

## Task 4 — Feedback Loops: how user feedback re-enters requirements

The point of a retro is process improvement. The point of a feedback loop is *product* improvement — and it only counts as a loop if it comes back around to the requirements, not if it dead-ends in a conversation.

### Where feedback comes from

| Source | Who | Typical content | Arrives as |
|---|---|---|---|
| Fortnightly pilot demo | Deniz + 2 other owners | Workflow friction, missing capability | Spoken, in a call |
| In-app feedback button | Ece and other employees | Small usability complaints | Written, structured |
| Support email | Deniz, Levent | Something is broken or a number looks wrong | Written, unstructured |
| Month-end export review | Levent (accountant) | Format and calculation discrepancies | Spreadsheet comments |
| Product analytics | Nobody — the system | Abandoned flows, unused features | Weekly numbers |
| Monitoring & error logs | Nobody — the system | Failures the user never reported | Alerts |

Two of those six sources are silent — analytics and monitoring. Those are the ones that tell you about the users who don't complain, they just stop opening the app.

### The loop, step by step

```
1. CAPTURE      any source → issue tracker, `feedback` label, within 24h
                        ↓
2. TRIAGE       Monday, 30 min → disposition + one-line reply to the reporter
                        ↓
3. CLASSIFY     bug / usability / new capability / rule change
                        ↓
4. ROUTE   ┌────────────┴────────────┐
           │                         │
     Agile side                Waterfall side
  (scheduling & UX)          (payroll module)
  → user story +             → written change request
    acceptance criteria      → accountant review if a rule changes
  → sized, prioritised       → SRS updated, PAY-xx ID assigned
    into the backlog         → traceability matrix updated
           │                         │
           └────────────┬────────────┘
                        ↓
5. PRIORITISE   sprint planning: value × risk (Day 6 method)
                        ↓
6. BUILD & VERIFY   acceptance criteria written from the original
                    feedback wording, not from my paraphrase
                        ↓
7. CLOSE THE LOOP   tell the person who raised it that it shipped
                        ↓
8. MEASURE      did the behaviour actually change? (analytics)
                        ↓
                └──→ back to step 1
```

### The two rules that make this a loop rather than a queue

1. **Step 7 is non-negotiable.** If the reporter is never told what happened, they stop reporting — and you lose the input long before you notice. Closing the loop is what keeps the loop fed.
2. **Payroll feedback can never take the shortcut.** A comment like "this number looks wrong" must not become a direct code fix. It goes through the change request → accountant review → SRS update path, because an undocumented rule change is exactly the silent error we flagged as the project's top risk back on Day 4.

### Where the loop currently leaks

| Leak | Fix | Where it's tracked |
|---|---|---|
| Verbal feedback at demos never gets written down | ACT-3 intake template | Task 2 |
| Nobody looks at analytics — the silent source | Add a 10-minute weekly analytics read to the Monday triage | Sprint 6 |
| Reporters are never told the outcome | One-line reply is part of the triage definition of done | ACT-3 |
| Accepted items are never re-checked against the feedback that caused them | Add "which feedback item does this close?" to the story template | Sprint 6 |

---
