# Day 6 — Requirements & Design: User Stories and Acceptance Criteria

**Ongoing product:** *Vardiya* — a shift-scheduling and timesheet app for cafés and restaurants with 5–20 employees.
**Personas:** Deniz (business owner), Ece (part-time barista), Levent (external accountant).

---

## Task 1 — Write User Stories (5 of them)

### US-01 — Creating the weekly schedule
> As a **business owner**, I want to **build the weekly shift schedule from a single screen**, **so that I can finish the plan in 15 minutes instead of preparing it from scratch on paper/Excel every week.**

- Value: cuts down Deniz's ~2 hours of repetitive weekly work.
- Size: L

### US-02 — Employee viewing their own shifts
> As an **employee**, I want to **see this week's shifts on my phone**, **so that I don't have to ask anyone "what time was I starting tomorrow?"**

- Value: reduces the daily question traffic reaching Deniz; cuts down late arrivals.
- Size: S

### US-03 — Opening a shift swap request
> As an **employee**, I want to **open a swap request for a shift I can't take**, **so that I can find someone to cover me without looking like I've walked off the job.**

- Value: lowers the number of last-minute uncovered shifts.
- Size: M

### US-04 — Approving a swap request
> As a **business owner**, I want to **approve or reject a shift swap that employees have agreed on**, **so that the schedule doesn't change without my knowledge and staffing balance isn't broken.**

- Value: control and a record; ends the "but I told you" disputes that surface later.
- Size: M

### US-05 — Monthly timesheet export
> As a **business owner**, I want to **export employees' total worked hours at month end in the format my accountant asks for**, **so that I don't make mistakes typing hours by hand and payroll isn't delayed.**

- Value: the product's only output measured in money; the core of the subscription rationale.
- Size: L

---

## Task 2 — Acceptance Criteria (for 2 stories)

### US-03 — Opening a shift swap request

**AC-1: Happy path — creating the request**
> **Given** Ece is assigned to the Saturday 12 March 16:00–00:00 shift
> **When** Ece uses the "open swap request" option for that shift
> **Then** the request is saved with status "Pending"
> **And** the request becomes visible to employees who hold the same role and have no shift at that time
> **And** Ece's shift stays assigned to her in the schedule until the request is resolved.

**AC-2: Time limit**
> **Given** less than 2 hours remain before a shift starts
> **When** the employee tries to open a swap request for that shift
> **Then** the system does not create the request
> **And** the message "Less than 2 hours remain before this shift — please contact your manager directly" is shown.

**AC-3: Eligibility check — overlap**
> **Given** Ece's request is open and Mert is assigned to the 12 March 14:00–22:00 shift
> **When** Mert tries to take over the request
> **Then** the system does not allow the takeover because of the time overlap
> **And** the conflicting shift is shown to Mert.

**AC-4: Eligibility check — legal limit**
> **Given** Mert's total assigned time this week is 41 hours and the shift he would take on is 8 hours
> **When** Mert tries to take over the request
> **Then** the system reports that the weekly 45-hour limit would be exceeded
> **And** the request can only proceed with additional approval from the business owner.

**AC-5: Single taker**
> **Given** two employees try to take over the same request at almost the same moment
> **When** the second request reaches the system
> **Then** only the first one is accepted
> **And** the second person is shown "This shift has already been taken by another employee."

**AC-6: Cancellation**
> **Given** Ece's request is still in "Pending" status
> **When** Ece withdraws the request
> **Then** the request closes as "Cancelled"
> **And** it is removed from other employees' lists
> **And** the shift remains assigned to Ece, unchanged.

---

### US-05 — Monthly timesheet export

**AC-1: Happy path**
> **Given** there are completed shifts in March
> **When** Deniz runs "export the March 2026 timesheet"
> **Then** a CSV file containing one row per employee is generated
> **And** each row includes full name, national ID/employee number, regular hours, overtime hours, night shift hours, and total hours
> **And** the file is named in the form `timesheet-2026-03-<business>.csv`.

**AC-2: Completed shifts only**
> **Given** the month also contains cancelled shifts and shifts dated in the future
> **When** the export runs
> **Then** only shifts with status "Completed" are counted
> **And** cancelled ones appear in no column.

**AC-3: Shift crossing midnight**
> **Given** a shift starts on 31 March at 20:00 and ends on 1 April at 04:00
> **When** the March timesheet is generated
> **Then** the 4 hours between 20:00 and 24:00 on 31 March are recorded in March
> **And** the remaining 4 hours carry over to the April period
> **And** no hour is counted in both months.

**AC-4: Mid-month hire**
> **Given** an employee started work on 14 March
> **When** the March timesheet is generated
> **Then** only shifts on and after 14 March are totalled
> **And** the employee appears in the CSV, but with no non-zero value relating to the period before that.

**AC-5: Swap history credited to the right person**
> **Given** the 12 March shift was transferred from Ece to Mert with approval
> **When** the March timesheet is generated
> **Then** that shift's hours are written to Mert's row
> **And** they do not appear in Ece's row.

