---
name: ticket-implementation-workflow
description: Use when implementing a ticket in this repository from start to finish. This workflow validates ticket readiness, creates a delivery-step artifact, implements step by step, and applies the ticket post-step workflow after each completed step.
---

# Ticket Implementation Workflow

Use this skill for ticket-based specification-first development work in Insurance Hub.

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

Start by reading `ai/artifacts/<ticket>/<ticket>-ticket-description.md`. This required file is
the ticket's specification; no separate spec document is needed.

If the file is missing or empty, create or populate it from the supplied ticket description before
planning. Use `ai/templates/ticket-content-template.md` when helpful to structure the content.
If the ticket content is unavailable, ask the user for it rather than inferring requirements from
the ticket ID. Preserve existing content and incorporate agreed clarifications into this file.

Before implementation, confirm that the ticket description contains enough information to proceed.

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
- work with the user to clarify requirements and update the ticket-description file
- do not move to planning until the implementation path is clear enough

### 2. Produce The Detailed Plan

Once the ticket is implementation-ready:

- create the delivery steps artifact at `ai/artifacts/<ticket>/<ticket>-delivery-steps.md`
- use `ai/templates/delivery-steps-template.md` and reference the ticket-description file as the specification
- make the steps granular enough to track progress and validate behavior incrementally
- include a progress section
- identify module boundaries, tests, and validation checkpoints

### 3. Implement Step By Step

Implement one delivery step at a time.

For each step:

- Before making any change for the step, refresh the committed-branch snapshot using the post-step
  workflow's `git diff main <current-branch>` convention and save it to
  `ai/artifacts/<ticket>/<ticket>-git-diff.txt`. Confirm `main` and the current branch resolve
  before replacing the artifact; if they do not, preserve the previous snapshot and report the
  blocker.
- Immediately check `git status --short` and report any staged, unstaged, or untracked work to the
  end user before editing. Inspect those changes and preserve them. If they overlap planned files or
  make ownership/scope unclear, resolve that with the user before proceeding.
- read the relevant local code before changing anything
- implement against the ticket description and record agreed requirement changes there before adjusting the plan
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

- the ticket-description file exists and reflects the agreed requirements
- ticket readiness was explicitly validated
- the delivery-step plan artifact exists
- each completed step has a matching summary artifact
- each step summary records applicable validation with the shared evidence format and each
  acceptance criterion maps to evidence or is identified as unresolved
- post-step workflow was applied after each completed step
- formatting and tests were run as required by the repository instructions and ticket scope
