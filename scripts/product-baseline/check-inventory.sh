#!/usr/bin/env bash
set -euo pipefail

fail() {
  echo "ERROR: $*" >&2
  exit 2
}

readonly repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly inventory_dir="${repo_root}/legacy/product-service/src/test/resources/product-read-baseline/inventory"
readonly manifest="${repo_root}/legacy/product-service/src/test/resources/product-read-baseline/manifest.json"

case "${BASELINE_PART:-inventory}" in
  inventory) ;;
  *) fail "BASELINE_PART must be 'inventory' for the implemented fixture check." ;;
esac

if [[ -n "${BASELINE_ENV:-}" ]]; then
  case "${BASELINE_ENV}" in
    local-dev | qa) readonly environments=("${BASELINE_ENV}") ;;
    *) fail "Unsupported BASELINE_ENV '${BASELINE_ENV}'; use local-dev or qa." ;;
  esac
else
  readonly environments=(local-dev qa)
fi

for environment in "${environments[@]}"; do
  inventory_file="${inventory_dir}/${environment}.json"
  [[ -f "${inventory_file}" ]] || fail "Missing ${environment} inventory fixture: ${inventory_file}"

  expected_scenario="INV-LOCAL-001"
  expected_context="kind-local-dev-insurance-hub"
  expected_service_namespace="local-dev-all"
  expected_data_namespace="local-dev-all"
  if [[ "${environment}" == "qa" ]]; then
    expected_scenario="INV-QA-001"
    expected_context="qa-insurance-hub"
    expected_service_namespace="qa-svc"
    expected_data_namespace="qa-data"
  fi

  jq -e \
    --arg environment "${environment}" \
    --arg scenario "${expected_scenario}" \
    --arg context "${expected_context}" \
    --arg service_namespace "${expected_service_namespace}" \
    --arg data_namespace "${expected_data_namespace}" '
      .schemaVersion == 1
      and .scenarioId == $scenario
      and .status == "captured"
      and .captureProfile == "inventory"
      and .provenance == "captured-observation"
      and .credentialCategory == "database-capture"
      and .environment == $environment
      and (.capturedAt | test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$"))
      and (.sourceRevision | test("^[0-9a-f]{40}$"))
      and .kubernetes.context == $context
      and .kubernetes.namespaces.service == $service_namespace
      and .kubernetes.namespaces.data == $data_namespace
      and .kubernetes.workloads.product.readyReplicas > 0
      and .kubernetes.workloads.gateway.readyReplicas > 0
      and ([.kubernetes.workloads.product.pods[] | select(.ready == "True")] | length) > 0
      and ([.kubernetes.workloads.gateway.pods[] | select(.ready == "True")] | length) > 0
      and (.kubernetes.services.product.readyEndpointAddresses | length) > 0
      and (.kubernetes.services.gateway.readyEndpointAddresses | length) > 0
      and .kubernetes.postgresCluster.readyInstances > 0
      and .connection.forwardVerified == true
      and .connection.transactionReadOnly == true
      and .postgres.database == "product"
      and .postgres.schema == "public"
      and .postgres.table.qualifiedName == "public.product"
      and .postgres.table.owner == "product"
      and (.postgres.table.columns | map({
        name,
        underlyingType,
        characterMaximumLength,
        nullable
      })) == [
        {name: "code", underlyingType: "varchar", characterMaximumLength: 255, nullable: false},
        {name: "definition", underlyingType: "jsonb", characterMaximumLength: null, nullable: false}
      ]
      and ([.postgres.table.constraints[] | select(
        .type == "p" and .definition == "PRIMARY KEY (code)"
      )] | length) == 1
      and (.postgres.table.indexes | length) > 0
      and (.postgres.roleMemberships | length) > 0
      and .postgres.effectivePrivileges.database.connect == true
      and .postgres.effectivePrivileges.schema.usage == true
      and .postgres.effectivePrivileges.table.select == true
      and .postgres.catalogProvenance.rowCount == 4
      and .postgres.catalogProvenance.codes == ["CAR", "FAI", "HSI", "TRI"]
      and (.ownership.secretReferences | length) > 0
      and (.ownership.writers | length) == 3
      and .sanitizationRecord.secretValuesCaptured == false
      and .sanitizationRecord.bearerTokensCaptured == false
      and .sanitizationRecord.catalogDefinitionsCaptured == false
    ' "${inventory_file}" >/dev/null || fail "Invalid ${environment} inventory fixture."

  jq -e --arg scenario "${expected_scenario}" '
    [.scenarios[] | select(
      .id == $scenario
      and .group == "inventory"
      and .captureProfile == "inventory"
      and .provenance == "captured-observation"
    )] | length == 1
  ' "${manifest}" >/dev/null || fail "Manifest does not register ${expected_scenario}."

  if jq -e '
    .. | objects
    | select(
        (.name? | type == "string")
        and (.name | test("password|secret|token|credential|private|api[_-]?key"; "i"))
        and has("value")
        and .value != "<redacted>"
      )
  ' "${inventory_file}" >/dev/null; then
    fail "Inventory fixture contains an unredacted sensitive environment value."
  fi

  if grep -Eq 'Bearer[[:space:]]+[A-Za-z0-9._-]+' "${inventory_file}"; then
    fail "Inventory fixture contains a bearer token-shaped value."
  fi

  echo "Validated ${environment} Product inventory fixture."
done
