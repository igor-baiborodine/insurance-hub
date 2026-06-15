> This document defines a practical local AI development configuration for this repository using **Zoo Code** with **Ollama**, including the selected planning and coding models, the recommended profile mappings for each mode, and the supporting workspace practices for indexing, context management, and day-to-day usage.

## Prerequisites

**Hardware & OS**

- Development laptop with:
  - 64 GB RAM
  - Intel i7-14650HX or similar multi-core CPU
  - NVIDIA RTX 4070 Laptop GPU or comparable GPU
  - Ubuntu 24.04 LTS or similar 64-bit Linux

**Required software**

- VS Code
- Zoo Code VS Code extension
- Ollama, installed and available on `PATH`
- Local model pulls for every selected model listed below

Recommended verification commands:

```bash
ollama ls
ollama serve
curl http://localhost:11434/api/tags
```

## Selected Local Models

**Planning models**

- `mistral-nemo:12b` — primary comprehensive planning model.
- `llama3.1:8b` — primary day-to-day planning assistant.
- `phi4-mini:latest` — fast checklist and drafting model.

**Coding models**

- `qwen2.5-coder:7b` — primary interactive coding model.
- `qwen3-coder:30b` — safety-critical and concurrency coding model.
- `codestral:22b` — reference and idiomatic backup model.

## Zoo Code Configuration

### API Configuration Profiles

Open Zoo Code provider settings, then create one API configuration profile for each selected model.

General defaults for every profile:

- Provider: Ollama or the local Ollama-compatible provider exposed by Zoo Code.
- Base URL: `http://localhost:11434`
- API key: blank
- Rate limit: `0`

#### Planning

1. **Ollama – Planning (Deep)**

    **Purpose:** High-stakes planning, migrations, rollback design, and constraint-heavy architecture work.

- Profile name: `Planing - mistral-nemo:12b`
- Model: `mistral-nemo:12b`
- Temperature: `0.2`
- Use for: `Ask`, `Architect`, or `Orchestrator` when depth matters more than speed.

2. **Ollama – Planning (Balanced)**

    **Purpose:** Default planning and architecture discussions for normal day-to-day work.

- Profile name: `Planning - llama3.1:8b`
- Model: `llama3.1:8b`
- Temperature: `0.3`
- Use for: default `Ask` and `Architect` sessions.

3. **Ollama – Planning (Fast)**

    **Purpose:** Rapid checklist generation, lightweight decomposition, and quick iteration.

- Profile name: `Planning - phi4-mini:3.8b`
- Model: `phi4-mini:latest`
- Temperature: `0.3`
- Use for: short planning bursts, quick clarifications, and fast drafts.

#### Coding

1. **Ollama – Coding (Fast)**

    **Purpose:** Main interactive coding profile for implementation speed.

- Profile name: `Coding - qwen2.5-coder:7b`
- Model: `qwen2.5-coder:7b`
- Temperature: `0.1`
- Use for: `Code`, `Debug`, and lighter `Orchestrator` tasks.

2. **Ollama – Coding (Safety)**

    **Purpose:** Complex concurrency, critical refactors, and instruction-sensitive implementation tasks.

- Profile name: `Coding - qwen3-coder:30b`
- Model: `qwen3-coder:30b`
- Temperature: `0.0`
- Use for: code paths where correctness and constraint-following matter more than latency.

3. **Ollama – Coding (Reference)**

    **Purpose:** High-fidelity reference output for idiomatic structure and clean-code comparison.

- Profile name: `Coding - codestral:22b`
- Model: `codestral:22b`
- Temperature: `0.1`
- Use for: architectural reference implementations, test structure review, and code-shape comparison.

### Mode to Profile Mapping

**Recommended default mapping in Zoo Code**

- `Code` → `Ollama – Coding (Fast)`
- `Debug` → `Ollama – Coding (Fast)`
- `Ask` → `Ollama – Planning (Balanced)`
- `Architect` → `Ollama – Planning (Balanced)`
- `Orchestrator` → `Ollama – Planning (Deep)`

**Recommended override mapping for specific tasks**

- Switch `Code` or `Debug` to `Ollama – Coding (Safety)` for concurrency, transactional logic, or tricky state changes.
- Switch `Code` to `Ollama – Coding (Reference)` when comparing implementation style or generating cleaner reference structure.
- Switch `Ask` or `Architect` to `Ollama – Planning (Fast)` for quick drafts.
- Switch `Ask`, `Architect`, or `Orchestrator` to `Ollama – Planning (Alt)` when you want a fast alternate answer.

## Codebase Indexing and Context Pre-filtering

