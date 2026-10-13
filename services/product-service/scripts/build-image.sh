#!/usr/bin/env bash

set -euo pipefail

fail() {
	printf 'product-image-build: %s\n' "$*" >&2
	exit 1
}

for command_name in docker git; do
	command -v "${command_name}" >/dev/null 2>&1 || fail "${command_name} is required"
done

module_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
readonly module_root
repository_root="$(git -C "${module_root}" rev-parse --show-toplevel 2>/dev/null)" || \
	fail "module is not inside a Git checkout"
readonly repository_root

: "${IMAGE_NAME:?IMAGE_NAME is required}"
: "${IMAGE_TAG:?IMAGE_TAG is required}"
: "${IMAGE_REF:?IMAGE_REF is required}"
: "${SOURCE_CREATED:?SOURCE_CREATED is required}"
: "${SOURCE_REPOSITORY:?SOURCE_REPOSITORY is required}"
: "${SOURCE_REVISION:?SOURCE_REVISION is required}"
: "${SOURCE_VERSION:?SOURCE_VERSION is required}"
: "${TARGET_PLATFORM:?TARGET_PLATFORM is required}"

[[ "${SOURCE_REVISION}" =~ ^[0-9a-f]{40}$ ]] || fail "SOURCE_REVISION must be 40 lowercase hexadecimal characters"
head_revision="$(git -C "${repository_root}" rev-parse --verify HEAD)"
[[ "${SOURCE_REVISION}" == "${head_revision}" ]] || fail "SOURCE_REVISION must match the checked-out HEAD"
expected_created="$(git -C "${repository_root}" show -s --format=%cI "${head_revision}")"
[[ "${SOURCE_CREATED}" == "${expected_created}" ]] || fail "SOURCE_CREATED must match the checked-out commit time"
expected_version="git-${SOURCE_REVISION:0:12}"
[[ "${SOURCE_VERSION}" == "${expected_version}" ]] || fail "SOURCE_VERSION must be ${expected_version}"
[[ "${IMAGE_TAG}" == "${expected_version}" ]] || fail "IMAGE_TAG must be ${expected_version}"
[[ "${IMAGE_REF}" == "${IMAGE_NAME}:${IMAGE_TAG}" ]] || fail "IMAGE_REF must be derived from IMAGE_NAME and IMAGE_TAG"
[[ "${IMAGE_NAME}" == "insurance-hub-product-api" ]] || fail "IMAGE_NAME must be insurance-hub-product-api"
[[ "${TARGET_PLATFORM}" == "linux/amd64" ]] || fail "TARGET_PLATFORM must be linux/amd64"

docker buildx build \
	--build-arg "SOURCE_CREATED=${SOURCE_CREATED}" \
	--build-arg "SOURCE_REPOSITORY=${SOURCE_REPOSITORY}" \
	--build-arg "SOURCE_REVISION=${SOURCE_REVISION}" \
	--build-arg "SOURCE_VERSION=${SOURCE_VERSION}" \
	--load \
	--platform "${TARGET_PLATFORM}" \
	--provenance=false \
	--tag "${IMAGE_REF}" \
	--file "${module_root}/Dockerfile" \
	"${module_root}"

image_id="$(docker image inspect --format '{{.Id}}' "${IMAGE_REF}")"
[[ "${image_id}" == sha256:* ]] || fail "built image does not have a content-addressed image ID"
printf 'product-image-build: image=%s image-id=%s platform=%s revision=%s\n' \
	"${IMAGE_REF}" "${image_id}" "${TARGET_PLATFORM}" "${SOURCE_REVISION}"
