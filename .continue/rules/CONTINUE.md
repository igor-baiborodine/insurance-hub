# Continue Rules Adapter

This file is a Continue rules adapter. It does not define separate repository policy.

For repository rules, follow the canonical instructions in this order:

1. `AGENTS.md`
2. `ai/manifest.md`
3. relevant files in `ai/rules/`

Use `AGENTS.md` for the full workflow entry point. Use `ai/manifest.md` to understand the canonical AI framework structure and instruction precedence.

Keep this file thin. If repository-wide rules change, update `AGENTS.md` or `ai/rules/`, not this adapter.
