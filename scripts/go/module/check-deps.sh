#!/usr/bin/env bash

set -euo pipefail

module_root=${MODULE_ROOT:?MODULE_ROOT is required}
common_tooling_dir=${COMMON_GO_TOOLING_DIR:?COMMON_GO_TOOLING_DIR is required}
go_command=${GO:-go}
make_command=${MAKE_COMMAND:-make}

for manifest in go.mod go.sum; do
	if [[ ! -f "$module_root/$manifest" ]]; then
		printf 'check-deps: required manifest is missing: %s\n' "$manifest" >&2
		exit 1
	fi
done

temporary_dir=$(mktemp -d "${TMPDIR:-/tmp}/go-module-deps.XXXXXX")
cleanup() {
	rm -rf -- "$temporary_dir"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

isolated_module=$temporary_dir/module
isolated_tooling=$isolated_module/.common-go-tooling
mkdir -p "$isolated_module" "$isolated_tooling"
tar --exclude='./.tools' -C "$module_root" -cf - . | tar -C "$isolated_module" -xf -
cp -a "$common_tooling_dir/." "$isolated_tooling/"

non_manifest_inventory() {
	find "$isolated_module" \
		\( -path "$isolated_module/.tools" -o -path "$isolated_tooling" \) -prune \
		-o -type f ! -name go.mod ! -name go.sum -exec sha256sum {} + | LC_ALL=C sort
}

before_inventory=$(non_manifest_inventory)
"$make_command" -C "$isolated_module" update-deps \
	GO="$go_command" COMMON_GO_TOOLING_DIR="$isolated_tooling"
after_inventory=$(non_manifest_inventory)

if [[ "$before_inventory" != "$after_inventory" ]]; then
	printf 'check-deps: update-deps changed files outside go.mod and go.sum\n' >&2
	exit 1
fi

drift=0
for manifest in go.mod go.sum; do
	if [[ ! -f "$isolated_module/$manifest" ]]; then
		printf 'check-deps: update-deps deleted %s\n' "$manifest" >&2
		drift=1
		continue
	fi
	if ! cmp -s "$module_root/$manifest" "$isolated_module/$manifest"; then
		printf 'check-deps: %s is not synchronized with update-deps\n' "$manifest" >&2
		diff -u \
			--label "current/$manifest" \
			--label "update-deps/$manifest" \
			"$module_root/$manifest" "$isolated_module/$manifest" || true
		drift=1
	fi
done

if [[ "$drift" -ne 0 ]]; then
	exit 1
fi

printf 'check-deps: go.mod and go.sum match isolated update-deps output\n'
