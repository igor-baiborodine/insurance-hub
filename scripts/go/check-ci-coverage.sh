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

require_trigger() {
	local workflow_path="$1"
	local workflow_relative="$2"
	local trigger_path="$3"
	local description="$4"
	local event
	for event in push pull_request; do
		if ! awk -v header="  ${event}:" -v trigger="      - '${trigger_path}'" '
		  $0 == header { inside = 1; next }
		  inside && ($0 ~ /^[^[:space:]]/ || $0 ~ /^  [A-Za-z_]+:$/) { exit }
		  inside && $0 == trigger { found = 1 }
		  END { exit(found ? 0 : 1) }
		' "${workflow_path}"; then
			fail "workflow ${workflow_relative} is missing ${event} path trigger for ${description}: ${trigger_path}"
		fi
	done
}

require_run_command() {
	local workflow_path="$1"
	local workflow_relative="$2"
	local target="$3"
	if ! awk -v target="${target}" '
	  function leading_spaces(value, copy) {
	    copy = value
	    sub(/^ */, "", copy)
	    return length(value) - length(copy)
	  }
	  function is_direct_make_command(value, remainder) {
	    sub(/^[[:space:]]*/, "", value)
	    if (value ~ /^#/) {
	      return 0
	    }
	    if (value !~ /^make[[:space:]]+/) {
	      return 0
	    }
	    sub(/^make[[:space:]]+/, "", value)
	    if (value == target) {
	      return 1
	    }
	    if (index(value, target) != 1) {
	      return 0
	    }
	    remainder = substr(value, length(target) + 1, 1)
	    return remainder ~ /[[:space:]\\]/
	  }
	  {
	    line = $0
	    if (inside_run_block) {
	      trimmed = line
	      sub(/^[[:space:]]*/, "", trimmed)
	      if (trimmed == "" || trimmed ~ /^#/) {
	        next
	      }
	      if (leading_spaces(line) > run_indent) {
	        if (is_direct_make_command(line)) {
	          found = 1
	        }
	        next
	      }
	      inside_run_block = 0
	    }
	    if (line ~ /^ *(- +)?run: */) {
	      run_indent = leading_spaces(line)
	      value = line
	      sub(/^ *(- +)?run: */, "", value)
	      if (value ~ /^[|>][+-]?( +#.*)?$/) {
	        inside_run_block = 1
	      } else if (is_direct_make_command(value)) {
	        found = 1
	      }
	    }
	  }
	  END { exit(found ? 0 : 1) }
	' "${workflow_path}"; then
		fail "workflow ${workflow_relative} does not invoke executable Make target: ${target}"
	fi
}

mapfile -t workflows < <(
	jq -r '[.modules[].ciWorkflow, .workspaces[].ciWorkflow] | unique[]' "${inventory_path}"
)

for workflow_relative in "${workflows[@]}"; do
	workflow_path="${repo_root}/${workflow_relative}"
	[[ -f "${workflow_path}" ]] || fail "missing workflow: ${workflow_relative}"

	for trigger_path in \
		'go.mod' \
		'go.sum' \
		'go.work' \
		'go.work.sum' \
		'**/go.mod' \
		'**/go.sum' \
		'**/go.work' \
		'**/go.work.sum' \
		'go-module-topology.json' \
		'scripts/go/**' \
		'docs/migration/phase-4/go-module-topology.md' \
		'Makefile' \
		"${workflow_relative}"
	do
		require_trigger "${workflow_path}" "${workflow_relative}" "${trigger_path}" \
			"repository topology policy"
	done

	while IFS= read -r directory; do
		if [[ "${directory}" != "." ]]; then
			require_trigger "${workflow_path}" "${workflow_relative}" "${directory}/**" \
				"inventory module directory ${directory}"
		fi
	done < <(
		jq -r --arg workflow "${workflow_relative}" \
			'.modules[] | select(.ciWorkflow == $workflow) | .directory' "${inventory_path}"
	)

	while IFS= read -r directory; do
		if [[ "${directory}" != "." ]]; then
			require_trigger "${workflow_path}" "${workflow_relative}" "${directory}/**" \
				"approved workspace directory ${directory}"
		fi
	done < <(
		jq -r --arg workflow "${workflow_relative}" \
			'.workspaces[] | select(.ciWorkflow == $workflow) | .directory' "${inventory_path}"
	)

	require_run_command "${workflow_path}" "${workflow_relative}" "go-modules-check"
	require_run_command "${workflow_path}" "${workflow_relative}" "go-topology-test"

	module_count="$(jq --arg workflow "${workflow_relative}" \
		'[.modules[] | select(.ciWorkflow == $workflow)] | length' "${inventory_path}")"
	workspace_count="$(jq --arg workflow "${workflow_relative}" \
		'[.workspaces[] | select(.ciWorkflow == $workflow)] | length' "${inventory_path}")"
	printf 'ci-coverage: workflow=%s modules=%d workspaces=%d targets=go-modules-check,go-topology-test\n' \
		"${workflow_relative}" "${module_count}" "${workspace_count}"
done

printf 'go-ci-coverage-check: passed %d workflow(s)\n' "${#workflows[@]}"
