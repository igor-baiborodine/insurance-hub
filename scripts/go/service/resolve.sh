#!/usr/bin/env bash

set -euo pipefail

fail() {
	printf 'go-service-dispatch: %s\n' "$*" >&2
	exit 1
}

if (($# != 1)); then
	fail "exactly one capability is required"
fi
readonly capability="$1"
[[ "${capability}" =~ ^[a-z][a-z0-9-]*$ ]] || fail "capability has an invalid format"

service_name="${SVC_NAME:-}"
[[ -n "${service_name}" ]] || fail "SVC_NAME is required"
[[ "${service_name}" =~ ^[a-z][a-z0-9-]*$ ]] || fail "SVC_NAME must match [a-z][a-z0-9-]*"
command -v jq >/dev/null 2>&1 || fail "jq >= 1.6 is required"
command -v realpath >/dev/null 2>&1 || fail "realpath is required"

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
readonly script_dir
repo_root="$(cd -- "${script_dir}/../../.." && pwd -P)"
readonly repo_root
catalog_path="${script_dir}/catalog.json"
readonly catalog_path

jq -e '
  .schemaVersion == 1 and
  (.services | type == "object" and length > 0) and
  ([.services | to_entries[] |
    (.key | test("^[a-z][a-z0-9-]*$")) and
    (.value.repositoryRoot | type == "string" and length > 0) and
    (.value.modulePath | type == "string" and length > 0) and
    (.value.localImage | type == "string" and length > 0) and
    (.value.registryImage | type == "string" and length > 0) and
    (.value.kustomizeBase | type == "string" and length > 0) and
    (.value.overlays["local-dev"] | type == "string" and length > 0) and
    (.value.overlays.qa | type == "string" and length > 0) and
    (.value.environments | type == "array" and index("local-dev") != null and index("qa") != null) and
    (.value.capabilities | type == "array" and length > 0 and all(.[]; test("^[a-z][a-z0-9-]*$")))
  ] | all)
' "${catalog_path}" >/dev/null || fail "catalog schema or service metadata is invalid"

record="$(jq -cer --arg service "${service_name}" '.services[$service] // error("unknown service")' \
	"${catalog_path}" 2>/dev/null)" || fail "unknown SVC_NAME"
jq -e --arg capability "${capability}" '.capabilities | index($capability) != null' \
	<<<"${record}" >/dev/null || fail "service does not support capability ${capability}"

repository_relative="$(jq -r '.repositoryRoot' <<<"${record}")"
module_relative="$(jq -r '.modulePath' <<<"${record}")"
[[ "${repository_relative}" != /* && "${module_relative}" != /* ]] || fail "catalog paths must be relative"

repository_path="$(realpath -e -- "${repo_root}/${repository_relative}")" || fail "declared repository root does not exist"
module_path="$(realpath -e -- "${repo_root}/${module_relative}")" || fail "declared module path does not exist"
case "${repository_path}" in
	"${repo_root}"/*) ;;
	*) fail "declared repository root escapes the checkout" ;;
esac
case "${module_path}" in
	"${repository_path}"/*) ;;
	*) fail "module path escapes its declared repository root" ;;
esac
[[ -f "${module_path}/Makefile" ]] || fail "selected module does not own a Makefile"

printf '%s\n' "${module_path#"${repo_root}/"}"
