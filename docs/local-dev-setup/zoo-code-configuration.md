> This document defines a repeatable configuration and workflow for using **Zoo Code** with **local Ollama models** on the Insurance Hub codebase, aligned with the Phase 3 and Phase 4 migration plan.

<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Prerequisites](#prerequisites)
  - [Hardware & OS](#hardware--os)
  - [Required software](#required-software)
- [Local models and strategy](#local-models-and-strategy)
  - [Confirm installed models](#confirm-installed-models)
  - [Model role mapping](#model-role-mapping)
- [Zoo Code installation and basic wiring](#zoo-code-installation-and-basic-wiring)
  - [Install Zoo Code](#install-zoo-code)
  - [Ensure Ollama API is running](#ensure-ollama-api-is-running)
- [API configuration profiles (Ollama)](#api-configuration-profiles-ollama)
  - [Profile: `Ollama – Coding`](#profile-ollama--coding)
  - [Profile: `Ollama – Ask/Architect`](#profile-ollama--askarchitect)
- [Mode ↔ profile mapping](#mode--profile-mapping)
- [Codebase indexing and context pre‑filtering](#codebase-indexing-and-context-pre%E2%80%91filtering)
  - [Turn on indexing for the workspace](#turn-on-indexing-for-the-workspace)
  - [Choose embedding provider](#choose-embedding-provider)
  - [Ignore patterns (`.rooignore`)](#ignore-patterns-rooignore)
  - [Practical pre‑filtering during use](#practical-pre%E2%80%91filtering-during-use)
- [Conversation context management](#conversation-context-management)
  - [Intelligent Context Condensing](#intelligent-context-condensing)
  - [Avoiding context poisoning](#avoiding-context-poisoning)
- [Repository‑level guidance: `AGENTS.md` and `SKILLS.md`](#repository%E2%80%91level-guidance-agentsmd-and-skillsmd)
  - [`AGENTS.md` (how to work with agents in this repo)](#agentsmd-how-to-work-with-agents-in-this-repo)
  - [`SKILLS.md` (workflow recipes)](#skillsmd-workflow-recipes)
- [Usage checklist](#usage-checklist)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

## Prerequisites

### Hardware & OS

- Development laptop with:
    - 64 GB RAM, multi‑core Intel CPU, and NVIDIA RTX 4070 (or comparable GPU).
    - Ubuntu 24.04 LTS (or similar 64‑bit Linux).

These specs are sufficient to run 7–16B code models locally via Ollama with acceptable latency.

### Required software

- **VS Code** (current stable):  
  https://code.visualstudio.com/

- **Zoo Code** VS Code extension:
    - Marketplace page:  
      https://marketplace.visualstudio.com/items?itemName=ZooCodeOrganization.zoo-code
    - Project home:  
      https://www.zoocode.dev
    - Docs home:  
      https://docs.zoocode.dev

- **Ollama**, installed and on PATH:
    - Website & install instructions:  
      https://ollama.com
    - Quickstart:  
      https://docs.ollama.com/quickstart

- Git, Docker, and any runtime dependencies are already required by the Insurance Hub project.

## Local models and strategy

### Confirm installed models

Check your local model list:

```bash
ollama ls
```

Make sure you have **at least**:

- One strong **code model** (for Code/Debug), such as:
    - `qwen2.5-coder:14b` or `deepseek-coder-v2:16b`.
- One general **reasoning/chat model** (for Ask/Architect), such as:
    - `llama3.1:8b`, `deepseek-r1:8b`, `mistral:latest`, or `phi4-mini:latest`.

You can add or remove models later depending on performance and quality.

### Model role mapping

Recommended roles:

- **Primary Code/Debug**: `qwen2.5-coder:14b` (or `deepseek-coder-v2:16b`).
- **Primary Ask/Architect**: `llama3.1:8b` or `deepseek-r1:8b`.
- **Fallback / fast Q&A**: `mistral:latest` or `phi4-mini:latest`.

## Zoo Code installation and basic wiring

### Install Zoo Code

1. Open VS Code.
2. Go to **Extensions** (`Ctrl+Shift+X`).
3. Search for **“Zoo Code”** and install it from Zoo Code Organization.
4. Click the **Zoo Code icon** in the Activity Bar to open the panel.

Useful links:

- Project:  
  https://github.com/Zoo-Code-Org/Zoo-Code
- Docs:  
  https://docs.zoocode.dev

### Ensure Ollama API is running

1. Start Ollama:

   ```bash
   ollama serve
   ```

2. Verify the API:

   ```bash
   curl http://localhost:11434/api/tags
   ```

   You should see your model list in JSON. This base URL (`http://localhost:11434`) is what Zoo Code will use.

Ollama API reference (for model naming and endpoints):  
https://github.com/ollama/ollama/blob/main/docs/api.md

## API configuration profiles (Ollama)

Zoo Code uses **API Configuration Profiles** to define provider, base URL, models, and defaults per profile.

- Docs:  
  https://docs.zoocode.dev/features/api-configuration-profiles

Open the provider settings:

1. In the Zoo Code panel, click the **gear icon → Providers**.
2. Locate the **profile dropdown** at the top.

### Profile: `Ollama – Coding`

**Purpose:** Main engine for `💻 Code` and `🪲 Debug` modes.

1. Click **“+”** in the profile dropdown and name the profile: `Ollama – Coding`.
2. Provider: select the **Ollama / local OpenAI‑compatible** provider (whatever Zoo exposes for local models).
3. Base URL: `http://localhost:11434`.
4. API key: leave blank (Ollama does not require keys).
5. Default model: choose a strong coder model:
    - Prefer: `qwen2.5-coder:14b`.
    - Alternative: `deepseek-coder-v2:16b` or `codestral:latest`.
6. Temperature: **0 – 0.2** for deterministic code generation.
7. Rate limit: `0` (no local throttling).

### Profile: `Ollama – Ask/Architect`

**Purpose:** Reasoning and architectural discussion for `❓ Ask` and `🏗️ Architect` modes.

1. Create another profile: `Ollama – Ask/Architect`.
2. Provider: same **Ollama/local** provider.
3. Base URL: `http://localhost:11434`.
4. Default model:
    - Prefer: `llama3.1:8b`.
    - Alternative: `deepseek-r1:8b`, `mistral:latest`, or `phi4-mini:latest`.
5. Temperature: **0.3 – 0.6** for more exploratory answers.
6. Rate limit: `0`.

You can pin these profiles so they appear at the top of the profile dropdown for quick access.

## Mode ↔ profile mapping

Zoo Code has several built‑in modes with different tools and behavior: **Code, Ask, Architect, Debug, Orchestrator**.

- Mode docs:  
  https://docs.zoocode.dev/basic-usage/using-modes

Recommended mapping:

- `💻 Code` → **`Ollama – Coding`**
- `🪲 Debug` → **`Ollama – Coding`**
- `❓ Ask` → **`Ollama – Ask/Architect`**
- `🏗️ Architect` → **`Ollama – Ask/Architect`**
- `🪃 Orchestrator` → **`Ollama – Coding`** (initially; you can later bind this to a cloud profile).

To configure:

1. Open Zoo Code **Settings**.
2. Navigate to the **Prompts / Modes** configuration section.
3. For each mode, set **Associated configuration profile** as above.
4. In the Zoo panel, switch modes and verify the **profile dropdown** changes accordingly.

## Codebase indexing and context pre‑filtering

Zoo Code builds a semantic index of your repo so it can retrieve relevant snippets instead of naïvely loading entire files. The design is inherited from Roo Code’s **Codebase Indexing** (semantic embeddings + vector store):

- Roo Code overview:  
  https://www.reddit.com/r/RooCode/comments/1m98zpw/how_roo_code_understands_your_entire_repo/
- Example video (indexing concepts):  
  https://www.youtube.com/watch?v=r1bpod1VWhg

### Turn on indexing for the workspace

1. Open the Insurance Hub repo as the **workspace root** in VS Code.
2. In Zoo Code, open the **codebase indexing sidebar** (database icon in the panel).
3. Enable **“Index this workspace”** (or equivalent toggle).
4. If you change provider settings for indexing, disable → save → re‑enable → save, then **Start Indexing** to rebuild.

Zoo will chunk files, compute embeddings, and store them in a local index (similar to Roo Code’s Qdrant‑based approach).

### Choose embedding provider

In Zoo Code **Settings → Code Indexing** (names may change slightly as Zoo evolves):

- Set **Embedding provider** to:
    - **Ollama** (if supported in your build), with base URL `http://localhost:11434`; or
    - A cloud provider (e.g. OpenAI) if you are comfortable sending embeddings externally.

Relevant Roo Code feature discussions (embedding providers):

- https://github.com/RooCodeInc/Roo-Code/issues/4869
- https://github.com/RooCodeInc/Roo-Code/issues/3959

If Ollama does not yet appear as an embedding provider, use the default embedding endpoint offered by Zoo Code (often OpenAI in current builds) and revisit this later.

### Ignore patterns (`.rooignore`)

Create `.rooignore` at the repo root to keep the index focused:

```gitignore
# Generated / build output
dist/
build/
out/
target/
coverage/

# Dependencies
node_modules/
**/vendor/
**/.venv/

# VCS and misc
.git/
*.log
*.jar
*.zip
```

Zoo Code uses `.rooignore` to filter file suggestions and related operations, which helps keep noise out of context and indexing.

- Release notes referencing `.rooignore`:  
  https://docs.zoocode.dev/update-notes/v3.8

### Practical pre‑filtering during use

When prompting:

- Prefer to **scope requests** with `@file` / `@folder` mentions, for example:
    - “For this task, focus only on files in `@product-service/` and its tests.”

Zoo will:

- Use file‑listing tools (e.g. `list_files`) and semantic search to prefer those locations.
- Pull **partial snippets** rather than whole files into model context.

`list_files` tool docs:  
https://docs.zoocode.dev/advanced-usage/available-tools/list-files

This provides both index‑time and query‑time pre‑filtering.

## Conversation context management

Long sessions can hit context limits or degrade (context poisoning). Zoo Code ships with **Intelligent Context Condensing** and guidance on poisoning.

### Intelligent Context Condensing

- Docs:  
  https://docs.zoocode.dev/features/intelligent-context-condensing

Key points:

- Enabled by default under Zoo Code **Context Management** settings.
- When you approach the model’s context limit, Zoo:
    - Summarizes earlier parts of the conversation into a condensed representation.
    - Preserves key facts, decisions, and instructions while freeing tokens.

You can customize the condense prompt to emphasize what to preserve (e.g., migration constraints, acceptance criteria, service names).

### Avoiding context poisoning

- Docs:  
  https://docs.zoocode.dev/advanced-usage/context-poisoning

Best practices:

- Treat each **ticket or migration step** as its own Zoo session.
- If responses become inconsistent or clearly wrong, **start a fresh session** instead of trying to "repair" the current one.
- Keep prompts focused, and rely on indexing + `@file` to reintroduce the exact code/context needed.

## Repository‑level guidance: `AGENTS.md` and `SKILLS.md`

To make this configuration discoverable and consistent for future work, add two repo‑root docs.

### `AGENTS.md` (how to work with agents in this repo)

Suggested content:

- Brief overview of Zoo Code and the chosen local model strategy (Ollama profiles and modes).
- Instructions for:
    - **Phase 3** tasks (data store consolidation, Java changes).
    - **Phase 4** tasks (Go rewrites with Strangler Fig).
- Mode usage:
    - Architect: planning and specs.
    - Code: implementation.
    - Debug: test and fix cycle.
    - Ask: read‑only Q&A.
    - Orchestrator: breaking large tasks into subtasks and delegating.
- Notes on indexing and `.rooignore`.

### `SKILLS.md` (workflow recipes)

Define explicit “skills” or recipes, for example:

1. **Spec‑First Implementation (Phase 3 – Java)**
    - **Architect**:
        - Read the relevant section of the migration doc and service code.
        - Produce a spec: goals, constraints, acceptance criteria.
    - **Code**:
        - Implement changes according to the spec, using local models and code indexing.
    - **Debug**:
        - Run tests, fix issues, verify acceptance criteria.

2. **Go Service Rewrite with Strangler Fig (Phase 4)**
    - **Architect**:
        - Design gRPC APIs, GORM models, MinIO integration, and observability (OTel / Prometheus / Tempo / Loki) for the target service.
    - **Code**:
        - Implement the Go service alongside the existing Java service.
    - **Debug**:
        - Ensure parity, add tests, and verify readiness for traffic cutover.
    - **Ask**:
        - Clarify design choices and constraints without editing code.
    - **Orchestrator**:
        - Break large migration tickets into smaller tasks across Architect/Code/Debug.

## Usage checklist

1. Install VS Code and Zoo Code.
2. Install Ollama and pull recommended models.
3. Verify `ollama serve` and `curl http://localhost:11434/api/tags`.
4. Import this repo at workspace root in VS Code.
5. Create or select:
    - `Ollama – Coding` profile.
    - `Ollama – Ask/Architect` profile.
6. Map modes to those profiles.
7. Ensure indexing is enabled; `.rooignore` is present and sane.
8. Read `AGENTS.md` and `SKILLS.md` to understand how to use Zoo for Phase 3/4 work.
