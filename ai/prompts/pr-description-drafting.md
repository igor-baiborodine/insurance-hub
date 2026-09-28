# Pull Request Description Drafting Prompt

The writable root repository for this session is `<repository-name>`.

Draft a pull request description for ticket `<ticket-id>`. Read and follow the repository's
`AGENTS.md`, `ai/manifest.md`, and relevant rules before drafting. Use these current Insurance Hub
artifacts:

- Ticket specification: `ai/artifacts/<ticket-id>/<ticket-id>-ticket-description.md`
- Delivery tracker: `ai/artifacts/<ticket-id>/<ticket-id>-delivery-steps.md`
- Git diff snapshot: `ai/artifacts/<ticket-id>/<ticket-id>-git-diff.txt`
- Shared template: `ai/templates/pr-description-template.md`
- Supporting evidence, when relevant: `ai/artifacts/<ticket-id>/<ticket-id>-step-*.md` and
  explicitly supplied investigation/review artifacts

Save the finished draft to:

`ai/artifacts/<ticket-id>/<ticket-id>-pr-description.md`

## Workflow

1. Read the complete ticket specification and shared PR template before drafting. Read the delivery
   tracker and completed step summaries to understand the delivered scope and recorded validation.
   If the ticket description is missing or empty, follow `AGENTS.md`: use supplied ticket content,
   or ask for the missing content. Do not infer requirements from the ticket ID.
2. Review the Git diff snapshot and the current branch's actual changes. The post-step snapshot is
   `git diff main <current-branch>`; it compares committed trees and omits staged/unstaged edits
   and untracked files. Inspect current Git status and review relevant staged, unstaged, and new
   files so the draft represents the actual proposed PR. Do not treat the snapshot alone as complete.
3. For API or contract changes, inspect the actual API definitions and implementation: relevant
   protobuf/OpenAPI files, controllers or handlers, gateway mappings, callers, and tests. State
   concrete HTTP methods and paths only when the repository sources establish them. For gRPC-only
   changes, name the service and RPC; do not invent an HTTP route. If no public endpoint exists or
   an endpoint cannot be established, say so briefly.
4. Compare the ticket requirements with the changed files. Describe only behavior and implementation
   supported by the ticket and diff. State the cause when investigation evidence establishes it;
   otherwise identify it as unconfirmed instead of inventing a root cause. Explain meaningful
   in-scope changes and relevant out-of-scope boundaries. Include a short provider/module/flow
   compatibility table only when it makes a cross-cutting change easier to review.
5. Draft the output using the shared template's three top-level sections and purpose: `Summary`,
   `Key Changes`, and `Testing Notes`. Put the business/operational issue, evidenced cause, and
   resulting behavior in the concise summary; group implementation details by meaningful change
   area. Mention rationale and out-of-scope behavior where useful without adding unsupported claims.
6. In `Testing Notes`, distinguish checks that artifacts prove were run from checks that are
   proposed, not run, blocked, or not applicable. Name automated tests added/updated from the diff.
   List appropriate validation commands by inspecting the owning Makefile and repository workflow.
   Label a suggested command `To run` when there is no evidence it was executed. Give a result only
   when artifacts or this session show its execution and outcome. For Go changes, use only the
   affected module's documented Makefile targets; follow `ai/rules/go-development.md` and
   `ai/rules/go-validation.md`. Do not prescribe direct Go tool invocations. Preserve
   `<PASS/FAIL — not run>` or equivalent explicit result placeholders for checks a reviewer must
   execute; never convert a planned command into a passing result.
7. Include manual validation where relevant:
   - **UI smoke:** specify a scenario and expected outcome when a documented UI flow, runbook, or
     environment supports it. Otherwise state `Not applicable` and why.
   - **Postman/local development:** when an API and local setup support it, list concrete method
     plus endpoint (and named collection/request if discoverable), setup, expected status/result,
     and `<PASS/FAIL — not run>` placeholder. If these details cannot be established, list the
     missing values as placeholders or mark the check not applicable with a reason. Do not suggest
     production credentials or execute requests.
8. Preserve generic placeholders for environment-specific values when sources require them. Do
   not include real secrets or present fabricated identifiers as environment values.
9. Save the draft at the specified local-only path. Do not stage, commit, publish, merge, or create
   the PR. Do not send messages or execute application smoke tests as part of drafting.

## Quality checks

- Keep the description reviewer-friendly and specific. Prefer outcomes and key decisions over a
  file-by-file transcript or copied diff.
- Ensure every behavior, cause, endpoint, test, command, and result can be traced to the ticket,
  repository sources, diff, or recorded execution evidence. Clearly label an inference or unknown.
- Do not imply that the draft's output was executed or that an unrun test passed. Do not hide a
  failed or blocked check.
- Keep local ticket artifacts under `ai/artifacts/<ticket-id>/`; they are excluded from Git and
  must not be committed. Do not overwrite a user-authored draft without checking its contents and
  preserving relevant edits.
- Before finishing, compare the saved description with the template and ticket acceptance criteria,
  and confirm the API and testing notes match the inspected evidence.
