.DEFAULT_GOAL := help

GO ?= go
DOCKER ?= docker
INSURANCE_HUB_ROOT ?= ../..
INSURANCE_HUB_ROOT := $(abspath $(INSURANCE_HUB_ROOT))
export INSURANCE_HUB_ROOT

include $(INSURANCE_HUB_ROOT)/scripts/python/toolchain.mk

TOOLS_DIR := $(CURDIR)/.tools
TOOLS_BIN := $(TOOLS_DIR)/bin
BUF := $(TOOLS_BIN)/buf
PROTOC_GEN_GO := $(TOOLS_BIN)/protoc-gen-go
PROTOC_GEN_GO_GRPC := $(TOOLS_BIN)/protoc-gen-go-grpc
GOLANGCI_LINT := $(TOOLS_BIN)/golangci-lint
GOVULNCHECK := $(TOOLS_BIN)/govulncheck
GOLANGCI_CONFIG := .golangci.yml

GO_VERSION := 1.27.3
BUF_VERSION := v1.73.0
PROTOC_GEN_GO_VERSION := v1.36.12
PROTOC_GEN_GO_GRPC_VERSION := v1.6.2
GOLANGCI_LINT_VERSION := v2.14.0
GOVULNCHECK_VERSION := v1.8.0
OTEL_VERSION := v1.46.0
OTEL_B3_VERSION := v1.46.0
SERVICEKIT_VERSION := v1.0.1
ELASTICSEARCH_CLIENT_VERSION := v8.19.7
ELASTICSEARCH_IMAGE := docker.elastic.co/elasticsearch/elasticsearch:8.19.6@sha256:bfb2108076735fbfc5dcd5d49f12f5aa93fab9759034c55f7da3b4345606c903
HEAD_REVISION := $(shell git -C $(CURDIR) rev-parse --verify HEAD 2>/dev/null)
HEAD_CREATED := $(shell git -C $(CURDIR) show -s --format=%cI HEAD 2>/dev/null)
HEAD_VERSION := git-$(shell git -C $(CURDIR) rev-parse --short=12 HEAD 2>/dev/null)
IMAGE_NAME ?= insurance-hub-policy-search-api
SOURCE_REVISION ?= $(HEAD_REVISION)
SOURCE_CREATED ?= $(HEAD_CREATED)
SOURCE_REPOSITORY ?= https://github.com/igor-baiborodine/insurance-hub
SOURCE_VERSION ?= $(HEAD_VERSION)
IMAGE_TAG ?= $(SOURCE_VERSION)
IMAGE_REF ?= $(IMAGE_NAME):$(IMAGE_TAG)
TARGET_PLATFORM ?= linux/amd64
TEST_PACKAGES ?= ./...
INTEGRATION_COVERAGE_FILE ?= integration-coverage.txt
INTEGRATION_SUITE ?= foundation
GO_SERVICE_TOOLING_CONFIG := $(CURDIR)/go-service-tooling.json

COMMON_GO_TOOLING_DIR ?= $(INSURANCE_HUB_ROOT)/scripts/go/module
COMMON_GO_TOOLING_DIR := $(abspath $(COMMON_GO_TOOLING_DIR))
include $(COMMON_GO_TOOLING_DIR)/common.mk

.PHONY: update-deps check-servicekit check-candidate-servicekit check-api-scope \
	test-integration image-build image-check test-tooling \
	_image-check-existing \
	check-go-pr check run help \
	shared-fast-build shared-fast-unit shared-candidate-format shared-candidate-lint \
	shared-candidate-race shared-candidate-dependency shared-candidate-vulnerability \
	shared-candidate-generated shared-candidate-breaking shared-candidate-integration \
	shared-candidate-image

export IMAGE_NAME IMAGE_TAG IMAGE_REF SOURCE_CREATED SOURCE_REPOSITORY SOURCE_REVISION \
	SOURCE_VERSION TARGET_PLATFORM

update-deps: ## Pin runtime dependencies and tidy only this module's manifests
	GOWORK=off $(GO) get \
		github.com/igor-baiborodine/insurance-hub/libs/servicekit@$(SERVICEKIT_VERSION) \
		github.com/elastic/go-elasticsearch/v8@$(ELASTICSEARCH_CLIENT_VERSION) \
		go.opentelemetry.io/otel/sdk/metric@$(OTEL_VERSION) \
		go.opentelemetry.io/contrib/propagators/b3@$(OTEL_B3_VERSION)
	GOWORK=off $(GO) mod tidy

