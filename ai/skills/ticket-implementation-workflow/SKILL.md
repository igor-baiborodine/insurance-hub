---
name: ticket-implementation-workflow
description: Use when planning delivery or implementing a ticket in this repository. Validates readiness and creates a delivery plan; a plan-only request stops there. For implementation, executes step by step and applies the ticket post-step workflow after each completed step.
---

<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Ticket Implementation Workflow](#ticket-implementation-workflow)
  - [Prerequisite](#prerequisite)
  - [Workflow](#workflow)
    - [1. Validate Ticket Readiness](#1-validate-ticket-readiness)
    - [2. Produce The Detailed Plan](#2-produce-the-detailed-plan)
    - [3. Implement Step By Step](#3-implement-step-by-step)
    - [4. Apply Post-Step Workflow](#4-apply-post-step-workflow)
  - [Completion Checklist](#completion-checklist)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Ticket Implementation Workflow

Use this skill for ticket-based specification-first development work in Insurance Hub.
A plan-only request ends after producing the plan in Step 2; continue to execution only when
implementation is requested.

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

Resolve `<ticket-dir>` using [AGENTS.md Artifact Rules](../../../AGENTS.md#artifact-rules), then
read `<ticket-dir>/<ticket>-ticket-content.md`. This required file is the ticket's specification;
no separate spec document is needed. Reuse that directory for every artifact below, including
when it is grouped under an epic. Apply the root artifact preflight before any artifact write.

If the file is missing or empty, create or populate it from ticket content supplied by the user before
planning. Use `ai/templates/ticket-content-template.md` when helpful to structure the content.
If the ticket content is unavailable, ask the user for it rather than inferring requirements from
the ticket ID. Preserve existing content and incorporate agreed clarifications into this file.

Before implementation, confirm that the ticket-content specification contains enough information to proceed.

Check for:

- concrete business objective
- endpoint, schema, and contract details when API work is involved
- validation and error-handling expectations
- persistence expectations and out-of-scope boundaries
- permissions and security requirements
- acceptance criteria that can be tested

For auxiliary Python work, load [the Python entry point](../../rules/python-rules.md) and all four
linked rules. Establish why Python fits better than a simple shell workflow, identify the script
owner and Make interface, and define target selection, mutation boundaries, rerun/recovery
behavior, and secret handling where applicable. Do not turn a tooling request into a Python
service or a wholesale shell rewrite.

If information is missing or ambiguous:

- stop implementation work
- identify the gaps explicitly
- work with the user to clarify requirements and update the ticket-content file
- do not move to planning until the implementation path is clear enough

For a scope-assessment request, select
[`ticket-requirements-workflow`](../ticket-requirements-workflow/SKILL.md); for specification
drafting, select [`ticket-specification-workflow`](../ticket-specification-workflow/SKILL.md).
Those workflows stop at their own artifacts. Use this implementation workflow for delivery
planning only when planning or implementation is requested; a plan-only request stops before
execution. Read the assessment when present, but use ticket content as the implementation
specification; do not require retrospective requirements files for existing ready tickets.

A conditional specification may carry bounded decision-resolution tasks. Validate each task's
safe investigation scope, responsible role, expected evidence, acceptance condition, and blocked
dependencies. Only these prerequisite tasks may proceed while the decision is open. Do not
silently resolve disagreements between requirements and ticket content or treat missing scope,
contracts, or safety obligations as ordinary implementation choices.

### 2. Produce The Detailed Plan

Once the ticket is implementation-ready:

- create the delivery steps artifact at `<ticket-dir>/<ticket>-delivery-steps.md`
- use `ai/templates/delivery-steps-template.md` and reference the ticket-content file as the specification
- assess whether the ticket should be delivered in one pull request or split across multiple pull
  requests, and record the decision and rationale in the plan
- when multiple pull requests are appropriate, mark every delivery step clearly with its planned
  pull request as `PR1`, `PR2`, and so on, identify ordering or dependency constraints, and use
  the corresponding `-1`, `-2`, and later suffixes for new PR branches as required by
  [Git branch rules](../../rules/git-rules.md#branch-names)
- make the steps granular enough to track progress and validate behavior incrementally
- include a progress section containing only step status, step title, planned pull request when
  applicable, and the step summary artifact reference; keep implementation details, findings,
  decisions, and validation results in the dedicated step execution summary document
- identify module boundaries, tests, and validation checkpoints

For conditional readiness, put each decision-resolution task before its dependent steps and
record the gate in the plan. Dependent steps remain blocked until the decision is recorded in
ticket content, the assessment is reconciled if present, and implementation readiness is rechecked.
Do not invent detailed implementation steps that depend on an unknown decision outcome.

For Python tooling, distinguish pure logic/fake-adapter tests, disposable integration tests, and
any separately authorized live operations. Select checks through
[Python validation](../../rules/python-validation.md); record missing targets or prerequisites
without treating them as successful validation.

### 3. Implement Step By Step

Implement one delivery step at a time.

For each step:

- Before making any change for the step, refresh the committed-branch snapshot using the post-step
  workflow's `git diff main <current-branch>` convention and save it to
  `<ticket-dir>/<ticket>-git-diff.txt`. Confirm `main` and the current branch resolve
  before replacing the artifact; if they do not, preserve the previous snapshot and report the
  blocker.
- Immediately check `git status --short` and report any staged, unstaged, or untracked work to the
  end user before editing. Inspect those changes and preserve them. If they overlap planned files or
  make ownership/scope unclear, resolve that with the user before proceeding.
- read the relevant local code before changing anything
- implement against the ticket-content specification and record agreed requirement changes there before adjusting the plan
- resolve prerequisite decision gates and recheck readiness before starting dependent implementation
- challenge assumptions and weak designs
- prefer established repository patterns over inventing new ones
- keep scope tight to the step at hand
- validate the proposed implementation critically instead of accepting the first workable approach
- For QA cluster, Flux, DNS, or registry-pull work, apply the repository
  [QA cluster network recovery rule](../../rules/repo-rules.md#qa-cluster-network-recovery) before
  classifying stale source or image state as a repository defect.

### 4. Apply Post-Step Workflow

After each completed implementation step, apply:

- `ai/skills/ticket-post-step-workflow/SKILL.md`

That workflow updates the delivery tracker, creates the per-step summary, and refreshes the ticket git diff artifact.

## Completion Checklist

Before considering the ticket workflow complete, ensure:

- the ticket-content file exists and reflects the agreed requirements
- ticket readiness was explicitly validated
- the delivery-step plan artifact exists
- each completed step has a matching summary artifact
- each step summary records applicable validation with the shared evidence format and each
  acceptance criterion maps to evidence or is identified as unresolved
- post-step workflow was applied after each completed step
- formatting and tests were run as required by the repository instructions and ticket scope
