# Day 12: Build, Quality & Release - Release Readiness and CI Basics

## 1. Release Checklist
**Task:** Draft a go-live checklist (migrations, Feature flags, rollback, smoke test).
**Answer:**
Before pressing the "Deploy" button, this checklist must be completed:
- [ ] **Database Migrations:** Are all new database tables and columns applied to the production database?
- [ ] **Feature Flags:** Are the necessary feature toggles correctly set (enabled/disabled) for the target environment?
- [ ] **Rollback Ready:** Is the previous stable version tag ready to be deployed instantly if something goes wrong?
- [ ] **Smoke Test:** Have we run a quick sanity check (Smoke Test) on the critical paths (e.g., login, checkout) immediately after deployment?

## 2. CI Purpose
**Task:** Explain how continuous integration supports SDLC quality gates.
**Answer:**
Continuous Integration (CI) acts as an automated quality gatekeeper. Every time a developer merges new code, the CI pipeline automatically builds the application and runs all the unit/integration tests. This ensures that broken or non-compiling code never reaches the release phase. It forces repeatable checks, allowing developers to focus on writing logic rather than doing tedious manual testing.

## 3. Rollback Plan
**Task:** Write a simple rollback/mitigation plan for a bad release.
**Answer:**
**Scenario:** A new payment update is deployed, but it causes the checkout page to crash for real users.
**Rollback Plan:**
1. **Identify:** Production monitoring alerts the team about the crash.
2. **Mitigate (Quickest Option):** If the feature is behind a Feature Flag, turn the flag OFF immediately to hide the broken feature from users.
3. **Rollback (If no flag):** Use the deployment tool (e.g., Docker, Vercel, or CI/CD pipeline) to instantly revert the live environment back to the previous stable Git tag (e.g., reverting from `v1.2.1` back to `v1.2.0`).
4. **Investigate:** Once the live site is safe, investigate the logs on a staging environment to find and fix the root cause.

## 4. Version Note
**Task:** Practice writing a short changelog entry for a sample release.
**Answer:**
**Version:** v1.2.0 - 2026-08-15
**🚀 New Features:**
- Added a "Forgot Password" flow allowing users to reset passwords via email link.
**🐛 Bug Fixes:**
- Fixed an issue where the user profile picture wouldn't load on the mobile app view.
- Resolved a timeout error that occasionally occurred during database heavy queries on the dashboard.