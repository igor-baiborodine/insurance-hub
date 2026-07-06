> This document defines a practical local AI development configuration for this repository using **Continue** with **Ollama**, including the selected models, the canonical `~/.continue/config.yaml` configuration, and the recommended day-to-day usage pattern.

## Prerequisites

**Hardware and OS**

- Development laptop with:
  - 64 GB RAM
  - Intel i7-14650HX or similar multi-core CPU
  - NVIDIA RTX 4070 Laptop GPU or comparable GPU
  - Ubuntu 24.04 LTS or similar 64-bit Linux

**Required software**

- VS Code
- Continue VS Code extension
- Ollama, installed and available on `PATH`
- Local model pulls for every selected model listed below

Recommended verification commands:

```bash
ollama ls
ollama serve
curl http://localhost:11434/api/tags
```

## Selected Local Models

This repository currently standardizes on four local Ollama models for Continue:

- `qwen3-coder:30b` for primary coding and more demanding implementation tasks
- `qwen2.5-coder:7b` for autocomplete
- `mistral-nemo` for planning and broader reasoning
- `llama3.1:8b` as an alternate general-purpose local model

## Continue Configuration

Continue should be configured through `~/.continue/config.yaml`.

Create or update that file with the following content:

```yaml
name: Main Config
version: 1.0.0
schema: v1
models:
  - name: Qwen3 Coder 30B
    provider: ollama
    model: qwen3-coder:30b
    capabilities:
      - tool_use
  - name: Qwen2.5 Coder 7B
    provider: ollama
    model: qwen2.5-coder:7b
    roles:
      - autocomplete
    capabilities:
      - tool_use
  - name: Mistral Nemo
    provider: ollama
    model: mistral-nemo
    capabilities:
      - tool_use
  - name: Llama3.1 8B
    provider: ollama
    model: llama3.1:8b
    capabilities:
      - tool_use
```

This configuration establishes:

- `Qwen3 Coder 30B` as the main coding model
- `Qwen2.5 Coder 7B` as the autocomplete model
- `Mistral Nemo` as the primary planning model
- `Llama3.1 8B` as a lighter alternate chat and planning model

## Setup Steps

1. Install VS Code, the Continue extension, and Ollama.
2. Pull the required local models:

   ```bash
   ollama pull qwen3-coder:30b
   ollama pull qwen2.5-coder:7b
   ollama pull mistral-nemo
   ollama pull llama3.1:8b
   ```

3. Create or update `~/.continue/config.yaml` with the configuration above.
4. Restart VS Code if Continue was already open during configuration.
5. Open this repository as the workspace root in VS Code.

## Verification

Use these checks to confirm that the local Continue setup is working:

1. Verify Ollama is serving models:

   ```bash
   ollama ls
   curl http://localhost:11434/api/tags
   ```

2. Open Continue in VS Code and confirm the configured models appear in the model selector.
3. Confirm autocomplete is backed by `Qwen2.5 Coder 7B`.
4. Run one short planning prompt with `Mistral Nemo`.
5. Run one implementation-oriented prompt with `Qwen3 Coder 30B`.

## Repository Guidance Integration

This repository keeps AI workflow guidance in repository-owned files rather than in the Continue config itself.

Use these files during Continue sessions:

- `AGENTS.md` as the repository-wide entry point
- `CONTINUE.md` as the thin Continue-facing pointer to the canonical instructions
- `.continue/rules/CONTINUE.md` as the Continue rules adapter
- `ai/` for shared rules, workflows, prompts, templates, and examples

For ticket-driven work, follow the spec-first workflow defined in the repository guidance rather than embedding process rules in `~/.continue/config.yaml`.

## Recommended Usage

Use the models with this default intent:

- `Mistral Nemo` for planning, decomposition, migration reasoning, and requirement clarification
- `Qwen3 Coder 30B` for primary implementation, refactoring, and code-sensitive tasks
- `Qwen2.5 Coder 7B` for autocomplete
- `Llama3.1 8B` for quick alternate reasoning or lighter prompts

Treat each ticket, migration step, or major implementation task as its own Continue session when possible. For better context quality, keep prompts scoped and reference relevant files or folders directly.

## Troubleshooting

- If models do not appear in Continue, confirm Ollama is running and the model names in `~/.continue/config.yaml` exactly match `ollama ls`.
- If Continue behaves as if no repository guidance exists, verify that the repository includes `AGENTS.md`, `CONTINUE.md`, and `.continue/rules/CONTINUE.md`.
- If autocomplete is missing or slow, confirm that `Qwen2.5 Coder 7B` is present locally and marked with the `autocomplete` role in the config.
- If a model startup is slow, wait for the first load to complete before judging steady-state responsiveness.
