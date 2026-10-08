#!/usr/bin/env bash
set -euo pipefail

fail() {
  echo "ERROR: $*" >&2
  exit 2
}

readonly repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly fixture="${repo_root}/legacy/product-service/src/test/resources/product-read-baseline/data-edges/cases.json"
readonly manifest="${repo_root}/legacy/product-service/src/test/resources/product-read-baseline/manifest.json"
readonly expected_case_ids='["DATA-EDGES-ABSENT-001","DATA-EDGES-CHOICE-PRESENCE-001","DATA-EDGES-EMPTY-001","DATA-EDGES-NULL-COVERS-001","DATA-EDGES-NULL-QUESTIONS-001","DATA-EDGES-PRIMITIVE-NULL-001","DATA-EDGES-PRODUCT-ORDER-001","DATA-EDGES-RICH-001","DATA-EDGES-SQL-NULL-CODE-001","DATA-EDGES-SQL-NULL-DEFINITION-001","DATA-EDGES-SUBTYPE-ABSENT-001","DATA-EDGES-SUBTYPE-NULL-001","DATA-EDGES-SUBTYPE-UNKNOWN-001","DATA-EDGES-UNKNOWN-CHOICE-001","DATA-EDGES-UNKNOWN-COVER-001","DATA-EDGES-UNKNOWN-QUESTION-001"]'

[[ -f "${fixture}" ]] || fail "Missing data-edge fixture: ${fixture}"

jq -e --argjson expected "${expected_case_ids}" '
  .schemaVersion == 1
  and .fixtureId == "DATA-EDGES-001"
  and .provenance == "synthetic-isolated"
  and (.description | type == "string" and length > 0)
  and ([.cases[].id] | sort) == $expected
  and (.cases | length == 16)
  and ([.cases[].id] | unique | length) == 16
  and ([.cases[].scenarioIds[]] | unique | length) == 25
  and ([.cases[].kind] | all(. == "http" or . == "database-constraint"))
  and ([.cases[].expected.outcome]
    | all(. == "http-success" or . == "http-failure" or . == "constraint-rejected"))
  and ([.cases[] | select(.kind == "http") | .requestPath] | all(startswith("/products")))
  and ([.cases[] | .rows | length] | all(. > 0))
  and ([.cases[] | .repetitions // 1] | all(. >= 1))
  and ([.cases[] | select(.id == "DATA-EDGES-RICH-001" or .id == "DATA-EDGES-PRODUCT-ORDER-001")
    | .repetitions] | all(. == 3))
  and ([.cases[] | select(.kind == "http") | .expected.contentType] | all(. == "application/json"))
  and ([.cases[] | select(.expected.outcome == "http-success") | .expected.status] | all(. == 200))
  and ([.cases[] | select(.expected.outcome == "http-success") | .expected.rawBody | fromjson | type]
    | all(. == "object" or . == "array"))
  and ([.cases[] | select(.expected.outcome == "http-failure") | .expected.status] | all(. == 500))
  and ([.cases[] | select(.expected.outcome == "http-failure") | .expected.rawBody | fromjson | type]
    | all(. == "object"))
  and ([.cases[] | select(.kind == "database-constraint") | .expected]
    | all(.outcome == "constraint-rejected" and .sqlState == "23502"))
  and (.cases[] | select(.id == "DATA-EDGES-RICH-001") | .expected.rawBody
    | contains("\"sumInsured\":12.3400")
      and contains("\"sumInsured\":12345678901234567890.123456789")
      and contains("\"sumInsured\":0.00"))
' "${fixture}" >/dev/null || fail "Invalid synthetic data-edge fixture."

jq -e --slurpfile fixture "${fixture}" '
  [
    .scenarios[]
    | select(.id == "DATA-DECIMAL-001"
      or .id == "DATA-QUESTION-VARIANTS-001"
      or .id == "DATA-PRESENCE-DEFAULTS-001"
      or .id == "DATA-UNKNOWN-FIELDS-001"
      or .id == "DATA-UNKNOWN-SUBTYPE-001"
      or .id == "DATA-ORDERING-001")
    | select(.group == "data-semantics"
      and .status == "captured"
      and .fixture == "data-edges/cases.json")
    | .variants[].id
  ] | sort == ([$fixture[0].cases[].scenarioIds[]] | unique | sort)
' "${manifest}" >/dev/null || fail "Manifest data-semantics coverage does not match the fixture."

if grep -Eq 'Bearer[[:space:]]+[A-Za-z0-9._-]+' "${fixture}"; then
  fail "Data-edge fixture contains a bearer token-shaped value."
fi

echo "Validated synthetic Product data-edge fixture."
