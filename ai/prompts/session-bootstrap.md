<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Session Bootstrap Prompt](#session-bootstrap-prompt)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Session Bootstrap Prompt

Before starting work on this repository, read and follow the instructions in `AGENTS.md`.
Then read `ai/manifest.md` for the loading order and framework structure.

For ticket work, first resolve `<ticket-dir>` through
[AGENTS.md Artifact Rules](../../AGENTS.md#artifact-rules), honoring supplied paths and existing
standalone or epic-grouped folders. Before creating or editing any ticket artifact, follow the
`AGENTS.md` artifact preflight: verify the intended path is ignored by local `.git/info/exclude` and that no
`ai/artifacts/` paths are in the Git index. If either check fails, stop artifact writes and report
the required local setup or indexed paths. Load existing artifacts relevant to the requested
stage and reuse the resolved directory across assessment, specification, and delivery.

For defining future scope, use `ticket-requirements-workflow` to write classified requirements in
`<ticket-dir>/<ticket>-requirements.md` and stop after assessment. Read existing requirements and
ticket content when present; assessment does not require ticket content to exist first. When
specification drafting is requested, use `ticket-specification-workflow` to resume from that
assessment and recheck readiness, potentially in a later session. Resolve blocking questions before
drafting; bounded decisions remain prerequisite gates for dependent work. Neither skill automatically
creates an implementation plan. Requirements record assessment and provenance; ticket content
remains the authoritative implementation specification. Existing ready tickets do not require a
retrospective assessment.

For planning or implementing a ticket, use `ticket-implementation-workflow` to validate readiness
and create `<ticket-dir>/<ticket>-delivery-steps.md` before execution. Read
`<ticket-dir>/<ticket>-ticket-content.md` as the specification before assessing readiness or
planning; follow `AGENTS.md` if it is missing or empty. No separate spec document is required.
Stop after the delivery plan when only planning is requested. When implementation is requested,
execute one tracked step at a time and apply `ticket-post-step-workflow` after each completed step.
Load the existing delivery plan and relevant step summaries from the same directory when
continuing work.

Use the Skills catalog in `AGENTS.md` to select any applicable workflow. Read each selected
`SKILL.md` completely, then load only the linked references or scripts required for the current
mode. Do not load every repository skill by default.

Also, load and follow other shared guidance from these folders when relevant to the task:

- `ai/rules/`
- `ai/examples/`
- `ai/templates/`
- `ai/checks/`

Treat files in `ai/rules/` as task-specific or repository working rules that supplement `AGENTS.md`. They do not override explicit repository rules unless the user says so.

Treat files in `ai/skills/` as canonical reusable workflow instructions. Apply them when the task
matches the skill name and description, observe relationships between selected skills, and treat
`.github/skills/` files only as vendor discovery adapters. For GitHub-hosted work, select the
skill's GitHub mode; for checkout-local artifacts, select its local mode when provided.

When using a shared rule or skill, briefly mention it in working notes so the applied guidance is visible.
