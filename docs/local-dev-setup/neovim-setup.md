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

Yes — here is a revised version that matches the document's tone and structure more closely.

## Test Setup

After installing Neovim, LazyVim, Go support, and the required system tools, test the setup with a

real Go repository. This confirms that project opening, file navigation, Go tooling, and Makefile
commands all work together. [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/62895318/7a2d2b42-c0a9-4b19-bdfc-18c7175a528c/neovim-setup.md?AWSAccessKeyId=ASIA2F3EMEYE2TH465MQ&Signature=IjZy%2FxeV%2FHgL8aljToJ1z9Gpj04%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEP3%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEaCXVzLWVhc3QtMSJHMEUCIQDsVK0igLJ4A8ZXetCKxiTYmFx1KJENyKl9gLW58WZdiQIgKtSiCc%2BddhN%2Bltd%2FLcI%2F9e4KYSduN%2FD0Tswn5paUGJ8q%2FAQIxv%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FARABGgw2OTk3NTMzMDk3MDUiDNcJkjN6G6BepWe%2F5yrQBCXdCdgpu43bh7FJjAZKG4WSp2zQvz1bTCnaZWUcfQ7PfWMv%2FDilo8EWkPUTOokdKwkFfWZwyTnG%2B8lIC54Bt09GN8h8ax%2FiyDTYDAtUQhlD1HgbuuedTAZVqBUZjUeBFnyzLvhJq6ivswrJwNm5u9knPNQjIRrnqhOzMi5eGk3NIhONENw3F1e8n%2FIW%2BijqncQmv6JZ1vmZIgTA1GwmUN9aNDtf%2BPSJopj9NO93ZzbNW49NjYJ%2FpqFPt0E2fkpj%2F3CQVOYegmHlCP80EGRolrj2AhfBwNOe9dmgJluoJNTGwQ8LK0jNz7wvZPQAU1nyItYizb3WkOmOokeOxnjcFLfvoQOEFPZBw%2B6a58TY4jEHncMOPeaM3uN7muNDwWbiU9Yud02jrTKeYgaBj34Ju7LboojwYb5tE%2BIpmziJGKmbn%2FJ4NxCG7ot1mUapPwEIXXZyT%2F2qSLc9aHeF6GVmdvCgNaZkzalJS1w7qNegoua%2F8dMkQ%2BRbBpcjKSjOO0h4Idpd8yESDHS8kPVYx4UiuRKsPLj8DNAw5NTJdphUets5zlHzuO%2FXi9t8B1XhNYNnbJY2TFLTix0qoGCqaeVTAXvgGR1MtRsqeL336sCd6tyWll%2BhTmPJYtcjEartDN4w52a9QBG0CYsIOQWGzkqXZtJDtpXZO6J13l5vGfuLBYOFWQoJKQeCIMggLc7v7tEnrVrLEi1sKKrEyD%2BKv7f%2FeRweAQOdUo52MNKeAQ%2Fx4BsPXUzMKmM8dEY9VAxhIrrnEd%2BYB9L9ePE9kBftUvH1%2FHgw77H%2F0gY6mAEv8BcI%2BdQeaUj%2FYPXGhfDRas09J9SwSlh6ocNzO1MOLtmqwJDhugkyGAT3vL8GDh3VBvyDeo4ZkLwnZknn%2BV%2B9jd8Z4BrQvZsGEktLvBN8bi1VurGjrquqJzMFh8432xfFuVRQmrKHSWsLPKqGexQ6ntFoxmqAdX%2FDXW1%2FnKCjaXJNkiAVs1Wz3DR0k4x9Cr7vWxs8LJF%2Biw%3D%3D&Expires=1784669890)

The repository used below is:

```text
https://github.com/igor-baiborodine/campsite-booking-go
```

This project contains a Go gRPC API example application and documents common Make targets for local
setup and testing. [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/images/62895318/b20c64d5-9127-4f77-beec-6edab9c5e30c/image.jpg?AWSAccessKeyId=ASIA2F3EMEYE2TH465MQ&Signature=wvhex0DO9HmQ72w63%2BdQFALLSk8%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEP3%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEaCXVzLWVhc3QtMSJHMEUCIQDsVK0igLJ4A8ZXetCKxiTYmFx1KJENyKl9gLW58WZdiQIgKtSiCc%2BddhN%2Bltd%2FLcI%2F9e4KYSduN%2FD0Tswn5paUGJ8q%2FAQIxv%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FARABGgw2OTk3NTMzMDk3MDUiDNcJkjN6G6BepWe%2F5yrQBCXdCdgpu43bh7FJjAZKG4WSp2zQvz1bTCnaZWUcfQ7PfWMv%2FDilo8EWkPUTOokdKwkFfWZwyTnG%2B8lIC54Bt09GN8h8ax%2FiyDTYDAtUQhlD1HgbuuedTAZVqBUZjUeBFnyzLvhJq6ivswrJwNm5u9knPNQjIRrnqhOzMi5eGk3NIhONENw3F1e8n%2FIW%2BijqncQmv6JZ1vmZIgTA1GwmUN9aNDtf%2BPSJopj9NO93ZzbNW49NjYJ%2FpqFPt0E2fkpj%2F3CQVOYegmHlCP80EGRolrj2AhfBwNOe9dmgJluoJNTGwQ8LK0jNz7wvZPQAU1nyItYizb3WkOmOokeOxnjcFLfvoQOEFPZBw%2B6a58TY4jEHncMOPeaM3uN7muNDwWbiU9Yud02jrTKeYgaBj34Ju7LboojwYb5tE%2BIpmziJGKmbn%2FJ4NxCG7ot1mUapPwEIXXZyT%2F2qSLc9aHeF6GVmdvCgNaZkzalJS1w7qNegoua%2F8dMkQ%2BRbBpcjKSjOO0h4Idpd8yESDHS8kPVYx4UiuRKsPLj8DNAw5NTJdphUets5zlHzuO%2FXi9t8B1XhNYNnbJY2TFLTix0qoGCqaeVTAXvgGR1MtRsqeL336sCd6tyWll%2BhTmPJYtcjEartDN4w52a9QBG0CYsIOQWGzkqXZtJDtpXZO6J13l5vGfuLBYOFWQoJKQeCIMggLc7v7tEnrVrLEi1sKKrEyD%2BKv7f%2FeRweAQOdUo52MNKeAQ%2Fx4BsPXUzMKmM8dEY9VAxhIrrnEd%2BYB9L9ePE9kBftUvH1%2FHgw77H%2F0gY6mAEv8BcI%2BdQeaUj%2FYPXGhfDRas09J9SwSlh6ocNzO1MOLtmqwJDhugkyGAT3vL8GDh3VBvyDeo4ZkLwnZknn%2BV%2B9jd8Z4BrQvZsGEktLvBN8bi1VurGjrquqJzMFh8432xfFuVRQmrKHSWsLPKqGexQ6ntFoxmqAdX%2FDXW1%2FnKCjaXJNkiAVs1Wz3DR0k4x9Cr7vWxs8LJF%2Biw%3D%3D&Expires=1784669890)

### Clone and Open the Project

Clone the repository and open it from the project root:

```bash
git clone https://github.com/igor-baiborodine/campsite-booking-go.git
cd campsite-booking-go
nvim .
```

Opening the project with `nvim .` lets LazyVim treat the repository as the current working
directory, so file search, diagnostics, LSP, and project navigation operate against the whole
project. [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/62895318/7a2d2b42-c0a9-4b19-bdfc-18c7175a528c/neovim-setup.md?AWSAccessKeyId=ASIA2F3EMEYE2TH465MQ&Signature=IjZy%2FxeV%2FHgL8aljToJ1z9Gpj04%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEP3%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEaCXVzLWVhc3QtMSJHMEUCIQDsVK0igLJ4A8ZXetCKxiTYmFx1KJENyKl9gLW58WZdiQIgKtSiCc%2BddhN%2Bltd%2FLcI%2F9e4KYSduN%2FD0Tswn5paUGJ8q%2FAQIxv%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FARABGgw2OTk3NTMzMDk3MDUiDNcJkjN6G6BepWe%2F5yrQBCXdCdgpu43bh7FJjAZKG4WSp2zQvz1bTCnaZWUcfQ7PfWMv%2FDilo8EWkPUTOokdKwkFfWZwyTnG%2B8lIC54Bt09GN8h8ax%2FiyDTYDAtUQhlD1HgbuuedTAZVqBUZjUeBFnyzLvhJq6ivswrJwNm5u9knPNQjIRrnqhOzMi5eGk3NIhONENw3F1e8n%2FIW%2BijqncQmv6JZ1vmZIgTA1GwmUN9aNDtf%2BPSJopj9NO93ZzbNW49NjYJ%2FpqFPt0E2fkpj%2F3CQVOYegmHlCP80EGRolrj2AhfBwNOe9dmgJluoJNTGwQ8LK0jNz7wvZPQAU1nyItYizb3WkOmOokeOxnjcFLfvoQOEFPZBw%2B6a58TY4jEHncMOPeaM3uN7muNDwWbiU9Yud02jrTKeYgaBj34Ju7LboojwYb5tE%2BIpmziJGKmbn%2FJ4NxCG7ot1mUapPwEIXXZyT%2F2qSLc9aHeF6GVmdvCgNaZkzalJS1w7qNegoua%2F8dMkQ%2BRbBpcjKSjOO0h4Idpd8yESDHS8kPVYx4UiuRKsPLj8DNAw5NTJdphUets5zlHzuO%2FXi9t8B1XhNYNnbJY2TFLTix0qoGCqaeVTAXvgGR1MtRsqeL336sCd6tyWll%2BhTmPJYtcjEartDN4w52a9QBG0CYsIOQWGzkqXZtJDtpXZO6J13l5vGfuLBYOFWQoJKQeCIMggLc7v7tEnrVrLEi1sKKrEyD%2BKv7f%2FeRweAQOdUo52MNKeAQ%2Fx4BsPXUzMKmM8dEY9VAxhIrrnEd%2BYB9L9ePE9kBftUvH1%2FHgw77H%2F0gY6mAEv8BcI%2BdQeaUj%2FYPXGhfDRas09J9SwSlh6ocNzO1MOLtmqwJDhugkyGAT3vL8GDh3VBvyDeo4ZkLwnZknn%2BV%2B9jd8Z4BrQvZsGEktLvBN8bi1VurGjrquqJzMFh8432xfFuVRQmrKHSWsLPKqGexQ6ntFoxmqAdX%2FDXW1%2FnKCjaXJNkiAVs1Wz3DR0k4x9Cr7vWxs8LJF%2Biw%3D%3D&Expires=1784669890)

