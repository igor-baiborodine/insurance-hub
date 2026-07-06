# Insurance Hub Agent Guide

This repository uses a spec-first, AI-assisted development workflow. These instructions are the repository-wide entry point for AI agents and human collaborators using AI tools.

## Repository Context

Insurance Hub is a multi-module insurance platform with legacy Java/Micronaut services, frontend modules, Kubernetes deployment assets, and migration documentation. Preserve existing module boundaries and local conventions unless a spec explicitly changes them.

## Instruction Precedence

When instructions overlap, apply them in this order:

1. Explicit user request for the current task
2. This `AGENTS.md`
3. `ai/manifest.md`
4. Relevant files in `ai/rules/`
5. Relevant workflows in `ai/skills/`
6. Task-local artifacts in `ai/artifacts/<ticket>/`
7. Vendor-specific adapter files, if any

If instructions conflict, surface the conflict before proceeding.

## Default Workflow

Use a spec-first workflow for non-trivial changes:

1. Validate that the ticket or request is implementation-ready.
2. Clarify or enrich missing requirements before coding.
3. Create a concrete delivery plan under `ai/artifacts/<ticket>/`.
4. Implement incrementally, one tracked step at a time.
5. Apply the post-step workflow after each completed delivery step.
6. Run validation appropriate to the changed modules.
7. Summarize what changed, what was verified, and what remains.

For small mechanical changes, keep the workflow lightweight, but still read relevant local code before editing and validate the result.

## AI Directory

The canonical AI framework lives under `ai/`:

- `ai/manifest.md` explains structure, precedence, and maintenance rules.
- `ai/rules/` contains stable repository and engineering rules.
- `ai/skills/` contains repeatable workflows.
- `ai/prompts/` contains reusable prompt fragments.
- `ai/examples/` contains examples and reference artifacts.
- `ai/templates/` contains reusable artifact templates.
- `ai/checks/` contains validation checklists.
- `ai/artifacts/` contains local ticket working artifacts and must not be tracked.

## Artifact Rules

Use `ai/artifacts/<ticket>/` for temporary ticket artifacts such as delivery plans, step summaries, and git diff snapshots.

Do not commit files under `ai/artifacts/`. The path should be excluded locally in `.git/info/exclude`.

## Validation Expectations

Prefer the narrowest validation that proves the change:

- run module tests when production logic changes
- run build or compile checks when shared contracts change
- run formatting or lint checks when the module provides them
- document any validation that could not be run

Do not claim validation was performed unless it was actually run.

## Safety Expectations

- Do not overwrite or revert user changes unless explicitly asked.
- Keep diffs scoped to the ticket or request.
- Preserve behavior unless the spec requires a change.
- Ask for clarification when acceptance criteria, contracts, persistence expectations, security expectations, or out-of-scope boundaries are unclear.
- Prefer existing project patterns over new abstractions.
