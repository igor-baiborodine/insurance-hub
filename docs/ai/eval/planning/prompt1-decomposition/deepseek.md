```shell
ollama run --verbose deepseek-r1:8b “List 7 concrete steps to plan and ship a small backend feature, from initial requirements to deployment. Group them into 3 phases and name each phase.”
```

Thinking...
Okay, the user wants a structured approach to planning and shipping a small backend feature, broken down into three phases with seven concrete steps.      
They're probably a developer or tech lead who's either new to feature development or wants to streamline their workflow.

First, I need to understand what makes a "small backend feature"—it's not a big rewrite but a focused improvement. The user might be frustrated with       
vague processes or over-engineering. They're looking for something practical, almost like a checklist but with clear phases to avoid feeling               
overwhelmed.

Hmm, the phrasing "from initial requirements to deployment" suggests they want end-to-end coverage. Maybe they've seen features stall in planning or       
deployment phases. The grouping into phases implies they value milestones—perhaps to track progress or justify timelines to stakeholders.

I should avoid jargon but keep it technical enough for backend devs. No fluff. Each step must be actionable: "write a migration" is concrete, while   
"implement the feature" is intentionally vague because actual implementation varies. The phases should reflect natural workflow—discovery first, then
execution, then polish.

Wait, should I include rollback? Yes, because even small features need a recovery plan. And monitoring—backend teams often forget that. The user           
might not realize how quickly a small bug can cascade without proper observability.

Phase 1 is all about alignment: requirements, design, and planning. Phase 2 is execution—coding, testing. Phase 3 is the often-neglected "release &        
sustain" phase, where communication and monitoring matter as much as the code.

I wonder if the user is preparing a training doc or just personalizing their workflow. Either way, the steps should feel universal enough for any          
tech stack but specific enough to be useful. No frameworks mentioned because the user didn't specify—keep it framework-agnostic unless it's about          
database changes (then migrations are key).

Finally, naming phases matters. "Define & Design" sounds deliberate, "Implement & Test" is action-oriented, and "Release & Monitor" signals                
completion. The names should match the user's mental model—backend folks think in terms of shipping cycles, not fluffy phases.                             
...done thinking.

Okay, here are 7 concrete steps grouped into 3 phases for planning and shipping a small backend feature, focusing on a typical development lifecycle:

## Phase 1: Define & Design (The Foundation)

1.  **Clarify Requirements & Scope:**
    *   **Action:** Engage with the requester/stakeholder to precisely understand the *why*, *what*, and *scope* of the feature. Define clear
        acceptance criteria. Explicitly define what *is not* part of this feature.
    *   **Output:** A clear, concise feature description and acceptance criteria (e.g., specific business rules, API contract changes, data model
        changes).

2.  **Design the Solution (High-Level):**
    *   **Action:** Sketch out the high-level architecture, data flow, and interactions. Identify the core components or services that will be
        affected. Consider potential impact on existing features. Decide on key design choices (e.g., API endpoints, database schema changes - even small
        ones need thought).
    *   **Output:** A high-level design document or diagram (initial sketch). This might involve simple database migration scripts if changes are
        required.

## Phase 2: Implement & Test (Building the Feature)

3.  **Develop the Core Logic:**
    *   **Action:** Write the code for the feature based on the design. Focus on implementing the agreed-upon functionality. Write unit tests as you
        go.
    *   **Output:** Working code for the feature logic, along with unit tests.

4.  **Implement Data Changes (if any):**
    *   ****Action:** If the feature requires database schema changes or data migrations, write and test the migration scripts (e.g., SQL, Flyway,
        Liquibase, Alembic). Ensure you have a rollback strategy for these changes.
    *   **Output:** Database migration scripts and tests.

5.  **Integrate and Write Integration/End-to-End Tests:**
    *   **Action:** Integrate the feature code with the rest of the system. Write integration tests to verify interactions between components and
        potentially end-to-end tests if applicable (e.g., testing API calls that involve the feature). Ensure the feature works with the existing system
        flow.
    *   **Output:** Integration and E2E tests confirming the feature works within the larger context.

## Phase 3: Release & Monitor (Getting It Live)

6.  **Prepare Deployment & Rollout Strategy:**
    *   **Action:** Determine how the change will be deployed (e.g., Git tag + CI/CD pipeline deployment, blue-green, canary). Prepare necessary
        documentation updates (internal wikis, API docs). Coordinate with the team for timing (if needed).
    *   **Output:** Deployment plan, potentially a rollback procedure documented.

7.  **Deploy, Monitor, and Gather Feedback:**
    *   **Action:** Deploy the feature through your standard process. Closely monitor logs, metrics, and error rates immediately after deployment.
        Have a plan (e.g., rollback script, scaling strategy) ready for any unexpected issues. Gather feedback from relevant stakeholders or users.
    *   **Output:** Successfully deployed feature, initial operational data, feedback collected for future iterations.

---
**Token Throughput**
- total duration:       34.13921118s
- load duration:        7.98098602s
- prompt eval count:    34 token(s)
- prompt eval duration: 37.520437ms
- prompt eval rate:     906.17 tokens/s
- eval count:           1101 token(s)
- eval duration:        25.791177604s
- eval rate:            42.69 tokens/s