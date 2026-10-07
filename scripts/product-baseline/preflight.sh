#!/usr/bin/env bash
set -euo pipefail

readonly script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

fail() {
  echo "ERROR: $*" >&2
  exit 2
}

require_command() {
  local command_name="$1"
  command -v "${command_name}" >/dev/null 2>&1 || \
    fail "Required command '${command_name}' is not available."
}

first_line() {
  local value="$1"
  printf '%s\n' "${value%%$'\n'*}"
}

readonly environment="${BASELINE_ENV:-}"
[[ -n "${environment}" ]] || fail "BASELINE_ENV is required; use local-dev or qa."

case "${environment}" in
  local-dev)
    readonly expected_context="kind-local-dev-insurance-hub"
    readonly service_namespace="local-dev-all"
    readonly data_namespace="local-dev-all"
    readonly product_deployment="local-dev-product-api-legacy"
    readonly product_service="local-dev-product-api-legacy"
    readonly gateway_deployment="local-dev-agent-portal-gateway-legacy"
    readonly gateway_service="local-dev-agent-portal-gateway-legacy"
    readonly database_secret="local-dev-postgres-product-user-creds"
    readonly default_database_port="5492"
    ;;
  qa)
    readonly expected_context="qa-insurance-hub"
    readonly service_namespace="qa-svc"
    readonly data_namespace="qa-data"
    readonly product_deployment="qa-product-api-legacy"
    readonly product_service="qa-product-api-legacy"
    readonly gateway_deployment="qa-agent-portal-gateway-legacy"
    readonly gateway_service="qa-agent-portal-gateway-legacy"
    readonly database_secret="qa-postgres-product-user-creds"
    readonly default_database_port="5492"
    ;;
  *) fail "Unsupported BASELINE_ENV '${environment}'; use local-dev or qa." ;;
esac

readonly requested_context="${BASELINE_KUBE_CONTEXT:-${expected_context}}"
[[ "${requested_context}" == "${expected_context}" ]] || \
  fail "BASELINE_KUBE_CONTEXT '${requested_context}' does not match ${environment} context '${expected_context}'."

readonly database_host="${BASELINE_PRODUCT_DB_HOST:-127.0.0.1}"
readonly database_port="${BASELINE_PRODUCT_DB_PORT:-${default_database_port}}"
[[ "${database_port}" =~ ^[0-9]+$ ]] || fail "BASELINE_PRODUCT_DB_PORT must be numeric."
if [[ "${environment}" == "local-dev" && "${database_port}" == "5482" ]]; then
  fail "Local port 5482 belongs to Pricing; Product PostgreSQL uses 5492 by default."
fi

require_command base64
require_command docker
require_command java
require_command kubectl
require_command mvn
require_command pg_isready
require_command psql

docker_versions="$(docker version --format '{{.Client.Version}} client, {{.Server.Version}} server' 2>&1)" || \
  fail "Docker Engine is unavailable: ${docker_versions}"
java_version="$(java -version 2>&1)" || fail "Java is unavailable."
maven_version="$(mvn -version 2>&1)" || fail "Maven is unavailable."
psql_version="$(psql --version 2>&1)" || fail "psql is unavailable."
kubectl version --client --output=json >/dev/null 2>&1 || \
  fail "kubectl client information is unavailable."

current_context="$(kubectl config current-context 2>/dev/null)" || \
  fail "No current Kubernetes context is configured."
[[ "${current_context}" == "${expected_context}" ]] || \
  fail "Current Kubernetes context '${current_context}' does not match '${expected_context}'."

kubectl --context="${expected_context}" auth can-i get deployments -n "${service_namespace}" | \
  grep -Fxq yes || fail "Cannot read deployments in namespace '${service_namespace}'."
kubectl --context="${expected_context}" auth can-i get services -n "${service_namespace}" | \
  grep -Fxq yes || fail "Cannot read services in namespace '${service_namespace}'."
kubectl --context="${expected_context}" auth can-i get endpointslices.discovery.k8s.io \
  -n "${service_namespace}" | grep -Fxq yes || \
  fail "Cannot read endpoint slices in namespace '${service_namespace}'."
kubectl --context="${expected_context}" auth can-i get \
  "secret/${database_secret}" -n "${data_namespace}" | grep -Fxq yes || \
  fail "Cannot read Product database credentials in namespace '${data_namespace}'."

for workload in "${product_deployment}" "${gateway_deployment}"; do
  ready_replicas="$(kubectl --context="${expected_context}" -n "${service_namespace}" \
    get deployment "${workload}" -o jsonpath='{.status.readyReplicas}')" || \
    fail "Deployment '${workload}' is unavailable."
  [[ "${ready_replicas:-0}" =~ ^[1-9][0-9]*$ ]] || \
    fail "Deployment '${workload}' has no ready replica."
done

for service in "${product_service}" "${gateway_service}"; do
  kubectl --context="${expected_context}" -n "${service_namespace}" \
    get service "${service}" >/dev/null || fail "Service '${service}' is unavailable."
  endpoint_ip="$(kubectl --context="${expected_context}" -n "${service_namespace}" \
    get endpointslices.discovery.k8s.io \
    --selector="kubernetes.io/service-name=${service}" \
    -o jsonpath='{.items[0].endpoints[0].addresses[0]}')" || \
    fail "Cannot inspect endpoint slices for service '${service}'."
  [[ -n "${endpoint_ip}" ]] || fail "Service '${service}' has no ready endpoint."
