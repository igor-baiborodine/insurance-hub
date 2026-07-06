# AI Framework Manifest

The `ai/` directory is the repository-owned, vendor-agnostic framework for AI-assisted development in Insurance Hub.

## Purpose

This framework exists to make AI-assisted work repeatable, auditable, and portable across tools. It separates stable repository policy from reusable workflows, prompts, examples, and local ticket artifacts.

## Structure

- `rules/` - stable engineering and process rules
- `skills/` - repeatable workflows for recurring tasks
- `prompts/` - reusable prompt fragments
- `examples/` - examples of acceptable artifacts and implementation patterns
- `templates/` - reusable spec, plan, verification, and PR templates
- `checks/` - task checklists
- `artifacts/` - local per-ticket working files, excluded from Git

## Source Of Truth

`AGENTS.md` is the repository entry point. This manifest defines how the `ai/` framework is organized and maintained. Vendor-specific files may point into this framework, but should not duplicate or override it.

## Vendor Adapters

Tool-specific files should be thin pointers into the canonical framework.

- `CONTINUE.md` is the root Continue-facing pointer for human discoverability.
- `.continue/rules/CONTINUE.md` is the Continue adapter for repository rules only.

These files must redirect to `AGENTS.md` and `ai/`; they must not become an independent rule system.

## Loading Order

At session start:

1. Read `AGENTS.md`.
2. Read this manifest.
3. Load only the rules, skills, prompts, templates, or examples relevant to the current task.
4. Load task-local artifacts from `ai/artifacts/<ticket>/` when continuing existing work.

## Precedence

Instruction precedence is:

1. explicit user request
2. root `AGENTS.md`
3. `ai/manifest.md`
4. relevant `ai/rules/*`
5. relevant `ai/skills/*`
6. task-local artifacts
7. vendor-specific adapters

If instructions conflict, stop and clarify instead of guessing.

## Maintenance Rules

- Keep rules stable and concise.
- Keep skills procedural and task-specific.
- Keep prompts lightweight and composable.
- Keep examples realistic and clearly labeled.
- Keep local ticket artifacts out of Git.
- Add language-specific rules only when they encode real repository practice.
