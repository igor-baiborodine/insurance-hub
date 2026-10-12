#!/usr/bin/env bash
set -euo pipefail

readonly repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repo_root"

fail() {
  printf 'product-security-verify: %s\n' "$*" >&2
  exit 1
}

readonly target_context="${PRODUCT_SECURITY_TARGET_CONTEXT:-}"
readonly target_namespace="${PRODUCT_SECURITY_TARGET_NAMESPACE:-}"
readonly expected_environment="${PRODUCT_SECURITY_EXPECTED_ENV:-}"
readonly http_endpoint="${PRODUCT_SECURITY_HTTP_ENDPOINT:-}"
readonly grpc_endpoint="${PRODUCT_SECURITY_GRPC_ENDPOINT:-}"
readonly health_endpoint="${PRODUCT_SECURITY_HEALTH_ENDPOINT:-}"

[[ -n "$target_context" ]] || fail 'PRODUCT_SECURITY_TARGET_CONTEXT is required'
[[ -n "$target_namespace" ]] || fail 'PRODUCT_SECURITY_TARGET_NAMESPACE is required'
case "$expected_environment" in
  local-dev | qa) ;;
  *) fail 'PRODUCT_SECURITY_EXPECTED_ENV must be local-dev or qa' ;;
esac
[[ -n "$http_endpoint" && -n "$grpc_endpoint" && -n "$health_endpoint" ]] || \
  fail 'all three Product endpoint inputs are required'
[[ ! ${PRODUCT_DATABASE_URL+x} ]] || fail 'PRODUCT_DATABASE_URL must be unset'
for tool in go make timeout; do
  command -v "$tool" >/dev/null 2>&1 || fail "missing prerequisite: $tool"
done

if [[ "$target_context" == isolated ]]; then
  [[ "$target_namespace" == isolated ]] || fail 'isolated context requires isolated namespace'
  [[ "$http_endpoint" == http://127.0.0.1:* && "$grpc_endpoint" == 127.0.0.1:* && \
    "$health_endpoint" == http://127.0.0.1:* ]] || \
    fail 'isolated context requires loopback Product endpoints'
else
  case "$expected_environment:$target_context:$target_namespace" in
    local-dev:kind-local-dev-insurance-hub:local-dev-all | qa:qa-insurance-hub:qa-svc) ;;
    *) fail 'target context and namespace do not match the selected environment' ;;
  esac
  command -v kubectl >/dev/null 2>&1 || fail 'missing prerequisite: kubectl'
  current_context=$(kubectl config current-context 2>/dev/null) || \
    fail 'cannot read the active Kubernetes context'
  [[ "$current_context" == "$target_context" ]] || fail 'active Kubernetes context differs from the requested target'
  current_namespace=$(kubectl config view --minify -o 'jsonpath={..namespace}' 2>/dev/null) || \
    fail 'cannot read the active Kubernetes namespace'
  [[ "${current_namespace:-default}" == "$target_namespace" ]] || \
    fail 'active Kubernetes namespace differs from the requested target'
fi

work_dir=$(mktemp -d "${TMPDIR:-/tmp}/product-security-verify.XXXXXX")
chmod 700 "$work_dir"
trap 'rm -rf -- "$work_dir"' EXIT

timeout 120s make -C services/product-service build-security-verifier \
  SECURITY_VERIFIER_OUTPUT="$work_dir/security-verifier" || fail 'verifier build failed or timed out'
"$work_dir/security-verifier" \
  -context "$target_context" -namespace "$target_namespace" \
  -http "$http_endpoint" -grpc "$grpc_endpoint" -health "$health_endpoint" \
  -accepted "$repo_root/legacy/product-service/src/test/resources/product-read-baseline/access/$expected_environment.json"
