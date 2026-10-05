#!/usr/bin/env bash

set -euo pipefail

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
module_root=$(dirname -- "$script_dir")
make_command=${MAKE_COMMAND:-make}
tools_bin=${TOOLS_BIN:?TOOLS_BIN is required}

fail() {
	printf 'test-tooling: %s\n' "$*" >&2
	exit 1
}

for tool in buf protoc-gen-go protoc-gen-go-grpc golangci-lint govulncheck; do
	[[ -x "$tools_bin/$tool" ]] || fail "missing pinned tool: $tools_bin/$tool"
done

temporary_root=$(mktemp -d "${TMPDIR:-/tmp}/go-service-tooling.XXXXXX")
case "$temporary_root" in
	"${TMPDIR:-/tmp}"/go-service-tooling.*) ;;
	*) fail "unexpected temporary path: $temporary_root" ;;
esac

cleanup() {
	rm -rf -- "$temporary_root"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

printf 'test-tooling: temporary fixture root: %s\n' "$temporary_root"
if [[ ${TEST_TOOLING_SELF_INTERRUPT:-0} == 1 ]]; then
	kill -TERM "$$"
	fail 'continued after controlled interrupt'
fi

base_fixture=$temporary_root/base
mkdir -p "$base_fixture/.tools/bin" "$temporary_root/logs"
tar --exclude='./.tools' -C "$module_root" -cf - . | tar -C "$base_fixture" -xf -
for tool in buf protoc-gen-go protoc-gen-go-grpc golangci-lint govulncheck; do
	ln -s "$tools_bin/$tool" "$base_fixture/.tools/bin/$tool"
done

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
	if ! "$make_command" --no-print-directory -C "$fixture" "$target" "$@" >"$log" 2>&1; then
		cat "$log" >&2
		fail "$name could not establish a passing $target baseline"
	fi
	after=$(write_inventory "$fixture")
	if [[ "$before" != "$after" ]]; then
		cat "$log" >&2
		fail "$name baseline mutated its disposable checkout"
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
	if "$make_command" --no-print-directory -C "$fixture" "$target" "$@" >"$log" 2>&1; then
		cat "$log" >&2
		fail "$name unexpectedly passed target $target"
	fi
	after=$(write_inventory "$fixture")
	if [[ "$before" != "$after" ]]; then
		cat "$log" >&2
		fail "$name mutated its disposable checkout"
	fi
	if ! grep -Fq -- "$expected" "$log"; then
		cat "$log" >&2
		fail "$name failed without expected diagnostic: $expected"
	fi
	printf 'test-tooling: passed controlled failure: %s -> %s\n' "$name" "$target"
}

fixture=$(new_fixture format-new-file)
cat >"$fixture/tooling_format_defect.go" <<'EOF'
package tooling
func formatDefect( ){}
EOF
expect_failure format-new-file format-check tooling_format_defect.go "$fixture" \
	FORMAT_FILES=tooling_format_defect.go

fixture=$(new_fixture lint-only)
cat >"$fixture/internal/config/tooling_lint_defect.go" <<'EOF'
package config

import "os"

func toolingLintDefect() {
	os.Chdir(".")
}
EOF
expect_failure lint-only lint errcheck "$fixture"

fixture=$(new_fixture test)
expect_success test-baseline test "$fixture"
cat >"$fixture/internal/config/tooling_failure_test.go" <<'EOF'
package config

import "testing"

func TestToolingControlledFailure(t *testing.T) {
	t.Fatal("controlled test failure")
}
EOF
expect_failure test test ': test] Error' "$fixture"

fixture=$(new_fixture dependency)
sed -i 's/google.golang.org\/grpc v1\.83\.2/google.golang.org\/grpc v1.84.0/' \
	"$fixture/go.mod"
expect_failure dependency check-deps 'go.mod is not synchronized with update-deps' "$fixture"

fixture=$(new_fixture proto-format)
sed -i 's/^  rpc Echo/rpc Echo/' "$fixture/api/scaffold/v1/example_service.proto"
expect_failure proto-format format-proto-check 'rpc Echo' "$fixture"

fixture=$(new_fixture proto-lint)
sed -i '0,/string message = 1/{s/string message = 1/string BadName = 1/}' \
	"$fixture/api/scaffold/v1/example_service.proto"
expect_failure proto-lint lint-proto 'should be lower_snake_case' "$fixture"

fixture=$(new_fixture proto-breaking)
sed -i '/message EchoResponse {/,/}/ s/string message = 1;/string message = 2;/' \
	"$fixture/api/scaffold/v1/example_service.proto"
expect_failure proto-breaking check-proto-breaking 'Previously present field "1"' "$fixture"

fixture=$(new_fixture generated-modified)
printf '\n// controlled drift\n' >>"$fixture/gen/scaffold/v1/example_service.pb.go"
expect_failure generated-modified check-proto-drift \
	'generated output differs from regeneration' "$fixture"

fixture=$(new_fixture generated-unexpected)
printf 'controlled unexpected output\n' >"$fixture/gen/scaffold/v1/unexpected.txt"
expect_failure generated-unexpected check-proto-drift \
	'unexpected output in generated tree' "$fixture"

fixture=$(new_fixture generated-missing)
rm -- "$fixture/gen/scaffold/v1/example_service_grpc.pb.go"
expect_failure generated-missing check-proto-drift 'missing expected output' "$fixture"

fixture=$(new_fixture generated-stale)
cat >"$fixture/gen/scaffold/v1/removed_service.pb.go" <<'EOF'
// Code generated by controlled tooling test. DO NOT EDIT.

package scaffoldv1
EOF
expect_failure generated-stale check-proto-drift \
	'stale generated output is no longer declared' "$fixture"

printf 'test-tooling: all controlled failures were rejected without fixture mutation\n'
