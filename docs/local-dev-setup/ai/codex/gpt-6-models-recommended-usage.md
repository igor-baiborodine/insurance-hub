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

For specification-first Go development, use **Sol as your default model**, switch to **Astra only for the most consequential research and architecture work**, and reserve **Luna for fast, bounded implementation tasks**. Start at the lowest reasoning level that produces a reliable result; reasoning increases depth but also consumes more of your Codex allowance. OpenAI positions Astra for highest capability, Sol for demanding reasoning, and Luna for efficient repeatable work. [developers.openai](https://developers.openai.com/api/docs/guides/latest-model)

## Recommended configuration

| Development mode                   | Recommended model |  Reasoning | Use it for                                                                                                                               |
|------------------------------------|-------------------|-----------:|------------------------------------------------------------------------------------------------------------------------------------------|
| Research and problem framing       | **6 Astra**       |   **High** | Exploring unfamiliar libraries, comparing architectural options, threat/risk analysis, domain modeling, identifying edge cases           |
| Specification and technical plan   | **6 Astra**       |   **High** | Producing an implementation-ready spec, package boundaries, API contracts, acceptance criteria, migration steps, test strategy           |
| Normal implementation              | **6 Sol**         | **Medium** | Adding a handler/service/repository, implementing a bounded user story, writing unit tests, routine refactors                            |
| Complex execution                  | **6 Sol**         |   **High** | Multi-package Go changes, cross-cutting observability, concurrency changes, DB/schema migrations, fixing a failure after normal attempts |
| Mechanical or repeatable execution | **6 Luna**        | **Medium** | Test table expansion, error wrapping consistency, straightforward DTO/mapper changes, docs, simple cleanup                               |
| Review and verification            | **6 Sol**         |   **High** | Reviewing the produced diff against your approved specification, finding missing tests, checking error handling and regressions          |

IntelliJ's AI Assistant picker shows `6 Astra`, `6 Sol`, and `6 Luna`, each in standard, max, and ultra variants. Treat those as **a model/capability class**, then choose the reasoning level below the divider for the actual task. The `Plus` plan entitlement and rollout determine exactly which variants remain usable, so use what the picker lets selecting rather than designing the workflow around a particular unavailable tier. [learn.chatgpt](https://learn.chatgpt.com/docs/models)

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

Use **Astra max/ultra**, if your Plus picker grants access, only for these “write the design once, then execute it many times” decisions. It is overkill for routine feature specifications and will burn quota faster.

### A planning prompt pattern

For repository ticket work, the ticket description at
`ai/artifacts/<ticket>/<ticket>-ticket-description.md` is the specification. Follow `AGENTS.md`,
`ai/manifest.md`, and the applicable canonical rules and skills for readiness, planning, and
implementation. Do not create a separate committed specification. The following is a human-facing
conversation starter; it does not replace those repository instructions:

```text
Help me assess whether ticket <ticket-id> is ready for implementation in this repository.

First read AGENTS.md, ai/manifest.md, the ticket description at
ai/artifacts/<ticket-id>/<ticket-id>-ticket-description.md, and applicable rules and skills.
Inspect the relevant packages, tests, and service boundaries.

Identify missing requirements, scope, assumptions, risks, and acceptance criteria. Do not invent
business requirements. Ask me to clarify any blocking gaps. Keep requirements and agreed
clarifications in the ticket-description file; put ordered steps and validation checkpoints in
ai/artifacts/<ticket-id>/<ticket-id>-delivery-steps.md using the repository workflow.

Do not begin implementation until readiness is established. Cite repository paths for conclusions.
```

Review the readiness assessment and clarify requirements in the ticket description. Keep ticket
artifacts local under `ai/artifacts/<ticket-id>/`; follow the repository's documented artifact and
Git rules.

## Implementation

For most approved specifications, choose:

```text
Model:     6 Sol
Reasoning: medium
```

Sol is OpenAI’s recommended strong-reasoning choice for demanding work, while medium is the intended everyday balance. This should be your workhorse setting for Go implementation: it keeps agentic changes deliberate enough to follow a multi-step spec without needlessly using Astra for every loop of compile–test–fix. [developers.openai](https://developers.openai.com/api/docs/guides/latest-model)

Escalate to:

```text
Model:     6 Sol
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
ai/artifacts/<ticket-id>/<ticket-id>-ticket-description.md, following its delivery plan and the
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

1. **Sol + medium** — Default implementation and small-to-medium Go work.
2. **Sol + high** — Cross-package change, hard debugging, or a failed first implementation.
3. **Astra + high** — Research, specification, architecture, ambiguous requirements, and major design review.
4. **Astra + xhigh/max/ultra** — Rare irreversible or security-/data-integrity-critical decisions.
5. **Luna + medium** — Well-scoped repetitive work after a reviewed plan exists.

OpenAI’s documentation explicitly recommends starting with Sol at Medium, Luna at High, or Astra at Light/low and increasing effort only for tasks that require more planning, analysis, or checking. Your spec-first workflow is a sensible exception: use Astra + High for planning because this is where correctness has the highest leverage, then return to Sol + Medium for implementation. [learn.chatgpt](https://learn.chatgpt.com/docs/models)

## Verification pass

Do not use the same high-effort planning prompt to both design and judge the result. After implementation, open a fresh **Sol + High** agent turn and ask it to act as a reviewer:

```text
Review the current change against
ai/artifacts/<ticket-id>/<ticket-id>-ticket-description.md and the delivery plan. Follow AGENTS.md,
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

Then decide which findings to accept before launching a narrow remediation task. This separation—Astra for specification, Sol for implementation, Sol High for independent verification—fits a disciplined Go workflow and should preserve more Plus quota than keeping Astra at Ultra for every request.