On first open, wait a few seconds for LazyVim to finish loading and for Go-related tools such as the
language server to attach to the project. [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/62895318/7a2d2b42-c0a9-4b19-bdfc-18c7175a528c/neovim-setup.md?AWSAccessKeyId=ASIA2F3EMEYE2TH465MQ&Signature=IjZy%2FxeV%2FHgL8aljToJ1z9Gpj04%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEP3%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEaCXVzLWVhc3QtMSJHMEUCIQDsVK0igLJ4A8ZXetCKxiTYmFx1KJENyKl9gLW58WZdiQIgKtSiCc%2BddhN%2Bltd%2FLcI%2F9e4KYSduN%2FD0Tswn5paUGJ8q%2FAQIxv%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FARABGgw2OTk3NTMzMDk3MDUiDNcJkjN6G6BepWe%2F5yrQBCXdCdgpu43bh7FJjAZKG4WSp2zQvz1bTCnaZWUcfQ7PfWMv%2FDilo8EWkPUTOokdKwkFfWZwyTnG%2B8lIC54Bt09GN8h8ax%2FiyDTYDAtUQhlD1HgbuuedTAZVqBUZjUeBFnyzLvhJq6ivswrJwNm5u9knPNQjIRrnqhOzMi5eGk3NIhONENw3F1e8n%2FIW%2BijqncQmv6JZ1vmZIgTA1GwmUN9aNDtf%2BPSJopj9NO93ZzbNW49NjYJ%2FpqFPt0E2fkpj%2F3CQVOYegmHlCP80EGRolrj2AhfBwNOe9dmgJluoJNTGwQ8LK0jNz7wvZPQAU1nyItYizb3WkOmOokeOxnjcFLfvoQOEFPZBw%2B6a58TY4jEHncMOPeaM3uN7muNDwWbiU9Yud02jrTKeYgaBj34Ju7LboojwYb5tE%2BIpmziJGKmbn%2FJ4NxCG7ot1mUapPwEIXXZyT%2F2qSLc9aHeF6GVmdvCgNaZkzalJS1w7qNegoua%2F8dMkQ%2BRbBpcjKSjOO0h4Idpd8yESDHS8kPVYx4UiuRKsPLj8DNAw5NTJdphUets5zlHzuO%2FXi9t8B1XhNYNnbJY2TFLTix0qoGCqaeVTAXvgGR1MtRsqeL336sCd6tyWll%2BhTmPJYtcjEartDN4w52a9QBG0CYsIOQWGzkqXZtJDtpXZO6J13l5vGfuLBYOFWQoJKQeCIMggLc7v7tEnrVrLEi1sKKrEyD%2BKv7f%2FeRweAQOdUo52MNKeAQ%2Fx4BsPXUzMKmM8dEY9VAxhIrrnEd%2BYB9L9ePE9kBftUvH1%2FHgw77H%2F0gY6mAEv8BcI%2BdQeaUj%2FYPXGhfDRas09J9SwSlh6ocNzO1MOLtmqwJDhugkyGAT3vL8GDh3VBvyDeo4ZkLwnZknn%2BV%2B9jd8Z4BrQvZsGEktLvBN8bi1VurGjrquqJzMFh8432xfFuVRQmrKHSWsLPKqGexQ6ntFoxmqAdX%2FDXW1%2FnKCjaXJNkiAVs1Wz3DR0k4x9Cr7vWxs8LJF%2Biw%3D%3D&Expires=1784669890)

### Verify Go Support

Once the repository is open, confirm that the Go setup is active.

Run these commands inside Neovim:

```vim
:checkhealth
:Mason
```

`checkhealth` helps confirm that Neovim can see required tools, and `:Mason` shows whether external
developer tools such as `gopls` are available or installed. [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/62895318/7a2d2b42-c0a9-4b19-bdfc-18c7175a528c/neovim-setup.md?AWSAccessKeyId=ASIA2F3EMEYE2TH465MQ&Signature=IjZy%2FxeV%2FHgL8aljToJ1z9Gpj04%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEP3%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEaCXVzLWVhc3QtMSJHMEUCIQDsVK0igLJ4A8ZXetCKxiTYmFx1KJENyKl9gLW58WZdiQIgKtSiCc%2BddhN%2Bltd%2FLcI%2F9e4KYSduN%2FD0Tswn5paUGJ8q%2FAQIxv%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FARABGgw2OTk3NTMzMDk3MDUiDNcJkjN6G6BepWe%2F5yrQBCXdCdgpu43bh7FJjAZKG4WSp2zQvz1bTCnaZWUcfQ7PfWMv%2FDilo8EWkPUTOokdKwkFfWZwyTnG%2B8lIC54Bt09GN8h8ax%2FiyDTYDAtUQhlD1HgbuuedTAZVqBUZjUeBFnyzLvhJq6ivswrJwNm5u9knPNQjIRrnqhOzMi5eGk3NIhONENw3F1e8n%2FIW%2BijqncQmv6JZ1vmZIgTA1GwmUN9aNDtf%2BPSJopj9NO93ZzbNW49NjYJ%2FpqFPt0E2fkpj%2F3CQVOYegmHlCP80EGRolrj2AhfBwNOe9dmgJluoJNTGwQ8LK0jNz7wvZPQAU1nyItYizb3WkOmOokeOxnjcFLfvoQOEFPZBw%2B6a58TY4jEHncMOPeaM3uN7muNDwWbiU9Yud02jrTKeYgaBj34Ju7LboojwYb5tE%2BIpmziJGKmbn%2FJ4NxCG7ot1mUapPwEIXXZyT%2F2qSLc9aHeF6GVmdvCgNaZkzalJS1w7qNegoua%2F8dMkQ%2BRbBpcjKSjOO0h4Idpd8yESDHS8kPVYx4UiuRKsPLj8DNAw5NTJdphUets5zlHzuO%2FXi9t8B1XhNYNnbJY2TFLTix0qoGCqaeVTAXvgGR1MtRsqeL336sCd6tyWll%2BhTmPJYtcjEartDN4w52a9QBG0CYsIOQWGzkqXZtJDtpXZO6J13l5vGfuLBYOFWQoJKQeCIMggLc7v7tEnrVrLEi1sKKrEyD%2BKv7f%2FeRweAQOdUo52MNKeAQ%2Fx4BsPXUzMKmM8dEY9VAxhIrrnEd%2BYB9L9ePE9kBftUvH1%2FHgw77H%2F0gY6mAEv8BcI%2BdQeaUj%2FYPXGhfDRas09J9SwSlh6ocNzO1MOLtmqwJDhugkyGAT3vL8GDh3VBvyDeo4ZkLwnZknn%2BV%2B9jd8Z4BrQvZsGEktLvBN8bi1VurGjrquqJzMFh8432xfFuVRQmrKHSWsLPKqGexQ6ntFoxmqAdX%2FDXW1%2FnKCjaXJNkiAVs1Wz3DR0k4x9Cr7vWxs8LJF%2Biw%3D%3D&Expires=1784669890)

If the repository has not been prepared yet, run the documented tool installation target from the
project root:

```bash
make install-tools
```

The repository README includes `make install-tools` as part of the local setup workflow. [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/images/62895318/b20c64d5-9127-4f77-beec-6edab9c5e30c/image.jpg?AWSAccessKeyId=ASIA2F3EMEYE2TH465MQ&Signature=wvhex0DO9HmQ72w63%2BdQFALLSk8%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEP3%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEaCXVzLWVhc3QtMSJHMEUCIQDsVK0igLJ4A8ZXetCKxiTYmFx1KJENyKl9gLW58WZdiQIgKtSiCc%2BddhN%2Bltd%2FLcI%2F9e4KYSduN%2FD0Tswn5paUGJ8q%2FAQIxv%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FARABGgw2OTk3NTMzMDk3MDUiDNcJkjN6G6BepWe%2F5yrQBCXdCdgpu43bh7FJjAZKG4WSp2zQvz1bTCnaZWUcfQ7PfWMv%2FDilo8EWkPUTOokdKwkFfWZwyTnG%2B8lIC54Bt09GN8h8ax%2FiyDTYDAtUQhlD1HgbuuedTAZVqBUZjUeBFnyzLvhJq6ivswrJwNm5u9knPNQjIRrnqhOzMi5eGk3NIhONENw3F1e8n%2FIW%2BijqncQmv6JZ1vmZIgTA1GwmUN9aNDtf%2BPSJopj9NO93ZzbNW49NjYJ%2FpqFPt0E2fkpj%2F3CQVOYegmHlCP80EGRolrj2AhfBwNOe9dmgJluoJNTGwQ8LK0jNz7wvZPQAU1nyItYizb3WkOmOokeOxnjcFLfvoQOEFPZBw%2B6a58TY4jEHncMOPeaM3uN7muNDwWbiU9Yud02jrTKeYgaBj34Ju7LboojwYb5tE%2BIpmziJGKmbn%2FJ4NxCG7ot1mUapPwEIXXZyT%2F2qSLc9aHeF6GVmdvCgNaZkzalJS1w7qNegoua%2F8dMkQ%2BRbBpcjKSjOO0h4Idpd8yESDHS8kPVYx4UiuRKsPLj8DNAw5NTJdphUets5zlHzuO%2FXi9t8B1XhNYNnbJY2TFLTix0qoGCqaeVTAXvgGR1MtRsqeL336sCd6tyWll%2BhTmPJYtcjEartDN4w52a9QBG0CYsIOQWGzkqXZtJDtpXZO6J13l5vGfuLBYOFWQoJKQeCIMggLc7v7tEnrVrLEi1sKKrEyD%2BKv7f%2FeRweAQOdUo52MNKeAQ%2Fx4BsPXUzMKmM8dEY9VAxhIrrnEd%2BYB9L9ePE9kBftUvH1%2FHgw77H%2F0gY6mAEv8BcI%2BdQeaUj%2FYPXGhfDRas09J9SwSlh6ocNzO1MOLtmqwJDhugkyGAT3vL8GDh3VBvyDeo4ZkLwnZknn%2BV%2B9jd8Z4BrQvZsGEktLvBN8bi1VurGjrquqJzMFh8432xfFuVRQmrKHSWsLPKqGexQ6ntFoxmqAdX%2FDXW1%2FnKCjaXJNkiAVs1Wz3DR0k4x9Cr7vWxs8LJF%2Biw%3D%3D&Expires=1784669890)

### Navigate Go Files

