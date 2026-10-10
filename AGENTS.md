<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Insurance Hub Agent Guide](#insurance-hub-agent-guide)
  - [Repository Context](#repository-context)
  - [Repository Shape](#repository-shape)
  - [Instruction Precedence](#instruction-precedence)
  - [Default Workflow](#default-workflow)
  - [Skills](#skills)
  - [Repository Conventions](#repository-conventions)
  - [AI Directory](#ai-directory)
  - [Artifact Rules](#artifact-rules)
  - [Validation Expectations](#validation-expectations)
  - [Safety Expectations](#safety-expectations)
  - [References](#references)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

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
6. Task-local artifacts in the resolved `<ticket-dir>` under `ai/artifacts/`
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

Resolve `<ticket-dir>` using [Artifact Rules](#artifact-rules) before looking up or creating ticket
content. Before creating or editing any file under `ai/artifacts/`, verify that the local exclusion
applies to the intended ticket-content path and that no artifact paths are indexed:

```sh
ticket_id=issue-131
ticket_dir="ai/artifacts/epic-4.2/${ticket_id}"
git check-ignore -q "${ticket_dir}/${ticket_id}-ticket-content.md"
git ls-files -- ai/artifacts/
```

Run from the repository root with the current ticket ID and resolved directory; the example uses
an epic child ticket. The ignore check must succeed and the index check must print no paths.
The checkout-local `.git/info/exclude` must contain the exact `/ai/artifacts/`
entry. If the ignore check fails, stop artifact writes and tell the user to add that entry locally;
do not edit `.git/info/exclude` automatically. If the index check prints paths, stop and report
them; do not untrack or alter them automatically. Read-only inspection may continue.

For ticket-based work, after this preflight, ensure `<ticket-dir>/<ticket>-ticket-content.md`
exists and read it before assessing readiness or planning. If it is missing or empty, populate it
from ticket content supplied by the user; ask for missing content rather than
inventing requirements. No separate spec document is required.

Use a spec-first workflow for non-trivial changes:

1. Validate that the ticket or request is implementation-ready.
2. Clarify or enrich missing requirements before coding.
3. Create a concrete delivery plan under the same resolved `<ticket-dir>`.
4. Implement incrementally, one tracked step at a time.
5. Apply the post-step workflow after each completed delivery step.
6. Run validation appropriate to the changed modules.
7. Summarize what changed, what was verified, and what remains.

For small mechanical changes, keep the workflow lightweight, but still read relevant local code before editing and validate the result.

## Skills

Repository skills are canonical reusable workflows under `ai/skills/`. Use this catalog to select
the smallest set that matches the current task, then read the selected `SKILL.md` completely before
acting. Read linked references or scripts only as directed by that skill.

| Skill | Use when | Workflow relationship |
| --- | --- | --- |
| [`code-review`](ai/skills/code-review/SKILL.md) | Reviewing a pull request or proposed change against ticket requirements, repository rules, contracts, tests, and evidence. | Read-only workflow with local-checkout and GitHub Copilot modes. Do not implement fixes during the review. |
| [`pr-description`](ai/skills/pr-description/SKILL.md) | Drafting a local PR description artifact or drafting and explicitly updating an existing GitHub pull request body. | Uses shared content and evidence standards with separate local and GitHub modes. A draft-only request does not authorize a GitHub update. |
| [`ticket-implementation-workflow`](ai/skills/ticket-implementation-workflow/SKILL.md) | Implementing a non-trivial ticket from readiness assessment through validated delivery steps. | Primary ticket delivery workflow; it invokes `ticket-post-step-workflow` after each completed step. |
| [`ticket-post-step-workflow`](ai/skills/ticket-post-step-workflow/SKILL.md) | A concrete ticket delivery step has been completed and its tracker, summary, validation evidence, and branch snapshot must be updated. | Use after each completed implementation step, normally through `ticket-implementation-workflow`. |

Select skills from their names and descriptions rather than loading every skill at session start.
When multiple skills apply, follow their stated sequence and keep their boundaries: completing a
review-only request does not authorize implementation, and completing implementation does not
replace the required post-step workflow. Briefly identify selected skills in working notes.

Files under `.github/skills/` are vendor discovery adapters, not a second skill system.
`.github/skills/code-review/SKILL.md` and `.github/skills/pr-description/SKILL.md` route GitHub
Copilot to their canonical workflows under `ai/skills/`.

## Repository Conventions

- Use `CONTRIBUTING.md` as the source of truth for branch naming, commit message conventions, prerequisites, and developer workflow expectations.
- Prefer existing Make targets and module-local build tooling over inventing ad hoc commands.
- After creating or updating any Markdown file, run
  `./scripts/markdown-toc.sh format <file>...` for every Markdown file changed by the current task,
  then run the same command with `check`. The script uses the repository-pinned `doctoc` version;
  never hand-edit its generated table of contents. Pass only task-owned files so unrelated user
  changes remain untouched.

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

`<ticket>` is the issue identifier, such as `issue-131`. `<ticket-dir>` is its resolved artifact
directory, relative to the repository root. Supported layouts are:

| Ticket organization | `<ticket-dir>` | Ticket-content example |
| --- | --- | --- |
| Standalone ticket | `ai/artifacts/<ticket>` | `ai/artifacts/issue-123/issue-123-ticket-content.md` |
| Ticket grouped under an epic | `ai/artifacts/epic-<number>/<ticket>` | `ai/artifacts/epic-4.2/issue-131/issue-131-ticket-content.md` |
| Epic's own ticket | `ai/artifacts/epic-<number>` | `ai/artifacts/epic-4.2/issue-130-ticket-content.md` |

Resolve the directory once and reuse it for the entire ticket workflow:

1. Honor an explicit user-supplied ticket-content path or artifact directory under `ai/artifacts/`.
2. Otherwise, search existing ticket-content files and ticket folders in both standalone and
   epic-grouped locations. Include ignored files/directories in discovery: ordinary `rg --files`
   omits these local artifacts. Reuse the matching location, including an existing empty placeholder
   folder; do not create a flat duplicate because a nested ticket-content file is missing.
3. If multiple locations match and the session does not identify the intended one, ask which to
   use before writing. Do not merge, relocate, or delete artifacts automatically.
4. If no location exists, use the epic grouping specified by the user or current task context;
   otherwise default to `ai/artifacts/<ticket>`. Do not infer epic membership from the issue number.

Keep the ticket-content specification, delivery plan, step summaries, diff snapshots, reviews, and
PR drafts together in `<ticket-dir>`. Filenames retain the ticket ID, such as
`<ticket>-ticket-content.md` and `<ticket>-delivery-steps.md`, regardless of directory depth.
Shared templates use `<ticket-dir>` as a placeholder; replace it with the resolved path. Resolve
relative links from each artifact's actual directory rather than assuming a fixed nesting depth.
Existing flat artifacts remain valid; adding epic grouping does not require moving them.

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
