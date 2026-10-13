#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
readonly script_dir
temporary_root="$(mktemp -d "${TMPDIR:-/tmp}/go-service-dispatch-test.XXXXXX")"
readonly temporary_root

cleanup() {
	case "${temporary_root}" in
		"${TMPDIR:-/tmp}"/go-service-dispatch-test.*) find -P "${temporary_root}" -depth -delete ;;
		*) printf 'go-service-dispatch-test: refusing unexpected temporary path: %s\n' "${temporary_root}" >&2 ;;
	esac
}
trap cleanup EXIT HUP INT TERM

expect_failure() {
	local description="$1"
	shift
	if "$@" >"${temporary_root}/stdout" 2>"${temporary_root}/stderr"; then
		printf 'go-service-dispatch-test: expected failure: %s\n' "${description}" >&2
		exit 1
	fi
}

expect_failure "missing service" env -u SVC_NAME "${script_dir}/resolve.sh" image-build
expect_failure "invalid service" env SVC_NAME='../product' "${script_dir}/resolve.sh" image-build
expect_failure "unknown service" env SVC_NAME='unknown' "${script_dir}/resolve.sh" image-build
expect_failure "unsupported capability" env SVC_NAME='product' "${script_dir}/resolve.sh" image-publish

resolved="$(SVC_NAME=product "${script_dir}/resolve.sh" image-build)"
[[ "${resolved}" == "services/product-service" ]] || {
	printf 'go-service-dispatch-test: Product module = %s\n' "${resolved}" >&2
	exit 1
}

for capability in image-check deploy-render deploy-check deploy-preflight deploy-status image-load deploy deploy-verify stop deploy-test-tooling deploy-test; do
	resolved="$(SVC_NAME=product "${script_dir}/resolve.sh" "${capability}")"
	[[ "${resolved}" == "services/product-service" ]] || {
		printf 'go-service-dispatch-test: Product %s module = %s\n' "${capability}" "${resolved}" >&2
		exit 1
	}
done

# Two disposable catalog entries prove that selected service metadata, not a
# hard-coded Product path, controls the same shared capability.
mkdir -p "${temporary_root}/repository/scripts/go/service" \
  "${temporary_root}/repository/services/first" "${temporary_root}/repository/services/second"
cp "${script_dir}/resolve.sh" "${temporary_root}/repository/scripts/go/service/resolve.sh"
cp "${script_dir}/Makefile" "${temporary_root}/repository/scripts/go/service/Makefile"
for service in first second; do
  cat >"${temporary_root}/repository/services/${service}/Makefile" <<EOF
.PHONY: deploy-preflight
deploy-preflight:
	@printf 'fixture:${service}\\n'
EOF
done
jq '{schemaVersion: 1, services: {
  first: {repositoryRoot:"services",modulePath:"services/first",localImage:"first",
    registryImage:"example.org/first",kustomizeBase:"k8s/first",
    overlays:{"local-dev":"k8s/local/first",qa:"k8s/qa/first"},
    environments:["local-dev","qa"],capabilities:["deploy-preflight"]},
  second: {repositoryRoot:"services",modulePath:"services/second",localImage:"second",
    registryImage:"example.org/second",kustomizeBase:"k8s/second",
    overlays:{"local-dev":"k8s/local/second",qa:"k8s/qa/second"},
    environments:["local-dev","qa"],capabilities:["deploy-preflight"]}}}' \
  <<< '{}' >"${temporary_root}/repository/scripts/go/service/catalog.json"
for service in first second; do
  resolved="$(SVC_NAME="${service}" "${temporary_root}/repository/scripts/go/service/resolve.sh" deploy-preflight)"
  [[ "${resolved}" == "services/${service}" ]] || {
    printf 'go-service-dispatch-test: fixture %s resolved to %s\n' "${service}" "${resolved}" >&2
    exit 1
  }
  invoked="$(make --no-print-directory -s -C "${temporary_root}/repository" \
    -f scripts/go/service/Makefile go-svc-deploy-preflight SVC_NAME="${service}")"
  [[ "${invoked}" == "fixture:${service}" ]] || {
    printf 'go-service-dispatch-test: fixture recipe %s returned %s\n' "${service}" "${invoked}" >&2
    exit 1
  }
done

printf 'go-service-dispatch-test: passed guards, Product capabilities, and two fixture service paths\n'
