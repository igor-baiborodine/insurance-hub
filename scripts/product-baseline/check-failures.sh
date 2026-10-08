#!/usr/bin/env bash
set -euo pipefail

fail() {
  echo "ERROR: $*" >&2
  exit 2
}

readonly repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly fixture="${repo_root}/legacy/product-service/src/test/resources/product-read-baseline/failures/cases.json"
readonly manifest="${repo_root}/legacy/product-service/src/test/resources/product-read-baseline/manifest.json"
readonly expected_case_ids='["FAIL-GATEWAY-DATABASE-GET-001","FAIL-GATEWAY-DATABASE-LIST-001","FAIL-GATEWAY-DECODE-STRUCTURE-001","FAIL-GATEWAY-DECODE-TYPE-001","FAIL-GATEWAY-EMPTY-LIST-001","FAIL-GATEWAY-GET-ERROR-001","FAIL-GATEWAY-GET-UNAVAILABLE-001","FAIL-GATEWAY-LIST-ERROR-001","FAIL-GATEWAY-LIST-UNAVAILABLE-001","FAIL-GATEWAY-LOOKUP-BAD-ENCODING-001","FAIL-GATEWAY-LOOKUP-EMPTY-SEGMENT-001","FAIL-GATEWAY-LOOKUP-ENCODED-SLASH-001","FAIL-GATEWAY-LOOKUP-LOWERCASE-001","FAIL-GATEWAY-LOOKUP-WHITESPACE-001","FAIL-GATEWAY-MISSING-001","FAIL-GATEWAY-SINGLE-GET-001","FAIL-GATEWAY-SINGLE-LIST-001","FAIL-PRODUCT-DATABASE-GET-001","FAIL-PRODUCT-DATABASE-LIST-001","FAIL-PRODUCT-DECODE-STRUCTURE-001","FAIL-PRODUCT-DECODE-TYPE-001","FAIL-PRODUCT-EMPTY-LIST-001","FAIL-PRODUCT-LOOKUP-BAD-ENCODING-001","FAIL-PRODUCT-LOOKUP-EMPTY-SEGMENT-001","FAIL-PRODUCT-LOOKUP-ENCODED-SLASH-001","FAIL-PRODUCT-LOOKUP-LOWERCASE-001","FAIL-PRODUCT-LOOKUP-WHITESPACE-001","FAIL-PRODUCT-MISSING-001","FAIL-PRODUCT-SINGLE-GET-001","FAIL-PRODUCT-SINGLE-LIST-001"]'

[[ -f "${fixture}" ]] || fail "Missing failure fixture: ${fixture}"

jq -e --argjson expected "${expected_case_ids}" '
  .schemaVersion == 1
  and .fixtureId == "HTTP-FAILURES-001"
  and .provenance == "synthetic-isolated"
  and (.cases | length == 30)
  and ([.cases[].id] | sort) == $expected
  and ([.cases[].scenarioIds[]] | unique | length) == 21
  and ([.cases[] | select(.target == "product")] | length) == 13
  and ([.cases[] | select(.target == "gateway")] | length) == 17
  and ([.cases[].target] | all(. == "product" or . == "gateway"))
  and ([.cases[].rawPath] | all(startswith("/products") or startswith("/api/products")))
  and ([.cases[].expected.contentType] | all(. == "application/json"))
  and ([.cases[].expected.status] | all(. == 200 or . == 400 or . == 404 or . == 500))
  and ([.cases[].expected.rawBody | fromjson | type] | all(. == "array" or . == "object"))
  and ([.cases[] | select(.target == "gateway") | .expected.downstreamAttempts]
    | all(. == 0 or . == 1 or . == 3))
  and ([.cases[] | select(.target == "gateway") | .expected.fallbackInvocations]
    | all(. == 0 or . == 1))
  and ([.cases[] | select(.target == "gateway" and .expected.fallbackInvocations == 1)] | length) == 8
  and ([.cases[] | select(.target == "gateway" and .expected.fallbackInvocations == 1)
    | .expected.downstreamAttempts] | all(. == 3))
  and ([.cases[] | select(.id == "FAIL-GATEWAY-LOOKUP-BAD-ENCODING-001")
    | .expected.downstreamAttempts] == [0])
  and ([.cases[] | select(.backendUnavailable == true)
    | (.expected.downstreamAttempts == 3 and (.expected.downstreamRawPaths | length) == 0)] | all)
  and ([.cases[] | select(.id == "FAIL-PRODUCT-DECODE-TYPE-001"
      or .id == "FAIL-PRODUCT-DECODE-STRUCTURE-001")
    | .expected.rawBody | contains("org.hibernate.HibernateException")] | all)
' "${fixture}" >/dev/null || fail "Invalid Product failure fixture."

jq -e --slurpfile fixture "${fixture}" '
  [
    .scenarios[]
    | select(.id == "HTTP-EMPTY-CATALOG-001"
      or .id == "HTTP-SINGLE-CATALOG-001"
      or .id == "HTTP-MISSING-001"
      or .id == "HTTP-LOOKUP-PATHS-001"
      or .id == "FAIL-DECODE-001"
      or .id == "FAIL-DATABASE-001"
      or .id == "FAIL-GATEWAY-BACKEND-001")
    | select(.group == "http-failure-and-boundary"
      and .status == "captured"
      and .fixture == "failures/cases.json")
    | .variants[].id
  ] | sort == ([$fixture[0].cases[].scenarioIds[]] | unique | sort)
' "${manifest}" >/dev/null || fail "Manifest failure coverage does not match the fixture."

if grep -Eq 'Bearer[[:space:]]+[A-Za-z0-9._-]+' "${fixture}"; then
  fail "Failure fixture contains a bearer token-shaped value."
fi

echo "Validated synthetic Product and gateway failure fixture."
