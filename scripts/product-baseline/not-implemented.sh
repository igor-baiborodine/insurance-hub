#!/usr/bin/env bash
set -euo pipefail

readonly capability="${1:?capability name is required}"
shift

for variable_name in "$@"; do
  if [[ -z "${!variable_name:-}" ]]; then
    echo "ERROR: ${variable_name} is required for product-baseline-${capability}." >&2
    exit 2
  fi
done

echo "ERROR: Product baseline capability '${capability}' is not implemented yet." >&2
exit 3
