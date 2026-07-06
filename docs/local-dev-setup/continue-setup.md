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

## Context Providers

Continue supports built-in context providers that appear when typing `@` in chat. For day-to-day repository work, the most useful built-in providers are:

- `@File` for referencing a specific file in the workspace
- `@Code` for referencing functions or classes across the project
- `@Current File` for the file currently open in the editor
- `@Terminal` for the last terminal command and its output
- `@Open` for currently open files
- `@Problems` for diagnostics from the current file
- `@Git Diff` for reviewing current branch changes
- `@Repository Map` for a codebase outline when broader structural context is needed

For large repositories, `@Repository Map` can be configured with `includeSignatures: false` to reduce context size.

For context beyond the built-in providers, prefer MCP servers. Continue supports MCP-backed context, which is the recommended path for extending prompts, context, and tool use beyond the built-in providers.

If you need a small custom integration without a full MCP server, Continue also supports an HTTP context provider that makes a `POST` request and expects a `ContextItem` object or an array of `ContextItem` objects in the response.

Older non-built-in context providers are deprecated. When extending Continue for repository-specific external context, prefer MCP instead of relying on deprecated providers.

## MCP Servers

Continue supports MCP server integration through the `mcpServers` configuration and uses MCP in agent mode. For this repository, MCP is the preferred extension mechanism when Continue needs external tools, remote data, or repository-adjacent services beyond the built-in context providers. citeturn2view0

You can configure MCP servers in either of these ways:

- add an `mcpServers` block directly to `~/.continue/config.yaml`
- add standalone MCP config files under `.continue/mcpServers/` in the workspace

When using standalone files in `.continue/mcpServers/`, include the required metadata fields `name`, `version`, and `schema`. Continue also supports JSON MCP config files from tools such as Claude Desktop, Cursor, or Cline when those files are placed in `.continue/mcpServers/`. citeturn2view0

Each MCP server entry can define:

- `name` as the display name
- `type` as the transport type
- `command` and `args` for local process startup
- `env` for environment variables and secrets

Continue documents three MCP transport styles:

- `stdio` for local MCP processes
- `sse` for remote server-sent events connections
- `streamable-http` for HTTP-based streaming connections

For local development in this repository, prefer `stdio` unless there is a concrete reason to run a shared remote MCP service. citeturn2view0

If an MCP server requires credentials, use Continue secrets and inject them through `env` rather than hardcoding tokens into the configuration file. citeturn2view0

Example standalone MCP block file:

```yaml
name: Playwright MCP
version: 0.0.1
schema: v1
mcpServers:
  - name: Browser search
    type: stdio
    command: npx
    args:
      - "@playwright/mcp@latest"
```

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
