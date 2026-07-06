# Repository Rules

## General

- Read relevant local code and docs before changing behavior.
- Prefer existing project patterns over new abstractions.
- Keep changes scoped to the request or ticket.
- Preserve behavior unless the spec explicitly changes it.
- Avoid unrelated formatting, metadata churn, or refactors.
- Update documentation when behavior, setup, workflow, or contracts change.

## Git And Artifacts

- Do not commit files under `ai/artifacts/`.
- Do not overwrite or revert user changes unless explicitly requested.
- For ticket work, store planning, step summaries, and diff snapshots under `ai/artifacts/<ticket>/`.
- Keep committed AI framework files under `ai/` concise and reusable.

## Validation

- Run the narrowest meaningful tests or checks for the changed modules.
- If validation is skipped or unavailable, state that explicitly and explain why.
- Do not treat generated plans or summaries as a substitute for running checks.
