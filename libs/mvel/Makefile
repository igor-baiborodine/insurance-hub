.DEFAULT_GOAL := help

MVEL_ROOT := $(abspath ../..)
include $(MVEL_ROOT)/scripts/python/toolchain.mk

GO ?= go
GO_VERSION := 1.27.4
GOLANGCI_LINT_VERSION := v2.14.0
GOVULNCHECK_VERSION := v1.8.0
TOOLS_BIN ?= $(CURDIR)/.tools/bin
GOLANGCI_LINT := $(TOOLS_BIN)/golangci-lint
GOVULNCHECK := $(TOOLS_BIN)/govulncheck
GO_FILES := $(shell find . -type f -name '*.go' -not -path './.tools/*' -print | sort)
TEST_PACKAGES ?= ./...

.PHONY: help bootstrap-go-tools verify-tools format format-check lint test test-race build \
	check-deps update-deps vulncheck check-step2

help: ## Show the bounded MVEL library commands
	@printf '%s\n' \
		'mvel: bootstrap-go-tools | format | test-race | check-step2' \
		'Step 3 adds repository inventory, shared CI and release validation.'

bootstrap-go-tools: python-runtime-check ## Install the pinned formatter, linter and scanner
	@mkdir -p "$(TOOLS_BIN)"
	@GOWORK=off GOBIN="$(TOOLS_BIN)" "$(GO)" install \
		github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	@GOWORK=off GOBIN="$(TOOLS_BIN)" "$(GO)" install \
		golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

verify-tools: python-runtime-check ## Verify the Go and module tool pins
	@test "$$($(GO) env GOVERSION)" = "go$(GO_VERSION)"
	@test -x "$(GOLANGCI_LINT)" && "$(GOLANGCI_LINT)" version | \
		grep -F 'has version $(patsubst v%,%,$(GOLANGCI_LINT_VERSION)) ' >/dev/null
	@test -x "$(GOVULNCHECK)" && "$(GOVULNCHECK)" -version | \
		grep -F 'Scanner: govulncheck@$(GOVULNCHECK_VERSION)' >/dev/null
	@"$(GOLANGCI_LINT)" config verify --config .golangci.yml

format: verify-tools ## Format all handwritten Go source
	@"$(GOLANGCI_LINT)" fmt --config .golangci.yml -- $(GO_FILES)

format-check: verify-tools ## Check formatting without changing source
	@"$(GOLANGCI_LINT)" fmt --config .golangci.yml --diff -- $(GO_FILES)

lint: verify-tools ## Run the pinned module linter
	@GOWORK=off "$(GOLANGCI_LINT)" run --config .golangci.yml ./...

test: python-runtime-check ## Run focused library tests
	@GOWORK=off "$(GO)" test -mod=readonly -count=1 $(TEST_PACKAGES)

test-race: python-runtime-check ## Run all library tests under the race detector
	@GOWORK=off "$(GO)" test -mod=readonly -count=1 -race ./...

build: python-runtime-check ## Build all library packages
	@GOWORK=off "$(GO)" build -mod=readonly ./...

check-deps: python-runtime-check ## Check go.mod and go.sum consistency
	@GOWORK=off "$(GO)" mod tidy -diff

update-deps: python-runtime-check ## Update module dependency metadata
	@GOWORK=off "$(GO)" mod tidy

vulncheck: verify-tools ## Check reachable dependency vulnerabilities
	@GOWORK=off "$(GOVULNCHECK)" ./...

check-step2: format-check lint test-race build check-deps vulncheck ## Validate the standalone evaluator
