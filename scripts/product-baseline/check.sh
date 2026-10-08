#!/usr/bin/env bash
set -euo pipefail

readonly script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

"${script_dir}/check-fixtures.sh"
make --no-print-directory product-baseline-test BASELINE_SUITE=all
BASELINE_FIXTURE_SET=local-dev "${script_dir}/replay.sh"
BASELINE_FIXTURE_SET=qa "${script_dir}/replay.sh"

echo "Product baseline offline aggregate passed."
echo "Run 'make product-baseline-verify BASELINE_ENV=<environment> ...' separately for each active live environment."
