# Continue Adapter

This repository uses `AGENTS.md` as the vendor-agnostic source of truth for AI-assisted development.

Before working in this repository with Continue:

1. Read and follow `AGENTS.md`.
2. Read `ai/manifest.md` for the canonical AI framework structure.
3. Load relevant rules, skills, prompts, templates, checks, and examples from `ai/` based on the task.

Do not duplicate repository policy in this file. Keep this file as a Continue-facing pointer to the canonical instructions.

For ticket-driven implementation, use:

- `ai/prompts/session-bootstrap.md`
- `ai/skills/ticket-implementation-workflow/SKILL.md`
- `ai/skills/ticket-post-step-workflow/SKILL.md`

Local ticket artifacts belong under `ai/artifacts/<ticket>/` and must not be committed.
