.DEFAULT_GOAL := help

GO ?= go
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
TEST_PACKAGES ?= ./...

COMMON_GO_TOOLING_DIR ?= $(INSURANCE_HUB_ROOT)/scripts/go/module
COMMON_GO_TOOLING_DIR := $(abspath $(COMMON_GO_TOOLING_DIR))
include $(COMMON_GO_TOOLING_DIR)/common.mk

.PHONY: update-deps check-servicekit check-go-pr check run help \
	shared-fast-build shared-fast-unit shared-candidate-format shared-candidate-lint \
	shared-candidate-race shared-candidate-dependency shared-candidate-vulnerability \
	shared-candidate-generated shared-candidate-breaking shared-candidate-integration \
	shared-candidate-image

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

check-go-pr: verify-tools format-check lint coverage-check build check-deps vulncheck check-servicekit ## Validate the current service foundation without source mutation

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
	@echo '$@: HTTP contract, real Elasticsearch integration, and image gates pending issue-229 Step 4' >&2
	@exit 1

run: ## Run the HTTP service using readonly standalone modules
	GOWORK=off $(GO) run -mod=readonly ./cmd/server

help: ## List supported service targets without installing tools
	@printf '%s\n' \
		'policy-search-service: make update-deps, format, format-check, lint, test, test-race, build, check-servicekit' \
		'check-go-pr/check validate the current foundation; HTTP contract and integration gates arrive in issue-229 Step 4'
