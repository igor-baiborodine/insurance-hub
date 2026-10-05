#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
readonly script_dir
repo_root="$(cd -- "${script_dir}/../.." && pwd -P)"
readonly repo_root
readonly inventory_path="${repo_root}/go-module-topology.json"
readonly make_command="${MAKE_COMMAND:-make}"

fail() {
	printf 'go-modules-check: %s\n' "$*" >&2
	exit 1
}

command -v jq >/dev/null 2>&1 || fail "jq >= 1.6 is required"
command -v go >/dev/null 2>&1 || fail "Go is required to inspect effective module metadata"
test -f "${inventory_path}" || fail "missing inventory: go-module-topology.json"

mapfile -t declared_variables < <(jq -r '[.modules[].validationVariables[]] | unique[]' "${inventory_path}")
read -r -a caller_variables <<<"${GO_MODULE_CALLER_VARIABLES:-}"

declare -A declared_variable_set=()
declare -A selected_values=([FORMAT_SCOPE]="all")
for variable in "${declared_variables[@]}"; do
	declared_variable_set["${variable}"]=1
done

for variable in "${caller_variables[@]}"; do
	if [[ "${variable}" == "GOWORK" ]]; then
		continue
	fi
	if [[ -z "${declared_variable_set[${variable}]+present}" ]]; then
		fail "unsupported caller variable: ${variable}"
	fi
	selected_values["${variable}"]="${!variable-}"
done

snapshot_dir="$(mktemp -d)"
case "${snapshot_dir}" in
	/tmp/tmp.*) ;;
	*) fail "refusing unexpected temporary path: ${snapshot_dir}" ;;
esac
readonly snapshot_dir

cleanup() {
	find "${snapshot_dir}" -depth -delete
}
trap cleanup EXIT HUP INT TERM

snapshot_manifests() {
	local output_path="$1"
	local absolute_path relative_path digest
	: >"${output_path}"
	while IFS= read -r -d '' absolute_path; do
		relative_path="${absolute_path#"${repo_root}/"}"
		digest="$(sha256sum "${absolute_path}" | awk '{print $1}')"
		printf '%s\t%s\n' "${relative_path}" "${digest}" >>"${output_path}"
	done < <(
		find -P "${repo_root}" -type f \
			\( -name go.mod -o -name go.sum -o -name go.work -o -name go.work.sum \) \
			-print0 | sort -z
	)
}

snapshot_manifests "${snapshot_dir}/before"

declare -a failures=()
module_count="$(jq '.modules | length' "${inventory_path}")"
visited_count=0

