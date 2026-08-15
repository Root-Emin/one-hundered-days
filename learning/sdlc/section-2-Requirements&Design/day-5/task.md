# Day 5 — Requirements & Design: Gathering and Clarifying Needs

**Ongoing product:** *Vardiya* — a shift-scheduling and timesheet app for cafés and restaurants with 5–20 employees.

**Today's example feature:** **Shift Swap Request**
> The request from Deniz, the café owner, exactly as I received it:
> *"Let the employees swap shifts among themselves, and keep me informed."*

That sentence is not a requirement — it's a **request**. Today's job is to turn it into a requirement.

---

## Task 1 — Stakeholder Questions: 10 discovery questions

The questions fall into three categories; they don't just ask "what do you want," but also "why" and "when does it break."

### Understanding the problem and its context
1. **How does this work today?** When a barista calls in sick, what exactly happens — who calls whom, how long does it take, and where does the outcome get recorded?
2. **How many shift swaps happened in the last 30 days?** How many closed without a hitch, and in how many did the shift end up uncovered?
3. **What is this costing you right now?** What does an uncovered shift cost the business — lost revenue, slower service, your own personal time?
4. **What's the worst that happens if we don't solve it?** In other words, is this genuinely a problem that needs solving, or is it a "nice to have"?

### Clarifying the rules and the decision mechanism
5. **Who approves a swap — you, or the employees among themselves?** If two of them agree, is your approval still required, or do you just want to be notified?
6. **Can anyone swap with anyone?** Can a barista trade places with a cashier? Can a new hire take the weekend closing shift?
7. **When *should* a swap be rejected?** For example, if someone would go over 60 hours in a week, or would work a closing shift followed by an opening shift the next morning, should the system allow it?
8. **How close to the shift can a swap request still be opened?** Is a request that arrives 1 hour before the shift still valid?

### Setting the boundaries and the measure of success
9. **When we say this feature works, what will we look at?** Which number has to change for us to call it done — the number of uncovered shifts, or the time you spend on it?
10. **When a swap goes through, how should it show up in the timesheet?** Whose account do the hours go to, and how does that affect end-of-month payroll?

**Note:** There is deliberately no question like "should notifications be push or SMS?" That's a **solution** question, and it's premature at this stage.

---

## Task 2 — Problem vs Solution

Stakeholders describe the problem in the form of a solution. Part of the job is rewinding the solution-shaped sentence to extract the problem underneath it.

| # | What the stakeholder said (solution) | The real problem underneath | Validating question |
|---|---|---|---|
| 1 | "Let's set up a WhatsApp group and have them post there" | Swap requests aren't recorded; who accepted what and when is unclear, and disputes come up afterwards | "Did you have a disagreement over a swap last month? What happened?" |
| 2 | "Send employees a push notification" | Swap requests aren't seen in time, and shifts end up uncovered | "How long after a request is opened does no reply become a problem?" |
| 3 | "Add drag-and-drop to the calendar" | Editing shifts takes too many clicks; the owner spends 2 hours a week on it | "How long does preparing the weekly plan take you right now?" |
| 4 | "Let's add an export-to-Excel button" | The accountant wants the data in a specific format; today it's entered by hand and errors creep in | "What format does the accountant want the data in, and by what day of the month?" |
| 5 | "Have AI create the schedule automatically" | Tracking who is available when is hard; the owner starts from scratch every week | "When you're building the plan, which piece of information is hardest to find?" |
| 6 | "The app should work on mobile too" | Employees don't have computers; they need to see their shifts on their phones | "Where do employees look at their shifts today?" |

**Rule:** If the sentence mentions a technology, a screen, or a button, it's a solution. A problem statement fits this template:
> As a **[who]**, in **[what situation]**, I can't **[do what]**, and as a result **[what concrete damage occurs]**.

Example: *"As the café owner, when an employee calls in sick at the last minute, I can't quickly see who's available; as a result, about 3 shifts a month run understaffed."*

---

## Task 3 — Constraints

A constraint is the boundary that shapes a requirement. A requirement written without knowing the constraints is an unimplementable requirement.

