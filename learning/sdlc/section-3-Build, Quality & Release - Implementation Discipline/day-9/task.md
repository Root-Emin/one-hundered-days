# Day 9: Build, Quality & Release - Implementation Discipline

## 1. Slice Work
**Task:** Break one user story into implementation tasks (API, UI, tests, docs).
**Answer:**
**User Story:** "As a user, I want to reset my password so I can regain access to my account."
**Implementation Tasks (Vertical Slice):**
1. **API / Backend:** Create the `/api/reset-password` endpoint to generate and validate reset tokens (e.g., using Go).
2. **Database:** Add a `reset_token` and `token_expiry` column to the Users table.
3. **UI / Frontend:** Build the "Forgot Password" form and the "Set New Password" screen.
4. **Tests:** Write unit tests for token generation and integration tests for the API endpoint.
5. **Docs:** Update the API documentation with the new endpoint details.

## 2. Branch Mentality
**Task:** Describe a safe workflow for coding the story (branch, small commits).
**Answer:**
A safe workflow starts by never pushing directly to the `main` branch. 
1. Create a specific feature branch (e.g., `feature/password-reset`).
2. Write code in small, logical chunks and commit frequently with clear messages (e.g., `feat: add reset token DB migration`, `feat: create UI form`).
3. Once the feature is complete, open a Pull Request (PR) to merge the branch into `main`.

## 3. Definition of Done Coding
**Task:** List coding DoD items (tests, lint, reviewed, documented).
**Answer:**
For a piece of code to be considered completely "Done", it must meet this checklist:
- [ ] Code is fully written and functionality works as expected.
- [ ] Code passes all automated Linter checks (no syntax or formatting errors).
- [ ] Unit and integration tests are written and all pass successfully.
- [ ] The Pull Request has been reviewed and approved by at least one other developer.
- [ ] Necessary technical documentation (README or API docs) has been updated.

## 4. Spike vs Build
**Task:** Decide when you'd spike (research) before committing to an approach.
**Answer:**
- **When to Build:** If the feature uses familiar technologies, clear requirements, and standard architecture (e.g., adding a standard CRUD endpoint), I will start building immediately.
- **When to Spike:** If the feature requires integrating a new, unknown third-party API (like a new AI model or payment gateway), or if the architectural approach is highly uncertain. I will time-box a "Spike" (e.g., 4 hours) to research, build a messy prototype, and reduce risk before actually implementing the production code.