while IFS= read -r module_record; do
	visited_count=$((visited_count + 1))
	directory="$(jq -r '.directory' <<<"${module_record}")"
	module_path="$(jq -r '.modulePath' <<<"${module_record}")"
	target="$(jq -r '.validationTarget' <<<"${module_record}")"
	package_scope="$(jq -r '.packageScope' <<<"${module_record}")"
	prerequisites="$(jq -r '.prerequisites | if length == 0 then "none" else join("; ") end' <<<"${module_record}")"
	consumers="$(jq -r '.consumers | join(",")' <<<"${module_record}")"

	if ! jq -e '.supportedModes | index("standalone") != null' <<<"${module_record}" >/dev/null; then
		failures+=("module ${module_path} does not declare required standalone mode")
		printf 'module-result: %s | failed: standalone mode is not declared\n' "${module_path}"
		continue
	fi

	printf 'module-check: %s\n' "${module_path}"
	printf '  mode: standalone (GOWORK=off)\n'
	printf '  directory: %s\n' "${directory}"
	printf '  target: %s\n' "${target}"
	printf '  package-scope: %s\n' "${package_scope}"
	printf '  consumers: %s\n' "${consumers}"
	printf '  prerequisites: %s\n' "${prerequisites}"

	mod_file="${repo_root}/${directory}/go.mod"
	if [[ "${directory}" == "." ]]; then
		mod_file="${repo_root}/go.mod"
	fi

	metadata_file="${snapshot_dir}/module-${visited_count}.json"
	set +e
	metadata="$(env -u GOFLAGS GOWORK=off go mod edit -json "${mod_file}")"
	metadata_status=$?
	set -e
	if ((metadata_status != 0)) || [[ -z "${metadata}" ]] || ! jq -e 'type == "object"' <<<"${metadata}" >/dev/null; then
		failures+=("could not inspect effective manifest metadata for ${module_path} at ${mod_file#"${repo_root}/"}")
		printf 'module-result: %s | failed: manifest metadata inspection\n' "${module_path}"
		continue
	fi
	printf '%s\n' "${metadata}" >"${metadata_file}"

	replacement_failure=0
	while IFS=$'\t' read -r required_module replacement_path; do
		if ! jq -e \
			--arg required_module "${required_module}" \
			--arg replacement_path "${replacement_path}" \
			'.localReplacements[]? | select(.modulePath == $required_module and .replacementPath == $replacement_path)' \
			<<<"${module_record}" >/dev/null; then
			failures+=("module ${module_path} requires ${required_module} from unapproved filesystem replacement ${replacement_path}; standalone portability is not proven")
			printf '  replacement-error: required=%s path=%s consequence=standalone portability is not proven\n' \
				"${required_module}" "${replacement_path}"
			replacement_failure=1
		fi
	done < <(
		jq -r '.Replace[]? | select((.New.Version // "") == "") | [.Old.Path, .New.Path] | @tsv' \
			"${metadata_file}"
	)

	while IFS=$'\t' read -r required_module replacement_path; do
		if ! jq -e \
			--arg required_module "${required_module}" \
			--arg replacement_path "${replacement_path}" \
			'.Replace[]? | select(.Old.Path == $required_module and .New.Path == $replacement_path and (.New.Version // "") == "")' \
			"${metadata_file}" >/dev/null; then
			failures+=("module ${module_path} inventory approves missing filesystem replacement ${required_module} => ${replacement_path}")
			printf '  replacement-error: approved replacement is absent: %s => %s\n' \
				"${required_module}" "${replacement_path}"
			replacement_failure=1
		fi
	done < <(jq -r '.localReplacements[]? | [.modulePath, .replacementPath] | @tsv' <<<"${module_record}")

	if ((replacement_failure != 0)); then
		printf 'module-result: %s | failed: replacement contract\n' "${module_path}"
		continue
	fi

	mapfile -t module_variables < <(jq -r '.validationVariables[]' <<<"${module_record}")
	child_environment=(env -u MAKEFLAGS -u MAKEOVERRIDES -u GOWORK)
	for variable in "${declared_variables[@]}"; do
		child_environment+=(-u "${variable}")
	done
	child_environment+=(GOWORK=off)
	forwarded_arguments=()
	for variable in "${module_variables[@]}"; do
		if [[ -n "${selected_values[${variable}]+present}" ]]; then
			child_environment+=("${variable}=${selected_values[${variable}]}")
			forwarded_arguments+=("${variable}=${selected_values[${variable}]}")
		fi
	done

	printf '  invocation: GOWORK=off %s --no-print-directory -C %s %s' \
		"${make_command}" "${directory}" "${target}"
	if ((${#forwarded_arguments[@]} > 0)); then
		printf ' %s' "${forwarded_arguments[@]}"
	fi
	printf '\n'

	set +e
	(
		cd "${repo_root}"
		"${child_environment[@]}" "${make_command}" --no-print-directory -C "${directory}" \
			"${target}" "${forwarded_arguments[@]}"
	)
	module_status=$?
	set -e
	if ((module_status != 0)); then
		failures+=("module ${module_path} target ${target} failed with status ${module_status}")
		printf 'module-result: %s | failed: target status %d\n' "${module_path}" "${module_status}"
	else
		printf 'module-result: %s | passed\n' "${module_path}"
	fi
done < <(jq -c '.modules[]' "${inventory_path}")

if ((visited_count != module_count)); then
	failures+=("inventory selected ${module_count} modules but orchestration visited ${visited_count}")
fi

snapshot_manifests "${snapshot_dir}/after"
if ! cmp -s "${snapshot_dir}/before" "${snapshot_dir}/after"; then
	printf 'go-modules-check: module/workspace manifests changed during validation:\n' >&2
	diff -u "${snapshot_dir}/before" "${snapshot_dir}/after" >&2 || true
	failures+=("module/workspace manifest path set or bytes changed during validation")
fi

if ((${#failures[@]} > 0)); then
	for message in "${failures[@]}"; do
		printf 'go-modules-check: %s\n' "${message}" >&2
	done
	exit 1
fi

printf 'go-modules-check: passed %d module(s) in standalone mode; manifests unchanged\n' \
	"${visited_count}"
