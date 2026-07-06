---
name: ticket-implementation-workflow
description: Use when implementing a ticket in this repository from start to finish. This workflow validates ticket readiness, creates a delivery-step artifact, implements step by step, and applies the ticket post-step workflow after each completed step.
---

# Ticket Implementation Workflow

Use this skill for ticket-driven development work in Insurance Hub.

## Prerequisite

At the start of a new session, apply the bootstrap guidance from:

- `ai/prompts/session-bootstrap.md`

That bootstrap prompt establishes:

- `AGENTS.md` as the first repository instruction source
- `ai/rules/` as supplemental rules
- `ai/skills/` as reusable workflows
- `ai/examples/` as examples to follow

## Workflow

### 1. Validate Ticket Readiness

Before implementation, confirm that the ticket contains enough information to proceed.

Check for:

- concrete business objective
- endpoint, schema, and contract details when API work is involved
- validation and error-handling expectations
- persistence expectations and out-of-scope boundaries
- permissions and security requirements
- acceptance criteria that can be tested

If information is missing or ambiguous:

- stop implementation work
- identify the gaps explicitly
- work with the user to enrich the ticket dev notes or equivalent artifacts
- do not move to planning until the implementation path is clear enough

### 2. Produce The Detailed Plan

Once the ticket is implementation-ready:

- create the delivery steps artifact at `ai/artifacts/<ticket>/<ticket>-delivery-steps.md`
- make the steps granular enough to track progress and validate behavior incrementally
- include a progress section
- identify module boundaries, tests, and validation checkpoints

### 3. Implement Step By Step

Implement one delivery step at a time.

For each step:

- read the relevant local code before changing anything
- challenge assumptions and weak designs
- prefer established repository patterns over inventing new ones
- keep scope tight to the step at hand
- validate the proposed implementation critically instead of accepting the first workable approach

### 4. Apply Post-Step Workflow

After each completed implementation step, apply:

- `ai/skills/ticket-post-step-workflow/SKILL.md`

That workflow updates the delivery tracker, creates the per-step summary, and refreshes the ticket git diff artifact.

## Completion Checklist

Before considering the ticket workflow complete, ensure:

- ticket readiness was explicitly validated
- the delivery-step plan artifact exists
- each completed step has a matching summary artifact
- post-step workflow was applied after each completed step
- formatting and tests were run as required by the repository instructions and ticket scope
