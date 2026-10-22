---
name: ticket-requirements-workflow
description: Assess future ticket scope and classified requirements from product or technical inputs, with or without acceptance criteria. Use for requirements assessment or ticket scoping; produces a requirements artifact without drafting ticket content or an implementation plan.
---

<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Ticket Requirements Workflow](#ticket-requirements-workflow)
  - [Inputs and Artifact Setup](#inputs-and-artifact-setup)
  - [Assess Scope and Requirements](#assess-scope-and-requirements)
    - [Establish the Scope and Evidence](#establish-the-scope-and-evidence)
    - [Assess Acceptance Criteria and Classify Requirements](#assess-acceptance-criteria-and-classify-requirements)
    - [Retain Optional Work](#retain-optional-work)
    - [Resolve Questions and Assess Drafting Readiness](#resolve-questions-and-assess-drafting-readiness)
  - [Validation and Handoff](#validation-and-handoff)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Ticket Requirements Workflow

Turn a problem, feature, bug, enhancement, or defined sub-epic item into classified requirements.
Accept product-owner or technical-staff input with or without explicit acceptance criteria (ACs).
The output is a durable assessment for later specification drafting, potentially in another session.

## Inputs and Artifact Setup

1. Follow [AGENTS.md](../../../AGENTS.md) and [the manifest](../../manifest.md); discover scoped
   instructions before inspecting or changing affected areas. Load the
   [spec-first rules](../../rules/spec-first-rules.md) and relevant domain rules.
2. Identify the ticket ID or a descriptive ad hoc identifier and the supplied problem/outcome.
   An issue number alone is not a specification: inspect available source content or ask for it.
   Do not invent a problem, stakeholder decision, or parent-ticket requirements.
3. Resolve `<ticket-dir>` once through [Artifact Rules](../../../AGENTS.md#artifact-rules).
   Include existing requirements files in ignored-artifact discovery when ticket content does not
   yet exist. Preserve the location for later drafting, including epic grouping and explicit paths.
   Before writing artifacts, apply the root artifact preflight, checking the intended requirements
   path as well as the ticket-content path. Never change the local exclusion or index automatically.
4. Read existing requirements and ticket content if present, plus supplied ACs and relevant
   source evidence. Preserve agreed content and record revisions; do not overwrite an existing
   specification with a fresh scaffold. A requirements-first request does not need a placeholder
   ticket-content file before assessment.

## Assess Scope and Requirements

Write `<ticket-dir>/<ticket>-requirements.md` using the
[requirements template](../../templates/requirements-template.md). Scale detail to the task;
use “None” or “Not applicable” with a reason where appropriate instead of inventing content.

### Establish the Scope and Evidence

- State the current problem, intended outcome, affected users/components, in-scope behavior,
  out-of-scope boundaries, dependencies, and constraints.
- Inspect the relevant implementation, contracts, documentation, or bug reproduction evidence.
  For migration or modernization, capture the current behavior that must be preserved before
  proposing replacement behavior. Distinguish observed facts, supplied requirements, inferred
  proposals, and assumptions; reference the evidence and identify unavailable sources.
- Examine applicable contracts, DTOs, validation/errors, persistence, compatibility, security,
  personal-data handling, operational behavior, and verification. Omit irrelevant domains with
  a brief explanation; do not turn a small task into an unrelated architecture exercise.

### Assess Acceptance Criteria and Classify Requirements

- **With explicit ACs:** preserve their meaning and identify each source criterion. Check clarity,
  consistency, observability, failure cases, and omissions against the intended outcome and
  affected boundaries. Map every supplied AC to requirements or an explicit unresolved issue.
  Label proposed additions or changes and explain why they are needed. Do not silently remove,
  weaken, or reclassify supplied commitments as optional; seek clarification for material scope
  changes or conflicts. Derive the scope and requirements from the assessed ACs.
- **Without explicit ACs:** use the supplied goal and repository evidence to propose a bounded
  scope and observable acceptance outcomes using best judgment. Label inferred requirements and
  assumptions; clarify material uncertainty without blocking on routine drafting choices.
- **For both paths:** explicitly separate **must-have (80-part)** and **nice-to-have (20-part)**
  requirements. This is a value/risk prioritization heuristic, not an item-count, effort, or
  coverage quota. Must-haves are the minimum complete, safe outcome and supplied commitments;
  nice-to-haves are independently deferrable improvements. An empty optional section is valid.
- Use stable requirement IDs, such as `M01` and `N01`, with source, rationale, and observable
  acceptance evidence. Retain IDs during revisions and when linking deferred work.
- Never trade away applicable security, authentication/authorization, personal-data protection,
  secret handling, data integrity, or mandatory compliance obligations to meet an 80/20 target.
  A safeguard necessary for safe delivery is mandatory; uncertainty about it is a blocker, not
  an optional ledger item. Optional enhancements must leave the mandatory safeguards intact.

### Retain Optional Work

For each nice-to-have, record its deferral rationale, reconsideration trigger, and acceptance
evidence. If an applicable ledger exists and maintaining it is within the request, inspect its
local guidance and reuse its conventions, stable IDs, and existing entries. Link both the
requirements entry and ledger entry; avoid duplicate entries and preserve their origin/status.

The [Phase 4 ledger](../../../docs/migration/phase-4/phase-4-nice-to-have-ledger.md) is an example
for Phase 4 work, not a global destination. Without a relevant ledger or authorization to edit it,
retain optional items in the requirements document and state that disposition. Do not create a
new ledger just to complete this workflow. A ledger entry adds no implementation commitment;
selecting it later requires an explicit scope/AC update in the receiving ticket.

### Resolve Questions and Assess Drafting Readiness

Track open questions and decisions with stable IDs, affected requirements, options/recommendation,
resolution owner (or explicitly unassigned), status, and resolution/evidence. Use one disposition:

- **Blocking:** missing or conflicting scope, ACs, contracts, or safety requirements prevent a
  meaningful specification. Ask focused questions, continue independent assessment, and do not
  mark the assessment ready for specification drafting while these remain unresolved.
- **Resolved:** record the decision and source; incorporate it into the requirements.
- **Deferred to delivery:** use only for bounded discovery or technical decisions whose resolution
  can be an explicit delivery step without assuming the answer. Record the safe investigation
  boundary, responsible role, expected decision artifact/evidence, acceptance condition, and
  dependent work it blocks. A generic “decide during implementation” note is insufficient.

Record readiness as **blocked**, **ready**, or **conditionally ready for decision work** and give
the rationale. The conditional state permits drafting only when every remaining question meets
the bounded-decision conditions. For example, a disposable comparison of two implementations
under an agreed contract may qualify; unresolved permission to expose customer data does not.
Do not guess missing business policy, public contracts, or protection requirements. Safe discovery
may investigate such a blocker as separately scoped work, without making dependent delivery ready.

## Validation and Handoff

- Check source-AC coverage, observable must-have acceptance outcomes, optional exclusions, question dispositions,
  relative links at the actual artifact depth, and consistency with any affected ledger.
- Run the repository Markdown TOC `format` and `check` commands on every Markdown file changed by
  this workflow, including local artifacts. Recheck artifact isolation before completion.
- Report the requirements path, readiness for drafting, material assumptions, unresolved questions
  and dependency gates, and validation actually performed. An assessment can be complete with
  clearly recorded blockers; that does not make the future ticket ready for drafting or delivery.
- Stop after requirements assessment. Do not create or revise ticket content, produce a delivery
  plan, or start implementation as a side effect of this skill. If reassessment affects an existing
  specification, record the discrepancy and required follow-up without silently changing it.
- When specification drafting is also requested, hand off explicitly to
  [ticket specification](../ticket-specification-workflow/SKILL.md) with the artifact path; otherwise
  leave that as a later action. The handoff does not require repeating the assessment from scratch.
  This skill does not authorize external ticket publication, commits, or pushes.
