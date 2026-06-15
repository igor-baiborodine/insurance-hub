## Configuration & Context Defenses

### Double-Layer Ignoring (`.gitignore` + `.rooignore`)
Roo Code merges both files into a global indexing blocklist.
- Use `.gitignore` for binaries (`target/`, `node_modules/`).
- Use `.rooignore` to block non-code weight (large `.json` mock payloads, logs, system architecture graphics, docs) from eating your 8k token buffer.

### The Multi-Root Approach

Never open the root monorepo directory directly for automated AI indexing. Instead, scope focus aggressively using a variable Multi-Root Workspace layout.

### The Multi-Root Approach
1. The automation script will automatically create the `development.code-workspace` file if it does not exist, or completely overwrite it if it does.
2. Isolate only the specific matching pairs (`service` + `service-api`) inside the `folders` block.
3. Keep the Root (`.`) mapped strictly as a configuration anchor.
4. Run the `./switch-dev-scope.sh <name>` automation script to dynamically shift focus when pivoting between features.

### Alias Configuration
To run the script easily, use this one-liner command from the monorepo root folder to append the `dev-scope` alias to your shell profile:
```bash
echo "alias dev-scope='\$(pwd)/local-dev/switch-dev-scope.sh'" >> ~/.bashrc && source ~/.bashrc
```
### Overriding the Isolation
If the agent needs data from a hidden service, do not reconfigure the tree. Explicitly invoke an automated file pull by targeting it directly in chat using **`@` tags** (e.g., `@legacy/product-service-api/src/...`).
