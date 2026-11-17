.DEFAULT_GOAL := help

GO ?= go
DOCKER ?= docker
INSURANCE_HUB_ROOT ?= ../..
INSURANCE_HUB_ROOT := $(abspath $(INSURANCE_HUB_ROOT))
export INSURANCE_HUB_ROOT
TOOLING_REPOSITORY_ROOT ?= $(INSURANCE_HUB_ROOT)
COMMON_GO_TOOLING_DIR := $(INSURANCE_HUB_ROOT)/scripts/go/module
PYTHON_TOOLING_FILES := services/chat-service/scripts/test_tooling.py
PYTHON_FAST_TEST_FILES :=

include $(INSURANCE_HUB_ROOT)/scripts/python/toolchain.mk

TOOLS_DIR := $(CURDIR)/.tools
TOOLS_BIN := $(TOOLS_DIR)/bin
GOLANGCI_LINT := $(TOOLS_BIN)/golangci-lint
GOVULNCHECK := $(TOOLS_BIN)/govulncheck
GOLANGCI_CONFIG := .golangci.yml

GO_VERSION := 1.27.4
GOLANGCI_LINT_VERSION := v2.14.0
GOVULNCHECK_VERSION := v1.8.0
SERVICEKIT_VERSION := v1.1.0
BUILD_EXECUTABLES := server
TEST_PACKAGES ?= ./...

HEAD_REVISION := $(shell git -C $(INSURANCE_HUB_ROOT) rev-parse --verify HEAD 2>/dev/null)
HEAD_SHORT := $(shell git -C $(INSURANCE_HUB_ROOT) rev-parse --short=12 HEAD 2>/dev/null)
HEAD_CREATED := $(shell git -C $(INSURANCE_HUB_ROOT) show -s --format=%cI HEAD 2>/dev/null)
HEAD_VERSION := git-$(HEAD_SHORT)
IMAGE_NAME ?= insurance-hub-chat-api
SOURCE_REVISION ?= $(HEAD_REVISION)
SOURCE_CREATED ?= $(HEAD_CREATED)
SOURCE_REPOSITORY ?= https://github.com/igor-baiborodine/insurance-hub
SOURCE_VERSION ?= $(HEAD_VERSION)
IMAGE_TAG ?= 1.0.0-local.$(HEAD_SHORT)
IMAGE_REF ?= $(IMAGE_NAME):$(IMAGE_TAG)
TARGET_PLATFORM ?= linux/amd64
GO_SERVICE_TOOLING_CONFIG := $(CURDIR)/go-service-tooling.json

export IMAGE_NAME IMAGE_TAG IMAGE_REF SOURCE_CREATED SOURCE_REPOSITORY SOURCE_REVISION SOURCE_VERSION TARGET_PLATFORM

.PHONY: bootstrap-go-tools bootstrap-tools verify-tools format format-check update-deps check-deps \
	lint test test-race test-integration build vulncheck check-go-pr check test-tooling \
	check-servicekit check-candidate-servicekit run dockerfile-generate dockerfile-check \
	image-build image-check _image-check-existing python-tooling-format-check \
	python-tooling-lint python-tooling-test-fast python-check shared-fast-build shared-fast-unit \
	shared-candidate-format shared-candidate-lint shared-candidate-race \
	shared-candidate-dependency shared-candidate-vulnerability shared-candidate-generated \
	shared-candidate-breaking shared-candidate-integration shared-candidate-image \
	shared-candidate-copy candidate-generated-inapplicable candidate-breaking-inapplicable \
	candidate-copy-inapplicable help

bootstrap-go-tools:
	mkdir -p "$(TOOLS_BIN)"
	GOWORK=off GOBIN="$(TOOLS_BIN)" $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	GOWORK=off GOBIN="$(TOOLS_BIN)" $(GO) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	@$(MAKE) --no-print-directory verify-tools

bootstrap-tools: python-toolchain-setup bootstrap-go-tools

verify-tools: python-runtime-check
	@test "$$(GOWORK=off $(GO) env GOVERSION)" = "go$(GO_VERSION)" || { echo 'verify-tools: wrong Go version' >&2; exit 1; }
	@test -x "$(GOLANGCI_LINT)" || { echo "verify-tools: golangci-lint is missing; run 'make bootstrap-tools'" >&2; exit 1; }
	@test -x "$(GOVULNCHECK)" || { echo "verify-tools: govulncheck is missing; run 'make bootstrap-tools'" >&2; exit 1; }
	@"$(GOLANGCI_LINT)" version | grep -F "golangci-lint has version $(patsubst v%,%,$(GOLANGCI_LINT_VERSION)) " >/dev/null
	@"$(GOVULNCHECK)" -version | grep -F "Scanner: govulncheck@$(GOVULNCHECK_VERSION)" >/dev/null
	@"$(GOLANGCI_LINT)" config verify --config "$(GOLANGCI_CONFIG)"
	@echo 'verify-tools: verified Go and the two module-owned tools'

