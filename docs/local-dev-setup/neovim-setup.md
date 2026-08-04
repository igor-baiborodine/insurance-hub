**Neovim Setup for Go Development on Ubuntu**

<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Prerequisites](#prerequisites)
  - [System Utilities & Compiler Tools](#system-utilities--compiler-tools)
  - [Go](#go)
  - [Lazygit](#lazygit)
  - [Nerd Font](#nerd-font)
- [Neovim on Ubuntu](#neovim-on-ubuntu)
  - [Option 1: Official Neovim Archive (Recommended)](#option-1-official-neovim-archive-recommended)
  - [Option 2: Ubuntu APT Package (Simplest, but often outdated)](#option-2-ubuntu-apt-package-simplest-but-often-outdated)
  - [Option 3: Snap](#option-3-snap)
- [LazyVim](#lazyvim)
- [Configuration & Customization](#configuration--customization)
  - [Custom Keymaps](#custom-keymaps)
  - [File Explorer (Snacks.nvim)](#file-explorer-snacksnvim)
  - [Solarized Theme (Optional)](#solarized-theme-optional)
  - [Autosave Support (Optional)](#autosave-support-optional)
- [Language & Tooling Extras](#language--tooling-extras)
  - [Go](#go-1)
  - [Markdown](#markdown)
  - [Git](#git)
  - [Makefile](#makefile)
- [Suggested Config Layout](#suggested-config-layout)
- [Useful Keybindings & Commands](#useful-keybindings--commands)
  - [Essential Navigation & Editing](#essential-navigation--editing)
  - [Go LSP Commands](#go-lsp-commands)
- [Alternative Standalone Neovim Setup (Optional)](#alternative-standalone-neovim-setup-optional)
- [Setup Walkthrough Test](#setup-walkthrough-test)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

The recommended path is to install Neovim and use **LazyVim** as the base configuration. LazyVim gives you an IDE-like setup without manually wiring every plugin: LSP, completion, diagnostics, formatting, Git integration, file search, syntax highlighting, and optional language-specific extras.

This guide provides a structured, step-by-step setup to transition from IntelliJ to a powerful terminal-based Go development environment.

## Prerequisites

Install the system tools, Go compiler, and Git utilities needed by LazyVim, Treesitter, plugin builders, search engines, and local workflows.

### System Utilities & Compiler Tools

```bash
sudo apt update
sudo apt install -y git build-essential curl unzip ripgrep fd-find make fzf
```

Ubuntu installs the `fd` tool as `fdfind`, while many Neovim plugins expect the executable name `fd`. Create a user-level symlink to resolve this:

```bash
mkdir -p ~/.local/bin
ln -sf "$(command -v fdfind)" ~/.local/bin/fd
echo 'export PATH="$PATH:$HOME/.local/bin"' >> ~/.bashrc
source ~/.bashrc
```

### Go

If Go is not installed yet, install it before configuring Go support in Neovim.

For the simplest Ubuntu-managed version:
```bash
sudo apt update
sudo apt install -y golang-go
go version
```

Ensure Go's binary directory is on your system `PATH` so tools installed via `go install` are executable:
```bash
echo 'export PATH="$HOME/go/bin:$PATH"' >> ~/.bashrc
source ~/.bashrc
```

### Lazygit

Lazygit provides a keyboard-driven Git terminal UI. LazyVim integrates with it seamlessly.

```bash
LAZYGIT_VERSION=$(curl -s "https://api.github.com/repos/jesseduffield/lazygit/releases/latest" | grep -Po '"tag_name": "v\K[^"]*')
curl -Lo lazygit.tar.gz "https://github.com/jesseduffield/lazygit/releases/latest/download/lazygit_${LAZYGIT_VERSION}_Linux_x86_64.tar.gz"
tar xf lazygit.tar.gz lazygit
sudo install lazygit /usr/local/bin
rm lazygit lazygit.tar.gz
```

### Nerd Font

LazyVim displays rich UI icons in file explorers, diagnostics, and status lines. These require a patched "Nerd Font" to render correctly.

1. **Install JetBrainsMono Nerd Font**

    ```bash
    wget https://github.com/ryanoasis/nerd-fonts/releases/latest/download/JetBrainsMono.zip
    unzip JetBrainsMono.zip -d JetBrainsMono
    mkdir -p ~/.local/share/fonts
    mv JetBrainsMono ~/.local/share/fonts/
    fc-cache -fv
    fc-list | grep "JetBrainsMono"
    rm JetBrainsMono.zip
    ```

2. **Set Font in Your Terminal Emulator**

    Open your terminal settings and choose the newly installed font:
    - **GNOME Terminal**: Preferences -> Profile -> Text -> Check "Custom font" -> Select `JetBrainsMono Nerd Font`.
    - **Konsole**: Settings -> Edit Current Profile -> Appearance -> Choose Font.
    - **Alacritty**: Update your `~/.config/alacritty/alacritty.toml`.
    - **Kitty**: Update your `~/.config/kitty/kitty.conf`.

## Neovim on Ubuntu

Choose **one** of the options below. We recommend **Option 1 (Official Archive)** as it ensures you get the latest Neovim features required by modern LazyVim plugins.

### Option 1: Official Neovim Archive (Recommended)
```bash
curl -LO https://github.com/neovim/neovim/releases/latest/download/nvim-linux-x86_64.tar.gz
sudo rm -rf /opt/nvim-linux-x86_64
sudo tar -C /opt -xzf nvim-linux-x86_64.tar.gz

echo 'export PATH="$PATH:/opt/nvim-linux-x86_64/bin"' >> ~/.bashrc
source ~/.bashrc
nvim --version
```

### Option 2: Ubuntu APT Package (Simplest, but often outdated)
```bash
sudo apt update
sudo apt install -y neovim
nvim --version
```

### Option 3: Snap
```bash
sudo snap install nvim --classic
nvim --version
```

## LazyVim

Back up any existing Neovim configuration first to prevent conflicts:
```bash
mv ~/.config/nvim ~/.config/nvim.bak 2>/dev/null || true
mv ~/.local/share/nvim ~/.local/share/nvim.bak 2>/dev/null || true
mv ~/.local/state/nvim ~/.local/state/nvim.bak 2>/dev/null || true
mv ~/.cache/nvim ~/.cache/nvim.bak 2>/dev/null || true
```

Clone the official LazyVim starter template:
```bash
git clone https://github.com/LazyVim/starter ~/.config/nvim
rm -rf ~/.config/nvim/.git
nvim
```
*On the first launch, Neovim will automatically install and configure its core plugin suite.*

Verify the setup's integrity inside Neovim:
```vim
:LazyHealth
:checkhealth
```

## Configuration & Customization

All user customization should be kept in specific configuration files under `~/.config/nvim/lua/` to avoid being overwritten during updates.

### Custom Keymaps
Custom keymaps should go in `~/.config/nvim/lua/config/keymaps.lua` (which already exists if you used the starter template).

Open the file:
```bash
nvim ~/.config/nvim/lua/config/keymaps.lua
```

Add your custom configurations (for example, setting `jk` to quickly exit modes):
```lua
-- Exit Insert mode quickly with jk
vim.keymap.set('i', 'jk', '<Esc>', {noremap = true, desc = "Exit Insert mode"})

-- Exit Terminal mode quickly with jk
vim.keymap.set('t', 'jk', [[<C-\><C-n>]], {noremap = true, desc = "Exit Terminal mode"})

-- Optional: Custom Lazygit shortcut mapping
vim.keymap.set("n", "<leader>gg", "<cmd>LazyGit<cr>", { desc = "LazyGit" })
```

### File Explorer (Snacks.nvim)
LazyVim uses `snacks.nvim` for its file explorer and picker. To permanently show hidden files (e.g., `.env`, `.github`) and files ignored by `.gitignore`:

Create `~/.config/nvim/lua/plugins/snacks.lua`:
```lua
return {
  "folke/snacks.nvim",
  opts = {
    picker = {
      sources = {
        explorer = {
          hidden = true,  -- Show hidden files like .env
          ignored = true, -- Show files ignored by Git
        },
      },
    },
  },
}
```

### Solarized Theme (Optional)
To use the Solarized color palette instead of the default theme, create `~/.config/nvim/lua/plugins/colorscheme.lua`:

```lua
return {
  'maxmx03/solarized.nvim',
  lazy = false,
  priority = 1000,
  ---@type solarized.config
  opts = {},
  config = function(_, opts)
    vim.o.termguicolors = true
    vim.o.background = 'light' -- or 'dark'
    require('solarized').setup(opts)
    vim.cmd.colorscheme 'solarized'
  end,
}
```
*Switch variations on the fly using `:set background=light` or `:set background=dark`.*

### Autosave Support (Optional)
To enable automatic background saving, create `~/.config/nvim/lua/plugins/autosave.lua`:

```lua
return {
  "okuuva/auto-save.nvim",
  version = "^1.0.0",
  event = { "InsertLeave", "TextChanged" },
  opts = {
    enabled = true,
    trigger_events = {
      immediate_save = { "BufLeave", "FocusLost", "QuitPre", "VimSuspend" },
      defer_save = { "InsertLeave", "TextChanged" },
      cancel_deferred_save = { "InsertEnter" },
    },
    condition = function(buf)
      local fn = vim.fn
      local utils = require("auto-save.utils.data")

      if fn.getbufvar(buf, "&modifiable") == 0 then
        return false
      end
      if utils.not_in(fn.getbufvar(buf, "&filetype"), {
        "oil",
        "neo-tree",
        "TelescopePrompt",
        "lazy",
        "mason",
        "toggleterm",
        "lazygit",
        "help",
      }) then
        return true
      end
      return false
    end,
    write_all_buffers = false,
    debounce_delay = 800,
  },
  keys = {
    { "<leader>ua", "<cmd>ASToggle<cr>", desc = "Toggle auto-save" },
  },
}
```

## Language & Tooling Extras

LazyVim manages IDE features for specific technologies through "extras".

### Go
Open Neovim and run:
```vim
:LazyExtras
```
Scroll to `lang.go`, press `x` to enable it, and restart Neovim. This automatically configures:
- **`gopls`**: The official Go language server.
- **Formating**: `goimports` and `gofumpt`.
- **Debugging & Testing**: `delve`, `nvim-dap-go`, and `neotest-golang`.
- **Linters & Helpers**: `golangci-lint`, `gomodifytags`, and `impl`.

You can also install these tools globally:
```bash
go install golang.org/x/tools/gopls@latest
go install golang.org/x/tools/cmd/goimports@latest
go install mvdan.cc/gofumpt@latest
go install github.com/go-delve/delve/cmd/dlv@latest
```

### Markdown
Run `:LazyExtras`, scroll to `lang.markdown`, and press `x` to enable.
- Provides `marksman` (LSP), `markdownlint-cli2`, `render-markdown.nvim` (in-editor formatting), and browser preview support.
- Toggle previews with: `:MarkdownPreviewToggle`.

### Git
LazyVim includes out-of-the-box Git integrations:
- `gitsigns.nvim` for inline change markers, hunks, staging, and blame.
- Neo-tree for visual Git status markers.
- Optional: run `:LazyExtras` and enable `lang.git` for commit, rebase, and `.gitignore` completion support.

### Makefile
While Neovim handles Makefiles natively, you can optimize syntax trees using Treesitter. Create `~/.config/nvim/lua/plugins/treesitter-extra.lua`:

```lua
return {
  "nvim-treesitter/nvim-treesitter",
  opts = {
    ensure_installed = {
      "make",
    },
  },
}
```

## Suggested Config Layout

Once you complete configuration, your layout will be structured as follows:
```text
~/.config/nvim/
├── init.lua
└── lua/
    ├── config/
    │   ├── autocmds.lua
    │   ├── keymaps.lua
    │   ├── lazy.lua
    │   └── options.lua
    └── plugins/
        ├── colorscheme.lua
        ├── autosave.lua
        ├── snacks.lua
        └── treesitter-extra.lua
```

## Useful Keybindings & Commands

LazyVim uses `<Space>` as the leader key. Tap `<Space>` in Normal mode and pause to see the visual keymapping assistant (powered by Which-key).

### Essential Navigation & Editing

| Need                                | Key or Command     |
|:------------------------------------|:-------------------|
| **Open current directory**          | `nvim .`           |
| **Find files**                      | `<Space><Space>`   |
| **Search project text**             | `<Space>sg`        |
| **Open file explorer**              | `<Space>e`         |
| **Format current file**             | `<Space>cf`        |
| **Save / Quit**                     | `:w` / `:q`        |
| **Manage plugins / Language tools** | `:Lazy` / `:Mason` |
| **Enable extras**                   | `:LazyExtras`      |

### Go LSP Commands

| Need                    | Key         |
|:------------------------|:------------|
| **Go to definition**    | `gd`        |
| **Hover documentation** | `K`         |
| **Show code actions**   | `<Space>ca` |
| **Rename symbol**       | `<Space>cr` |
| **Show diagnostics**    | `<Space>xx` |


## Alternative Standalone Neovim Setup (Optional)

If you prefer building a custom setup from scratch rather than using LazyVim, you can build a smaller configuration with `lazy.nvim` directly using these key plugins:
- `neovim/nvim-lspconfig`
- `williamboman/mason.nvim`
- `williamboman/mason-lspconfig.nvim`
- `nvim-treesitter/nvim-treesitter`
- `ray-x/go.nvim`
- `lewis6991/gitsigns.nvim`
- `nvim-telescope/telescope.nvim`

*Note: For developers migrating from JetBrains/IntelliJ seeking a quick and productive experience, LazyVim is heavily recommended over custom standalones.*

## Setup Walkthrough Test

Let's validate your newly configured environment with a real repository maintenance task using `igor-baiborodine/campsite-booking-go`.

1. **Clone and Open the Repository**

    ```bash
    git clone https://github.com/igor-baiborodine/campsite-booking-go.git
    cd campsite-booking-go
    nvim .
    ```
    Verify that Neovim opens correctly in directory explorer mode. Navigate to `go.mod`, `Makefile`, and `README.md` to confirm file reading.

2. **Run Health Checks**

    Run `:checkhealth` and `:Mason` inside Neovim. Verify that `gopls` and formatting dependencies display as healthy and installed.

3. **Verify Go LSP Navigation**

Open a Go file in the repository and test `gopls` by using `gd` on a few symbols.

If `gopls` is attached, `gd` should jump to the symbol definition:

- On a function call, it jumps to the function declaration.
- On a type name, it jumps to the type definition.
- On a variable or constant, it jumps to where it was declared.
- If the definition is in another file or package, Neovim opens that location.

4. **Upgrade Go Module Dependencies**

    Open `go.mod`. Update the `go` version directive to your target version (e.g., `1.26.5`).

    Open a terminal within Neovim:
    ```vim
    :terminal
    ```
    Run module updates inside the terminal window:
    ```bash
    go version
    go get -u ./...
    go mod tidy
    go test ./...
    ```
    Exit the terminal buffer with `jk` or `Ctrl-\ Ctrl-n` and check the modifications using:
    ```bash
    git diff go.mod go.sum
    ```

5. **Upgrade Makefile Tool Versions**

    Open the `Makefile` inside Neovim:
    ```vim
    :e Makefile
    ```
    Locate the pinned tool versions and upgrade them:
    ```make
    PROTOC_GEN_GO_VERSION = v1.36.10
    PROTOC_GEN_GO_GRPC_VERSION = v1.6.2
    GOLANGCI_LINT_VERSION = v2.12.2
    ```
    Save the file (`:w`) and execute:
    ```bash
    make install-tools
    ```

6. **Start and Test App Stack**

    Verify local infrastructure by running the stack via the terminal:
    ```bash
    make compose-up-postgres
    docker inspect --format="{{.State.Health.Status}}" postgres
    # Once postgres is healthy, run:
    make compose-up-all
    ```
    Execute unit and integration tests:
    ```bash
    make test
    make test-integration
    ```

7. **Verify gRPC Endpoints via grpcurl**

    Ensure endpoints respond properly:
    ```bash
    go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest
    grpcurl -plaintext localhost:8085 list
    ```

8. **Commit Your Changes**

    Check everything into your branch inside the Neovim terminal:
    ```bash
    git checkout -b upgrade-go-setup
    git status
    git add go.mod go.sum Makefile
    git commit -m "chore: upgrade go version and developer tools"
    ```