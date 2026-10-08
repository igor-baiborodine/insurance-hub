#!/usr/bin/env bash
set -euo pipefail

readonly script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly repo_root="$(cd "${script_dir}/../.." && pwd)"
readonly accepted="${repo_root}/legacy/product-service/src/test/resources/product-read-baseline/http/local-dev.json"
readonly temp_dir="$(mktemp -d)"
trap 'rm -rf "${temp_dir}"' EXIT

require_failure() {
  local label="$1"
  local expected_text="$2"
  local candidate="$3"
  local output="${temp_dir}/${label}.log"
  if "${script_dir}/compare-http.py" "${accepted}" "${candidate}" >"${output}" 2>&1; then
    echo "ERROR: Comparator accepted ${label} drift." >&2
    exit 1
  fi
  grep -Fq "${expected_text}" "${output}" || {
    echo "ERROR: ${label} drift did not produce the expected visible difference." >&2
    cat "${output}" >&2
    exit 1
  }
}

command -v python3 >/dev/null 2>&1 || {
  echo "ERROR: Required command 'python3' is not available." >&2
  exit 2
}

cp "${accepted}" "${temp_dir}/accepted-copy.json"
"${script_dir}/compare-http.py" "${accepted}" "${temp_dir}/accepted-copy.json" >/dev/null

jq '(.observations[0].response.rawBody) |= (fromjson | to_entries | reverse | from_entries | tojson)' \
  "${accepted}" >"${temp_dir}/object-key-order.json"
"${script_dir}/compare-http.py" "${accepted}" "${temp_dir}/object-key-order.json" >/dev/null

jq '.observations[0].response.headers += {date: "volatile", contentLength: 1}' \
  "${accepted}" >"${temp_dir}/volatile-headers.json"
"${script_dir}/compare-http.py" "${accepted}" "${temp_dir}/volatile-headers.json" >/dev/null

jq '(.observations[] | select(.scenarioId == "HTTP-DIRECT-LIST-001") | .response.rawBody)
  |= (fromjson | reverse | tojson)' "${accepted}" >"${temp_dir}/product-order.json"
"${script_dir}/compare-http.py" "${accepted}" "${temp_dir}/product-order.json" >/dev/null

python3 - "${accepted}" "${temp_dir}/precision.json" <<'PY'
import sys
source = open(sys.argv[1], encoding="utf-8").read()
source = source.replace('200000}', '200000.0}', 1)
open(sys.argv[2], 'w', encoding="utf-8").write(source)
PY
require_failure precision "response JSON differs" "${temp_dir}/precision.json"

jq '(.observations[0].response.rawBody) |= (fromjson | .name = null | tojson)' \
  "${accepted}" >"${temp_dir}/presence.json"
require_failure presence "response JSON differs" "${temp_dir}/presence.json"

jq '(.observations[0].response.rawBody) |= (fromjson | .questions[0].type = "choice" | tojson)' \
  "${accepted}" >"${temp_dir}/subtype.json"
require_failure subtype "response JSON differs" "${temp_dir}/subtype.json"

jq '(.observations[] | select(.scenarioId == "HTTP-DIRECT-GET-FAI-001") | .response.rawBody)
  |= (fromjson | .covers |= reverse | tojson)' "${accepted}" >"${temp_dir}/nested-order.json"
require_failure nested-array-order "response JSON differs" "${temp_dir}/nested-order.json"

jq '.observations[0].response.status = 201' "${accepted}" >"${temp_dir}/status.json"
require_failure status "status differs" "${temp_dir}/status.json"

jq 'del(.observations[0])' "${accepted}" >"${temp_dir}/missing-scenario.json"
require_failure missing-scenario "scenario set differs" "${temp_dir}/missing-scenario.json"

echo "Comparator accepted only object-key, top-level product-order, and ignored-header changes."
echo "Comparator detected precision, presence, subtype, nested-array-order, status, and scenario drift."
