# CodeCompanion.nvim for Go Development — Evaluation

> Context: migrating from IntelliJ AI Assistant (Chat + Agent mode with Codex) to NeoVim + CodeCompanion, with **OpenCode Go** as the AI coding subscription.

## The Five CodeCompanion Interactions

CodeCompanion organizes every way of talking to an LLM or agent into five **interactions**, each independently configurable with its own adapter.


| Interaction | Command              | What it does                                                                                                |
| ----------- | -------------------- | ----------------------------------------------------------------------------------------------------------- |
| Chat        | `:CodeCompanionChat` | Dedicated conversational buffer (`ft=codecompanion`) with multi-turn dialogue, tools, and context injection |
| CLI         | `:CodeCompanionCLI`  | Terminal wrapper around agent CLI tools like OpenCode, Claude Code, and Codex                               |
| Inline      | `:CodeCompanion`     | LLM writes code directly into the current buffer (with diff approval)                                       |
| Cmd         | `:CodeCompanionCmd`  | Generates Neovim command-line commands from natural language                                                |
| Background  | automatic            | Silent tasks like chat title generation and message compaction (opt-in)                                     |

### Chat and CLI Interactions Comparison

The core difference: **Chat is a structured conversation managed by CodeCompanion, while CLI is a wrapper around an external agent program that CodeCompanion merely pipes prompts into.** They communicate with the AI through entirely different mechanisms, which changes everything about how context, capabilities, and editing work .


| Aspect                | Chat (`:CodeCompanionChat`)                | CLI (`:CodeCompanionCLI`)                                                          |
| --------------------- | ------------------------------------------ | ---------------------------------------------------------------------------------- |
| Protocol              | ACP (JSON-RPC)                             | Terminal stdin/stdout — no protocol                                               |
| Buffer                | Custom chat buffer (`ft=codecompanion`)    | Terminal buffer (`ft=codecompanion_cli`)                                           |
| Capabilities          | Defined by CodeCompanion's tools/MCP       | Whatever the agent natively does (shell access, filesystem)                        |
| Context injection     | Inserted into system/user messages         | Prepended as text to the agent's stdin stream                                      |
| File sync after edits | Manual or via tool handlers                | Automatic (`watch.lua` + `checktime` — buffer reloads when the agent edits files) |
| Turn model            | Turn-based: you send, LLM responds, repeat | Agent runs autonomously, streaming output continuously                             |

**What that means in practice**:

**Chat** is CodeCompanion's own UI. Your message is parsed with treesitter, assembled into a structured request (system prompt + messages + tool definitions), and sent over HTTP or ACP; the response streams back into the markdown-formatted buffer. The LLM can only "do" things through tools CodeCompanion exposes — `@{cmd_runner}` for shell commands, `@{read_file}` for file access, etc. Without those tools, it's a pure conversation. 

**CLI** spawns the actual `opencode` process inside a Neovim terminal. CodeCompanion doesn't parse the conversation at all — it just injects your editor context (buffers, visual selections, `#{diagnostics}`, `@path` file references) into the prompt text and sends it to the agent's stdin. The agent then works with its *own* native capabilities: reading your whole repo, running `go build`, editing files directly on disk. Because the agent modifies the filesystem itself, CodeCompanion watches for file changes and reloads affected buffers automatically .

The reason the CLI interaction exists at all: sharing context with a terminal agent is normally painful — you'd switch to the CLI, type `@`, hunt for the file, or copy-paste a snippet. CodeCompanion removes that friction without taking over the agent's job .

**In IntelliJ terms**:

- **Chat ≈ IntelliJ's Chat mode**: structured Q&A, the LLM answers and can act only through the plugin's sanctioned tools.
- **CLI ≈ IntelliJ's Agent mode**: the agent (your OpenCode Go) has free rein — shell, filesystem, the works — CodeCompanion just makes feeding it context effortless.

And since OpenCode supports both paths (ACP in chat *and* as a CLI agent), the practical rule of thumb is: use **chat** for discussion, explanation, and tool-bounded edits; use **CLI** when you want OpenCode to autonomously implement, refactor, and run your Go toolchain — the Codex-agent experience you had in IntelliJ.

