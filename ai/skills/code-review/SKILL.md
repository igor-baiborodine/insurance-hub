---
name: code-review
description: Review Insurance Hub pull requests or proposed changes against ticket requirements, repository rules, contracts, tests, and recorded evidence. Use for local review artifacts and GitHub Copilot code review; do not use to implement fixes.
---

<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Insurance Hub Code Review](#insurance-hub-code-review)
  - [Shared review standard](#shared-review-standard)
  - [Findings](#findings)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Insurance Hub Code Review

Conduct a read-only, evidence-based review. Follow root `AGENTS.md`, `ai/manifest.md`, applicable
nested instructions and rules, and the precedence and conflict procedure they define.

Choose the execution mode before reviewing:

- For a review from a local checkout that produces a ticket PR review artifact, resolve
  `<ticket-dir>` through root `AGENTS.md` and follow [local review](references/local-review.md).
  Select a local proposed-change review or exact published-PR verification before collecting evidence;
  add the strict upstream match to the local review only when specifically requested.
- For GitHub Copilot code review on a pull request, read and follow
  [GitHub review](references/github-review.md).

## Shared review standard

Assess only areas relevant to the ticket and proposed change:

1. Verify every acceptance criterion and material non-functional requirement against the change.
   For behavior changes, trace representative success, failure, and compatibility/invariant
   examples from stable AC IDs to the affected boundary, check/scenario ID, independently expected
   result, observed result, and final-state evidence. Inspect bug reproductions and independent
   migration baselines where applicable. A passing assertion copied from the implementation or
   generated replacement does not establish the expected behavior. Check whether a relevant
   behavior mutation fails the corresponding check; if not demonstrated, assess the gap without
   claiming mutation sensitivity. Manual scenarios need a reason automation was unsuitable and
   an actual observation.
2. Check behavior, boundaries, validation, errors, cancellation, cleanup, and unsafe defaults where
   applicable.
3. Build a repository-rule applicability checklist from root and nested `AGENTS.md` files,
   `ai/manifest.md`, applicable `ai/rules/`, relevant workflow skills, and
   `ai/checks/before-merge.md`. Apply documented user or ticket exceptions according to precedence.
4. Inspect authoritative contracts, implementations, callers, and generated sources together.
   Never infer API behavior solely from generated output, a ticket title, or a PR description.
   For substantial contract, persistence, or architecture choices, inspect the recorded alternatives,
   tradeoffs, failure modes, migration impact, and rationale against the specification before
   accepting the selected design.
5. Assess tests at the changed boundary and inspect the owning Makefiles or other validation entry
   points before accepting coverage claims. For non-core functionality such as auxiliary Python
   scripts, apply the repository's [80/20 test direction](../../rules/repo-rules.md#testability):
   expect focused likely-path coverage without demanding low-value exhaustive matrices. Still
   require every applicable contract safety check, including target guards, failure handling,
   cleanup, secret protection, permissions, and dry-run boundaries.
6. Check security, privacy, persistence, migration, deployment, telemetry, and operational impact
   when the changed paths or requirements make them applicable.
7. Report maintainability concerns only when they violate a repository rule or create a concrete
   defect or meaningful maintenance risk.

Verify implementation and execution evidence directly. Plans are not results. A missing or
contradictory execution record is unverified, not a pass. Compare each claimed pass's
[tested-state identity and freshness](../ticket-post-step-workflow/SKILL.md#validation-evidence-record)
with the revision, specification, and dirty inputs under review. A stale or identity-incomplete
historical pass does not prove the reviewed state. Do not report an unrelated pre-existing
issue as a regression unless it directly blocks acceptance, and label it as pre-existing when it
must be reported.

For auxiliary Python changes, load [the Python entry point](../../rules/python-rules.md) and all
four linked rules. Check the language/ownership boundary, Make recipes and their side effects,
target guards, subprocess and secret handling, cleanup, and recovery for the behavior changed.
Assess permissions against the selected profile's contract, and distinguish fake-adapter or
disposable test results from live evidence. Apply the same review-only boundary; do not execute
mutating administrative targets to reproduce a finding.

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