format: verify-tools
	@MODULE_ROOT="$(CURDIR)" GOWORK=off GOLANGCI_LINT="$(GOLANGCI_LINT)" \
		GOLANGCI_CONFIG="$(GOLANGCI_CONFIG)" FORMAT_FILES_SET="$(if $(filter undefined,$(origin FORMAT_FILES)),0,1)" \
		PYTHON="$(PYTHON)" "$(COMMON_GO_TOOLING_DIR)/format-go.sh" format

format-check: verify-tools
	@MODULE_ROOT="$(CURDIR)" GOWORK=off GOLANGCI_LINT="$(GOLANGCI_LINT)" \
		GOLANGCI_CONFIG="$(GOLANGCI_CONFIG)" FORMAT_FILES_SET="$(if $(filter undefined,$(origin FORMAT_FILES)),0,1)" \
		PYTHON="$(PYTHON)" "$(COMMON_GO_TOOLING_DIR)/format-go.sh" check

update-deps:
	GOWORK=off $(GO) get github.com/igor-baiborodine/insurance-hub/libs/servicekit@$(SERVICEKIT_VERSION)
	GOWORK=off $(GO) mod tidy

check-deps: verify-tools
	@MODULE_ROOT="$(CURDIR)" COMMON_GO_TOOLING_DIR="$(COMMON_GO_TOOLING_DIR)" \
		GO="$(GO)" MAKE_COMMAND="$(MAKE)" PYTHON="$(PYTHON)" \
		"$(COMMON_GO_TOOLING_DIR)/check-deps.sh"

lint: verify-tools
	GOWORK=off $(GOLANGCI_LINT) run --config "$(GOLANGCI_CONFIG)" ./...

test:
	@test -n "$(strip $(TEST_PACKAGES))" || { echo 'TEST_PACKAGES must not be empty' >&2; exit 1; }
	GOWORK=off $(GO) test -mod=readonly -count=1 $(TEST_PACKAGES)

test-race:
	GOWORK=off $(GO) test -mod=readonly -count=1 -race ./...

test-integration:
	GOWORK=off $(GO) test -mod=readonly -count=1 -race ./internal/service \
		-run '^TestRunServesManagementAndExactBusinessNotFound$$'

build: python-runtime-check
	@MODULE_ROOT="$(CURDIR)" GO="$(GO)" BUILD_EXECUTABLES="$(BUILD_EXECUTABLES)" \
		PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" "$(COMMON_GO_TOOLING_DIR)/module_tooling.py" build

vulncheck: verify-tools
	GOWORK=off GOFLAGS=-mod=readonly $(GOVULNCHECK) ./...

python-tooling-format-check: python-toolchain-check
	@cd "$(PYTHON_REPOSITORY_ROOT)" && RUFF_CACHE_DIR="$(TOOLS_DIR)/ruff-cache" \
		PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m ruff format --check \
		--config "$(PYTHON_REPOSITORY_ROOT)/pyproject.toml" -- $(PYTHON_TOOLING_FILES)

python-tooling-lint: python-toolchain-check
	@cd "$(PYTHON_REPOSITORY_ROOT)" && RUFF_CACHE_DIR="$(TOOLS_DIR)/ruff-cache" \
		PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m ruff check \
		--config "$(PYTHON_REPOSITORY_ROOT)/pyproject.toml" -- $(PYTHON_TOOLING_FILES)

python-tooling-test-fast:
	@true

python-check: python-tooling-format-check python-tooling-lint python-tooling-test-fast

dockerfile-generate: python-runtime-check
	@PYTHONPATH="$(INSURANCE_HUB_ROOT)" PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" \
		-m scripts.go.service.dockerfile --config "$(GO_SERVICE_TOOLING_CONFIG)" \
		--repository-root "$(TOOLING_REPOSITORY_ROOT)" generate

dockerfile-check: python-runtime-check
	@PYTHONPATH="$(INSURANCE_HUB_ROOT)" PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" \
		-m scripts.go.service.dockerfile --config "$(GO_SERVICE_TOOLING_CONFIG)" \
		--repository-root "$(TOOLING_REPOSITORY_ROOT)" check

