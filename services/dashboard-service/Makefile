.DEFAULT_GOAL := help

GO ?= go
DOCKER ?= docker
KUBECTL ?= kubectl
DEPLOY_ENV ?=
INSURANCE_HUB_ROOT ?= ../..
INSURANCE_HUB_ROOT := $(abspath $(INSURANCE_HUB_ROOT))
export INSURANCE_HUB_ROOT
PYTHON_TOOLING_FILES := $(wildcard scripts/dashboard_deployment_checks.py scripts/deployment.py \
	scripts/disposable_deployment.py scripts/image_check.py scripts/service_process.py \
	scripts/elasticsearch_integration.py scripts/test_elasticsearch_integration.py \
	scripts/check_gateway_contract.py scripts/test_check_gateway_contract.py \
	scripts/test_deployment.py scripts/test_disposable_deployment.py \
	scripts/test_disposable_command.py \
	scripts/test_service_process.py scripts/test_tooling.py \
	scripts/tooling_runner.py scripts/test_tooling_runner.py)
PYTHON_FAST_TEST_FILES := $(wildcard scripts/test_check_gateway_contract.py \
	scripts/test_elasticsearch_integration.py \
	scripts/test_deployment.py \
	scripts/test_disposable_command.py \
	scripts/test_service_process.py \
	scripts/test_tooling_runner.py)

include $(INSURANCE_HUB_ROOT)/scripts/python/toolchain.mk

TOOLS_DIR := $(CURDIR)/.tools
TOOLS_BIN := $(TOOLS_DIR)/bin
BUF := $(TOOLS_BIN)/buf
PROTOC_GEN_GO := $(TOOLS_BIN)/protoc-gen-go
PROTOC_GEN_GO_GRPC := $(TOOLS_BIN)/protoc-gen-go-grpc
PROTOC_GEN_GRPC_GATEWAY := $(TOOLS_BIN)/protoc-gen-grpc-gateway
PROTOC_GEN_OPENAPIV2 := $(TOOLS_BIN)/protoc-gen-openapiv2
GOLANGCI_LINT := $(TOOLS_BIN)/golangci-lint
GOVULNCHECK := $(TOOLS_BIN)/govulncheck
GOLANGCI_CONFIG := .golangci.yml

PROTO_SCHEMA := api/dashboard/v1/dashboard_service.proto
CONTRACT_BASELINE_DIR := testdata/contract-baseline
GENERATED_PROTO_FILES := \
	gen/dashboard/v1/dashboard_service.pb.go \
	gen/dashboard/v1/dashboard_service.pb.gw.go \
	gen/dashboard/v1/dashboard_service_grpc.pb.go \
	gen/openapiv2/dashboard/v1/dashboard_service.swagger.json

GO_VERSION := 1.27.3
BUF_VERSION := v1.73.0
PROTOC_GEN_GO_VERSION := v1.36.12
PROTOC_GEN_GO_GRPC_VERSION := v1.6.2
GRPC_GATEWAY_VERSION := v2.30.0
GOLANGCI_LINT_VERSION := v2.14.0
GOVULNCHECK_VERSION := v1.8.0

