# Day 14: Operate, Improve & Capstone - Maintenance, Monitoring, and Incidents

## 1. Maintenance Types
**Task:** Contrast corrective, adaptive, perfective, and preventive maintenance with examples.
**Answer:**
- **Corrective Maintenance:** Fixing active bugs and errors in the system. *(Example: Patching a bug that causes the checkout page to crash on mobile devices.)*
- **Adaptive Maintenance:** Updating the software to keep it compatible with changing environments or regulations. *(Example: Updating the mobile app codebase to support a new iOS or Android version.)*
- **Perfective Maintenance:** Improving performance or usability without changing core functionality. *(Example: Optimizing a database query so the user dashboard loads in 1 second instead of 3 seconds.)*
- **Preventive Maintenance:** Identifying and fixing potential future problems before they impact users. *(Example: Upgrading an old third-party library before it reaches its end-of-life and becomes a security vulnerability.)*

## 2. Signals
**Task:** List metrics/logs you'd watch for a simple web API.
**Answer:**
For a simple web API, I would monitor these key signals (often called the RED metrics or Golden Signals):
- **Error Rate:** The percentage of HTTP 500 (Server Error) responses compared to successful HTTP 200 responses.
- **Latency/Response Time:** How long it takes for the API to respond to a request (e.g., aiming for under 200ms).
- **Traffic (Throughput):** The number of requests per second (RPS) hitting the API.
- **Resource Utilization:** CPU and Memory usage of the server or container hosting the API.

## 3. Incident Outline
**Task:** Write a mini incident timeline (detect -> mitigate -> fix -> review).
**Answer:**
- **Detect (10:00 AM):** An automated monitoring tool alerts the team via Slack that the API error rate has spiked to 40%.
- **Mitigate (10:05 AM):** The on-call developer realizes the latest deployment caused the issue and immediately rolls back to the previous stable version. The bleeding stops, and users can use the app again.
- **Fix (10:45 AM):** The developer investigates the logs, finds a typo in the database connection string of the new code, corrects it, tests it locally, and prepares a proper patch.
- **Review (Next Day):** The team holds a blameless "Post-Incident Review" to discuss why the CI/CD pipeline didn't catch the typo before it reached production, and they add a new automated test to prevent it from happening again.

## 4. On-call Empathy
**Task:** Note what information makes incidents easier to handle.
**Answer:**
When a developer is woken up at 3:00 AM by an incident alert, solving the problem is much easier if they have:
- **Clear Alerts:** An alert that specifically says "Database connection failing on Auth Service" rather than a generic "System is down."
- **Runbooks/Playbooks:** A step-by-step documentation guide on how to handle common failures for that specific service.
- **Structured Logs:** Logs that include correlation IDs so the developer can trace exactly what a specific user was doing when the error occurred.
- **A Blameless Culture:** Knowing that the goal is to fix the system, not to punish the person who wrote the buggy code.