image-build: dockerfile-check python-runtime-check
	@PYTHONPATH="$(INSURANCE_HUB_ROOT)" PYTHONDONTWRITEBYTECODE=1 DOCKER="$(DOCKER)" \
		"$(PYTHON)" -m scripts.go.service.image --config "$(GO_SERVICE_TOOLING_CONFIG)" build

image-check: image-build
	@$(MAKE) --no-print-directory _image-check-existing

_image-check-existing: dockerfile-check python-runtime-check
	@PYTHONPATH="$(INSURANCE_HUB_ROOT)" PYTHONDONTWRITEBYTECODE=1 DOCKER="$(DOCKER)" \
		"$(PYTHON)" -m scripts.go.service.image --config "$(GO_SERVICE_TOOLING_CONFIG)" check

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

candidate-generated-inapplicable:
	@echo 'generated phase: inapplicable because Chat C01 owns no generated source or contract'

candidate-breaking-inapplicable:
	@echo 'breaking phase: inapplicable because Chat C01 publishes no protobuf or gateway contract'

candidate-copy-inapplicable:
	@echo 'copy phase: inapplicable because Chat owns its final source and retains provenance only'

shared-fast-build:
	@if [ -n "$$SERVICEKIT_CONTEXT_SOCKET" ]; then SERVICEKIT_PHASE=build PYTHONPATH="$(INSURANCE_HUB_ROOT)/scripts/go" "$(PYTHON)" "$(INSURANCE_HUB_ROOT)/scripts/go/shared_cli.py" phase; else $(MAKE) --no-print-directory build; fi

shared-fast-unit:
	@if [ -n "$$SERVICEKIT_CONTEXT_SOCKET" ]; then SERVICEKIT_PHASE=unit PYTHONPATH="$(INSURANCE_HUB_ROOT)/scripts/go" "$(PYTHON)" "$(INSURANCE_HUB_ROOT)/scripts/go/shared_cli.py" phase; else $(MAKE) --no-print-directory test; fi

shared-candidate-format shared-candidate-lint shared-candidate-race shared-candidate-dependency shared-candidate-vulnerability:
	@phase="$(@:shared-candidate-%=%)"; if [ -n "$$SERVICEKIT_CONTEXT_SOCKET" ]; then SERVICEKIT_PHASE="$$phase" PYTHONPATH="$(INSURANCE_HUB_ROOT)/scripts/go" "$(PYTHON)" "$(INSURANCE_HUB_ROOT)/scripts/go/shared_cli.py" phase; else case "$$phase" in format) target=format-check;; race) target=test-race;; dependency) target=check-deps;; vulnerability) target=vulncheck;; *) target="$$phase";; esac; $(MAKE) --no-print-directory "$$target"; fi

shared-candidate-generated shared-candidate-breaking shared-candidate-integration shared-candidate-copy shared-candidate-image:
	@phase="$(@:shared-candidate-%=%)"; if [ -n "$$SERVICEKIT_CONTEXT_SOCKET" ]; then PYTHONPATH="$(INSURANCE_HUB_ROOT)" PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m scripts.go.service.shared_phase --config "$(GO_SERVICE_TOOLING_CONFIG)" "$$phase"; else case "$$phase" in generated) target=candidate-generated-inapplicable;; breaking) target=candidate-breaking-inapplicable;; integration) target=test-integration;; copy) target=candidate-copy-inapplicable;; image) target=image-check;; esac; $(MAKE) --no-print-directory "$$target"; fi

test-tooling: verify-tools python-toolchain-check
	@MODULE_ROOT="$(CURDIR)" INSURANCE_HUB_ROOT="$(INSURANCE_HUB_ROOT)" \
		COMMON_GO_TOOLING_DIR="$(COMMON_GO_TOOLING_DIR)" MAKE_COMMAND="$(MAKE)" \
		TOOLS_BIN="$(TOOLS_BIN)" GO="$(GO)" UV="$(UV)" PYTHON="$(PYTHON)" \
		./scripts/test-tooling.sh

check-go-pr: verify-tools dockerfile-check format-check lint test-race test-integration build check-deps vulncheck python-check check-servicekit

check: check-go-pr

run:
	GOWORK=off $(GO) run -mod=readonly ./cmd/server

help:
	@printf '%s\n' 'Chat service commands:' \
		'  make bootstrap-tools  Install the pinned Go and Python tools explicitly' \
		'  make check            Validate the owned module, runtime and image source' \
		'  make test-tooling     Exercise focused controlled tooling defects' \
		'  make image-check      Build and inspect the nonroot Chat image' \
		'  make run              Start the two-listener service'
