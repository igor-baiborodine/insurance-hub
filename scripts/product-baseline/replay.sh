#!/usr/bin/env bash
set -euo pipefail

readonly script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly repo_root="$(cd "${script_dir}/../.." && pwd)"
readonly fixture_set="${BASELINE_FIXTURE_SET:-}"
readonly http_fixture="${repo_root}/legacy/product-service/src/test/resources/product-read-baseline/http/${fixture_set}.json"

case "${fixture_set}" in
  local-dev | qa) ;;
  *)
    echo "ERROR: BASELINE_FIXTURE_SET is required; use local-dev or qa." >&2
    exit 2
    ;;
esac

BASELINE_ENV="${fixture_set}" "${script_dir}/check-inventory.sh"
BASELINE_ENV="${fixture_set}" "${script_dir}/check-catalog.sh"
BASELINE_ENV="${fixture_set}" "${script_dir}/check-http.sh"
"${script_dir}/resolve-java.sh"

echo "Replaying ${fixture_set} direct Product observations against fresh disposable PostgreSQL..."
mvn -B -ntp -f legacy/product-service/pom.xml \
  -Dskip.surefire.tests=true \
  -Dit.test=ProductBaselineReplayIT \
  -Dbaseline.fixture.set="${fixture_set}" \
  verify

echo "Replaying ${fixture_set} gateway observations through a real isolated gateway listener..."
mvn -B -ntp -f legacy/agent-portal-gateway/pom.xml \
  -Dskip.surefire.tests=true \
  -Dit.test=GatewayBaselineReplayIT \
  -Dbaseline.fixture.set="${fixture_set}" \
  -Dbaseline.fixture.file="${http_fixture}" \
  verify
