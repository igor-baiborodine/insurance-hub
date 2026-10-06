---
name: code-review
description: Review Insurance Hub pull requests or proposed changes against ticket requirements, repository rules, contracts, tests, and recorded evidence. Use for local review artifacts and GitHub Copilot code review; do not use to implement fixes.
---

# Insurance Hub Code Review

Conduct a read-only, evidence-based review. Follow root `AGENTS.md`, `ai/manifest.md`, applicable
nested instructions and rules, and the precedence and conflict procedure they define.

Choose the execution mode before reviewing:

- For a review from a local checkout that produces `<ticket-dir>/<ticket>-pr-review.md`,
  resolve `<ticket-dir>` through root `AGENTS.md` and follow [local review](references/local-review.md).
- For GitHub Copilot code review on a pull request, read and follow
  [GitHub review](references/github-review.md).

## Shared review standard

Assess only areas relevant to the ticket and proposed change:

1. Verify every acceptance criterion and material non-functional requirement against the change.
2. Check behavior, boundaries, validation, errors, cancellation, cleanup, and unsafe defaults where
   applicable.
3. Build a repository-rule applicability checklist from root and nested `AGENTS.md` files,
   `ai/manifest.md`, applicable `ai/rules/`, relevant workflow skills, and
   `ai/checks/before-merge.md`. Apply documented user or ticket exceptions according to precedence.
4. Inspect authoritative contracts, implementations, callers, and generated sources together.
   Never infer API behavior solely from generated output, a ticket title, or a PR description.
5. Assess tests at the changed boundary and inspect the owning Makefiles or other validation entry
   points before accepting coverage claims.
6. Check security, privacy, persistence, migration, deployment, telemetry, and operational impact
   when the changed paths or requirements make them applicable.
7. Report maintainability concerns only when they violate a repository rule or create a concrete
   defect or meaningful maintenance risk.

Verify implementation and execution evidence directly. Plans are not results. A missing or
contradictory execution record is unverified, not a pass. Do not report an unrelated pre-existing
issue as a regression unless it directly blocks acceptance, and label it as pre-existing when it
must be reported.

## Findings

Lead with the most severe, actionable findings. For each finding:

- identify the changed file and exact line or smallest relevant range;
- state the concrete behavior or risk, expected behavior, specific recommendation, and evidence;
- cite the ticket criterion, repository rule, source contract, test, or established neighboring
  behavior that makes it a finding;
- separate confirmed defects, missing coverage, unverified claims, and open questions;
- avoid speculative risks, generic best practices, superseded lower-precedence rules, and personal
  style preferences.

Use these severity meanings, adapting their labels to the review host:

- **Must fix / high:** likely functional defect, missed requirement, broken contract,
  security/privacy issue, data-loss risk, production incident risk, or blocking ambiguity.
- **Should fix / medium:** meaningful testing, compatibility, reliability, or maintainability gap.
- **Nit / low:** actionable low-risk cleanup; omit it when it adds little review value.

If no issues are found, say so directly and still identify material unverified areas. Never invent a
positive observation or low-value comment to ensure the review contains findings.

Do not edit production code, tests, contracts, generated files, or application configuration during
the review. Do not stage, commit, publish, merge, deploy, or mutate application data. Never include
tokens, credentials, customer data, or other secrets in quoted evidence.
