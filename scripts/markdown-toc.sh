#!/usr/bin/env bash

set -euo pipefail

readonly DOCTOC_VERSION=2.5.0

usage() {
	printf 'Usage: %s <format|check> <markdown-file>...\n' "${0##*/}" >&2
}

fail() {
	printf 'markdown-toc: %s\n' "$1" >&2
	exit 1
}

[[ $# -ge 2 ]] || {
	usage
	exit 2
}

mode=$1
shift

case "$mode" in
format | check) ;;
*)
	usage
	exit 2
	;;
esac

command -v npx >/dev/null 2>&1 || fail 'npx is required'

repository_root=$(git rev-parse --show-toplevel 2>/dev/null) ||
	fail 'run this command from an Insurance Hub checkout'

declare -a markdown_files=()
for requested_file in "$@"; do
	if [[ "$requested_file" = /* ]]; then
		candidate=$requested_file
	else
		candidate=$PWD/$requested_file
	fi

	[[ -f "$candidate" ]] || fail "file does not exist: $requested_file"
	case "$candidate" in
	*.md) ;;
	*) fail "expected a .md file: $requested_file" ;;
	esac

	canonical_file=$(realpath -- "$candidate")
	case "$canonical_file" in
	"$repository_root"/*) ;;
	*) fail "file is outside the repository: $requested_file" ;;
	esac
	markdown_files+=("$canonical_file")
done

run_doctoc() {
	npx --yes --package "doctoc@${DOCTOC_VERSION}" doctoc \
		--github \
		--notitle \
		--mintocitems 1 \
		"$1"
}

if [[ "$mode" == format ]]; then
	for markdown_file in "${markdown_files[@]}"; do
		run_doctoc "$markdown_file"
	done
	exit 0
fi

temporary_dir=$(mktemp -d)
cleanup() {
	find "$temporary_dir" -type f -delete
	rmdir "$temporary_dir"
}
trap cleanup EXIT

status=0
index=0
for markdown_file in "${markdown_files[@]}"; do
	index=$((index + 1))
	candidate="$temporary_dir/$index.md"
	cp -- "$markdown_file" "$candidate"
	run_doctoc "$candidate" >/dev/null
	if ! cmp -s -- "$markdown_file" "$candidate"; then
		printf 'markdown-toc: stale or missing table of contents: %s\n' \
			"${markdown_file#"$repository_root"/}" >&2
		status=1
	fi
done

if [[ $status -ne 0 ]]; then
	printf 'markdown-toc: run the format mode for the reported files\n' >&2
fi

exit "$status"
