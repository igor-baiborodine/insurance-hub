#!/usr/bin/env bash
set -euo pipefail

fail() {
  echo "ERROR: $*" >&2
  exit 2
}

readonly script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly repo_root="$(cd "${script_dir}/../.." && pwd)"
readonly environment="${BASELINE_ENV:-}"
case "${environment}" in
  local-dev | qa) ;;
  *) fail "BASELINE_ENV is required; use local-dev or qa." ;;
esac

readonly fixture="${repo_root}/legacy/product-service/src/test/resources/product-read-baseline/catalog/${environment}.json"
[[ -f "${fixture}" ]] || fail "Missing accepted ${environment} catalog fixture."
BASELINE_CATALOG_FILE="${fixture}" "${script_dir}/check-catalog.sh"

readonly temp_dir="$(mktemp -d)"
readonly container_name="product-baseline-roundtrip-${environment}-$$"
cleanup() {
  docker rm -f "${container_name}" >/dev/null 2>&1 || true
  rm -rf "${temp_dir}"
}
trap cleanup EXIT

jq -r '.rows[] | [.code, .rawLosslessDefinitionJson] | @tsv' "${fixture}" >"${temp_dir}/catalog.tsv"
chmod 0755 "${temp_dir}"
chmod 0644 "${temp_dir}/catalog.tsv"
docker run --detach --name "${container_name}" \
  -e POSTGRES_PASSWORD=baseline_roundtrip \
  -e POSTGRES_DB=product_test \
  -v "${temp_dir}:/baseline:ro" \
  postgres:16.4-alpine >/dev/null

ready_count=0
for _ in $(seq 1 60); do
  ready_count="$(docker logs "${container_name}" 2>&1 | \
    grep -c 'database system is ready to accept connections' || true)"
  if [[ "${ready_count}" -ge 2 ]] && \
    docker exec "${container_name}" psql -X -q -A -t \
      -U postgres -d product_test -c 'SELECT 1' >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
[[ "${ready_count}" -ge 2 ]] || \
  fail "Disposable PostgreSQL did not become ready."

docker exec -i "${container_name}" psql -X -v ON_ERROR_STOP=1 -U postgres -d product_test <<'SQL' >/dev/null
CREATE TABLE public.product (
  code varchar(255) PRIMARY KEY,
  definition jsonb NOT NULL
);
\copy public.product (code, definition) FROM '/baseline/catalog.tsv'
SQL

docker exec "${container_name}" psql -X -q -A -t -v ON_ERROR_STOP=1 \
  --field-separator=$'\t' \
  -U postgres -d product_test \
  -c "SELECT code, md5(definition::text) FROM public.product ORDER BY code" \
  >"${temp_dir}/observed.tsv"
jq -r '.rows[] | [.code, .checksum.value] | @tsv' "${fixture}" >"${temp_dir}/expected.tsv"
cmp "${temp_dir}/expected.tsv" "${temp_dir}/observed.tsv" || \
  fail "Disposable PostgreSQL changed row identity or raw JSON numeric representation."

echo "Validated ${environment} catalog restore/read round trip in disposable PostgreSQL."
