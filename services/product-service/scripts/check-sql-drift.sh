#!/usr/bin/env bash

set -euo pipefail

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
module_root=$(dirname -- "$script_dir")
make_command=${MAKE_COMMAND:-make}
tools_bin=${TOOLS_BIN:?TOOLS_BIN is required}
sql_schema=${SQL_SCHEMA:?SQL_SCHEMA is required}
sql_queries=${SQL_QUERIES:?SQL_QUERIES is required}
generated_sql_files=${GENERATED_SQL_FILES:?GENERATED_SQL_FILES is required}

read -r -a expected_outputs <<<"$generated_sql_files"
if [[ "${#expected_outputs[@]}" -eq 0 ]]; then
	printf 'check-sql-drift: no generated outputs are declared\n' >&2
	exit 1
fi

temporary_dir=$(mktemp -d "${TMPDIR:-/tmp}/product-service-sql-drift.XXXXXX")
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
	printf 'check-sql-drift: generated output declarations contain duplicates\n' >&2
	exit 1
fi

while IFS= read -r path; do
	case "$path" in
		internal/postgres/dbgen/*.go) ;;
		*)
			printf 'check-sql-drift: invalid generated output declaration: %s\n' "$path" >&2
			exit 1
			;;
	esac
	case "/$path/" in
		*/../*)
			printf 'check-sql-drift: generated output escapes the module: %s\n' "$path" >&2
			exit 1
			;;
	esac
done <"$expected_inventory"

required_inputs=(Makefile sqlc.yaml "$sql_schema" "$sql_queries")
for path in "${required_inputs[@]}"; do
	if [[ ! -f "$module_root/$path" || ! -r "$module_root/$path" ]]; then
		printf 'check-sql-drift: missing or unreadable generation input: %s\n' "$path" >&2
		exit 1
	fi
done
if [[ ! -x "$tools_bin/sqlc" ]]; then
	printf 'check-sql-drift: missing pinned tool: %s/sqlc\n' "$tools_bin" >&2
	exit 1
fi

copy_generation_context() {
	local destination=$1
	mkdir -p "$destination/.tools/bin"
	(
		cd "$module_root"
		cp --parents -- "${required_inputs[@]}" "$destination"
	)
	cp -- "$tools_bin/sqlc" "$destination/.tools/bin/"
}

write_generated_inventory() {
	local root=$1
	local output=$2
	local generated_root=$root/internal/postgres/dbgen
	if [[ ! -d "$generated_root" ]]; then
		: >"$output"
		return
	fi
	find "$generated_root" \( -type f -o -type l \) \
		-printf 'internal/postgres/dbgen/%P\n' | LC_ALL=C sort >"$output"
}

write_non_output_inventory() {
	local root=$1
	find "$root" -path "$root/internal/postgres/dbgen" -prune -o -type f -exec sha256sum {} + |
		LC_ALL=C sort
}

verify_regenerated_set() {
	local root=$1
	local label=$2
	local inventory=$temporary_dir/$label-files
	write_generated_inventory "$root" "$inventory"
	if ! cmp -s "$expected_inventory" "$inventory"; then
		printf 'check-sql-drift: %s produced an undeclared output set\n' "$label" >&2
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

generation_one_before=$(write_non_output_inventory "$generation_one")
"$make_command" -C "$generation_one" gen-sql
generation_one_after=$(write_non_output_inventory "$generation_one")
if [[ "$generation_one_before" != "$generation_one_after" ]]; then
	printf 'check-sql-drift: gen-sql mutated generation inputs or pinned tools\n' >&2
	exit 1
fi

generation_two_before=$(write_non_output_inventory "$generation_two")
"$make_command" -C "$generation_two" gen-sql
generation_two_after=$(write_non_output_inventory "$generation_two")
if [[ "$generation_two_before" != "$generation_two_after" ]]; then
	printf 'check-sql-drift: repeated gen-sql mutated generation inputs or pinned tools\n' >&2
	exit 1
fi

verify_regenerated_set "$generation_one" generation-one
verify_regenerated_set "$generation_two" generation-two

while IFS= read -r path; do
	if ! cmp -s "$generation_one/$path" "$generation_two/$path"; then
		printf 'check-sql-drift: repeated generation is not byte-reproducible: %s\n' \
			"$path" >&2
		exit 1
	fi
done <"$expected_inventory"

checkout_inventory=$temporary_dir/checkout-files
write_generated_inventory "$module_root" "$checkout_inventory"
drift=0

while IFS= read -r path; do
	[[ -n "$path" ]] || continue
	printf 'check-sql-drift: missing expected output: %s\n' "$path" >&2
	drift=1
done < <(comm -23 "$expected_inventory" "$checkout_inventory")

while IFS= read -r path; do
	[[ -n "$path" ]] || continue
	printf 'check-sql-drift: unexpected generated output: %s\n' "$path" >&2
	drift=1
done < <(comm -13 "$expected_inventory" "$checkout_inventory")

while IFS= read -r path; do
	if [[ -f "$module_root/$path" ]] &&
		! cmp -s "$generation_one/$path" "$module_root/$path"; then
		printf 'check-sql-drift: generated output differs from regeneration: %s\n' \
			"$path" >&2
		drift=1
	fi
done <"$expected_inventory"

if [[ "$drift" -ne 0 ]]; then
	exit 1
fi

printf 'check-sql-drift: exact output set and bytes match two reproducible regenerations\n'
