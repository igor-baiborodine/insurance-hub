<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Continue Adapter](#continue-adapter)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Continue Adapter

This human-facing pointer complements the Continue rule in `.continue/rules/CONTINUE.md`.
The rule is the repository discovery entry point; `AGENTS.md` remains the canonical guide.

Before working in this repository with Continue:

1. Read and follow `AGENTS.md`.
2. Read `ai/manifest.md` for the canonical AI framework structure.
3. Load relevant rules, skills, prompts, templates, checks, and examples from `ai/` based on the task.

Do not duplicate repository policy in this file. Keep this file as a Continue-facing pointer to the canonical instructions.

For ticket stages, select the matching skill from the `AGENTS.md` catalog. For implementation,
use:

- `ai/framework/prompts/session-bootstrap.md`
- `ai/framework/skills/ticket-implementation-workflow/SKILL.md`
- `ai/framework/skills/ticket-post-step-workflow/SKILL.md`

Resolve local ticket paths through [AGENTS.md Artifact Rules](AGENTS.md#artifact-rules), which
supports standalone and epic-grouped tickets. Artifacts remain local-only and must not be committed.
