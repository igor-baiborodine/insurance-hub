#!/usr/bin/env bash
set -euo pipefail

fail() {
  echo "ERROR: $*" >&2
  exit 2
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "Required command '$1' is not available."
}

readonly script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly repo_root="$(cd "${script_dir}/../.." && pwd)"
readonly environment="${BASELINE_ENV:-}"
readonly product_url="${BASELINE_PRODUCT_URL:-http://127.0.0.1:19081}"
readonly gateway_url="${BASELINE_GATEWAY_URL:-http://127.0.0.1:19082}"
readonly token_file="${BASELINE_GATEWAY_TOKEN_FILE:-}"
readonly after_database_port="${BASELINE_PRODUCT_DB_AFTER_PORT:-${BASELINE_PRODUCT_DB_PORT:-5492}}"

case "${environment}" in
  local-dev | qa) ;;
  *) fail "BASELINE_ENV is required; use local-dev or qa." ;;
esac
[[ -f "${token_file}" ]] || fail "BASELINE_GATEWAY_TOKEN_FILE must name a readable short-lived token file."
for command_name in curl jq python3 shred; do
  require_command "${command_name}"
done

cd "${repo_root}"
umask 077
readonly temp_dir="$(mktemp -d)"
readonly accepted="legacy/product-service/src/test/resources/product-read-baseline/http/${environment}.json"
readonly catalog="legacy/product-service/src/test/resources/product-read-baseline/catalog/${environment}.json"
readonly authorization_header_file="${temp_dir}/gateway-authorization.header"
trap '[[ ! -f "${authorization_header_file}" ]] || shred -u "${authorization_header_file}"; rm -rf "${temp_dir}"' EXIT

BASELINE_PREFLIGHT_CATALOG_OUTPUT="${temp_dir}/before.json" "${script_dir}/preflight.sh" >/dev/null
jq -e --slurpfile accepted "${catalog}" '
  .schemaIdentity == $accepted[0].schemaIdentity and .dataIdentity == $accepted[0].dataIdentity
' "${temp_dir}/before.json" >/dev/null || fail "Live schema or catalog identity does not match the accepted ${environment} baseline."

bearer_token="$(tr -d '\r\n' <"${token_file}")"
[[ -n "${bearer_token}" ]] || fail "The gateway token file is empty."
printf 'Authorization: Bearer %s\n' "${bearer_token}" >"${authorization_header_file}"
chmod 0600 "${authorization_header_file}"
unset bearer_token

capture_response() {
  local boundary="$1"
  local scenario_id="$2"
  local path="$3"
  local output_prefix="$4"
  local base_url authorization=()
  if [[ "${boundary}" == "direct" ]]; then
    base_url="${product_url}"
  else
    base_url="${gateway_url}"
    authorization=(--header "@${authorization_header_file}")
  fi

  local status content_type
  status="$(curl --silent --show-error --request GET --header 'Accept: application/json' \
    "${authorization[@]}" --dump-header "${output_prefix}.headers" --output "${output_prefix}.body" \
    --write-out '%{http_code}' "${base_url}${path}")"
  jq empty "${output_prefix}.body" || fail "${scenario_id} did not return JSON."
  content_type="$(awk 'tolower($0) ~ /^content-type:/ {sub(/^[^:]+:[[:space:]]*/, ""); sub(/\r$/, ""); print; exit}' \
    "${output_prefix}.headers")"
  jq -n --arg scenario_id "${scenario_id}" --arg boundary "${boundary}" --arg path "${path}" \
    --arg content_type "${content_type}" --arg status "${status}" --rawfile body "${output_prefix}.body" '
      {
        scenarioId: $scenario_id,
        boundary: $boundary,
        request: {method: "GET", path: $path, query: ""},
        response: {status: ($status | tonumber), headers: {contentType: $content_type}, rawBody: $body}
      }
    ' >"${output_prefix}.json"
}

capture_response direct HTTP-DIRECT-LIST-001 /products "${temp_dir}/direct-list"
capture_response gateway HTTP-GATEWAY-LIST-001 /api/products "${temp_dir}/gateway-list"
for code in CAR FAI HSI TRI; do
  capture_response direct "HTTP-DIRECT-GET-${code}-001" "/products/${code}" "${temp_dir}/direct-${code}"
  capture_response gateway "HTTP-GATEWAY-GET-${code}-001" "/api/products/${code}" "${temp_dir}/gateway-${code}"
done
shred -u "${authorization_header_file}"

BASELINE_PRODUCT_DB_PORT="${after_database_port}" \
  BASELINE_PREFLIGHT_CATALOG_OUTPUT="${temp_dir}/after.json" \
  "${script_dir}/preflight.sh" >/dev/null
jq -e --slurpfile after "${temp_dir}/after.json" '
  .schemaIdentity == $after[0].schemaIdentity and .dataIdentity == $after[0].dataIdentity
' "${temp_dir}/before.json" >/dev/null || fail "Schema or catalog identity changed during live verification."

jq -s --arg environment "${environment}" '{environment: $environment, observations: .}' \
  "${temp_dir}"/direct-*.json "${temp_dir}"/gateway-*.json >"${temp_dir}/observed.json"
"${script_dir}/compare-http.py" "${accepted}" "${temp_dir}/observed.json"
echo "Read-only live verification passed for ${environment}; schema and catalog identities were unchanged."
