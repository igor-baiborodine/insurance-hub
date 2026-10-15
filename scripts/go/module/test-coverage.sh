#!/usr/bin/env bash

set -euo pipefail

common_tooling_dir=${COMMON_GO_TOOLING_DIR:?COMMON_GO_TOOLING_DIR is required}
go_command=${GO:-go}
make_command=${MAKE_COMMAND:-make}

fail() {
	printf 'coverage-tooling-test: %s\n' "$*" >&2
	exit 1
}

temp_parent=$(cd -- "${TMPDIR:-/tmp}" && pwd -P)
temporary_root=$(mktemp -d "${temp_parent}/go-coverage-tooling.XXXXXX")
case "$temporary_root" in
"${temp_parent}"/go-coverage-tooling.*) ;;
*) fail "refusing unexpected temporary path: $temporary_root" ;;
esac
cleanup() {
	rm -rf -- "$temporary_root"
}
trap cleanup EXIT HUP INT TERM

fixture=$temporary_root/orders-service
mkdir -p "$fixture"
cat >"$fixture/go.mod" <<'EOF'
module example.test/orders-service

go 1.27.2
EOF
cat >"$fixture/service.go" <<'EOF'
package orders

func Available() bool {
	return true
}
EOF
cat >"$fixture/service_test.go" <<'EOF'
package orders

import "testing"

func TestAvailable(t *testing.T) {
	if !Available() {
		t.Fatal("expected order service to be available")
	}
}
EOF
cat >"$fixture/Makefile" <<'EOF'
GO ?= go
BUF_VERSION := unused
PROTOC_GEN_GO_VERSION := unused
PROTOC_GEN_GO_GRPC_VERSION := unused
GOLANGCI_LINT_VERSION := unused
GOVULNCHECK_VERSION := unused
include $(COMMON_GO_TOOLING_DIR)/common.mk
EOF

run_make() {
	"$make_command" --no-print-directory -C "$fixture" "$@" \
		GO="$go_command" \
		COMMON_GO_TOOLING_DIR="$common_tooling_dir"
}

expect_failure() {
	local name=$1
	local expected=$2
	shift 2
	local log=$temporary_root/$name.log
	if run_make "$@" >"$log" 2>&1; then
		cat "$log" >&2
		fail "$name unexpectedly passed"
	fi
	grep -Fq -- "$expected" "$log" || {
		cat "$log" >&2
		fail "$name failed without expected diagnostic: $expected"
	}
	printf 'coverage-tooling-test: passed controlled failure: %s\n' "$name"
}

run_make coverage-check COVERAGE_FILE=reports/coverage.out
test -s "$fixture/reports/coverage.out" || fail 'generic coverage report was not created'
grep -Fq 'mode: atomic' "$fixture/reports/coverage.out" ||
	fail 'generic coverage report did not use atomic mode'
printf 'coverage-tooling-test: generic non-Product race coverage passed\n'

cat >"$fixture/threshold.out" <<'EOF'
mode: atomic
example.test/orders-service/service.go:1.1,1.2 3 1
example.test/orders-service/service.go:2.1,2.2 1 0
EOF
run_make coverage-validate COVERAGE_FILE=threshold.out COVERAGE_MINIMUM=75
expect_failure below-threshold 'coverage-check: below minimum' \
	coverage-validate COVERAGE_FILE=threshold.out COVERAGE_MINIMUM=76

cat >"$fixture/exclusions.out" <<'EOF'
mode: atomic
example.test/orders-service/service.go:1.1,1.2 3 1
example.test/orders-service/generated/service.pb.go:1.1,1.2 100 0
EOF
run_make coverage-validate COVERAGE_FILE=exclusions.out COVERAGE_MINIMUM=75 \
	COVERAGE_EXCLUDE_PATHS=generated
expect_failure missing-exclusion 'coverage-check: below minimum' \
	coverage-validate COVERAGE_FILE=exclusions.out COVERAGE_MINIMUM=75
expect_failure missing-report 'coverage-check: profile not found' \
	coverage-validate COVERAGE_FILE=missing.out

cat >"$fixture/without-coverage.mk" <<'EOF'
.PHONY: check
check:
	@true
EOF
if "$make_command" --no-print-directory -C "$fixture" -f without-coverage.mk \
	coverage-check >"$temporary_root/missing-capability.log" 2>&1; then
	fail 'module without coverage capability unexpectedly passed'
fi
grep -Fq "No rule to make target 'coverage-check'" \
	"$temporary_root/missing-capability.log" || {
	cat "$temporary_root/missing-capability.log" >&2
	fail 'missing coverage capability failed without an actionable Make diagnostic'
}

printf 'coverage-tooling-test: generic interface and controlled failures passed\n'
