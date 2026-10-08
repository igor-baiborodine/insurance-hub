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
readonly database_port="${BASELINE_PRODUCT_DB_PORT:-5492}"
readonly after_database_port="${BASELINE_PRODUCT_DB_AFTER_PORT:-${BASELINE_PRODUCT_DB_PORT:-5492}}"

[[ "${BASELINE_PART:-}" == "access" ]] || fail "BASELINE_PART must be 'access'."
case "${environment}" in
  local-dev) readonly context="kind-local-dev-insurance-hub" ;;
  qa) readonly context="qa-insurance-hub" ;;
  *) fail "Unsupported BASELINE_ENV '${environment}'; use local-dev or qa." ;;
esac
[[ -f "${token_file}" ]] || fail "BASELINE_GATEWAY_TOKEN_FILE must name a readable short-lived token file."
for command_name in curl date git jq sha256sum shred; do
  require_command "${command_name}"
done

cd "${repo_root}"
umask 077
readonly temp_dir="$(mktemp -d)"
trap 'rm -rf "${temp_dir}"' EXIT
readonly catalog_file="legacy/product-service/src/test/resources/product-read-baseline/catalog/${environment}.json"
readonly inventory_file="legacy/product-service/src/test/resources/product-read-baseline/inventory/${environment}.json"

BASELINE_PREFLIGHT_CATALOG_OUTPUT="${temp_dir}/before.json" "${script_dir}/preflight.sh" >/dev/null
observed_epoch="$(date -u -d "$(jq -r '.observedAt' "${temp_dir}/before.json")" +%s)" || \
  fail "Could not parse the environment observation timestamp."
host_epoch="$(date -u +%s)"
clock_delta=$((host_epoch - observed_epoch))
((clock_delta < 0)) && clock_delta=$((-clock_delta))
((clock_delta <= 300)) || fail "Environment clock differs from the capture host by more than five minutes."
valid_token="$(tr -d '\r\n' <"${token_file}")"
[[ -n "${valid_token}" ]] || fail "The gateway token file is empty."
IFS='.' read -r token_header token_payload token_signature extra <<<"${valid_token}"
[[ -n "${token_header}" && -n "${token_payload}" && -n "${token_signature}" && -z "${extra:-}" ]] || \
  fail "The gateway token file does not contain a compact signed JWT."
if [[ "${token_signature:0:1}" == "A" ]]; then
  signature_prefix="B"
else
  signature_prefix="A"
fi
invalid_token="${token_header}.${token_payload}.${signature_prefix}${token_signature:1}"
printf 'Authorization: Bearer %s\n' "${valid_token}" >"${temp_dir}/valid.header"
printf 'Authorization: Bearer %s\n' "${invalid_token}" >"${temp_dir}/invalid.header"
printf 'Authorization: Bearer not-a-jwt\n' >"${temp_dir}/malformed.header"
chmod 0600 "${temp_dir}"/*.header
unset valid_token invalid_token token_header token_payload token_signature signature_prefix

capture_response() {
  local boundary="$1"
  local route="$2"
  local credential="$3"
  local path="$4"
  local output_prefix="$5"
  local base_url header=()
  local scenario_credential="${credential^^}"
  scenario_credential="${scenario_credential/INVALID-SIGNATURE/SIGNATURE}"
  if [[ "${boundary}" == "direct" && "${scenario_credential}" == "MISSING" ]]; then
    scenario_credential="NONE"
  fi
  if [[ "${boundary}" == "direct" ]]; then
    base_url="${product_url}"
  else
    base_url="${gateway_url}"
  fi
  case "${credential}" in
    valid) header=(--header "@${temp_dir}/valid.header") ;;
    malformed) header=(--header "@${temp_dir}/malformed.header") ;;
    invalid-signature) header=(--header "@${temp_dir}/invalid.header") ;;
    missing) ;;
    *) fail "Unsupported credential category '${credential}'." ;;
  esac

  local status content_type
  status="$(curl --silent --show-error --request GET --header 'Accept: application/json' "${header[@]}" \
    --dump-header "${output_prefix}.headers" --output "${output_prefix}.body" --write-out '%{http_code}' \
    "${base_url}${path}")"
  content_type="$(awk 'tolower($0) ~ /^content-type:/ {sub(/^[^:]+:[[:space:]]*/, ""); sub(/\r$/, ""); print; exit}' "${output_prefix}.headers")"
  jq -n \
    --arg scenario_id "ACCESS-${boundary^^}-${route^^}-${scenario_credential}-001" \
    --arg boundary "${boundary}" --arg route "${route}" --arg path "${path}" \
    --arg credential "${credential}" --arg status "${status}" --arg content_type "${content_type}" \
    --arg body_checksum "$(sha256sum "${output_prefix}.body" | awk '{print $1}')" \
    --rawfile body "${output_prefix}.body" '
      {
        scenarioId: $scenario_id,
        boundary: $boundary,
        route: $route,
        request: {
          method: "GET", path: $path, credentialCategory: $credential,
          authorization: (if $credential == "missing" then null else "Bearer <redacted>" end)
        },
        response: {
          status: ($status | tonumber), contentType: $content_type,
          rawBody: $body, rawBodySha256: $body_checksum
        }
      }
    ' >"${output_prefix}.json"
}

