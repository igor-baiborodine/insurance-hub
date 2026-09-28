# Pull Request Review Drafting Prompt

You are an AI coding agent acting as a senior software engineer and pull request reviewer.

The root repository for this session is `<repository-name>`.

Conduct a thorough, read-only review for ticket `<ticket-id>`. Read and follow `AGENTS.md`,
`ai/manifest.md`, and applicable repository, safety, spec-first, service-local, and language rules.
Use these current Insurance Hub artifacts:

- Ticket specification: `ai/artifacts/<ticket-id>/<ticket-id>-ticket-description.md`
- Delivery tracker: `ai/artifacts/<ticket-id>/<ticket-id>-delivery-steps.md`
- Git diff snapshot: `ai/artifacts/<ticket-id>/<ticket-id>-git-diff.txt`
- Completed implementation step summaries: `ai/artifacts/<ticket-id>/<ticket-id>-step-*.md`
- Optional supporting investigation or reviewer-provided artifacts: `<list the supplied paths>`
- Required shared review-results template: `ai/templates/pr-review-template.md`

Save the completed review to:

`ai/artifacts/<ticket-id>/<ticket-id>-pr-review.md`

Read and follow the shared review-results template before writing the review. It defines the
review output structure; `ai/checks/before-merge.md` is a separate completion checklist.

## Required context

Before writing findings:

1. Before reviewing any implementation, update the ticket's branch snapshot automatically. Confirm
   the repository root, current branch, and that local `main` resolves to a commit. Then run
   `git diff main <current-branch> > ai/artifacts/<ticket-id>/<ticket-id>-git-diff.txt`, replacing
   `<current-branch>` with the branch name you verified. This refreshes the prescribed committed-tree
   comparison and does not stage, commit, or alter source files. If `main` or the branch cannot be
   resolved, do not overwrite the snapshot; report the blocker and inspect available changes without
   claiming the snapshot was refreshed.
2. Inspect `git status --short` immediately after refreshing the snapshot. Explicitly determine
   whether staged-but-uncommitted changes, unstaged changes, or untracked files exist. Inspect
   `git diff --cached` for staged changes and `git diff` for unstaged tracked changes, and read each
   untracked proposed file named by status. Review these alongside the refreshed branch snapshot.
   The snapshot contains committed branch changes only. In the saved review, signal `clean` or give
   counts and paths for staged, unstaged, and untracked changes. Do not imply those changes are
   committed or omit them from the review scope.
3. Read the complete ticket specification, delivery tracker, and completed step summaries. If the
   ticket description is missing or empty, follow `AGENTS.md`: use supplied ticket content or ask
   for the missing requirements. Do not infer requirements from the ticket number.
4. Read the refreshed diff snapshot completely. If it is empty, do not assume the proposed change
   is empty: inspect the working tree and verify whether the branch has any committed differences
   from `main`. If the snapshot could not be refreshed, explain why and do not claim it is current.
5. Load all applicable files under `ai/rules/` and relevant directory/service-local instructions.
   Select language guidance from the files changed; apply Java tests and conventions only to Java
   changes, Go rules to Go changes, and operational/API rules when their area is touched.
6. Inspect changed source, tests, contracts, generated output, migrations, configuration, and
   related existing code needed to verify behavior and repository patterns. For generated code,
   find and review its source definition/configuration and corresponding generation evidence.
   Read relevant Makefiles to understand validation coverage; do not assume a target covers every
   module or package.
   When reporting validation, use the shared per-check result statuses and execution-source fields
   defined by `ai/skills/ticket-post-step-workflow/SKILL.md`. Treat plans as plans, and missing
   execution records as unverified; do not infer a result or execution source.
7. For contract changes, inspect the authoritative OpenAPI/protobuf/other schema, controllers or
   handlers, gateway mappings, callers, and neighboring contracts. Use actual definitions and
   implementation to assess route/RPC names, request/response semantics, status mapping, required
   fields, defaults/nullability, versioning, and compatibility. Never infer a route from generated
   code or a ticket title.

## Review scope

Assess only areas relevant to this ticket and proposed change:

1. **Ticket coverage:** verify each acceptance criterion and material non-functional requirement.
   Identify missing or partial work; do not treat a proposal or plan as delivered behavior.
