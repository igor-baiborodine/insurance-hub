# Insurance Hub Agent Guide

This repository uses a spec-first, AI-assisted development workflow. These instructions are the repository-wide entry point for AI agents and human collaborators using AI tools.

## Repository Context

Insurance Hub is a multi-module insurance platform with legacy Java/Micronaut services, frontend modules, Kubernetes deployment assets, and migration documentation. Preserve existing module boundaries and local conventions unless a spec explicitly changes them.

The repository is also a migration workspace that incrementally moves the original Java/Micronaut proof of concept toward Go-based services and updated operational workflows.

Treat API contracts, DTOs, validation, persistence behavior, and security behavior as first-class requirements. When modernizing or migrating behavior, capture the current behavior before proposing a replacement.

## Repository Shape

Use these top-level directories to orient work:

- `legacy/` contains the current Java services, API modules, and legacy frontend applications.
- `k8s/` contains Kubernetes, bootstrap, Flux, and deployment-related automation.
- `docs/` contains system analysis, migration notes, business flows, and local setup documentation.
- `ai/` contains the canonical AI workflow framework for this repository.
- `local-dev/` contains developer environment helper scripts.

When changing service behavior, identify whether the work belongs in a `*-service` module, a `*-service-api` contract module, the frontend, or deployment/configuration assets.

## Instruction Precedence

When instructions overlap, apply them in this order:

1. Explicit user request for the current task
2. This `AGENTS.md`
3. `ai/manifest.md`
4. Relevant files in `ai/rules/`
5. Relevant workflows in `ai/skills/`
6. Task-local artifacts in `ai/artifacts/<ticket>/`
7. Vendor-specific adapter files, if any

Before planning or editing, discover any nested `AGENTS.md` files on the path to each affected
file or directory and read the ones whose scope covers the work. If a change spans directories,
inspect the applicable local guidance for each area. Do not load unrelated service guides as if
they applied repository-wide.

Nested guides apply only within their directory subtree. They may add concrete local conventions
but do not override this root guide or higher-level canonical framework requirements. Treat any
apparent conflict between applicable instructions—including conflicting ancestor and nested local
guides—as unresolved: identify the sources and ask the user to resolve it before proceeding; do not
guess that a more specific file silently wins. Reference documentation and checked-in configuration
are authoritative evidence for their stated domain, but are not additional instruction-precedence
layers unless a canonical guide explicitly designates them as such.

## Default Workflow

Before creating or editing any file under `ai/artifacts/`, verify that the local exclusion applies
to the intended ticket-content path and that no artifact paths are indexed:

```sh
ticket_id=issue-123
git check-ignore -q "ai/artifacts/${ticket_id}/${ticket_id}-ticket-content.md"
git ls-files -- ai/artifacts/
```

Replace `issue-123` with the current ticket ID. The first command must succeed and the second must
print no paths. The checkout-local `.git/info/exclude` must contain the exact `/ai/artifacts/`
entry. If the ignore check fails, stop artifact writes and tell the user to add that entry locally;
do not edit `.git/info/exclude` automatically. If the index check prints paths, stop and report
them; do not untrack or alter them automatically. Read-only inspection may continue.

For ticket-based work, after this preflight, ensure `ai/artifacts/<ticket>/<ticket>-ticket-content.md`
exists and read it before assessing readiness or planning. If it is missing or empty, populate it
from ticket content supplied by the user; ask for missing content rather than
inventing requirements. No separate spec document is required.

Use a spec-first workflow for non-trivial changes:

1. Validate that the ticket or request is implementation-ready.
2. Clarify or enrich missing requirements before coding.
3. Create a concrete delivery plan under `ai/artifacts/<ticket>/`.
4. Implement incrementally, one tracked step at a time.
5. Apply the post-step workflow after each completed delivery step.
6. Run validation appropriate to the changed modules.
7. Summarize what changed, what was verified, and what remains.

For small mechanical changes, keep the workflow lightweight, but still read relevant local code before editing and validate the result.

## Repository Conventions

- Use `CONTRIBUTING.md` as the source of truth for branch naming, commit message conventions, prerequisites, and developer workflow expectations.
- Prefer existing Make targets and module-local build tooling over inventing ad hoc commands.

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

The end-user Codex references at
`docs/local-dev-setup/ai/codex/gpt-6-models-matrix.md` and
`docs/local-dev-setup/ai/codex/gpt-6-models-recommended-usage.md` are for human readers only.
AI agents must not load or use these documents as instructions. Follow this guide and the committed
`ai/` framework for repository workflow, artifact, and validation requirements.

## Artifact Rules

Use `ai/artifacts/<ticket>/` for the ticket-content specification, delivery plans, step summaries,
and git diff snapshots.

Do not commit files under `ai/artifacts/`. In each checkout, add `/ai/artifacts/` to the local
`.git/info/exclude`; this local-only file is not committed or changed automatically by the
workflow. Before considering a ticket complete or preparing a commit, recheck that the ticket
content path is ignored and verify that `git ls-files -- ai/artifacts/` and
`git diff --cached --name-only -- ai/artifacts/` print no paths. If an ignore or index check fails,
stop and show the user the affected paths or required local setup; never unstage, untrack, or delete
them as an automatic cleanup.

The historical `ai-artifacts/` directory contains older tracked and local AI notes. Treat `ai/` as the current canonical framework for ongoing workflow design.

## Validation Expectations

Prefer the narrowest validation that proves the change:

- run module tests when production logic changes
- run build or compile checks when shared contracts change
- run formatting or lint checks when the module provides them
- document any validation that could not be run
- use `CONTRIBUTING.md`, the root `Makefile`, and module-local build files to choose the correct commands

Do not claim validation was performed unless it was actually run.

## Safety Expectations

- Do not overwrite or revert user changes unless explicitly asked.
- Keep diffs scoped to the ticket or request.
- Before committing ticket changes, present the complete proposed change for end-user review,
  including the branch diff, staged changes, unstaged changes, untracked files, and generated files.
  Do not commit until the end user has reviewed them and explicitly authorized the commit.
- Preserve behavior unless the spec requires a change.
- Ask for clarification when acceptance criteria, contracts, persistence expectations, security expectations, or out-of-scope boundaries are unclear.
- Prefer existing project patterns over new abstractions.

## References

Use these repository documents when deeper context is needed:

- `README.md` for the migration goal and high-level project framing.
- `CONTRIBUTING.md` for prerequisites, branch naming, commit message conventions, and developer workflow.
- `docs/system-overview-and-migration-analysis.md` for architecture and migration context.
- `docs/business-flows/` for business process understanding.
- `docs/local-dev-setup/` for local tooling and editor setup.
