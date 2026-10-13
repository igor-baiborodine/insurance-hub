#!/usr/bin/env bash

set -euo pipefail

fail() {
	printf 'product-image-check: %s\n' "$*" >&2
	exit 1
}

for command_name in docker go jq tar; do
	command -v "${command_name}" >/dev/null 2>&1 || fail "${command_name} is required"
done

: "${IMAGE_REF:?IMAGE_REF is required}"
: "${SOURCE_CREATED:?SOURCE_CREATED is required}"
: "${SOURCE_REPOSITORY:?SOURCE_REPOSITORY is required}"
: "${SOURCE_REVISION:?SOURCE_REVISION is required}"
: "${SOURCE_VERSION:?SOURCE_VERSION is required}"

module_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
readonly module_root
temporary_root="$(mktemp -d "${TMPDIR:-/tmp}/insurance-hub-product-image.XXXXXX")"
readonly temporary_root
container_id=""

cleanup() {
	if [[ -n "${container_id}" ]]; then
		docker rm -f "${container_id}" >/dev/null 2>&1 || true
	fi
	case "${temporary_root}" in
		"${TMPDIR:-/tmp}"/insurance-hub-product-image.*) find -P "${temporary_root}" -depth -delete ;;
		*) printf 'product-image-check: refusing unexpected temporary path: %s\n' "${temporary_root}" >&2 ;;
	esac
}
trap cleanup EXIT HUP INT TERM

inspect_path="${temporary_root}/inspect.json"
docker image inspect "${IMAGE_REF}" >"${inspect_path}"
jq -e \
	--arg created "${SOURCE_CREATED}" \
	--arg repository "${SOURCE_REPOSITORY}" \
	--arg revision "${SOURCE_REVISION}" \
	--arg version "${SOURCE_VERSION}" '
  length == 1 and
  .[0].Os == "linux" and
  .[0].Architecture == "amd64" and
  .[0].Config.User == "65532:65532" and
  .[0].Config.Entrypoint == ["/product-service"] and
  ((.[0].Config.Cmd // []) | length == 0) and
  .[0].Config.Labels["org.opencontainers.image.created"] == $created and
  .[0].Config.Labels["org.opencontainers.image.source"] == $repository and
  .[0].Config.Labels["org.opencontainers.image.revision"] == $revision and
  .[0].Config.Labels["org.opencontainers.image.title"] == "Insurance Hub Product API" and
  .[0].Config.Labels["org.opencontainers.image.version"] == $version
' "${inspect_path}" >/dev/null || fail "image configuration or OCI identity does not match the Product contract"

container_id="$(docker create "${IMAGE_REF}")"
docker export --output "${temporary_root}/rootfs.tar" "${container_id}"
tar -tf "${temporary_root}/rootfs.tar" | sed 's#^\./##' >"${temporary_root}/rootfs.txt"
grep -Fxq 'product-service' "${temporary_root}/rootfs.txt" || fail "runtime binary is missing"
grep -Fxq 'etc/ssl/certs/ca-certificates.crt' "${temporary_root}/rootfs.txt" || fail "CA certificate bundle is missing"
if grep -Eq '(^|/)(sh|bash|ash|dash|zsh)$' "${temporary_root}/rootfs.txt"; then
	fail "runtime image unexpectedly contains a shell"
fi
if grep -Eq '(^|/)(\.tools|scripts|testdata)(/|$)|(^|/).*_test(\.go)?$|\.sql$' \
	"${temporary_root}/rootfs.txt"; then
	fail "runtime image contains excluded development, test, or SQL content"
fi
docker rm "${container_id}" >/dev/null
container_id=""

set +e
docker run --rm "${IMAGE_REF}" >"${temporary_root}/missing-config.log" 2>&1
missing_config_status=$?
set -e
((missing_config_status != 0)) || fail "image started without required configuration"
grep -Fq '"msg":"configuration failed"' "${temporary_root}/missing-config.log" || \
	fail "missing configuration did not produce the bounded startup diagnostic"
grep -Fq '"error":"[REDACTED]"' "${temporary_root}/missing-config.log" || \
	fail "missing configuration diagnostic was not redacted"

(
	cd "${module_root}"
	PRODUCT_TEST_IMAGE="${IMAGE_REF}" GOWORK=off go test -mod=readonly -count=1 \
		-tags=image -run '^TestProductRuntimeImage$' -timeout=4m ./internal/testing
)

printf 'product-image-check: image=%s contents=passed startup=passed runtime=passed signal=passed\n' \
	"${IMAGE_REF}"
