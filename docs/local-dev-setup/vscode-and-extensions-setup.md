> This document captures repeatable steps to install and verify Visual Studio Code (VS Code) along with necessary extensions, focusing on Java/Go local-first development with Ollama and the Zoo Code extension.

<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Supported Platforms](#supported-platforms)
- [Ubuntu 24.04](#ubuntu-2404)
  - [VS Code](#vs-code)
  - [Extensions](#extensions)
    - [Zoo Code](#zoo-code)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

## Supported Platforms

- Ubuntu 24.04 LTS (documented and validated)
- Other OSes (macOS, Windows, additional Linux distros) can be added later with the same structure. 

## Ubuntu 24.04

### VS Code

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

1. **GUI launch**

    - Open the Activities / application menu, search for “Visual Studio Code” or “Code”, and confirm the editor launches successfully. [linuxiac](https://linuxiac.com/how-to-install-vs-code-on-ubuntu-24-04-lts/)

2. **CLI integration**

   In a terminal:

   ```bash
   code --version
   ```

    - Expected result: a version string and build info printed to stdout, exit code 0.
    - If the command is not found, ensure the `code` package installed correctly and that `/usr/bin` is on `PATH`. [linuxiac](https://linuxiac.com/how-to-install-vs-code-on-ubuntu-24-04-lts/)

### Extensions

#### Zoo Code

**Installation**

Use the official Visual Studio Code Marketplace listing to install Zoo Code in VS Code. [marketplace.visualstudio](https://marketplace.visualstudio.com/items?itemName=ZooCodeOrganization.zoo-code)

1. Open Visual Studio Code.
2. Open the **Extensions** view from the Activity Bar, or press `Ctrl+Shift+X`.
3. Search for `Zoo Code`.
4. Select the extension published as `zoocodeorganization.zoo-code`.
5. Click **Install** and allow VS Code to complete the installation. [marketplace.visualstudio](https://marketplace.visualstudio.com/items?itemName=ZooCodeOrganization.zoo-code)

For a reproducible command-line install, you can also install the extension with the VS Code CLI:

```bash
code --install-extension ZooCodeOrganization.zoo-code
```

Alternative (if needed): install the extension directly from the Marketplace page in the browser and allow it to open in VS Code, but the Extensions view or `code --install-extension` method above should be treated as the primary path for this project. [marketplace.visualstudio](https://marketplace.visualstudio.com/items?itemName=ZooCodeOrganization.zoo-code)

**Verification**

These checks satisfy “Zoo Code is installed in VS Code,” “the extension is enabled and visible in the VS Code activity/sidebar,” and “the extension version used during setup is captured.” 

1. **Extension installed and enabled**

    - Open the **Extensions** view in VS Code and search for `@installed Zoo Code`.
    - Confirm the Zoo Code extension appears in the installed extensions list and is enabled. [marketplace.visualstudio](https://marketplace.visualstudio.com/items?itemName=ZooCodeOrganization.zoo-code)

2. **Extension visible in VS Code**

    - Confirm Zoo Code is visible in the VS Code interface after installation.
    - If the extension does not appear immediately, reload the VS Code window and verify it is still installed and enabled. 
   
3. **Version capture**

   In a terminal:

   ```bash
   code --list-extensions --show-versions | grep zoocodeorganization.zoo-code
   ```

    - Expected result: output includes `zoocodeorganization.zoo-code@<version>`.
    - Record the reported version in the setup notes so the extension version used during validation is preserved. 