Use LazyVim's built-in file and search features to move around the repository. [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/62895318/7a2d2b42-c0a9-4b19-bdfc-18c7175a528c/neovim-setup.md?AWSAccessKeyId=ASIA2F3EMEYE2TH465MQ&Signature=IjZy%2FxeV%2FHgL8aljToJ1z9Gpj04%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEP3%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEaCXVzLWVhc3QtMSJHMEUCIQDsVK0igLJ4A8ZXetCKxiTYmFx1KJENyKl9gLW58WZdiQIgKtSiCc%2BddhN%2Bltd%2FLcI%2F9e4KYSduN%2FD0Tswn5paUGJ8q%2FAQIxv%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FARABGgw2OTk3NTMzMDk3MDUiDNcJkjN6G6BepWe%2F5yrQBCXdCdgpu43bh7FJjAZKG4WSp2zQvz1bTCnaZWUcfQ7PfWMv%2FDilo8EWkPUTOokdKwkFfWZwyTnG%2B8lIC54Bt09GN8h8ax%2FiyDTYDAtUQhlD1HgbuuedTAZVqBUZjUeBFnyzLvhJq6ivswrJwNm5u9knPNQjIRrnqhOzMi5eGk3NIhONENw3F1e8n%2FIW%2BijqncQmv6JZ1vmZIgTA1GwmUN9aNDtf%2BPSJopj9NO93ZzbNW49NjYJ%2FpqFPt0E2fkpj%2F3CQVOYegmHlCP80EGRolrj2AhfBwNOe9dmgJluoJNTGwQ8LK0jNz7wvZPQAU1nyItYizb3WkOmOokeOxnjcFLfvoQOEFPZBw%2B6a58TY4jEHncMOPeaM3uN7muNDwWbiU9Yud02jrTKeYgaBj34Ju7LboojwYb5tE%2BIpmziJGKmbn%2FJ4NxCG7ot1mUapPwEIXXZyT%2F2qSLc9aHeF6GVmdvCgNaZkzalJS1w7qNegoua%2F8dMkQ%2BRbBpcjKSjOO0h4Idpd8yESDHS8kPVYx4UiuRKsPLj8DNAw5NTJdphUets5zlHzuO%2FXi9t8B1XhNYNnbJY2TFLTix0qoGCqaeVTAXvgGR1MtRsqeL336sCd6tyWll%2BhTmPJYtcjEartDN4w52a9QBG0CYsIOQWGzkqXZtJDtpXZO6J13l5vGfuLBYOFWQoJKQeCIMggLc7v7tEnrVrLEi1sKKrEyD%2BKv7f%2FeRweAQOdUo52MNKeAQ%2Fx4BsPXUzMKmM8dEY9VAxhIrrnEd%2BYB9L9ePE9kBftUvH1%2FHgw77H%2F0gY6mAEv8BcI%2BdQeaUj%2FYPXGhfDRas09J9SwSlh6ocNzO1MOLtmqwJDhugkyGAT3vL8GDh3VBvyDeo4ZkLwnZknn%2BV%2B9jd8Z4BrQvZsGEktLvBN8bi1VurGjrquqJzMFh8432xfFuVRQmrKHSWsLPKqGexQ6ntFoxmqAdX%2FDXW1%2FnKCjaXJNkiAVs1Wz3DR0k4x9Cr7vWxs8LJF%2Biw%3D%3D&Expires=1784669890)

Common commands:

