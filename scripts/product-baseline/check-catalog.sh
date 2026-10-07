#!/usr/bin/env bash
set -euo pipefail

fail() {
  echo "ERROR: $*" >&2
  exit 2
}

readonly repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly catalog_dir="${repo_root}/legacy/product-service/src/test/resources/product-read-baseline/catalog"
readonly inventory_dir="${repo_root}/legacy/product-service/src/test/resources/product-read-baseline/inventory"
readonly manifest="${repo_root}/legacy/product-service/src/test/resources/product-read-baseline/manifest.json"

if [[ -n "${BASELINE_CATALOG_FILE:-}" ]]; then
  readonly catalog_files=("${BASELINE_CATALOG_FILE}")
elif [[ -n "${BASELINE_ENV:-}" ]]; then
  case "${BASELINE_ENV}" in
    local-dev | qa) readonly catalog_files=("${catalog_dir}/${BASELINE_ENV}.json") ;;
    *) fail "Unsupported BASELINE_ENV '${BASELINE_ENV}'; use local-dev or qa." ;;
  esac
else
  readonly catalog_files=("${catalog_dir}/local-dev.json" "${catalog_dir}/qa.json")
fi

for catalog_file in "${catalog_files[@]}"; do
  [[ -f "${catalog_file}" ]] || fail "Missing catalog fixture: ${catalog_file}"
  environment="$(jq -r '.environment' "${catalog_file}")"
  case "${environment}" in
    local-dev) expected_scenario="DATA-LOCAL-001" ;;
    qa) expected_scenario="DATA-QA-001" ;;
    *) fail "Catalog fixture has unsupported environment '${environment}'." ;;
  esac

  jq -e --arg environment "${environment}" --arg scenario "${expected_scenario}" '
    .schemaVersion == 1
    and .scenarioId == $scenario
    and .status == "captured"
    and .captureProfile == "stored-catalog"
    and .provenance == "captured-representative"
    and .credentialCategory == "database-capture"
    and .environment == $environment
    and (.capturedAt | test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$"))
    and (.sourceRevision | test("^[0-9a-f]{40}$"))
    and .connection.transactionReadOnly == true
    and .inventoryIdentity.path == ("legacy/product-service/src/test/resources/product-read-baseline/inventory/" + $environment + ".json")
    and .inventoryIdentity.algorithm == "SHA-256"
    and (.inventoryIdentity.value | test("^[0-9a-f]{64}$"))
    and .database == {name: "product", schema: "public", table: "public.product", tableOwner: "product"}
    and .schemaIdentity.algorithm == "MD5"
    and (.schemaIdentity.value | test("^[0-9a-f]{32}$"))
    and .dataIdentity.algorithm == "MD5"
    and (.dataIdentity.value | test("^[0-9a-f]{32}$"))
    and .dataIdentity.rowCount == 4
    and .dataIdentity.codes == ["CAR", "FAI", "HSI", "TRI"]
    and ([.rows[].code] == ["CAR", "FAI", "HSI", "TRI"])
    and ([.rows[] | .rawLosslessDefinitionJson | fromjson | type] | all(. == "object"))
    and ([.rows[] | .checksum.algorithm] | all(. == "MD5"))
    and ([.rows[] | .checksum.value | test("^[0-9a-f]{32}$")] | all)
    and (.coverage.questionTypes | sort) == ["choice", "numeric"]
    and (.coverage.products | length) == 4
    and .sanitizationRecord.credentialsCaptured == false
    and .sanitizationRecord.bearerTokensCaptured == false
  ' "${catalog_file}" >/dev/null || fail "Invalid ${environment} catalog fixture."

  while IFS= read -r code; do
    expected_checksum="$(jq -r --arg code "${code}" '.rows[] | select(.code == $code) | .checksum.value' "${catalog_file}")"
    definition="$(jq -r --arg code "${code}" '.rows[] | select(.code == $code) | .rawLosslessDefinitionJson' "${catalog_file}")"
    actual_checksum="$(printf '%s' "${definition}" | md5sum | awk '{print $1}')"
    [[ "${actual_checksum}" == "${expected_checksum}" ]] || \
      fail "Raw JSON checksum mismatch for ${environment}/${code}."
  done < <(jq -r '.rows[].code' "${catalog_file}")

  catalog_identity_input="$(jq -r '[.rows[] | (.code + ":" + .checksum.value)] | join(",")' "${catalog_file}")"
  catalog_identity="$(printf '%s' "${catalog_identity_input}" | md5sum | awk '{print $1}')"
  [[ "${catalog_identity}" == "$(jq -r '.dataIdentity.value' "${catalog_file}")" ]] || \
    fail "Catalog checksum mismatch for ${environment}."

  inventory_file="${inventory_dir}/${environment}.json"
  [[ -f "${inventory_file}" ]] || fail "Missing linked ${environment} inventory fixture."
  inventory_checksum="$(sha256sum "${inventory_file}" | awk '{print $1}')"
  [[ "${inventory_checksum}" == "$(jq -r '.inventoryIdentity.value' "${catalog_file}")" ]] || \
    fail "Linked ${environment} inventory checksum does not match."

  jq -e --arg scenario "${expected_scenario}" '
    [.scenarios[] | select(
      .id == $scenario
      and .group == "representative-data"
      and .captureProfile == "stored-catalog"
      and .provenance == "captured-representative"
      and .status == "captured"
      and .fixture == ("catalog/" + (if $scenario == "DATA-LOCAL-001" then "local-dev" else "qa" end) + ".json")
    )] | length == 1
  ' "${manifest}" >/dev/null || fail "Manifest does not register ${expected_scenario}."

  if grep -Eq 'Bearer[[:space:]]+[A-Za-z0-9._-]+' "${catalog_file}"; then
    fail "Catalog fixture contains a bearer token-shaped value."
  fi

  echo "Validated ${environment} Product catalog fixture."
done
