<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Context and Evidence](#context-and-evidence)
- [Scope](#scope)
- [Acceptance Criteria Assessment](#acceptance-criteria-assessment)
- [Requirements](#requirements)
  - [Must-have (80-part)](#must-have-80-part)
  - [Nice-to-have (20-part)](#nice-to-have-20-part)
- [Dependencies and Constraints](#dependencies-and-constraints)
- [Open Questions and Decisions to Be Made](#open-questions-and-decisions-to-be-made)
- [Readiness and Specification Handoff](#readiness-and-specification-handoff)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

<!--
Resolve <ticket-dir> using AGENTS.md Artifact Rules and apply artifact preflight, then save as
<ticket-dir>/<ticket>-requirements.md. Requirements assessment can precede ticket content.
Use this with ai/skills/ticket-requirements-workflow/SKILL.md. Replace bracketed prompts, preserve
stable IDs and supplied AC meaning, and remove this authoring comment from the completed file.
Use None or Not applicable with a reason instead of inventing requirements or optional work.
Resolve relative links from the destination directory. Run the repository Markdown TOC formatter
and checker after authoring. This assessment is not a second implementation specification:
<ticket>-ticket-content.md becomes the authoritative specification after drafting.
Assessment stops at this document. Later, ticket-specification-workflow can draft ticket content
and fill in the specification link and AC mapping below; no delivery plan is required now.
-->

### Context and Evidence

- **Problem/current behavior:** [What happens now; include reproduction or baseline when relevant.]
- **Intended outcome:** [Business or technical value and affected users.]
- **Input and provenance:** [Product/technical source, supplied description, parent item, links.]
- **Observed evidence:** [Inspected implementation/contracts/docs; distinguish unavailable evidence.]
- **Assumptions/proposals:** [Inferences and rationale; do not present these as stakeholder decisions.]

### Scope

**In scope:**

- [Required behavior and affected modules, services, contracts, environments, or tooling.]

**Out of scope:**

- [Explicit exclusions and neighboring work.]

**Preserve:** [Existing behavior or compatibility obligations, especially during migration.]

### Acceptance Criteria Assessment

[State whether explicit ACs were supplied. Preserve their wording or faithful meaning and IDs.
Assess gaps, ambiguity, conflicts, observability, and failure cases. Without supplied ACs, state
that the acceptance outcomes below are proposals derived from the goal and evidence.]

| Source AC | Supplied criterion | Assessment or proposed clarification | Requirement/question IDs |
| --- | --- | --- | --- |
| [AC1] | [Source criterion] | [Covered, gap, conflict, or proposed change with rationale] | [M01 / Q01] |

### Requirements

Use 80/20 as prioritization, not a numeric quota. Must-haves deliver the complete safe outcome;
nice-to-haves can be independently deferred. Keep supplied commitments and applicable security,
personal-data protection, and data-integrity safeguards mandatory. Label inferred proposals.

#### Must-have (80-part)

| ID | Requirement | Source and rationale | Acceptance outcome / verification |
| --- | --- | --- | --- |
| M01 | [Observable required behavior] | [Supplied AC, evidence, or inferred proposal] | [Condition/action and expected result; planned check] |

#### Nice-to-have (20-part)

[Write None if no independently deferrable improvements were identified. These do not block the
ticket or belong in its mandatory ACs unless explicitly selected and reclassified.]

| ID | Requirement and source | Why deferred | Reconsider when | Acceptance evidence | Disposition / ledger link |
| --- | --- | --- | --- | --- | --- |
| N01 | [Optional improvement and origin] | [Value/cost rationale; mandatory safeguards remain intact] | [Trigger] | [Observable result if selected] | [Retained here or existing ledger entry/status] |

### Dependencies and Constraints

- **Dependencies:** [Prerequisite tickets, components, environments, or decisions.]
- **Contracts and data:** [API/DTO/schema, validation/error, persistence, compatibility constraints.]
- **Security and personal data:** [Required access, privacy, integrity, retention, and secret
  protections; no sensitive values. Mark unresolved safety obligations as blockers.]
- **Operations and verification:** [Rollout/recovery constraints and available verification paths;
  planned checks are not execution evidence.]

### Open Questions and Decisions to Be Made

[Use stable IDs. Record resolved decisions too, with their source. Do not mark unanswered
questions resolved merely because they were added to a plan.]

| ID | Question/decision and affected requirements | Options / recommendation | Owner or role | Disposition | Resolution / evidence |
| --- | --- | --- | --- | --- | --- |
| Q01 | [Question; M01] | [Options and rationale] | [Owner/role or Unassigned] | [Blocking / Resolved / Deferred to delivery] | [Decision and source, or pending action] |

For each **Deferred to delivery** decision, record:

- **Decision ID and resolution step:** [Q01 and concrete investigation/decision task.]
- **Safe investigation boundary:** [What may be evaluated without assuming the outcome.]
- **Responsible role:** [Who resolves or accepts the decision.]
- **Expected output and acceptance condition:** [Decision artifact/evidence and how it is judged.]
- **Blocked dependent work:** [What cannot start before resolution.]
- **Recheck:** [Update requirements/ticket content and reassess readiness before releasing the gate.]

### Readiness and Specification Handoff

- **Status:** [Blocked / Ready / Conditionally ready for decision work.]
- **Rationale:** [Unresolved blockers, or why all remaining questions qualify as bounded steps.]
- **Specification:** [Relative link to <ticket>-ticket-content.md once drafted; otherwise Not drafted.]
- **Coverage:** [Map must-have IDs to specification AC IDs once drafted; note any unresolved gaps.]
- **Optional-work disposition:** [Applicable ledger and entries, or retained locally with reason.]
- **Next action:** [Clarification needed, draft specification, or handoff with dependency gates.]
