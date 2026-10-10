#!/usr/bin/env bash

set -euo pipefail

tooling_initialize() {
	local temporary_prefix=$1
	shift
	module_root=${MODULE_ROOT:?MODULE_ROOT is required}
	common_tooling_dir=${COMMON_GO_TOOLING_DIR:?COMMON_GO_TOOLING_DIR is required}
	make_command=${MAKE_COMMAND:-make}
	tools_bin=${TOOLS_BIN:?TOOLS_BIN is required}
	tool_names=("$@")

	for tool in "${tool_names[@]}"; do
		[[ -x "$tools_bin/$tool" ]] || tooling_fail "missing pinned tool: $tools_bin/$tool"
	done

	temporary_root=$(mktemp -d "${TMPDIR:-/tmp}/${temporary_prefix}.XXXXXX")
	case "$temporary_root" in
		"${TMPDIR:-/tmp}"/"${temporary_prefix}".*) ;;
		*) tooling_fail "unexpected temporary path: $temporary_root" ;;
	esac
	trap tooling_cleanup EXIT
	trap 'exit 129' HUP
	trap 'exit 130' INT
	trap 'exit 143' TERM

	printf 'test-tooling: temporary fixture root: %s\n' "$temporary_root"
	if [[ ${TEST_TOOLING_SELF_INTERRUPT:-0} == 1 ]]; then
		kill -TERM "$$"
		tooling_fail 'continued after controlled interrupt'
	fi

	base_fixture=$temporary_root/base
	mkdir -p "$base_fixture/.tools/bin" "$base_fixture/.common-go-tooling" \
		"$temporary_root/logs"
	tar --exclude='./.tools' -C "$module_root" -cf - . | tar -C "$base_fixture" -xf -
	cp -a "$common_tooling_dir/." "$base_fixture/.common-go-tooling/"
	for tool in "${tool_names[@]}"; do
		ln -s "$tools_bin/$tool" "$base_fixture/.tools/bin/$tool"
	done
}

tooling_fail() {
	printf 'test-tooling: %s\n' "$*" >&2
	exit 1
}

tooling_cleanup() {
	if [[ -n "${temporary_root:-}" ]]; then
		rm -rf -- "$temporary_root"
	fi
}

new_fixture() {
	local name=$1
	local fixture=$temporary_root/$name
	mkdir -p "$fixture"
	cp -a "$base_fixture/." "$fixture/"
	printf '%s\n' "$fixture"
}

write_inventory() {
	local fixture=$1
	find "$fixture" -path "$fixture/.tools" -prune -o -type f -exec sha256sum {} + |
		LC_ALL=C sort
}

expect_success() {
	local name=$1
	local target=$2
	local fixture=$3
	shift 3
	local log=$temporary_root/logs/$name.log
	local before after

	before=$(write_inventory "$fixture")
	if ! "$make_command" --no-print-directory -C "$fixture" "$target" "$@" \
		COMMON_GO_TOOLING_DIR="$fixture/.common-go-tooling" >"$log" 2>&1; then
		cat "$log" >&2
		tooling_fail "$name could not establish a passing $target baseline"
	fi
	after=$(write_inventory "$fixture")
	if [[ "$before" != "$after" ]]; then
		cat "$log" >&2
		tooling_fail "$name baseline mutated its disposable checkout"
	fi
}

expect_failure() {
	local name=$1
	local target=$2
	local expected=$3
	local fixture=$4
	shift 4
	local log=$temporary_root/logs/$name.log
	local before after

	before=$(write_inventory "$fixture")
	if "$make_command" --no-print-directory -C "$fixture" "$target" "$@" \
		COMMON_GO_TOOLING_DIR="$fixture/.common-go-tooling" >"$log" 2>&1; then
		cat "$log" >&2
		tooling_fail "$name unexpectedly passed target $target"
	fi
	after=$(write_inventory "$fixture")
	if [[ "$before" != "$after" ]]; then
		cat "$log" >&2
		tooling_fail "$name mutated its disposable checkout"
	fi
	if ! grep -Fq -- "$expected" "$log"; then
		cat "$log" >&2
		tooling_fail "$name failed without expected diagnostic: $expected"
	fi
	printf 'test-tooling: passed controlled failure: %s -> %s\n' "$name" "$target"
}