done

if ! pg_isready --host="${database_host}" --port="${database_port}" \
  --timeout=5 --quiet; then
  fail "Product PostgreSQL endpoint ${database_host}:${database_port} is unreachable. Start the Product port-forward or provide an explicit Product endpoint."
fi

database_username="$(kubectl --context="${expected_context}" -n "${data_namespace}" \
  get secret "${database_secret}" -o jsonpath='{.data.username}' | base64 --decode)" || \
  fail "Cannot read the Product database username."
database_password="$(kubectl --context="${expected_context}" -n "${data_namespace}" \
  get secret "${database_secret}" -o jsonpath='{.data.password}' | base64 --decode)" || \
  fail "Cannot read the Product database password."
[[ -n "${database_username}" && -n "${database_password}" ]] || \
  fail "Product database credentials are incomplete."

readonly identity_sql="SET default_transaction_read_only=on; SELECT current_database() || '|' || current_schema() || '|' || COALESCE(to_regclass('public.product')::text, ''); SELECT count(*)::text FROM public.product;"
if [[ -n "${BASELINE_PREFLIGHT_INVENTORY_OUTPUT:-}" && -n "${BASELINE_PREFLIGHT_CATALOG_OUTPUT:-}" ]]; then
  unset database_password
  fail "Only one preflight database output may be requested."
elif [[ -n "${BASELINE_PREFLIGHT_CATALOG_OUTPUT:-}" ]]; then
  require_command jq
  if ! PGPASSWORD="${database_password}" PGCONNECT_TIMEOUT=5 \
    psql -X -q -A -t -v ON_ERROR_STOP=1 \
      --host="${database_host}" --port="${database_port}" \
      --username="${database_username}" --dbname=product \
      --file="${script_dir}/catalog.sql" \
      >"${BASELINE_PREFLIGHT_CATALOG_OUTPUT}"; then
    unset database_password
    fail "Read-only Product catalog and identity check failed."
  fi
  unset database_password
  jq -e '
    .database == "product"
    and .schema == "public"
    and .table == "public.product"
    and .transactionReadOnly == true
    and (.dataIdentity.rowCount | type == "number")
  ' "${BASELINE_PREFLIGHT_CATALOG_OUTPUT}" >/dev/null || \
    fail "Endpoint ${database_host}:${database_port} is not the expected product/public.product database."
  database_row_count="$(jq -r '.dataIdentity.rowCount' \
    "${BASELINE_PREFLIGHT_CATALOG_OUTPUT}")"
elif [[ -n "${BASELINE_PREFLIGHT_INVENTORY_OUTPUT:-}" ]]; then
  require_command jq
  if ! PGPASSWORD="${database_password}" PGCONNECT_TIMEOUT=5 \
    psql -X -q -A -t -v ON_ERROR_STOP=1 \
      --host="${database_host}" --port="${database_port}" \
      --username="${database_username}" --dbname=product \
      --file="${script_dir}/inventory.sql" \
      >"${BASELINE_PREFLIGHT_INVENTORY_OUTPUT}"; then
    unset database_password
    fail "Read-only Product database inventory and identity check failed."
  fi
  unset database_password
  jq -e '
    .database == "product"
    and .schema == "public"
    and .table.qualifiedName == "public.product"
    and (.catalogProvenance.rowCount | type == "number")
  ' "${BASELINE_PREFLIGHT_INVENTORY_OUTPUT}" >/dev/null || \
    fail "Endpoint ${database_host}:${database_port} is not the expected product/public.product database."
  database_row_count="$(jq -r '.catalogProvenance.rowCount' \
    "${BASELINE_PREFLIGHT_INVENTORY_OUTPUT}")"
else
  if ! database_output="$(PGPASSWORD="${database_password}" PGCONNECT_TIMEOUT=5 \
    psql -X -q -A -t -v ON_ERROR_STOP=1 \
      --host="${database_host}" --port="${database_port}" \
      --username="${database_username}" --dbname=product \
      --command="${identity_sql}" 2>&1)"; then
    unset database_password
    fail "Read-only Product database identity check failed: ${database_output}"
  fi
  unset database_password

  mapfile -t database_rows <<<"${database_output}"
  [[ "${database_rows[0]:-}" == "product|public|product" ]] || \
    fail "Endpoint ${database_host}:${database_port} is not the expected product/public.product database."
  [[ "${database_rows[1]:-}" =~ ^[0-9]+$ ]] || \
    fail "Could not verify read access to public.product."
  database_row_count="${database_rows[1]}"
fi

echo "Product baseline preflight passed for ${environment}."
echo "  Kubernetes context: ${expected_context}"
echo "  Service namespace: ${service_namespace}"
echo "  Data namespace: ${data_namespace}"
echo "  Product database endpoint: ${database_host}:${database_port}"
echo "  Product catalog rows visible: ${database_row_count}"
echo "  Docker: ${docker_versions}"
echo "  Java: $(first_line "${java_version}")"
echo "  Maven: $(first_line "${maven_version}")"
echo "  PostgreSQL client: ${psql_version}"
echo "  kubectl client metadata: available"