Zoo Code builds a semantic index of your repo so it can retrieve relevant snippets instead of naively loading entire files. The design is inherited from Roo Code’s **Codebase Indexing** approach, which combines semantic embeddings with a vector store for targeted retrieval.

- Roo Code overview:  
  https://www.reddit.com/r/RooCode/comments/1m98zpw/how_roo_code_understands_your_entire_repo/
- Example video (indexing concepts):  
  https://www.youtube.com/watch?v=r1bpod1VWhg

**Turn on indexing for the workspace**

1. Open the repository as the **workspace root** in VS Code.
2. In Zoo Code, open the **codebase indexing** sidebar from the database icon in the panel.
3. Enable **Index this workspace** or the equivalent toggle.
4. If you change indexing provider settings, disable indexing, save, re-enable it, save again, and then click **Start Indexing** to rebuild.

Zoo Code will chunk files, compute embeddings, and store them in a local index so it can retrieve relevant code more precisely.

**Choose embedding provider**

In Zoo Code **Settings → Code Indexing**:

- Set **Embedding provider** to:
  - **Ollama**, if your build supports it, using base URL `http://localhost:11434`; or
  - A cloud provider such as OpenAI if you are comfortable sending embeddings externally.

Relevant Roo Code feature discussions:

- https://github.com/RooCodeInc/Roo-Code/issues/4869
- https://github.com/RooCodeInc/Roo-Code/issues/3959

If Ollama does not appear as an embedding provider in your Zoo Code build, use the default supported provider for now and revisit this setting later.

**Ignore patterns (`.rooignore`)**

Create a `.rooignore` file at the repository root to keep indexing focused:

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

Zoo Code uses `.rooignore` to reduce noisy suggestions and keep irrelevant generated content out of the index.

- Release notes referencing `.rooignore`: https://docs.zoocode.dev/update-notes/v3.8

**Practical pre-filtering during use**

When prompting, prefer scoped requests with `@file` or `@folder` mentions. Example:

- “For this task, focus only on files in `@product-service/` and its tests.”

Zoo Code will then:

- Prefer those locations in file discovery and semantic search.
- Pull partial snippets instead of entire files into model context.

`list_files` tool docs: https://docs.zoocode.dev/advanced-usage/available-tools/list-files

This gives you both index-time and prompt-time context narrowing.

## Conversation Context Management

Long sessions can hit context limits or gradually degrade in quality. Zoo Code includes **Intelligent Context Condensing** and guidance for avoiding context poisoning.

**Intelligent Context Condensing**

- Docs: https://docs.zoocode.dev/features/intelligent-context-condensing

Key points:

- It is enabled by default under Zoo Code **Context Management** settings.
- As you approach the model’s context limit, Zoo Code condenses earlier conversation history into a shorter working summary.
- The goal is to preserve important facts, decisions, and instructions while freeing tokens for continued work.

You can customize the condense prompt to preserve project-specific details such as migration constraints, acceptance criteria, or service names.

**Avoiding context poisoning**

- Docs: https://docs.zoocode.dev/advanced-usage/context-poisoning

Recommended practices:

- Treat each ticket, migration step, or major implementation task as its own Zoo Code session.
- If the assistant becomes inconsistent or clearly wrong, start a fresh session instead of trying to repair a polluted one.
- Keep prompts tightly scoped, and use indexing plus `@file` references to reintroduce the exact code and context needed.

## Recommended Usage

**Practical operating pattern:**

1. Start in `Architect` with `Ollama – Planning (Balanced)`.
2. Escalate to `Ollama – Planning (Deep)` for migrations, zero-downtime changes, and failure-mode analysis.
3. Implement in `Code` with `Ollama – Coding (Fast)`.
4. Re-run difficult or safety-critical code tasks with `Ollama – Coding (Safety)`.
5. Use `Ollama – Coding (Reference)` when you want a second implementation style for comparison.

    This keeps the default workflow responsive while still preserving access to deeper planning and stricter coding profiles when needed.

## Usage Checklist

1. Install VS Code, Zoo Code, and Ollama.
2. Pull all selected models:
   - `mistral-nemo:12b`
   - `llama3.1:8b`
   - `phi4-mini:latest`
   - `qwen2.5-coder:7b`
   - `qwen3-coder:30b`
   - `codestral:22b`
3. Start Ollama and verify the local API.
4. Open the repository in VS Code.
5. Create all seven Zoo Code profiles.
6. Assign default mode mappings.
7. Pin the most-used profiles near the top of the profile selector.
8. Test one planning prompt and one coding prompt against the intended profiles before standardizing team usage.