<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Spec-First Rules](#spec-first-rules)
  - [Readiness](#readiness)
  - [Planning](#planning)
  - [Implementation](#implementation)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Spec-First Rules

Use these rules for non-trivial feature, bugfix, migration, and integration work.

## Readiness

For future-work scoping, use
[`ticket-requirements-workflow`](../skills/ticket-requirements-workflow/SKILL.md). Assess supplied
inputs and ACs in `<ticket-dir>/<ticket>-requirements.md`, separating must-haves from deferrable
nice-to-haves without compromising security or personal-data protection, then stop at assessment.
When specification drafting is requested, use
[`ticket-specification-workflow`](../skills/ticket-specification-workflow/SKILL.md) to resume from
that artifact and recheck readiness. Neither skill automatically produces an implementation plan.
Requirements may precede ticket content; they are not a second implementation specification.

For ticket implementation, resolve `<ticket-dir>` through [AGENTS.md Artifact Rules](../../AGENTS.md#artifact-rules)
and use `<ticket-dir>/<ticket>-ticket-content.md` as the specification.
Read it before checking readiness. Keep requirements and agreed clarifications in this file;
do not create a separate spec document for the same ticket.

Do not start implementation until the task has enough information to proceed safely.

Check for:

- concrete business objective
- affected modules and boundaries
- API endpoint, schema, and contract details when API work is involved
- validation and error-handling expectations
- persistence expectations
- permissions and security expectations
- out-of-scope boundaries
- acceptance criteria that can be tested

If key information is missing, clarify it with the user and update the ticket-content file before planning implementation.

A specification may be conditionally ready when remaining questions are bounded decision work:
each must define a safe investigation scope, responsible role, expected evidence, acceptance
condition, and the dependent work it blocks. Plan and execute only those prerequisite resolution
steps until their decisions are recorded in ticket content and readiness is rechecked. Unresolved
scope, contracts, or safety obligations cannot be bypassed by calling them delivery steps; scope
safe discovery separately when necessary. Ordinary ready tickets need no retrospective requirements
assessment.

## Planning

Create a delivery plan before coding when the work has more than one meaningful step.

The plan should:

- reference the ticket-content file as its specification
- identify module boundaries
- break work into verifiable steps
- include test and validation checkpoints
- record assumptions and decisions
- be updated as steps complete

## Implementation

- Implement one delivery step at a time.
- Re-check assumptions before changing shared contracts.
- Prefer incremental validation over a large final-only validation pass.
- Keep generated artifacts factual and auditable.
