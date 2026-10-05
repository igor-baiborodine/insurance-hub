#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
readonly script_dir
repo_root="$(cd -- "${script_dir}/../.." && pwd -P)"
readonly repo_root
readonly inventory_path="${repo_root}/go-module-topology.json"

fail() {
	printf 'go-boundary-check: %s\n' "$*" >&2
	exit 1
}

command -v jq >/dev/null 2>&1 || fail "jq >= 1.6 is required"
command -v go >/dev/null 2>&1 || fail "Go is required to inspect package imports"
test -f "${inventory_path}" || fail "missing inventory: go-module-topology.json"

repository_module_prefix="$(jq -r '.repositoryModulePrefix' "${inventory_path}")"
mapfile -t module_records < <(
	jq -c '.modules | sort_by(.modulePath | length) | reverse[]' "${inventory_path}"
)

graph_dir="$(mktemp -d)"
case "${graph_dir}" in
	/tmp/tmp.*) ;;
	*) fail "refusing unexpected temporary path: ${graph_dir}" ;;
esac
readonly graph_dir

cleanup() {
	find "${graph_dir}" -depth -delete
}
trap cleanup EXIT HUP INT TERM

declare -a failures=()
module_index=0

for module_record in "${module_records[@]}"; do
	module_index=$((module_index + 1))
	directory="$(jq -r '.directory' <<<"${module_record}")"
	module_path="$(jq -r '.modulePath' <<<"${module_record}")"
	graph_error_path="${graph_dir}/module-${module_index}.stderr"

	set +e
	graph_data="$({
		cd "${repo_root}/${directory}"
		env -u GOFLAGS GOWORK=off go list -e -json -mod=readonly ./...
	} 2>"${graph_error_path}")"
	graph_status=$?
	set -e

	if ((graph_status != 0)) || [[ -z "${graph_data}" ]]; then
		failures+=("module=${module_path} consumer-package=unknown import=unknown owner=unknown rule=package-graph-load-failed remediation=resolve standalone readonly package loading")
		continue
	fi

	graph_path="${graph_dir}/module-${module_index}.json"
	if ! jq -s 'sort_by(.ImportPath)' <<<"${graph_data}" >"${graph_path}"; then
		failures+=("module=${module_path} consumer-package=unknown import=unknown owner=unknown rule=invalid-package-graph remediation=resolve standalone package-list output")
		continue
	fi

	package_count="$(jq 'length' "${graph_path}")"
	if ((package_count == 0)); then
		failures+=("module=${module_path} consumer-package=unknown import=unknown owner=unknown rule=empty-package-graph remediation=ensure ./... selects the owned packages")
		continue
	fi

	packages="$(jq -r 'map(.ImportPath) | sort | join(",")' "${graph_path}")"
	import_count="$(
		jq -r '.[] | ((.Imports // []) + (.TestImports // []) + (.XTestImports // []))[]?' \
			"${graph_path}" | sort -u | wc -l
	)"
	printf 'package-graph: module=%s mode=standalone packages=%d direct-imports=%d\n' \
		"${module_path}" "${package_count}" "${import_count}"
	printf '  packages: %s\n' "${packages}"

	while IFS=$'\t' read -r consumer_package import_path; do
		if [[ "${import_path}" != "${repository_module_prefix}"* ]]; then
			continue
		fi

		owner_record=""
		for candidate_record in "${module_records[@]}"; do
			candidate_path="$(jq -r '.modulePath' <<<"${candidate_record}")"
			if [[ "${import_path}" == "${candidate_path}" || "${import_path}" == "${candidate_path}/"* ]]; then
				owner_record="${candidate_record}"
				break
			fi
		done

		if [[ -z "${owner_record}" ]]; then
			failures+=("module=${module_path} consumer-package=${consumer_package} import=${import_path} owner=unknown rule=unknown-repository-owner remediation=inventory the owning module or remove the repository-local import")
			continue
		fi

		owner_path="$(jq -r '.modulePath' <<<"${owner_record}")"
		if [[ "${owner_path}" == "${module_path}" ]]; then
			continue
		fi

		owner_role="$(jq -r '.role' <<<"${owner_record}")"
		owner_relative_import="${import_path#"${owner_path}"}"
		if [[ "${owner_relative_import}" == "/internal" || "${owner_relative_import}" == "/internal/"* ]]; then
			failures+=("module=${module_path} consumer-package=${consumer_package} import=${import_path} owner=${owner_path} rule=sibling-internal-import remediation=depend on an approved public contract/shared module")
			continue
		fi

		if [[ "${owner_role}" == "service-scaffold" ]]; then
			failures+=("module=${module_path} consumer-package=${consumer_package} import=${import_path} owner=${owner_path} rule=scaffold-runtime-dependency remediation=copy the scaffold structure and own runtime code independently")
			continue
		fi

		if [[ "${owner_role}" != "public-contract" && "${owner_role}" != "public-shared" ]]; then
			failures+=("module=${module_path} consumer-package=${consumer_package} import=${import_path} owner=${owner_path} rule=non-public-cross-module-import remediation=use an approved public contract/shared module")
			continue
		fi

		if ! jq -e --arg consumer "${module_path}" '.consumers | index($consumer) != null' \
			<<<"${owner_record}" >/dev/null; then
			failures+=("module=${module_path} consumer-package=${consumer_package} import=${import_path} owner=${owner_path} rule=unapproved-consumer remediation=record the consumer after contract ownership approval")
		fi
	done < <(
		jq -r '
		  .[]
		  | .ImportPath as $consumer
		  | ((.Imports // []) + (.TestImports // []) + (.XTestImports // []) | unique[])
		  | [$consumer, .]
		  | @tsv
		' "${graph_path}" | sort -u
	)

	while IFS= read -r incomplete_package; do
		failures+=("module=${module_path} consumer-package=${incomplete_package} import=unknown owner=unknown rule=incomplete-package-graph remediation=resolve all standalone readonly imports before boundary validation")
	done < <(
		jq -r '
		  .[]
		  | select((.Incomplete // false) or (.Error != null) or ((.DepsErrors // []) | length > 0))
		  | .ImportPath
		' "${graph_path}" | sort -u
	)
done

if ((${#failures[@]} > 0)); then
	printf '%s\n' "${failures[@]}" | sort -u | while IFS= read -r message; do
		printf 'go-boundary-check: %s\n' "${message}" >&2
	done
	exit 1
fi

printf 'go-boundary-check: passed %d module graph(s) in standalone readonly mode\n' \
	"${#module_records[@]}"