### Business
- Budget: roughly 3 people × 3 months for the MVP; an external payment integration doesn't fit that budget.
- The pricing model is a monthly subscription, so the product has to demonstrate value within the first 15 minutes — a long setup is unacceptable.
- The target customer is a small business → we can't provide training/onboarding support; the product has to explain itself.
- The competition is WhatsApp and paper — meaning the competition is *free and already installed*. The product can't be "slightly better"; it has to make a visible difference.

### Time
- Must go live with 3 pilot cafés within 10 weeks (before the season starts).
- The timesheet export has to be ready by the 1st of the month — if it misses the month-end close, the customer goes back to their old method for another month.
- Sprint length is 2 weeks; that gives a 5-sprint MVP window.

### Legal & Compliance
- KVKK (Turkish data protection law): employee personal data (name, phone, working hours) is processed → a privacy notice, explicit consent, a defined retention period, and a deletion-request flow are mandatory.
- Labor law: the 45-hour work week, overtime calculation, weekly rest day, and night shift rules directly shape the timesheet.
- Data must be stored in Türkiye, or on a server with an appropriate legal basis.
- Authorization becomes a legal requirement: one employee must not be able to see another employee's pay/hours data.

### Technical
- Employees' phones are low-end Android; the app can't be heavy and must work on a poor connection.
- The team knows Node.js + PostgreSQL; learning a new stack doesn't fit the time constraint.
- A third-party service will be used for notifications → delivery isn't guaranteed by us, which is a risk.
- Shift data is time- and timezone-sensitive; shifts crossing midnight affect the data model from the very beginning (expensive to fix later).
- Offline case: an employee may want to check their shift on the metro → at minimum, the last viewed schedule should be kept in cache.

---

## Task 4 — Ambiguity Hunt

### The vague request
> **"Make the app faster."**

That sentence could mean at least 6 different things: page load, saving a shift, generating a report, notification delay, search results, or even "make it fast to use" (i.e. fewer clicks). As written, it can't be built — because there's no measure for saying "done."

### Clarifying questions
1. **What exactly are you doing when it feels slow?** Give me one example: which screen, which button?
2. **How long does it take, and how long did you expect it to take?** Did you measure it, or is it a feeling?
3. **Is it always slow, or only at certain moments?** For instance, Monday morning when everyone logs in at once?
4. **What device and connection are you on when this happens?** The computer at the office, or a phone on mobile data?
5. **What's the data size?** Is it slow at a café with 8 employees, or at a business with 20 employees and 6 months of history?
6. **By "slow," do you mean the waiting time, or that the task takes too many steps?** Is the system responding late, or are you clicking too much?
7. **When did the slowness start?** Was it always like this, or only after the latest release?
8. **What are you concretely losing because of it?** Are you putting off entering shifts, or have employees stopped opening the app?
9. **What's the acceptable threshold?** Could you say "if it opens in under 2 seconds, it's no longer a problem"?
10. **Is speed the priority, or accuracy?** If we showed slightly stale data to deliver the report in half a second instead of 3 seconds, would you accept that?

### The real requirement that emerges after the questions (sample output)

> **REQ-PERF-01**
> The weekly shift calendar screen must become interactive in **under 2 seconds at p95** on a mid-range Android device over a 4G connection, for a business account with 20 employees and 4 weeks of data.
> **Measurement:** Real user monitoring (RUM), including the Monday 08:00–10:00 peak window.
> **Acceptance criterion:** p95 < 2s across the 3 pilot businesses for 2 weeks.

The difference: the first sentence is a complaint, the second is a contract.

---

## Notes (reminders to self)

- **You don't understand the problem until you've asked "why" three times.** The first answer is always a solution.
- **A requirement that can't be measured isn't a requirement.** Everywhere you see the words "fast," "easy," "user-friendly," or "secure," a number is missing.
- **Constraints come before requirements.** A requirement written without knowing the budget, the regulations, and the stack the team already knows is technically correct but dead on arrival in practice.
- **Discovery isn't the place to talk technology.** "React or Vue" isn't this phase's question; this phase's question is "when, what, and why can't the user do something."