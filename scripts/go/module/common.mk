# Shared non-runtime Go module tooling. Including modules retain their own pins, caches, and
# service-specific targets.

EXTRA_TOOL_PACKAGES ?=
VERIFY_EXTRA_TOOL_SPECS ?=

COMMON_TOOL_PACKAGES := \
	github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION) \
	google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION) \
	google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GO_GRPC_VERSION) \
	github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) \
	golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

.PHONY: bootstrap-tools verify-tools format format-check check-deps update-proto-deps \
	format-proto format-proto-check lint-proto check-proto-breaking gen-proto \
	check-proto-drift lint test test-race build vulncheck

bootstrap-tools:
	mkdir -p "$(TOOLS_BIN)"
	@set -eu; \
	for package in $(COMMON_TOOL_PACKAGES) $(EXTRA_TOOL_PACKAGES); do \
		GOWORK=off GOBIN="$(TOOLS_BIN)" $(GO) install "$$package"; \
	done
	@$(MAKE) --no-print-directory verify-tools

verify-tools:
	@MODULE_ROOT="$(CURDIR)" \
		GO="$(GO)" \
		BUF="$(BUF)" \
		PROTOC_GEN_GO="$(PROTOC_GEN_GO)" \
		PROTOC_GEN_GO_GRPC="$(PROTOC_GEN_GO_GRPC)" \
		GOLANGCI_LINT="$(GOLANGCI_LINT)" \
		GOVULNCHECK="$(GOVULNCHECK)" \
		GOLANGCI_CONFIG="$(GOLANGCI_CONFIG)" \
		GO_VERSION="$(GO_VERSION)" \
		BUF_VERSION="$(BUF_VERSION)" \
		PROTOC_GEN_GO_VERSION="$(PROTOC_GEN_GO_VERSION)" \
		PROTOC_GEN_GO_GRPC_VERSION="$(PROTOC_GEN_GO_GRPC_VERSION)" \
		GOLANGCI_LINT_VERSION="$(GOLANGCI_LINT_VERSION)" \
		GOVULNCHECK_VERSION="$(GOVULNCHECK_VERSION)" \
		EXTRA_TOOL_SPECS="$(VERIFY_EXTRA_TOOL_SPECS)" \
		"$(COMMON_GO_TOOLING_DIR)/verify-tools.sh"

format: verify-tools
	@MODULE_ROOT="$(CURDIR)" \
		GOWORK=off \
		GOLANGCI_LINT="$(GOLANGCI_LINT)" \
		GOLANGCI_CONFIG="$(GOLANGCI_CONFIG)" \
		FORMAT_FILES_SET="$(if $(filter undefined,$(origin FORMAT_FILES)),0,1)" \
		"$(COMMON_GO_TOOLING_DIR)/format-go.sh" format

format-check: verify-tools
	@MODULE_ROOT="$(CURDIR)" \
		GOWORK=off \
		GOLANGCI_LINT="$(GOLANGCI_LINT)" \
		GOLANGCI_CONFIG="$(GOLANGCI_CONFIG)" \
		FORMAT_FILES_SET="$(if $(filter undefined,$(origin FORMAT_FILES)),0,1)" \
		"$(COMMON_GO_TOOLING_DIR)/format-go.sh" check

check-deps: verify-tools
	@MODULE_ROOT="$(CURDIR)" \
		COMMON_GO_TOOLING_DIR="$(COMMON_GO_TOOLING_DIR)" \
		GO="$(GO)" \
		MAKE_COMMAND="$(MAKE)" \
		"$(COMMON_GO_TOOLING_DIR)/check-deps.sh"

update-proto-deps:
	@test "$$($(BUF) --version)" = "$(patsubst v%,%,$(BUF_VERSION))"
	$(BUF) dep update

format-proto: verify-tools
	$(BUF) format --write "$(PROTO_SCHEMA)"

format-proto-check: verify-tools
	$(BUF) format --diff --exit-code "$(PROTO_SCHEMA)"

lint-proto: verify-tools
	$(BUF) lint "$(PROTO_SCHEMA)"

check-proto-breaking: verify-tools
	@for path in \
		"$(CONTRACT_BASELINE_DIR)/buf.yaml" \
		"$(CONTRACT_BASELINE_DIR)/buf.lock" \
		"$(CONTRACT_BASELINE_DIR)/$(PROTO_SCHEMA)"; do \
		if test ! -f "$$path" || test ! -r "$$path"; then \
			echo "check-proto-breaking: missing or unreadable baseline file: $$path" >&2; \
			exit 1; \
		fi; \
	done
	$(BUF) breaking "$(PROTO_SCHEMA)" --against \
		"$(CONTRACT_BASELINE_DIR)/$(PROTO_SCHEMA)"

gen-proto:
	@test "$$($(BUF) --version)" = "$(patsubst v%,%,$(BUF_VERSION))"
	@test "$$($(PROTOC_GEN_GO) --version)" = "protoc-gen-go $(PROTOC_GEN_GO_VERSION)"
	@test "$$($(PROTOC_GEN_GO_GRPC) --version)" = \
		"protoc-gen-go-grpc $(patsubst v%,%,$(PROTOC_GEN_GO_GRPC_VERSION))"
	rm -f $(GENERATED_PROTO_FILES)
	$(BUF) generate

check-proto-drift: verify-tools
	@MODULE_ROOT="$(CURDIR)" \
		COMMON_GO_TOOLING_DIR="$(COMMON_GO_TOOLING_DIR)" \
		MAKE_COMMAND="$(MAKE)" \
		TOOLS_BIN="$(TOOLS_BIN)" \
		CHECK_NAME="check-proto-drift" \
		GENERATION_TARGET="gen-proto" \
		GENERATION_INPUTS="Makefile buf.gen.yaml buf.yaml buf.lock $(PROTO_SCHEMA)" \
		GENERATION_TOOLS="buf protoc-gen-go protoc-gen-go-grpc" \
		GENERATED_ROOT="gen" \
		GENERATED_PATTERN="gen/*.go" \
		GENERATED_FILES="$(GENERATED_PROTO_FILES)" \
		STALE_GENERATED_PATTERN='^// Code generated .* DO NOT EDIT\.$$' \
		"$(COMMON_GO_TOOLING_DIR)/check-generated-drift.sh"

lint: verify-tools
	GOWORK=off $(GOLANGCI_LINT) run --config "$(GOLANGCI_CONFIG)" ./...

test:
	@test -n "$(strip $(TEST_PACKAGES))" || { echo "TEST_PACKAGES must not be empty" >&2; exit 1; }
	GOWORK=off $(GO) test -mod=readonly -count=1 $(TEST_PACKAGES)

test-race:
	GOWORK=off $(GO) test -mod=readonly -count=1 -race ./...

build:
	@set -eu; \
	output_dir=$$(mktemp -d); \
	trap 'rm -rf "$$output_dir"' EXIT HUP INT TERM; \
	GOWORK=off $(GO) build -mod=readonly -o "$$output_dir/" ./...; \
	test -x "$$output_dir/server"

vulncheck: verify-tools
	GOWORK=off GOFLAGS=-mod=readonly $(GOVULNCHECK) ./...
