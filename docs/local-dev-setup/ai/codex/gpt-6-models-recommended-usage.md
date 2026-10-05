<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->
**Table of Contents** 

- [Recommended configuration](#recommended-configuration)
- [Research and planning](#research-and-planning)
  - [A planning prompt pattern](#a-planning-prompt-pattern)
- [Implementation](#implementation)
- [Execution prompt](#execution-prompt)
- [A practical model ladder](#a-practical-model-ladder)
- [Verification pass](#verification-pass)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

For specification-first Go development, use **GPT-6.1 Sol as your default model**, switch to **Astra only for the most consequential research and architecture work**, and reserve **Luna for fast, bounded implementation tasks**. Start at the lowest reasoning level that produces a reliable result; higher reasoning generally increases response time and token usage. OpenAI positions Astra for highest capability, GPT-6.1 Sol for complex work with a better speed/cost balance, and Luna for efficient repeatable work. [developers.openai](https://developers.openai.com/api/docs/guides/latest-model)

## Recommended configuration

| Development mode                   | Recommended model |  Reasoning | Use it for                                                                                                                               |
|------------------------------------|-------------------|-----------:|------------------------------------------------------------------------------------------------------------------------------------------|
| Research and problem framing       | **6 Astra**       |   **High** | Exploring unfamiliar libraries, comparing architectural options, threat/risk analysis, domain modeling, identifying edge cases           |
| Specification and technical plan   | **6 Astra**       |   **High** | Producing an implementation-ready spec, package boundaries, API contracts, acceptance criteria, migration steps, test strategy           |
| Normal implementation              | **6.1 Sol**       | **Medium** | Adding a handler/service/repository, implementing a bounded user story, writing unit tests, routine refactors                            |
| Complex execution                  | **6.1 Sol**       |   **High** | Multi-package Go changes, cross-cutting observability, concurrency changes, DB/schema migrations, fixing a failure after normal attempts |
| Mechanical or repeatable execution | **6 Luna**        | **Medium** | Test table expansion, error wrapping consistency, straightforward DTO/mapper changes, docs, simple cleanup                               |
| PR code review and verification    | **6.1 Sol**       |   **High** | Reviewing the diff against requirements, contracts, tests, repository rules, and validation evidence                                    |
| PR description drafting            | **6.1 Sol**       | **Medium** | Synthesizing the ticket, diff, implementation decisions, and verified test results into the repository PR template                       |

For routine PR code review, open a fresh **GPT-6.1 Sol + High** turn so the reviewer does not inherit the implementation agent's assumptions. Escalate to **xhigh** for a large or ambiguous change, or when security, concurrency, persistence, migrations, or cross-service contracts materially raise the risk. For PR description drafting, use **GPT-6.1 Sol + Medium** because the task requires evidence gathering and technical synthesis but usually not deep defect analysis. **Luna + Medium** is sufficient for a small mechanical PR when the ticket, diff, and validation evidence are already clear.

IntelliJ's AI Assistant picker may shorten the model names to `6 Astra`, `6.1 Sol`, and `6 Luna`. Treat the selected model and its reasoning or intelligence level as separate choices. Available levels can include Light, Medium, High, Extra High, Max, and Ultra, depending on the selected model, account entitlement, and product rollout. Use the levels available in the picker rather than designing the workflow around an unavailable tier. [learn.chatgpt](https://learn.chatgpt.com/docs/models)

## Research and planning

For the initial exploration and planning stage, choose:

```text
Model:     6 Astra
Reasoning: high
```

This is the place to spend more quota. Research and specification defects are expensive because they propagate into every implementation step. Astra is OpenAI’s highest-capability model, while higher reasoning settings cause the model to perform more complete analysis. [developers.openai](https://developers.openai.com/api/docs/guides/latest-model)

Avoid `xhigh` as your default. Use it only when the decision is genuinely high impact or difficult to reverse, for example:

- Defining Insurance Hub’s aggregate boundaries and consistency rules across policies, quotes, claims, and payments.
- Choosing an event/outbox/idempotency design for a durable asynchronous workflow.
- Designing authorization, tenant isolation, secret handling, or PII retention.
- Planning a major Go package reorganization, persistence migration, or API versioning change.
- Reconciling contradictory requirements or diagnosing a hard concurrency/data-integrity defect.

Use **Astra at xhigh or max**, or **Ultra** when the picker offers it, only for these “write the design once, then execute it many times” decisions. It is overkill for routine feature specifications and consumes more time and usage allowance.

### A planning prompt pattern

For repository ticket work, the ticket content at
`ai/artifacts/<ticket>/<ticket>-ticket-content.md` is the specification. Follow `AGENTS.md`,
`ai/manifest.md`, and the applicable canonical rules and skills for readiness, planning, and
implementation. Do not create a separate committed specification. The following is a human-facing
conversation starter; it does not replace those repository instructions:

```text
Help me assess whether ticket <ticket-id> is ready for implementation in this repository.

First read AGENTS.md, ai/manifest.md, the ticket content at
ai/artifacts/<ticket-id>/<ticket-id>-ticket-content.md, and applicable rules and skills.
Inspect the relevant packages, tests, and service boundaries.

Identify missing requirements, scope, assumptions, risks, and acceptance criteria. Do not invent
business requirements. Ask me to clarify any blocking gaps. Keep requirements and agreed
clarifications in the ticket-content file; put ordered steps and validation checkpoints in
ai/artifacts/<ticket-id>/<ticket-id>-delivery-steps.md using the repository workflow.

Do not begin implementation until readiness is established. Cite repository paths for conclusions.
```

Review the readiness assessment and clarify requirements in the ticket content. Keep ticket
artifacts local under `ai/artifacts/<ticket-id>/`; follow the repository's documented artifact and
Git rules.

## Implementation

For most approved specifications, choose:

```text
Model:     6.1 Sol
Reasoning: medium
```

GPT-6.1 Sol is OpenAI’s balanced choice for complex work, while medium is the intended everyday reasoning level. This should be your workhorse setting for Go implementation: it keeps agentic changes deliberate enough to follow a multi-step spec without needlessly using Astra for every loop of compile–test–fix. [developers.openai](https://developers.openai.com/api/docs/guides/latest-model)

Escalate to:

```text
Model:     6.1 Sol
Reasoning: high
```

when the task spans several layers or tools: e.g., add a policy workflow that touches HTTP transport, application service, domain types, persistence, integration events, tracing, and tests. It is also appropriate when the model fails a test twice or begins changing files outside the approved scope.

Use Luna as an efficiency option:

```text
Model:     6 Luna
Reasoning: medium
```

only after the specification and design are clear. Luna is suited to repeatable, constrained work, not to deciding the design. It is a good fit for generating table-driven test cases from an existing contract, applying a known error-handling convention, or completing isolated mappings.

## Execution prompt

Keep the implementation agent constrained to the approved plan:

```text
Implement the ready ticket described by
ai/artifacts/<ticket-id>/<ticket-id>-ticket-content.md, following its delivery plan and the
repository's AGENTS.md, ai/manifest.md, and applicable rules and skills.

Rules:
- First summarize the concrete files and changes you intend to make.
- Follow the ticket acceptance criteria; do not introduce unrelated refactors.
- Preserve Go package boundaries and existing conventions.
- Add or update table-driven tests for every acceptance criterion.
- Discover the affected Go module(s), read their Makefiles and CI configuration, then run the
  narrowest relevant repository-approved Make targets. Do not assume a root target covers every
  module or invoke Go tools directly. Record commands, working directories, scope, outcomes, and
  checks that were blocked or not run in the ticket artifacts.
- Report: modified files, tests run and results, deviations from the spec,
  and any unresolved risks.
- If a requirement is ambiguous, stop and ask before choosing a design.
```

For IntelliJ AI Assistant, split a larger feature into small agent runs: domain/contracts first, then persistence/transport, then tests and verification. This improves reviewability and prevents an implementation run from consuming context and quota on unrelated parts of the repository.

## A practical model ladder

Use this escalation path rather than selecting the most powerful setting every time:

1. **GPT-6.1 Sol + medium** — Default implementation, PR-description drafting, and small-to-medium Go work.
2. **GPT-6.1 Sol + high** — PR code review, cross-package change, hard debugging, or a failed first implementation.
3. **Astra + high** — Research, specification, architecture, ambiguous requirements, and major design review.
4. **GPT-6.1 Sol + xhigh or Astra + xhigh/max** — Rare, demanding reviews and irreversible or security-/data-integrity-critical decisions.
5. **Luna + medium** — Well-scoped repetitive work after a reviewed plan exists.

OpenAI’s documentation recommends choosing the lightest model and reasoning level that meets the task's quality bar, then increasing effort when the work requires more planning, analysis, or checking. This spec-first workflow uses Astra + High for planning because correctness has the highest leverage there, then returns to GPT-6.1 Sol + Medium for implementation. [learn.chatgpt](https://learn.chatgpt.com/docs/model-selection)

## Verification pass

Do not use the same high-effort planning prompt to both design and judge the result. After implementation, open a fresh **GPT-6.1 Sol + High** agent turn and ask it to act as a reviewer:

```text
Review the current change against
ai/artifacts/<ticket-id>/<ticket-id>-ticket-content.md and the delivery plan. Follow AGENTS.md,
ai/manifest.md, the applicable repository rules, and the shared PR review workflow/template.

Do not edit files. Identify:
- Missing acceptance criteria
- Incorrect or incomplete Go error handling
- Context cancellation, timeout, retry, and idempotency problems
- Data races, transaction-boundary issues, and backward-compatibility risks
- Missing or weak tests
- Unnecessary scope expansion

Return findings ranked by severity, with exact file and line references.
```

Then decide which findings to accept before launching a narrow remediation task. This separation—Astra for specification, GPT-6.1 Sol for implementation, and GPT-6.1 Sol + High for independent verification—fits a disciplined Go workflow and should preserve more Plus quota than keeping Astra at the highest available level for every request.
