# Day 11: Build, Quality & Release - Testing in the Life Cycle

## 1. Testing Levels
**Task:** Explain unit, integration, and end-to-end tests with one example each.
**Answer:**
- **Unit Test:** Tests a single, isolated small function or component. *Example:* Testing a calculation function that computes the total price of a shopping cart to ensure it returns the correct mathematical result.
- **Integration Test:** Tests how multiple distinct components work together. *Example:* Testing if a backend API successfully connects to the database and correctly saves a new user record.
- **End-to-End (E2E) Test:** Tests the entire application flow from the user's perspective, using automated browsers. *Example:* A script that opens the app, types in credentials, clicks "Login", and verifies that the user dashboard loads successfully.

## 2. Map to Stories
**Task:** For a user story, list which tests you'd write at each level.
**Answer:**
**User Story:** "As a user, I want to update my profile picture so I can personalize my account."
- **Unit Test:** Write a test for the file validation function to ensure it only accepts `.jpg` or `.png` formats and rejects `.pdf`.
- **Integration Test:** Write a test to ensure that when a valid image is uploaded to the endpoint, the application successfully saves the file URL to the database.
- **E2E Test:** Write a test that clicks the "Upload" button, uploads a sample image, clicks "Save", and verifies that the new profile picture is visible on the screen.

## 3. Bug Life Cycle
**Task:** Outline steps from bug report -> triage -> fix -> verify -> close.
**Answer:**
1. **Bug Report:** A user or tester finds an issue (e.g., "The app crashes on the payment screen") and creates a ticket detailing the steps to reproduce it.
2. **Triage:** The PM or Tech Lead reviews the bug, assesses its severity (e.g., Critical/Low), prioritizes it, and assigns it to a developer.
3. **Fix:** The developer debugs the issue, writes the code to fix it, adds a test to prevent it from happening again, and opens a Pull Request.
4. **Verify:** Once merged, the QA team tests the application in a staging environment to confirm the bug is actually resolved.
5. **Close:** After successful verification, the bug ticket is officially closed.

## 4. Quality != Only QA
**Task:** Note developer responsibilities for quality before handoff.
**Answer:**
Developers should never just write code and "throw it over the wall" to the QA team. Before handing off, a developer is responsible for:
- Writing and passing their own Unit and Integration tests.
- Running the code locally to ensure it meets the Definition of Done.
- Thinking about edge cases and handling potential errors gracefully.
- Participating in Code Reviews to catch obvious mistakes before they reach QA.