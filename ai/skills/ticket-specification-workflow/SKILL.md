---
name: ticket-specification-workflow
description: Draft or revise local ticket-content specifications from an existing requirements assessment. Use when ticket specification drafting is requested, including in a later session; does not plan or implement delivery. GitHub synchronization requires separate explicit permission.
---

<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Ticket Specification Workflow](#ticket-specification-workflow)
  - [Inputs and Readiness](#inputs-and-readiness)
  - [Draft the Ticket Content](#draft-the-ticket-content)
  - [Validation and Handoff](#validation-and-handoff)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Ticket Specification Workflow

Turn an assessed scope and classified requirements into a self-contained ticket specification.
This skill can run in a later session than requirements assessment. Its output is
`<ticket-dir>/<ticket>-ticket-content.md`; implementation planning remains a separate workflow.

## Inputs and Readiness

1. Follow [AGENTS.md](../../../AGENTS.md), [the manifest](../../manifest.md), and the
   [spec-first rules](../../rules/spec-first-rules.md). Discover scoped instructions for affected
   paths and resolve `<ticket-dir>` through [Artifact Rules](../../../AGENTS.md#artifact-rules),
   honoring explicit paths and existing requirements artifacts, including ignored epic folders.
   Apply the root artifact preflight before writing; never change the exclusion or index automatically.
2. Read `<ticket-dir>/<ticket>-requirements.md`, existing ticket content when present, and the
   source ACs, decisions, and relevant evidence referenced by the assessment. Preserve agreed
   content and stable IDs; do not overwrite an existing specification with a new scaffold.
3. If requirements assessment is missing or materially incomplete, identify the missing input and
   use [ticket requirements](../ticket-requirements-workflow/SKILL.md) for the needed assessment
   before drafting. A request for a specification from raw input may compose those two skills,
   but neither proceeds to implementation planning automatically. Do not invent requirements
   from an issue number or require a retrospective assessment merely to implement a ready ticket.
4. Recheck readiness against current evidence and any changes since assessment. Reuse valid
   assessment work; inspect changed or uncertain boundaries rather than repeating it wholesale.
   Reconcile material conflicts with the user, record agreed assessment updates, and preserve
   the meaning of supplied ACs. Do not treat an old “ready” label as sufficient evidence.

The readiness gate has three outcomes:

- **Blocked:** unresolved scope, AC, contract, or safety questions prevent a meaningful
  specification. Record the blockers, request clarification, and stop drafting. Applicable
  security, personal-data protection, and data-integrity requirements cannot become optional
  merely to get past this gate.
- **Ready:** the requirements and decisions support an observable, bounded specification.
- **Conditionally ready for decision work:** every remaining question defines a safe investigation
  scope, responsible role, expected decision evidence, acceptance condition, and blocked dependent
  work. A disposable technical comparison under an agreed contract can qualify; unresolved
  permission to expose personal data cannot. Do not guess business policy or public contracts.
  Separately scoped safe discovery can investigate a blocker without authorizing dependent work.

## Draft the Ticket Content

Use the [ticket-content template](../../templates/ticket-content-template.md) in the same resolved
artifact directory. Preserve the assessment's in-scope and out-of-scope boundaries.

- Carry forward the problem, intended outcome, mandatory requirements, constraints, dependencies,
  and resolved decisions. Map every must-have ID to observable ACs and expected validation;
  preserve supplied AC identity and meaning. Link the requirements assessment for provenance,
  but do not leave mandatory requirements defined only in that assessment or an optional ledger.
- Give behavior ACs stable IDs. In Dev Notes, link each important success, failure, and compatibility
  example to its requirement/AC IDs, affected boundary, independently specified expected result,
  and proposed test or manual scenario ID. Keep bug reproductions and migration baseline fixtures separate from the
  replacement implementation. If a case cannot be automated, explain the manual route rather than
  presenting it as a passing test. Do not require executable business examples for routine
  documentation-only changes.
- For substantial contract, persistence, or architecture choices, record a short design challenge
  in Dev Notes: alternatives and tradeoffs, failure modes, migration impact, and why the selected
  design meets the ACs. Leave unresolved choices as bounded decision gates; a shared architecture
  decision is needed only if the reasoning will serve work beyond this ticket.
- Exclude deferred nice-to-haves from mandatory scope and ACs. Link their disposition without
  implying a delivery commitment. If an optional item is explicitly selected, record the decision
  and update its classification and receiving scope/ACs before treating it as required; follow
  applicable ledger conventions when a ledger update is within the request.
- For a conditional draft, identify each deferred question in Dev Notes with its resolution
  task, safe scope, responsible role, expected evidence, acceptance condition, and dependency gate.
  Add an AC requiring the decision to be recorded and ticket content updated before dependent
  implementation. Do not fabricate the decision's outcome or imply that listing it resolves it.
- Describe prerequisite decision tasks sufficiently for later planning, but do not create
  `<ticket-dir>/<ticket>-delivery-steps.md` or expand the specification into an implementation
  plan. The implementation workflow later orders these tasks and gates dependent work.
- Keep ticket content as the authoritative implementation specification and requirements as the
  assessment/provenance record. Update the assessment's specification link and must-have-to-AC
  coverage, and reconcile any agreed revisions before handoff.

## Validation and Handoff

- Check source-AC preservation, must-have-to-AC coverage, optional exclusions, question dispositions,
  and relative links at the actual directory depth. Confirm that conditional drafts identify
  decision work without assuming its result and that mandatory safeguards remain intact.
- Run repository Markdown TOC `format` and `check` on all changed Markdown, including local
  artifacts. Recheck artifact isolation before completion.
- Report the specification path, readiness, unresolved decision gates, and validation actually
  performed. A drafted specification is not evidence that the future feature was implemented.
- For an issue-based ticket, apply [GitHub ticket synchronization](../../rules/spec-first-rules.md#github-ticket-synchronization)
  after final formatting: prepare the issue-body difference and request explicit update permission
  unless existing authorization covers it. Verify the published body after any authorized update.
  Keep evidence in the requirements assessment when no tracker exists. Report pending or blocked
  synchronization separately from specification readiness; ad hoc tickets are exempt.
- Stop after specification drafting. If planning or implementation is separately requested,
  hand off to [ticket implementation](../ticket-implementation-workflow/SKILL.md), which owns
  the delivery plan, readiness recheck, and post-step workflow. This skill does not authorize
  code changes, external ticket publication, commits, or pushes.
