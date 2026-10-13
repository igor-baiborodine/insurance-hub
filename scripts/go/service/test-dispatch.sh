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

for capability in image-check deploy-render deploy-check; do
	resolved="$(SVC_NAME=product "${script_dir}/resolve.sh" "${capability}")"
	[[ "${resolved}" == "services/product-service" ]] || {
		printf 'go-service-dispatch-test: Product %s module = %s\n' "${capability}" "${resolved}" >&2
		exit 1
	}
done

printf 'go-service-dispatch-test: passed missing, invalid, unknown, unsupported, and Product capability cases\n'
