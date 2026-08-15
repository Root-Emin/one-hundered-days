# Day 10: Build, Quality & Release - Code Review and Collaboration

## 1. Review Checklist
**Task:** Create a 8–10 item code review checklist (correctness, tests, naming, security).
**Answer:**
1. **Correctness:** Does the code actually solve the problem or implement the feature as described in the requirements?
2. **Readability:** Are variables, functions, and classes named clearly and intuitively?
3. **Complexity:** Is the code overly complex? Can it be simplified?
4. **Error Handling:** Are potential errors and edge cases caught and handled gracefully?
5. **Security:** Are there any hardcoded secrets (API keys, passwords) or obvious vulnerabilities (e.g., SQL injection risks)?
6. **Testing:** Are there adequate unit or integration tests for the new logic, and do they all pass?
7. **Performance:** Are there any obvious performance bottlenecks (e.g., unnecessary database queries inside a loop)?
8. **Architecture:** Does the new code follow the established architectural patterns of the project (e.g., MVC, clean architecture)?
9. **Documentation:** Has the README, API documentation, or inline comments been updated if necessary?
10. **Leftovers:** Have all `console.log()` / `print()` statements and commented-out code blocks been removed?

## 2. Give Feedback
**Task:** Practice writing one kind comment and one necessary critical comment on a sample diff.
**Answer:**
- **Kind Comment:** "Great job refactoring this authentication function! It is much cleaner and easier to read now. Really nice use of early returns."
- **Critical Comment:** "I noticed we aren't handling the scenario where the API returns a 500 error on this specific line. If that happens, the app might crash. Could we add a `try/catch` block and a fallback UI message here?"

## 3. Receive Feedback
**Task:** Write how you'd respond to a review you disagree with professionally.
**Answer:**
"I appreciate the suggestion to extract this logic into a completely separate microservice. However, considering our current MVP timeline and the simplicity of this feature, I kept it within this module to avoid premature complexity. How about we leave it here for now and create a technical debt ticket to extract it once the user base scales?"

## 4. PR Hygiene
**Task:** List what belongs in a good pull request description.
**Answer:**
A high-quality Pull Request description should include:
- **Title:** A clear, concise summary of the change (e.g., `feat: add password reset API endpoint`).
- **Context/Why:** A brief explanation of *why* this change is being made and what problem it solves.
- **What Changed:** A bulleted list of the technical changes made in the code.
- **Related Tickets:** Links to the relevant Jira, Trello, or GitHub issue.
- **Testing Steps:** Clear instructions for the reviewer on how to run and test the feature locally.
- **Visuals:** Screenshots or screen recordings (GIFs) of before and after, if the PR includes UI changes.