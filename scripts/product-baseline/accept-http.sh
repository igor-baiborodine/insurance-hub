#!/usr/bin/env bash
set -euo pipefail

fail() {
  echo "ERROR: $*" >&2
  exit 2
}

readonly script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly repo_root="$(cd "${script_dir}/../.." && pwd)"
readonly run_file="${1:-}"
[[ -f "${run_file}" ]] || fail "Provide an existing captured HTTP run file."

cd "${repo_root}"
BASELINE_HTTP_FILE="${run_file}" "${script_dir}/check-http.sh"
readonly environment="$(jq -r '.environment' "${run_file}")"
readonly output_dir="legacy/product-service/src/test/resources/product-read-baseline/http"
readonly output_file="${output_dir}/${environment}.json"
mkdir -p "${output_dir}"
install -m 0644 "${run_file}" "${output_file}"
BASELINE_HTTP_FILE="${output_file}" "${script_dir}/check-http.sh"
echo "Accepted reviewed ${environment} Product HTTP fixture at ${output_file}."
