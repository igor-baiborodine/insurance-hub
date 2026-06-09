<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Supported Platforms](#supported-platforms)
- [VS Code](#vs-code)
- [Extensions](#extensions)
- [Optional Configuration](#optional-configuration)
  - [JetBrains Mono Font](#jetbrains-mono-font)
  - [Settings (settings.json)](#settings-settingsjson)
  - [Key bindings (keybindings.json)](#key-bindings-keybindingsjson)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

> This document captures repeatable steps to install and verify Visual Studio Code (VS Code) along with necessary extensions and configurations, focusing on Java/Go local-first development with Ollama and the Zoo Code extension.

## Supported Platforms

- Ubuntu 24.04 LTS (documented and validated)
- Other OSes (macOS, Windows, additional Linux distros) can be added later with the same structure. 

## VS Code

**Installation**

Use the official Microsoft APT repository so VS Code updates with the system package manager. [linuxiac](https://linuxiac.com/how-to-install-vs-code-on-ubuntu-24-04-lts/)

```bash
# Update package index and install prerequisites
sudo apt update
sudo apt install wget gpg apt-transport-https

# Import Microsoft GPG key
wget -qO- https://packages.microsoft.com/keys/microsoft.asc | gpg --dearmor > packages.microsoft.gpg
sudo install -D -o root -g root -m 644 packages.microsoft.gpg /etc/apt/keyrings/packages.microsoft.gpg
rm packages.microsoft.gpg

# Add VS Code repository
echo "deb [arch=amd64,arm64,armhf signed-by=/etc/apt/keyrings/packages.microsoft.gpg] https://packages.microsoft.com/repos/code stable main" | sudo tee /etc/apt/sources.list.d/vscode.list > /dev/null

# Refresh package index
sudo apt update

# Install Visual Studio Code
sudo apt install code
```

Alternative (if needed): VS Code is also available as a Snap package on Ubuntu, installable with `sudo snap install --classic code`, but the APT-based method above should be treated as the primary path for this project. [linuxiac](https://linuxiac.com/how-to-install-vs-code-on-ubuntu-24-04-lts/)

**Verification**

These checks satisfy “VS Code is installed” and “command-line integration works (`code --version`).” 

1. GUI launch

    - Open the Activities / application menu, search for “Visual Studio Code” or “Code”, and confirm the editor launches successfully. [linuxiac](https://linuxiac.com/how-to-install-vs-code-on-ubuntu-24-04-lts/)

2. CLI integration

   In a terminal:

   ```bash
   code --version
   ```

    - Expected result: a version string and build info printed to stdout, exit code 0.
    - If the command is not found, ensure the `code` package installed correctly and that `/usr/bin` is on `PATH`. [linuxiac](https://linuxiac.com/how-to-install-vs-code-on-ubuntu-24-04-lts/)

## Extensions

VS Code extensions required for this project (Java, Micronaut, Docker/Kubernetes, data tooling, Markdown, Makefile tools, and Zoo Code) are installed via a single script so setup is repeatable and can be automated in CI or on new machines.

**Installation**

From a terminal, in the repository root:

```bash
.docs/local-dev-setup/install-vscode-extensions.sh
```

The script installs all required extensions using the `code --install-extension` CLI, including:

- Java development:  
  - `vscjava.vscode-java-pack`  
  - `vscjava.vscode-lombok`
- Micronaut / GraalVM:  
  - `oracle-labs-graalvm.micronaut`
- Docker and Kubernetes tooling:  
  - `ms-azuretools.vscode-docker`  
  - `ms-kubernetes-tools.vscode-kubernetes-tools`
- Data and messaging tooling:  
  - `confluentinc.vscode-confluent`  
  - `ria.elastic`  
  - `ms-ossdata.vscode-pgsql`
- Markdown and build tooling:  
  - `yzhang.markdown-all-in-one`  
  - `ms-vscode.makefile-tools`
- Zoo Code AI agentic development:
  - `zoocodeorganization.zoo-code`
- Basic spell checker:
  - `streetsidesoftware.code-spell-checker`
  
**Verification**

These checks satisfy “all required extensions are installed and enabled” and “extension versions used during setup are captured”:

1. Extensions installed

   ```bash
   code --list-extensions
   ```

   - Expected result: output includes the IDs listed in `install-vscode-extensions.sh`.

2. Extensions enabled and visible

   - Open the **Extensions** view in VS Code and filter with `@installed`.  
   - Confirm all extensions from the script appear and are enabled.

3. Version capture

   ```bash
   code --list-extensions --show-versions
   ```

   - Expected result: each extension appears as `<extension-id>@<version>`.  
   - Record these versions in setup or validation notes if reproducibility across machines or environments is required.

## Optional Configuration

### JetBrains Mono Font

To enhance the visual and functional aspects of VS Code, this project optionally recommends using JetBrains’ **JetBrains Mono** font and maintaining a consistent configuration for the editor and terminal to improve code readability.

If JetBrains Mono is not already installed at the system level, download the official JetBrains Mono ZIP archive from JetBrains’ website and install it into the local font directory.

1. Create a user font directory (if not present)

   ```bash
   mkdir -p ~/.local/share/fonts/jetbrains-mono
   ```

2. Unpack the JetBrains Mono archive

   If the ZIP file is in `~/Downloads`:

   ```bash
   cd ~/Downloads
   unzip JetBrainsMono-*.zip -d jetbrains-mono
   ```

   The extracted folder should contain a `fonts/ttf` directory with `.ttf` files such as `JetBrainsMono-Regular.ttf`.

3. Copy the `.ttf` files into the font directory

   ```bash
   cp jetbrains-mono/fonts/ttf/*.ttf ~/.local/share/fonts/jetbrains-mono/
   ```

4. Refresh the font cache

   ```bash
   fc-cache -f -v
   ```

5. Verify availability

   ```bash
   fc-list | grep "JetBrains Mono"
   ```

   - Expected result: several lines listing JetBrains Mono fonts and their paths.

If system-wide installation is preferred, use `/usr/share/fonts/truetype/jetbrains-mono` instead of `~/.local/share/fonts/jetbrains-mono` and run the copy and `fc-cache` commands with `sudo`.

### Settings (settings.json)

After installing JetBrains Mono, configure VS Code to use it for the editor and the integrated terminal, and to apply a consistent zoom level along with other settings. This `settings.json` snippet can be added either to **User** settings or to a project-specific `.vscode/settings.json`.

```json
"window.zoomLevel": 2.0,
"editor.fontFamily": "JetBrains Mono, Consolas, 'Courier New', monospace",
"editor.fontSize": 13,
"editor.fontLigatures": true,
"terminal.integrated.fontFamily": "'JetBrains Mono'",
"terminal.integrated.fontSize": 12.5
"files.autoSave": "afterDelay",
"files.autoSaveDelay": 1000
```

- `window.zoomLevel`: scales the entire VS Code UI, including Explorer, tabs, and panels, which is useful on high-DPI displays.  
- `editor.fontFamily`: prefers JetBrains Mono while falling back to other monospaced fonts if it is unavailable.  
- `editor.fontSize`: sets the editor font size in points; adjust if needed for specific displays.  
- `editor.fontLigatures`: enables JetBrains Mono’s programming ligatures for improved readability of operators.  
- `terminal.integrated.fontFamily` and `terminal.integrated.fontSize`: ensure the integrated terminal uses JetBrains Mono with a size tuned for readability inside the VS Code panel.
- `files.autoSave` and `files.autoSaveDelay`: enable auto-save for any changes after 1 second.

**To apply this configuration:**

1. Open VS Code.  
2. Open the Command Palette (`Ctrl+Shift+P`).  
3. Run **Preferences: Open Settings (JSON)**.  
4. Paste or merge the snippet above into the JSON file, taking care to keep the JSON valid (commas between entries, no duplicate keys).  
5. Save the file and fully restart VS Code to ensure the font is picked up.

### Key bindings (keybindings.json)

This project uses a small set of custom keybindings to mimic familiar shortcuts from JetBrains IntelliJ IDEA, especially for terminal copy/paste and editor navigation, so the workflow feels more consistent and efficient for keyboard-driven users.

**To apply these keybindings:**

1. Open VS Code.  
2. Open the Command Palette (`Ctrl+Shift+P`).  
3. Run **Preferences: Open Keyboard Shortcuts (JSON)**.  
4. Replace or merge the contents with the following array (ensure it is valid JSON):

```json
[
    {
        "key": "ctrl+c",
        "command": "workbench.action.terminal.copySelection",
        "when": "terminalFocus && terminalHasSelection"
    },
    {
        "key": "ctrl+v",
        "command": "workbench.action.terminal.paste",
        "when": "terminalFocus"
    },
    {
        "key": "alt+right",
        "command": "workbench.action.nextEditor"
    },
    {
        "key": "alt+left",
        "command": "workbench.action.previousEditor"
    },
    {
        "key": "ctrl+alt+e",
        "command": "workbench.action.quickOpen",
        "args": "view "
    },
    {
        "key": "alt+down",
        "command": "editor.action.showContextMenu",
        "when": "editorTextFocus"
    }
]
```

**What these bindings do:**

- `Ctrl+C` in the integrated terminal copies the current selection instead of sending an interrupt signal, but only when the terminal has focus and there is a selection.  
- `Ctrl+V` in the integrated terminal pastes from the clipboard into the terminal when it has focus.  
- `Alt+Right` switches to the next editor tab.  
- `Alt+Left` switches to the previous editor tab.  
- `All+Down` shows context menu inside the actual code editor.
- `Ctrl+Alt+E` opens Quick Open pre-seeded with the text `view `, useful for quickly jumping to items whose names begin with “view ”.

After you paste this into `keybindings.json`, save the file and test each shortcut in a normal editing session.
