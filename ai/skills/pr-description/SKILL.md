---
name: pr-description
description: Draft or update Insurance Hub pull request descriptions from ticket requirements, the proposed change, and verified validation evidence. Use for local description artifacts and GitHub pull request bodies; do not use to create, review, merge, or close a pull request.
---

# Insurance Hub Pull Request Description

Produce a reviewer-friendly description that explains the delivered outcome, the meaningful
implementation changes, and the available validation evidence. Follow root `AGENTS.md`,
`ai/manifest.md`, applicable nested instructions and rules, and the precedence and conflict
procedure they define.

Choose the execution mode before gathering evidence:

- For a local checkout that produces
  `ai/artifacts/<ticket>/<ticket>-pr-description.md`, read and follow
  [local drafting](references/local-drafting.md).
- For a pull request hosted on GitHub, including a request to update its body, read and follow
  [GitHub drafting and update](references/github-drafting.md).

## Shared description standard

Use `ai/templates/pr-description-template.md` and retain its three top-level sections:

1. **Summary**: state the business or operational issue, established cause when relevant, and
   resulting behavior in one to three concise sentences.
2. **Key Changes**: group concrete implementation details by meaningful change area. Include
   rationale and material out-of-scope boundaries when they help a reviewer assess the change.
3. **Testing Notes**: report every relevant executed, failed, blocked, unexecuted, or inapplicable
   check using the validation-evidence meanings in
   `ai/skills/ticket-post-step-workflow/SKILL.md`.

Describe only behavior, causes, endpoints, tests, commands, and results supported by the available
ticket, repository, diff, or execution evidence. Label an inference or unknown. Plans and proposed
commands are not results, and a CI result does not establish a local result or the reverse.

For API or contract changes, inspect authoritative schemas, controllers or handlers, gateway
mappings, callers, and tests. State an HTTP method and path only when repository sources establish
them. For a gRPC-only change, name the service and RPC without inventing an HTTP route.

Include a compact compatibility table only when it makes a cross-cutting provider, module, or flow
change easier to assess. Prefer outcomes and significant decisions over a file-by-file transcript
or copied diff. Preserve placeholders for environment-specific values and never include secrets.

Before finishing, compare the description with the shared template, requirements, actual proposed
change, and available validation evidence. Do not create, review, merge, close, or otherwise modify
a pull request except for an explicitly requested GitHub description update.