| Need                              | Command or Key                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                        |
|-----------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Open current directory as project | `nvim .`  [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/62895318/7a2d2b42-c0a9-4b19-bdfc-18c7175a528c/neovim-setup.md?AWSAccessKeyId=ASIA2F3EMEYE2TH465MQ&Signature=IjZy%2FxeV%2FHgL8aljToJ1z9Gpj04%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEP3%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEaCXVzLWVhc3QtMSJHMEUCIQDsVK0igLJ4A8ZXetCKxiTYmFx1KJENyKl9gLW58WZdiQIgKtSiCc%2BddhN%2Bltd%2FLcI%2F9e4KYSduN%2FD0Tswn5paUGJ8q%2FAQIxv%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FARABGgw2OTk3NTMzMDk3MDUiDNcJkjN6G6BepWe%2F5yrQBCXdCdgpu43bh7FJjAZKG4WSp2zQvz1bTCnaZWUcfQ7PfWMv%2FDilo8EWkPUTOokdKwkFfWZwyTnG%2B8lIC54Bt09GN8h8ax%2FiyDTYDAtUQhlD1HgbuuedTAZVqBUZjUeBFnyzLvhJq6ivswrJwNm5u9knPNQjIRrnqhOzMi5eGk3NIhONENw3F1e8n%2FIW%2BijqncQmv6JZ1vmZIgTA1GwmUN9aNDtf%2BPSJopj9NO93ZzbNW49NjYJ%2FpqFPt0E2fkpj%2F3CQVOYegmHlCP80EGRolrj2AhfBwNOe9dmgJluoJNTGwQ8LK0jNz7wvZPQAU1nyItYizb3WkOmOokeOxnjcFLfvoQOEFPZBw%2B6a58TY4jEHncMOPeaM3uN7muNDwWbiU9Yud02jrTKeYgaBj34Ju7LboojwYb5tE%2BIpmziJGKmbn%2FJ4NxCG7ot1mUapPwEIXXZyT%2F2qSLc9aHeF6GVmdvCgNaZkzalJS1w7qNegoua%2F8dMkQ%2BRbBpcjKSjOO0h4Idpd8yESDHS8kPVYx4UiuRKsPLj8DNAw5NTJdphUets5zlHzuO%2FXi9t8B1XhNYNnbJY2TFLTix0qoGCqaeVTAXvgGR1MtRsqeL336sCd6tyWll%2BhTmPJYtcjEartDN4w52a9QBG0CYsIOQWGzkqXZtJDtpXZO6J13l5vGfuLBYOFWQoJKQeCIMggLc7v7tEnrVrLEi1sKKrEyD%2BKv7f%2FeRweAQOdUo52MNKeAQ%2Fx4BsPXUzMKmM8dEY9VAxhIrrnEd%2BYB9L9ePE9kBftUvH1%2FHgw77H%2F0gY6mAEv8BcI%2BdQeaUj%2FYPXGhfDRas09J9SwSlh6ocNzO1MOLtmqwJDhugkyGAT3vL8GDh3VBvyDeo4ZkLwnZknn%2BV%2B9jd8Z4BrQvZsGEktLvBN8bi1VurGjrquqJzMFh8432xfFuVRQmrKHSWsLPKqGexQ6ntFoxmqAdX%2FDXW1%2FnKCjaXJNkiAVs1Wz3DR0k4x9Cr7vWxs8LJF%2Biw%3D%3D&Expires=1784669890)         |
| Find files                        | `<Space><Space>`  [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/62895318/7a2d2b42-c0a9-4b19-bdfc-18c7175a528c/neovim-setup.md?AWSAccessKeyId=ASIA2F3EMEYE2TH465MQ&Signature=IjZy%2FxeV%2FHgL8aljToJ1z9Gpj04%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEP3%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEaCXVzLWVhc3QtMSJHMEUCIQDsVK0igLJ4A8ZXetCKxiTYmFx1KJENyKl9gLW58WZdiQIgKtSiCc%2BddhN%2Bltd%2FLcI%2F9e4KYSduN%2FD0Tswn5paUGJ8q%2FAQIxv%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FARABGgw2OTk3NTMzMDk3MDUiDNcJkjN6G6BepWe%2F5yrQBCXdCdgpu43bh7FJjAZKG4WSp2zQvz1bTCnaZWUcfQ7PfWMv%2FDilo8EWkPUTOokdKwkFfWZwyTnG%2B8lIC54Bt09GN8h8ax%2FiyDTYDAtUQhlD1HgbuuedTAZVqBUZjUeBFnyzLvhJq6ivswrJwNm5u9knPNQjIRrnqhOzMi5eGk3NIhONENw3F1e8n%2FIW%2BijqncQmv6JZ1vmZIgTA1GwmUN9aNDtf%2BPSJopj9NO93ZzbNW49NjYJ%2FpqFPt0E2fkpj%2F3CQVOYegmHlCP80EGRolrj2AhfBwNOe9dmgJluoJNTGwQ8LK0jNz7wvZPQAU1nyItYizb3WkOmOokeOxnjcFLfvoQOEFPZBw%2B6a58TY4jEHncMOPeaM3uN7muNDwWbiU9Yud02jrTKeYgaBj34Ju7LboojwYb5tE%2BIpmziJGKmbn%2FJ4NxCG7ot1mUapPwEIXXZyT%2F2qSLc9aHeF6GVmdvCgNaZkzalJS1w7qNegoua%2F8dMkQ%2BRbBpcjKSjOO0h4Idpd8yESDHS8kPVYx4UiuRKsPLj8DNAw5NTJdphUets5zlHzuO%2FXi9t8B1XhNYNnbJY2TFLTix0qoGCqaeVTAXvgGR1MtRsqeL336sCd6tyWll%2BhTmPJYtcjEartDN4w52a9QBG0CYsIOQWGzkqXZtJDtpXZO6J13l5vGfuLBYOFWQoJKQeCIMggLc7v7tEnrVrLEi1sKKrEyD%2BKv7f%2FeRweAQOdUo52MNKeAQ%2Fx4BsPXUzMKmM8dEY9VAxhIrrnEd%2BYB9L9ePE9kBftUvH1%2FHgw77H%2F0gY6mAEv8BcI%2BdQeaUj%2FYPXGhfDRas09J9SwSlh6ocNzO1MOLtmqwJDhugkyGAT3vL8GDh3VBvyDeo4ZkLwnZknn%2BV%2B9jd8Z4BrQvZsGEktLvBN8bi1VurGjrquqJzMFh8432xfFuVRQmrKHSWsLPKqGexQ6ntFoxmqAdX%2FDXW1%2FnKCjaXJNkiAVs1Wz3DR0k4x9Cr7vWxs8LJF%2Biw%3D%3D&Expires=1784669890) |
| Open file explorer                | `<Space>e`  [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/62895318/7a2d2b42-c0a9-4b19-bdfc-18c7175a528c/neovim-setup.md?AWSAccessKeyId=ASIA2F3EMEYE2TH465MQ&Signature=IjZy%2FxeV%2FHgL8aljToJ1z9Gpj04%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEP3%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEaCXVzLWVhc3QtMSJHMEUCIQDsVK0igLJ4A8ZXetCKxiTYmFx1KJENyKl9gLW58WZdiQIgKtSiCc%2BddhN%2Bltd%2FLcI%2F9e4KYSduN%2FD0Tswn5paUGJ8q%2FAQIxv%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FARABGgw2OTk3NTMzMDk3MDUiDNcJkjN6G6BepWe%2F5yrQBCXdCdgpu43bh7FJjAZKG4WSp2zQvz1bTCnaZWUcfQ7PfWMv%2FDilo8EWkPUTOokdKwkFfWZwyTnG%2B8lIC54Bt09GN8h8ax%2FiyDTYDAtUQhlD1HgbuuedTAZVqBUZjUeBFnyzLvhJq6ivswrJwNm5u9knPNQjIRrnqhOzMi5eGk3NIhONENw3F1e8n%2FIW%2BijqncQmv6JZ1vmZIgTA1GwmUN9aNDtf%2BPSJopj9NO93ZzbNW49NjYJ%2FpqFPt0E2fkpj%2F3CQVOYegmHlCP80EGRolrj2AhfBwNOe9dmgJluoJNTGwQ8LK0jNz7wvZPQAU1nyItYizb3WkOmOokeOxnjcFLfvoQOEFPZBw%2B6a58TY4jEHncMOPeaM3uN7muNDwWbiU9Yud02jrTKeYgaBj34Ju7LboojwYb5tE%2BIpmziJGKmbn%2FJ4NxCG7ot1mUapPwEIXXZyT%2F2qSLc9aHeF6GVmdvCgNaZkzalJS1w7qNegoua%2F8dMkQ%2BRbBpcjKSjOO0h4Idpd8yESDHS8kPVYx4UiuRKsPLj8DNAw5NTJdphUets5zlHzuO%2FXi9t8B1XhNYNnbJY2TFLTix0qoGCqaeVTAXvgGR1MtRsqeL336sCd6tyWll%2BhTmPJYtcjEartDN4w52a9QBG0CYsIOQWGzkqXZtJDtpXZO6J13l5vGfuLBYOFWQoJKQeCIMggLc7v7tEnrVrLEi1sKKrEyD%2BKv7f%2FeRweAQOdUo52MNKeAQ%2Fx4BsPXUzMKmM8dEY9VAxhIrrnEd%2BYB9L9ePE9kBftUvH1%2FHgw77H%2F0gY6mAEv8BcI%2BdQeaUj%2FYPXGhfDRas09J9SwSlh6ocNzO1MOLtmqwJDhugkyGAT3vL8GDh3VBvyDeo4ZkLwnZknn%2BV%2B9jd8Z4BrQvZsGEktLvBN8bi1VurGjrquqJzMFh8432xfFuVRQmrKHSWsLPKqGexQ6ntFoxmqAdX%2FDXW1%2FnKCjaXJNkiAVs1Wz3DR0k4x9Cr7vWxs8LJF%2Biw%3D%3D&Expires=1784669890)       |
| Search project text               | `<Space>sg`  [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/62895318/7a2d2b42-c0a9-4b19-bdfc-18c7175a528c/neovim-setup.md?AWSAccessKeyId=ASIA2F3EMEYE2TH465MQ&Signature=IjZy%2FxeV%2FHgL8aljToJ1z9Gpj04%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEP3%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEaCXVzLWVhc3QtMSJHMEUCIQDsVK0igLJ4A8ZXetCKxiTYmFx1KJENyKl9gLW58WZdiQIgKtSiCc%2BddhN%2Bltd%2FLcI%2F9e4KYSduN%2FD0Tswn5paUGJ8q%2FAQIxv%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FARABGgw2OTk3NTMzMDk3MDUiDNcJkjN6G6BepWe%2F5yrQBCXdCdgpu43bh7FJjAZKG4WSp2zQvz1bTCnaZWUcfQ7PfWMv%2FDilo8EWkPUTOokdKwkFfWZwyTnG%2B8lIC54Bt09GN8h8ax%2FiyDTYDAtUQhlD1HgbuuedTAZVqBUZjUeBFnyzLvhJq6ivswrJwNm5u9knPNQjIRrnqhOzMi5eGk3NIhONENw3F1e8n%2FIW%2BijqncQmv6JZ1vmZIgTA1GwmUN9aNDtf%2BPSJopj9NO93ZzbNW49NjYJ%2FpqFPt0E2fkpj%2F3CQVOYegmHlCP80EGRolrj2AhfBwNOe9dmgJluoJNTGwQ8LK0jNz7wvZPQAU1nyItYizb3WkOmOokeOxnjcFLfvoQOEFPZBw%2B6a58TY4jEHncMOPeaM3uN7muNDwWbiU9Yud02jrTKeYgaBj34Ju7LboojwYb5tE%2BIpmziJGKmbn%2FJ4NxCG7ot1mUapPwEIXXZyT%2F2qSLc9aHeF6GVmdvCgNaZkzalJS1w7qNegoua%2F8dMkQ%2BRbBpcjKSjOO0h4Idpd8yESDHS8kPVYx4UiuRKsPLj8DNAw5NTJdphUets5zlHzuO%2FXi9t8B1XhNYNnbJY2TFLTix0qoGCqaeVTAXvgGR1MtRsqeL336sCd6tyWll%2BhTmPJYtcjEartDN4w52a9QBG0CYsIOQWGzkqXZtJDtpXZO6J13l5vGfuLBYOFWQoJKQeCIMggLc7v7tEnrVrLEi1sKKrEyD%2BKv7f%2FeRweAQOdUo52MNKeAQ%2Fx4BsPXUzMKmM8dEY9VAxhIrrnEd%2BYB9L9ePE9kBftUvH1%2FHgw77H%2F0gY6mAEv8BcI%2BdQeaUj%2FYPXGhfDRas09J9SwSlh6ocNzO1MOLtmqwJDhugkyGAT3vL8GDh3VBvyDeo4ZkLwnZknn%2BV%2B9jd8Z4BrQvZsGEktLvBN8bi1VurGjrquqJzMFh8432xfFuVRQmrKHSWsLPKqGexQ6ntFoxmqAdX%2FDXW1%2FnKCjaXJNkiAVs1Wz3DR0k4x9Cr7vWxs8LJF%2Biw%3D%3D&Expires=1784669890)      |
| Format current file               | `<Space>cf`  [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/62895318/7a2d2b42-c0a9-4b19-bdfc-18c7175a528c/neovim-setup.md?AWSAccessKeyId=ASIA2F3EMEYE2TH465MQ&Signature=IjZy%2FxeV%2FHgL8aljToJ1z9Gpj04%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEP3%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEaCXVzLWVhc3QtMSJHMEUCIQDsVK0igLJ4A8ZXetCKxiTYmFx1KJENyKl9gLW58WZdiQIgKtSiCc%2BddhN%2Bltd%2FLcI%2F9e4KYSduN%2FD0Tswn5paUGJ8q%2FAQIxv%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FARABGgw2OTk3NTMzMDk3MDUiDNcJkjN6G6BepWe%2F5yrQBCXdCdgpu43bh7FJjAZKG4WSp2zQvz1bTCnaZWUcfQ7PfWMv%2FDilo8EWkPUTOokdKwkFfWZwyTnG%2B8lIC54Bt09GN8h8ax%2FiyDTYDAtUQhlD1HgbuuedTAZVqBUZjUeBFnyzLvhJq6ivswrJwNm5u9knPNQjIRrnqhOzMi5eGk3NIhONENw3F1e8n%2FIW%2BijqncQmv6JZ1vmZIgTA1GwmUN9aNDtf%2BPSJopj9NO93ZzbNW49NjYJ%2FpqFPt0E2fkpj%2F3CQVOYegmHlCP80EGRolrj2AhfBwNOe9dmgJluoJNTGwQ8LK0jNz7wvZPQAU1nyItYizb3WkOmOokeOxnjcFLfvoQOEFPZBw%2B6a58TY4jEHncMOPeaM3uN7muNDwWbiU9Yud02jrTKeYgaBj34Ju7LboojwYb5tE%2BIpmziJGKmbn%2FJ4NxCG7ot1mUapPwEIXXZyT%2F2qSLc9aHeF6GVmdvCgNaZkzalJS1w7qNegoua%2F8dMkQ%2BRbBpcjKSjOO0h4Idpd8yESDHS8kPVYx4UiuRKsPLj8DNAw5NTJdphUets5zlHzuO%2FXi9t8B1XhNYNnbJY2TFLTix0qoGCqaeVTAXvgGR1MtRsqeL336sCd6tyWll%2BhTmPJYtcjEartDN4w52a9QBG0CYsIOQWGzkqXZtJDtpXZO6J13l5vGfuLBYOFWQoJKQeCIMggLc7v7tEnrVrLEi1sKKrEyD%2BKv7f%2FeRweAQOdUo52MNKeAQ%2Fx4BsPXUzMKmM8dEY9VAxhIrrnEd%2BYB9L9ePE9kBftUvH1%2FHgw77H%2F0gY6mAEv8BcI%2BdQeaUj%2FYPXGhfDRas09J9SwSlh6ocNzO1MOLtmqwJDhugkyGAT3vL8GDh3VBvyDeo4ZkLwnZknn%2BV%2B9jd8Z4BrQvZsGEktLvBN8bi1VurGjrquqJzMFh8432xfFuVRQmrKHSWsLPKqGexQ6ntFoxmqAdX%2FDXW1%2FnKCjaXJNkiAVs1Wz3DR0k4x9Cr7vWxs8LJF%2Biw%3D%3D&Expires=1784669890)      |
| Show diagnostics                  | `<Space>xx`  [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/62895318/7a2d2b42-c0a9-4b19-bdfc-18c7175a528c/neovim-setup.md?AWSAccessKeyId=ASIA2F3EMEYE2TH465MQ&Signature=IjZy%2FxeV%2FHgL8aljToJ1z9Gpj04%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEP3%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEaCXVzLWVhc3QtMSJHMEUCIQDsVK0igLJ4A8ZXetCKxiTYmFx1KJENyKl9gLW58WZdiQIgKtSiCc%2BddhN%2Bltd%2FLcI%2F9e4KYSduN%2FD0Tswn5paUGJ8q%2FAQIxv%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FARABGgw2OTk3NTMzMDk3MDUiDNcJkjN6G6BepWe%2F5yrQBCXdCdgpu43bh7FJjAZKG4WSp2zQvz1bTCnaZWUcfQ7PfWMv%2FDilo8EWkPUTOokdKwkFfWZwyTnG%2B8lIC54Bt09GN8h8ax%2FiyDTYDAtUQhlD1HgbuuedTAZVqBUZjUeBFnyzLvhJq6ivswrJwNm5u9knPNQjIRrnqhOzMi5eGk3NIhONENw3F1e8n%2FIW%2BijqncQmv6JZ1vmZIgTA1GwmUN9aNDtf%2BPSJopj9NO93ZzbNW49NjYJ%2FpqFPt0E2fkpj%2F3CQVOYegmHlCP80EGRolrj2AhfBwNOe9dmgJluoJNTGwQ8LK0jNz7wvZPQAU1nyItYizb3WkOmOokeOxnjcFLfvoQOEFPZBw%2B6a58TY4jEHncMOPeaM3uN7muNDwWbiU9Yud02jrTKeYgaBj34Ju7LboojwYb5tE%2BIpmziJGKmbn%2FJ4NxCG7ot1mUapPwEIXXZyT%2F2qSLc9aHeF6GVmdvCgNaZkzalJS1w7qNegoua%2F8dMkQ%2BRbBpcjKSjOO0h4Idpd8yESDHS8kPVYx4UiuRKsPLj8DNAw5NTJdphUets5zlHzuO%2FXi9t8B1XhNYNnbJY2TFLTix0qoGCqaeVTAXvgGR1MtRsqeL336sCd6tyWll%2BhTmPJYtcjEartDN4w52a9QBG0CYsIOQWGzkqXZtJDtpXZO6J13l5vGfuLBYOFWQoJKQeCIMggLc7v7tEnrVrLEi1sKKrEyD%2BKv7f%2FeRweAQOdUo52MNKeAQ%2Fx4BsPXUzMKmM8dEY9VAxhIrrnEd%2BYB9L9ePE9kBftUvH1%2FHgw77H%2F0gY6mAEv8BcI%2BdQeaUj%2FYPXGhfDRas09J9SwSlh6ocNzO1MOLtmqwJDhugkyGAT3vL8GDh3VBvyDeo4ZkLwnZknn%2BV%2B9jd8Z4BrQvZsGEktLvBN8bi1VurGjrquqJzMFh8432xfFuVRQmrKHSWsLPKqGexQ6ntFoxmqAdX%2FDXW1%2FnKCjaXJNkiAVs1Wz3DR0k4x9Cr7vWxs8LJF%2Biw%3D%3D&Expires=1784669890)      |

