> This document captures repeatable steps to install and verify Visual Studio Code along with the Continue extension, focusing on local-first development with Ollama and the Continue extension. 

## Supported Platforms

- Ubuntu 24.04 LTS (documented and validated)
- Other OSes (macOS, Windows, additional Linux distros) can be added later with the same structure. 

## Ubuntu 24.04

### Install

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

### Verification

These checks satisfy “VS Code is installed” and “command-line integration works (`code --version`).” 

1. **GUI launch**

    - Open the Activities / application menu, search for “Visual Studio Code” or “Code”, and confirm the editor launches successfully. [linuxiac](https://linuxiac.com/how-to-install-vs-code-on-ubuntu-24-04-lts/)

2. **CLI integration**

   In a terminal:

   ```bash
   code --version
   ```

    - Expected result: a version string and build info printed to stdout, exit code 0.
    - If the command is not found, ensure the `code` package installed correctly and that `/usr/bin` is on `PATH`. [linuxiac](https://linuxiac.com/how-to-install-vs-code-on-ubuntu-24-04-lts/)

## Continue Extension

### Install

You can install Continue either via the VS Code UI or the `code` CLI.

- **From the Extensions view (recommended for most users)**
   1. Open VS Code.
   2. Open the Extensions view (`Ctrl+Shift+X`).
   3. Search for `Continue`.
   4. Install the extension named **“Continue – open-source AI code agent”** with identifier `Continue.continue`.

- **From the command line (useful for scripts/onboarding)**

  ```bash
  code --install-extension Continue.continue
  ```

### Verification

1. Open VS Code and confirm a **Continue** icon appears in the activity/sidebar.
2. Click the Continue icon to open the panel and confirm it loads without errors.
3. In the Extensions view, verify that:
   - `Continue` appears under **Installed**.
   - The entry does not show an **Enable** button (which would mean it is disabled).
