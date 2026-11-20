.DEFAULT_GOAL := help

MVEL_ROOT := $(abspath ../..)
include $(MVEL_ROOT)/scripts/python/toolchain.mk

GO ?= go
GO_VERSION := 1.27.4
GOLANGCI_LINT_VERSION := v2.14.0
GOVULNCHECK_VERSION := v1.8.0
TOOLS_BIN ?= $(CURDIR)/.tools/bin
TEST_PACKAGES ?= ./...
FORMAT_SCOPE ?= changed
export GO GO_VERSION GOLANGCI_LINT_VERSION GOVULNCHECK_VERSION TOOLS_BIN TEST_PACKAGES FORMAT_SCOPE
export MODULE_ROOT := $(CURDIR)
export PYTHON
MVEL_TOOL := $(MVEL_ROOT)/scripts/go/library_tooling.py

.PHONY: help bootstrap-go-tools verify-tools check-boundaries format format-check lint test test-race \
	coverage build check-deps update-deps vulncheck check-go-pr check

help: ## Show the bounded MVEL library commands
	@printf '%s\n' 'mvel: bootstrap-go-tools | format | coverage | check-go-pr | update-deps' \
		'Checks require pinned tools; no server, schema, database or image setup.'

bootstrap-go-tools: python-runtime-check ## Install the pinned formatter, linter and scanner
	@"$(PYTHON)" "$(MVEL_TOOL)" bootstrap

verify-tools: python-runtime-check ## Verify the Go and module tool pins
	@"$(PYTHON)" "$(MVEL_TOOL)" verify

format format-check lint test test-race build check-deps update-deps vulncheck check-boundaries: python-runtime-check
	@"$(PYTHON)" "$(MVEL_TOOL)" "$@"

coverage: python-runtime-check ## Run race-enabled tests and write ignored coverage.txt
	@"$(PYTHON)" "$(MVEL_TOOL)" coverage

check-go-pr: verify-tools format-check lint test-race build check-deps vulncheck check-boundaries ## Validate the complete MVEL module without source mutation

check: check-go-pr ## Alias for the complete library gate
