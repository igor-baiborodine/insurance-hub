#!/usr/bin/env bash
set -euo pipefail

fail() { echo "ERROR: $*" >&2; exit 2; }
readonly repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly access_dir="${repo_root}/legacy/product-service/src/test/resources/product-read-baseline/access"
readonly synthetic="${access_dir}/cases.json"
readonly manifest="${repo_root}/legacy/product-service/src/test/resources/product-read-baseline/manifest.json"
readonly expected_live_ids='["ACCESS-DIRECT-GET-MALFORMED-001","ACCESS-DIRECT-GET-NONE-001","ACCESS-DIRECT-GET-SIGNATURE-001","ACCESS-DIRECT-GET-VALID-001","ACCESS-DIRECT-LIST-MALFORMED-001","ACCESS-DIRECT-LIST-NONE-001","ACCESS-DIRECT-LIST-SIGNATURE-001","ACCESS-DIRECT-LIST-VALID-001","ACCESS-GATEWAY-GET-MALFORMED-001","ACCESS-GATEWAY-GET-MISSING-001","ACCESS-GATEWAY-GET-SIGNATURE-001","ACCESS-GATEWAY-GET-VALID-001","ACCESS-GATEWAY-LIST-MALFORMED-001","ACCESS-GATEWAY-LIST-MISSING-001","ACCESS-GATEWAY-LIST-SIGNATURE-001","ACCESS-GATEWAY-LIST-VALID-001"]'

jq -e '
  .schemaVersion == 1 and .fixtureId == "JAVA-ACCESS-001" and .provenance == "synthetic-isolated"
  and (.cases | length == 38)
  and ([.cases[] | select(.target == "gateway")] | length == 20)
  and ([.cases[] | select(.target == "product")] | length == 18)
  and ([.cases[] | select(.target == "gateway" and (.credential == "missing" or .credential == "malformed"
      or .credential == "invalid-signature" or .credential == "expired" or .credential == "missing-subject"))
      | .expected.status] | all(. == 401))
  and ([.cases[] | select(.target == "gateway" and (.credential == "valid" or .credential == "not-before"
      or .credential == "issuer-variation" or .credential == "audience-variation" or .credential == "role-variation"))
      | .expected.status] | all(. == 200))
  and ([.cases[] | select(.target == "product") | .expected.status] | all(. == 200))
  and ([.cases[] | select(.target == "gateway") | .expected.downstreamAuthorizationPresent] | all(. == false))
  and ([.cases[] | select(.target == "gateway") | .expected.downstreamIdentityHeaderNames] | all(length == 0))
  and .sanitization.tokensRetained == false and .sanitization.signingMaterialRetained == false
' "${synthetic}" >/dev/null || fail "Invalid isolated Java access fixture."

if [[ -n "${BASELINE_ACCESS_FILE:-}" ]]; then
  access_files=("${BASELINE_ACCESS_FILE}")
elif [[ -n "${BASELINE_ENV:-}" ]]; then
  access_files=("${access_dir}/${BASELINE_ENV}.json")
else
  access_files=("${access_dir}/local-dev.json" "${access_dir}/qa.json")
fi

for access_file in "${access_files[@]}"; do
  [[ -f "${access_file}" ]] || fail "Missing access fixture: ${access_file}"
  environment="$(jq -r '.environment' "${access_file}")"
  case "${environment}" in local-dev | qa) ;; *) fail "Unsupported fixture environment '${environment}'." ;; esac
  jq -e --arg environment "${environment}" --argjson expected "${expected_live_ids}" '
    .schemaVersion == 1 and .status == "captured" and .captureProfile == "access"
    and .provenance == "captured-representative" and .environment == $environment
    and (.observations | length == 16)
    and ([.observations[].scenarioId] | sort) == $expected
    and ([.observations[] | select(.boundary == "direct") | .response.status] | all(. == 200))
    and ([.observations[] | select(.boundary == "gateway" and .request.credentialCategory == "valid")
      | .response.status] | all(. == 200))
    and ([.observations[] | select(.boundary == "gateway" and .request.credentialCategory != "valid")
      | .response.status] | all(. == 401))
    and ([.observations[].response.rawBodySha256] | all(test("^[0-9a-f]{64}$")))
    and ([.observations[] | select(.response.status == 200) | .response.rawBody | fromjson | type]
      | all(. == "array" or . == "object"))
    and ([.observations[] | select(.response.status == 401) | .response.rawBody] | all(. == ""))
    and ([.. | strings | select(startswith("Bearer ") and . != "Bearer <redacted>")] | length == 0)
    and .sanitizationRecord.bearerTokensCaptured == false
  ' "${access_file}" >/dev/null || fail "Invalid ${environment} access fixture."
  while IFS=$'\t' read -r checksum body; do
    [[ "$(printf '%s' "${body}" | sha256sum | awk '{print $1}')" == "${checksum}" ]] || \
      fail "Raw access body checksum mismatch in ${environment}."
  done < <(jq -r '.observations[] | [.response.rawBodySha256, .response.rawBody] | @tsv' "${access_file}")
  for kind in inventory catalog; do
    linked_file="${repo_root}/$(jq -r ".identities.${kind}.path" "${access_file}")"
    [[ -f "${linked_file}" ]] || fail "Missing linked ${kind} fixture for ${environment}."
    [[ "$(sha256sum "${linked_file}" | awk '{print $1}')" == \
      "$(jq -r ".identities.${kind}.value" "${access_file}")" ]] || \
      fail "Linked ${environment} ${kind} checksum does not match."
  done
  jq -e --slurpfile catalog "${access_dir}/../catalog/${environment}.json" '
    .identities.schema == $catalog[0].schemaIdentity and .identities.data == $catalog[0].dataIdentity
  ' "${access_file}" >/dev/null || fail "Access fixture identity does not match accepted ${environment} catalog."
  echo "Validated ${environment} Product access fixture."
done

if [[ -z "${BASELINE_ACCESS_FILE:-}" ]]; then
  jq -e '
    [.scenarios[] | select(.group == "access") | select(.status == "captured"
      and .fixture == "access/cases.json" and .fixtures["local-dev"] == "access/local-dev.json"
      and .fixtures.qa == "access/qa.json")] | length == 5
  ' "${manifest}" >/dev/null || fail "Manifest access scenarios are not linked to all accepted fixtures."
fi
echo "Validated isolated Java access fixture."
