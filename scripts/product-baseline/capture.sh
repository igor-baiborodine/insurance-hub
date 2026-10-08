#!/usr/bin/env bash
set -euo pipefail

case "${BASELINE_PART:-}" in
  inventory) exec "$(dirname "${BASH_SOURCE[0]}")/capture-inventory.sh" ;;
  catalog) exec "$(dirname "${BASH_SOURCE[0]}")/capture-catalog.sh" ;;
  http) exec "$(dirname "${BASH_SOURCE[0]}")/capture-http.sh" ;;
  access) exec "$(dirname "${BASH_SOURCE[0]}")/capture-access.sh" ;;
  *)
    echo "ERROR: BASELINE_PART must be 'inventory', 'catalog', 'http', or 'access'." >&2
    exit 2
    ;;
esac
