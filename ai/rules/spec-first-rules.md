# Spec-First Rules

Use these rules for non-trivial feature, bugfix, migration, and integration work.

## Readiness

For ticket work, use `ai/artifacts/<ticket>/<ticket>-ticket-content.md` as the specification.
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
