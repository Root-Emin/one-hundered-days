# Day 16: Operate, Improve & Capstone - Full-Cycle Capstone Plan

## 1. Pick a Feature
**Task:** Choose a small product feature to plan through the entire SDLC.
**Answer:**
**Feature:** "Automated Priority Scoring for Incoming Tickets" (Part of the AI Ticket Triage system).
When a new customer support ticket is submitted, the system will use a local AI model (Gemma) to analyze the text and automatically assign a priority score (1 to 5) before a human agent sees it.

## 2. Full Packet
**Task:** Produce requirements (stories+AC), design sketch, build/test plan, release checklist, and ops notes.
**Answer:**
- **Requirements (Story & AC):** 
  - *User Story:* As a customer support agent, I want incoming tickets to be automatically scored for priority by AI, so I know which urgent issues to handle first.
  - *Acceptance Criteria (AC):* 1. The system must send the ticket text to the Go backend. 2. The Gemma model must evaluate the text and return an integer score (1-5). 3. The score must be saved to the database and displayed on the Next.js frontend frontend.
- **Design Sketch:** 
  - The Next.js client sends a `POST` request to the Go backend (`/api/tickets`).
  - The Go service handles the request, communicates with the WebLLM/Gemma instance to get the score.
  - The Go service writes the ticket and its priority score to the PostgreSQL database and returns a `200 OK` response to the client.
- **Build/Test Plan:** 
  - *Build:* Write the Go handler for the API and the database interaction logic.
  - *Test:* Write unit tests in Go to verify the database saving logic (mocking the AI response). Write an E2E test to ensure the UI updates when a high-priority ticket is created.
- **Release Checklist:** 
  - [ ] Apply database migration (add `priority_score` column to the `tickets` table).
  - [ ] Ensure the Gemma model path and necessary API configurations are set in the production `.env` file.
  - [ ] Run a quick smoke test in the Staging environment.
- **Ops Notes:** 
  - LLM inference can be slow. Set up a timeout alert if the scoring process takes longer than 5 seconds. 
  - Monitor CPU usage on the server hosting the model.

## 3. Model Choice
**Task:** State Waterfall/Agile/Hybrid choice and why.
**Answer:**
**Hybrid Model.** 
Planning the initial system architecture, the monorepo structure, and the database schema required strict upfront planning (Waterfall). However, refining the LLM prompts, adjusting the scoring logic, and improving the UI will require short, continuous iterations based on feedback (Agile).

## 4. Reflection
**Task:** Write what phase you underestimate most and how you'll practice it next.
**Answer:**
I usually underestimate the **Testing and Operations** phases. When building the logic in Go or creating the UI in React/Next.js, it's easy to focus only on getting the code to work ("Happy Path"). I underestimate how much time it takes to write robust integration tests and set up proper error logging. Next time, I will practice "Test-Driven Development (TDD)" by writing a basic test for my Go endpoint before I actually write the routing logic.