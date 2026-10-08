#!/usr/bin/env bash
set -euo pipefail

readonly script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

case "${BASELINE_PART:-}" in
  inventory) exec "${script_dir}/check-inventory.sh" ;;
  catalog) exec "${script_dir}/check-catalog.sh" ;;
  http) exec "${script_dir}/check-http.sh" ;;
  "")
    BASELINE_PART=inventory "${script_dir}/check-inventory.sh"
    BASELINE_PART=catalog "${script_dir}/check-catalog.sh"
    BASELINE_PART=http "${script_dir}/check-http.sh"
    ;;
  *)
    echo "ERROR: BASELINE_PART must be 'inventory', 'catalog', or 'http' when provided." >&2
    exit 2
    ;;
esac