A simple first navigation exercise is:

1. Open `go.mod` with `<Space><Space>`.
2. Open a Go source file from `cmd/`, `internal/`, or another project folder.
3. Move the cursor onto a function, type, or interface name.
4. Use LazyVim's LSP actions for definition lookup, hover documentation, rename, code actions, and
   diagnostics as described earlier in the document. [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/62895318/7a2d2b42-c0a9-4b19-bdfc-18c7175a528c/neovim-setup.md?AWSAccessKeyId=ASIA2F3EMEYE2TH465MQ&Signature=IjZy%2FxeV%2FHgL8aljToJ1z9Gpj04%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEP3%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEaCXVzLWVhc3QtMSJHMEUCIQDsVK0igLJ4A8ZXetCKxiTYmFx1KJENyKl9gLW58WZdiQIgKtSiCc%2BddhN%2Bltd%2FLcI%2F9e4KYSduN%2FD0Tswn5paUGJ8q%2FAQIxv%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FARABGgw2OTk3NTMzMDk3MDUiDNcJkjN6G6BepWe%2F5yrQBCXdCdgpu43bh7FJjAZKG4WSp2zQvz1bTCnaZWUcfQ7PfWMv%2FDilo8EWkPUTOokdKwkFfWZwyTnG%2B8lIC54Bt09GN8h8ax%2FiyDTYDAtUQhlD1HgbuuedTAZVqBUZjUeBFnyzLvhJq6ivswrJwNm5u9knPNQjIRrnqhOzMi5eGk3NIhONENw3F1e8n%2FIW%2BijqncQmv6JZ1vmZIgTA1GwmUN9aNDtf%2BPSJopj9NO93ZzbNW49NjYJ%2FpqFPt0E2fkpj%2F3CQVOYegmHlCP80EGRolrj2AhfBwNOe9dmgJluoJNTGwQ8LK0jNz7wvZPQAU1nyItYizb3WkOmOokeOxnjcFLfvoQOEFPZBw%2B6a58TY4jEHncMOPeaM3uN7muNDwWbiU9Yud02jrTKeYgaBj34Ju7LboojwYb5tE%2BIpmziJGKmbn%2FJ4NxCG7ot1mUapPwEIXXZyT%2F2qSLc9aHeF6GVmdvCgNaZkzalJS1w7qNegoua%2F8dMkQ%2BRbBpcjKSjOO0h4Idpd8yESDHS8kPVYx4UiuRKsPLj8DNAw5NTJdphUets5zlHzuO%2FXi9t8B1XhNYNnbJY2TFLTix0qoGCqaeVTAXvgGR1MtRsqeL336sCd6tyWll%2BhTmPJYtcjEartDN4w52a9QBG0CYsIOQWGzkqXZtJDtpXZO6J13l5vGfuLBYOFWQoJKQeCIMggLc7v7tEnrVrLEi1sKKrEyD%2BKv7f%2FeRweAQOdUo52MNKeAQ%2Fx4BsPXUzMKmM8dEY9VAxhIrrnEd%2BYB9L9ePE9kBftUvH1%2FHgw77H%2F0gY6mAEv8BcI%2BdQeaUj%2FYPXGhfDRas09J9SwSlh6ocNzO1MOLtmqwJDhugkyGAT3vL8GDh3VBvyDeo4ZkLwnZknn%2BV%2B9jd8Z4BrQvZsGEktLvBN8bi1VurGjrquqJzMFh8432xfFuVRQmrKHSWsLPKqGexQ6ntFoxmqAdX%2FDXW1%2FnKCjaXJNkiAVs1Wz3DR0k4x9Cr7vWxs8LJF%2Biw%3D%3D&Expires=1784669890)

This is enough to confirm that project search, syntax highlighting, and Go language features are
working correctly. [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/62895318/7a2d2b42-c0a9-4b19-bdfc-18c7175a528c/neovim-setup.md?AWSAccessKeyId=ASIA2F3EMEYE2TH465MQ&Signature=IjZy%2FxeV%2FHgL8aljToJ1z9Gpj04%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEP3%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEaCXVzLWVhc3QtMSJHMEUCIQDsVK0igLJ4A8ZXetCKxiTYmFx1KJENyKl9gLW58WZdiQIgKtSiCc%2BddhN%2Bltd%2FLcI%2F9e4KYSduN%2FD0Tswn5paUGJ8q%2FAQIxv%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FARABGgw2OTk3NTMzMDk3MDUiDNcJkjN6G6BepWe%2F5yrQBCXdCdgpu43bh7FJjAZKG4WSp2zQvz1bTCnaZWUcfQ7PfWMv%2FDilo8EWkPUTOokdKwkFfWZwyTnG%2B8lIC54Bt09GN8h8ax%2FiyDTYDAtUQhlD1HgbuuedTAZVqBUZjUeBFnyzLvhJq6ivswrJwNm5u9knPNQjIRrnqhOzMi5eGk3NIhONENw3F1e8n%2FIW%2BijqncQmv6JZ1vmZIgTA1GwmUN9aNDtf%2BPSJopj9NO93ZzbNW49NjYJ%2FpqFPt0E2fkpj%2F3CQVOYegmHlCP80EGRolrj2AhfBwNOe9dmgJluoJNTGwQ8LK0jNz7wvZPQAU1nyItYizb3WkOmOokeOxnjcFLfvoQOEFPZBw%2B6a58TY4jEHncMOPeaM3uN7muNDwWbiU9Yud02jrTKeYgaBj34Ju7LboojwYb5tE%2BIpmziJGKmbn%2FJ4NxCG7ot1mUapPwEIXXZyT%2F2qSLc9aHeF6GVmdvCgNaZkzalJS1w7qNegoua%2F8dMkQ%2BRbBpcjKSjOO0h4Idpd8yESDHS8kPVYx4UiuRKsPLj8DNAw5NTJdphUets5zlHzuO%2FXi9t8B1XhNYNnbJY2TFLTix0qoGCqaeVTAXvgGR1MtRsqeL336sCd6tyWll%2BhTmPJYtcjEartDN4w52a9QBG0CYsIOQWGzkqXZtJDtpXZO6J13l5vGfuLBYOFWQoJKQeCIMggLc7v7tEnrVrLEi1sKKrEyD%2BKv7f%2FeRweAQOdUo52MNKeAQ%2Fx4BsPXUzMKmM8dEY9VAxhIrrnEd%2BYB9L9ePE9kBftUvH1%2FHgw77H%2F0gY6mAEv8BcI%2BdQeaUj%2FYPXGhfDRas09J9SwSlh6ocNzO1MOLtmqwJDhugkyGAT3vL8GDh3VBvyDeo4ZkLwnZknn%2BV%2B9jd8Z4BrQvZsGEktLvBN8bi1VurGjrquqJzMFh8432xfFuVRQmrKHSWsLPKqGexQ6ntFoxmqAdX%2FDXW1%2FnKCjaXJNkiAVs1Wz3DR0k4x9Cr7vWxs8LJF%2Biw%3D%3D&Expires=1784669890)

### Run Makefile Targets

This repository includes documented Make targets for setup, local services, and testing. [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/images/62895318/b20c64d5-9127-4f77-beec-6edab9c5e30c/image.jpg?AWSAccessKeyId=ASIA2F3EMEYE2TH465MQ&Signature=wvhex0DO9HmQ72w63%2BdQFALLSk8%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEP3%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEaCXVzLWVhc3QtMSJHMEUCIQDsVK0igLJ4A8ZXetCKxiTYmFx1KJENyKl9gLW58WZdiQIgKtSiCc%2BddhN%2Bltd%2FLcI%2F9e4KYSduN%2FD0Tswn5paUGJ8q%2FAQIxv%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FARABGgw2OTk3NTMzMDk3MDUiDNcJkjN6G6BepWe%2F5yrQBCXdCdgpu43bh7FJjAZKG4WSp2zQvz1bTCnaZWUcfQ7PfWMv%2FDilo8EWkPUTOokdKwkFfWZwyTnG%2B8lIC54Bt09GN8h8ax%2FiyDTYDAtUQhlD1HgbuuedTAZVqBUZjUeBFnyzLvhJq6ivswrJwNm5u9knPNQjIRrnqhOzMi5eGk3NIhONENw3F1e8n%2FIW%2BijqncQmv6JZ1vmZIgTA1GwmUN9aNDtf%2BPSJopj9NO93ZzbNW49NjYJ%2FpqFPt0E2fkpj%2F3CQVOYegmHlCP80EGRolrj2AhfBwNOe9dmgJluoJNTGwQ8LK0jNz7wvZPQAU1nyItYizb3WkOmOokeOxnjcFLfvoQOEFPZBw%2B6a58TY4jEHncMOPeaM3uN7muNDwWbiU9Yud02jrTKeYgaBj34Ju7LboojwYb5tE%2BIpmziJGKmbn%2FJ4NxCG7ot1mUapPwEIXXZyT%2F2qSLc9aHeF6GVmdvCgNaZkzalJS1w7qNegoua%2F8dMkQ%2BRbBpcjKSjOO0h4Idpd8yESDHS8kPVYx4UiuRKsPLj8DNAw5NTJdphUets5zlHzuO%2FXi9t8B1XhNYNnbJY2TFLTix0qoGCqaeVTAXvgGR1MtRsqeL336sCd6tyWll%2BhTmPJYtcjEartDN4w52a9QBG0CYsIOQWGzkqXZtJDtpXZO6J13l5vGfuLBYOFWQoJKQeCIMggLc7v7tEnrVrLEi1sKKrEyD%2BKv7f%2FeRweAQOdUo52MNKeAQ%2Fx4BsPXUzMKmM8dEY9VAxhIrrnEd%2BYB9L9ePE9kBftUvH1%2FHgw77H%2F0gY6mAEv8BcI%2BdQeaUj%2FYPXGhfDRas09J9SwSlh6ocNzO1MOLtmqwJDhugkyGAT3vL8GDh3VBvyDeo4ZkLwnZknn%2BV%2B9jd8Z4BrQvZsGEktLvBN8bi1VurGjrquqJzMFh8432xfFuVRQmrKHSWsLPKqGexQ6ntFoxmqAdX%2FDXW1%2FnKCjaXJNkiAVs1Wz3DR0k4x9Cr7vWxs8LJF%2Biw%3D%3D&Expires=1784669890)
You can run them either in your normal terminal or from a terminal opened inside Neovim.

