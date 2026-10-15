#!/usr/bin/env bash

set -euo pipefail

fail() {
	printf 'go-ci-coverage-check: %s\n' "$*" >&2
	exit 1
}

if (($# != 2)); then
	fail "expected repository root and inventory path"
fi

repo_root="$1"
inventory_path="$2"
readonly repo_root inventory_path

[[ -d "${repo_root}" ]] || fail "repository root does not exist: ${repo_root}"
[[ -f "${inventory_path}" ]] || fail "inventory does not exist: ${inventory_path}"
command -v jq >/dev/null 2>&1 || fail "jq >= 1.6 is required"

require_literal() {
	local workflow_path="$1"
	local workflow_relative="$2"
	local literal="$3"
	local description="$4"
	grep -Fq -- "${literal}" "${workflow_path}" ||
		fail "workflow ${workflow_relative} is missing ${description}: ${literal}"
}

forbid_literal() {
	local workflow_path="$1"
	local workflow_relative="$2"
	local literal="$3"
	local description="$4"
	if grep -Fq -- "${literal}" "${workflow_path}"; then
		fail "workflow ${workflow_relative} contains forbidden ${description}: ${literal}"
	fi
}

require_pull_request_trigger() {
	local workflow_path="$1"
	local workflow_relative="$2"
	local trigger_path="$3"
	local description="$4"
	if ! awk -v trigger="      - '${trigger_path}'" '
	  $0 == "  pull_request:" { inside = 1; next }
	  inside && ($0 ~ /^[^[:space:]]/ || $0 ~ /^  [A-Za-z_]+:$/) { exit }
	  inside && $0 == "    paths:" { inside_paths = 1; next }
	  inside && $0 ~ /^    [^[:space:]]/ { inside_paths = 0 }
	  inside && inside_paths && $0 == trigger { found = 1 }
	  END { exit(found ? 0 : 1) }
	' "${workflow_path}"; then
		fail "workflow ${workflow_relative} is missing pull_request path trigger for ${description}: ${trigger_path}"
	fi
}

forbid_pull_request_trigger() {
	local workflow_path="$1"
	local workflow_relative="$2"
	local trigger_path="$3"
	local description="$4"
	if awk -v trigger="      - '${trigger_path}'" '
	  $0 == "  pull_request:" { inside = 1; next }
	  inside && ($0 ~ /^[^[:space:]]/ || $0 ~ /^  [A-Za-z_]+:$/) { exit }
	  inside && $0 == "    paths:" { inside_paths = 1; next }
	  inside && $0 ~ /^    [^[:space:]]/ { inside_paths = 0 }
	  inside && inside_paths && $0 == trigger { found = 1 }
	  END { exit(found ? 0 : 1) }
	' "${workflow_path}"; then
		fail "workflow ${workflow_relative} contains forbidden pull_request path trigger for ${description}: ${trigger_path}"
	fi
}

is_standard_service_directory() {
	local directory="$1"
	local service_name
	[[ "${directory}" == services/* ]] || return 1
	service_name="${directory#services/}"
	[[ "${service_name}" != */* && "${service_name}" == *-service ]]
}

mapfile -t workflows < <(
	jq -r '[.modules[].ciWorkflow, .workspaces[].ciWorkflow] | unique[]' "${inventory_path}"
)

for workflow_relative in "${workflows[@]}"; do
	workflow_path="${repo_root}/${workflow_relative}"
	[[ -f "${workflow_path}" ]] || fail "missing workflow: ${workflow_relative}"

	if grep -Eq '^  push:' "${workflow_path}"; then
		fail "workflow ${workflow_relative} must not run on push"
	fi
	require_literal "${workflow_path}" "${workflow_relative}" '  pull_request:' \
		'pull_request-only event'
	require_literal "${workflow_path}" "${workflow_relative}" '      - main' \
		'main target branch'
	require_literal "${workflow_path}" "${workflow_relative}" 'cancel-in-progress: true' \
		'per-PR cancellation'
	require_literal "${workflow_path}" "${workflow_relative}" \
		'ref: ${{ github.event.pull_request.head.sha }}' 'explicit pull request head checkout'
	require_literal "${workflow_path}" "${workflow_relative}" 'fetch-depth: 0' \
		'complete checkout history'
	require_literal "${workflow_path}" "${workflow_relative}" \
		'git diff --name-only --diff-filter=ACDMRT -z "$PR_BASE" "$PR_HEAD"' \
		'complete base/head module selection'
	require_literal "${workflow_path}" "${workflow_relative}" \
		'if (( ${#modules[@]} == 0 ))' 'explicit zero-module no-op'
	require_literal "${workflow_path}" "${workflow_relative}" \
		'if (( ${#modules[@]} > 1 ))' 'multiple-module rejection'
	require_literal "${workflow_path}" "${workflow_relative}" \
		'Multiple Go modules changed; split the pull request' \
		'multiple-module diagnostic'

	while IFS=$'\t' read -r directory role validation_target; do
		[[ "${directory}" == "." ]] && continue
		if is_standard_service_directory "${directory}"; then
			[[ "${role}" == "business-service" ]] ||
				fail "standard service ${directory} must use the business-service role"
			require_pull_request_trigger "${workflow_path}" "${workflow_relative}" \
				'services/*-service/**/*.go' 'generic service Go source'
			require_pull_request_trigger "${workflow_path}" "${workflow_relative}" \
				'services/*-service/go.mod' 'generic service manifest'
			require_pull_request_trigger "${workflow_path}" "${workflow_relative}" \
				'services/*-service/go.sum' 'generic service checksum'
			forbid_pull_request_trigger "${workflow_path}" "${workflow_relative}" \
				'services/*-service/**' 'broad generic service trigger'
			for selector_contract in \
				'services/*)' \
				'service_path=${changed_path#services/}' \
				'service_name=${service_path%%/*}' \
				'[[ "$service_name" == *-service && "$service_path" == */* ]]' \
				'selected["services/$service_name"]=1' \
				'services/*-service)' \
				"module_role='business-service'"
			do
				require_literal "${workflow_path}" "${workflow_relative}" \
					"${selector_contract}" 'generic service selector contract'
			done
		else
			require_pull_request_trigger "${workflow_path}" "${workflow_relative}" \
				"${directory}/**/*.go" "inventory module Go source ${directory}"
			require_pull_request_trigger "${workflow_path}" "${workflow_relative}" \
				"${directory}/go.mod" "inventory module manifest ${directory}"
			require_pull_request_trigger "${workflow_path}" "${workflow_relative}" \
				"${directory}/go.sum" "inventory module checksum ${directory}"
			forbid_pull_request_trigger "${workflow_path}" "${workflow_relative}" \
				"${directory}/**" "broad inventory module trigger ${directory}"
			require_literal "${workflow_path}" "${workflow_relative}" "${directory}/*)" \
				"inline selector root ${directory}"
			require_literal "${workflow_path}" "${workflow_relative}" \
				"module_role='${role}'" "inventory role mapping ${directory}"
		fi
		require_literal "${workflow_path}" "${workflow_relative}" \
			"validation_target=${validation_target}" "owning validation target ${directory}"
	done < <(
		jq -r --arg workflow "${workflow_relative}" \
			'.modules[] | select(.ciWorkflow == $workflow) | [.directory, .role, .validationTarget] | @tsv' \
			"${inventory_path}"
	)

	for required in \
		'uses: actions/setup-go@v7' \
		'cache-dependency-path: ${{ steps.select.outputs.go_sum }}' \
		'uses: actions/cache@v5' \
		'path: ${{ steps.select.outputs.module_dir }}/.tools' \
		'make -C "$MODULE_DIR" bootstrap-go-tools' \
		'COVERAGE_FILE: ${{ github.workspace }}/${{ steps.select.outputs.coverage_file }}' \
		'make -C "$MODULE_DIR" "$VALIDATION_TARGET"' \
		'FORMAT_SCOPE=changed FORMAT_BASE="$PR_BASE"' \
		'rm -f -- "$COVERAGE_FILE"' \
		"steps.select.outputs.module_role != 'service-scaffold'" \
		'uses: codecov/codecov-action@v7' \
		'files: ${{ steps.select.outputs.coverage_file }}' \
		'flags: ${{ steps.select.outputs.coverage_flag }}' \
		'disable_search: true' \
		'fail_ci_if_error: true'
	do
		require_literal "${workflow_path}" "${workflow_relative}" "${required}" \
			'selected-module validation contract'
	done

	for forbidden in \
		'actions/setup-java@' \
		'actions/setup-node@' \
		'make go-modules-check' \
		'make go-topology-test' \
		'make go-scaffold-test-tooling' \
		'make go-product-test-tooling' \
		'make go-scaffold-check-copy' \
		'make -C services/product-service check-ci' \
		'make go-svc-' \
		'make postgres-svc-role' \
		'make product-baseline-' \
		'make product-security-' \
		'kubectl ' \
		'docker build'
	do
		forbid_literal "${workflow_path}" "${workflow_relative}" "${forbidden}" \
			'non-Go or all-module execution'
	done

	module_count="$(jq --arg workflow "${workflow_relative}" \
		'[.modules[] | select(.ciWorkflow == $workflow)] | length' "${inventory_path}")"
	workspace_count="$(jq --arg workflow "${workflow_relative}" \
		'[.workspaces[] | select(.ciWorkflow == $workflow)] | length' "${inventory_path}")"
	printf 'ci-coverage: workflow=%s modules=%d workspaces=%d selected-module-pr-validation=present\n' \
		"${workflow_relative}" "${module_count}" "${workspace_count}"
done

printf 'go-ci-coverage-check: passed %d workflow(s)\n' "${#workflows[@]}"