check-servicekit: ## Verify published Servicekit version and module checksums
	@case "$${GOWORK:-}" in ''|off) ;; *) echo 'check-servicekit: workspace override rejected' >&2; exit 1;; esac
	@case " $${GOFLAGS:-} " in *-modfile*) echo 'check-servicekit: alternate module file rejected' >&2; exit 1;; esac
	@test "$$(GOWORK=off $(GO) list -mod=readonly -m -f '{{.Version}}{{if .Replace}} replaced{{end}}' github.com/igor-baiborodine/insurance-hub/libs/servicekit)" = "$(SERVICEKIT_VERSION)" || { echo 'check-servicekit: selected version or source differs' >&2; exit 1; }
	@GOWORK=off $(GO) mod verify
	@echo "check-servicekit: selected public servicekit $(SERVICEKIT_VERSION) with verified modules"

check-candidate-servicekit: ## Verify the controlled shared-library candidate source
	@test -n "$${SERVICEKIT_CANDIDATE_LIBRARY:-}" && test -d "$$SERVICEKIT_CANDIDATE_LIBRARY" || { echo 'check-candidate-servicekit: candidate library missing' >&2; exit 1; }
	@test "$$(GOWORK=off $(GO) list -m -f '{{.Replace.Dir}}' github.com/igor-baiborodine/insurance-hub/libs/servicekit)" = "$$SERVICEKIT_CANDIDATE_LIBRARY" || { echo 'check-candidate-servicekit: wrong library source selected' >&2; exit 1; }
	@echo 'check-candidate-servicekit: selected verified candidate directory'

check-api-scope: ## Reject generated RPC and separately maintained OpenAPI contracts
	@test -z "$$(find api gen -type f \( -name '*.proto' -o -name 'openapi.yaml' -o -name '*.pb.go' \) 2>/dev/null)" || { echo 'check-api-scope: policy search owns direct HTTP without a generated or OpenAPI contract' >&2; exit 1; }

test-integration: python-runtime-check ## Run the foundation suite against owned disposable Elasticsearch
	@DOCKER="$(DOCKER)" GO="$(GO)" ELASTICSEARCH_IMAGE="$(ELASTICSEARCH_IMAGE)" \
		INTEGRATION_SUITE="$(INTEGRATION_SUITE)" \
		INTEGRATION_COVERAGE_FILE="$(INTEGRATION_COVERAGE_FILE)" \
		PYTHONPATH="$(INSURANCE_HUB_ROOT)" PYTHONDONTWRITEBYTECODE=1 \
		$(PYTHON) scripts/integration_test.py

image-build: python-runtime-check ## Build the pinned linux/amd64 policy-search runtime image
	@DOCKER="$(DOCKER)" PYTHONPATH="$(INSURANCE_HUB_ROOT)" PYTHONDONTWRITEBYTECODE=1 \
		$(PYTHON) -m scripts.go.service.image --config "$(GO_SERVICE_TOOLING_CONFIG)" build

image-check: image-build ## Validate policy-search image identity, nonroot startup, liveness, and shutdown
	@$(MAKE) --no-print-directory _image-check-existing

_image-check-existing: python-runtime-check
	@DOCKER="$(DOCKER)" PYTHONPATH="$(INSURANCE_HUB_ROOT)" PYTHONDONTWRITEBYTECODE=1 \
		$(PYTHON) -m scripts.go.service.image --config "$(GO_SERVICE_TOOLING_CONFIG)" check

test-tooling: python-toolchain-check ## Exercise process, integration, and image tooling failures
	@cd "$(INSURANCE_HUB_ROOT)" && PYTHONPATH="$(INSURANCE_HUB_ROOT)" \
		PYTHONDONTWRITEBYTECODE=1 $(PYTHON) -m pytest -q \
		services/policy-search-service/scripts/test_policy_tooling.py

check-go-pr: INTEGRATION_COVERAGE_FILE = integration-coverage.txt
check-go-pr: verify-tools format-check lint coverage-check test-integration build check-api-scope \
	check-deps vulncheck check-servicekit ## Validate policy-search code and real Elasticsearch

check: check-go-pr ## Alias for the current service foundation gate

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
		PYTHONPATH="$(INSURANCE_HUB_ROOT)" "$(PYTHON)" -m scripts.go.service.shared_phase \
			--config "$(GO_SERVICE_TOOLING_CONFIG)" "$$phase"; \
	else case "$$phase" in generated|breaking) target=check-api-scope;; integration) target=test-integration;; image) target=image-check;; esac; \
		$(MAKE) --no-print-directory "$$target"; fi

run: ## Run the HTTP service using readonly standalone modules
	GOWORK=off $(GO) run -mod=readonly ./cmd/server

help: ## List supported service targets without installing tools
	@printf '%s\n' \
		'policy-search-service: make update-deps, format, format-check, lint, test, test-race, build' \
		'make test-integration image-check test-tooling' \
		'make check-go-pr validates code, disposable Elasticsearch, and dependencies'
