<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->
**Table of Contents**  

- [Spec-First Development: GPT-6 Model and Reasoning Matrix](#spec-first-development-gpt-6-model-and-reasoning-matrix)
  - [TL;DR](#tldr)
    - [Recommended settings](#recommended-settings)
    - [Switching automatically](#switching-automatically)
    - [Cost](#cost)
  - [1. Ground Truth (Verified Facts)](#1-ground-truth-verified-facts)
    - [1.1 Pricing per 1M tokens, Standard tier, short context [V]](#11-pricing-per-1m-tokens-standard-tier-short-context-v)
    - [1.2 Effort support [V]](#12-effort-support-v)
    - [1.3 Benchmarks that drive the matrix [V]](#13-benchmarks-that-drive-the-matrix-v)
    - [1.4 Switching mechanics [V]](#14-switching-mechanics-v)
  - [2. Workflow Model](#2-workflow-model)
  - [3. The Matrix](#3-the-matrix)
    - [3.1 Phase A: Research and Planning](#31-phase-a-research-and-planning)
    - [3.2 Phase B: Code Execution](#32-phase-b-code-execution)
    - [3.3 Escalation Ladder (Phase B)](#33-escalation-ladder-phase-b)
  - [4. Token Cost Trade-offs](#4-token-cost-trade-offs)
    - [4.1 Per-turn formula](#41-per-turn-formula)
    - [4.2 Reference turns [E, computed from verified prices]](#42-reference-turns-e-computed-from-verified-prices)
    - [4.3 Effort-to-output multipliers [E, starting heuristics; replace with your `reasoning_tokens` telemetry]](#43-effort-to-output-multipliers-e-starting-heuristics-replace-with-your-reasoning_tokens-telemetry)
    - [4.4 Example feature budget (one medium feature, about 12 tasks)](#44-example-feature-budget-one-medium-feature-about-12-tasks)
  - [5. Prompt Configuration Templates](#5-prompt-configuration-templates)
    - [5.1 Stable cached prefix (shared by every Phase B call)](#51-stable-cached-prefix-shared-by-every-phase-b-call)
    - [5.2 Phase A: Architect (Astra high)](#52-phase-a-architect-astra-high)
    - [5.3 Phase A: Adversarial Reviewer (Sol xhigh)](#53-phase-a-adversarial-reviewer-sol-xhigh)
    - [5.4 Phase A to B handoff contract: TASKS.yaml (Sol medium output)](#54-phase-a-to-b-handoff-contract-tasksyaml-sol-medium-output)
    - [5.5 Phase B: Implementer (Sol medium, effort switching inside one session)](#55-phase-b-implementer-sol-medium-effort-switching-inside-one-session)
    - [5.6 Phase B: Bulk generator (Luna, CI-gated)](#56-phase-b-bulk-generator-luna-ci-gated)
  - [6. Automated Router](#6-automated-router)
    - [6.1 Policy file: `router.yaml`](#61-policy-file-routeryaml)
    - [6.2 Minimal Go dispatcher (sketch)](#62-minimal-go-dispatcher-sketch)
  - [7. Calibration Checklist](#7-calibration-checklist)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Spec-First Development: GPT-6 Model and Reasoning Matrix

Version 1.0 — September 28, 2026. Covers `gpt-6-astra`, `gpt-6-sol`, `gpt-6-luna` on the OpenAI Responses API.

How to read this document:
- **[V]** means the fact comes from a cited source.
- **[E]** means it is an engineering estimate. Calibrate it against your own telemetry before you rely on it.

OpenAI publishes no official time-to-first-token or tokens-per-second figures for GPT-6. Every latency band below is **[E]** unless it says otherwise.

---

## TL;DR

### Recommended settings

| Stage                                         | Model and effort                             | Why                                                                        |
|-----------------------------------------------|----------------------------------------------|----------------------------------------------------------------------------|
| Collecting repo and doc context               | Luna medium, batch pricing                   | Summarising, not deciding, so the cheapest model is enough.                |
| Writing the spec                              | Astra high                                   | Astra's factual error rate bottoms out at high; max doesn't improve it.    |
| Irreversible decisions (schemas, public APIs) | Astra xhigh, or max with pro mode            | Worth the extra cost only where mistakes are expensive to undo.            |
| Critical review of the spec                   | Sol xhigh                                    | Sol's best agentic setting, and a second model reviews more independently. |
| Implementing features                         | Sol medium                                   | Good quality at about $0.09 per turn.                                      |
| Debugging failed tests                        | Sol high, then Astra high after two failures | Escalate only when the cheaper setting fails.                              |
| Boilerplate and bulk code                     | Luna low to max                              | Only merge after tests and a runtime check pass.                           |

Two findings from the benchmarks shape these choices:
- **More effort isn't always better.** Sol scored lower at max than at xhigh on AutomationBench, and Luna scored lower at xhigh than at high. The router in the document blocks those two settings.
- **Luna is great value but needs an execution check.** On DeepSWE it scored 66.6% at $0.22 per task, against Sol's 68.8% at $2.74. But in the DevOps test, Luna's Kubernetes manifests passed validation every time and still crashed on all three deployments. 
### Switching automatically

- **Within one model:** change effort by sending a `configuration_update` item and leave the request-level effort alone. That keeps the prompt cache, and cached input costs 10% of the normal price. - **Between models:** start a new session seeded with the written spec rather than passing reasoning state across. OpenAI doesn't document whether Astra's reasoning can be reused by Sol or Luna.
- **Keep requests under 272K tokens.** Above that, input costs 2x and output 1.5x. 

### Cost

Output tokens, including reasoning, are about 80% of a planning turn's cost on every model, so effort level is the main cost lever. A typical 12-task feature comes to about $8 with this split, against an estimated $25–30 if everything ran on Astra high. Those figures come from list prices and my assumed turn sizes, not measured runs.

## 1. Ground Truth (Verified Facts)

### 1.1 Pricing per 1M tokens, Standard tier, short context [V]

| Model       |  Input | Cached read | Cache write | Output | Long ctx (>272K) in/out |
|-------------|-------:|------------:|------------:|-------:|------------------------:|
| gpt-6-astra | $10.00 |       $1.00 |      $12.50 | $50.00 |               $20 / $75 |
| gpt-6-sol   |  $2.00 |       $0.20 |       $2.50 | $10.00 |                $4 / $15 |
| gpt-6-luna  |  $0.10 |       $0.01 |      $0.125 |  $0.50 |           $0.20 / $0.75 |

Source: [OpenAI API pricing](https://developers.openai.com/api/docs/pricing).

- Batch and Flex cost 50% of Standard.
- Fast mode (previously called Priority) costs 2x Standard. It is set with `service_tier: "fast"` ([OpenAI pricing](https://developers.openai.com/api/docs/pricing)).
- For Astra, Fast mode delivers up to 2x speed, with no latency SLA ([GPT-6 Astra announcement](https://openai.com/index/gpt-6-astra/), [model guidance](https://developers.openai.com/api/docs/guides/latest-model)).
- The 272K threshold applies to the whole request ([ComputingForGeeks](https://computingforgeeks.com/gpt-6-sol-luna-released-features-benchmarks/)).
- Reasoning tokens are billed as output ([OpenAI reasoning guide](https://developers.openai.com/api/docs/guides/reasoning)).

### 1.2 Effort support [V]

| Model       | Accepted `reasoning.effort`                            | Default        |
|-------------|--------------------------------------------------------|----------------|
| gpt-6-astra | low, medium, high, xhigh, max. `none` returns HTTP 400 | not documented |
| gpt-6-sol   | none, low, medium, high, xhigh, max                    | medium         |
| gpt-6-luna  | none, low, medium, high, xhigh, max                    | medium         |

Sources: [OpenAI reasoning guide](https://developers.openai.com/api/docs/guides/reasoning), [ComputingForGeeks](https://computingforgeeks.com/gpt-6-sol-luna-released-features-benchmarks/).

All three models share these limits:
- 1.05M-token context window.
- 128K max output.
- Text and image input.

### 1.3 Benchmarks that drive the matrix [V]

Each cell shows the model's best score on that benchmark, with the effort level and cost per task that produced it.

| Benchmark                                     | Astra                         | Sol                  | Luna                |
|-----------------------------------------------|-------------------------------|----------------------|---------------------|
| DeepSWE 1.1 (long-horizon SWE)                | 74.1% (xhigh, $4.43)          | 68.8% (max, $2.74)   | 66.6% (max, $0.22)  |
| FrontierCode 1.1 Main                         | 53.3% (max, $4.59)            | 49.3% (max, $2.14)   | 42.4% (max, $0.11)  |
| AutomationBench (agentic tools)               | 41.4% (max, $1.73)            | 33.2% (xhigh, $0.27) | 20.7% (max, $0.037) |
| Factual error rate (lower is better)          | 3.9% (high; no better at max) | 4.5% (xhigh)         | 7.6% (max)          |
| Coding deception rate @ max (lower is better) | 0.5%                          | 1.3%                 | 2.8%                |

Source: [ComputingForGeeks summary of OpenAI charts](https://computingforgeeks.com/gpt-6-sol-luna-released-features-benchmarks/).

Other coding benchmarks:
- On Terminal-Bench 4.0, Astra scored 57.9% ([OpenAI](https://openai.com/index/gpt-6-astra/)) and Sol scored 43% ([Artificial Analysis via ComputingForGeeks](https://computingforgeeks.com/gpt-6-sol-luna-released-features-benchmarks/)).

**Non-monotonic effort (important).** More effort is not always better:
- Sol at `max` scored 32.0% on AutomationBench, below its 33.2% at `xhigh`.
- Luna at `xhigh` scored 12.6%, below its 14.5% at `high`.

**Luna needs an execution gate.** In a DevOps test, Luna's Kubernetes manifests passed `kubeconform -strict` 3/3 times but hit `CrashLoopBackOff` 3/3 times on k3s. Sol passed 9/9 checks ([ComputingForGeeks](https://computingforgeeks.com/gpt-6-sol-luna-released-features-benchmarks/)).

**Measured latency anchors (short DevOps prompts, mean of 3 runs) [V]:**
- Sol: 8.4–15.7 s
- Luna: 9.1–16.3 s

Source: [ComputingForGeeks](https://computingforgeeks.com/gpt-6-sol-luna-released-features-benchmarks/). Luna is not always faster at the wall clock, because it can emit more reasoning.

Long-horizon anchor: Astra took about 40 min per OSWorld 2.0 task, compared with about 75 min for GPT-5.6 Sol ([OpenAI](https://openai.com/index/gpt-6-astra/)).

### 1.4 Switching mechanics [V]

| Mechanism                                                                          | What it does                                                                            | Constraint                                                                                                                                                                                            |
|------------------------------------------------------------------------------------|-----------------------------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `configuration_update` input item                                                  | Changes effort mid-conversation without rewriting the prefix, so the cache is preserved | GPT-6 in standard single-agent mode only. Two updates cannot sit next to each other. Cannot be combined with auto-compaction or auto-truncation. The response still reports the request-level effort. |
| Request-level `reasoning.effort` edit                                              | Changes effort                                                                          | Can rewrite hidden instructions, which breaks the cache match                                                                                                                                         |
| `allowed_tools`                                                                    | Restricts tools per turn                                                                | Keep the `tools` list identical so the cache is not broken                                                                                                                                            |
| `prompt_cache_options: {mode: "explicit", ttl: "30m"}` + `prompt_cache_breakpoint` | Explicit cache boundary                                                                 | Minimum prefix is 1,024 tokens. TTL is 30 min after last write or reuse.                                                                                                                              |
| `reasoning.mode: "pro"`                                                            | More model work, billed at standard rates                                               | For hard, latency-tolerant tasks                                                                                                                                                                      |
| `reasoning.context: "all_turns"` + `previous_response_id`                          | Carries prior reasoning forward                                                         | Reuse only works within the same model family                                                                                                                                                         |
| `async: true` on tools                                                             | Model keeps reasoning while tools run                                                   | Responses API only                                                                                                                                                                                    |

Sources: [OpenAI reasoning guide](https://developers.openai.com/api/docs/guides/reasoning), [model guidance](https://developers.openai.com/api/docs/guides/latest-model), [ComputingForGeeks](https://computingforgeeks.com/gpt-6-sol-luna-released-features-benchmarks/).

**Design consequence.** Switching models loses the per-model prompt cache [E: caches are keyed per model]. OpenAI does not document whether Astra's reasoning items can be replayed into Sol or Luna; for GPT-5.6 this worked across Sol, Terra, and Luna. So the matrix hands off between models through a **written spec artifact**, not through reasoning state. Effort changes within one model use `configuration_update`.

---

## 2. Workflow Model

```
PHASE A: RESEARCH & PLANNING           PHASE B: CODE EXECUTION
(slow, expensive, low volume)          (fast, cheap, high volume)

A1 Context harvest ──┐                 B1 Scaffold / boilerplate
A2 Architecture/spec ├─► SPEC.md ─────► B2 Feature implementation
A3 Adversarial review│   + TASKS.yaml  B3 Test-fail debugging ──┐
A4 Task decomposition┘   (contract)    B4 Review / security     │
        ▲                              B5 Trivial follow-ups    │
        └──────── escalation: spec gap or 2 failed B3 loops ◄───┘
```

Each phase has an exit gate:
- **Phase A exits** when the spec passes a checklist:
  - Interfaces, data model, error semantics, and non-goals are defined.
  - Acceptance tests are written as runnable commands.
  - Open questions are empty.
- **Phase B exits** only on green CI: tests, lint, `kubeconform`, a deploy smoke test, and so on. It never exits on model self-assessment.

---

## 3. The Matrix

Cells show **effort · latency band [E] · cost per turn [E]**. Bold marks the recommended primary. A dash (—) means not recommended.

Cost per turn uses a reference shape:
- **Planning turn:** 100K input (80K cached) and 25K output.
- **Execution turn:** 60K input (50K cached) and 6K output.

Output grows with effort, so read cost as the price at that effort's typical output size. The worked math is in section 4.

### 3.1 Phase A: Research and Planning

| Stage | GPT-6 Astra | GPT-6 Sol | GPT-6 Luna |
|---|---|---|---|
| A1 Context harvest (repo survey, doc digest, dependency map) | — (overkill) | low · 5–20 s · ~$0.10 | **medium · 5–20 s · ~$0.005** (bulk fan-out, Batch/Flex OK) |
| A2 Architecture & spec authoring | **high · 1–5 min · ~$1.50** | xhigh · 1–4 min · ~$0.35 (budget fallback) | — |
| A2+ Irreversible decisions (schema migrations, public APIs, multi-region topology) | **xhigh, or max + `mode: pro` · 5–20 min · $3–8** | — | — |
| A3 Adversarial spec review (failure modes, security) | high · 1–4 min · ~$1.20 | **xhigh · 1–4 min · ~$0.35** (different model = independent critique) | — |
| A4 Task decomposition to TASKS.yaml | medium · 30–90 s · ~$0.60 | **medium · 20–60 s · ~$0.12** | low · 10–30 s · ~$0.005 |

Why each stage is set this way:
- **A2 on Astra high.** Astra's factual error rate is already at its best at `high`, and `max` doesn't improve it. That makes `high` the cost-efficient ceiling for spec prose. Reserve `xhigh`/`max` for decisions that are expensive to reverse.
- **A3 on Sol xhigh.** This is Sol's best agentic setting, and running a different model than the author gives independent critique.
- **A1 on Luna.** Luna is summarising, not deciding, so its higher error rate is tolerable. Keep it behind citations: it must quote file paths and line numbers.

### 3.2 Phase B: Code Execution

| Stage | GPT-6 Astra | GPT-6 Sol | GPT-6 Luna |
|---|---|---|---|
| B1 Scaffold, boilerplate, CRUD, test stubs | — | low · 5–15 s · ~$0.05 | **low · 5–15 s · ~$0.003**, CI-gated |
| B2 Feature implementation against spec | high (only for tasks tagged `complexity: hard`) · 1–3 min · ~$0.60 | **medium · 10–40 s · ~$0.09** | high · 15–60 s · ~$0.006 (low-risk modules only) |
| B3 Test-failure debugging | **high** (escalation target) · 1–5 min · ~$0.80 | **high** (first attempt) · 30–120 s · ~$0.15 | — |
| B4 Code review & security pass | high · 1–3 min · ~$0.70 | **xhigh · 1–3 min · ~$0.20** | — |
| B5 Trivial follow-ups (rename, docstring, format, one-line fix) | — | **none/low · 2–8 s · ~$0.03** | none · 1–5 s · ~$0.002 |
| Bulk / overnight codegen (migrations across N services) | — | medium + Batch · async · ~$0.045 | **max + Batch · async · ~$0.004** |

Notes on execution:
- **Luna at max is the value outlier.** It reached 66.6% on DeepSWE at $0.22 per task, versus Sol's 68.8% at $2.74. That's 97% of the score at 8% of the cost. The benchmark set also shows Luna produces manifests that validate but fail at runtime. So only merge Luna output after an execution check.
- **Never set Sol above `xhigh`, or Luna above `high`, for agentic loops** unless your evals prove otherwise, because of the non-monotonic results in 1.3.
- **Astra in Phase B is an escalation target only.**

### 3.3 Escalation Ladder (Phase B)

```
Sol medium ──fail──► Sol high (configuration_update, same session, cache kept)
            ──fail──► Astra high (NEW session, seeded with SPEC.md + failing test + diff)
            ──fail──► back to Phase A: spec defect; Astra xhigh amends SPEC.md
```

De-escalation: after a green run, send `configuration_update → low` for the follow-ups (B5).

---

## 4. Token Cost Trade-offs

### 4.1 Per-turn formula

\[ C = \frac{I_{fresh}\,p_{in} + I_{cached}\,p_{cr} + I_{write}\,p_{cw} + O\,p_{out}}{10^6} \]

Here \(O\) is visible output plus reasoning tokens. If the request exceeds 272K tokens, apply the long-context rates to the whole request.

### 4.2 Reference turns [E, computed from verified prices]

| Turn shape | Astra | Sol | Luna |
|---|---:|---:|---:|
| Planning: 80K cached + 20K fresh in, 25K out | $1.53 | $0.31 | $0.015 |
| Execution: 50K cached + 10K fresh in, 6K out | $0.45 | $0.09 | $0.0045 |
| Same execution turn, cold cache (60K fresh) | $0.90 | $0.18 | $0.009 |
| Same planning turn at 300K input (long-ctx rates) | ≈ $3.90+ | ≈ $0.80+ | ≈ $0.04+ |

Takeaways:
1. **Output dominates.** Output tokens make up about 80% of the planning turn's cost on every model. Effort is the main cost lever, and `max_output_tokens` is your hard cap.
2. **Caching halves execution cost.** Keep system prompt, spec, and tool schemas as a stable prefix above an explicit breakpoint.
3. **Stay under 272K.** Feeding the whole repo into Phase B roughly doubles input cost and adds 1.5x to output cost. Feed the spec, the task, and the touched files only.
4. **Astra's per-token premium is partly offset.** Astra uses notably fewer output tokens: about 65% fewer than Claude Opus 5 on Agents' Last Exam ([OpenAI](https://openai.com/index/gpt-6-astra/)). Its cost per task is therefore less than 5x Sol's, even though its list price is 5x.

### 4.3 Effort-to-output multipliers [E, starting heuristics; replace with your `reasoning_tokens` telemetry]

| Effort | none | low | medium | high | xhigh | max |
|---|---|---|---|---|---|---|
| Relative reasoning tokens (medium = 1.0) | 0 | 0.3 | 1.0 | 2–3 | 4–6 | 6–10 |
| Suggested `max_output_tokens` | 4K | 8K | 25K | 40K | 64K | 100K |

OpenAI recommends reserving at least 25K tokens for reasoning plus output when you first experiment ([reasoning guide](https://developers.openai.com/api/docs/guides/reasoning)).

### 4.4 Example feature budget (one medium feature, about 12 tasks)

| Step | Config | Est. cost |
|---|---|---:|
| A1 harvest ×4 | Luna medium | $0.02 |
| A2 spec ×3 turns | Astra high | $4.60 |
| A3 review ×1 | Sol xhigh | $0.35 |
| A4 decompose ×1 | Sol medium | $0.12 |
| B2 implement ×12 | Sol medium | $1.10 |
| B3 debug ×4 (1 escalates) | Sol high ×3, Astra high ×1 | $1.25 |
| B4 review ×2 | Sol xhigh | $0.40 |
| B5 follow-ups ×15 | Sol low | $0.45 |
| **Total** | | **≈ $8.30** |

For comparison, the all-Astra-high equivalent is about $25–30 [E]. Most of the savings come from moving implementation to Sol while keeping the spec on Astra.

---

## 5. Prompt Configuration Templates

### 5.1 Stable cached prefix (shared by every Phase B call)

Order matters for caching. The stable content comes first, then the breakpoint, then the volatile content.

```
[system]    ROLE + GLOBAL RULES            (stable)
[developer] SPEC.md (frozen version hash)  (stable per feature)
[tools]     full tool list, never mutated  (stable)
─── prompt_cache_breakpoint ───
[user]      TASK-### + touched files + failing output   (volatile)
```

### 5.2 Phase A: Architect (Astra high)

```json
{
  "model": "gpt-6-astra",
  "reasoning": { "effort": "high", "summary": "auto" },
  "max_output_tokens": 40000,
  "store": true,
  "prompt_cache_options": { "mode": "explicit", "ttl": "30m" },
  "tools": [ /* repo_search, read_file, web_search — full list */ ],
  "input": [
    { "role": "system", "content": [{ "type": "input_text", "text":
"You are the ARCHITECT. You do not write implementation code.\nProduce SPEC.md with sections: Context, Goals, Non-goals, Constraints, Architecture (components + data flow), Interfaces (exact signatures / OpenAPI / proto), Data model & migrations, Error semantics, Observability (metrics, traces, logs), Security, Rollout & rollback, Acceptance tests (runnable shell commands), Risks, Open questions.\nRules: cite file:line for every claim about existing code; mark assumptions ASSUMPTION:; Open questions must be empty before you emit STATUS: SPEC_READY." }],
      "prompt_cache_breakpoint": true },
    { "role": "user", "content": [{ "type": "input_text", "text": "<feature request + A1 digest>" }] }
  ]
}
```

For irreversible decisions, use `"reasoning": {"effort": "xhigh"}`. Or use `{"effort": "max", "mode": "pro"}` with `max_output_tokens: 100000` and `service_tier: "flex"` if the run can be asynchronous.

### 5.3 Phase A: Adversarial Reviewer (Sol xhigh)

```json
{
  "model": "gpt-6-sol",
  "reasoning": { "effort": "xhigh" },
  "max_output_tokens": 64000,
  "input": [
    { "role": "system", "content": [{ "type": "input_text", "text":
"You are a hostile reviewer. Find defects in SPEC.md: ambiguous interfaces, missing error paths, race conditions, migration hazards, untestable acceptance criteria, security gaps. Output JSON: {\"blocking\":[...],\"non_blocking\":[...],\"verdict\":\"APPROVE|REVISE\"}. Each item: section, issue, concrete fix." }] },
    { "role": "user", "content": [{ "type": "input_text", "text": "<SPEC.md>" }] }
  ]
}
```

### 5.4 Phase A to B handoff contract: TASKS.yaml (Sol medium output)

```yaml
spec_version: sha256:…
tasks:
  - id: TASK-001
    title: Add PolicyRepository.FindByHolder
    complexity: easy | normal | hard        # drives routing
    risk: low | high                        # high => no Luna
    files: [internal/policy/repo.go, internal/policy/repo_test.go]
    depends_on: []
    acceptance: ["go test ./internal/policy/... -run FindByHolder"]
```

### 5.5 Phase B: Implementer (Sol medium, effort switching inside one session)

The first call sets the request-level effort once and never edits it again:

```json
{
  "model": "gpt-6-sol",
  "reasoning": { "effort": "medium" },
  "max_output_tokens": 25000,
  "store": true,
  "prompt_cache_options": { "mode": "explicit", "ttl": "30m" },
  "tools": [ /* read_file, apply_patch, run_tests, run_shell */ ],
  "input": [
    { "role": "system", "content": [{ "type": "input_text", "text":
"You are the IMPLEMENTER. SPEC.md is the contract; do not change interfaces it defines. If the spec is wrong or silent, stop and emit SPEC_GAP: <description> instead of guessing. Work loop: read → patch → run acceptance commands → report. Output only diffs and a 3-line summary." }] },
    { "role": "developer", "content": [{ "type": "input_text", "text": "<SPEC.md>" }],
      "prompt_cache_breakpoint": true },
    { "role": "user", "content": [{ "type": "input_text", "text": "<TASK-001 yaml + file contents>" }] }
  ]
}
```

To escalate to debugging (B3), send a follow-up in the same session. Setting request-level `reasoning.effort` to `"medium"` again is intentional: leaving it unchanged keeps the cache.

```json
{
  "model": "gpt-6-sol",
  "previous_response_id": "resp_…",
  "reasoning": { "effort": "medium" },
  "input": [
    { "type": "configuration_update", "reasoning": { "effort": "high" } },
    { "role": "user", "content": [{ "type": "input_text", "text": "Acceptance failed:\n<test output>\nDiagnose root cause before patching." }] }
  ]
}
```

To de-escalate for follow-ups (B5), use the same pattern with `{"type":"configuration_update","reasoning":{"effort":"low"}}`. The API rejects two `configuration_update` items placed next to each other, so always put a user message between them.

To switch to review mode (B4) without mutating tools, pass `"tool_choice": {"type": "allowed_tools", "mode": "auto", "tools": [{"type": "function", "name": "read_file"}]}` [verify exact shape against the current API reference].

### 5.6 Phase B: Bulk generator (Luna, CI-gated)

```json
{
  "model": "gpt-6-luna",
  "reasoning": { "effort": "low" },
  "service_tier": "flex",
  "max_output_tokens": 8000,
  "input": [
    { "role": "system", "content": [{ "type": "input_text", "text":
"Generate code exactly matching the provided interface and example. No new dependencies. No placeholder logic. Output a single unified diff." }] },
    { "role": "user", "content": [{ "type": "input_text", "text": "<interface + one golden example + target file>" }] }
  ]
}
```

Luna output is merged only if the task's acceptance commands and a runtime smoke test (for example, a deploy to kind or k3s) pass.

---

## 6. Automated Router

### 6.1 Policy file: `router.yaml`

```yaml
models:
  architect: gpt-6-astra
  worker:    gpt-6-sol
  bulk:      gpt-6-luna

routes:
  A1_harvest:     { model: bulk,      effort: medium, tier: flex,     max_out: 25000 }
  A2_spec:        { model: architect, effort: high,   tier: standard, max_out: 40000 }
  A2_irreversible:{ model: architect, effort: xhigh,  tier: standard, max_out: 64000, mode: pro }
  A3_review:      { model: worker,    effort: xhigh,  tier: standard, max_out: 64000 }
  A4_decompose:   { model: worker,    effort: medium, tier: standard, max_out: 25000 }
  B1_scaffold:    { model: bulk,      effort: low,    tier: standard, max_out: 8000,  require_ci: true }
  B2_implement:   { model: worker,    effort: medium, tier: standard, max_out: 25000 }
  B2_hard:        { model: architect, effort: high,   tier: standard, max_out: 40000 }
  B3_debug:       { model: worker,    effort: high,   switch: configuration_update }
  B3_escalate:    { model: architect, effort: high,   new_session: true, seed: [SPEC.md, failing_test, diff] }
  B4_review:      { model: worker,    effort: xhigh,  allowed_tools: [read_file, run_tests] }
  B5_followup:    { model: worker,    effort: low,    switch: configuration_update }
  bulk_overnight: { model: bulk,      effort: max,    tier: batch,    require_ci: true }

rules:
  - if: task.risk == high           then: forbid_model: bulk
  - if: task.complexity == hard     then: route: B2_hard
  - if: output contains "SPEC_GAP"  then: route: A2_spec   # back to planning
  - if: b3_failures >= 2            then: route: B3_escalate
  - if: est_input_tokens > 272000   then: action: trim_context   # avoid long-ctx rates
  - never: [ "sol.effort == max", "luna.effort == xhigh" ]       # non-monotonic zones
budgets:
  per_feature_usd: 15
  alert_on_reasoning_tokens_p95_over: 60000
```

### 6.2 Minimal Go dispatcher (sketch)

```go
type Route struct {
    Model, Effort, Tier string
    MaxOut              int
    Switch              string // "configuration_update" | ""
    NewSession          bool
}

func (r *Router) Next(s *Session, t Task, lastOut string) Route {
    switch {
    case strings.Contains(lastOut, "SPEC_GAP"):
        return r.routes["A2_spec"]
    case s.Phase == PhaseB && s.Failures >= 2:
        return r.routes["B3_escalate"]
    case s.Phase == PhaseB && s.Failures == 1:
        return r.routes["B3_debug"] // same session; emit configuration_update
    case t.Complexity == "hard":
        return r.routes["B2_hard"]
    case t.Risk == "low" && t.Kind == "scaffold":
        return r.routes["B1_scaffold"]
    default:
        return r.routes["B2_implement"]
    }
}

// Switching rule: same model → append configuration_update, keep request effort fixed.
// Different model → new session seeded with SPEC.md (artifact handoff, no reasoning replay).
```

---

## 7. Calibration Checklist

1. Log `usage.output_tokens_details.reasoning_tokens`, `cached_tokens`, and wall-clock time per route. Replace the **[E]** bands and multipliers after about 50 runs per route.
2. Run a fixed eval set of 20–30 tasks from your own repo at each candidate effort. Keep the knee of the cost/pass-rate curve, not the maximum.
3. Track cache hit rate per route. Below about 60% on Phase B usually means the prefix is being mutated, typically by tool list edits or effort edits at request level.
4. Re-verify prices and effort support monthly. GPT-6 Sol and Luna have no dated snapshots yet ([ComputingForGeeks](https://computingforgeeks.com/gpt-6-sol-luna-released-features-benchmarks/)).
