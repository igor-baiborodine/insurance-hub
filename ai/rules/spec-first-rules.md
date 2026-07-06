# Spec-First Rules

Use these rules for non-trivial feature, bugfix, migration, and integration work.

## Readiness

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

If key information is missing, stop and clarify or enrich the ticket notes before planning implementation.

## Planning

Create a delivery plan before coding when the work has more than one meaningful step.

The plan should:

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
