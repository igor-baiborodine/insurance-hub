#!/usr/bin/env bash
set -euo pipefail

readonly script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

case "${BASELINE_PART:-}" in
  inventory) exec "${script_dir}/check-inventory.sh" ;;
  catalog) exec "${script_dir}/check-catalog.sh" ;;
  http) exec "${script_dir}/check-http.sh" ;;
  data-edges) exec "${script_dir}/check-data-edges.sh" ;;
  failures) exec "${script_dir}/check-failures.sh" ;;
  access) exec "${script_dir}/check-access.sh" ;;
  db-permissions) exec "${script_dir}/check-db-permissions.sh" ;;
  "")
    BASELINE_PART=inventory "${script_dir}/check-inventory.sh"
    BASELINE_PART=catalog "${script_dir}/check-catalog.sh"
    BASELINE_PART=http "${script_dir}/check-http.sh"
    BASELINE_PART=data-edges "${script_dir}/check-data-edges.sh"
    BASELINE_PART=failures "${script_dir}/check-failures.sh"
    BASELINE_PART=access "${script_dir}/check-access.sh"
    BASELINE_PART=db-permissions "${script_dir}/check-db-permissions.sh"
    ;;
  *)
    echo "ERROR: BASELINE_PART must be 'inventory', 'catalog', 'http', 'data-edges', 'failures', 'access', or 'db-permissions' when provided." >&2
    exit 2
    ;;
esac
