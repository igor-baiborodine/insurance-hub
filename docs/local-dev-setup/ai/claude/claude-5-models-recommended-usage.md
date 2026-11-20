<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Recommended configuration](#recommended-configuration)
- [Selecting a model and effort in Claude Code](#selecting-a-model-and-effort-in-claude-code)
- [Research and planning](#research-and-planning)
  - [A planning prompt pattern](#a-planning-prompt-pattern)
- [Implementation](#implementation)
- [Execution prompt](#execution-prompt)
- [Python auxiliary tooling](#python-auxiliary-tooling)
- [A practical model ladder](#a-practical-model-ladder)
- [Verification pass](#verification-pass)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

For specification-first Go development in Claude Code, use **Claude Opus 5.5 as your default model**, switch to **Claude Fable 5.2 only for the most consequential research and architecture work**, and reserve **Claude Sonnet 5.5 for fast, bounded implementation tasks**. Start at the lowest effort level that produces a reliable result; higher effort generally increases response time and token usage. Anthropic positions Fable 5.2 for "demanding reasoning and long-horizon agentic work", Opus 5.5 for "long-running agentic coding and knowledge work", and Sonnet 5.5 as "the best combination of speed and intelligence". [platform.claude.com](https://platform.claude.com/docs/en/about-claude/models/overview)

Model names, prices, and defaults in this guide reflect the lineup as of November 2026. Check the
linked documentation when the lineup changes.

## Recommended configuration

| Development mode                   | Recommended model         |     Effort | Use it for                                                                                                                               |
|------------------------------------|---------------------------|-----------:|------------------------------------------------------------------------------------------------------------------------------------------|
| Research and problem framing       | **Fable 5.2** (`fable`)   |   **High** | Exploring unfamiliar libraries, comparing architectural options, threat/risk analysis, domain modeling, identifying edge cases           |
| Specification and technical plan   | **Fable 5.2** (`fable`)   |   **High** | Producing an implementation-ready spec, package boundaries, API contracts, acceptance criteria, migration steps, test strategy           |
| Normal implementation              | **Opus 5.5** (`opus`)     | **Medium** | Adding a handler/service/repository, implementing a bounded user story, writing unit tests, routine refactors                            |
| Complex execution                  | **Opus 5.5** (`opus`)     |   **High** | Multi-package Go changes, cross-cutting observability, concurrency changes, DB/schema migrations, fixing a failure after normal attempts |
| Mechanical or repeatable execution | **Sonnet 5.5** (`sonnet`) | **Medium** | Test table expansion, error wrapping consistency, straightforward DTO/mapper changes, docs, simple cleanup                               |
| PR code review and verification    | **Opus 5.5** (`opus`)     |   **High** | Reviewing the diff against requirements, contracts, tests, repository rules, and validation evidence                                    |
| PR description drafting            | **Opus 5.5** (`opus`)     | **Medium** | Synthesizing the ticket, diff, implementation decisions, and verified test results into the repository PR template                       |

For routine PR code review, start a fresh **Opus 5.5 + High** session (or run `/clear`) so the reviewer does not inherit the implementation session's assumptions. Escalate to **xhigh**, or to **Fable 5.2 + High**, for a large or ambiguous change, or when security, concurrency, persistence, migrations, or cross-service contracts materially raise the risk. For PR description drafting, use **Opus 5.5 + Medium** because the task requires evidence gathering and technical synthesis but usually not deep defect analysis. **Sonnet 5.5 + Medium** is sufficient for a small mechanical PR when the ticket, diff, and validation evidence are already clear.

**Haiku 5.5** (`haiku`) targets high-volume, latency-sensitive work such as classification and
routing. Do not use it for repository ticket work; it is acceptable for quick read-only lookups.

## Selecting a model and effort in Claude Code

Treat the model and its effort level as separate choices. [code.claude.com](https://code.claude.com/docs/en/model-config)

- `/model` opens the picker. `Enter` switches and saves the choice as the default for new
  sessions; `s` switches for the current session only. Use the left and right arrow keys in the
  picker to adjust effort.
- `/model <alias>` switches directly, for example `/model fable` or `/model opus`.
- `/effort <level>` sets effort directly; `/effort auto` returns to the model's default.
- `claude --model <alias> --effort <level>` applies both to a single session at launch.
- Including the word `ultrathink` in a prompt raises effort for that turn only, without changing
  the session setting.

Available effort levels are `low`, `medium`, `high`, `xhigh`, and `max`. Claude Code defaults to
**medium** for Fable 5.2, Opus 5.5, Sonnet 5.5, and Haiku 5.5, so every **High** recommendation in
this guide must be set explicitly. Organization or plan limits can cap the available models and
effort; use what the picker offers rather than designing the workflow around an unavailable tier.

## Research and planning

For the initial exploration and planning stage, choose:

```text
Model:  fable
Effort: high
```

This is the place to spend more of your usage allowance. Research and specification defects are expensive because they propagate into every implementation step. Fable 5.2 is Anthropic's most capable widely available model and costs about twice as much per token as Opus 5.5, while higher effort makes any model perform more complete analysis. [platform.claude.com](https://platform.claude.com/docs/en/build-with-claude/effort)

Avoid `xhigh` as your default. Use it only when the decision is genuinely high impact or difficult to reverse, for example:

- Defining Insurance Hub's aggregate boundaries and consistency rules across policies, quotes, claims, and payments.
- Choosing an event/outbox/idempotency design for a durable asynchronous workflow.
- Designing authorization, tenant isolation, secret handling, or PII retention.
- Planning a major Go package reorganization, persistence migration, or API versioning change.
- Reconciling contradictory requirements or diagnosing a hard concurrency/data-integrity defect.

Use **Fable 5.2 at xhigh or max** only for these "write the design once, then execute it many times" decisions. It is overkill for routine feature specifications and consumes more time and usage allowance.

Claude Code's plan mode (cycle permission modes with `Shift+Tab`) is a good fit for this stage: it
keeps the session read-only while the model explores the repository and proposes a plan.

### A planning prompt pattern

For repository ticket work, the ticket content at
`<ticket-dir>/<ticket>-ticket-content.md` is the specification. `<ticket-dir>` is resolved by the
artifact rules in `AGENTS.md`, for example `ai/artifacts/issue-123`,
`ai/artifacts/epic-4.2/issue-131`, or `ai/artifacts/adhoc/<branch-derived-name>`. Follow
`AGENTS.md`, `ai/manifest.md`, and the applicable canonical rules and skills for readiness,
planning, and implementation. Do not create a separate committed specification. The following is a
human-facing conversation starter; it does not replace those repository instructions:

```text
Help me assess whether ticket <ticket> is ready for implementation in this repository.

First read AGENTS.md, ai/manifest.md, the ticket content under its resolved artifact directory,
and the applicable rules and skills. Inspect the relevant packages, tests, and service boundaries.

Identify missing requirements, scope, assumptions, risks, and acceptance criteria. Do not invent
business requirements. Ask me to clarify any blocking gaps. Keep requirements and agreed
clarifications in the ticket-content file; put ordered steps and validation checkpoints in
<ticket-dir>/<ticket>-delivery-steps.md using the repository workflow.

Do not begin implementation until readiness is established. Cite repository paths for conclusions.
```

Review the readiness assessment and clarify requirements in the ticket content. Keep ticket
artifacts local under `<ticket-dir>`; follow the repository's documented artifact and Git rules.

## Implementation

For most approved specifications, choose:

```text
Model:  opus
Effort: medium
```

Opus 5.5 is Anthropic's recommended starting point for most workloads, and medium is its default effort. This should be your workhorse setting for Go implementation: it keeps agentic changes deliberate enough to follow a multi-step spec without needlessly using Fable for every loop of compile–test–fix. [platform.claude.com](https://platform.claude.com/docs/en/about-claude/models/overview)

Escalate to:

```text
Model:  opus
Effort: high
```

when the task spans several layers or tools: e.g., add a policy workflow that touches HTTP transport, application service, domain types, persistence, integration events, tracing, and tests. It is also appropriate when the model fails a test twice or begins changing files outside the approved scope.

Use Sonnet as an efficiency option:

```text
Model:  sonnet
Effort: medium
```

only after the specification and design are clear. Sonnet 5.5 is suited to fast, constrained work, not to deciding the design. It is a good fit for generating table-driven test cases from an existing contract, applying a known error-handling convention, or completing isolated mappings.

## Execution prompt

Keep the implementation agent constrained to the approved plan:

```text
Implement the ready ticket described by <ticket-dir>/<ticket>-ticket-content.md, following its
delivery plan and the repository's AGENTS.md, ai/manifest.md, and applicable rules and skills.

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

The repository's `.claude/skills/` adapters route Claude Code to the canonical ticket workflows, so
you can ask for a stage by skill name, for example the `ticket-implementation-workflow` skill.

Split a larger feature into small sessions: domain/contracts first, then persistence/transport, then tests and verification. This improves reviewability and prevents an implementation session from consuming context and usage allowance on unrelated parts of the repository. Run `/clear` between unrelated steps rather than carrying a long conversation forward.

## Python auxiliary tooling

Python supports administrative automation, deployment helpers, structured-data checks, and test
harnesses where Bash becomes difficult to reason about. Keep short command sequences in Bash;
service implementation continues to follow the Java-to-Go migration. The
[Python entry point](../../../../ai/framework/rules/python/README.md) and its linked
[development](../../../../ai/framework/rules/python/development.md),
[testing](../../../../ai/framework/rules/python/test.md),
[formatting](../../../../ai/framework/rules/python/formatting.md), and
[validation](../../../../ai/framework/rules/python/validation.md) rules define the repository
policy. The existing ticket implementation and code-review skills apply to this work as well.

Use this Python-specific addition to a planning or execution prompt:

```text
For the auxiliary tooling in this task, apply ai/framework/rules/python/README.md and all four
linked rules. Explain the Python-versus-Bash choice using the actual workflow. Identify the script
owner, supported Python version, and owning Make targets before implementation.

Keep administrative tooling separate from application runtime behavior. For state-changing
operations, define explicit targets, authorization boundaries, idempotency, partial-failure
recovery, subprocess timeouts/cleanup, and secret handling. Derive permissions from the selected
role profile; do not assume every profile is a reader.

Run the relevant Make-based checks with disposable resources where mutations are needed. Record
what was tested, what used fake adapters, and what still requires live-environment evidence.
```

For a review prompt, ask for findings on those same boundaries and evidence. A small script can
still carry consequential administrative permissions; assess the operation's impact and recovery
requirements as well as its size. This section is a human-facing prompt aid, not another source
of repository instructions.

## A practical model ladder

Use this escalation path rather than selecting the most powerful setting every time:

1. **Opus 5.5 + medium** — Default implementation, PR-description drafting, and small-to-medium Go work.
2. **Opus 5.5 + high** — PR code review, cross-package change, hard debugging, or a failed first implementation.
3. **Fable 5.2 + high** — Research, specification, architecture, ambiguous requirements, and major design review.
4. **Opus 5.5 + xhigh or Fable 5.2 + xhigh/max** — Rare, demanding reviews and irreversible or security-/data-integrity-critical decisions.
5. **Sonnet 5.5 + medium** — Well-scoped repetitive work after a reviewed plan exists.

Anthropic recommends starting with Opus 5.5 and moving to Fable 5.2 "when your evals on Claude Opus 5.5 at higher effort still fall short". This spec-first workflow uses Fable + High for planning because correctness has the highest leverage there, then returns to Opus + Medium for implementation. [platform.claude.com](https://platform.claude.com/docs/en/about-claude/models/choosing-a-model)

## Verification pass

Do not use the same high-effort planning session to both design and judge the result. After implementation, start a fresh **Opus 5.5 + High** session and ask it to act as a reviewer:

```text
Review the current change against <ticket-dir>/<ticket>-ticket-content.md and the delivery plan.
Follow AGENTS.md, ai/manifest.md, the applicable repository rules, and the shared code-review
workflow/template.

Do not edit files. Identify:
- Missing acceptance criteria
- Incorrect or incomplete Go error handling
- Context cancellation, timeout, retry, and idempotency problems
- Data races, transaction-boundary issues, and backward-compatibility risks
- Missing or weak tests
- Unnecessary scope expansion

Return findings ranked by severity, with exact file and line references.
```

Then decide which findings to accept before launching a narrow remediation task. This separation—Fable for specification, Opus for implementation, and a fresh Opus + High session for independent verification—fits a disciplined Go workflow and should preserve more of your plan's usage allowance than keeping Fable at the highest available effort for every request.
