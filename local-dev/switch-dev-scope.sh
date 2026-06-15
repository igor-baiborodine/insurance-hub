#!/bin/bash
# Location: local-dev/switch-dev-scope.sh
# Usage examples:
#   dev-scope pricing-service pricing-service-api
#   dev-scope pricing-service auth-service payment-service

if [ "$#" -eq 0 ]; then
    echo "❌ Error: Please provide at least one service name."
    echo "Usage: dev-scope <service1> [service2] ..."
    exit 1
fi

# TARGETS THE TRUE ROOT: Paths relative to the local-dev/ script folder
WORKSPACE_FILE="../development.code-workspace"
ROOIGNORE_FILE="../.rooignore"
LEGACY_DIR="../legacy"

# --- 1. START WRITING THE WORKSPACE FILE ---
cat << EOF > "$WORKSPACE_FILE"
{
  "folders": [
    {
      "name": "⚙️ Monorepo Root (Config Only)",
      "path": "."
    }
EOF

# --- 2. START WRITING THE ROOIGNORE FILE ---
cat << EOF > "$ROOIGNORE_FILE"
# Master blocklist for Zoo Code / Roo Code
# Everything is blocked EXCEPT the active services

docs/
k8s/
local-dev/

# Block all microservices by default
legacy/*/
EOF

# --- 3. DYNAMICALLY LOOP THROUGH ALL ARGUMENTS ---
echo "✨ Configuring workspace focus for:"
for SERVICE in "$@"; do
    # Safety Check: Verify if the directory actually exists in the legacy/ folder
    if [ ! -d "$LEGACY_DIR/$SERVICE" ]; then
        echo "⚠️  Skipping '$SERVICE': Could not find directory '$SERVICE' in legacy/"
        continue
    fi

    echo "  • $SERVICE"

    # Append exactly one folder entry per service argument to the workspace JSON
    cat << EOF >> "$WORKSPACE_FILE"
    ,
    {
      "name": "${SERVICE}",
      "path": "legacy/${SERVICE}"
    }
EOF

    # Append exactly one exception rule (!) to .rooignore for this specific folder
    cat << EOF >> "$ROOIGNORE_FILE"
!legacy/${SERVICE}/
EOF
done

# --- 4. CLOSE AND FINALIZE THE WORKSPACE FILE ---
cat << EOF >> "$WORKSPACE_FILE"
  ],
  "settings": {
    "search.exclude": {
      "**/node_modules": true,
      "**/target": true,
      "**/.git": true
    }
  }
}
EOF

echo "🚀 Configured successfully!"
echo "🔄 Press Ctrl+Shift+P -> 'Developer: Reload Window' in VS Code to apply updates instantly."
