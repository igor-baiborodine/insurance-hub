# CodeCompanion Setup for Chat with OpenCode ACP

START doctoc generated TOC please keep comment here to allow auto update
DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE

- [Prerequisites](#prerequisites)
  - [Neovim and Go Setup](#neovim-and-go-setup)
  - [OpenCode Go Subscription](#opencode-go-subscription)
  - [OpenCode CLI](#opencode-cli)
  - [Shell Environment](#shell-environment)
- [Installing and Configuring CodeCompanion](#installing-and-configuring-codecompanion)
- [Using Chat in Practice](#using-chat-in-practice)
  - [Basic Chat Flow](#basic-chat-flow)
  - [Chat Context and Commands](#chat-context-and-commands)
  - [Reviewing Responses](#reviewing-responses)
- [Chat Walkthrough Test](#chat-walkthrough-test)

END doctoc generated TOC please keep comment here to allow auto update

This guide documents the first iteration of a hybrid CodeCompanion setup for NeoVim:

- **One interaction only**: Chat
- **One provider**: OpenCode via **ACP**
- **No inline interaction yet**

The goal is to keep the setup narrow and fully aligned with the official documentation. Later iterations can add Inline with an HTTP provider such as Ollama.

## Prerequisites

### Neovim and Go Setup

This guide assumes you already have:

- Neovim installed and working
- Your Go development environment configured
- A `lazy.nvim`-based configuration or LazyVim-style layout under `~/.config/nvim`

CodeCompanion requires:

- `curl`
- Neovim 0.11.0 or greater
- `nvim-treesitter` and a working parser setup for the plugin's markdown-based UI
- Optional but recommended: `ripgrep` and `fd` for surrounding Neovim tooling

Verify the installation later with:

```vim
:checkhealth codecompanion
```

### OpenCode Go Subscription

OpenCode Go is the subscription tier that gives you access to coding models through one API key. The official OpenCode docs describe Go as a low-cost plan with a first-month discount and recurring monthly pricing, plus usage caps and access to multiple coding models.

Before configuring CodeCompanion, make sure you have:

1. An OpenCode account.
2. An active OpenCode Go subscription.
3. Your OpenCode API key copied from the OpenCode dashboard.

### OpenCode CLI

Install the OpenCode CLI first, because ACP uses the `opencode` command to start the agent subprocess.

Use the official installer:

```bash
curl -fsSL https://opencode.ai/install | bash
```

Or install via Go if you prefer a source-based install:

```bash
go install github.com/opencode-ai/opencode@latest
```

Add `opencode` executable location to the `PATH` variable:
```bash
echo 'export PATH="$HOME/.opencode/bin:$PATH"' >> ~/.bashrc
source ~/.bashrc
hash -r
```

Verify it works:

```bash
opencode --version
opencode help
```

Then go to the reference project repository and connect it to OpenCode Go:

```bash
opencode
```

Inside the OpenCode TUI:

1. Run `/connect`.
2. Select `OpenCode Go`.
3. Paste your API key.

### Shell Environment

Add the OpenCode API key to your shell profile so the agent can pick it up consistently:

```bash
echo 'export OPENCODE_API_KEY="YOUR_OPENCODE_GO_API_KEY"' >> ~/.bashrc
source ~/.bashrc
```

You can confirm it is available in the current shell with:

```bash
echo "$OPENCODE_API_KEY"
```

## Installing and Configuring CodeCompanion

Install CodeCompanion through `lazy.nvim` and configure Chat to use OpenCode via ACP in a single plugin file.

Create `~/.config/nvim/lua/plugins/codecompanion.lua`:

```lua
return {
  "olimorris/codecompanion.nvim",
  dependencies = {
    "nvim-lua/plenary.nvim",
    "nvim-treesitter/nvim-treesitter",
  },
  opts = {
    interactions = {
      chat = {
        adapter = {
          name = "opencode",
          model = "glm-5.2",
        },
      },
    },
    opts = {
      log_level = "DEBUG",
    },
  },
}
```

Restart Neovim and allow Lazy to install the plugin dependencies.

After installation, check the plugin health:

```vim
:checkhealth codecompanion
```

## Using Chat in Practice

### Basic Chat Flow

Open a chat buffer with:

```vim
:CodeCompanionChat
```

Or open one and send a message immediately:

```vim
:CodeCompanionChat Explain this Go package structure
```

In the chat buffer:

- Type your prompt.
- Press `c-s` in insert mode or `cr` in normal mode to send it.
- Use `:CodeCompanionChat Toggle` to show or hide the buffer.

### Chat Context and Commands

Chat is the best place for broader discussion, planning, and agentic coding tasks. You can provide context from the editor and use the built-in chat commands documented by CodeCompanion:

- `#` for editor context
- `/` for prompt presets and commands
- `@` for tools and agent-style behavior

Examples:

```vim
:CodeCompanionChat #buffer explain this package layout
:CodeCompanionChat /explain
:CodeCompanionChat /tests
```

For Go work, `#buffer` and `/tests` are especially useful when generating or reviewing tests.

### Reviewing Responses

CodeCompanion stores chat as a dedicated buffer with turn-based responses, so you can keep a visible history of the conversation and revisit earlier instructions easily.

## Chat Walkthrough Test

Use the reference Go repository from `neovim-setup.md`: [`igor-baiborodine/campsite-booking-go`](https://github.com/igor-baiborodine/campsite-booking-go).

### 1. Clone and Open the Repository

```bash
git clone https://github.com/igor-baiborodine/campsite-booking-go.git
cd campsite-booking-go
nvim .
```

Confirm that the project opens correctly and that your Go tooling works as expected.

### 2. Open Chat and Ask for an Overview

Inside Neovim:

```vim
:CodeCompanionChat
```

Then ask something like:

```text
Give me a concise overview of this Go service architecture.
```

This is a good first check that the OpenCode ACP connection is working end-to-end.

### 3. Ask for Test Ideas

Use the current buffer as context:

```vim
:CodeCompanionChat #buffer suggest test cases for this package
```

You can also ask for a broader refactor plan:

```vim
:CodeCompanionChat #buffer propose a safer error-handling strategy
```

### 4. Validate Against the Repository Workflow

Use chat to guide the repository tasks you already perform in Neovim:

- Clarify a function's behavior.
- Ask for edge cases to cover.
- Draft a refactor plan before editing the code manually.
- Generate a checklist for validation steps after a change.

### 5. Confirm the Setup

If the chat responds correctly and can see repository context, your OpenCode ACP + CodeCompanion chat setup is working.

At that point, you have a clean baseline for the next iteration: add Inline later with an HTTP provider, and keep Chat on OpenCode ACP.

## Notes for the Next Iteration

This guide intentionally avoids Inline because CodeCompanion's official docs state that Inline supports **HTTP adapters only**, while OpenCode is used here as an **ACP chat agent**.

The next logical step is to add a second provider for Inline, such as Ollama, while keeping OpenCode for Chat.

## References

- CodeCompanion Getting Started: https://codecompanion.olimorris.dev/getting-started
- CodeCompanion Installation: https://codecompanion.olimorris.dev/installation
- CodeCompanion HTTP adapters: https://codecompanion.olimorris.dev/configuration/adapters-http
- CodeCompanion ACP adapters: https://codecompanion.olimorris.dev/configuration/adapters-acp
- CodeCompanion chat buffer: https://codecompanion.olimorris.dev/usage/chat-buffer/
- OpenCode ACP: https://opencode.ai/docs/acp/
- OpenCode Go: https://opencode.ai/go
- OpenCode Go docs: https://opencode.ai/docs/go/
- 