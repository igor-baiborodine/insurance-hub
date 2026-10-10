.DEFAULT_GOAL := bootstrap-tools

GO ?= go
DOCKER ?= docker
NODE ?= node
NPM ?= npm
PYTHON ?= python3

TOOLS_DIR := $(CURDIR)/.tools
TOOLS_BIN := $(TOOLS_DIR)/bin
BUF := $(TOOLS_BIN)/buf
PROTOC_GEN_GO := $(TOOLS_BIN)/protoc-gen-go
PROTOC_GEN_GO_GRPC := $(TOOLS_BIN)/protoc-gen-go-grpc
GOLANGCI_LINT := $(TOOLS_BIN)/golangci-lint
GOVULNCHECK := $(TOOLS_BIN)/govulncheck
SQLC := $(TOOLS_BIN)/sqlc
MMDC := $(TOOLS_BIN)/mmdc
GOLANGCI_CONFIG := .golangci.yml
PROTO_SCHEMA := api/product/v1/product_service.proto
CONTRACT_BASELINE_DIR := testdata/contract-baseline
GENERATED_PROTO_FILES := gen/product/v1/product_service.pb.go gen/product/v1/product_service_grpc.pb.go

GO_VERSION := 1.27.2
BUF_VERSION := v1.73.0
PROTOC_GEN_GO_VERSION := v1.36.12
PROTOC_GEN_GO_GRPC_VERSION := v1.6.2
GOLANGCI_LINT_VERSION := v2.14.0
GOVULNCHECK_VERSION := v1.8.0
SQLC_VERSION := v1.31.1
PROTOBUF_GO_VERSION := v1.36.12
GRPC_GO_VERSION := v1.83.2
PROTOVALIDATE_GO_VERSION := v1.4.0
PROTOVALIDATE_SCHEMA_GO_VERSION := v1.36.12-20260825204119-511051f7f437.1
OTEL_VERSION := v1.46.0
OTEL_GRPC_VERSION := v0.71.0
X_NET_VERSION := v0.60.0
PGX_VERSION := v5.11.0
TESTCONTAINERS_VERSION := v0.44.0
MOBY_GO_ARCHIVE_VERSION := v0.3.0
MERMAID_CLI_VERSION := 11.12.0
POSTGRES_IMAGE := postgres:17.10-alpine@sha256:742f40ea20b9ff2ff31db5458d127452988a2164df9e17441e191f3b72252193
SQL_SCHEMA := internal/postgres/sql/schema.sql
SQL_QUERIES := internal/postgres/sql/queries.sql
GENERATED_SQL_FILES := internal/postgres/dbgen/db.go internal/postgres/dbgen/models.go \
	internal/postgres/dbgen/queries.sql.go internal/postgres/dbgen/querier.go
TEST_PACKAGES ?= ./...
COVERAGE_FILE ?= coverage.txt
INTEGRATION_SUITE ?= all
FIXTURE_SET ?= qa
DOC_FILES ?= README.md ../../docs/migration/phase-4/product-service/README.md
EXTRA_TOOL_PACKAGES := github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)
VERIFY_EXTRA_TOOL_SPECS := sqlc|$(SQLC)|$(SQLC_VERSION)|version

COMMON_GO_TOOLING_DIR ?= ../../scripts/go/module
COMMON_GO_TOOLING_DIR := $(abspath $(COMMON_GO_TOOLING_DIR))
include $(COMMON_GO_TOOLING_DIR)/common.mk

.PHONY: bootstrap-tools verify-tools format format-check update-deps check-deps \
	update-proto-deps format-proto format-proto-check lint-proto check-proto-breaking \
	gen-proto check-proto-drift gen-sql check-sql-drift lint test test-race coverage test-integration \
	build vulncheck bootstrap-doc-tools check-docs preview-docs check test-tooling run

bootstrap-tools: bootstrap-doc-tools

bootstrap-doc-tools:
	@major="$$($(NODE) -p 'process.versions.node.split(".")[0]')"; \
		if test "$$major" -lt 20; then \
			echo "Node.js >= 20 is required for Mermaid CLI" >&2; \
			exit 1; \
		fi
	@set -eu; \
		installed="$$($(MMDC) --version 2>/dev/null || true)"; \
		if test "$$installed" != "$(MERMAID_CLI_VERSION)"; then \
			mkdir -p "$(TOOLS_DIR)" "$(TOOLS_BIN)" "$(TOOLS_DIR)/puppeteer"; \
			PUPPETEER_CACHE_DIR="$(TOOLS_DIR)/puppeteer" $(NPM) install \
				--prefix "$(TOOLS_DIR)" --no-save --no-package-lock \
				"@mermaid-js/mermaid-cli@$(MERMAID_CLI_VERSION)"; \
			ln -sfn ../node_modules/.bin/mmdc "$(MMDC)"; \
		fi; \
		if ! find "$(TOOLS_DIR)/puppeteer" -type f -name chrome-headless-shell \
			-perm -u+x -print -quit 2>/dev/null | grep -q .; then \
			PUPPETEER_CACHE_DIR="$(TOOLS_DIR)/puppeteer" $(NPM) rebuild \
				--prefix "$(TOOLS_DIR)" puppeteer; \
		fi
	@test "$$($(MMDC) --version)" = "$(MERMAID_CLI_VERSION)"