**AC-6: Character and format compatibility**
> **Given** employee names contain Turkish characters
> **When** the CSV is opened in the accountant's software
> **Then** the characters display without corruption (UTF-8 BOM)
> **And** the decimal separator is in the format the accountant expects
> **And** the file has been validated at least once by Levent using sample data.

**AC-7: Empty period**
> **Given** there are no completed shifts in the selected month
> **When** the export is run
> **Then** no error is raised
> **And** a file containing only the header row is generated
> **And** the user is shown "There are no completed shifts in this period."

---

## Task 3 — Split Large Stories

### Epic: "Timesheets and accounting export"
> The business owner should be able to send all worked hours to the accountant at month end, calculated in line with legal rules.

**Why it's an epic:** it contains a calculation engine, legal rules, a correction flow, a file format, and delivery. It can't be finished in one sprint, one person can't estimate it, and there's no single measure for saying "done."

### After splitting (thin vertical slices)

| ID | Story | Size | Splitting axis |
|---|---|---|---|
| **PAY-1** | As a business owner, I want to see one employee's **raw total hours** for a chosen month on screen. | S | Simplest business rule |
| **PAY-2** | … see the raw total hours of **all employees** in a single list. | S | Singular to plural |
| **PAY-3** | … see time beyond the weekly 45 hours broken out as **overtime**. | M | Adding a business rule |
| **PAY-4** | … see hours between 20:00–06:00 broken out as **night shift hours**. | M | Adding a business rule |
| **PAY-5** | … have hours worked on public holidays calculated separately. | M | Adding a business rule |
| **PAY-6** | … **download this on-screen table as CSV**. | S | Output channel |
| **PAY-7** | … have the CSV use the **column layout** my accountant's software expects. | M | Format compatibility |
| **PAY-8** | … be able to **manually correct** an employee's hours with a written reason. | M | Exception flow |
| **PAY-9** | … see a record of **who/when/why** for the corrections made. | S | Audit trail |
| **PAY-10** | … **email the file** directly to my accountant. | S | Automation |

**Splitting axes used:** by business rule (PAY-3/4/5), happy path → exception (PAY-8), singular → plural (PAY-1→PAY-2), manual → automatic (PAY-6→PAY-10), basic output → format compatibility (PAY-6→PAY-7).

**What a bad split would look like:** dividing into "database table," "backend endpoint," "frontend screen." Those are **layer** slices; no one sees anything until all three are done, meaning none of them is deliverable on its own.

---

## Task 4 — Priority Pass

The ordering was done along two axes: **user value** (is the product useful without this) and **risk/uncertainty** (the likelihood and cost of getting it wrong). High-value + high-risk items were pulled forward, because delaying risky work means delaying bad news.

| Rank | Story | Value | Risk | Rationale |
|---|---|---|---|---|
| 1 | **US-01** Creating the weekly schedule | 5 | 3 | The data source for everything else. With no shifts, neither viewing nor timesheets are possible. A hard dependency. |
| 2 | **US-02** Employee viewing their shifts | 4 | 1 | Low cost, and the first slice that shows the product's value on the employee side. User habit in the pilot starts here. |
| 3 | **PAY-1 → PAY-3** Timesheet core | 5 | 5 | The riskiest area: a legal calculation error is silent and expensive. It has to be built early so it can be validated against the pilot's real data over 2 months. |
| 4 | **US-03** Opening a swap request | 4 | 3 | The pain Deniz raises most often. But it's meaningless without the schedule screen → comes after rank 1. |
| 5 | **US-04** Swap approval | 4 | 2 | Can't ship before US-03 is done; closes the need for control and a record. |
| 6 | **PAY-6 → PAY-7** CSV output and format | 5 | 4 | The product's monetary justification. It has a deadline on the 1st of the month, but comes after the timesheet core because the calculation has to be right first. |
| 7 | **PAY-4, PAY-5** Night shift and holiday rules | 3 | 4 | High risk, but not applicable at all of the pilot cafés; can go into the first post-MVP iteration. |
| 8 | **PAY-8, PAY-9** Manual correction and audit trail | 3 | 2 | The escape hatch for when a calculation comes out wrong. Valuable, but less urgent if the core works correctly. |
| 9 | **PAY-10** Sending by email | 2 | 1 | A convenience feature. Downloading and sending it by hand is acceptable for now. |

**MVP line:** ranks 1–6. From 7 onward is v1.1.

**The one thing that would upend this ordering:** a pilot café that runs mostly night shifts. In that case PAY-4 moves up — priority isn't a fixed list, it's a decision that gets updated as you learn.

---

## Notes (reminders to self)

- **INVEST check:** a good story should be Independent, Negotiable, Valuable, Estimable, Small, and Testable. Run every story you write through those six letters.
- **The "so that" clause is the most important part of a story.** If you can't write it, you're probably writing work that has no value.
- **Acceptance criteria exist to catch edge cases, not the happy path.** Everyone thinks of the happy path already; what sinks the product is the shift that crosses midnight.
- **Stories are sliced vertically, not layer by layer.** "Let's do the backend, frontend later" isn't a story — it's a task list.
- **Priority isn't value ÷ cost.** Risk is a multiplier too; doing the high-uncertainty work early protects the project from blowing up at a late stage.