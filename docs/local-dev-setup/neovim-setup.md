# Neovim Setup for Go Development on Ubuntu

The recommended path is to install Neovim and use **LazyVim** as the base configuration. LazyVim
gives you an IDE-like setup without manually wiring every plugin: LSP, completion, diagnostics,
formatting, Git integration, file search, syntax highlighting, and optional language-specific
extras.

## Recommended Approach

Use this setup if you are moving from IntelliJ to a free Go development environment:

1. Install a recent Neovim version.
2. Install required system tools.
3. Install Go.
4. Install a Nerd Font and configure it in the terminal.
5. Bootstrap LazyVim.
6. Enable LazyVim extras for Go, Markdown, and optionally Git.
7. Use built-in Makefile support, with optional Treesitter enhancement.

This is easier to maintain than a fully custom Neovim configuration and gives you a working
IDE-style baseline quickly.

## Install Neovim on Ubuntu

There are several installation options. Prefer a recent version because LazyVim and modern plugins
expect newer Neovim features.

### Option 1: Official Neovim Archive

This is a good choice when the Ubuntu package is too old.

```bash
sudo apt update
sudo apt install -y curl git build-essential ripgrep fd-find make unzip

curl -LO https://github.com/neovim/neovim/releases/latest/download/nvim-linux-x86_64.tar.gz
sudo rm -rf /opt/nvim-linux-x86_64
sudo tar -C /opt -xzf nvim-linux-x86_64.tar.gz

echo 'export PATH="$PATH:/opt/nvim-linux-x86_64/bin"' >> ~/.bashrc
source ~/.bashrc

nvim --version
```

Ubuntu installs the `fd` tool as `fdfind`, while many Neovim plugins expect the executable name
`fd`. Add a user-level symlink:

```bash
mkdir -p ~/.local/bin
ln -sf "$(command -v fdfind)" ~/.local/bin/fd
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bashrc
source ~/.bashrc
```

### Option 2: Ubuntu APT Package

This is the simplest option, but it may install an older version.

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

## Install Prerequisites

Install the common tools needed by LazyVim, Treesitter, plugin builds, search, and Git workflows:

Recommended tools:

- `git`: required for LazyVim and plugin installation.
- `build-essential`: provides a compiler for Treesitter parsers and native plugin builds.
- `curl` and `unzip`: used by plugins and tooling installers.
- `ripgrep`: fast project-wide search.
- `fd-find`: fast file discovery.
- `make`: required for Makefile-based projects and many Go repositories.
- `fzf`: a general-purpose fuzzy finder for the command line that some LazyVim pickers and
  extensions can use for fast, interactive filtering of files, buffers, or command history.
- `lazygit`: a simple terminal UI wrapping common git commands (staging, committing, branching,
  diffs) with keyboard-driven navigation.

```bash
sudo apt update
sudo apt install -y git build-essential curl unzip ripgrep fd-find make fzf
```

```bash
LAZYGIT_VERSION=$(curl -s "https://api.github.com/repos/jesseduffield/lazygit/releases/latest" | grep -Po '"tag_name": "v\K[^"]*')
curl -Lo lazygit.tar.gz "https://github.com/jesseduffield/lazygit/releases/latest/download/lazygit_${LAZYGIT_VERSION}_Linux_x86_64.tar.gz"
tar xf lazygit.tar.gz lazygit
sudo install lazygit /usr/local/bin
```

## Install Go

If Go is not installed yet, install it before configuring Go support in Neovim.

Simple Ubuntu package option:

```bash
sudo apt update
sudo apt install -y golang-go
go version
```

For production Go work, prefer the official Go installation instructions when you need a newer Go
version than Ubuntu provides.

Make sure Go's binary directory is on your `PATH` if you install tools with `go install`:

```bash
echo 'export PATH="$HOME/go/bin:$PATH"' >> ~/.bashrc
source ~/.bashrc
```

## Install and Configure a Nerd Font

LazyVim works without a Nerd Font, but icons in the file explorer, status line, diagnostics, and UI
will look wrong unless your terminal uses one.

Neovim itself does not render fonts. Your terminal emulator does. Installing a Nerd Font means:

1. Install the font on Ubuntu.
2. Configure your terminal to use that font.

Example using JetBrainsMono Nerd Font:

```bash
wget https://github.com/ryanoasis/nerd-fonts/releases/latest/download/JetBrainsMono.zip
unzip JetBrainsMono.zip -d JetBrainsMono
mkdir -p ~/.local/share/fonts
mv JetBrainsMono ~/.local/share/fonts/
fc-cache -fv
fc-list | grep "JetBrainsMono"
```

Then set the font in your terminal:

- GNOME Terminal: Preferences -> Profile -> Text -> Custom font.
- Konsole: Settings -> Edit Current Profile -> Appearance.
- Alacritty: configure `~/.config/alacritty/alacritty.toml`.
- Kitty: configure `~/.config/kitty/kitty.conf`.

If LazyVim shows boxes or question marks instead of icons, the terminal font is not configured
correctly.

## Bootstrap LazyVim

Back up any existing Neovim configuration first:

```bash
mv ~/.config/nvim ~/.config/nvim.bak 2>/dev/null || true
mv ~/.local/share/nvim ~/.local/share/nvim.bak 2>/dev/null || true
mv ~/.local/state/nvim ~/.local/state/nvim.bak 2>/dev/null || true
mv ~/.cache/nvim ~/.cache/nvim.bak 2>/dev/null || true
```

Clone the LazyVim starter:

```bash
git clone https://github.com/LazyVim/starter ~/.config/nvim
rm -rf ~/.config/nvim/.git
nvim
```

On first launch, LazyVim will install its core plugins automatically.

After installation, run these health checks inside Neovim:

```vim
:LazyHealth
:checkhealth
```

