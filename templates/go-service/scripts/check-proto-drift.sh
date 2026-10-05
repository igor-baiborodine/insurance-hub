#!/usr/bin/env bash

set -euo pipefail

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
module_root=$(dirname -- "$script_dir")
make_command=${MAKE_COMMAND:-make}
tools_bin=${TOOLS_BIN:?TOOLS_BIN is required}
proto_schema=${PROTO_SCHEMA:?PROTO_SCHEMA is required}
generated_proto_files=${GENERATED_PROTO_FILES:?GENERATED_PROTO_FILES is required}

read -r -a expected_outputs <<<"$generated_proto_files"
if [[ "${#expected_outputs[@]}" -eq 0 ]]; then
	printf 'check-proto-drift: no generated outputs are declared\n' >&2
	exit 1
fi

temporary_dir=$(mktemp -d "${TMPDIR:-/tmp}/go-service-proto-drift.XXXXXX")
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
	printf 'check-proto-drift: generated output declarations contain duplicates\n' >&2
	exit 1
fi

while IFS= read -r path; do
	case "$path" in
		gen/*.go) ;;
		*)
			printf 'check-proto-drift: invalid generated output declaration: %s\n' "$path" >&2
			exit 1
			;;
	esac
	case "/$path/" in
		*/../*)
			printf 'check-proto-drift: generated output escapes the module: %s\n' "$path" >&2
			exit 1
			;;
	esac
done <"$expected_inventory"

required_inputs=(Makefile buf.gen.yaml buf.yaml buf.lock "$proto_schema")
for path in "${required_inputs[@]}"; do
	if [[ ! -f "$module_root/$path" || ! -r "$module_root/$path" ]]; then
		printf 'check-proto-drift: missing or unreadable generation input: %s\n' "$path" >&2
		exit 1
	fi
done
for tool in buf protoc-gen-go protoc-gen-go-grpc; do
	if [[ ! -x "$tools_bin/$tool" ]]; then
		printf 'check-proto-drift: missing pinned tool: %s/%s\n' "$tools_bin" "$tool" >&2
		exit 1
	fi
done

copy_generation_context() {
	local destination=$1
	mkdir -p "$destination/.tools/bin"
	(
		cd "$module_root"
		cp --parents -- "${required_inputs[@]}" "$destination"
	)
	cp -- \
		"$tools_bin/buf" \
		"$tools_bin/protoc-gen-go" \
		"$tools_bin/protoc-gen-go-grpc" \
		"$destination/.tools/bin/"
}

write_generated_inventory() {
	local root=$1
	local output=$2
	if [[ ! -d "$root/gen" ]]; then
		: >"$output"
		return
	fi
	find "$root/gen" \( -type f -o -type l \) -printf 'gen/%P\n' | LC_ALL=C sort >"$output"
}

write_non_output_inventory() {
	local root=$1
	find "$root" -path "$root/gen" -prune -o -type f -exec sha256sum {} + |
		LC_ALL=C sort
}

verify_regenerated_set() {
	local root=$1
	local label=$2
	local inventory=$temporary_dir/$label-files
	local differences=$temporary_dir/$label-differences
	write_generated_inventory "$root" "$inventory"
	if ! cmp -s "$expected_inventory" "$inventory"; then
		printf 'check-proto-drift: %s produced an undeclared output set\n' "$label" >&2
		diff -u \
			--label declared-generated-files \
			--label "$label-generated-files" \
			"$expected_inventory" "$inventory" >"$differences" || true
		cat "$differences" >&2
		exit 1
	fi
}

generation_one=$temporary_dir/generation-one
generation_two=$temporary_dir/generation-two
copy_generation_context "$generation_one"
copy_generation_context "$generation_two"

generation_one_before=$(write_non_output_inventory "$generation_one")
"$make_command" -C "$generation_one" gen-proto
generation_one_after=$(write_non_output_inventory "$generation_one")
if [[ "$generation_one_before" != "$generation_one_after" ]]; then
	printf 'check-proto-drift: gen-proto mutated generation inputs or pinned tools\n' >&2
	exit 1
fi

generation_two_before=$(write_non_output_inventory "$generation_two")
"$make_command" -C "$generation_two" gen-proto
generation_two_after=$(write_non_output_inventory "$generation_two")
if [[ "$generation_two_before" != "$generation_two_after" ]]; then
	printf 'check-proto-drift: repeated gen-proto mutated generation inputs or pinned tools\n' >&2
	exit 1
fi

verify_regenerated_set "$generation_one" generation-one
verify_regenerated_set "$generation_two" generation-two

while IFS= read -r path; do
	if ! cmp -s "$generation_one/$path" "$generation_two/$path"; then
		printf 'check-proto-drift: repeated generation is not byte-reproducible: %s\n' \
			"$path" >&2
		exit 1
	fi
done <"$expected_inventory"

checkout_inventory=$temporary_dir/checkout-files
write_generated_inventory "$module_root" "$checkout_inventory"
drift=0

while IFS= read -r path; do
	[[ -n "$path" ]] || continue
	printf 'check-proto-drift: missing expected output: %s\n' "$path" >&2
	drift=1
done < <(comm -23 "$expected_inventory" "$checkout_inventory")

while IFS= read -r path; do
	[[ -n "$path" ]] || continue
	if [[ -f "$module_root/$path" ]] &&
		grep -Eq '^// Code generated .* DO NOT EDIT\.$' "$module_root/$path"; then
		printf 'check-proto-drift: stale generated output is no longer declared: %s\n' \
			"$path" >&2
	else
		printf 'check-proto-drift: unexpected output in generated tree: %s\n' "$path" >&2
	fi
	drift=1
done < <(comm -13 "$expected_inventory" "$checkout_inventory")

while IFS= read -r path; do
	if [[ -f "$module_root/$path" ]] &&
		! cmp -s "$generation_one/$path" "$module_root/$path"; then
		printf 'check-proto-drift: generated output differs from regeneration: %s\n' \
			"$path" >&2
		drift=1
	fi
done <"$expected_inventory"

if [[ "$drift" -ne 0 ]]; then
	exit 1
fi

printf 'check-proto-drift: exact output set and bytes match two reproducible regenerations\n'