for boundary in direct gateway; do
  for route in list get; do
    if [[ "${boundary}" == "direct" ]]; then
      [[ "${route}" == "list" ]] && path="/products" || path="/products/TRI"
    else
      [[ "${route}" == "list" ]] && path="/api/products" || path="/api/products/TRI"
    fi
    for credential in valid missing malformed invalid-signature; do
      capture_response "${boundary}" "${route}" "${credential}" "${path}" \
        "${temp_dir}/${boundary}-${route}-${credential}"
    done
  done
done
shred -u "${temp_dir}"/*.header

BASELINE_PRODUCT_DB_PORT="${after_database_port}" BASELINE_PREFLIGHT_CATALOG_OUTPUT="${temp_dir}/after.json" \
  "${script_dir}/preflight.sh" >/dev/null
jq -e --slurpfile after "${temp_dir}/after.json" '
  .schemaIdentity == $after[0].schemaIdentity and .dataIdentity == $after[0].dataIdentity
' "${temp_dir}/before.json" >/dev/null || fail "Schema or catalog identity changed during access capture."
jq -e --slurpfile accepted "${catalog_file}" '
  .schemaIdentity == $accepted[0].schemaIdentity and .dataIdentity == $accepted[0].dataIdentity
' "${temp_dir}/after.json" >/dev/null || fail "Live catalog identity does not match the accepted fixture."

readonly captured_at="$(jq -r '.observedAt' "${temp_dir}/after.json")"
readonly run_stamp="$(printf '%s' "${captured_at}" | tr -d ':-' | sed 's/Z$//; s/T/-/')"
readonly run_dir="ai/artifacts/epic-4.2/issue-131/access-runs"
readonly output_file="${run_dir}/${environment}-${run_stamp}.json"
mkdir -p "${run_dir}"
[[ ! -e "${output_file}" ]] || fail "Access run already exists at ${output_file}; no capture was replaced."

jq -s \
  --arg environment "${environment}" --arg context "${context}" --arg captured_at "${captured_at}" \
  --arg source_revision "$(git rev-parse HEAD)" \
  --arg command "make product-baseline-capture BASELINE_ENV=${environment} BASELINE_PART=access BASELINE_PRODUCT_DB_PORT=${database_port} BASELINE_PRODUCT_DB_AFTER_PORT=${after_database_port} BASELINE_PRODUCT_URL=${product_url} BASELINE_GATEWAY_URL=${gateway_url} BASELINE_GATEWAY_TOKEN_FILE=<temporary-token-file>" \
  --arg inventory_path "${inventory_file}" --arg inventory_checksum "$(sha256sum "${inventory_file}" | awk '{print $1}')" \
  --arg catalog_path "${catalog_file}" --arg catalog_checksum "$(sha256sum "${catalog_file}" | awk '{print $1}')" \
  --slurpfile catalog "${catalog_file}" --slurpfile inventory "${inventory_file}" '
    {
      schemaVersion: 1, status: "captured", captureProfile: "access", provenance: "captured-representative",
      environment: $environment, capturedAt: $captured_at, captureCommand: $command,
      sourceRevision: $source_revision, kubernetesContext: $context,
      identities: {
        inventory: {path: $inventory_path, algorithm: "SHA-256", value: $inventory_checksum},
        catalog: {path: $catalog_path, algorithm: "SHA-256", value: $catalog_checksum},
        schema: $catalog[0].schemaIdentity, data: $catalog[0].dataIdentity
      },
      serviceRevisions: {
        product: $inventory[0].kubernetes.workloads.product,
        gateway: $inventory[0].kubernetes.workloads.gateway
      },
      observations: .,
      liveCoverage: ["valid", "missing", "malformed", "invalid-signature"],
      isolatedCoverage: ["expired", "not-before", "issuer", "audience", "role", "identity-propagation"],
      sanitizationRecord: {bearerTokensCaptured: false, signingMaterialCaptured: false, credentialsCaptured: false}
    }
  ' "${temp_dir}"/direct-*.json "${temp_dir}"/gateway-*.json >"${temp_dir}/access.json"

mv "${temp_dir}/access.json" "${output_file}"
chmod 0644 "${output_file}"
echo "Captured read-only ${environment} Product access run at ${output_file}."
echo "Review it, then accept it with: scripts/product-baseline/accept-access.sh ${output_file}"
