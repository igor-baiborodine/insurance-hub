> This document details the selection process for local planning models used in this repository on a laptop running Ollama. The target system, equipped with 64 GB of RAM, an Intel i7-14650HX, and an NVIDIA GeForce RTX 4070 Laptop GPU, provides a robust environment for local inference but requires balancing reasoning depth and multi-step plan quality against VRAM constraints and latency to maintain a productive spec-first development experience.

<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Hardware Context](#hardware-context)
- [Evaluation](#evaluation)
  - [Models](#models)
  - [Prompts](#prompts)
  - [Criteria](#criteria)
- [Performance Summary](#performance-summary)
  - [DeepSeek‑R1‑8B (Reasoning Planner)](#deepseek%E2%80%91r1%E2%80%918b-reasoning-planner)
  - [Llama‑3.1‑8B (Balanced Planning Assistant)](#llama%E2%80%9131%E2%80%918b-balanced-planning-assistant)
  - [Mistral (7B) (Alternative Planning/Workhorse)](#mistral-7b-alternative-planningworkhorse)
  - [Phi4‑mini (Fast Checklist generator)](#phi4%E2%80%91mini-fast-checklist-generator)
- [Final Model Selection](#final-model-selection)
  - [Large-model Experiment (Magistral‑24B)](#large-model-experiment-magistral%E2%80%9124b)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

## Hardware Context

- CPU: Intel i7‑14650HX (16 cores, 24 threads, well-suited for local inference). 
- GPU: NVIDIA GeForce RTX 4070 Laptop GPU, 8 GB VRAM. 
- RAM: 64 GB. 

## Evaluation

### Models

To choose planning models, four candidates were evaluated locally:

- `phi4-mini:latest` (3.8B)
- `mistral:latest` (7B)
- `llama3.1:8b`
- `deepseek-r1:8b`

### Prompts

Each model was tested using the following generic planning prompt suite:

1. **Decomposition**

   “List 7 concrete steps to plan and ship a small backend feature, from initial requirements to deployment. Group them into 3 phases and name each phase.”

2. **Prioritization**

   “You have tasks A, B, C, D, E and can only do 3 this week. Describe a simple prioritization strategy and pick the 3 tasks with a one-sentence justification each.”

3. **Constraint plan**

   “Design a 3–5 step deployment plan for a web service that must have zero downtime and a rollback option after each step. Number the steps and clearly mark the rollback for each.”

4. **Info-first planning**

   “Before planning a database migration, list the key information you need to collect. Then, based only on that information, propose a 4-step high-level migration plan.”

5. **Clarification + plan**

   “A stakeholder says, ‘Make the system more scalable and reliable.’ First, ask 5 clarifying questions. Then propose a 4-step improvement plan assuming reasonable answers.”

### Criteria

For each prompt and model, the following were recorded:

- Throughput metrics from Ollama (`eval rate` in tokens/s, total tokens generated, prompt eval rate, load duration, total duration). 
- Qualitative reasoning characteristics: structure, constraint‑handling, depth, and practicality of the plan. 

## Performance Summary

### DeepSeek‑R1‑8B (Reasoning Planner)

- **Role:** High‑depth planning and “safety‑critical” strategy work.
- **Reasoning behavior:**
    - Consistently produced the longest outputs, e.g., 1,101 tokens for a decomposition prompt, 2,750 tokens for a zero‑downtime deployment plan, and 1,672–1,927 tokens for info‑first migration and clarification prompts. 
    - Spent a large fraction of tokens in explicit “thinking” blocks, iterating on plans and checking constraints (e.g., revisiting load balancer behavior, rollback states, and detailed migration prerequisites). 
- **Latency and throughput:**
    - Eval rate around 42–45 tokens/s, slower than other models, but on much larger token counts. 
    - Total durations ranged from ~15 seconds for short prompts up to ~40–46 seconds for complex planning prompts and over one minute for the strict zero‑downtime deployment plan. 
- **When to use:**
    - When planning real deployments or database migrations where missing an edge case is costly.
    - When an additional 20–60 seconds of latency is acceptable in exchange for a richer “pre‑flight checklist” style output with explicit reasoning about constraints and failure modes. 

### Llama‑3.1‑8B (Balanced Planning Assistant)

- **Role:** Default planning assistant for day‑to‑day work, where a balance between depth and responsiveness is needed.
- **Reasoning behavior:**
    - Produced clean, structured responses with clear numbering and grouping on decomposition, prioritization, constraint plans, migration planning, and clarification prompts. 
    - Often took a “standard professional” approach (e.g., clear steps, reasonable grouping, sometimes including example dialogue), without the extreme verbosity of DeepSeek. 
- **Latency and throughput:**
    - Eval rate around 47–48 tokens/s across prompts, similar to Mistral. 
    - Load durations consistently under 2 seconds, with total durations around 9–15 seconds depending on token count. 
- **When to use:**
    - For general planning and technical writing where responses must be fast enough for interactive use but still structured and detailed.
    - As the primary “planning model” referenced by tickets that expect spec‑first discussion and architecture support.

### Mistral (7B) (Alternative Planning/Workhorse)

- **Role:** Alternative balanced planner and general‑purpose technical writing model.
- **Reasoning behavior:**
    - Produced responses very similar in structure and quality to Llama‑3.1 on decomposition, prioritization, constraint plans, and info‑first prompts, often using standard frameworks (e.g., urgency/importance for prioritization). 
    - Slightly more concise than Llama in some cases, with fewer tokens overall while still respecting formatting and constraints. 
- **Latency and throughput:**
    - Eval rate around 50 tokens/s, consistently a bit faster than Llama‑3.1 on generation. 
    - Load times are among the best in the group (around 1.3–1.7 seconds), leading to a very responsive experience for cold and warm starts. 
- **When to use:**
    - As a backup or alternate planning model when slightly higher generation speed is preferred.
    - For integration into tools where low latency is especially visible (e.g., IDE helpers).

### Phi4‑mini (Fast Checklist generator)

- **Role:** Lightweight planner optimized for speed and quick drafting.
- **Reasoning behavior:**
    - Generated concise but well‑structured answers, correctly following formatting and constraint instructions (e.g., numbering steps and marking rollbacks, handling two‑part prompts with info collection plus a plan). 
    - Focused on high‑level checklists rather than exhaustive edge‑case analysis. 
- **Latency and throughput:**
    - Fastest eval rate across all tests, consistently around 82–84 tokens/s. 
    - Prompt eval rates above 2,000 tokens/s in multiple runs, and total durations often in the 4–8 second range even for multi‑part prompts. 
- **When to use:**
    - For rapid prototyping, quick checklists, and high‑level planning where “instant” responses are more important than exhaustive reasoning.
    - As a utility model when iterating rapidly on ideas or exploring variants on a plan.

## Final Model Selection

Based on local hardware benchmarks and qualitative analysis:

- **Primary comprehensive planning model:**
    - `deepseek-r1:8b` is selected for high‑stakes planning tasks where depth and explicit reasoning about constraints outweigh latency. 
- **Primary day‑to‑day planning assistant:**
    - `llama3.1:8b` is selected as the main planning model for most spec‑first and architecture discussions, offering a strong trade‑off between reasoning quality and responsiveness. 
- **Supporting models:**
    - `mistral:latest` is kept as an alternative workhorse with similar quality and slightly better speed characteristics. 
    - `phi4-mini:latest` is kept as a fast drafting and checklist generator when ultra‑low latency is desired. 

### Large-model Experiment (Magistral‑24B)

To understand how a larger model behaves on this hardware, Prompt 1 from the generic planning suite was run against `magistral:24b`. The recorded metrics were:

- Load duration: ~2.16 s
- Prompt evaluation: 204 tokens in ~0.9 s (≈225 tokens/s)
- Generation: 4,135 tokens in ~11m19 s (≈6.1 tokens/s)
- Total duration: ~11m24.9 s for a single response

On this RTX 4070 (8 GB VRAM, 64 GB RAM) setup, these numbers mean:

- **Interactivity:** While load time and prompt prefill are acceptable, the effective generation speed (~6 tokens/s) combined with a long response length pushes total latency into the **11+ minute** range for a single planning answer.
- **Comparison to 8B models:** The 7–8B models (Phi‑4‑mini, Mistral, Llama‑3.1‑8B, DeepSeek‑R1‑8B) consistently produced comparable planning outputs in **4–15 seconds**, with eval rates in the ~40–80 tokens/s range on similar prompts. 

**Given this experiment:**

- `magistral:24b` is considered **impractical for interactive local planning** on this machine due to multi‑minute response times.
- The project will **not** rely on 24B‑class models in the local runtime; instead, it standardizes on **7–8B models** that deliver a much better balance of reasoning quality and latency for spec‑first and coding workflows. 