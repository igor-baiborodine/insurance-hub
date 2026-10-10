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
		  inside && $0 == "    paths:" { inside_paths = 1; next }
		  inside && $0 ~ /^    [^[:space:]]/ { inside_paths = 0 }
		  inside && inside_paths && $0 == trigger { found = 1 }
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
	  function is_direct_make_command(value) {
	    sub(/^[[:space:]]*/, "", value)
	    sub(/[[:space:]]*$/, "", value)
	    if (value == "make " target) {
	      return 1
	    }
	    if (target == "go-modules-check" &&
	        value == "make go-modules-check FORMAT_SCOPE=\"$FORMAT_SCOPE\" FORMAT_BASE=\"$FORMAT_BASE\"") {
	      return 1
	    }
	    return 0
	  }
	  {
	    line = $0
	    indent = leading_spaces(line)
	    trimmed = line
	    sub(/^[[:space:]]*/, "", trimmed)
	    if (trimmed == "" || trimmed ~ /^#/) {
	      next
	    }
	    if (indent == 0 && trimmed == "jobs:") {
	      inside_jobs = 1
	      jobs_indent = indent
	      job_indent = -1
	      inside_steps = 0
	      step_indent = -1
	      next
	    }
	    if (!inside_jobs) {
	      next
	    }
	    if (indent <= jobs_indent) {
	      inside_jobs = 0
	      job_indent = -1
	      inside_steps = 0
	      step_indent = -1
	      next
	    }
	    if (indent == jobs_indent + 2 &&
	        trimmed ~ /^[A-Za-z0-9_.-]+:[[:space:]]*(#.*)?$/) {
	      job_indent = indent
	      inside_steps = 0
	      step_indent = -1
	      next
	    }
	    if (job_indent < 0) {
	      next
	    }
	    if (indent <= job_indent) {
	      inside_steps = 0
	      step_indent = -1
	      next
	    }
	    if (indent == job_indent + 2 && trimmed == "steps:") {
	      inside_steps = 1
	      steps_indent = indent
	      step_indent = -1
	      next
	    }
	    if (!inside_steps) {
	      next
	    }
	    if (indent <= steps_indent) {
	      inside_steps = 0
	      step_indent = -1
	      next
	    }
	    if (trimmed ~ /^-[[:space:]]+/ &&
	        (step_indent < 0 || indent == step_indent)) {
	      step_indent = indent
	    }
	    value = trimmed
	    valid_run = 0
	    if (indent == step_indent && value ~ /^-[[:space:]]+run:[[:space:]]*/) {
	      sub(/^-[[:space:]]+run:[[:space:]]*/, "", value)
	      valid_run = 1
	    } else if (indent == step_indent + 2 && value ~ /^run:[[:space:]]*/) {
	      sub(/^run:[[:space:]]*/, "", value)
	      valid_run = 1
	    }
	    if (!valid_run) {
	      next
	    }
	    if (is_direct_make_command(value)) {
	      found = 1
	    }
	  }
	  END { exit(found ? 0 : 1) }
	' "${workflow_path}"; then
		fail "workflow ${workflow_relative} does not invoke executable Make target: ${target}"
	fi
}

require_setup_go_cache_input() {
	local workflow_path="$1"
	local workflow_relative="$2"
	local cache_input="$3"
	local description="$4"
	if ! awk -v cache_input="${cache_input}" '
	  function finish_step() {
	    if (setup_go && cache_enabled && cache_block && cache_found) {
	      found = 1
	    }
	  }
	  /^      - / {
	    finish_step()
	    setup_go = 0
	    cache_enabled = 0
	    cache_block = 0
	    cache_found = 0
	  }
	  /^        uses: actions\/setup-go@/ || /^      - uses: actions\/setup-go@/ {
	    setup_go = 1
	  }
	  setup_go && $0 == "          cache: true" { cache_enabled = 1 }
	  setup_go && $0 == "          cache-dependency-path: |" { cache_block = 1; next }
	  setup_go && cache_block && $0 == "            " cache_input { cache_found = 1 }
	  END {
	    finish_step()
	    exit(found ? 0 : 1)
	  }
	' "${workflow_path}"; then
		fail "workflow ${workflow_relative} is missing active ${description}: ${cache_input}"
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
		'legacy/product-service/src/test/resources/product-read-baseline/**' \
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
	require_run_command "${workflow_path}" "${workflow_relative}" "go-scaffold-bootstrap-tools"
	require_run_command "${workflow_path}" "${workflow_relative}" "go-product-bootstrap-tools"
	require_run_command "${workflow_path}" "${workflow_relative}" "go-scaffold-test-tooling"
	require_run_command "${workflow_path}" "${workflow_relative}" "go-product-test-tooling"
	require_run_command "${workflow_path}" "${workflow_relative}" "go-scaffold-check-copy"
	require_setup_go_cache_input "${workflow_path}" "${workflow_relative}" \
		'templates/go-service/go.sum' 'scaffold setup-go cache input'
	require_setup_go_cache_input "${workflow_path}" "${workflow_relative}" \
		'services/product-service/go.sum' 'Product setup-go cache input'

	module_count="$(jq --arg workflow "${workflow_relative}" \
		'[.modules[] | select(.ciWorkflow == $workflow)] | length' "${inventory_path}")"
	workspace_count="$(jq --arg workflow "${workflow_relative}" \
		'[.workspaces[] | select(.ciWorkflow == $workflow)] | length' "${inventory_path}")"
	printf 'ci-coverage: workflow=%s modules=%d workspaces=%d tooling-and-topology-targets=7\n' \
		"${workflow_relative}" "${module_count}" "${workspace_count}"
done

printf 'go-ci-coverage-check: passed %d workflow(s)\n' "${#workflows[@]}"
