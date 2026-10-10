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
TEST_PACKAGES ?= ./...

COMMON_GO_TOOLING_DIR ?= ../../scripts/go/module
COMMON_GO_TOOLING_DIR := $(abspath $(COMMON_GO_TOOLING_DIR))
include $(COMMON_GO_TOOLING_DIR)/common.mk

.PHONY: bootstrap-tools verify-tools format format-check update-deps check-deps \
	update-proto-deps format-proto format-proto-check lint-proto check-proto-breaking \
	gen-proto check-proto-drift lint test test-race build vulncheck check test-tooling \
	check-copy run

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

check: verify-tools format-check format-proto-check lint lint-proto test-race build \
	check-deps check-proto-drift check-proto-breaking vulncheck

test-tooling: verify-tools
	@MODULE_ROOT="$(CURDIR)" COMMON_GO_TOOLING_DIR="$(COMMON_GO_TOOLING_DIR)" \
		MAKE_COMMAND="$(MAKE)" TOOLS_BIN="$(TOOLS_BIN)" ./scripts/test-tooling.sh

check-copy: verify-tools
	@COMMON_GO_TOOLING_DIR="$(COMMON_GO_TOOLING_DIR)" \
		MAKE_COMMAND="$(MAKE)" ./scripts/check-copy.sh

run:
	GOWORK=off $(GO) run -mod=readonly ./cmd/server
