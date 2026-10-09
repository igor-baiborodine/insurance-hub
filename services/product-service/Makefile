.DEFAULT_GOAL := bootstrap-tools

GO ?= go

TOOLS_DIR := $(CURDIR)/.tools
TOOLS_BIN := $(TOOLS_DIR)/bin
GOLANGCI_LINT := $(TOOLS_BIN)/golangci-lint
GOVULNCHECK := $(TOOLS_BIN)/govulncheck
GOLANGCI_CONFIG := .golangci.yml

GO_VERSION := 1.27.2
GOLANGCI_LINT_VERSION := v2.14.0
GOVULNCHECK_VERSION := v1.8.0
OTEL_VERSION := v1.46.0
X_NET_VERSION := v0.60.0

.PHONY: bootstrap-tools verify-tools format format-check update-deps check-deps \
	lint test test-race build vulncheck check run

bootstrap-tools:
	mkdir -p "$(TOOLS_BIN)"
	GOWORK=off GOBIN="$(TOOLS_BIN)" $(GO) install \
		github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	GOWORK=off GOBIN="$(TOOLS_BIN)" $(GO) install \
		golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	@$(MAKE) --no-print-directory verify-tools

verify-tools:
	@GO="$(GO)" \
		GOLANGCI_LINT="$(GOLANGCI_LINT)" \
		GOVULNCHECK="$(GOVULNCHECK)" \
		GOLANGCI_CONFIG="$(GOLANGCI_CONFIG)" \
		GO_VERSION="$(GO_VERSION)" \
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
		go.opentelemetry.io/otel@$(OTEL_VERSION) \
		go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc@$(OTEL_VERSION) \
		go.opentelemetry.io/otel/sdk@$(OTEL_VERSION) \
		go.opentelemetry.io/otel/trace@$(OTEL_VERSION) \
		golang.org/x/net@$(X_NET_VERSION)
	GOWORK=off $(GO) mod tidy

check-deps: verify-tools
	@GO="$(GO)" MAKE_COMMAND="$(MAKE)" ./scripts/check-deps.sh

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

check: verify-tools format-check lint test-race build check-deps vulncheck

run:
	GOWORK=off $(GO) run -mod=readonly ./cmd/server