check-docs:
	@test "$$($(MMDC) --version 2>/dev/null)" = "$(MERMAID_CLI_VERSION)" || { \
		echo "check-docs: Mermaid CLI $(MERMAID_CLI_VERSION) is required; run bootstrap-tools" >&2; \
		exit 1; \
	}
	@PUPPETEER_CACHE_DIR="$(TOOLS_DIR)/puppeteer" $(PYTHON) scripts/check-docs.py \
		--renderer "$(MMDC)" $(DOC_FILES)

preview-docs:
	@test "$$($(MMDC) --version 2>/dev/null)" = "$(MERMAID_CLI_VERSION)" || { \
		echo "preview-docs: Mermaid CLI $(MERMAID_CLI_VERSION) is required; run bootstrap-tools" >&2; \
		exit 1; \
	}
	rm -rf "$(TOOLS_DIR)/docs-preview"
	@PUPPETEER_CACHE_DIR="$(TOOLS_DIR)/puppeteer" $(PYTHON) scripts/check-docs.py \
		--renderer "$(MMDC)" --output-directory "$(TOOLS_DIR)/docs-preview" $(DOC_FILES)

update-deps:
	GOWORK=off $(GO) get \
		buf.build/go/protovalidate@$(PROTOVALIDATE_GO_VERSION) \
		buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go@$(PROTOVALIDATE_SCHEMA_GO_VERSION) \
		go.opentelemetry.io/otel@$(OTEL_VERSION) \
		go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc@$(OTEL_VERSION) \
		go.opentelemetry.io/otel/sdk@$(OTEL_VERSION) \
		go.opentelemetry.io/otel/trace@$(OTEL_VERSION) \
		go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc@$(OTEL_GRPC_VERSION) \
		google.golang.org/grpc@$(GRPC_GO_VERSION) \
		google.golang.org/protobuf@$(PROTOBUF_GO_VERSION) \
		github.com/jackc/pgx/v5@$(PGX_VERSION) \
		github.com/testcontainers/testcontainers-go@$(TESTCONTAINERS_VERSION) \
		github.com/testcontainers/testcontainers-go/modules/postgres@$(TESTCONTAINERS_VERSION) \
		github.com/moby/go-archive@$(MOBY_GO_ARCHIVE_VERSION) \
		golang.org/x/net@$(X_NET_VERSION)
	GOWORK=off $(GO) mod tidy

gen-sql:
	@test "$$($(SQLC) version)" = "$(SQLC_VERSION)"
	rm -f $(GENERATED_SQL_FILES)
	$(SQLC) generate

check-sql-drift: verify-tools
	@MODULE_ROOT="$(CURDIR)" \
		COMMON_GO_TOOLING_DIR="$(COMMON_GO_TOOLING_DIR)" \
		MAKE_COMMAND="$(MAKE)" \
		TOOLS_BIN="$(TOOLS_BIN)" \
		CHECK_NAME="check-sql-drift" \
		GENERATION_TARGET="gen-sql" \
		GENERATION_INPUTS="Makefile sqlc.yaml $(SQL_SCHEMA) $(SQL_QUERIES)" \
		GENERATION_TOOLS="sqlc" \
		GENERATED_ROOT="internal/postgres/dbgen" \
		GENERATED_PATTERN="internal/postgres/dbgen/*.go" \
		GENERATED_FILES="$(GENERATED_SQL_FILES)" \
		"$(COMMON_GO_TOOLING_DIR)/check-generated-drift.sh"

coverage:
	@test -n "$(strip $(COVERAGE_FILE))" || { echo "COVERAGE_FILE must not be empty" >&2; exit 1; }
	GOWORK=off $(GO) test -mod=readonly -count=1 \
		-covermode=atomic -coverprofile="$(COVERAGE_FILE)" ./...

test-integration:
	@GO="$(GO)" \
		DOCKER="$(DOCKER)" \
		POSTGRES_IMAGE="$(POSTGRES_IMAGE)" \
		INTEGRATION_SUITE="$(INTEGRATION_SUITE)" \
		FIXTURE_SET="$(FIXTURE_SET)" \
		./scripts/test-integration.sh

check: verify-tools check-docs format-check format-proto-check lint lint-proto test-race test-integration build \
	check-deps check-proto-drift check-sql-drift check-proto-breaking vulncheck

test-tooling: verify-tools
	@MODULE_ROOT="$(CURDIR)" COMMON_GO_TOOLING_DIR="$(COMMON_GO_TOOLING_DIR)" \
		MAKE_COMMAND="$(MAKE)" TOOLS_BIN="$(TOOLS_BIN)" ./scripts/test-tooling.sh

run:
	GOWORK=off $(GO) run -mod=readonly ./cmd/server
