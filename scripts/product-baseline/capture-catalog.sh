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

[[ "${part}" == "catalog" ]] || fail "BASELINE_PART must be 'catalog'."

case "${environment}" in
  local-dev)
    readonly context="kind-local-dev-insurance-hub"
    readonly scenario_id="DATA-LOCAL-001"
    ;;
  qa)
    readonly context="qa-insurance-hub"
    readonly scenario_id="DATA-QA-001"
    ;;
  *) fail "Unsupported BASELINE_ENV '${environment}'; use local-dev or qa." ;;
esac

for command_name in git jq kubectl psql sha256sum; do
  require_command "${command_name}"
done

cd "${repo_root}"
umask 077
readonly temp_dir="$(mktemp -d)"
trap 'rm -rf "${temp_dir}"' EXIT

readonly database_host="${BASELINE_PRODUCT_DB_HOST:-127.0.0.1}"
readonly database_port="${BASELINE_PRODUCT_DB_PORT:-5492}"
BASELINE_PREFLIGHT_CATALOG_OUTPUT="${temp_dir}/database.json" \
  "${script_dir}/preflight.sh"

jq -e '
  .transactionIsolation == "repeatable read"
  and .transactionReadOnly == true
  and .database == "product"
  and .schema == "public"
  and .table == "public.product"
  and .dataIdentity.rowCount == 4
  and .dataIdentity.codes == ["CAR", "FAI", "HSI", "TRI"]
' "${temp_dir}/database.json" >/dev/null || \
  fail "Catalog capture did not contain exactly CAR, FAI, HSI, and TRI."

readonly inventory_file="legacy/product-service/src/test/resources/product-read-baseline/inventory/${environment}.json"
[[ -f "${inventory_file}" ]] || fail "Missing accepted ${environment} inventory fixture."
readonly inventory_checksum="$(sha256sum "${inventory_file}" | awk '{print $1}')"
readonly captured_at="$(jq -r '.observedAt' "${temp_dir}/database.json")"
readonly run_stamp="$(printf '%s' "${captured_at}" | tr -d ':-' | sed 's/Z$//; s/T/-/')"
readonly run_dir="ai/artifacts/epic-4.2/issue-131/catalog-runs"
readonly output_file="${run_dir}/${environment}-${run_stamp}.json"
mkdir -p "${run_dir}"
[[ ! -e "${output_file}" ]] || fail "Catalog run already exists at ${output_file}; no capture was replaced."

jq -n \
  --arg scenario_id "${scenario_id}" \
  --arg environment "${environment}" \
  --arg context "${context}" \
  --arg source_revision "$(git rev-parse HEAD)" \
  --arg capture_command "make product-baseline-capture BASELINE_ENV=${environment} BASELINE_PART=catalog" \
  --arg endpoint "${database_host}:${database_port}" \
  --arg inventory_file "${inventory_file}" \
  --arg inventory_checksum "${inventory_checksum}" \
  --slurpfile database "${temp_dir}/database.json" '
    ($database[0]) as $db
    | {
        schemaVersion: 1,
        scenarioId: $scenario_id,
        status: "captured",
        captureProfile: "stored-catalog",
        provenance: "captured-representative",
        credentialCategory: "database-capture",
        capturedAt: $db.observedAt,
        captureClock: "Target PostgreSQL statement clock inside a repeatable-read, read-only transaction",
        captureCommand: $capture_command,
        environment: $environment,
        sourceRevision: $source_revision,
        kubernetesContext: $context,
        connection: {endpoint: $endpoint, transactionReadOnly: $db.transactionReadOnly},
        inventoryIdentity: {path: $inventory_file, algorithm: "SHA-256", value: $inventory_checksum},
        database: {
          name: $db.database,
          schema: $db.schema,
          table: $db.table,
          tableOwner: $db.tableOwner
        },
        schemaIdentity: $db.schemaIdentity,
        dataIdentity: $db.dataIdentity,
        transactionSnapshot: $db.transactionSnapshot,
        rows: $db.rows,
        coverage: $db.coverage,
        sanitizationRecord: {
          sourceContainsOnlyAgreedDemoCatalog: true,
          credentialsCaptured: false,
          bearerTokensCaptured: false,
          transformation: "PostgreSQL definition::text retained as a JSON string; no typed or floating-point conversion"
        }
      }
  ' >"${temp_dir}/catalog.json"

jq empty "${temp_dir}/catalog.json"
mv "${temp_dir}/catalog.json" "${output_file}"
chmod 0644 "${output_file}"

echo "Captured read-only ${environment} Product catalog run at ${output_file}."
echo "Review it, then accept it with: scripts/product-baseline/accept-catalog.sh ${output_file}"
