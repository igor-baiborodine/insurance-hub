#!/usr/bin/env bash
set -euo pipefail

fail() {
  echo "ERROR: $*" >&2
  exit 2
}

readonly repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly http_dir="${repo_root}/legacy/product-service/src/test/resources/product-read-baseline/http"
readonly catalog_dir="${repo_root}/legacy/product-service/src/test/resources/product-read-baseline/catalog"
readonly inventory_dir="${repo_root}/legacy/product-service/src/test/resources/product-read-baseline/inventory"
readonly manifest="${repo_root}/legacy/product-service/src/test/resources/product-read-baseline/manifest.json"

if [[ -n "${BASELINE_HTTP_FILE:-}" ]]; then
  readonly http_files=("${BASELINE_HTTP_FILE}")
elif [[ -n "${BASELINE_ENV:-}" ]]; then
  case "${BASELINE_ENV}" in
    local-dev | qa) readonly http_files=("${http_dir}/${BASELINE_ENV}.json") ;;
    *) fail "Unsupported BASELINE_ENV '${BASELINE_ENV}'; use local-dev or qa." ;;
  esac
else
  readonly http_files=("${http_dir}/local-dev.json" "${http_dir}/qa.json")
fi

readonly expected_ids='["HTTP-DIRECT-GET-CAR-001","HTTP-DIRECT-GET-FAI-001","HTTP-DIRECT-GET-HSI-001","HTTP-DIRECT-GET-TRI-001","HTTP-DIRECT-LIST-001","HTTP-GATEWAY-GET-CAR-001","HTTP-GATEWAY-GET-FAI-001","HTTP-GATEWAY-GET-HSI-001","HTTP-GATEWAY-GET-TRI-001","HTTP-GATEWAY-LIST-001"]'

for http_file in "${http_files[@]}"; do
  [[ -f "${http_file}" ]] || fail "Missing HTTP fixture: ${http_file}"
  environment="$(jq -r '.environment' "${http_file}")"
  case "${environment}" in local-dev | qa) ;; *) fail "Unsupported fixture environment '${environment}'." ;; esac
  jq -e --arg environment "${environment}" --argjson expected "${expected_ids}" '
    .schemaVersion == 1
    and .status == "captured"
    and .captureProfile == "http-success"
    and .provenance == "captured-representative"
    and .environment == $environment
    and (.capturedAt | test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$"))
    and (.sourceRevision | test("^[0-9a-f]{40}$"))
    and ([.observations[].scenarioId] | sort) == $expected
    and (.observations | length) == 10
    and ([.observations[] | .request.method] | all(. == "GET"))
    and ([.observations[] | .request.query] | all(. == ""))
    and ([.observations[] | .response.status] | all(. == 200))
    and ([.observations[] | .response.headers.contentType] | all(. == "application/json"))
    and ([.observations[] | .response.rawBody | fromjson | type] | all(. == "array" or . == "object"))
    and ([.observations[] | .response.rawBodySha256] | all(test("^[0-9a-f]{64}$")))
    and ([.observations[] | select(.boundary == "direct") | .request.credentialCategory] | all(. == "none"))
    and ([.observations[] | select(.boundary == "gateway") | .request.credentialCategory] | all(. == "gateway-valid"))
    and ([.observations[] | select(.boundary == "gateway") | .request.headers.authorization] | all(. == "Bearer <redacted>"))
    and .sanitizationRecord.bearerTokensCaptured == false
    and .sanitizationRecord.credentialsCaptured == false
  ' "${http_file}" >/dev/null || fail "Invalid ${environment} HTTP fixture."

  while IFS=$'\t' read -r checksum body; do
    actual="$(printf '%s' "${body}" | sha256sum | awk '{print $1}')"
    [[ "${actual}" == "${checksum}" ]] || fail "Raw HTTP body checksum mismatch in ${environment}."
  done < <(jq -r '.observations[] | [.response.rawBodySha256, .response.rawBody] | @tsv' "${http_file}")

  for kind in inventory catalog; do
    linked_file="${repo_root}/$(jq -r ".identities.${kind}.path" "${http_file}")"
    [[ -f "${linked_file}" ]] || fail "Missing linked ${kind} fixture for ${environment}."
    linked_checksum="$(sha256sum "${linked_file}" | awk '{print $1}')"
    [[ "${linked_checksum}" == "$(jq -r ".identities.${kind}.value" "${http_file}")" ]] || \
      fail "Linked ${environment} ${kind} checksum does not match."
  done
  jq -e --slurpfile catalog "${catalog_dir}/${environment}.json" '
    .identities.schema == $catalog[0].schemaIdentity and .identities.data == $catalog[0].dataIdentity
  ' "${http_file}" >/dev/null || fail "HTTP fixture identity does not match accepted ${environment} catalog."
  jq -e --arg environment "${environment}" --argjson expected "${expected_ids}" '
    [.scenarios[] | select(.id as $id | $expected | index($id))
      | select(.status == "captured" and .fixtures[$environment] == ("http/" + $environment + ".json")) | .id] | sort == $expected
  ' "${manifest}" >/dev/null || fail "Manifest does not register accepted ${environment} HTTP scenarios."
  jq -e '[.. | strings | select(startswith("Bearer ") and . != "Bearer <redacted>")] | length == 0' \
    "${http_file}" >/dev/null || fail "HTTP fixture contains a bearer token-shaped value."
  echo "Validated ${environment} Product HTTP fixture."
done