PROTOBUF_GO_VERSION := v1.36.12
GRPC_GO_VERSION := v1.83.2
PROTOVALIDATE_GO_VERSION := v1.4.0
PROTOVALIDATE_SCHEMA_GO_VERSION := v1.36.12-20260825204119-511051f7f437.1
OTEL_VERSION := v1.46.0
OTEL_GRPC_VERSION := v0.71.0
OTEL_HTTP_VERSION := v0.70.0
X_NET_VERSION := v0.60.0
SERVICEKIT_VERSION := v1.0.1
ELASTICSEARCH_CLIENT_VERSION := v8.19.7
ELASTICSEARCH_IMAGE := docker.elastic.co/elasticsearch/elasticsearch:8.19.6@sha256:bfb2108076735fbfc5dcd5d49f12f5aa93fab9759034c55f7da3b4345606c903
GENPROTO_GOOGLEAPIS_API_VERSION := v0.0.0-20260819154853-08b0e4226688
TEST_PACKAGES ?= ./...
COVERAGE_EXCLUDE_PATHS := gen internal/testing
INTEGRATION_COVERAGE_FILE ?=
ELASTICSEARCH_INTEGRATION_COVERAGE_FILE ?= elasticsearch-integration-coverage.txt
HEAD_REVISION := $(shell git -C $(CURDIR) rev-parse --verify HEAD 2>/dev/null)
HEAD_SHORT := $(shell git -C $(CURDIR) rev-parse --short=12 HEAD 2>/dev/null)
HEAD_CREATED := $(shell git -C $(CURDIR) show -s --format=%cI HEAD 2>/dev/null)
HEAD_VERSION := git-$(HEAD_SHORT)
IMAGE_NAME ?= insurance-hub-dashboard-api
SOURCE_REVISION ?= $(HEAD_REVISION)
SOURCE_CREATED ?= $(HEAD_CREATED)
SOURCE_REPOSITORY ?= https://github.com/igor-baiborodine/insurance-hub
SOURCE_VERSION ?= $(HEAD_VERSION)
IMAGE_TAG ?= 1.0.0-local.$(HEAD_SHORT)
IMAGE_REF ?= $(IMAGE_NAME):$(IMAGE_TAG)
TARGET_PLATFORM ?= linux/amd64
GO_SERVICE_TOOLING_CONFIG := $(CURDIR)/go-service-tooling.json

EXTRA_TOOL_PACKAGES := \
	github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@$(GRPC_GATEWAY_VERSION) \
	github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2@$(GRPC_GATEWAY_VERSION)
VERIFY_EXTRA_TOOL_SPECS := \
	protoc-gen-grpc-gateway|$(PROTOC_GEN_GRPC_GATEWAY)|Version $(GRPC_GATEWAY_VERSION), commit unknown, built at unknown|--version;\
	protoc-gen-openapiv2|$(PROTOC_GEN_OPENAPIV2)|Version $(GRPC_GATEWAY_VERSION), commit unknown, built at unknown|--version
PROTO_GENERATION_TOOLS := \
	buf protoc-gen-go protoc-gen-go-grpc protoc-gen-grpc-gateway protoc-gen-openapiv2
