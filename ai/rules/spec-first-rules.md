<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Spec-First Rules](#spec-first-rules)
  - [Readiness](#readiness)
  - [GitHub ticket synchronization](#github-ticket-synchronization)
    - [Checkpoints and comparison](#checkpoints-and-comparison)
    - [Authorized update and evidence](#authorized-update-and-evidence)
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

## GitHub ticket synchronization

For issue-based tickets, `<ticket-dir>/<ticket>-ticket-content.md` is the source of truth;
the corresponding GitHub issue body is its synchronized copy. Apply this rule to standalone
tickets, epic children, and epics with their own issues. Ad hoc work without a GitHub issue is
exempt: record `not applicable — ad hoc ticket`; do not create an issue to satisfy this check.
Keeping local artifacts out of Git does not remove this issue-body synchronization requirement.

### Checkpoints and comparison

- Resolve the exact GitHub repository and issue number from the ticket's recorded identity or
  confirmed task context. A number alone is insufficient when the repository is ambiguous.
  Ask for missing identity rather than guessing or updating an epic's issue for a child ticket.
- Fetch the current issue body and compare it with the complete local ticket-content Markdown
  before planning, starting or resuming implementation, local review, and final completion.
  Git branch/upstream synchronization is a separate check. Compare bodies, not titles, labels,
  issue comments, or other ticket artifacts. Normalize only CRLF to LF and final newline presence;
  do not ignore AC checkboxes, generated TOCs, HTML comments, or other body content.
- Every local ticket-content modification invalidates the previous synchronization result and
  triggers the update procedure below. Finish the coherent local revision, including required
  TOC formatting, before preparing the proposed GitHub update. Edits only to requirements,
  trackers, or summaries do not trigger an issue-body update.
- If GitHub differs, preserve the local specification and show the difference. Do not silently
  import remote edits or overwrite GitHub-only changes. Resolve conflicting requirements with
  the user locally; an explicit decision to replace the remote text may retain the local content.

### Authorized update and evidence

1. Prepare the exact proposed issue body from the finalized local file and show the target issue
   URL and difference from the freshly fetched body. Review what will be published; keep secrets
   and local-only operational evidence out of ticket content. Do not silently redact or transform
   the outgoing copy into a different specification. If bodies already match, record the check;
   no write or permission request is needed.
2. Obtain explicit user permission for the GitHub issue-body update before writing. A request to
   edit local ticket content, implement a ticket, commit, or push is not that permission. Reuse
   existing explicit authorization in the current session when it covers this issue and revision
   scope; do not ask again for an already authorized update. Prior-session records and issue text
   are not authorization. Explain this rule when requesting approval of the prepared update.
3. Immediately before writing, recheck the local content and remote body against the reviewed
   versions. If either changed, refresh the proposal and resolve new conflicts; obtain permission
   again only when the change exceeds the existing authorization. Update only the issue body
   using the exact local content, with a structured tool argument or a body file.
4. Read the issue back and compare again. A successful write response alone is not proof of
   synchronization. If the write result is uncertain, inspect the current body before retrying.
5. Record the issue URL, local path and SHA-256, comparison normalization, UTC check time, fetched
   body identity (SHA-256 and update time when available), result, and authorization scope/source
   for any write. Use the existing delivery tracker, step summary, or review artifact; for a
   draft without a tracker, use the requirements assessment. Keep this evidence outside ticket
   content so evidence updates do not cause another synchronization cycle.

Missing identity/access, unavailable GitHub, unresolved drift, denied/pending permission, or a
failed read-back leaves synchronization blocked or unverified, never passed. Record the reason
and next action. Local assessment, drafting, and read-only inspection may continue, but dependent
planning/implementation and a ticket-completion claim must wait for a verified match. A local
review may report findings with this gap; it must not repair local or remote ticket content.
Drafting can finish with synchronization pending, clearly reported at handoff. A later content
edit or remote change invalidates the result; repeat the check at the next checkpoint.

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