To open a terminal inside Neovim, run:

```vim
:terminal
```

Then execute targets from the project root, for example:

```bash
make test
```

Useful targets from this repository include:

- `make install-tools` [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/images/62895318/b20c64d5-9127-4f77-beec-6edab9c5e30c/image.jpg?AWSAccessKeyId=ASIA2F3EMEYE2TH465MQ&Signature=wvhex0DO9HmQ72w63%2BdQFALLSk8%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEP3%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEaCXVzLWVhc3QtMSJHMEUCIQDsVK0igLJ4A8ZXetCKxiTYmFx1KJENyKl9gLW58WZdiQIgKtSiCc%2BddhN%2Bltd%2FLcI%2F9e4KYSduN%2FD0Tswn5paUGJ8q%2FAQIxv%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FARABGgw2OTk3NTMzMDk3MDUiDNcJkjN6G6BepWe%2F5yrQBCXdCdgpu43bh7FJjAZKG4WSp2zQvz1bTCnaZWUcfQ7PfWMv%2FDilo8EWkPUTOokdKwkFfWZwyTnG%2B8lIC54Bt09GN8h8ax%2FiyDTYDAtUQhlD1HgbuuedTAZVqBUZjUeBFnyzLvhJq6ivswrJwNm5u9knPNQjIRrnqhOzMi5eGk3NIhONENw3F1e8n%2FIW%2BijqncQmv6JZ1vmZIgTA1GwmUN9aNDtf%2BPSJopj9NO93ZzbNW49NjYJ%2FpqFPt0E2fkpj%2F3CQVOYegmHlCP80EGRolrj2AhfBwNOe9dmgJluoJNTGwQ8LK0jNz7wvZPQAU1nyItYizb3WkOmOokeOxnjcFLfvoQOEFPZBw%2B6a58TY4jEHncMOPeaM3uN7muNDwWbiU9Yud02jrTKeYgaBj34Ju7LboojwYb5tE%2BIpmziJGKmbn%2FJ4NxCG7ot1mUapPwEIXXZyT%2F2qSLc9aHeF6GVmdvCgNaZkzalJS1w7qNegoua%2F8dMkQ%2BRbBpcjKSjOO0h4Idpd8yESDHS8kPVYx4UiuRKsPLj8DNAw5NTJdphUets5zlHzuO%2FXi9t8B1XhNYNnbJY2TFLTix0qoGCqaeVTAXvgGR1MtRsqeL336sCd6tyWll%2BhTmPJYtcjEartDN4w52a9QBG0CYsIOQWGzkqXZtJDtpXZO6J13l5vGfuLBYOFWQoJKQeCIMggLc7v7tEnrVrLEi1sKKrEyD%2BKv7f%2FeRweAQOdUo52MNKeAQ%2Fx4BsPXUzMKmM8dEY9VAxhIrrnEd%2BYB9L9ePE9kBftUvH1%2FHgw77H%2F0gY6mAEv8BcI%2BdQeaUj%2FYPXGhfDRas09J9SwSlh6ocNzO1MOLtmqwJDhugkyGAT3vL8GDh3VBvyDeo4ZkLwnZknn%2BV%2B9jd8Z4BrQvZsGEktLvBN8bi1VurGjrquqJzMFh8432xfFuVRQmrKHSWsLPKqGexQ6ntFoxmqAdX%2FDXW1%2FnKCjaXJNkiAVs1Wz3DR0k4x9Cr7vWxs8LJF%2Biw%3D%3D&Expires=1784669890)
- `make compose-up-postgres` [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/images/62895318/b20c64d5-9127-4f77-beec-6edab9c5e30c/image.jpg?AWSAccessKeyId=ASIA2F3EMEYE2TH465MQ&Signature=wvhex0DO9HmQ72w63%2BdQFALLSk8%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEP3%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEaCXVzLWVhc3QtMSJHMEUCIQDsVK0igLJ4A8ZXetCKxiTYmFx1KJENyKl9gLW58WZdiQIgKtSiCc%2BddhN%2Bltd%2FLcI%2F9e4KYSduN%2FD0Tswn5paUGJ8q%2FAQIxv%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FARABGgw2OTk3NTMzMDk3MDUiDNcJkjN6G6BepWe%2F5yrQBCXdCdgpu43bh7FJjAZKG4WSp2zQvz1bTCnaZWUcfQ7PfWMv%2FDilo8EWkPUTOokdKwkFfWZwyTnG%2B8lIC54Bt09GN8h8ax%2FiyDTYDAtUQhlD1HgbuuedTAZVqBUZjUeBFnyzLvhJq6ivswrJwNm5u9knPNQjIRrnqhOzMi5eGk3NIhONENw3F1e8n%2FIW%2BijqncQmv6JZ1vmZIgTA1GwmUN9aNDtf%2BPSJopj9NO93ZzbNW49NjYJ%2FpqFPt0E2fkpj%2F3CQVOYegmHlCP80EGRolrj2AhfBwNOe9dmgJluoJNTGwQ8LK0jNz7wvZPQAU1nyItYizb3WkOmOokeOxnjcFLfvoQOEFPZBw%2B6a58TY4jEHncMOPeaM3uN7muNDwWbiU9Yud02jrTKeYgaBj34Ju7LboojwYb5tE%2BIpmziJGKmbn%2FJ4NxCG7ot1mUapPwEIXXZyT%2F2qSLc9aHeF6GVmdvCgNaZkzalJS1w7qNegoua%2F8dMkQ%2BRbBpcjKSjOO0h4Idpd8yESDHS8kPVYx4UiuRKsPLj8DNAw5NTJdphUets5zlHzuO%2FXi9t8B1XhNYNnbJY2TFLTix0qoGCqaeVTAXvgGR1MtRsqeL336sCd6tyWll%2BhTmPJYtcjEartDN4w52a9QBG0CYsIOQWGzkqXZtJDtpXZO6J13l5vGfuLBYOFWQoJKQeCIMggLc7v7tEnrVrLEi1sKKrEyD%2BKv7f%2FeRweAQOdUo52MNKeAQ%2Fx4BsPXUzMKmM8dEY9VAxhIrrnEd%2BYB9L9ePE9kBftUvH1%2FHgw77H%2F0gY6mAEv8BcI%2BdQeaUj%2FYPXGhfDRas09J9SwSlh6ocNzO1MOLtmqwJDhugkyGAT3vL8GDh3VBvyDeo4ZkLwnZknn%2BV%2B9jd8Z4BrQvZsGEktLvBN8bi1VurGjrquqJzMFh8432xfFuVRQmrKHSWsLPKqGexQ6ntFoxmqAdX%2FDXW1%2FnKCjaXJNkiAVs1Wz3DR0k4x9Cr7vWxs8LJF%2Biw%3D%3D&Expires=1784669890)
- `make compose-up-all` [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/images/62895318/b20c64d5-9127-4f77-beec-6edab9c5e30c/image.jpg?AWSAccessKeyId=ASIA2F3EMEYE2TH465MQ&Signature=wvhex0DO9HmQ72w63%2BdQFALLSk8%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEP3%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEaCXVzLWVhc3QtMSJHMEUCIQDsVK0igLJ4A8ZXetCKxiTYmFx1KJENyKl9gLW58WZdiQIgKtSiCc%2BddhN%2Bltd%2FLcI%2F9e4KYSduN%2FD0Tswn5paUGJ8q%2FAQIxv%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FARABGgw2OTk3NTMzMDk3MDUiDNcJkjN6G6BepWe%2F5yrQBCXdCdgpu43bh7FJjAZKG4WSp2zQvz1bTCnaZWUcfQ7PfWMv%2FDilo8EWkPUTOokdKwkFfWZwyTnG%2B8lIC54Bt09GN8h8ax%2FiyDTYDAtUQhlD1HgbuuedTAZVqBUZjUeBFnyzLvhJq6ivswrJwNm5u9knPNQjIRrnqhOzMi5eGk3NIhONENw3F1e8n%2FIW%2BijqncQmv6JZ1vmZIgTA1GwmUN9aNDtf%2BPSJopj9NO93ZzbNW49NjYJ%2FpqFPt0E2fkpj%2F3CQVOYegmHlCP80EGRolrj2AhfBwNOe9dmgJluoJNTGwQ8LK0jNz7wvZPQAU1nyItYizb3WkOmOokeOxnjcFLfvoQOEFPZBw%2B6a58TY4jEHncMOPeaM3uN7muNDwWbiU9Yud02jrTKeYgaBj34Ju7LboojwYb5tE%2BIpmziJGKmbn%2FJ4NxCG7ot1mUapPwEIXXZyT%2F2qSLc9aHeF6GVmdvCgNaZkzalJS1w7qNegoua%2F8dMkQ%2BRbBpcjKSjOO0h4Idpd8yESDHS8kPVYx4UiuRKsPLj8DNAw5NTJdphUets5zlHzuO%2FXi9t8B1XhNYNnbJY2TFLTix0qoGCqaeVTAXvgGR1MtRsqeL336sCd6tyWll%2BhTmPJYtcjEartDN4w52a9QBG0CYsIOQWGzkqXZtJDtpXZO6J13l5vGfuLBYOFWQoJKQeCIMggLc7v7tEnrVrLEi1sKKrEyD%2BKv7f%2FeRweAQOdUo52MNKeAQ%2Fx4BsPXUzMKmM8dEY9VAxhIrrnEd%2BYB9L9ePE9kBftUvH1%2FHgw77H%2F0gY6mAEv8BcI%2BdQeaUj%2FYPXGhfDRas09J9SwSlh6ocNzO1MOLtmqwJDhugkyGAT3vL8GDh3VBvyDeo4ZkLwnZknn%2BV%2B9jd8Z4BrQvZsGEktLvBN8bi1VurGjrquqJzMFh8432xfFuVRQmrKHSWsLPKqGexQ6ntFoxmqAdX%2FDXW1%2FnKCjaXJNkiAVs1Wz3DR0k4x9Cr7vWxs8LJF%2Biw%3D%3D&Expires=1784669890)
- `make test` [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/images/62895318/b20c64d5-9127-4f77-beec-6edab9c5e30c/image.jpg?AWSAccessKeyId=ASIA2F3EMEYE2TH465MQ&Signature=wvhex0DO9HmQ72w63%2BdQFALLSk8%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEP3%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEaCXVzLWVhc3QtMSJHMEUCIQDsVK0igLJ4A8ZXetCKxiTYmFx1KJENyKl9gLW58WZdiQIgKtSiCc%2BddhN%2Bltd%2FLcI%2F9e4KYSduN%2FD0Tswn5paUGJ8q%2FAQIxv%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FARABGgw2OTk3NTMzMDk3MDUiDNcJkjN6G6BepWe%2F5yrQBCXdCdgpu43bh7FJjAZKG4WSp2zQvz1bTCnaZWUcfQ7PfWMv%2FDilo8EWkPUTOokdKwkFfWZwyTnG%2B8lIC54Bt09GN8h8ax%2FiyDTYDAtUQhlD1HgbuuedTAZVqBUZjUeBFnyzLvhJq6ivswrJwNm5u9knPNQjIRrnqhOzMi5eGk3NIhONENw3F1e8n%2FIW%2BijqncQmv6JZ1vmZIgTA1GwmUN9aNDtf%2BPSJopj9NO93ZzbNW49NjYJ%2FpqFPt0E2fkpj%2F3CQVOYegmHlCP80EGRolrj2AhfBwNOe9dmgJluoJNTGwQ8LK0jNz7wvZPQAU1nyItYizb3WkOmOokeOxnjcFLfvoQOEFPZBw%2B6a58TY4jEHncMOPeaM3uN7muNDwWbiU9Yud02jrTKeYgaBj34Ju7LboojwYb5tE%2BIpmziJGKmbn%2FJ4NxCG7ot1mUapPwEIXXZyT%2F2qSLc9aHeF6GVmdvCgNaZkzalJS1w7qNegoua%2F8dMkQ%2BRbBpcjKSjOO0h4Idpd8yESDHS8kPVYx4UiuRKsPLj8DNAw5NTJdphUets5zlHzuO%2FXi9t8B1XhNYNnbJY2TFLTix0qoGCqaeVTAXvgGR1MtRsqeL336sCd6tyWll%2BhTmPJYtcjEartDN4w52a9QBG0CYsIOQWGzkqXZtJDtpXZO6J13l5vGfuLBYOFWQoJKQeCIMggLc7v7tEnrVrLEi1sKKrEyD%2BKv7f%2FeRweAQOdUo52MNKeAQ%2Fx4BsPXUzMKmM8dEY9VAxhIrrnEd%2BYB9L9ePE9kBftUvH1%2FHgw77H%2F0gY6mAEv8BcI%2BdQeaUj%2FYPXGhfDRas09J9SwSlh6ocNzO1MOLtmqwJDhugkyGAT3vL8GDh3VBvyDeo4ZkLwnZknn%2BV%2B9jd8Z4BrQvZsGEktLvBN8bi1VurGjrquqJzMFh8432xfFuVRQmrKHSWsLPKqGexQ6ntFoxmqAdX%2FDXW1%2FnKCjaXJNkiAVs1Wz3DR0k4x9Cr7vWxs8LJF%2Biw%3D%3D&Expires=1784669890)
- `make test-integration` [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/images/62895318/b20c64d5-9127-4f77-beec-6edab9c5e30c/image.jpg?AWSAccessKeyId=ASIA2F3EMEYE2TH465MQ&Signature=wvhex0DO9HmQ72w63%2BdQFALLSk8%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEP3%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEaCXVzLWVhc3QtMSJHMEUCIQDsVK0igLJ4A8ZXetCKxiTYmFx1KJENyKl9gLW58WZdiQIgKtSiCc%2BddhN%2Bltd%2FLcI%2F9e4KYSduN%2FD0Tswn5paUGJ8q%2FAQIxv%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FARABGgw2OTk3NTMzMDk3MDUiDNcJkjN6G6BepWe%2F5yrQBCXdCdgpu43bh7FJjAZKG4WSp2zQvz1bTCnaZWUcfQ7PfWMv%2FDilo8EWkPUTOokdKwkFfWZwyTnG%2B8lIC54Bt09GN8h8ax%2FiyDTYDAtUQhlD1HgbuuedTAZVqBUZjUeBFnyzLvhJq6ivswrJwNm5u9knPNQjIRrnqhOzMi5eGk3NIhONENw3F1e8n%2FIW%2BijqncQmv6JZ1vmZIgTA1GwmUN9aNDtf%2BPSJopj9NO93ZzbNW49NjYJ%2FpqFPt0E2fkpj%2F3CQVOYegmHlCP80EGRolrj2AhfBwNOe9dmgJluoJNTGwQ8LK0jNz7wvZPQAU1nyItYizb3WkOmOokeOxnjcFLfvoQOEFPZBw%2B6a58TY4jEHncMOPeaM3uN7muNDwWbiU9Yud02jrTKeYgaBj34Ju7LboojwYb5tE%2BIpmziJGKmbn%2FJ4NxCG7ot1mUapPwEIXXZyT%2F2qSLc9aHeF6GVmdvCgNaZkzalJS1w7qNegoua%2F8dMkQ%2BRbBpcjKSjOO0h4Idpd8yESDHS8kPVYx4UiuRKsPLj8DNAw5NTJdphUets5zlHzuO%2FXi9t8B1XhNYNnbJY2TFLTix0qoGCqaeVTAXvgGR1MtRsqeL336sCd6tyWll%2BhTmPJYtcjEartDN4w52a9QBG0CYsIOQWGzkqXZtJDtpXZO6J13l5vGfuLBYOFWQoJKQeCIMggLc7v7tEnrVrLEi1sKKrEyD%2BKv7f%2FeRweAQOdUo52MNKeAQ%2Fx4BsPXUzMKmM8dEY9VAxhIrrnEd%2BYB9L9ePE9kBftUvH1%2FHgw77H%2F0gY6mAEv8BcI%2BdQeaUj%2FYPXGhfDRas09J9SwSlh6ocNzO1MOLtmqwJDhugkyGAT3vL8GDh3VBvyDeo4ZkLwnZknn%2BV%2B9jd8Z4BrQvZsGEktLvBN8bi1VurGjrquqJzMFh8432xfFuVRQmrKHSWsLPKqGexQ6ntFoxmqAdX%2FDXW1%2FnKCjaXJNkiAVs1Wz3DR0k4x9Cr7vWxs8LJF%2Biw%3D%3D&Expires=1784669890)

