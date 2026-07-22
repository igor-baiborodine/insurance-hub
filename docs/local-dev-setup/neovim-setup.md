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

## Test Setup

After installing Neovim, LazyVim, Go support, and the required system tools, test the setup with a real Go repository. This confirms that project opening, file navigation, Go tooling, and Makefile commands all work together.

The repository used below is `igor-baiborodine/campsite-booking-go`.

This project contains a Go gRPC API example application and documents common Make targets for local setup and testing.

### Clone and Open Project

Clone the repository and open it from the project root:

```bash
git clone https://github.com/igor-baiborodine/campsite-booking-go.git
cd campsite-booking-go
nvim .
```

Opening the project with `nvim .` lets LazyVim treat the repository as the current working directory, so file search, diagnostics, LSP, and project navigation operate against the whole project.

### Verify Go Support

Once the repository is open, confirm that the Go setup is active.

Run these commands inside Neovim:

```vim
:checkhealth
:Mason
```

`checkhealth` helps confirm that Neovim can see required tools, and `:Mason` shows whether external developer tools such as `gopls` are available or installed.

If the repository has not been prepared yet, run the documented tool installation target from the project root:

```bash
make install-tools
```

### Navigate Go Files

Use LazyVim's built-in file and search features to move around the repository.

Common commands:

| Need                              | Command or Key   |
|-----------------------------------|------------------|
| Open current directory as project | `nvim .`         |
| Find files                        | `<Space><Space>` |
| Open file explorer                | `<Space>e`       |
| Search project text               | `<Space>sg`      |
| Format current file               | `<Space>cf`      |

### Suggested First Test

Use the following sequence for a complete first test:

1. Clone the repository.
2. Open it with `nvim .`
3. Open `go.mod`
4. Open one Go source file
5. Use `<Space><Space>` and `<Space>e` to navigate files.
6. Run `<Space>cf` to test formatting.
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

These targets cover the local development and integration testing workflow.
