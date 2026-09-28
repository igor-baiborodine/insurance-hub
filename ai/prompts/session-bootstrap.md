# Session Bootstrap Prompt

Before starting work on this repository, read and follow the instructions in `AGENTS.md`.
Then read `ai/manifest.md` for the loading order and framework structure.

For ticket work, ensure `ai/artifacts/<ticket>/<ticket>-ticket-description.md` exists and read it
as the specification before readiness assessment or planning. Follow `AGENTS.md` when the file
is missing or empty. For an existing ticket, also read its delivery plan and relevant step summaries.

Also load and follow shared guidance from these folders when relevant to the task:

- `ai/rules/`
- `ai/skills/`
- `ai/examples/`
- `ai/templates/`
- `ai/checks/`

Treat files in `ai/rules/` as task-specific or repository working rules that supplement `AGENTS.md`. They do not override explicit repository rules unless the user says so.

Treat files in `ai/skills/` as reusable workflow instructions. Apply them when the task matches their purpose.

When using a shared rule or skill, briefly mention it in working notes so the applied guidance is visible.
