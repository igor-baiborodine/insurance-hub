.DEFAULT_GOAL := bootstrap-tools

GO ?= go

TOOLS_DIR := $(CURDIR)/.tools
TOOLS_BIN := $(TOOLS_DIR)/bin
BUF := $(TOOLS_BIN)/buf
PROTOC_GEN_GO := $(TOOLS_BIN)/protoc-gen-go
PROTOC_GEN_GO_GRPC := $(TOOLS_BIN)/protoc-gen-go-grpc
GOLANGCI_LINT := $(TOOLS_BIN)/golangci-lint
GOVULNCHECK := $(TOOLS_BIN)/govulncheck
GOLANGCI_CONFIG := .golangci.yml

PROTO_SCHEMA := api/scaffold/v1/example_service.proto
CONTRACT_BASELINE_DIR := testdata/contract-baseline
GENERATED_PROTO_FILES := \
	gen/scaffold/v1/example_service.pb.go \
	gen/scaffold/v1/example_service_grpc.pb.go

GO_VERSION := 1.27.2
BUF_VERSION := v1.73.0
PROTOC_GEN_GO_VERSION := v1.36.12
PROTOC_GEN_GO_GRPC_VERSION := v1.6.2
GOLANGCI_LINT_VERSION := v2.14.0
GOVULNCHECK_VERSION := v1.8.0

PROTOBUF_GO_VERSION := v1.36.12
GRPC_GO_VERSION := v1.83.2
PROTOVALIDATE_GO_VERSION := v1.4.0
PROTOVALIDATE_SCHEMA_GO_VERSION := v1.36.12-20260825204119-511051f7f437.1
OTEL_VERSION := v1.46.0
OTEL_GRPC_VERSION := v0.71.0
X_NET_VERSION := v0.60.0

.PHONY: bootstrap-tools verify-tools format format-check update-deps check-deps \
	update-proto-deps format-proto format-proto-check lint-proto check-proto-breaking \
	gen-proto check-proto-drift lint test test-race build vulncheck check test-tooling \
	check-copy run

bootstrap-tools:
	mkdir -p "$(TOOLS_BIN)"
	GOWORK=off GOBIN="$(TOOLS_BIN)" $(GO) install \
		github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION)
	GOWORK=off GOBIN="$(TOOLS_BIN)" $(GO) install \
		google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)
	GOWORK=off GOBIN="$(TOOLS_BIN)" $(GO) install \
		google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GO_GRPC_VERSION)
	GOWORK=off GOBIN="$(TOOLS_BIN)" $(GO) install \
		github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	GOWORK=off GOBIN="$(TOOLS_BIN)" $(GO) install \
		golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	@$(MAKE) --no-print-directory verify-tools

verify-tools:
	@GO="$(GO)" \
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
		./scripts/verify-tools.sh

format: verify-tools
	@GOWORK=off \
		GOLANGCI_LINT="$(GOLANGCI_LINT)" \
		GOLANGCI_CONFIG="$(GOLANGCI_CONFIG)" \
		FORMAT_FILES_SET="$(if $(filter undefined,$(origin FORMAT_FILES)),0,1)" \
		./scripts/format-go.sh format

format-check: verify-tools
	@GOWORK=off \
		GOLANGCI_LINT="$(GOLANGCI_LINT)" \
		GOLANGCI_CONFIG="$(GOLANGCI_CONFIG)" \
		FORMAT_FILES_SET="$(if $(filter undefined,$(origin FORMAT_FILES)),0,1)" \
		./scripts/format-go.sh check

update-deps:
	GOWORK=off $(GO) get \
		buf.build/go/protovalidate@$(PROTOVALIDATE_GO_VERSION) \
		buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go@$(PROTOVALIDATE_SCHEMA_GO_VERSION) \
		go.opentelemetry.io/otel@$(OTEL_VERSION) \
		go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc@$(OTEL_GRPC_VERSION) \
		go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc@$(OTEL_VERSION) \
		go.opentelemetry.io/otel/sdk@$(OTEL_VERSION) \
		google.golang.org/grpc@$(GRPC_GO_VERSION) \
		google.golang.org/protobuf@$(PROTOBUF_GO_VERSION) \
		golang.org/x/net@$(X_NET_VERSION)
	GOWORK=off $(GO) mod tidy

check-deps: verify-tools
	@GO="$(GO)" MAKE_COMMAND="$(MAKE)" ./scripts/check-deps.sh

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
	@test "$$($(PROTOC_GEN_GO) --version)" = \
		"protoc-gen-go $(PROTOC_GEN_GO_VERSION)"
	@test "$$($(PROTOC_GEN_GO_GRPC) --version)" = \
		"protoc-gen-go-grpc $(patsubst v%,%,$(PROTOC_GEN_GO_GRPC_VERSION))"
	rm -f $(GENERATED_PROTO_FILES)
	$(BUF) generate

check-proto-drift: verify-tools
	@MAKE_COMMAND="$(MAKE)" \
		TOOLS_BIN="$(TOOLS_BIN)" \
		PROTO_SCHEMA="$(PROTO_SCHEMA)" \
		GENERATED_PROTO_FILES="$(GENERATED_PROTO_FILES)" \
		./scripts/check-proto-drift.sh

lint: verify-tools
	GOWORK=off $(GOLANGCI_LINT) run --config "$(GOLANGCI_CONFIG)" ./...

test:
	GOWORK=off $(GO) test -mod=readonly -count=1 ./...

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

check: verify-tools format-check format-proto-check lint lint-proto test-race build \
	check-deps check-proto-drift check-proto-breaking vulncheck

test-tooling: verify-tools
	@MAKE_COMMAND="$(MAKE)" TOOLS_BIN="$(TOOLS_BIN)" ./scripts/test-tooling.sh

check-copy: verify-tools
	@MAKE_COMMAND="$(MAKE)" ./scripts/check-copy.sh

run:
	GOWORK=off $(GO) run -mod=readonly ./cmd/server