This matches the normal workflow already described earlier in the document, where Neovim is used to
edit the `Makefile` and source code, while `make` commands are executed from the project root
. [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/62895318/7a2d2b42-c0a9-4b19-bdfc-18c7175a528c/neovim-setup.md?AWSAccessKeyId=ASIA2F3EMEYE2TH465MQ&Signature=IjZy%2FxeV%2FHgL8aljToJ1z9Gpj04%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEP3%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEaCXVzLWVhc3QtMSJHMEUCIQDsVK0igLJ4A8ZXetCKxiTYmFx1KJENyKl9gLW58WZdiQIgKtSiCc%2BddhN%2Bltd%2FLcI%2F9e4KYSduN%2FD0Tswn5paUGJ8q%2FAQIxv%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FARABGgw2OTk3NTMzMDk3MDUiDNcJkjN6G6BepWe%2F5yrQBCXdCdgpu43bh7FJjAZKG4WSp2zQvz1bTCnaZWUcfQ7PfWMv%2FDilo8EWkPUTOokdKwkFfWZwyTnG%2B8lIC54Bt09GN8h8ax%2FiyDTYDAtUQhlD1HgbuuedTAZVqBUZjUeBFnyzLvhJq6ivswrJwNm5u9knPNQjIRrnqhOzMi5eGk3NIhONENw3F1e8n%2FIW%2BijqncQmv6JZ1vmZIgTA1GwmUN9aNDtf%2BPSJopj9NO93ZzbNW49NjYJ%2FpqFPt0E2fkpj%2F3CQVOYegmHlCP80EGRolrj2AhfBwNOe9dmgJluoJNTGwQ8LK0jNz7wvZPQAU1nyItYizb3WkOmOokeOxnjcFLfvoQOEFPZBw%2B6a58TY4jEHncMOPeaM3uN7muNDwWbiU9Yud02jrTKeYgaBj34Ju7LboojwYb5tE%2BIpmziJGKmbn%2FJ4NxCG7ot1mUapPwEIXXZyT%2F2qSLc9aHeF6GVmdvCgNaZkzalJS1w7qNegoua%2F8dMkQ%2BRbBpcjKSjOO0h4Idpd8yESDHS8kPVYx4UiuRKsPLj8DNAw5NTJdphUets5zlHzuO%2FXi9t8B1XhNYNnbJY2TFLTix0qoGCqaeVTAXvgGR1MtRsqeL336sCd6tyWll%2BhTmPJYtcjEartDN4w52a9QBG0CYsIOQWGzkqXZtJDtpXZO6J13l5vGfuLBYOFWQoJKQeCIMggLc7v7tEnrVrLEi1sKKrEyD%2BKv7f%2FeRweAQOdUo52MNKeAQ%2Fx4BsPXUzMKmM8dEY9VAxhIrrnEd%2BYB9L9ePE9kBftUvH1%2FHgw77H%2F0gY6mAEv8BcI%2BdQeaUj%2FYPXGhfDRas09J9SwSlh6ocNzO1MOLtmqwJDhugkyGAT3vL8GDh3VBvyDeo4ZkLwnZknn%2BV%2B9jd8Z4BrQvZsGEktLvBN8bi1VurGjrquqJzMFh8432xfFuVRQmrKHSWsLPKqGexQ6ntFoxmqAdX%2FDXW1%2FnKCjaXJNkiAVs1Wz3DR0k4x9Cr7vWxs8LJF%2Biw%3D%3D&Expires=1784669890)

