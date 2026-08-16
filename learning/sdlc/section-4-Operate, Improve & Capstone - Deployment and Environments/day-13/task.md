# Day 13: Operate, Improve & Capstone - Deployment and Environments

## 1. Environment Map
**Task:** Distinguish local, staging, and production responsibilities.
**Answer:**
- **Local:** The developer's own machine (e.g., macOS/Linux terminal). Used for writing code, fast iteration, and running initial unit tests. It usually uses a mock or local database (like a local Docker container).
- **Staging:** A pre-production environment that perfectly mirrors the production setup. It is used for realistic validation, QA testing, and stakeholder approval before anything goes live.
- **Production (Prod):** The live environment where real users and customers interact with the software. Stability, security, and performance are the top priorities here.

## 2. Promotion Path
**Task:** Describe how a change moves across environments.
**Answer:**
A change starts as code on a developer's **Local** machine. Once pushed and merged via a Pull Request, the CI/CD pipeline automatically builds and deploys the artifact to the **Staging** environment. After QA and automated tests verify the change in Staging, a manual or automated trigger "promotes" that exact same, verified build to the **Production** environment.

## 3. Config Awareness
**Task:** List what must differ per environment (secrets, URLs, feature flags).
**Answer:**
Hardcoding values is dangerous. The following configurations must change dynamically depending on the environment (usually via `.env` files):
- **Database Connection Strings:** Local DB vs. Staging DB vs. Production DB URLs.
- **Third-Party API Keys:** Test keys for payment gateways in Staging vs. live secret keys in Production.
- **Base URLs:** The URL the frontend uses to call the backend API (e.g., `localhost:8080` vs. `api.myapp.com`).
- **Feature Flags:** A new feature might be turned ON in Staging for testing, but kept OFF in Production until marketing is ready.

## 4. Unsafe Shortcut
**Task:** Explain why 'deploy untested hotfixes straight to prod' is risky—and a safer alternative.
**Answer:**
- **The Risk:** Deploying a "quick fix" directly to Production without testing can introduce catastrophic side effects. A rushed hotfix might solve a minor UI bug but accidentally break the database connection, causing total system downtime and data corruption for real users.
- **The Safer Alternative:** Even during a critical emergency, a hotfix must pass through a rapid pipeline. Write the fix locally, run the automated tests, deploy to Staging for a quick but focused verification (smoke test), and only then promote it to Production. Predictability is always better than panic.