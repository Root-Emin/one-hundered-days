# Day 2: SDLC Fundamentals - Today's Tasks

## 1. Phase Walkthrough
**Task:** For each major phase, write one input and one output.
**Answer:**
- **Requirements:** 
  - *Input:* Customer request or business problem.
  - *Output:* Requirements document / Jira tickets.
- **Design:** 
  - *Input:* Requirements document.
  - *Output:* UI/UX mockups (e.g., Figma files) and system architecture diagrams.
- **Implementation (Coding):** 
  - *Input:* UI/UX mockups and architecture plan.
  - *Output:* Working software code and Pull Requests (PR).
- **Testing:** 
  - *Input:* Working software code.
  - *Output:* Bug reports and verified, stable code.
- **Deployment:** 
  - *Input:* Verified, stable code.
  - *Output:* Live feature accessible to end-users.

## 2. Handoffs
**Task:** Describe what 'done' means moving from design to implementation and from testing to release.
**Answer:**
- **Design to Implementation 'Done':** All UI/UX screens are approved, edge cases (e.g., empty states, error messages) are designed, and the developer has all the necessary assets and API structures planned to start coding.
- **Testing to Release 'Done':** All critical and high-priority bugs are fixed, automated and manual tests have passed, and the QA team has officially signed off on the build for production.

## 3. Roles Snapshot
**Task:** Match roles (PM/PO, designer, developer, QA, DevOps) to phases they heavily influence.
**Answer:**
- **Requirements:** PM/PO (Product Manager / Product Owner)
- **Design:** Designer (UI/UX)
- **Implementation:** Developer (Frontend/Backend)
- **Testing:** QA (Quality Assurance)
- **Deployment & Maintenance:** DevOpsd

## 4. Mini Timeline
**Task:** Sketch a 6-box timeline for a small feature from request to production.
**Answer:**
1. **[Request]** -> User asks for a "Forgot Password" feature.
2. **[Define]** -> PM/PO writes the acceptance criteria and creates a task ticket.
3. **[Design]** -> Designer creates the email template and password reset UI screen.
4. **[Build]** -> Developer writes the backend logic and frontend UI for the feature.
5. **[Test]** -> QA tests the reset link, checks edge cases, and verifies security.
6. **[Production]** -> DevOps deploys the code, and the feature goes live for all users.