### Architecture Notes

- **Adapters come in two types**: HTTP adapters connect you to an LLM API (Anthropic, OpenAI, Ollama, Copilot…), while ACP adapters connect you to a stateful agent via the Agent Client Protocol. ACP adapters work only in the chat interaction.
- **Chat buffer power features**:
  - *Editor Context* via `#` (e.g. `#{buffer}`, `#{diagnostics}`)
  - *Slash Commands* via `/` to inject context
  - *Tools* via `@` (e.g. `@{grep_search}`) that let the LLM act agentically
  - Since v19, a dedicated agent mode via the `@{agent}` group, which swaps in a curated toolset and system prompt for autonomous coding behavior
- The default adapter is GitHub Copilot; with `copilot.vim`/`copilot.lua` installed, CodeCompanion works out of the box.

---

## Mapping from IntelliJ AI Assistant

### Chat mode → `:CodeCompanionChat`

Direct equivalent of the IntelliJ AI Assistant chat panel.

- Open with `:CodeCompanionChat`; toggle with `:CodeCompanionChat Toggle`
- Send prompts with `c-s` (insert mode) or `cr` (normal mode)
- Switch adapters mid-chat with `ga`
- Add visually selected code with `:CodeCompanionChat Add`
- Review everything the LLM changed with `:CodeCompanionChat Changes`

### Agent mode (Codex in IntelliJ) → OpenCode via CLI or ACP

Since OpenCode supports both integration paths in CodeCompanion, there are two options — pick one or use both.

#### Option 1: CLI interaction (recommended for IntelliJ Codex parity)

Runs the real `opencode` binary in a Neovim terminal with full editor-context injection (`#{buffer}`, `#{diagnostics}`, `#{terminal}`, visual selections). OpenCode natively understands `@path` references inserted by CodeCompanion.

```lua
require("codecompanion").setup({
  interactions = {
    cli = {
      agent = "opencode",  -- or override ad-hoc: :CodeCompanionCLI agent=opencode 
<prompt>
    },
  },
})
```

Authentication: once you've run `/connect` → **OpenCode Go** in the OpenCode TUI and pasted your API key, the CLI interaction inherits that auth — no extra CodeCompanion-side key config needed.

**Daily CLI workflow:**

- `:CodeCompanionCLI <prompt>` — send a prompt to OpenCode (review before submitting)
- `:CodeCompanionCLI! <prompt>` — auto-submit, fire-and-forget, cursor stays put
- `:CodeCompanionCLI Ask` — rich input buffer with editor context and slash commands (`:w` sends, `:w!` sends and submits)
- Visual-select Go code, then `:CodeCompanionCLI Can you refactor this?` — the selection goes to OpenCode automatically

**Go-workflow keymaps:**

```lua
-- Send gopls diagnostics to OpenCode and auto-submit
vim.keymap.set("n", "<localleader>cd", function()
  return require("codecompanion").cli("#{diagnostics} Can you fix these?", { focus = false, submit = true })
end, { desc = "Send diagnostics to OpenCode" })

-- Share failing `go test` output from the terminal
vim.keymap.set("n", "<localleader>ct", function()
  return require("codecompanion").cli("#{terminal} Fix these test failures.", { focus = false, submit = true })
end, { desc = "Send terminal output to OpenCode" })
```

#### Option 2: ACP adapter in chat

OpenCode speaks the Agent Client Protocol (`opencode acp`), so it can drive the chat buffer with agent behavior in the CodeCompanion UI instead of a terminal.

```lua
require("codecompanion").setup({
  interactions = {
    chat = {
      adapter = {
        name = "opencode",
        model = "glm-5.1",  -- or any model your Go plan exposes
      },
    },
  },
})
```

- Since v18.4.0, switch ACP models on the fly with `ga` in the chat buffer (handy for hopping between GLM, Kimi, DeepSeek under the Go subscription)
- Custom models can also be pinned in `~/.config/opencode/config.json`

### Bonus: Inline (`:CodeCompanion`) for quick edits