### Suggested First Test

Use the following sequence for a complete first test:

1. Clone the repository.
2. Open it with `nvim .`
3. Open `go.mod`
4. Open one Go source file
5. Use `<Space><Space>` and `<Space>e` to navigate files [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/62895318/7a2d2b42-c0a9-4b19-bdfc-18c7175a528c/neovim-setup.md?AWSAccessKeyId=ASIA2F3EMEYE2TH465MQ&Signature=IjZy%2FxeV%2FHgL8aljToJ1z9Gpj04%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEP3%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEaCXVzLWVhc3QtMSJHMEUCIQDsVK0igLJ4A8ZXetCKxiTYmFx1KJENyKl9gLW58WZdiQIgKtSiCc%2BddhN%2Bltd%2FLcI%2F9e4KYSduN%2FD0Tswn5paUGJ8q%2FAQIxv%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FARABGgw2OTk3NTMzMDk3MDUiDNcJkjN6G6BepWe%2F5yrQBCXdCdgpu43bh7FJjAZKG4WSp2zQvz1bTCnaZWUcfQ7PfWMv%2FDilo8EWkPUTOokdKwkFfWZwyTnG%2B8lIC54Bt09GN8h8ax%2FiyDTYDAtUQhlD1HgbuuedTAZVqBUZjUeBFnyzLvhJq6ivswrJwNm5u9knPNQjIRrnqhOzMi5eGk3NIhONENw3F1e8n%2FIW%2BijqncQmv6JZ1vmZIgTA1GwmUN9aNDtf%2BPSJopj9NO93ZzbNW49NjYJ%2FpqFPt0E2fkpj%2F3CQVOYegmHlCP80EGRolrj2AhfBwNOe9dmgJluoJNTGwQ8LK0jNz7wvZPQAU1nyItYizb3WkOmOokeOxnjcFLfvoQOEFPZBw%2B6a58TY4jEHncMOPeaM3uN7muNDwWbiU9Yud02jrTKeYgaBj34Ju7LboojwYb5tE%2BIpmziJGKmbn%2FJ4NxCG7ot1mUapPwEIXXZyT%2F2qSLc9aHeF6GVmdvCgNaZkzalJS1w7qNegoua%2F8dMkQ%2BRbBpcjKSjOO0h4Idpd8yESDHS8kPVYx4UiuRKsPLj8DNAw5NTJdphUets5zlHzuO%2FXi9t8B1XhNYNnbJY2TFLTix0qoGCqaeVTAXvgGR1MtRsqeL336sCd6tyWll%2BhTmPJYtcjEartDN4w52a9QBG0CYsIOQWGzkqXZtJDtpXZO6J13l5vGfuLBYOFWQoJKQeCIMggLc7v7tEnrVrLEi1sKKrEyD%2BKv7f%2FeRweAQOdUo52MNKeAQ%2Fx4BsPXUzMKmM8dEY9VAxhIrrnEd%2BYB9L9ePE9kBftUvH1%2FHgw77H%2F0gY6mAEv8BcI%2BdQeaUj%2FYPXGhfDRas09J9SwSlh6ocNzO1MOLtmqwJDhugkyGAT3vL8GDh3VBvyDeo4ZkLwnZknn%2BV%2B9jd8Z4BrQvZsGEktLvBN8bi1VurGjrquqJzMFh8432xfFuVRQmrKHSWsLPKqGexQ6ntFoxmqAdX%2FDXW1%2FnKCjaXJNkiAVs1Wz3DR0k4x9Cr7vWxs8LJF%2Biw%3D%3D&Expires=1784669890)
6. Run `<Space>cf` to test formatting [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/62895318/7a2d2b42-c0a9-4b19-bdfc-18c7175a528c/neovim-setup.md?AWSAccessKeyId=ASIA2F3EMEYE2TH465MQ&Signature=IjZy%2FxeV%2FHgL8aljToJ1z9Gpj04%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEP3%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEaCXVzLWVhc3QtMSJHMEUCIQDsVK0igLJ4A8ZXetCKxiTYmFx1KJENyKl9gLW58WZdiQIgKtSiCc%2BddhN%2Bltd%2FLcI%2F9e4KYSduN%2FD0Tswn5paUGJ8q%2FAQIxv%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FARABGgw2OTk3NTMzMDk3MDUiDNcJkjN6G6BepWe%2F5yrQBCXdCdgpu43bh7FJjAZKG4WSp2zQvz1bTCnaZWUcfQ7PfWMv%2FDilo8EWkPUTOokdKwkFfWZwyTnG%2B8lIC54Bt09GN8h8ax%2FiyDTYDAtUQhlD1HgbuuedTAZVqBUZjUeBFnyzLvhJq6ivswrJwNm5u9knPNQjIRrnqhOzMi5eGk3NIhONENw3F1e8n%2FIW%2BijqncQmv6JZ1vmZIgTA1GwmUN9aNDtf%2BPSJopj9NO93ZzbNW49NjYJ%2FpqFPt0E2fkpj%2F3CQVOYegmHlCP80EGRolrj2AhfBwNOe9dmgJluoJNTGwQ8LK0jNz7wvZPQAU1nyItYizb3WkOmOokeOxnjcFLfvoQOEFPZBw%2B6a58TY4jEHncMOPeaM3uN7muNDwWbiU9Yud02jrTKeYgaBj34Ju7LboojwYb5tE%2BIpmziJGKmbn%2FJ4NxCG7ot1mUapPwEIXXZyT%2F2qSLc9aHeF6GVmdvCgNaZkzalJS1w7qNegoua%2F8dMkQ%2BRbBpcjKSjOO0h4Idpd8yESDHS8kPVYx4UiuRKsPLj8DNAw5NTJdphUets5zlHzuO%2FXi9t8B1XhNYNnbJY2TFLTix0qoGCqaeVTAXvgGR1MtRsqeL336sCd6tyWll%2BhTmPJYtcjEartDN4w52a9QBG0CYsIOQWGzkqXZtJDtpXZO6J13l5vGfuLBYOFWQoJKQeCIMggLc7v7tEnrVrLEi1sKKrEyD%2BKv7f%2FeRweAQOdUo52MNKeAQ%2Fx4BsPXUzMKmM8dEY9VAxhIrrnEd%2BYB9L9ePE9kBftUvH1%2FHgw77H%2F0gY6mAEv8BcI%2BdQeaUj%2FYPXGhfDRas09J9SwSlh6ocNzO1MOLtmqwJDhugkyGAT3vL8GDh3VBvyDeo4ZkLwnZknn%2BV%2B9jd8Z4BrQvZsGEktLvBN8bi1VurGjrquqJzMFh8432xfFuVRQmrKHSWsLPKqGexQ6ntFoxmqAdX%2FDXW1%2FnKCjaXJNkiAVs1Wz3DR0k4x9Cr7vWxs8LJF%2Biw%3D%3D&Expires=1784669890)
7. Run `:terminal`, then execute:

```bash
make test
```

If you also want to test local infrastructure commands from the repository, run:

```bash
make compose-up-postgres
```

and later:

```bash
make compose-up-all
```

These targets are documented by the repository for local development and integration testing
workflows. [ppl-ai-file-upload.s3.amazonaws](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/images/62895318/b20c64d5-9127-4f77-beec-6edab9c5e30c/image.jpg?AWSAccessKeyId=ASIA2F3EMEYE2TH465MQ&Signature=wvhex0DO9HmQ72w63%2BdQFALLSk8%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEP3%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEaCXVzLWVhc3QtMSJHMEUCIQDsVK0igLJ4A8ZXetCKxiTYmFx1KJENyKl9gLW58WZdiQIgKtSiCc%2BddhN%2Bltd%2FLcI%2F9e4KYSduN%2FD0Tswn5paUGJ8q%2FAQIxv%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FARABGgw2OTk3NTMzMDk3MDUiDNcJkjN6G6BepWe%2F5yrQBCXdCdgpu43bh7FJjAZKG4WSp2zQvz1bTCnaZWUcfQ7PfWMv%2FDilo8EWkPUTOokdKwkFfWZwyTnG%2B8lIC54Bt09GN8h8ax%2FiyDTYDAtUQhlD1HgbuuedTAZVqBUZjUeBFnyzLvhJq6ivswrJwNm5u9knPNQjIRrnqhOzMi5eGk3NIhONENw3F1e8n%2FIW%2BijqncQmv6JZ1vmZIgTA1GwmUN9aNDtf%2BPSJopj9NO93ZzbNW49NjYJ%2FpqFPt0E2fkpj%2F3CQVOYegmHlCP80EGRolrj2AhfBwNOe9dmgJluoJNTGwQ8LK0jNz7wvZPQAU1nyItYizb3WkOmOokeOxnjcFLfvoQOEFPZBw%2B6a58TY4jEHncMOPeaM3uN7muNDwWbiU9Yud02jrTKeYgaBj34Ju7LboojwYb5tE%2BIpmziJGKmbn%2FJ4NxCG7ot1mUapPwEIXXZyT%2F2qSLc9aHeF6GVmdvCgNaZkzalJS1w7qNegoua%2F8dMkQ%2BRbBpcjKSjOO0h4Idpd8yESDHS8kPVYx4UiuRKsPLj8DNAw5NTJdphUets5zlHzuO%2FXi9t8B1XhNYNnbJY2TFLTix0qoGCqaeVTAXvgGR1MtRsqeL336sCd6tyWll%2BhTmPJYtcjEartDN4w52a9QBG0CYsIOQWGzkqXZtJDtpXZO6J13l5vGfuLBYOFWQoJKQeCIMggLc7v7tEnrVrLEi1sKKrEyD%2BKv7f%2FeRweAQOdUo52MNKeAQ%2Fx4BsPXUzMKmM8dEY9VAxhIrrnEd%2BYB9L9ePE9kBftUvH1%2FHgw77H%2F0gY6mAEv8BcI%2BdQeaUj%2FYPXGhfDRas09J9SwSlh6ocNzO1MOLtmqwJDhugkyGAT3vL8GDh3VBvyDeo4ZkLwnZknn%2BV%2B9jd8Z4BrQvZsGEktLvBN8bi1VurGjrquqJzMFh8432xfFuVRQmrKHSWsLPKqGexQ6ntFoxmqAdX%2FDXW1%2FnKCjaXJNkiAVs1Wz3DR0k4x9Cr7vWxs8LJF%2Biw%3D%3D&Expires=1784669890)

If you want, I can also rewrite your earlier Solarized section so it matches this same polished
style exactly.
