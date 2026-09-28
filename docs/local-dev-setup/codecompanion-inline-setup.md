**CodeCompanion Inline with Ollama and OpenCode (HTTP)**

<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->
**Table of Contents**  *generated with [DocToc](https://github.com/thlorenz/doctoc)*

- [Recommended Ollama models for Go development](#recommended-ollama-models-for-go-development)
- [1. Requirements and assumptions](#1-requirements-and-assumptions)
- [2. Extending the config: HTTP adapters](#2-extending-the-config-http-adapters)
  - [2.1 Add an Ollama HTTP adapter](#21-add-an-ollama-http-adapter)
  - [2.2 Optional: OpenCode HTTP adapter for inline](#22-optional-opencode-http-adapter-for-inline)
- [3. Using inline interaction with Ollama](#3-using-inline-interaction-with-ollama)
  - [3.1 Basic inline workflow](#31-basic-inline-workflow)
  - [3.2 Verifying Ollama is used](#32-verifying-ollama-is-used)
- [4. Switching inline models and adapters on the fly](#4-switching-inline-models-and-adapters-on-the-fly)
  - [4.1 Change model with `adapter=` argument](#41-change-model-with-adapter-argument)
  - [4.2 Using keymaps (optional)](#42-using-keymaps-optional)
- [5. Inline tests in a Go project](#5-inline-tests-in-a-go-project)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

This document continues the **CodeCompanion Setup for Chat with OpenCode ACP** guide and shows how to:

- Configure **inline** interaction via a local **Ollama** HTTP adapter.
- Test inline editing in a Go project.
- Optionally, use **OpenCode** in inline mode via HTTP.

## Recommended Ollama models for Go development

Given local dev system specs:

- 64 GiB RAM
- Intel® Core™ i7-14650HX × 24 CPUs (8 P-cores, 8 E-cores)
- NVIDIA GeForce RTX™ 4070 Laptop GPU
- 1 TB SSD

mid-size coding models can be run comfortably locally.

From given `ollama list`, good candidates for **Go-focused development** are:

- **Primary choice:** `deepseek-coder-v2:16b`
  Strong code-specialized model with good reasoning; 8.9 GB is fine on this hardware.
- **Secondary choice (heavier):** `gemma4:12b`
  Strong at coding and agentic workflows; acceptable latency for inline refactors and test generation.
- **Secondary choice (lighter):** `qwen2.5-coder:7b`
  Smaller, faster, still code-tuned; good when you want low latency.
- **Fallback / general-purpose:** `llama3.1:8b`
  Great general model; use it for mixed tasks (docs, planning) if you want something non-coder-specific.

In the inline config below, `deepseek-coder-v2:16b` is used as the default, with `qwen2.5-coder:7b` and `llama3.1:8b` listed as alternatives.

## Requirements and Assumptions

This continuation assumes you already have:

- Neovim 0.11+ with `lazy.nvim` configured.
- CodeCompanion working in **chat** mode with OpenCode ACP (per the first guide).
- `nvim-treesitter` installed with Go and Markdown parsers.
- **Ollama** running locally (default: `http://localhost:11434`).

For OpenCode inline, you also need an HTTP API endpoint compatible with CodeCompanion’s `openai_compatible` adapter (for example an OpenCode HTTP endpoint exposed via their CLI or gateway).

## Extend Config: HTTP adapters

Open `~/.config/nvim/lua/plugins/codecompanion.lua` and extend the `opts` table to add HTTP adapters and inline interaction.

Start from your existing chat config:

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

### Add Ollama HTTP adapter

Append an `adapters.http` block that extends the built-in `ollama` adapter and configure inline to use it:

```lua
return {
  "olimorris/codecompanion.nvim",
  dependencies = {
    "nvim-lua/plenary.nvim",
    "nvim-treesitter/nvim-treesitter",
  },
  opts = {
    adapters = {
      http = {
        ollama_local = function()
          return require("codecompanion.adapters").extend("ollama", {
            env = {
              -- Use local Ollama; override with OLLAMA_HOST if needed
              url = os.getenv("OLLAMA_HOST") or "http://localhost:11434",
            },
            schema = {
              model = {
                -- Primary recommendation for Go dev
                default = "deepseek-coder-v2:16b",
                -- Optional choices; CodeCompanion will show these in pickers
                choices = {
                  "deepseek-coder-v2:16b",
                  "qwen2.5-coder:7b",
                  "llama3.1:8b",
                  "gemma4:12b",
                },
              },
              num_ctx = {
                default = 4096,
              },
              temperature = {
                default = 0.2,
              },
            },
          })
        end,
      },
    },

    interactions = {
      chat = {
        adapter = {
          name = "opencode",
          model = "glm-5.2",
        },
      },

      inline = {
        adapter = {
          name = "ollama_local",
          model = "deepseek-coder-v2:16b",
        },
      },
    },

    opts = {
      log_level = "DEBUG",
    },
  },
}
```


Notes:

- Only **HTTP** adapters are allowed for the `inline` interaction; ACP (OpenCode chat) cannot be used for inline.
- `ollama_local` is an arbitrary name for this preset; the base adapter is `"ollama"` from CodeCompanion’s HTTP implementations.

### Optional: Alternative OpenCode HTTP Adapter

If you want an inline option that uses **OpenCode** instead of Ollama (for example via an OpenCode HTTP endpoint that follows an OpenAI-compatible API), you can add a second HTTP adapter.

Example using an OpenCode HTTP endpoint that speaks OpenAI-compatible JSON:

```lua
    adapters = {
      http = {
        ollama_local = function()
          -- ... as above ...
        end,

        opencode_http = function()
          return require("codecompanion.adapters").extend("openai_compatible", {
            env = {
              -- Replace with your OpenCode HTTP base URL
              url = "https://YOUR-OPENCODE-HTTP-ENDPOINT",
              api_key = "OPENCODE_API_KEY",
            },
            headers = {
              ["Content-Type"] = "application/json",
              ["Authorization"] = "Bearer ${api_key}",
            },
            schema = {
              model = {
                -- For example: an OpenCode Go HTTP model ID
                default = "opencode-go/glm-5.2",
              },
            },
          })
        end,
      },
    },
```

Then you can either:

- Make **OpenCode** the default inline adapter:

  ```lua
  inline = {
    adapter = {
      name = "opencode_http",
      model = "opencode-go/glm-5.2",
    },
  },
  ```

- Or keep Ollama as the default and override the adapter per prompt (see section 4).

The exact URL/model string depends on how OpenCode exposes HTTP; the pattern is the same as other `openai_compatible` integrations.

## Use Inline Interaction with Ollama

Once the config is in place, restart Neovim and execute the following command to verify that `inline` is configured and the `ollama_local` adapter is available.

```vim
:checkhealth codecompanion
```

### Basic Inline Workflow

Inline interaction uses the `:CodeCompanion` command (no `Chat` suffix). Common patterns:

1. **Inline suggestion in visual selection:**

    Select some Go code in **visual** mode and execute the following command:
   
    ```vim
    :'<,'>CodeCompanion improve this error handling
    ```

    CodeCompanion sends the selection plus your prompt to the configured inline adapter (`ollama_local` with `deepseek-coder-v2:16b`) and applies the edits directly in the buffer.

2. **Inline refactor of current function (normal mode):**

    ```vim
    :CodeCompanion refactor this function to be more idiomatic Go
    ```
    
    The inline assistant classifies the scope (current function / block) and edits in place.

### Verify Ollama is Used

You can confirm Ollama is actually serving inline requests by:

- Watching Ollama logs in a separate terminal by looking for requests with `model: "deepseek-coder-v2:16b"` (or `qwen2.5-coder:7b`).
  :

  ```bash
  ollama serve
  ```

- Temporarily switching the inline model to something else and observing changes in style or latency.

## Switch Inline Models and Adapters on the Fly

Inline supports overriding the adapter and model in the command line per prompt.

### 4.1 Change model with `adapter=` argument

Example: use the lighter `qwen2.5-coder:7b` model via `ollama_local` for a particular refactor:

```vim
:'<,'>CodeCompanion adapter=ollama_local model=qwen2.5-coder:7b refactor this function for readability
```

If you want to use OpenCode HTTP inline instead of Ollama for one operation:

```vim
:'<,'>CodeCompanion adapter=opencode_http model=opencode-go/glm-5.2 help me design a safer error-handling pattern
```

These per-command overrides do **not** change your default config; they apply only to that inline request.

### 4.2 Using keymaps (optional)

You can add convenience mappings in your Neovim config, for example:

```lua
vim.keymap.set("v", "<leader>ci", ":CodeCompanion inline<CR>", { desc = "Inline CodeCompanion (default adapter)" })
vim.keymap.set("v", "<leader>co", ":<C-u>'<,'>CodeCompanion adapter=ollama_local <Space>", { desc = "Inline with Ollama" })
vim.keymap.set("v", "<leader>cg", ":<C-u>'<,'>CodeCompanion adapter=opencode_http <Space>", { desc = "Inline with OpenCode HTTP" })
```

These mappings are optional but make it easier to switch adapters without retyping long commands.

## 5. Inline tests in a Go project

Use the same reference repository as in the chat guide (`campsite-booking-go`) and run a few focused inline tests:

1. **Generate tests from a function:**

    - Select a Go function.
    - Run:

      ```vim
      :'<,'>CodeCompanion adapter=ollama_local model=deepseek-coder-v2:16b write table-driven tests for this function
      ```

2. **Improve error handling:**

   ```vim
   :'<,'>CodeCompanion adapter=ollama_local harden error handling for this block, prefer wrapping errors with context
   ```

3. **Ask OpenCode inline for design advice:**

   ```vim
   :'<,'>CodeCompanion adapter=opencode_http model=opencode-go/glm-5.2 propose a more robust package layout for this service
   ```

4. **Validate behavior:**

    - Inspect the diff to ensure changes are reasonable.
    - Run Go tests in the terminal to confirm the generated code compiles and behaves as expected.

If these operations work end-to-end (inline edits applied, models responding quickly), your **Inline + Ollama + OpenCode** setup is complete.