Here's a drafted section matching the document's style, following the LazyVim colorscheme pattern with the exact plugin manager config from the solarized.nvim repo. [lazy.folke](https://lazy.folke.io/spec/examples)

## Install Solarized Theme (Optional)

If you prefer the Solarized color palette over LazyVim's default theme, install `solarized.nvim`, a
Lua-native port with Treesitter and LSP highlighting support.

Create a new plugin spec file:

```text
~/.config/nvim/lua/plugins/colorscheme.lua
```

Add the following, using lazy.nvim's package manager syntax to install the plugin and set it as
LazyVim's active colorscheme: 

```lua
return {
  'maxmx03/solarized.nvim',
  lazy = false,
  priority = 1000,
  ---@type solarized.config
  opts = {},
  config = function(_, opts)
    vim.o.termguicolors = true
    vim.o.background = 'light'
    require('solarized').setup(opts)
    vim.cmd.colorscheme 'solarized'
  end,
}:
```

Save the file, then run `:Lazy sync` to install the plugin immediately, or simply restart Neovim to
have lazy.nvim install it automatically on next launch. 

To switch between the light and dark variants at any time without editing the config, run:

```
:set background=light
```

or

```
:set background=dark
```

Solarized.nvim requires Neovim v0.9.1 or newer and the `nvim-treesitter` plugin, both of which are
already included in a standard LazyVim setup. Make sure `vim.o.termguicolors = true` is set in your
Neovim config (LazyVim enables this by default), otherwise the Solarized palette will render with
incorrect colors in the terminal. 

## Enable Go Support

LazyVim provides a `lang.go` extra. Enable it from inside Neovim:

```vim
:LazyExtras
```

Find `lang.go` in the disabled extras list and press `x` to enable it. Restart Neovim afterward.

The Go extra typically provides or integrates:

- `gopls`: official Go language server for completion, diagnostics, navigation, hover, rename, and
  references.
- `goimports`: import organization and formatting.
- `gofumpt`: stricter Go formatting.
- `golangci-lint`: project linting.
- `delve`: Go debugging.
- `nvim-dap-go`: debug adapter integration.
- `neotest-golang`: test execution inside Neovim.
- `gomodifytags`: struct tag editing.
- `impl`: interface implementation helper.
- Treesitter parsers for Go-related filetypes.

You can also install common Go tools manually:

```bash
go install golang.org/x/tools/gopls@latest
go install golang.org/x/tools/cmd/goimports@latest
go install mvdan.cc/gofumpt@latest
go install github.com/go-delve/delve/cmd/dlv@latest
```

## Enable Markdown Support

Enable LazyVim's Markdown extra:

```vim
:LazyExtras
```

Find `lang.markdown` and press `x` to enable it.

This usually gives you:

- `marksman`: Markdown language server.
- `markdownlint-cli2`: Markdown linting.
- `markdown-toc`: table-of-contents support.
- `prettier`: formatting where configured.
- `render-markdown.nvim`: in-editor Markdown rendering.
- `markdown-preview.nvim`: browser preview support.

Useful command:

```vim
:MarkdownPreviewToggle
```

Depending on the LazyVim version and keymap configuration, Markdown preview may also be available
through a leader-key mapping.

## Git Support

LazyVim includes practical Git support by default.

Common integrations:

| Feature                             | Tool                  |
|-------------------------------------|-----------------------|
| Inline changed-line markers         | `gitsigns.nvim`       |
| Hunk staging, reset, preview, blame | `gitsigns.nvim`       |
| Git file and commit search          | Telescope Git pickers |
| Git status in file explorer         | Neo-tree              |
| Terminal Git UI                     | Lazygit integration   |

Typical LazyVim Git actions:

| Need                      | Key or Command                                          |
|---------------------------|---------------------------------------------------------|
| Open Lazygit              | `<Space>gg`                                             |
| Navigate Git hunks        | `]h` and `[h`                                           |
| Stage current hunk        | leader-key Git/hunk menu, depending on LazyVim mappings |
| View Git-related mappings | press `<Space>` and wait for which-key                  |

Optional: enable the `lang.git` extra in `:LazyExtras` for better Git commit, rebase, gitignore, and
Git-related completion support.

You can still use standard terminal Git normally:

```bash
git status
git add .
git commit -m "Describe the change"
git push
```

## Makefile Support

Makefiles do not require a heavyweight plugin.

Neovim already detects `Makefile` and `.mk` files and provides syntax highlighting and indentation.
LazyVim's Treesitter setup can improve highlighting if the `make` parser is installed.

Optional Treesitter config:

```lua
-- ~/.config/nvim/lua/plugins/treesitter-extra.lua
return {
  "nvim-treesitter/nvim-treesitter",
  opts = {
    ensure_installed = {
      "make",
    },
  },
}
```

Normal workflow:

```bash
nvim Makefile
make
make test
make build
```

## Useful LazyVim Commands and Keybindings

LazyVim uses `<Space>` as the leader key. Press `<Space>` in normal mode and pause to see available
commands through which-key.

| Need                   | Command or Key   |
|------------------------|------------------|
| Open current directory | `nvim .`         |
| Save file              | `:w`             |
| Quit                   | `:q`             |
| Search in current file | `/text`          |
| Find files             | `<Space><Space>` |
| Search project text    | `<Space>sg`      |
| Open file explorer     | `<Space>e`       |
| Format current file    | `<Space>cf`      |
| Show code actions      | `<Space>ca`      |
| Rename symbol          | `<Space>cr`      |
| Show diagnostics       | `<Space>xx`      |
| Manage plugins         | `:Lazy`          |
| Manage language tools  | `:Mason`         |
| Enable extras          | `:LazyExtras`    |
| Check LazyVim health   | `:LazyHealth`    |
| Check Neovim health    | `:checkhealth`   |

Important LSP habits for Go development:

| Need                | Typical Action                               |
|---------------------|----------------------------------------------|
| Go to definition    | use LazyVim's LSP mapping shown by which-key |
| Hover documentation | use LazyVim's LSP mapping shown by which-key |
| Rename symbol       | `<Space>cr`                                  |
| Code action         | `<Space>ca`                                  |
| Format file         | `<Space>cf`                                  |
| View diagnostics    | `<Space>xx`                                  |

## Suggested Config Layout

After installing LazyVim, your config will look roughly like this:

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
        └── treesitter-extra.lua
```

Add custom plugin specs under:

```text
~/.config/nvim/lua/plugins/
```

Add custom keymaps under:

```text
~/.config/nvim/lua/config/keymaps.lua
```

Example optional Lazygit mapping:

```lua
vim.keymap.set("n", "<leader>gg", "<cmd>LazyGit<cr>", { desc = "LazyGit" })
```

## Optional Standalone Neovim Setup

If you do not want LazyVim, you can build a smaller custom setup using `lazy.nvim` directly. For Go
development, the common plugin set is:

- `neovim/nvim-lspconfig`
- `williamboman/mason.nvim`
- `williamboman/mason-lspconfig.nvim`
- `nvim-treesitter/nvim-treesitter`
- `ray-x/go.nvim`
- `lewis6991/gitsigns.nvim`
- `tpope/vim-fugitive`
- `nvim-telescope/telescope.nvim`
- `iamcco/markdown-preview.nvim`

That approach gives more control but requires more configuration work. For a former IntelliJ user
who wants to become productive quickly, LazyVim is the better starting point.

Yes — here is a fully rewritten `## Test Setup` section you can paste into the document.

## Test Setup

After installing Neovim, LazyVim, Go support, and the required system tools, test the setup with a
real maintenance task on a real repository. Use the `igor-baiborodine/campsite-booking-go` project
to validate project navigation, Go upgrades, Makefile-based tool installation, local environment
startup, test execution, and Git workflows from within Neovim. [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/62895318/d8d935f0-c4bb-4ced-bc41-1eef7861a540/neovim-setup.md?AWSAccessKeyId=ASIA2F3EMEYE3AK5EPXQ&Signature=uNgOA%2FVezjiKlCNebMP2EXIbjCc%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEBUaCXVzLWVhc3QtMSJIMEYCIQD9n1zqjkPL3vWa76o253opN%2FVa%2FAv7qk6s5vppiYQD8wIhAK9Xdb51zJ9ozPxDw0IL4w3eaBunbZxyRQN60b%2FXICNIKvwECN7%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEQARoMNjk5NzUzMzA5NzA1IgwIhihE%2FwtcQJKpsJkq0ASWR%2BOAhmXayo4nx12u%2BlrdmR7lZ9O6VhgiZiZLV3FYCD7iWufKMAuXN5zzhr01kDuR8SJVoX7GRzMZbrWTDlg%2Fv5nH%2FruxjOtbjvN9gmtrt6VAVPnYkiKk6h0InHtCgOK8IIJVpLOXIjH8vAJp6QiwhTRE0qQdDCoBmesbd%2B3lrPYNhKgSl9c6T5BKnqCpC34VsEBhIdFtQqL6mqV0Zbb%2FHMRXFA0CmeFc63JZ%2FfKKZ%2FN1%2FV0NxjDwIlYrt7%2B5fTFLy8pFgbURMMdhsbI%2BEHhBNkzBzt6CerOD7jDLDxnTRlR%2FfwhCTM0I3dCPQDR6dGo%2B0ZVNgKU2hYEI8goZ5%2F8KD6rpNmA%2Bf5sTTHz3MwdmPT2xiNOp%2B5PzOc69N2sT7XCiDGg1LaEtfWDPgxlXyb3s6Fc8GxfQ4YZ%2FH7g4jVqoI05HpMb6F2DcLQjzsFJzMLVT8o4lrQ1d3VdQAWV%2FPKm9JJVEtM0bMSAoP8zKbsiFXDMf9sARBCvr0lVq5guk46nqeNeg4Xkp7U7NgyVdX09Qnno0wJt7qmNH0jvQSGTwEHY3ilmZIPxMnJHUZNsdQV80yrAUb2k3JTHBaZUsKmEGt96s0OJRbSG1cPdGxnDDVqaOE17nxe7TExhkxdK%2BSOCr10vHAUDl80fmPbQSNWtYwqgpL2tgU%2FavPSn%2FDarMsJX9jSHXJkEYhasascKDbl%2B%2FNQTbOaCJ8yUZvr%2F7XrrqPXir42Y3fQH7zccFheJnEJGJkpLpePitCMcBxdBhaUebk0lOd0rMeBIqPyNBFIxSMLDQhNMGOpcBZwzcuqs1Dt32G0mWkRcxXULM7z1CkOs9DHId82Lx82qpSV53E8jlZqRf6YBjvwBH%2BzrC4BYgH%2BZvXS8eEePu5xI8hvDfd5eR4X4naaRUrrjGPhs%2Frcol%2FiJoQpQ3ZeY5Me%2Fqq3%2BKKXy6jhMV57w%2B9nJQvX4FVekPO3zQAycDZq0iTBu4zgiifk5vi01urmtLQde5AwJ64g%3D%3D&Expires=1784755715)

This repository contains a Go gRPC API example application. Its README documents local setup, Docker
Compose targets, unit and integration tests, `grpcurl` commands, concurrent test scripts, and
performance-related commands, which makes it a practical project for end-to-end validation. [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/images/62895318/b20c64d5-9127-4f77-beec-6edab9c5e30c/image.jpg?AWSAccessKeyId=ASIA2F3EMEYE3AK5EPXQ&Signature=JmpTdD9odV3rDmgDoqFqOH29i20%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEBUaCXVzLWVhc3QtMSJIMEYCIQD9n1zqjkPL3vWa76o253opN%2FVa%2FAv7qk6s5vppiYQD8wIhAK9Xdb51zJ9ozPxDw0IL4w3eaBunbZxyRQN60b%2FXICNIKvwECN7%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEQARoMNjk5NzUzMzA5NzA1IgwIhihE%2FwtcQJKpsJkq0ASWR%2BOAhmXayo4nx12u%2BlrdmR7lZ9O6VhgiZiZLV3FYCD7iWufKMAuXN5zzhr01kDuR8SJVoX7GRzMZbrWTDlg%2Fv5nH%2FruxjOtbjvN9gmtrt6VAVPnYkiKk6h0InHtCgOK8IIJVpLOXIjH8vAJp6QiwhTRE0qQdDCoBmesbd%2B3lrPYNhKgSl9c6T5BKnqCpC34VsEBhIdFtQqL6mqV0Zbb%2FHMRXFA0CmeFc63JZ%2FfKKZ%2FN1%2FV0NxjDwIlYrt7%2B5fTFLy8pFgbURMMdhsbI%2BEHhBNkzBzt6CerOD7jDLDxnTRlR%2FfwhCTM0I3dCPQDR6dGo%2B0ZVNgKU2hYEI8goZ5%2F8KD6rpNmA%2Bf5sTTHz3MwdmPT2xiNOp%2B5PzOc69N2sT7XCiDGg1LaEtfWDPgxlXyb3s6Fc8GxfQ4YZ%2FH7g4jVqoI05HpMb6F2DcLQjzsFJzMLVT8o4lrQ1d3VdQAWV%2FPKm9JJVEtM0bMSAoP8zKbsiFXDMf9sARBCvr0lVq5guk46nqeNeg4Xkp7U7NgyVdX09Qnno0wJt7qmNH0jvQSGTwEHY3ilmZIPxMnJHUZNsdQV80yrAUb2k3JTHBaZUsKmEGt96s0OJRbSG1cPdGxnDDVqaOE17nxe7TExhkxdK%2BSOCr10vHAUDl80fmPbQSNWtYwqgpL2tgU%2FavPSn%2FDarMsJX9jSHXJkEYhasascKDbl%2B%2FNQTbOaCJ8yUZvr%2F7XrrqPXir42Y3fQH7zccFheJnEJGJkpLpePitCMcBxdBhaUebk0lOd0rMeBIqPyNBFIxSMLDQhNMGOpcBZwzcuqs1Dt32G0mWkRcxXULM7z1CkOs9DHId82Lx82qpSV53E8jlZqRf6YBjvwBH%2BzrC4BYgH%2BZvXS8eEePu5xI8hvDfd5eR4X4naaRUrrjGPhs%2Frcol%2FiJoQpQ3ZeY5Me%2Fqq3%2BKKXy6jhMV57w%2B9nJQvX4FVekPO3zQAycDZq0iTBu4zgiifk5vi01urmtLQde5AwJ64g%3D%3D&Expires=1784755715)

### Clone and Open Project

All commands in the repository README are intended to be run from the project root. [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/images/62895318/b20c64d5-9127-4f77-beec-6edab9c5e30c/image.jpg?AWSAccessKeyId=ASIA2F3EMEYE3AK5EPXQ&Signature=JmpTdD9odV3rDmgDoqFqOH29i20%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEBUaCXVzLWVhc3QtMSJIMEYCIQD9n1zqjkPL3vWa76o253opN%2FVa%2FAv7qk6s5vppiYQD8wIhAK9Xdb51zJ9ozPxDw0IL4w3eaBunbZxyRQN60b%2FXICNIKvwECN7%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEQARoMNjk5NzUzMzA5NzA1IgwIhihE%2FwtcQJKpsJkq0ASWR%2BOAhmXayo4nx12u%2BlrdmR7lZ9O6VhgiZiZLV3FYCD7iWufKMAuXN5zzhr01kDuR8SJVoX7GRzMZbrWTDlg%2Fv5nH%2FruxjOtbjvN9gmtrt6VAVPnYkiKk6h0InHtCgOK8IIJVpLOXIjH8vAJp6QiwhTRE0qQdDCoBmesbd%2B3lrPYNhKgSl9c6T5BKnqCpC34VsEBhIdFtQqL6mqV0Zbb%2FHMRXFA0CmeFc63JZ%2FfKKZ%2FN1%2FV0NxjDwIlYrt7%2B5fTFLy8pFgbURMMdhsbI%2BEHhBNkzBzt6CerOD7jDLDxnTRlR%2FfwhCTM0I3dCPQDR6dGo%2B0ZVNgKU2hYEI8goZ5%2F8KD6rpNmA%2Bf5sTTHz3MwdmPT2xiNOp%2B5PzOc69N2sT7XCiDGg1LaEtfWDPgxlXyb3s6Fc8GxfQ4YZ%2FH7g4jVqoI05HpMb6F2DcLQjzsFJzMLVT8o4lrQ1d3VdQAWV%2FPKm9JJVEtM0bMSAoP8zKbsiFXDMf9sARBCvr0lVq5guk46nqeNeg4Xkp7U7NgyVdX09Qnno0wJt7qmNH0jvQSGTwEHY3ilmZIPxMnJHUZNsdQV80yrAUb2k3JTHBaZUsKmEGt96s0OJRbSG1cPdGxnDDVqaOE17nxe7TExhkxdK%2BSOCr10vHAUDl80fmPbQSNWtYwqgpL2tgU%2FavPSn%2FDarMsJX9jSHXJkEYhasascKDbl%2B%2FNQTbOaCJ8yUZvr%2F7XrrqPXir42Y3fQH7zccFheJnEJGJkpLpePitCMcBxdBhaUebk0lOd0rMeBIqPyNBFIxSMLDQhNMGOpcBZwzcuqs1Dt32G0mWkRcxXULM7z1CkOs9DHId82Lx82qpSV53E8jlZqRf6YBjvwBH%2BzrC4BYgH%2BZvXS8eEePu5xI8hvDfd5eR4X4naaRUrrjGPhs%2Frcol%2FiJoQpQ3ZeY5Me%2Fqq3%2BKKXy6jhMV57w%2B9nJQvX4FVekPO3zQAycDZq0iTBu4zgiifk5vi01urmtLQde5AwJ64g%3D%3D&Expires=1784755715)

```bash
git clone https://github.com/igor-baiborodine/campsite-booking-go.git
cd campsite-booking-go
nvim .
```

Opening the project with `nvim .` makes LazyVim treat the repository as the current working
directory, so file search, diagnostics, LSP, and project navigation all work across the full code
base. [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/62895318/d8d935f0-c4bb-4ced-bc41-1eef7861a540/neovim-setup.md?AWSAccessKeyId=ASIA2F3EMEYE3AK5EPXQ&Signature=uNgOA%2FVezjiKlCNebMP2EXIbjCc%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEBUaCXVzLWVhc3QtMSJIMEYCIQD9n1zqjkPL3vWa76o253opN%2FVa%2FAv7qk6s5vppiYQD8wIhAK9Xdb51zJ9ozPxDw0IL4w3eaBunbZxyRQN60b%2FXICNIKvwECN7%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEQARoMNjk5NzUzMzA5NzA1IgwIhihE%2FwtcQJKpsJkq0ASWR%2BOAhmXayo4nx12u%2BlrdmR7lZ9O6VhgiZiZLV3FYCD7iWufKMAuXN5zzhr01kDuR8SJVoX7GRzMZbrWTDlg%2Fv5nH%2FruxjOtbjvN9gmtrt6VAVPnYkiKk6h0InHtCgOK8IIJVpLOXIjH8vAJp6QiwhTRE0qQdDCoBmesbd%2B3lrPYNhKgSl9c6T5BKnqCpC34VsEBhIdFtQqL6mqV0Zbb%2FHMRXFA0CmeFc63JZ%2FfKKZ%2FN1%2FV0NxjDwIlYrt7%2B5fTFLy8pFgbURMMdhsbI%2BEHhBNkzBzt6CerOD7jDLDxnTRlR%2FfwhCTM0I3dCPQDR6dGo%2B0ZVNgKU2hYEI8goZ5%2F8KD6rpNmA%2Bf5sTTHz3MwdmPT2xiNOp%2B5PzOc69N2sT7XCiDGg1LaEtfWDPgxlXyb3s6Fc8GxfQ4YZ%2FH7g4jVqoI05HpMb6F2DcLQjzsFJzMLVT8o4lrQ1d3VdQAWV%2FPKm9JJVEtM0bMSAoP8zKbsiFXDMf9sARBCvr0lVq5guk46nqeNeg4Xkp7U7NgyVdX09Qnno0wJt7qmNH0jvQSGTwEHY3ilmZIPxMnJHUZNsdQV80yrAUb2k3JTHBaZUsKmEGt96s0OJRbSG1cPdGxnDDVqaOE17nxe7TExhkxdK%2BSOCr10vHAUDl80fmPbQSNWtYwqgpL2tgU%2FavPSn%2FDarMsJX9jSHXJkEYhasascKDbl%2B%2FNQTbOaCJ8yUZvr%2F7XrrqPXir42Y3fQH7zccFheJnEJGJkpLpePitCMcBxdBhaUebk0lOd0rMeBIqPyNBFIxSMLDQhNMGOpcBZwzcuqs1Dt32G0mWkRcxXULM7z1CkOs9DHId82Lx82qpSV53E8jlZqRf6YBjvwBH%2BzrC4BYgH%2BZvXS8eEePu5xI8hvDfd5eR4X4naaRUrrjGPhs%2Frcol%2FiJoQpQ3ZeY5Me%2Fqq3%2BKKXy6jhMV57w%2B9nJQvX4FVekPO3zQAycDZq0iTBu4zgiifk5vi01urmtLQde5AwJ64g%3D%3D&Expires=1784755715)

As a first step inside Neovim, open these files:

- `go.mod`
- `go.sum`
- `Makefile`
- `README.md`

Useful navigation commands from the current setup:

| Need | Command or Key |
|---|---|
| Find files | `<Space><Space>`  [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/62895318/d8d935f0-c4bb-4ced-bc41-1eef7861a540/neovim-setup.md?AWSAccessKeyId=ASIA2F3EMEYE3AK5EPXQ&Signature=uNgOA%2FVezjiKlCNebMP2EXIbjCc%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEBUaCXVzLWVhc3QtMSJIMEYCIQD9n1zqjkPL3vWa76o253opN%2FVa%2FAv7qk6s5vppiYQD8wIhAK9Xdb51zJ9ozPxDw0IL4w3eaBunbZxyRQN60b%2FXICNIKvwECN7%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEQARoMNjk5NzUzMzA5NzA1IgwIhihE%2FwtcQJKpsJkq0ASWR%2BOAhmXayo4nx12u%2BlrdmR7lZ9O6VhgiZiZLV3FYCD7iWufKMAuXN5zzhr01kDuR8SJVoX7GRzMZbrWTDlg%2Fv5nH%2FruxjOtbjvN9gmtrt6VAVPnYkiKk6h0InHtCgOK8IIJVpLOXIjH8vAJp6QiwhTRE0qQdDCoBmesbd%2B3lrPYNhKgSl9c6T5BKnqCpC34VsEBhIdFtQqL6mqV0Zbb%2FHMRXFA0CmeFc63JZ%2FfKKZ%2FN1%2FV0NxjDwIlYrt7%2B5fTFLy8pFgbURMMdhsbI%2BEHhBNkzBzt6CerOD7jDLDxnTRlR%2FfwhCTM0I3dCPQDR6dGo%2B0ZVNgKU2hYEI8goZ5%2F8KD6rpNmA%2Bf5sTTHz3MwdmPT2xiNOp%2B5PzOc69N2sT7XCiDGg1LaEtfWDPgxlXyb3s6Fc8GxfQ4YZ%2FH7g4jVqoI05HpMb6F2DcLQjzsFJzMLVT8o4lrQ1d3VdQAWV%2FPKm9JJVEtM0bMSAoP8zKbsiFXDMf9sARBCvr0lVq5guk46nqeNeg4Xkp7U7NgyVdX09Qnno0wJt7qmNH0jvQSGTwEHY3ilmZIPxMnJHUZNsdQV80yrAUb2k3JTHBaZUsKmEGt96s0OJRbSG1cPdGxnDDVqaOE17nxe7TExhkxdK%2BSOCr10vHAUDl80fmPbQSNWtYwqgpL2tgU%2FavPSn%2FDarMsJX9jSHXJkEYhasascKDbl%2B%2FNQTbOaCJ8yUZvr%2F7XrrqPXir42Y3fQH7zccFheJnEJGJkpLpePitCMcBxdBhaUebk0lOd0rMeBIqPyNBFIxSMLDQhNMGOpcBZwzcuqs1Dt32G0mWkRcxXULM7z1CkOs9DHId82Lx82qpSV53E8jlZqRf6YBjvwBH%2BzrC4BYgH%2BZvXS8eEePu5xI8hvDfd5eR4X4naaRUrrjGPhs%2Frcol%2FiJoQpQ3ZeY5Me%2Fqq3%2BKKXy6jhMV57w%2B9nJQvX4FVekPO3zQAycDZq0iTBu4zgiifk5vi01urmtLQde5AwJ64g%3D%3D&Expires=1784755715) |
| Open file explorer | `<Space>e`  [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/62895318/d8d935f0-c4bb-4ced-bc41-1eef7861a540/neovim-setup.md?AWSAccessKeyId=ASIA2F3EMEYE3AK5EPXQ&Signature=uNgOA%2FVezjiKlCNebMP2EXIbjCc%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEBUaCXVzLWVhc3QtMSJIMEYCIQD9n1zqjkPL3vWa76o253opN%2FVa%2FAv7qk6s5vppiYQD8wIhAK9Xdb51zJ9ozPxDw0IL4w3eaBunbZxyRQN60b%2FXICNIKvwECN7%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEQARoMNjk5NzUzMzA5NzA1IgwIhihE%2FwtcQJKpsJkq0ASWR%2BOAhmXayo4nx12u%2BlrdmR7lZ9O6VhgiZiZLV3FYCD7iWufKMAuXN5zzhr01kDuR8SJVoX7GRzMZbrWTDlg%2Fv5nH%2FruxjOtbjvN9gmtrt6VAVPnYkiKk6h0InHtCgOK8IIJVpLOXIjH8vAJp6QiwhTRE0qQdDCoBmesbd%2B3lrPYNhKgSl9c6T5BKnqCpC34VsEBhIdFtQqL6mqV0Zbb%2FHMRXFA0CmeFc63JZ%2FfKKZ%2FN1%2FV0NxjDwIlYrt7%2B5fTFLy8pFgbURMMdhsbI%2BEHhBNkzBzt6CerOD7jDLDxnTRlR%2FfwhCTM0I3dCPQDR6dGo%2B0ZVNgKU2hYEI8goZ5%2F8KD6rpNmA%2Bf5sTTHz3MwdmPT2xiNOp%2B5PzOc69N2sT7XCiDGg1LaEtfWDPgxlXyb3s6Fc8GxfQ4YZ%2FH7g4jVqoI05HpMb6F2DcLQjzsFJzMLVT8o4lrQ1d3VdQAWV%2FPKm9JJVEtM0bMSAoP8zKbsiFXDMf9sARBCvr0lVq5guk46nqeNeg4Xkp7U7NgyVdX09Qnno0wJt7qmNH0jvQSGTwEHY3ilmZIPxMnJHUZNsdQV80yrAUb2k3JTHBaZUsKmEGt96s0OJRbSG1cPdGxnDDVqaOE17nxe7TExhkxdK%2BSOCr10vHAUDl80fmPbQSNWtYwqgpL2tgU%2FavPSn%2FDarMsJX9jSHXJkEYhasascKDbl%2B%2FNQTbOaCJ8yUZvr%2F7XrrqPXir42Y3fQH7zccFheJnEJGJkpLpePitCMcBxdBhaUebk0lOd0rMeBIqPyNBFIxSMLDQhNMGOpcBZwzcuqs1Dt32G0mWkRcxXULM7z1CkOs9DHId82Lx82qpSV53E8jlZqRf6YBjvwBH%2BzrC4BYgH%2BZvXS8eEePu5xI8hvDfd5eR4X4naaRUrrjGPhs%2Frcol%2FiJoQpQ3ZeY5Me%2Fqq3%2BKKXy6jhMV57w%2B9nJQvX4FVekPO3zQAycDZq0iTBu4zgiifk5vi01urmtLQde5AwJ64g%3D%3D&Expires=1784755715) |
| Search project text | `<Space>sg`  [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/62895318/d8d935f0-c4bb-4ced-bc41-1eef7861a540/neovim-setup.md?AWSAccessKeyId=ASIA2F3EMEYE3AK5EPXQ&Signature=uNgOA%2FVezjiKlCNebMP2EXIbjCc%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEBUaCXVzLWVhc3QtMSJIMEYCIQD9n1zqjkPL3vWa76o253opN%2FVa%2FAv7qk6s5vppiYQD8wIhAK9Xdb51zJ9ozPxDw0IL4w3eaBunbZxyRQN60b%2FXICNIKvwECN7%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEQARoMNjk5NzUzMzA5NzA1IgwIhihE%2FwtcQJKpsJkq0ASWR%2BOAhmXayo4nx12u%2BlrdmR7lZ9O6VhgiZiZLV3FYCD7iWufKMAuXN5zzhr01kDuR8SJVoX7GRzMZbrWTDlg%2Fv5nH%2FruxjOtbjvN9gmtrt6VAVPnYkiKk6h0InHtCgOK8IIJVpLOXIjH8vAJp6QiwhTRE0qQdDCoBmesbd%2B3lrPYNhKgSl9c6T5BKnqCpC34VsEBhIdFtQqL6mqV0Zbb%2FHMRXFA0CmeFc63JZ%2FfKKZ%2FN1%2FV0NxjDwIlYrt7%2B5fTFLy8pFgbURMMdhsbI%2BEHhBNkzBzt6CerOD7jDLDxnTRlR%2FfwhCTM0I3dCPQDR6dGo%2B0ZVNgKU2hYEI8goZ5%2F8KD6rpNmA%2Bf5sTTHz3MwdmPT2xiNOp%2B5PzOc69N2sT7XCiDGg1LaEtfWDPgxlXyb3s6Fc8GxfQ4YZ%2FH7g4jVqoI05HpMb6F2DcLQjzsFJzMLVT8o4lrQ1d3VdQAWV%2FPKm9JJVEtM0bMSAoP8zKbsiFXDMf9sARBCvr0lVq5guk46nqeNeg4Xkp7U7NgyVdX09Qnno0wJt7qmNH0jvQSGTwEHY3ilmZIPxMnJHUZNsdQV80yrAUb2k3JTHBaZUsKmEGt96s0OJRbSG1cPdGxnDDVqaOE17nxe7TExhkxdK%2BSOCr10vHAUDl80fmPbQSNWtYwqgpL2tgU%2FavPSn%2FDarMsJX9jSHXJkEYhasascKDbl%2B%2FNQTbOaCJ8yUZvr%2F7XrrqPXir42Y3fQH7zccFheJnEJGJkpLpePitCMcBxdBhaUebk0lOd0rMeBIqPyNBFIxSMLDQhNMGOpcBZwzcuqs1Dt32G0mWkRcxXULM7z1CkOs9DHId82Lx82qpSV53E8jlZqRf6YBjvwBH%2BzrC4BYgH%2BZvXS8eEePu5xI8hvDfd5eR4X4naaRUrrjGPhs%2Frcol%2FiJoQpQ3ZeY5Me%2Fqq3%2BKKXy6jhMV57w%2B9nJQvX4FVekPO3zQAycDZq0iTBu4zgiifk5vi01urmtLQde5AwJ64g%3D%3D&Expires=1784755715) |
| Format current file | `<Space>cf`  [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/62895318/d8d935f0-c4bb-4ced-bc41-1eef7861a540/neovim-setup.md?AWSAccessKeyId=ASIA2F3EMEYE3AK5EPXQ&Signature=uNgOA%2FVezjiKlCNebMP2EXIbjCc%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEBUaCXVzLWVhc3QtMSJIMEYCIQD9n1zqjkPL3vWa76o253opN%2FVa%2FAv7qk6s5vppiYQD8wIhAK9Xdb51zJ9ozPxDw0IL4w3eaBunbZxyRQN60b%2FXICNIKvwECN7%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEQARoMNjk5NzUzMzA5NzA1IgwIhihE%2FwtcQJKpsJkq0ASWR%2BOAhmXayo4nx12u%2BlrdmR7lZ9O6VhgiZiZLV3FYCD7iWufKMAuXN5zzhr01kDuR8SJVoX7GRzMZbrWTDlg%2Fv5nH%2FruxjOtbjvN9gmtrt6VAVPnYkiKk6h0InHtCgOK8IIJVpLOXIjH8vAJp6QiwhTRE0qQdDCoBmesbd%2B3lrPYNhKgSl9c6T5BKnqCpC34VsEBhIdFtQqL6mqV0Zbb%2FHMRXFA0CmeFc63JZ%2FfKKZ%2FN1%2FV0NxjDwIlYrt7%2B5fTFLy8pFgbURMMdhsbI%2BEHhBNkzBzt6CerOD7jDLDxnTRlR%2FfwhCTM0I3dCPQDR6dGo%2B0ZVNgKU2hYEI8goZ5%2F8KD6rpNmA%2Bf5sTTHz3MwdmPT2xiNOp%2B5PzOc69N2sT7XCiDGg1LaEtfWDPgxlXyb3s6Fc8GxfQ4YZ%2FH7g4jVqoI05HpMb6F2DcLQjzsFJzMLVT8o4lrQ1d3VdQAWV%2FPKm9JJVEtM0bMSAoP8zKbsiFXDMf9sARBCvr0lVq5guk46nqeNeg4Xkp7U7NgyVdX09Qnno0wJt7qmNH0jvQSGTwEHY3ilmZIPxMnJHUZNsdQV80yrAUb2k3JTHBaZUsKmEGt96s0OJRbSG1cPdGxnDDVqaOE17nxe7TExhkxdK%2BSOCr10vHAUDl80fmPbQSNWtYwqgpL2tgU%2FavPSn%2FDarMsJX9jSHXJkEYhasascKDbl%2B%2FNQTbOaCJ8yUZvr%2F7XrrqPXir42Y3fQH7zccFheJnEJGJkpLpePitCMcBxdBhaUebk0lOd0rMeBIqPyNBFIxSMLDQhNMGOpcBZwzcuqs1Dt32G0mWkRcxXULM7z1CkOs9DHId82Lx82qpSV53E8jlZqRf6YBjvwBH%2BzrC4BYgH%2BZvXS8eEePu5xI8hvDfd5eR4X4naaRUrrjGPhs%2Frcol%2FiJoQpQ3ZeY5Me%2Fqq3%2BKKXy6jhMV57w%2B9nJQvX4FVekPO3zQAycDZq0iTBu4zgiifk5vi01urmtLQde5AwJ64g%3D%3D&Expires=1784755715) |
| Show diagnostics | `<Space>xx`  [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/62895318/d8d935f0-c4bb-4ced-bc41-1eef7861a540/neovim-setup.md?AWSAccessKeyId=ASIA2F3EMEYE3AK5EPXQ&Signature=uNgOA%2FVezjiKlCNebMP2EXIbjCc%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEBUaCXVzLWVhc3QtMSJIMEYCIQD9n1zqjkPL3vWa76o253opN%2FVa%2FAv7qk6s5vppiYQD8wIhAK9Xdb51zJ9ozPxDw0IL4w3eaBunbZxyRQN60b%2FXICNIKvwECN7%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEQARoMNjk5NzUzMzA5NzA1IgwIhihE%2FwtcQJKpsJkq0ASWR%2BOAhmXayo4nx12u%2BlrdmR7lZ9O6VhgiZiZLV3FYCD7iWufKMAuXN5zzhr01kDuR8SJVoX7GRzMZbrWTDlg%2Fv5nH%2FruxjOtbjvN9gmtrt6VAVPnYkiKk6h0InHtCgOK8IIJVpLOXIjH8vAJp6QiwhTRE0qQdDCoBmesbd%2B3lrPYNhKgSl9c6T5BKnqCpC34VsEBhIdFtQqL6mqV0Zbb%2FHMRXFA0CmeFc63JZ%2FfKKZ%2FN1%2FV0NxjDwIlYrt7%2B5fTFLy8pFgbURMMdhsbI%2BEHhBNkzBzt6CerOD7jDLDxnTRlR%2FfwhCTM0I3dCPQDR6dGo%2B0ZVNgKU2hYEI8goZ5%2F8KD6rpNmA%2Bf5sTTHz3MwdmPT2xiNOp%2B5PzOc69N2sT7XCiDGg1LaEtfWDPgxlXyb3s6Fc8GxfQ4YZ%2FH7g4jVqoI05HpMb6F2DcLQjzsFJzMLVT8o4lrQ1d3VdQAWV%2FPKm9JJVEtM0bMSAoP8zKbsiFXDMf9sARBCvr0lVq5guk46nqeNeg4Xkp7U7NgyVdX09Qnno0wJt7qmNH0jvQSGTwEHY3ilmZIPxMnJHUZNsdQV80yrAUb2k3JTHBaZUsKmEGt96s0OJRbSG1cPdGxnDDVqaOE17nxe7TExhkxdK%2BSOCr10vHAUDl80fmPbQSNWtYwqgpL2tgU%2FavPSn%2FDarMsJX9jSHXJkEYhasascKDbl%2B%2FNQTbOaCJ8yUZvr%2F7XrrqPXir42Y3fQH7zccFheJnEJGJkpLpePitCMcBxdBhaUebk0lOd0rMeBIqPyNBFIxSMLDQhNMGOpcBZwzcuqs1Dt32G0mWkRcxXULM7z1CkOs9DHId82Lx82qpSV53E8jlZqRf6YBjvwBH%2BzrC4BYgH%2BZvXS8eEePu5xI8hvDfd5eR4X4naaRUrrjGPhs%2Frcol%2FiJoQpQ3ZeY5Me%2Fqq3%2BKKXy6jhMV57w%2B9nJQvX4FVekPO3zQAycDZq0iTBu4zgiifk5vi01urmtLQde5AwJ64g%3D%3D&Expires=1784755715) |

### Verify Editor and Go Tooling

Before changing the project, confirm that Neovim can see the required tooling.

Run these commands inside Neovim:

```vim
:checkhealth
:Mason
```

`checkhealth` confirms that Neovim can see the required tools, and `:Mason` shows whether developer
tools such as `gopls`, `goimports`, `gofumpt`, and `golangci-lint` are installed or available
. [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/62895318/d8d935f0-c4bb-4ced-bc41-1eef7861a540/neovim-setup.md?AWSAccessKeyId=ASIA2F3EMEYE3AK5EPXQ&Signature=uNgOA%2FVezjiKlCNebMP2EXIbjCc%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEBUaCXVzLWVhc3QtMSJIMEYCIQD9n1zqjkPL3vWa76o253opN%2FVa%2FAv7qk6s5vppiYQD8wIhAK9Xdb51zJ9ozPxDw0IL4w3eaBunbZxyRQN60b%2FXICNIKvwECN7%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEQARoMNjk5NzUzMzA5NzA1IgwIhihE%2FwtcQJKpsJkq0ASWR%2BOAhmXayo4nx12u%2BlrdmR7lZ9O6VhgiZiZLV3FYCD7iWufKMAuXN5zzhr01kDuR8SJVoX7GRzMZbrWTDlg%2Fv5nH%2FruxjOtbjvN9gmtrt6VAVPnYkiKk6h0InHtCgOK8IIJVpLOXIjH8vAJp6QiwhTRE0qQdDCoBmesbd%2B3lrPYNhKgSl9c6T5BKnqCpC34VsEBhIdFtQqL6mqV0Zbb%2FHMRXFA0CmeFc63JZ%2FfKKZ%2FN1%2FV0NxjDwIlYrt7%2B5fTFLy8pFgbURMMdhsbI%2BEHhBNkzBzt6CerOD7jDLDxnTRlR%2FfwhCTM0I3dCPQDR6dGo%2B0ZVNgKU2hYEI8goZ5%2F8KD6rpNmA%2Bf5sTTHz3MwdmPT2xiNOp%2B5PzOc69N2sT7XCiDGg1LaEtfWDPgxlXyb3s6Fc8GxfQ4YZ%2FH7g4jVqoI05HpMb6F2DcLQjzsFJzMLVT8o4lrQ1d3VdQAWV%2FPKm9JJVEtM0bMSAoP8zKbsiFXDMf9sARBCvr0lVq5guk46nqeNeg4Xkp7U7NgyVdX09Qnno0wJt7qmNH0jvQSGTwEHY3ilmZIPxMnJHUZNsdQV80yrAUb2k3JTHBaZUsKmEGt96s0OJRbSG1cPdGxnDDVqaOE17nxe7TExhkxdK%2BSOCr10vHAUDl80fmPbQSNWtYwqgpL2tgU%2FavPSn%2FDarMsJX9jSHXJkEYhasascKDbl%2B%2FNQTbOaCJ8yUZvr%2F7XrrqPXir42Y3fQH7zccFheJnEJGJkpLpePitCMcBxdBhaUebk0lOd0rMeBIqPyNBFIxSMLDQhNMGOpcBZwzcuqs1Dt32G0mWkRcxXULM7z1CkOs9DHId82Lx82qpSV53E8jlZqRf6YBjvwBH%2BzrC4BYgH%2BZvXS8eEePu5xI8hvDfd5eR4X4naaRUrrjGPhs%2Frcol%2FiJoQpQ3ZeY5Me%2Fqq3%2BKKXy6jhMV57w%2B9nJQvX4FVekPO3zQAycDZq0iTBu4zgiifk5vi01urmtLQde5AwJ64g%3D%3D&Expires=1784755715)

If `gopls` is not installed, install it first because it is the main Go language server used for
completion, diagnostics, hover, references, and definition lookup. [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/62895318/d8d935f0-c4bb-4ced-bc41-1eef7861a540/neovim-setup.md?AWSAccessKeyId=ASIA2F3EMEYE3AK5EPXQ&Signature=uNgOA%2FVezjiKlCNebMP2EXIbjCc%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEBUaCXVzLWVhc3QtMSJIMEYCIQD9n1zqjkPL3vWa76o253opN%2FVa%2FAv7qk6s5vppiYQD8wIhAK9Xdb51zJ9ozPxDw0IL4w3eaBunbZxyRQN60b%2FXICNIKvwECN7%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEQARoMNjk5NzUzMzA5NzA1IgwIhihE%2FwtcQJKpsJkq0ASWR%2BOAhmXayo4nx12u%2BlrdmR7lZ9O6VhgiZiZLV3FYCD7iWufKMAuXN5zzhr01kDuR8SJVoX7GRzMZbrWTDlg%2Fv5nH%2FruxjOtbjvN9gmtrt6VAVPnYkiKk6h0InHtCgOK8IIJVpLOXIjH8vAJp6QiwhTRE0qQdDCoBmesbd%2B3lrPYNhKgSl9c6T5BKnqCpC34VsEBhIdFtQqL6mqV0Zbb%2FHMRXFA0CmeFc63JZ%2FfKKZ%2FN1%2FV0NxjDwIlYrt7%2B5fTFLy8pFgbURMMdhsbI%2BEHhBNkzBzt6CerOD7jDLDxnTRlR%2FfwhCTM0I3dCPQDR6dGo%2B0ZVNgKU2hYEI8goZ5%2F8KD6rpNmA%2Bf5sTTHz3MwdmPT2xiNOp%2B5PzOc69N2sT7XCiDGg1LaEtfWDPgxlXyb3s6Fc8GxfQ4YZ%2FH7g4jVqoI05HpMb6F2DcLQjzsFJzMLVT8o4lrQ1d3VdQAWV%2FPKm9JJVEtM0bMSAoP8zKbsiFXDMf9sARBCvr0lVq5guk46nqeNeg4Xkp7U7NgyVdX09Qnno0wJt7qmNH0jvQSGTwEHY3ilmZIPxMnJHUZNsdQV80yrAUb2k3JTHBaZUsKmEGt96s0OJRbSG1cPdGxnDDVqaOE17nxe7TExhkxdK%2BSOCr10vHAUDl80fmPbQSNWtYwqgpL2tgU%2FavPSn%2FDarMsJX9jSHXJkEYhasascKDbl%2B%2FNQTbOaCJ8yUZvr%2F7XrrqPXir42Y3fQH7zccFheJnEJGJkpLpePitCMcBxdBhaUebk0lOd0rMeBIqPyNBFIxSMLDQhNMGOpcBZwzcuqs1Dt32G0mWkRcxXULM7z1CkOs9DHId82Lx82qpSV53E8jlZqRf6YBjvwBH%2BzrC4BYgH%2BZvXS8eEePu5xI8hvDfd5eR4X4naaRUrrjGPhs%2Frcol%2FiJoQpQ3ZeY5Me%2Fqq3%2BKKXy6jhMV57w%2B9nJQvX4FVekPO3zQAycDZq0iTBu4zgiifk5vi01urmtLQde5AwJ64g%3D%3D&Expires=1784755715)

### Upgrade Go Version and Module Dependencies

The repository README lists Go `>= 1.22` as a prerequisite, so testing the project against local Go
`1.26.5` is within the documented baseline for the repository. [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/images/62895318/b20c64d5-9127-4f77-beec-6edab9c5e30c/image.jpg?AWSAccessKeyId=ASIA2F3EMEYE3AK5EPXQ&Signature=JmpTdD9odV3rDmgDoqFqOH29i20%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEBUaCXVzLWVhc3QtMSJIMEYCIQD9n1zqjkPL3vWa76o253opN%2FVa%2FAv7qk6s5vppiYQD8wIhAK9Xdb51zJ9ozPxDw0IL4w3eaBunbZxyRQN60b%2FXICNIKvwECN7%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEQARoMNjk5NzUzMzA5NzA1IgwIhihE%2FwtcQJKpsJkq0ASWR%2BOAhmXayo4nx12u%2BlrdmR7lZ9O6VhgiZiZLV3FYCD7iWufKMAuXN5zzhr01kDuR8SJVoX7GRzMZbrWTDlg%2Fv5nH%2FruxjOtbjvN9gmtrt6VAVPnYkiKk6h0InHtCgOK8IIJVpLOXIjH8vAJp6QiwhTRE0qQdDCoBmesbd%2B3lrPYNhKgSl9c6T5BKnqCpC34VsEBhIdFtQqL6mqV0Zbb%2FHMRXFA0CmeFc63JZ%2FfKKZ%2FN1%2FV0NxjDwIlYrt7%2B5fTFLy8pFgbURMMdhsbI%2BEHhBNkzBzt6CerOD7jDLDxnTRlR%2FfwhCTM0I3dCPQDR6dGo%2B0ZVNgKU2hYEI8goZ5%2F8KD6rpNmA%2Bf5sTTHz3MwdmPT2xiNOp%2B5PzOc69N2sT7XCiDGg1LaEtfWDPgxlXyb3s6Fc8GxfQ4YZ%2FH7g4jVqoI05HpMb6F2DcLQjzsFJzMLVT8o4lrQ1d3VdQAWV%2FPKm9JJVEtM0bMSAoP8zKbsiFXDMf9sARBCvr0lVq5guk46nqeNeg4Xkp7U7NgyVdX09Qnno0wJt7qmNH0jvQSGTwEHY3ilmZIPxMnJHUZNsdQV80yrAUb2k3JTHBaZUsKmEGt96s0OJRbSG1cPdGxnDDVqaOE17nxe7TExhkxdK%2BSOCr10vHAUDl80fmPbQSNWtYwqgpL2tgU%2FavPSn%2FDarMsJX9jSHXJkEYhasascKDbl%2B%2FNQTbOaCJ8yUZvr%2F7XrrqPXir42Y3fQH7zccFheJnEJGJkpLpePitCMcBxdBhaUebk0lOd0rMeBIqPyNBFIxSMLDQhNMGOpcBZwzcuqs1Dt32G0mWkRcxXULM7z1CkOs9DHId82Lx82qpSV53E8jlZqRf6YBjvwBH%2BzrC4BYgH%2BZvXS8eEePu5xI8hvDfd5eR4X4naaRUrrjGPhs%2Frcol%2FiJoQpQ3ZeY5Me%2Fqq3%2BKKXy6jhMV57w%2B9nJQvX4FVekPO3zQAycDZq0iTBu4zgiifk5vi01urmtLQde5AwJ64g%3D%3D&Expires=1784755715)

Open `go.mod` and update the `go` directive to the Go major/minor version you want the project to
target. Then open a terminal inside Neovim:

```vim
:terminal
```

Run these commands from the project root:

```bash
go version
go list -u -m all
go get -u ./...
go mod tidy
go test ./...
```

This sequence checks the local Go version, lists available module upgrades, upgrades dependencies,
cleans up `go.mod` and `go.sum`, and verifies that the code still builds and tests successfully.

After the commands finish, review the resulting changes in:

- `go.mod`
- `go.sum`

A useful follow-up check is:

```bash
git status
git diff -- go.mod go.sum
```

### Upgrade Tool Versions in Makefile

The repository README uses `make install-tools` to install development tools including `protoc`,
`mockery`, `golines`, `goimports`, `gofumpt`, and `golangci-lint`. [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/images/62895318/b20c64d5-9127-4f77-beec-6edab9c5e30c/image.jpg?AWSAccessKeyId=ASIA2F3EMEYE3AK5EPXQ&Signature=JmpTdD9odV3rDmgDoqFqOH29i20%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEBUaCXVzLWVhc3QtMSJIMEYCIQD9n1zqjkPL3vWa76o253opN%2FVa%2FAv7qk6s5vppiYQD8wIhAK9Xdb51zJ9ozPxDw0IL4w3eaBunbZxyRQN60b%2FXICNIKvwECN7%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEQARoMNjk5NzUzMzA5NzA1IgwIhihE%2FwtcQJKpsJkq0ASWR%2BOAhmXayo4nx12u%2BlrdmR7lZ9O6VhgiZiZLV3FYCD7iWufKMAuXN5zzhr01kDuR8SJVoX7GRzMZbrWTDlg%2Fv5nH%2FruxjOtbjvN9gmtrt6VAVPnYkiKk6h0InHtCgOK8IIJVpLOXIjH8vAJp6QiwhTRE0qQdDCoBmesbd%2B3lrPYNhKgSl9c6T5BKnqCpC34VsEBhIdFtQqL6mqV0Zbb%2FHMRXFA0CmeFc63JZ%2FfKKZ%2FN1%2FV0NxjDwIlYrt7%2B5fTFLy8pFgbURMMdhsbI%2BEHhBNkzBzt6CerOD7jDLDxnTRlR%2FfwhCTM0I3dCPQDR6dGo%2B0ZVNgKU2hYEI8goZ5%2F8KD6rpNmA%2Bf5sTTHz3MwdmPT2xiNOp%2B5PzOc69N2sT7XCiDGg1LaEtfWDPgxlXyb3s6Fc8GxfQ4YZ%2FH7g4jVqoI05HpMb6F2DcLQjzsFJzMLVT8o4lrQ1d3VdQAWV%2FPKm9JJVEtM0bMSAoP8zKbsiFXDMf9sARBCvr0lVq5guk46nqeNeg4Xkp7U7NgyVdX09Qnno0wJt7qmNH0jvQSGTwEHY3ilmZIPxMnJHUZNsdQV80yrAUb2k3JTHBaZUsKmEGt96s0OJRbSG1cPdGxnDDVqaOE17nxe7TExhkxdK%2BSOCr10vHAUDl80fmPbQSNWtYwqgpL2tgU%2FavPSn%2FDarMsJX9jSHXJkEYhasascKDbl%2B%2FNQTbOaCJ8yUZvr%2F7XrrqPXir42Y3fQH7zccFheJnEJGJkpLpePitCMcBxdBhaUebk0lOd0rMeBIqPyNBFIxSMLDQhNMGOpcBZwzcuqs1Dt32G0mWkRcxXULM7z1CkOs9DHId82Lx82qpSV53E8jlZqRf6YBjvwBH%2BzrC4BYgH%2BZvXS8eEePu5xI8hvDfd5eR4X4naaRUrrjGPhs%2Frcol%2FiJoQpQ3ZeY5Me%2Fqq3%2BKKXy6jhMV57w%2B9nJQvX4FVekPO3zQAycDZq0iTBu4zgiifk5vi01urmtLQde5AwJ64g%3D%3D&Expires=1784755715)

Open the Makefile:

```vim
:e Makefile
```

Update the pinned tool version variables:

```make
PROTOC_GEN_GO_VERSION =
PROTOC_GEN_GO_GRPC_VERSION =
MOCKERY_VERSION =
GOIMPORTS_VERSION =
GOLINES_VERSION =
GOFUMPT_VERSION =
GOLANGCI_LINT_VERSION =
```

At minimum, the following newer versions were verified from current release sources:

```make
PROTOC_GEN_GO_VERSION = v1.36.10
PROTOC_GEN_GO_GRPC_VERSION = v1.6.2
GOLANGCI_LINT_VERSION = v2.12.2
```

These versions are based on the current protobuf-go, grpc-go, and golangci-lint release information
. [github](https://github.com/protocolbuffers/protobuf-go/releases)

Save the Makefile and reinstall the tools:

```bash
make install-tools
```

After installation, verify that the updated tools are available:

```bash
protoc-gen-go --version
protoc-gen-go-grpc --version
mockery --version
goimports -V
golines --version
gofumpt --version
golangci-lint version
```

Because `golangci-lint` support for new Go versions is not automatic, test it immediately after the
upgrade. [golangci-lint](https://golangci-lint.run/docs/welcome/faq/)

### Configure Local Environment

The repository README documents two main Docker Compose targets: one for PostgreSQL only and one for
the full stack with PostgreSQL plus the Campgrounds API. 

Start PostgreSQL first:

```bash
make compose-up-postgres
docker inspect --format="{{.State.Health.Status}}" postgres
```

The README expects the PostgreSQL container to become `healthy` before continuing. 

Then either run the application in your preferred way, or start the full stack:

```bash
make compose-up-all
```

This target is documented by the repository as the Docker Compose path for starting PostgreSQL and
the API together. 

A practical Neovim workflow is to keep one window on `README.md`, one on the source code or
Makefile, and one terminal buffer for running commands.

### Run Documented Project Commands

Once the environment is configured, execute the commands already documented in the repository README
to verify that your upgraded Go version and updated tooling did not break the normal workflow.

Start with the test targets:

```bash
make test
make test-integration
```

The README defines `make test` for unit tests and `make test-integration` for integration tests.

Then install `grpcurl`, which the README uses for service and message inspection:

```bash
go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest
grpcurl -version
```

Run the documented gRPC discovery commands:

```bash
grpcurl -plaintext localhost:8085 list
grpcurl -plaintext localhost:8085 describe campgroundspb.v1.CampgroundsService
grpcurl -plaintext localhost:8085 describe campgroundspb.v1.GetBookingRequest
```

The repository README also provides example `grpcurl` calls for functional API checks such as
creating a campsite, listing campsites, creating a booking, and retrieving a booking. Keep the
README open in one split and execute each example command from a Neovim terminal buffer.

Next, run the concurrent test scripts documented by the repository:

```bash
./tests/concurrent/create-bookings.sh 4 <CAMPSITE_ID> <START_DATE> <END_DATE>
./tests/concurrent/update-bookings.sh <CAMPSITE_ID> <BOOKING_ID> <START_DATE> <END_DATE>
```

These scripts are part of the repository's documented concurrency checks. 

### Use Git Inside Neovim

You can use Git entirely from within Neovim by opening a terminal buffer and running normal Git
commands there, while keeping the edited files open in other splits. This matches the Git workflow
already described elsewhere in this document. 

Open a terminal buffer:

```vim
:terminal
```

Create a branch for the upgrade work:

```bash
git checkout -b upgrade-go-1.26.5
```

Review and stage changes:

```bash
git status
git diff
git add go.mod go.sum Makefile README.md
```

Commit and push the branch:

```bash
git commit -m "Upgrade Go version and tooling"
git push -u origin upgrade-go-1.26.5
```

Common Git commands to use inside the Neovim terminal:

| Need                 | Command                                    |
|----------------------|--------------------------------------------|
| Show changed files   | `git status`                               |
| Review changes       | `git diff`                                 |
| Create a new branch  | `git checkout -b branch-name`              |
| Stage selected files | `git add go.mod go.sum Makefile README.md` |
| Stage all changes    | `git add .`                                |
| Commit changes       | `git commit -m "Describe the change"`      |
| Push a new branch    | `git push -u origin branch-name`           |
| Push later commits   | `git push`                                 |

If `lazygit` is installed, you can also use LazyVim's Lazygit integration for a more visual Git
workflow from within Neovim. 

### Suggested End-to-End Workflow

Use the following sequence for a realistic full test of the setup:

1. Clone the repository and open it with `nvim .` 
2. Open `go.mod`, `go.sum`, `Makefile`, and `README.md`
3. Run `:checkhealth` and `:Mason`
4. Update the `go` directive in `go.mod`
5. Run `go list -u -m all`, `go get -u ./...`, `go mod tidy`, and `go test ./...`
6. Update the Makefile tool version variables
7. Run `make install-tools`
8. Run `make compose-up-postgres` and wait for PostgreSQL to become healthy
9. Run `make compose-up-all` if you want the full stack
10. Run `make test` and `make test-integration`
11. Execute the documented `grpcurl` commands and concurrent test scripts
12. Create a Git branch, commit the changes, and push them to the remote repository
