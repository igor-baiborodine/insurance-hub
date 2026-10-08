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
readonly part="${BASELINE_PART:-}"
readonly product_url="${BASELINE_PRODUCT_URL:-http://127.0.0.1:19081}"
readonly gateway_url="${BASELINE_GATEWAY_URL:-http://127.0.0.1:19082}"
readonly token_file="${BASELINE_GATEWAY_TOKEN_FILE:-}"
readonly database_port="${BASELINE_PRODUCT_DB_PORT:-5492}"
readonly after_database_port="${BASELINE_PRODUCT_DB_AFTER_PORT:-${BASELINE_PRODUCT_DB_PORT:-5492}}"

[[ "${part}" == "http" ]] || fail "BASELINE_PART must be 'http'."
case "${environment}" in
  local-dev) readonly context="kind-local-dev-insurance-hub" ;;
  qa) readonly context="qa-insurance-hub" ;;
  *) fail "Unsupported BASELINE_ENV '${environment}'; use local-dev or qa." ;;
esac
[[ -f "${token_file}" ]] || fail "BASELINE_GATEWAY_TOKEN_FILE must name a readable short-lived token file."

for command_name in curl git jq sha256sum shred; do
  require_command "${command_name}"
done

cd "${repo_root}"
umask 077
readonly temp_dir="$(mktemp -d)"
trap 'rm -rf "${temp_dir}"' EXIT
readonly catalog_file="legacy/product-service/src/test/resources/product-read-baseline/catalog/${environment}.json"
readonly inventory_file="legacy/product-service/src/test/resources/product-read-baseline/inventory/${environment}.json"
[[ -f "${catalog_file}" && -f "${inventory_file}" ]] || fail "Accepted catalog and inventory fixtures are required."

