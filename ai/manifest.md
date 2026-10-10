<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [AI Framework Manifest](#ai-framework-manifest)
  - [Purpose](#purpose)
  - [Structure](#structure)
  - [Source Of Truth](#source-of-truth)
  - [Vendor Adapters](#vendor-adapters)
  - [Loading Order](#loading-order)
  - [Precedence](#precedence)
  - [Maintenance Rules](#maintenance-rules)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# AI Framework Manifest

The `ai/` directory is the repository-owned, vendor-agnostic framework for AI-assisted development in Insurance Hub.

## Purpose

This framework exists to make AI-assisted work repeatable, auditable, and portable across tools. It separates stable repository policy from reusable workflows, prompts, examples, and local ticket artifacts.

## Structure

- `rules/` - stable engineering and process rules
- `skills/` - repeatable workflows for recurring tasks
- `prompts/` - reusable prompt fragments, indexed in `ai/prompts/README.md`
- `examples/` - examples of acceptable artifacts and implementation patterns
- `templates/` - reusable ticket-content specification, delivery plan, and PR templates
- `checks/` - task checklists
- `artifacts/` - local ticket working files, standalone or grouped under `epic-<number>/`, excluded from Git

## Source Of Truth

`AGENTS.md` is the repository entry point. This manifest defines how the `ai/` framework is organized and maintained. Vendor-specific files may point into this framework, but should not duplicate or override it.

## Vendor Adapters

Tool-specific files should be thin pointers into the canonical framework.

- `CONTINUE.md` is the root Continue-facing pointer for human discoverability.
- `.continue/rules/CONTINUE.md` is the Continue adapter for repository rules only.
- `.github/skills/code-review/SKILL.md` is the GitHub Copilot discovery adapter for the canonical
  code-review workflow under `ai/skills/`.
- `.github/skills/pr-description/SKILL.md` is the GitHub Copilot discovery adapter for the
  canonical PR-description workflow under `ai/skills/`.
- `.github/copilot-instructions.md` routes GitHub Copilot reviews and PR-description work to the
  corresponding adapters.

These files must redirect to `AGENTS.md` and `ai/`; they must not become an independent rule system.

## Loading Order

At session start:

1. Read `AGENTS.md`.
2. Read this manifest.
3. Use the `AGENTS.md` Skills catalog and each skill's frontmatter to select applicable skills;
   read selected `SKILL.md` files completely, without loading unrelated skills.
4. Load only the other rules, prompts, templates, or examples relevant to the current task.
5. For ticket work, resolve `<ticket-dir>` using [AGENTS.md Artifact Rules](../AGENTS.md#artifact-rules)
   and read `<ticket-dir>/<ticket>-ticket-content.md` as the specification before readiness
   assessment or planning. Load the delivery plan and step summaries from that same directory
   when continuing existing work.

Before planning or editing, follow `AGENTS.md` to discover and load nested instructions that apply
to the affected paths. Do not load unrelated service guides. Root `AGENTS.md` is the canonical
definition of instruction precedence, local-guide scope, and conflict handling.

The ticket-content file is required at ticket startup. Follow `AGENTS.md` if it is missing or
empty. `ai/templates/ticket-content-template.md` provides a structure for its content; a separate
spec file is not required.

## Precedence

Use the precedence order and conflict procedure defined in root [`AGENTS.md`](../AGENTS.md).
This manifest describes how to load the shared framework; it does not maintain a second precedence
list. Nested instruction discovery is scoped to the affected paths as described there.

## Maintenance Rules

For Go implementation, migration, or tooling work, load [the Go entry point](rules/go-rules.md)
and all four linked rules: [development](rules/go-development.md), [testing](rules/go-test.md),
[formatting](rules/go-formatting.md), and [validation](rules/go-validation.md).
They require Makefile targets for Go development operations and supplement root policy. Discover
applicable service-local instructions using the procedure in [`AGENTS.md`](../AGENTS.md); surface
and resolve conflicts rather than silently treating a local guide as an override.

- Keep rules stable and concise.
- Keep skills procedural and task-specific.
- Keep the `AGENTS.md` Skills catalog synchronized with canonical directories under `ai/skills/`.
- Keep vendor skill adapters as pointers to canonical skills; do not duplicate workflow bodies.
- Keep prompts lightweight and composable.
- Keep examples realistic and clearly labeled.
- Keep local ticket artifacts out of Git.
- Add language-specific rules only when they encode real repository practice.
