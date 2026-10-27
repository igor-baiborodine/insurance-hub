.DEFAULT_GOAL := help

GO ?= go
INSURANCE_HUB_ROOT ?= ../..
INSURANCE_HUB_ROOT := $(abspath $(INSURANCE_HUB_ROOT))
export INSURANCE_HUB_ROOT
PYTHON_TOOLING_FILES := $(wildcard scripts/check_foundations.py scripts/test_check_foundations.py scripts/check_copy.py scripts/scaffold_process.py \
	scripts/test_check_copy.py scripts/test_scaffold_process.py scripts/test_tooling.py \
	scripts/tooling_runner.py scripts/test_tooling_runner.py)
PYTHON_FAST_TEST_FILES := $(wildcard scripts/test_check_foundations.py scripts/test_check_copy.py scripts/test_scaffold_process.py \
	scripts/test_tooling_runner.py)

include $(INSURANCE_HUB_ROOT)/scripts/python/toolchain.mk

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

GO_VERSION := 1.27.3
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

COMMON_GO_TOOLING_DIR ?= $(INSURANCE_HUB_ROOT)/scripts/go/module
COMMON_GO_TOOLING_DIR := $(abspath $(COMMON_GO_TOOLING_DIR))
include $(COMMON_GO_TOOLING_DIR)/common.mk

.PHONY: python-tooling-format-check python-tooling-lint python-tooling-test-fast python-check \
	bootstrap-go-tools bootstrap-tools verify-tools format format-check update-deps check-deps \
	update-proto-deps format-proto format-proto-check lint-proto check-proto-breaking \
	gen-proto check-proto-drift lint test test-race coverage coverage-validate coverage-check \
	test-coverage-tooling build vulncheck check-go-pr check test-tooling check-copy check-foundations run

python-tooling-format-check: python-toolchain-check
	@cd "$(CURDIR)" && RUFF_CACHE_DIR="$(TOOLS_DIR)/ruff-cache" \
		PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m ruff format --check \
		--config "$(PYTHON_REPOSITORY_ROOT)/pyproject.toml" -- $(PYTHON_TOOLING_FILES)

python-tooling-lint: python-toolchain-check
	@cd "$(CURDIR)" && RUFF_CACHE_DIR="$(TOOLS_DIR)/ruff-cache" \
		PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m ruff check \
		--config "$(PYTHON_REPOSITORY_ROOT)/pyproject.toml" -- $(PYTHON_TOOLING_FILES)

python-tooling-test-fast: python-toolchain-check
	@cd "$(CURDIR)" && PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m pytest -q -p no:cacheprovider \
		-c "$(PYTHON_REPOSITORY_ROOT)/pyproject.toml" --rootdir "$(CURDIR)" \
		-m 'not integration and not acceptance' $(PYTHON_FAST_TEST_FILES)

python-check: python-tooling-format-check python-tooling-lint python-tooling-test-fast

bootstrap-go-tools bootstrap-tools: python-toolchain-setup

update-deps:
	GOWORK=off $(GO) get \
		buf.build/go/protovalidate@$(PROTOVALIDATE_GO_VERSION) \
		buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go@$(PROTOVALIDATE_SCHEMA_GO_VERSION) \
		go.opentelemetry.io/otel@$(OTEL_VERSION) \
		go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc@$(OTEL_GRPC_VERSION) \
		go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc@$(OTEL_VERSION) \
		go.opentelemetry.io/otel/sdk@$(OTEL_VERSION) \
		go.opentelemetry.io/otel/sdk/metric@$(OTEL_VERSION) \
		go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc@$(OTEL_VERSION) \
		google.golang.org/grpc@$(GRPC_GO_VERSION) \
		google.golang.org/protobuf@$(PROTOBUF_GO_VERSION) \
		golang.org/x/net@$(X_NET_VERSION)
	GOWORK=off $(GO) mod tidy

check-go-pr: verify-tools format-check format-proto-check lint lint-proto test-race build \
	check-deps check-proto-drift check-proto-breaking vulncheck python-check check-foundations

check: check-go-pr

test-tooling: verify-tools python-toolchain-check
	@MODULE_ROOT="$(CURDIR)" INSURANCE_HUB_ROOT="$(INSURANCE_HUB_ROOT)" \
		COMMON_GO_TOOLING_DIR="$(COMMON_GO_TOOLING_DIR)" \
		MAKE_COMMAND="$(MAKE)" TOOLS_BIN="$(TOOLS_BIN)" \
		GO="$(GO)" UV="$(UV)" \
		COMMON_GO_TOOLING_PYTHON="$(COMMON_GO_TOOLING_PYTHON)" \
		PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" ./scripts/tooling_runner.py
	@$(MAKE) --no-print-directory test-coverage-tooling

check-copy: verify-tools python-toolchain-check
	@INSURANCE_HUB_ROOT="$(INSURANCE_HUB_ROOT)" \
		COMMON_GO_TOOLING_DIR="$(COMMON_GO_TOOLING_DIR)" \
		COMMON_GO_TOOLING_PYTHON="$(COMMON_GO_TOOLING_PYTHON)" \
		GO="$(GO)" MAKE_COMMAND="$(MAKE)" UV="$(UV)" PYTHONDONTWRITEBYTECODE=1 \
		"$(PYTHON)" ./scripts/check_copy.py

run:
	GOWORK=off $(GO) run -mod=readonly ./cmd/server

check-foundations: python-runtime-check ## Reject common version drift against Product (source template only)
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" scripts/check_foundations.py \
		--scaffold "$(CURDIR)" --reference "$(INSURANCE_HUB_ROOT)/services/product-service"

.PHONY: help
help: ## Show the explicit setup and validation entry points without installing tools
	@printf '%s\n' 'Go service commands (see README.md for the full interface):' \
		'  make bootstrap-tools  Install pinned local tools explicitly' \
		'  make check            Validate the module without source mutation' \
		'  make run              Start the service with environment configuration'