BASELINE_PREFLIGHT_CATALOG_OUTPUT="${temp_dir}/before.json" "${script_dir}/preflight.sh" >/dev/null
bearer_token="$(tr -d '\r\n' <"${token_file}")"
[[ -n "${bearer_token}" ]] || fail "The gateway token file is empty."
readonly authorization_header_file="${temp_dir}/gateway-authorization.header"
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

  local status
  status="$(curl --silent --show-error \
    --request GET --header 'Accept: application/json' "${authorization[@]}" \
    --dump-header "${output_prefix}.headers" --output "${output_prefix}.body" \
    --write-out '%{http_code}' "${base_url}${path}")"
  [[ "${status}" == "200" ]] || fail "${scenario_id} returned HTTP ${status}."
  jq empty "${output_prefix}.body" || fail "${scenario_id} did not return JSON."
  local content_type content_length
  content_type="$(awk 'tolower($0) ~ /^content-type:/ {sub(/^[^:]+:[[:space:]]*/, ""); sub(/\r$/, ""); print; exit}' "${output_prefix}.headers")"
  content_length="$(awk 'tolower($0) ~ /^content-length:/ {sub(/^[^:]+:[[:space:]]*/, ""); sub(/\r$/, ""); print; exit}' "${output_prefix}.headers")"
  jq -n \
    --arg scenario_id "${scenario_id}" \
    --arg boundary "${boundary}" \
    --arg path "${path}" \
    --arg credential "$([[ "${boundary}" == "gateway" ]] && printf gateway-valid || printf none)" \
    --arg content_type "${content_type}" \
    --arg content_length "${content_length}" \
    --arg body_checksum "$(sha256sum "${output_prefix}.body" | awk '{print $1}')" \
    --rawfile body "${output_prefix}.body" '
      {
        scenarioId: $scenario_id,
        boundary: $boundary,
        request: {
          method: "GET",
          path: $path,
          query: "",
          headers: {accept: "application/json", authorization: (if $boundary == "gateway" then "Bearer <redacted>" else null end)},
          credentialCategory: $credential
        },
        response: {
          status: 200,
          headers: {contentType: $content_type, contentLength: ($content_length | tonumber)},
          rawBody: $body,
          rawBodySha256: $body_checksum
        },
        retryFallback: {observedAtCaptureBoundary: false, note: "Successful response; downstream attempts are not observable from this capture boundary"}
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
' "${temp_dir}/before.json" >/dev/null || fail "Schema or catalog identity changed during HTTP capture."
jq -e --slurpfile accepted "${catalog_file}" '
  .schemaIdentity == $accepted[0].schemaIdentity and .dataIdentity == $accepted[0].dataIdentity
' "${temp_dir}/after.json" >/dev/null || fail "Live schema or catalog identity does not match the accepted catalog fixture."

readonly captured_at="$(jq -r '.observedAt' "${temp_dir}/after.json")"
readonly run_stamp="$(printf '%s' "${captured_at}" | tr -d ':-' | sed 's/Z$//; s/T/-/')"
readonly run_dir="ai/artifacts/epic-4.2/issue-131/http-runs"
readonly output_file="${run_dir}/${environment}-${run_stamp}.json"
mkdir -p "${run_dir}"
[[ ! -e "${output_file}" ]] || fail "HTTP run already exists at ${output_file}; no capture was replaced."

jq -s \
  --arg environment "${environment}" \
  --arg context "${context}" \
  --arg captured_at "${captured_at}" \
  --arg source_revision "$(git rev-parse HEAD)" \
  --arg command "make product-baseline-capture BASELINE_ENV=${environment} BASELINE_PART=http BASELINE_PRODUCT_DB_PORT=${database_port} BASELINE_PRODUCT_DB_AFTER_PORT=${after_database_port} BASELINE_PRODUCT_URL=${product_url} BASELINE_GATEWAY_URL=${gateway_url} BASELINE_GATEWAY_TOKEN_FILE=<temporary-token-file>" \
  --arg inventory_path "${inventory_file}" \
  --arg inventory_checksum "$(sha256sum "${inventory_file}" | awk '{print $1}')" \
  --arg catalog_path "${catalog_file}" \
  --arg catalog_checksum "$(sha256sum "${catalog_file}" | awk '{print $1}')" \
  --slurpfile catalog "${catalog_file}" \
  --slurpfile inventory "${inventory_file}" '
    {
      schemaVersion: 1,
      status: "captured",
      captureProfile: "http-success",
      provenance: "captured-representative",
      environment: $environment,
      capturedAt: $captured_at,
      captureCommand: $command,
      sourceRevision: $source_revision,
      kubernetesContext: $context,
      identities: {
        inventory: {path: $inventory_path, algorithm: "SHA-256", value: $inventory_checksum},
        catalog: {path: $catalog_path, algorithm: "SHA-256", value: $catalog_checksum},
        schema: $catalog[0].schemaIdentity,
        data: $catalog[0].dataIdentity
      },
      serviceRevisions: {
        product: {
          deployment: $inventory[0].kubernetes.workloads.product.deployment,
          deploymentRevision: $inventory[0].kubernetes.workloads.product.deploymentRevision,
          image: $inventory[0].kubernetes.workloads.product.container.image,
          imageDigest: $inventory[0].kubernetes.workloads.product.pods[0].imageDigest
        },
        gateway: {
          deployment: $inventory[0].kubernetes.workloads.gateway.deployment,
          deploymentRevision: $inventory[0].kubernetes.workloads.gateway.deploymentRevision,
          image: $inventory[0].kubernetes.workloads.gateway.container.image,
          imageDigest: $inventory[0].kubernetes.workloads.gateway.pods[0].imageDigest
        }
      },
      observations: .,
      sanitizationRecord: {
        bearerTokensCaptured: false,
        credentialsCaptured: false,
        requestAuthorizationReplacement: "Bearer <redacted>",
        responseBodies: "Raw JSON response bytes stored as JSON strings without DTO deserialization",
        omittedVolatileHeaders: ["Date", "connection"]
      }
    }
  ' "${temp_dir}"/direct-*.json "${temp_dir}"/gateway-*.json >"${temp_dir}/http.json"

jq empty "${temp_dir}/http.json"
mv "${temp_dir}/http.json" "${output_file}"
chmod 0644 "${output_file}"
echo "Captured read-only ${environment} Product HTTP run at ${output_file}."
echo "Review it, then accept it with: scripts/product-baseline/accept-http.sh ${output_file}"
