#!/bin/sh

set -eu

config=${GOLANGCI_CONFIG:-.golangci.yml}
module_path=$(awk '$1 == "module" { print $2; exit }' go.mod)
module_go_version=$(awk '$1 == "go" { print $2; exit }' go.mod)

fail() {
	printf 'verify-tools: %s\n' "$*" >&2
	exit 1
}

require_executable() {
	name=$1
	path=$2
	[ -x "$path" ] || fail "$name is missing; run 'make bootstrap-tools'"
}

expect_equal() {
	label=$1
	expected=$2
	actual=$3
	[ "$actual" = "$expected" ] ||
		fail "$label mismatch: expected '$expected', got '$actual'"
}

require_executable buf "$BUF"
require_executable protoc-gen-go "$PROTOC_GEN_GO"
require_executable protoc-gen-go-grpc "$PROTOC_GEN_GO_GRPC"
require_executable golangci-lint "$GOLANGCI_LINT"
require_executable govulncheck "$GOVULNCHECK"

expect_equal "go.mod Go version" "$GO_VERSION" "$module_go_version"
expect_equal "Go toolchain version" "go$GO_VERSION" "$($GO env GOVERSION)"
expect_equal "Buf version" "${BUF_VERSION#v}" "$($BUF --version)"
expect_equal "protoc-gen-go version" \
	"protoc-gen-go $PROTOC_GEN_GO_VERSION" "$($PROTOC_GEN_GO --version)"
expect_equal "protoc-gen-go-grpc version" \
	"protoc-gen-go-grpc ${PROTOC_GEN_GO_GRPC_VERSION#v}" "$($PROTOC_GEN_GO_GRPC --version)"

golangci_lint_version=$(
	"$GOLANGCI_LINT" version |
		sed -n 's/^golangci-lint has version \([^ ]*\).*/\1/p'
)
expect_equal "golangci-lint version" "${GOLANGCI_LINT_VERSION#v}" "$golangci_lint_version"

govulncheck_version=$(
	"$GOVULNCHECK" -version 2>&1 |
		sed -n 's/^Scanner: govulncheck@\([^ ]*\).*/\1/p'
)
expect_equal "govulncheck version" "$GOVULNCHECK_VERSION" "$govulncheck_version"

"$GOLANGCI_LINT" config verify --config "$config" >/dev/null

formatter_order=$(awk '
	/^  enable:$/ { in_enable = 1; next }
	in_enable && /^  [a-z]/ { exit }
	in_enable && /^    - / { print $2 }
' "$config" | paste -sd, -)
expect_equal "formatter order" "gofumpt,goimports,golines" "$formatter_order"

config_module_path=$(awk '
	/^    gofumpt:$/ { in_gofumpt = 1; next }
	in_gofumpt && /^    [a-z]/ { exit }
	in_gofumpt && /^      module-path:/ { print $2; exit }
' "$config")
expect_equal "gofumpt module path" "$module_path" "$config_module_path"

goimports_prefix=$(awk '
	/^    goimports:$/ { in_goimports = 1; next }
	in_goimports && /^    [a-z]/ { exit }
	in_goimports && /^        - / { print $2; exit }
' "$config")
expect_equal "goimports local prefix" "$module_path" "$goimports_prefix"

max_len=$(awk '$1 == "max-len:" { print $2; exit }' "$config")
tab_len=$(awk '$1 == "tab-len:" { print $2; exit }' "$config")
expect_equal "golines maximum length" "100" "$max_len"
expect_equal "golines tab width" "8" "$tab_len"

formatters=$($GOLANGCI_LINT formatters --config "$config")
printf '%s\n' "$formatters" | grep -q '^gofumpt:' || fail "gofumpt is not enabled"
printf '%s\n' "$formatters" | grep -q '^goimports:' || fail "goimports is not enabled"
printf '%s\n' "$formatters" | grep -q '^golines:' || fail "golines is not enabled"

linters=$($GOLANGCI_LINT linters --config "$config")
printf '%s\n' "$linters" | grep -q '^govet:' || fail "govet is not enabled"

grep -q '^  tests: true$' "$config" || fail "all-test analysis is not configured"
grep -q '^  modules-download-mode: readonly$' "$config" ||
	fail "readonly module resolution is not configured"

printf '%s\n' \
	"verified Go $GO_VERSION and pinned module-local tools" \
	"verified golangci-lint schema v2 and formatter order: gofumpt, goimports, golines" \
	"verified module/import prefix: $module_path" \
	"verified golines: 100 columns, tab width 8; readonly module loading; all tests; govet"
