#!/usr/bin/env bash

set -euo pipefail

module_root=${MODULE_ROOT:?MODULE_ROOT is required}
common_tooling_dir=${COMMON_GO_TOOLING_DIR:?COMMON_GO_TOOLING_DIR is required}
make_command=${MAKE_COMMAND:-make}
tools_bin=${TOOLS_BIN:?TOOLS_BIN is required}
check_name=${CHECK_NAME:?CHECK_NAME is required}
generation_target=${GENERATION_TARGET:?GENERATION_TARGET is required}
generation_inputs=${GENERATION_INPUTS:?GENERATION_INPUTS is required}
generation_tools=${GENERATION_TOOLS:?GENERATION_TOOLS is required}
generated_root=${GENERATED_ROOT:?GENERATED_ROOT is required}
generated_pattern=${GENERATED_PATTERN:?GENERATED_PATTERN is required}
generated_files=${GENERATED_FILES:?GENERATED_FILES is required}
stale_generated_pattern=${STALE_GENERATED_PATTERN:-}

read -r -a required_inputs <<<"$generation_inputs"
read -r -a required_tools <<<"$generation_tools"
read -r -a expected_outputs <<<"$generated_files"
if [[ "${#expected_outputs[@]}" -eq 0 ]]; then
	printf '%s: no generated outputs are declared\n' "$check_name" >&2
	exit 1
fi

temporary_dir=$(mktemp -d "${TMPDIR:-/tmp}/go-generated-drift.XXXXXX")
cleanup() {
	rm -rf -- "$temporary_dir"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

expected_inventory=$temporary_dir/expected-files
printf '%s\n' "${expected_outputs[@]}" | LC_ALL=C sort -u >"$expected_inventory"
if [[ $(wc -l <"$expected_inventory") -ne ${#expected_outputs[@]} ]]; then
	printf '%s: generated output declarations contain duplicates\n' "$check_name" >&2
	exit 1
fi

while IFS= read -r path; do
	case "$path" in
		$generated_pattern) ;;
		*)
			printf '%s: invalid generated output declaration: %s\n' "$check_name" "$path" >&2
			exit 1
			;;
	esac
	case "/$path/" in
		*/../*)
			printf '%s: generated output escapes the module: %s\n' "$check_name" "$path" >&2
			exit 1
			;;
	esac
done <"$expected_inventory"

for path in "${required_inputs[@]}"; do
	if [[ ! -f "$module_root/$path" || ! -r "$module_root/$path" ]]; then
		printf '%s: missing or unreadable generation input: %s\n' "$check_name" "$path" >&2
		exit 1
	fi
done
for tool in "${required_tools[@]}"; do
	if [[ ! -x "$tools_bin/$tool" ]]; then
		printf '%s: missing pinned tool: %s/%s\n' "$check_name" "$tools_bin" "$tool" >&2
		exit 1
	fi
done

copy_generation_context() {
	local destination=$1
	local isolated_tooling=$destination/.common-go-tooling
	mkdir -p "$destination/.tools/bin" "$isolated_tooling"
	(
		cd "$module_root"
		cp --parents -- "${required_inputs[@]}" "$destination"
	)
	for tool in "${required_tools[@]}"; do
		cp -- "$tools_bin/$tool" "$destination/.tools/bin/"
	done
	cp -a "$common_tooling_dir/." "$isolated_tooling/"
}

write_generated_inventory() {
	local root=$1
	local output=$2
	local output_root=$root/$generated_root
	if [[ ! -d "$output_root" ]]; then
		: >"$output"
		return
	fi
	find "$output_root" \( -type f -o -type l \) \
		-printf "$generated_root/%P\n" | LC_ALL=C sort >"$output"
}

write_non_output_inventory() {
	local root=$1
	find "$root" \
		\( -path "$root/$generated_root" -o -path "$root/.common-go-tooling" \) -prune \
		-o -type f -exec sha256sum {} + | LC_ALL=C sort
}

verify_regenerated_set() {
	local root=$1
	local label=$2
	local inventory=$temporary_dir/$label-files
	write_generated_inventory "$root" "$inventory"
	if ! cmp -s "$expected_inventory" "$inventory"; then
		printf '%s: %s produced an undeclared output set\n' "$check_name" "$label" >&2
		diff -u \
			--label declared-generated-files \
			--label "$label-generated-files" \
			"$expected_inventory" "$inventory" >&2 || true
		exit 1
	fi
}

generation_one=$temporary_dir/generation-one
generation_two=$temporary_dir/generation-two
copy_generation_context "$generation_one"
copy_generation_context "$generation_two"

for generation in "$generation_one" "$generation_two"; do
	before=$(write_non_output_inventory "$generation")
	"$make_command" -C "$generation" "$generation_target" \
		COMMON_GO_TOOLING_DIR="$generation/.common-go-tooling"
	after=$(write_non_output_inventory "$generation")
	if [[ "$before" != "$after" ]]; then
		printf '%s: %s mutated generation inputs or pinned tools\n' \
			"$check_name" "$generation_target" >&2
		exit 1
	fi
done

verify_regenerated_set "$generation_one" generation-one
verify_regenerated_set "$generation_two" generation-two

while IFS= read -r path; do
	if ! cmp -s "$generation_one/$path" "$generation_two/$path"; then
		printf '%s: repeated generation is not byte-reproducible: %s\n' \
			"$check_name" "$path" >&2
		exit 1
	fi
done <"$expected_inventory"

checkout_inventory=$temporary_dir/checkout-files
write_generated_inventory "$module_root" "$checkout_inventory"
drift=0

while IFS= read -r path; do
	[[ -n "$path" ]] || continue
	printf '%s: missing expected output: %s\n' "$check_name" "$path" >&2
	drift=1
done < <(comm -23 "$expected_inventory" "$checkout_inventory")

while IFS= read -r path; do
	[[ -n "$path" ]] || continue
	if [[ -n "$stale_generated_pattern" && -f "$module_root/$path" ]] &&
		grep -Eq "$stale_generated_pattern" "$module_root/$path"; then
		printf '%s: stale generated output is no longer declared: %s\n' \
			"$check_name" "$path" >&2
	else
		printf '%s: unexpected output in generated tree: %s\n' "$check_name" "$path" >&2
	fi
	drift=1
done < <(comm -13 "$expected_inventory" "$checkout_inventory")

while IFS= read -r path; do
	if [[ -f "$module_root/$path" ]] &&
		! cmp -s "$generation_one/$path" "$module_root/$path"; then
		printf '%s: generated output differs from regeneration: %s\n' \
			"$check_name" "$path" >&2
		drift=1
	fi
done <"$expected_inventory"

if [[ "$drift" -ne 0 ]]; then
	exit 1
fi

printf '%s: exact output set and bytes match two reproducible regenerations\n' "$check_name"
