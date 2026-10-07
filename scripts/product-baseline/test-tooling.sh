#!/usr/bin/env bash
set -euo pipefail

readonly script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly temp_dir="$(mktemp -d)"
trap 'rm -rf "${temp_dir}"' EXIT

expect_failure() {
  local expected="$1"
  shift
  local output_file="${temp_dir}/failure-$RANDOM.log"

  if "$@" >"${output_file}" 2>&1; then
    echo "ERROR: Expected controlled failure containing '${expected}'." >&2
    exit 1
  fi
  if ! grep -Fq "${expected}" "${output_file}"; then
    echo "ERROR: Controlled failure did not contain '${expected}'." >&2
    cat "${output_file}" >&2
    exit 1
  fi
}

echo "Checking controlled Product baseline tooling failures..."
expect_failure "BASELINE_ENV is required" env -u BASELINE_ENV "${script_dir}/preflight.sh"
expect_failure "Unsupported BASELINE_ENV 'production'" \
  env BASELINE_ENV=production "${script_dir}/preflight.sh"
expect_failure "port 5482 belongs to Pricing" \
  env BASELINE_ENV=local-dev BASELINE_PRODUCT_DB_PORT=5482 "${script_dir}/preflight.sh"
expect_failure "Unsupported BASELINE_SUITE 'unknown'" \
  env BASELINE_SUITE=unknown "${script_dir}/validate-suite.sh"
expect_failure "not implemented yet" \
  env BASELINE_ENV=local-dev BASELINE_PART=inventory \
  "${script_dir}/not-implemented.sh" capture BASELINE_ENV BASELINE_PART

echo "Controlled failure checks passed before any live or disposable data mutation."
