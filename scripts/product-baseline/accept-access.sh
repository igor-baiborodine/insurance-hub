#!/usr/bin/env bash
set -euo pipefail

fail() { echo "ERROR: $*" >&2; exit 2; }
readonly script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly repo_root="$(cd "${script_dir}/../.." && pwd)"
readonly run_file="${1:-}"
[[ -f "${run_file}" ]] || fail "Provide an existing captured access run file."
cd "${repo_root}"
BASELINE_ACCESS_FILE="${run_file}" "${script_dir}/check-access.sh"
readonly environment="$(jq -r '.environment' "${run_file}")"
readonly output_file="legacy/product-service/src/test/resources/product-read-baseline/access/${environment}.json"
install -m 0644 "${run_file}" "${output_file}"
BASELINE_ACCESS_FILE="${output_file}" "${script_dir}/check-access.sh"
echo "Accepted reviewed ${environment} Product access fixture at ${output_file}."
