#!/usr/bin/env bash

set -euo pipefail

mode=${1:-}
case "$mode" in
	format) operation=format ;;
	check) operation=format-check ;;
	*)
		printf 'format-go: expected mode "format" or "check"\n' >&2
		exit 2
		;;
esac

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
module_root=$(dirname -- "$script_dir")
cd "$module_root"

golangci_lint=${GOLANGCI_LINT:?GOLANGCI_LINT is required}
golangci_config=${GOLANGCI_CONFIG:-.golangci.yml}
format_scope=${FORMAT_SCOPE:-changed}
format_base=${FORMAT_BASE:-}
format_files_set=${FORMAT_FILES_SET:-0}
format_files=${FORMAT_FILES:-}

temporary_dir=$(mktemp -d "${TMPDIR:-/tmp}/product-service-format.XXXXXX")
cleanup() {
	rm -rf -- "$temporary_dir"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

candidates_file=$temporary_dir/candidates
selected_file=$temporary_dir/selected
: >"$candidates_file"
: >"$selected_file"

fail() {
	printf 'format-go: %s\n' "$*" >&2
	exit 1
}

is_excluded_path() {
	case "$1" in
		gen/* | vendor/* | third_party/* | testdata/* | .tools/*) return 0 ;;
		*) return 1 ;;
	esac
}

add_if_eligible() {
	local path=$1
	local explicit=$2
	local canonical relative

	path=${path#./}
	case "$path" in
		'' | /*)
			[[ "$explicit" == true ]] && fail "explicit path must be module-relative: $path"
			return 0
			;;
	esac

	canonical=$(realpath -m -- "$path")
	case "$canonical" in
		"$module_root"/*) relative=${canonical#"$module_root"/} ;;
		*)
			[[ "$explicit" == true ]] && fail "explicit path escapes the module: $path"
			return 0
			;;
	esac

	if [[ "$relative" != *.go ]]; then
		[[ "$explicit" == true ]] && fail "explicit path is not a Go file: $path"
		return 0
	fi
	if is_excluded_path "$relative"; then
		[[ "$explicit" == true ]] && fail "explicit path is excluded from formatting: $path"
		return 0
	fi
	if [[ ! -f "$relative" ]]; then
		[[ "$explicit" == true ]] && fail "explicit path is not a file: $path"
		return 0
	fi
	if grep -Eq '^// Code generated .* DO NOT EDIT\.$' "$relative"; then
		[[ "$explicit" == true ]] && fail "explicit path has a generated-file header: $path"
		return 0
	fi

	printf '%s\0' "$relative" >>"$selected_file"
}

selection_source=
if [[ "$format_files_set" == 1 ]]; then
	selection_source=FORMAT_FILES
	if [[ -n "$format_files" ]]; then
		read -r -a requested_files <<<"$format_files"
		for path in "${requested_files[@]}"; do
			add_if_eligible "$path" true
		done
	fi
else
	case "$format_scope" in
		all)
			selection_source=FORMAT_SCOPE=all
			find . \
				\( -path './gen' -o -path './vendor' -o -path './third_party' \
				-o -path './testdata' -o -path './.tools' \) -prune \
				-o -type f -name '*.go' -print0 >"$candidates_file"
			;;
		changed)
			git rev-parse --is-inside-work-tree >/dev/null 2>&1 ||
				fail "FORMAT_SCOPE=changed requires a Git worktree; use FORMAT_SCOPE=all"
			if [[ -n "$format_base" ]]; then
				git rev-parse --verify --quiet "${format_base}^{commit}" >/dev/null ||
					fail "FORMAT_BASE does not resolve to a commit: $format_base"
				selection_source="FORMAT_SCOPE=changed FORMAT_BASE=$format_base"
				git diff --name-only --diff-filter=ACMR --relative -z "$format_base" -- . \
					>"$candidates_file"
			else
				git rev-parse --verify --quiet HEAD >/dev/null ||
					fail "FORMAT_SCOPE=changed requires HEAD; use FORMAT_SCOPE=all"
				selection_source='FORMAT_SCOPE=changed against HEAD'
				git diff --name-only --diff-filter=ACMR --relative -z HEAD -- . \
					>"$candidates_file"
			fi
			git ls-files --others --exclude-standard -z -- '*.go' >>"$candidates_file"
			;;
		*) fail "FORMAT_SCOPE must be 'changed' or 'all': $format_scope" ;;
	esac

	while IFS= read -r -d '' path; do
		add_if_eligible "$path" false
	done <"$candidates_file"
fi

LC_ALL=C sort -zu -o "$selected_file" "$selected_file"

selected_count=$(awk 'BEGIN { RS = "\0" } END { print NR }' "$selected_file")
printf '%s: selected %s eligible handwritten Go file(s) from %s\n' \
	"$operation" "$selected_count" "$selection_source"

if [[ "$selected_count" -eq 0 ]]; then
	exit 0
fi

while IFS= read -r -d '' path; do
	printf '  %s\n' "$path"
done <"$selected_file"

formatter_args=(fmt --config "$golangci_config")
if [[ "$mode" == check ]]; then
	formatter_args+=(--diff)
	xargs -0 -- "$golangci_lint" "${formatter_args[@]}" -- <"$selected_file"
	exit 0
fi

for iteration in 1 2 3 4 5; do
	before=$(xargs -0 sha256sum -- <"$selected_file")
	xargs -0 -- "$golangci_lint" "${formatter_args[@]}" -- <"$selected_file"
	after=$(xargs -0 sha256sum -- <"$selected_file")
	if [[ "$before" == "$after" ]]; then
		printf 'format: formatter pipeline converged after %s pass(es)\n' "$iteration"
		exit 0
	fi
done

fail 'formatter pipeline did not converge after 5 passes'