GENERATED_PROTO_PATTERN := gen/*

COMMON_GO_TOOLING_DIR ?= $(INSURANCE_HUB_ROOT)/scripts/go/module
COMMON_GO_TOOLING_DIR := $(abspath $(COMMON_GO_TOOLING_DIR))
include $(COMMON_GO_TOOLING_DIR)/common.mk

.PHONY: python-tooling-format-check python-tooling-lint-fix python-tooling-lint python-tooling-test-fast python-check \
	bootstrap-go-tools bootstrap-tools verify-tools format format-check update-deps check-deps \
	update-proto-deps format-proto format-proto-check lint-proto check-proto-breaking \
	gen-proto check-proto-drift check-gateway-contract lint test test-race coverage coverage-validate coverage-check \
	test-coverage-tooling test-integration test-elasticsearch-integration build vulncheck check-go-pr check test-tooling check-servicekit check-candidate-servicekit run \
	image-build image-check _image-check-existing deploy-render deploy-check \
	deploy-preflight deploy-status image-load deploy deploy-verify stop deploy-test-tooling deploy-test \
	shared-fast-build shared-fast-unit shared-candidate-format shared-candidate-lint \
	shared-candidate-race shared-candidate-dependency shared-candidate-vulnerability \
	shared-candidate-generated shared-candidate-breaking shared-candidate-integration \
	shared-candidate-image

export IMAGE_NAME IMAGE_TAG IMAGE_REF SOURCE_CREATED SOURCE_REPOSITORY SOURCE_REVISION SOURCE_VERSION TARGET_PLATFORM

image-build: python-runtime-check ## Build the pinned linux/amd64 Dashboard runtime image
	@PYTHONPATH="$(INSURANCE_HUB_ROOT)" PYTHONDONTWRITEBYTECODE=1 DOCKER="$(DOCKER)" \
		"$(PYTHON)" -m scripts.go.service.image --config "$(GO_SERVICE_TOOLING_CONFIG)" build

image-check: image-build ## Build and validate Dashboard image identity, total sales, health and shutdown
	@$(MAKE) --no-print-directory _image-check-existing

_image-check-existing: python-runtime-check
	@PYTHONPATH="$(INSURANCE_HUB_ROOT)" PYTHONDONTWRITEBYTECODE=1 \
		DOCKER="$(DOCKER)" "$(PYTHON)" -m scripts.go.service.image \
		--config "$(GO_SERVICE_TOOLING_CONFIG)" inspect
	@PYTHONPATH="$(INSURANCE_HUB_ROOT)" PYTHONDONTWRITEBYTECODE=1 \
		DOCKER="$(DOCKER)" "$(PYTHON)" scripts/image_check.py

deploy-render: ## Render one Dashboard overlay without applying it
	@PYTHONPATH="$(INSURANCE_HUB_ROOT)" PYTHONDONTWRITEBYTECODE=1 KUBECTL="$(KUBECTL)" \
		"$(PYTHON)" scripts/dashboard_deployment_checks.py render

deploy-check: python-runtime-check ## Validate isolated Dashboard overlays and Java preservation
	@PYTHONPATH="$(INSURANCE_HUB_ROOT)" PYTHONDONTWRITEBYTECODE=1 KUBECTL="$(KUBECTL)" \
		"$(PYTHON)" scripts/dashboard_deployment_checks.py check

deploy-preflight deploy-status image-load deploy deploy-verify stop: python-runtime-check
	@PYTHONPATH="$(INSURANCE_HUB_ROOT)" PYTHONDONTWRITEBYTECODE=1 \
		KUBECTL="$(KUBECTL)" DOCKER="$(DOCKER)" "$(PYTHON)" scripts/deployment.py '$@'

deploy-test-tooling: python-toolchain-check ## Exercise fail-closed Dashboard deployment guards
	@PYTHONPATH="$(INSURANCE_HUB_ROOT)" PYTHONDONTWRITEBYTECODE=1 \
		"$(PYTHON)" -m pytest -q scripts/test_deployment.py

deploy-test: python-toolchain-check image-build ## Exercise Dashboard in an owned disposable Kind namespace
	@PYTHONPATH="$(INSURANCE_HUB_ROOT)" PYTHONDONTWRITEBYTECODE=1 IMAGE_REF="$(IMAGE_REF)" \
		"$(PYTHON)" -m pytest -q -m integration scripts/test_disposable_deployment.py

python-tooling-format-check: python-toolchain-check
	@cd "$(CURDIR)" && RUFF_CACHE_DIR="$(TOOLS_DIR)/ruff-cache" \
		PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m ruff format --check \
		--config "$(PYTHON_REPOSITORY_ROOT)/pyproject.toml" -- $(PYTHON_TOOLING_FILES)

python-tooling-lint: python-toolchain-check
	@cd "$(CURDIR)" && RUFF_CACHE_DIR="$(TOOLS_DIR)/ruff-cache" \
		PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m ruff check \
		--config "$(PYTHON_REPOSITORY_ROOT)/pyproject.toml" -- $(PYTHON_TOOLING_FILES)

python-tooling-lint-fix: python-toolchain-check
	@cd "$(CURDIR)" && RUFF_CACHE_DIR="$(TOOLS_DIR)/ruff-cache" \
		PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m ruff check --fix \
		--config "$(PYTHON_REPOSITORY_ROOT)/pyproject.toml" -- $(PYTHON_TOOLING_FILES)

python-tooling-test-fast: python-toolchain-check
	@cd "$(CURDIR)" && PYTHONPATH="$(INSURANCE_HUB_ROOT):$(CURDIR)/scripts" \
		PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m pytest -q -p no:cacheprovider \
		-c "$(PYTHON_REPOSITORY_ROOT)/pyproject.toml" --rootdir "$(CURDIR)" \
		-m 'not integration and not acceptance' $(PYTHON_FAST_TEST_FILES)

python-check: python-tooling-format-check python-tooling-lint python-tooling-test-fast

bootstrap-go-tools bootstrap-tools: python-toolchain-setup

update-deps:
	GOWORK=off $(GO) get \
		buf.build/go/protovalidate@$(PROTOVALIDATE_GO_VERSION) \
		buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go@$(PROTOVALIDATE_SCHEMA_GO_VERSION) \
		github.com/elastic/go-elasticsearch/v8@$(ELASTICSEARCH_CLIENT_VERSION) \
		github.com/grpc-ecosystem/grpc-gateway/v2@$(GRPC_GATEWAY_VERSION) \
		go.opentelemetry.io/otel@$(OTEL_VERSION) \
		go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc@$(OTEL_GRPC_VERSION) \
		go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp@$(OTEL_HTTP_VERSION) \
		go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc@$(OTEL_VERSION) \
		go.opentelemetry.io/otel/sdk@$(OTEL_VERSION) \
		go.opentelemetry.io/otel/sdk/metric@$(OTEL_VERSION) \
		go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc@$(OTEL_VERSION) \
		google.golang.org/grpc@$(GRPC_GO_VERSION) \
		google.golang.org/protobuf@$(PROTOBUF_GO_VERSION) \
		google.golang.org/genproto/googleapis/api@$(GENPROTO_GOOGLEAPIS_API_VERSION) \
		golang.org/x/net@$(X_NET_VERSION)
	GOWORK=off $(GO) get github.com/igor-baiborodine/insurance-hub/libs/servicekit@$(SERVICEKIT_VERSION)
	GOWORK=off $(GO) mod tidy

check-go-pr: INTEGRATION_COVERAGE_FILE = integration-coverage.txt
check-go-pr: verify-tools format-check format-proto-check lint lint-proto coverage-check test-integration \
	test-elasticsearch-integration build \
	check-deps check-proto-drift check-proto-breaking check-gateway-contract vulncheck python-check check-servicekit

check-gateway-contract: python-runtime-check
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" scripts/check_gateway_contract.py \
		--contract testdata/http-contract.json \
		--swagger gen/openapiv2/dashboard/v1/dashboard_service.swagger.json

check-servicekit:
	@case "$${GOWORK:-}" in ''|off) ;; *) echo 'check-servicekit: workspace override rejected' >&2; exit 1;; esac
	@case " $${GOFLAGS:-} " in *-modfile*) echo 'check-servicekit: alternate module file rejected' >&2; exit 1;; esac
	@test "$$(GOWORK=off $(GO) list -mod=readonly -m -f '{{.Version}}{{if .Replace}} replaced{{end}}' github.com/igor-baiborodine/insurance-hub/libs/servicekit)" = "$(SERVICEKIT_VERSION)" || { echo 'check-servicekit: selected version or source differs' >&2; exit 1; }
	@GOWORK=off $(GO) mod verify
	@echo "check-servicekit: selected public servicekit $(SERVICEKIT_VERSION) with verified modules"

check-candidate-servicekit:
	@test -n "$${SERVICEKIT_CANDIDATE_LIBRARY:-}" && test -d "$$SERVICEKIT_CANDIDATE_LIBRARY" || { echo 'check-candidate-servicekit: candidate library missing' >&2; exit 1; }
	@test "$$(GOWORK=off $(GO) list -m -f '{{.Replace.Dir}}' github.com/igor-baiborodine/insurance-hub/libs/servicekit)" = "$$SERVICEKIT_CANDIDATE_LIBRARY" || { echo 'check-candidate-servicekit: wrong library source selected' >&2; exit 1; }
	@echo 'check-candidate-servicekit: selected verified candidate directory'

check: check-go-pr

test-integration: ## Exercise real Dashboard HTTP-to-RPC composition and lifecycle on owned listeners
	@if [ -n "$(strip $(INTEGRATION_COVERAGE_FILE))" ]; then \
		mkdir -p "$(dir $(INTEGRATION_COVERAGE_FILE))"; \
	fi
	GOWORK=off $(GO) test -mod=readonly -count=1 -race ./internal/service \
		$(if $(strip $(INTEGRATION_COVERAGE_FILE)),-covermode=atomic -coverprofile="$(INTEGRATION_COVERAGE_FILE)")

test-elasticsearch-integration: python-runtime-check ## Exercise total-sales against owned disposable Elasticsearch
	@DOCKER="$(DOCKER)" GO="$(GO)" ELASTICSEARCH_IMAGE="$(ELASTICSEARCH_IMAGE)" \
		ELASTICSEARCH_INTEGRATION_COVERAGE_FILE="$(ELASTICSEARCH_INTEGRATION_COVERAGE_FILE)" \
		PYTHONPATH="$(INSURANCE_HUB_ROOT)" PYTHONDONTWRITEBYTECODE=1 \
		"$(PYTHON)" scripts/elasticsearch_integration.py

shared-fast-build:
	@if [ -n "$$SERVICEKIT_CONTEXT_SOCKET" ]; then \
		SERVICEKIT_PHASE=build PYTHONPATH="$(INSURANCE_HUB_ROOT)/scripts/go" "$(PYTHON)" "$(INSURANCE_HUB_ROOT)/scripts/go/shared_cli.py" phase; \
	else $(MAKE) --no-print-directory build; fi

shared-fast-unit:
	@if [ -n "$$SERVICEKIT_CONTEXT_SOCKET" ]; then \
		SERVICEKIT_PHASE=unit PYTHONPATH="$(INSURANCE_HUB_ROOT)/scripts/go" "$(PYTHON)" "$(INSURANCE_HUB_ROOT)/scripts/go/shared_cli.py" phase; \
	else $(MAKE) --no-print-directory test; fi

shared-candidate-format:
shared-candidate-lint:
shared-candidate-race:
shared-candidate-dependency:
shared-candidate-vulnerability:
shared-candidate-format shared-candidate-lint shared-candidate-race shared-candidate-dependency shared-candidate-vulnerability:
	@phase="$(@:shared-candidate-%=%)"; \
	if [ -n "$$SERVICEKIT_CONTEXT_SOCKET" ]; then \
		SERVICEKIT_PHASE="$$phase" PYTHONPATH="$(INSURANCE_HUB_ROOT)/scripts/go" "$(PYTHON)" "$(INSURANCE_HUB_ROOT)/scripts/go/shared_cli.py" phase; \
	else case "$$phase" in format) target=format-check;; race) target=test-race;; dependency) target=check-deps;; vulnerability) target=vulncheck;; *) target="$$phase";; esac; \
		$(MAKE) --no-print-directory "$$target"; fi

shared-candidate-generated:
shared-candidate-breaking:
shared-candidate-integration:
shared-candidate-image:
shared-candidate-generated shared-candidate-breaking shared-candidate-integration shared-candidate-image:
	@phase="$(@:shared-candidate-%=%)"; \
	if [ -n "$$SERVICEKIT_CONTEXT_SOCKET" ]; then \
		PYTHONPATH="$(INSURANCE_HUB_ROOT)" PYTHONDONTWRITEBYTECODE=1 \
			"$(PYTHON)" -m scripts.go.service.shared_phase \
			--config "$(GO_SERVICE_TOOLING_CONFIG)" "$$phase"; \
	else case "$$phase" in generated) target=check-proto-drift;; breaking) target=check-proto-breaking;; integration) target=test-integration;; image) target=image-check;; esac; \
		$(MAKE) --no-print-directory "$$target"; fi


test-tooling: verify-tools python-toolchain-check
	@MODULE_ROOT="$(CURDIR)" INSURANCE_HUB_ROOT="$(INSURANCE_HUB_ROOT)" \
		COMMON_GO_TOOLING_DIR="$(COMMON_GO_TOOLING_DIR)" \
		MAKE_COMMAND="$(MAKE)" TOOLS_BIN="$(TOOLS_BIN)" \
		GO="$(GO)" UV="$(UV)" \
		COMMON_GO_TOOLING_PYTHON="$(COMMON_GO_TOOLING_PYTHON)" \
		PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" ./scripts/tooling_runner.py
	@$(MAKE) --no-print-directory test-coverage-tooling


run:
	GOWORK=off $(GO) run -mod=readonly ./cmd/server


.PHONY: help
help: ## Show the explicit setup and validation entry points without installing tools
	@printf '%s\n' 'Go service commands (see README.md for the full interface):' \
		'  make bootstrap-tools  Install pinned local tools explicitly' \
		'  make check            Validate the module without source mutation' \
		'  make run              Start the service with environment configuration'