No strict IntelliJ AI Assistant equivalent, but covers the "select code → ask AI to fix/explain" micro-workflow.

```lua
-- Presets: /fix, /explain, /tests, /lsp
-- Example on a visual selection:
:'<,'>CodeCompanion /tests   -- great for Go table-driven tests
```

---

## OpenCode Go

### Plan Facts

- **\$5 first month, then $10/month**, cancel anytime
- One API key routed to ~14 open-weight coding models: GLM-5.1/GLM-5, Kimi K2.5/K2.6, DeepSeek V4 Pro/Flash, Qwen3.7 Max/Plus, MiniMax M2.5/M2.7/M3, MiMo-V2.5/Pro
- Works inside OpenCode or **any other agent** (one key, or bring your own — 75+ providers supported)
- Rolling usage caps: ~\$12/5h, \$30/week, $60/month
- Trade-off: models are all from Chinese AI labs — no Claude/GPT/Gemini in the Go plan; bring your own provider keys if you need those

### Models Comparison for Go Development

Among the models in your list, **GLM‑5.2 is the best fit overall for Go development** if your goal is high‑quality, long‑horizon coding and agentic workflows.

**Why GLM‑5.2 stands out**:

- **Explicitly “coding‑first”**: GLM‑5.2 is described by Zhipu/Z.ai as an open‑weight mixture‑of‑experts model built specifically for long‑horizon coding and agentic work, not just general chat.
- **Top coding benchmarks among open models**: It leads open‑weights on SWE‑bench Pro and Terminal‑Bench, beating previous GLM‑5.1 and even GPT‑5.x–class baselines on those coding benchmarks.
- **1M‑token context for large Go codebases**: It has a 1M‑token context window with efficient sparse attention, letting it hold project‑scale repositories and multi‑file Go refactors in a single run.
- **Tool calling and agent support**: The API supports tool calling, structured outputs, and adjustable reasoning, which aligns well with agentic IDE setups like CodeCompanion + OpenCode.

All of that makes it a strong default choice for Go development: it’s tuned for coding, proven on code benchmarks, and engineered for long, multi‑step engineering tasks.

**How other models compare for Go work**:

Here’s how the main coding‑oriented models in your list stack up, relative to GLM‑5.2:


| Model                      | Coding focus                               | Context window | Notable strengths vs GLM‑5.2                                                                                                                                                               |
| -------------------------- | ------------------------------------------ | -------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **GLM‑5.2**               | Coding‑first, agentic                     | 1M             | Best open coding benchmarks; long‑horizon coding/agents.                                                                                                                                   |
| **GLM‑5.1**               | Coding‑first, long‑horizon               | 200K           | Very strong coding, but smaller context and slightly older than 5.2.                                                                                                                        |
| **DeepSeek V4 Pro**        | Frontier coding & reasoning                | 1M             | Extremely high coding scores (SWE‑bench Verified ~80.6, LiveCodeBench 93.5); great for competitive programming and complex reasoning; more “max‑reasoning” than strictly coding‑tuned. |
| **Kimi K2.7 Code**         | Dedicated coding/agentic model             | 256K           | 1T MoE coding model with strong long‑horizon coding benchmarks and 256K context; excellent for multi‑step Go agents, slightly smaller context than GLM‑5.2.                              |
| **Kimi K3**                | Frontier agentic coding, especially web/UI | 1M             | 2.8T MoE; #1 open model on web‑engineering/coding arena, extremely strong for front‑end and UI; more specialized and heavy than you likely need for typical Go backend work.              |
| **Qwen 3.8 Max**           | Long‑horizon engineering/agents           | 1M             | Designed to hold multi‑day engineering tasks; strong agentic coding and terminal benchmarks, but “mid‑pack” on SWE‑bench Pro versus more coding‑specialized models.                   |
| **MiniMax M2.7**           | Agentic coding at low cost                 | ~204K          | Nearly Claude‑level coding at much lower cost; good for cost‑sensitive coding, but not the absolute top performer.                                                                        |
| **MiMo‑V2.5 / V2.5‑Pro** | Open agentic coding                        | up to 1M       | Open‑weight MoE models tuned for long‑horizon agentic coding; strong, but positioned as cost‑efficient Claude‑adjacent rather than clearly ahead of GLM‑5.2.                           |
| **Grok 4.5**               | Proprietary frontier coding & agents       | 500K           | Very strong coding/agent use with good integration in Grok Build/Cursor; proprietary and ecosystem‑specific, not open‑weights.                                                            |
| **GPT‑5.6 Luna**          | Cost‑efficient general frontier model     | ~1M            | Good coding scores and large context, but tuned for cost‑sensitive, high‑volume workloads rather than peak coding performance; more “cheap generalist” than coding specialist.          |
| **DeepSeek V4 Flash**      | Fast, cheap coding                         | 1M             | Latency‑optimized sibling to V4 Pro; great for quick code generation and tests, but below Pro on hard coding benchmarks.                                                                   |

