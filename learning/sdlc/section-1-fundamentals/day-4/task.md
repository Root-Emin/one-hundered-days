# Day 4 — SDLC Fundamentals: Practice

**Product idea:** *Vardiya* — a shift-scheduling and timesheet app for cafés and restaurants with 5–20 employees.
**Scope:** Web dashboard (owner) + mobile view (employee). Create shifts, notify employees, handle leave and swap requests, and export monthly worked hours to accounting as CSV.

---

## Task 1 — Case Study: Which model? → **Hybrid**

Choice: **Hybrid** (Waterfall for the timesheet/compliance side, Agile for scheduling & UX).

- **The product has two different natures.** The shift-scheduling screen will evolve alongside its users (usage habits can't be known up front → Agile), but the timesheet output is bound to labor law and accounting formats; its rules can be written down at the start and won't change → Waterfall discipline fits that module.
- **The cost of error is asymmetric.** A misplaced button on the scheduling screen = one sprint of rework. A wrong overtime calculation in the timesheet = legal and financial damage to the business. It makes sense to lock down the high-cost part up front with a full specification (SRS + sign-off).
- **Customer access is limited but real.** Demos are possible every two weeks with 3 pilot cafés; there is no daily on-site customer representative (which pure Agile wants). This points to a Hybrid with sprints but lightweight ceremonies.
- **Small team, short timeline.** A 3-person team can't carry the documentation load that pure Waterfall produces, and it also lacks the automation/maturity that pure Agile requires. Hybrid balances the load by making documentation mandatory only in the risky module.
- **Market uncertainty is moderate.** The assumption that "cafés manage shifts over WhatsApp" needs validating; shipping an early version and collecting feedback is essential. Waterfall's deliver-once-after-4-months model would delay that validation far too long.

**In short:** Design the fixed, expensive-to-get-wrong part up front; discover the uncertain, cheap-to-get-wrong part through iteration.

---

## Task 2 — Phase Checklist (expected artifacts per phase)

### 1. Planning / Discovery
- [ ] Vision and scope document (1 page: problem, target user, "what we won't build")
- [ ] Stakeholder list (business owner, employee, accountant)
- [ ] Simple business case: target of 50 businesses × ₺X/month, estimated development cost
- [ ] High-level roadmap (MVP → v1 → v1.5)
- [ ] Assumption and risk list (first draft)

### 2. Requirements / Analysis
- [ ] Product backlog (in user story format) — the Agile side
- [ ] **SRS** for the timesheet module: overtime, weekly rest day, night shift rules — the Waterfall side
- [ ] Acceptance criteria for each story (Given/When/Then)
- [ ] 2 personas (Deniz, the café owner; Ece, the part-time barista)
- [ ] Requirements traceability matrix (for the timesheet module only)

### 3. Design
- [ ] System architecture diagram (client / API / database / notification service)
- [ ] Data model — ERD (Employee, Shift, Availability, LeaveRequest, TimeEntry)
- [ ] API contract (OpenAPI/Swagger draft)
- [ ] Wireframes + clickable prototype (weekly shift calendar screen)
- [ ] NFR list: under 2s load time for up to 20 users, KVKK-compliant data retention
- [ ] Draft threat model (authentication, authorization: an employee must not see another's pay)

### 4. Implementation
- [ ] Source code + repo conventions (branching strategy, commit format)
- [ ] Code review records (PRs)
- [ ] Unit tests (especially for the timesheet calculation functions)
- [ ] CI pipeline configuration
- [ ] Database migration scripts
- [ ] Draft changelog / release notes

### 5. Testing
- [ ] Test plan (scope, environments, exit criteria)
- [ ] Test cases + an edge-case table for the timesheet (mid-month hire, shift crossing midnight, public holidays)
- [ ] Bug reports and severity levels
- [ ] Regression test suite (automated)
- [ ] UAT sign-off form — signed by the pilot café
- [ ] Verification report of the accounting CSV export, reviewed with a real accountant

### 6. Deployment
- [ ] Release notes
- [ ] Runbook: how to deploy, how to roll back
- [ ] Rollback plan and its trigger conditions
- [ ] Environment/infrastructure configuration (env variables, IaC)
- [ ] Monitoring and alerting setup (error rate, API latency, failed notifications)
- [ ] User guide / onboarding email

### 7. Maintenance / Operations
- [ ] Support and SLA document (response time commitment)
- [ ] Incident records and postmortem template
- [ ] Usage analytics report (monthly: active businesses, number of shifts created)
- [ ] Technical debt register
- [ ] Improvement backlog fed by user feedback

---

## Task 3 — Risk Flag

**Riskiest phase: Requirements / Analysis.**
Specifically, eliciting the timesheet rules. Why: a mistake in this phase is silent — the code runs, the screen opens, the CSV is produced, but the numbers are wrong. The error surfaces months later in accounting; by then the incorrect data has already been generated, customer trust is damaged, and legal liability is likely in play. On top of that, the cost of fixing a requirements error is many times higher than fixing one in the implementation phase, because design, code, tests, and already-produced data are all affected retroactively.

**Mitigation plan:**
1. **Expert validation:** After writing the timesheet rules, pay a certified accountant for a 2-hour review; keep the approved SRS as the reference document.
2. **Specification by example:** Instead of abstract rule statements, write out 15 real scenarios in a table (input shifts → expected output hours/pay). That table doubles as the test suite.
3. **Early thin vertical slice:** In the first 2 weeks, produce an end-to-end timesheet from one café's real data for the previous month; compare the result against the payroll they calculated by hand.
4. **Traceability:** Give every timesheet rule an ID (PAY-01, PAY-02, …) and reference that ID in the code and test files, so that when a rule changes, every affected place is found in a single search.
5. **Change control:** Requirement changes in the timesheet module only enter through written approval; no rule changes via hallway conversation.

---

## Task 4 — Teach-back Script (~1 min 45 sec)

> "So you're curious about SDLC — it's almost the same thing as opening a café.
>
> Say you're going to open one. First you sit down and think: who is it for, where will it be, how much money do you have? We call that **planning**.
>
> Then you list exactly what you want: seating for 30, an espresso machine, breakfast on the menu too. That's **requirements analysis** — in software we also start by pinning down 'what exactly is this program going to do.'
>
> Next you sit with an architect and draw the plan: where the kitchen goes, where the outlets go, how the plumbing runs. That's **design**. In software, too, the system's blueprint is drawn before any code is written — because moving an outlet after the wall is up is expensive.
>
> Then construction starts: the workers arrive, the work gets done. That's **development**, the actual coding part. It's the only thing people picture when they hear 'software,' but as you can see, it's just one piece of the whole.
>
> Then, before opening day, you try everything: does the machine work, does the water run, does the order system print the right receipt? That's **testing**. The point is for us to find the bugs before the customer does.
>
> Then you open the doors — **deployment**. And the job doesn't end there: there's daily upkeep, customers saying 'I wish it also had this,' things breaking. We call that **maintenance**, and it's the longest stretch of a product's life.
>
> SDLC is just the name for those six steps. There are different ways of working through them too: some teams finish each step completely before moving to the next — *Waterfall*. Others move in small pieces and show something working every 2 weeks — *Agile*. And most teams mix the two.
>
> In one sentence: SDLC is the name for the difference between 'sit down and code something' and 'ship a product people can rely on.'"