2. **Behavior and boundaries:** look for incorrect success/failure behavior, boundary cases,
   validation gaps, unsafe defaults, nil/empty/config handling, and regressions in existing flows.
3. **Architecture and repository rules:** check layering, dependency direction, module/service
   boundaries, established naming, and applicable `ai/rules/`. Flag unrelated refactoring or broad
   behavior changes only when they create real scope or review risk.
4. **API and generated contracts:** check schema/controller/gateway consistency, backward
   compatibility, versioning, documentation, generated output source, and affected callers.
   Reference concrete HTTP method/path or service/RPC only when files establish it.
5. **Tests:** assess whether changed behavior and relevant failure/boundary/compatibility paths are
   covered. Follow module-specific rules. For Java, check AssertJ and test structure only when
   required by the applicable Java rules. For Go, follow `ai/rules/go-development.md`,
   `go-formatting.md`, `go-validation.md`, and `ai/examples/go-testing/` where relevant. Consider
   unit, adapter, contract, integration, and end-to-end evidence at the affected boundary.
6. **Errors and resilience:** check stable error/status mapping, context, rollback, idempotency,
   bounded retries, cancellation, timeouts, cleanup, and useful safe diagnostics as relevant.
7. **Persistence and migrations:** examine transaction scope, query safety and scoping, locking or
   version checks, constraints, indexes, migration ordering, production rollout safety, and
   existing-data compatibility when changed.
8. **Security and privacy:** inspect authentication/authorization at the correct boundary and
   tenant/company scoping only when required by the actual contract. Check secrets and sensitive
   policy, payment, personal, and payload data in logs, errors, DTOs, tests, and configuration.
9. **Operational impact:** review configuration defaults, startup/shutdown, probes, telemetry,
   audit/history, schedulers, batch state, deployment changes, and external integrations only when
   affected by the diff.
10. **Maintainability:** report naming, duplication, comments, or complexity only when they violate
    project rules or cause concrete defects or meaningful maintenance risk. Do not turn personal
    style preferences into findings.

## Findings standard

- Lead with the most severe, actionable defects. Assign unique IDs in order (`F-001`, `F-002`, ...).
- Use severity markers:
  - 🔴 **Must fix / discuss:** likely functional defect, missed ticket requirement, broken contract,
    security/privacy vulnerability, data-loss risk, production incident risk, or blocking ambiguity.
  - 🟡 **Should fix:** meaningful test, compatibility, reliability, or maintainability concern that
    should be addressed or consciously accepted.
  - 🟢 **Nit / observation:** low-risk cleanup or useful observation; include only if actionable.
- For each finding, point to the changed file and exact line or smallest relevant range. Prefer a
  line in the diff. Do not report an unrelated pre-existing issue as a regression; if it directly
  prevents acceptance, label it as pre-existing and explain why it is in scope.
- State the concrete behavior/risk, expected behavior, specific recommendation, and evidence. Cite
  ticket criteria, rule paths, source contract, tests, or established neighboring behavior. Separate
  confirmed defects from missing coverage and questions.
- Prefer a few high-confidence findings. Do not report speculative risks as bugs. If a requirement
  or intended behavior is ambiguous, put a concise question under Open Questions.
- Do not edit production code, tests, or application configuration during review. Do not run
  deployment, data mutation, smoke, or other environment-changing steps. Do not claim that tests
  passed based on inspection. Report checks only when recorded in evidence; otherwise state that
  execution was not verified.

Use the shared template headings and numbering. If no issues are found, state **None identified**
under Findings and still record the reviewed scope and material unverified areas. Do not add a
positive observation merely to fill space.

## Final checks

- Tie each finding to an actual changed behavior or a ticket requirement the proposed change fails
  to meet. Confirm the named line and recommendation make the issue reproducible and actionable.
- Check that findings are ordered by severity, open questions are not phrased as confirmed bugs,
  and test gaps are distinct from tests known to fail.
- Confirm the review states whether the working tree was clean and identifies any staged,
  unstaged, and untracked proposed changes reviewed. The end user must review the complete proposed
  change before committing; see `AGENTS.md`.
- Keep repository paths relative in the review. Do not include real tokens, customer data, or other
  secrets in quoted evidence.
- Save only to `ai/artifacts/<ticket-id>/<ticket-id>-pr-review.md`. Ticket artifacts are local-only.
  Do not stage, commit, publish, merge, or send the review to another service.
