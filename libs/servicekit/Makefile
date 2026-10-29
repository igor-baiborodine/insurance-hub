.DEFAULT_GOAL := help

SERVICEKIT_ROOT := $(abspath ../..)
include $(SERVICEKIT_ROOT)/scripts/python/toolchain.mk

GO ?= go
GO_VERSION := 1.27.3
GOLANGCI_LINT_VERSION := v2.14.0
GOVULNCHECK_VERSION := v1.8.0
OTEL_VERSION := v1.46.0
OTEL_GRPC_VERSION := v0.71.0
TOOLS_BIN ?= $(CURDIR)/.tools/bin
TEST_PACKAGES ?= ./...
FORMAT_SCOPE ?= changed
export GO GO_VERSION GOLANGCI_LINT_VERSION GOVULNCHECK_VERSION TOOLS_BIN TEST_PACKAGES FORMAT_SCOPE
export MODULE_ROOT := $(CURDIR)
export PYTHON
SERVICEKIT_TOOL := $(SERVICEKIT_ROOT)/scripts/go/library_tooling.py

.PHONY: help bootstrap-go-tools verify-tools check-boundaries format format-check lint test test-race coverage build \
	check-deps update-deps vulncheck check-go-pr check

help: ## Show library-only setup and check interfaces
	@printf '%s\n' 'servicekit: bootstrap-go-tools | format | coverage | check-go-pr | update-deps' \
		'Checks require pinned tools; no server, protobuf, database or image setup.'

bootstrap-go-tools: python-runtime-check ## Explicitly install the two pinned library tools
	@"$(PYTHON)" "$(SERVICEKIT_TOOL)" bootstrap

verify-tools: python-runtime-check ## Verify Go and library formatter/linter/vulnerability pins
	@"$(PYTHON)" "$(SERVICEKIT_TOOL)" verify

format format-check lint test test-race build check-deps update-deps vulncheck check-boundaries: python-runtime-check
	@"$(PYTHON)" "$(SERVICEKIT_TOOL)" "$@"

coverage: python-runtime-check ## Run race-enabled tests for all library packages and write ignored coverage.txt
	@"$(PYTHON)" "$(SERVICEKIT_TOOL)" coverage

check-go-pr: verify-tools format-check lint test-race build check-deps vulncheck check-boundaries ## Validate all library packages without source mutation

check: check-go-pr ## Alias for the complete library gate
