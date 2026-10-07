#!/usr/bin/env bash
set -euo pipefail

case "${BASELINE_PART:-}" in
  inventory) exec "$(dirname "${BASH_SOURCE[0]}")/capture-inventory.sh" ;;
  catalog) exec "$(dirname "${BASH_SOURCE[0]}")/capture-catalog.sh" ;;
  *)
    echo "ERROR: BASELINE_PART must be 'inventory' or 'catalog'." >&2
    exit 2
    ;;
esac