For day‑to‑day Go backend development (services, handlers, tests, refactors) and agentic coding inside tools like CodeCompanion/OpenCode, **GLM‑5.2 gives you the best balance of:**

- Top‑tier **coding accuracy** on established benchmarks.
- **Open weights** and broad tooling support.
- **1M context** for whole‑repo Go work.

A second model can be added later:

- Pair **DeepSeek V4 Pro** for the most demanding reasoning‑heavy tasks (complex optimisation, algorithms, competitive‑style problems).
- Or **Kimi K2.7 Code** as a strong, coding‑focused alternative with excellent long‑horizon software‑engineering performance.

## Recommended Starting Setup

For a Go dev environment:

- **CLI interaction** → OpenCode (parity with IntelliJ Codex agent)
- **Chat interaction** → HTTP adapter (Copilot works out of the box, or Ollama for local models), or OpenCode via ACP for conversational Q&A
- **Inline interaction** → `/fix`, `/explain`, `/tests` presets for quick edits
- Cycle between chat and CLI buffers with `{` and `}`
- Toggle either interaction with `require("codecompanion").toggle()` — same muscle memory as the IntelliJ AI tool window

**Recommended keymaps:**

- `<c-a>` — Action Palette
- `<localleader>a` — toggle chat buffer
- `ga` (visual mode) — add selection to chat
- `<localleader>cd` / `<localleader>ct` — send diagnostics / terminal output to OpenCode

### Important Caveat

CodeCompanion is not an LSP — keep your gopls setup (via nvim-lspconfig or your distro) separate. CodeCompanion's `#{diagnostics}` context builds on top of it.

---

## Sources

- [CodeCompanion Getting Started](https://codecompanion.olimorris.dev/getting-started)
- [CodeCompanion CLI interaction docs](https://codecompanion.olimorris.dev/usage/cli)
- [CodeCompanion Inline interaction docs](https://codecompanion.olimorris.dev/usage/inline)
- [CodeCompanion site](https://codecompanion.olimorris.dev/)
- [codecompanion.nvim help file (codecompanion.txt)](https://raw.githubusercontent.com/olimorris/codecompanion.nvim/refs/heads/main/doc/codecompanion.txt)
- [DeepWiki: Interaction Types](https://deepwiki.com/olimorris/codecompanion.nvim/3.1-interaction-types)
- [DeepWiki: CLI Interaction](https://deepwiki.com/olimorris/codecompanion.nvim/7.2-cli-interaction)
- [codecompanion.nvim v19.0.0 release notes](https://newreleases.io/project/github/olimorris/codecompanion.nvim/release/v19.0.0)
- [codecompanion.nvim v18.4.0 — ACP model selection](https://github.com/olimorris/codecompanion.nvim/discussions/2643)
- [OpenCode Go plan](https://opencode.ai/go)
- [OpenCode Go docs](https://opencode.ai/docs/go/)
- [OpenCode ACP docs](https://open-code.ai/en/docs/acp)
- [OpenCode review / plan comparison](https://dev.to/jovan_chan_9500711396d4e6/opencode-review-2026-the-open-source-terminal-coding-agent-that-challenges-claude-code-5a89)
