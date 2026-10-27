.DEFAULT_GOAL := bootstrap-tools

GO ?= go
DOCKER ?= docker
NODE ?= node
NPM ?= npm
KUBECTL ?= kubectl
JQ ?= jq
DEPLOY_ENV ?=

PRODUCT_SERVICE_DIR := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))
PRODUCT_REPOSITORY_ROOT := $(abspath $(PRODUCT_SERVICE_DIR)../..)
ifneq ($(origin PRODUCT_TOOLING_REPOSITORY_ROOT), undefined)
PRODUCT_REPOSITORY_ROOT := $(abspath $(PRODUCT_TOOLING_REPOSITORY_ROOT))
endif
export PRODUCT_TOOLING_REPOSITORY_ROOT := $(PRODUCT_REPOSITORY_ROOT)
include $(PRODUCT_REPOSITORY_ROOT)/scripts/python/toolchain.mk

TOOLS_DIR := $(CURDIR)/.tools
TOOLS_BIN := $(TOOLS_DIR)/bin
BUF := $(TOOLS_BIN)/buf
PROTOC_GEN_GO := $(TOOLS_BIN)/protoc-gen-go
PROTOC_GEN_GO_GRPC := $(TOOLS_BIN)/protoc-gen-go-grpc
GOLANGCI_LINT := $(TOOLS_BIN)/golangci-lint
GOVULNCHECK := $(TOOLS_BIN)/govulncheck
SQLC := $(TOOLS_BIN)/sqlc
MMDC := $(TOOLS_BIN)/mmdc
PUPPETEER_CONFIG := puppeteer-config.json
GOLANGCI_CONFIG := .golangci.yml
PROTO_SCHEMA := api/product/v1/product_service.proto
CONTRACT_BASELINE_DIR := testdata/contract-baseline
GENERATED_PROTO_FILES := gen/product/v1/product_service.pb.go gen/product/v1/product_service_grpc.pb.go

GO_VERSION := 1.27.3
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
OTEL_B3_VERSION := v1.46.0
X_NET_VERSION := v0.60.0
PGX_VERSION := v5.11.0
GOOSE_VERSION := v3.27.0
TESTCONTAINERS_VERSION := v0.44.0
MOBY_GO_ARCHIVE_VERSION := v0.3.0
MERMAID_CLI_VERSION := 11.14.0
PUPPETEER_VERSION := 24.43.1
POSTGRES_IMAGE := postgres:17.10-alpine@sha256:742f40ea20b9ff2ff31db5458d127452988a2164df9e17441e191f3b72252193
HEAD_REVISION := $(shell git -C $(CURDIR) rev-parse --verify HEAD 2>/dev/null)
HEAD_CREATED := $(shell git -C $(CURDIR) show -s --format=%cI HEAD 2>/dev/null)
HEAD_VERSION := git-$(shell git -C $(CURDIR) rev-parse --short=12 HEAD 2>/dev/null)
IMAGE_NAME ?= insurance-hub-product-api
SOURCE_REVISION ?= $(HEAD_REVISION)
SOURCE_CREATED ?= $(HEAD_CREATED)
SOURCE_REPOSITORY ?= https://github.com/igor-baiborodine/insurance-hub
SOURCE_VERSION ?= $(HEAD_VERSION)
IMAGE_TAG ?= $(SOURCE_VERSION)
IMAGE_REF ?= $(IMAGE_NAME):$(IMAGE_TAG)
TARGET_PLATFORM ?= linux/amd64
SQL_SCHEMA := internal/postgres/sql/schema.sql
SQL_QUERIES := internal/postgres/sql/queries.sql
GENERATED_SQL_FILES := internal/postgres/dbgen/db.go internal/postgres/dbgen/models.go \
	internal/postgres/dbgen/queries.sql.go internal/postgres/dbgen/querier.go
TEST_PACKAGES ?= ./...
COVERAGE_FILE ?= coverage.txt
INTEGRATION_COVERAGE_FILE ?=
COVERAGE_MINIMUM ?= 75
COVERAGE_EXCLUDE_PATHS := gen internal/postgres/dbgen internal/testing
INTEGRATION_SUITE ?= all
FIXTURE_SET ?= qa
SECURITY_HARNESS_OUTPUT ?=
TELEMETRY_ACCEPTANCE_OUTPUT ?=
DOC_FILES ?= README.md ../../docs/migration/phase-4/product-service/README.md \
	../../docs/migration/phase-4/product-service/deployment-runbook.md \
	../../docs/migration/phase-4/product-service/established-cutover-runbook.md \
	../../docs/migration/phase-4/product-service/qualification-runbook.md \
	../../docs/migration/phase-4/product-service/observability-contract.md \
	../../docs/migration/phase-4/product-service/observability-runbook.md
EXTRA_TOOL_PACKAGES := github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)
VERIFY_EXTRA_TOOL_SPECS := sqlc|$(SQLC)|$(SQLC_VERSION)|version

COMMON_GO_TOOLING_DIR ?= ../../scripts/go/module
COMMON_GO_TOOLING_DIR := $(abspath $(COMMON_GO_TOOLING_DIR))
include $(COMMON_GO_TOOLING_DIR)/common.mk

.PHONY: pilot-test-json
pilot-test-json: ## Report selected uncached test execution as JSON for framework adoption
	@test -n "$(strip $(TEST_PACKAGES))" || { echo "TEST_PACKAGES must not be empty" >&2; exit 1; }
	GOWORK=off $(GO) test -json -mod=readonly -count=1 $(TEST_PACKAGES)

.PHONY: bootstrap-go-tools bootstrap-tools verify-tools format format-check update-deps check-deps \
	update-proto-deps format-proto format-proto-check lint-proto check-proto-breaking \
	gen-proto check-proto-drift gen-sql check-sql-drift lint test test-race coverage \
	coverage-validate coverage-check test-integration test-telemetry-continuity \
	build build-security-harness build-security-verifier build-qualification-client build-lifecycle-client build-telemetry-acceptance image-build image-check deploy-render deploy-check \
	deploy-preflight deploy-status image-load deploy deploy-verify stop deploy-test-tooling deploy-test vulncheck \
	bootstrap-doc-tools check-docs preview-docs check-go-pr check test-tooling run

export IMAGE_NAME IMAGE_TAG IMAGE_REF SOURCE_CREATED SOURCE_REPOSITORY SOURCE_REVISION SOURCE_VERSION TARGET_PLATFORM

image-build: python-runtime-check ## Build the pinned linux/amd64 Product runtime image
	@./scripts/build-image.sh

image-check: python-runtime-check image-build ## Build and validate Product image identity, contents, startup, reads, and SIGTERM
	@./scripts/check-image.sh

deploy-render: ## Render Product Go overlays without applying them. Usage: make deploy-render [DEPLOY_ENV=local-dev|qa]
	@KUBECTL="$(KUBECTL)" DEPLOY_ENV="$(DEPLOY_ENV)" ./scripts/render-deployment.sh

deploy-check: python-runtime-check ## Validate Product Go overlays, Java isolation, and active QA inclusion
	@KUBECTL="$(KUBECTL)" PYTHON="$(PYTHON)" ./scripts/check-deployment.sh

deploy-preflight deploy-status image-load deploy deploy-verify stop: python-runtime-check ## Guarded Product deployment operation; requires exact DEPLOY_* target coordinates
	@PYTHONDONTWRITEBYTECODE=1 $(PYTHON) scripts/deployment.py '$@'

deploy-test-tooling: python-toolchain-check ## Exercise guarded Product deployment operations with fake cluster adapters
	@PYTHONDONTWRITEBYTECODE=1 $(PYTHON) -m pytest -q scripts/test_deployment_operations.py

deploy-test: python-toolchain-check image-build ## Exercise the image and rendered pod in an owned disposable Kind namespace
	@PYTHONDONTWRITEBYTECODE=1 IMAGE_REF="$(IMAGE_REF)" $(PYTHON) -m pytest -q \
		-m integration scripts/test_deployment_lifecycle.py

build-security-harness: verify-tools ## Build the disposable Product security helper at SECURITY_HARNESS_OUTPUT
	@test -n "$(strip $(SECURITY_HARNESS_OUTPUT))" || { echo "SECURITY_HARNESS_OUTPUT is required" >&2; exit 1; }
	GOWORK=off $(GO) build -mod=readonly -race -o "$(SECURITY_HARNESS_OUTPUT)" \
		./internal/testing/cmd/securityharness

build-security-verifier: verify-tools ## Build the read-only Product security verifier at SECURITY_VERIFIER_OUTPUT
	@test -n "$(strip $(SECURITY_VERIFIER_OUTPUT))" || { echo "SECURITY_VERIFIER_OUTPUT is required" >&2; exit 1; }
	GOWORK=off $(GO) build -mod=readonly -o "$(SECURITY_VERIFIER_OUTPUT)" \
		./internal/testing/cmd/securityverify

build-qualification-client: verify-tools ## Build the read-only QA Product gRPC client at QUALIFICATION_CLIENT_OUTPUT
	@test -n "$(strip $(QUALIFICATION_CLIENT_OUTPUT))" || { echo "QUALIFICATION_CLIENT_OUTPUT is required" >&2; exit 1; }
	GOWORK=off $(GO) build -mod=readonly -o "$(QUALIFICATION_CLIENT_OUTPUT)" \
		./internal/testing/cmd/qualificationclient

build-lifecycle-client: verify-tools ## Build disposable HTTP/gRPC lifecycle client at LIFECYCLE_CLIENT_OUTPUT
	@test -n "$(strip $(LIFECYCLE_CLIENT_OUTPUT))" || { echo "LIFECYCLE_CLIENT_OUTPUT is required" >&2; exit 1; }
	GOWORK=off $(GO) build -mod=readonly -o "$(LIFECYCLE_CLIENT_OUTPUT)" \
		./internal/testing/cmd/lifecycleclient

build-telemetry-acceptance: verify-tools ## Build the disposable Product telemetry acceptance helper
	@test -n "$(strip $(TELEMETRY_ACCEPTANCE_OUTPUT))" || { echo "TELEMETRY_ACCEPTANCE_OUTPUT is required" >&2; exit 1; }
	GOWORK=off $(GO) build -mod=readonly -o "$(TELEMETRY_ACCEPTANCE_OUTPUT)" \
		./internal/testing/cmd/telemetryacceptance

bootstrap-tools: bootstrap-doc-tools

bootstrap-doc-tools: python-runtime-check
	@NODE="$(NODE)" NPM="$(NPM)" PYTHONDONTWRITEBYTECODE=1 $(PYTHON) scripts/bootstrap_docs.py

check-docs: python-runtime-check
	@test "$$($(MMDC) --version 2>/dev/null)" = "$(MERMAID_CLI_VERSION)" || { \
		echo "check-docs: Mermaid CLI $(MERMAID_CLI_VERSION) is required; run bootstrap-tools" >&2; \
		exit 1; \
	}
	@PUPPETEER_CACHE_DIR="$(TOOLS_DIR)/puppeteer" $(PYTHON) scripts/check_docs.py \
		--renderer "$(MMDC)" --puppeteer-config "$(PUPPETEER_CONFIG)" $(DOC_FILES)

preview-docs: python-runtime-check
	@test "$$($(MMDC) --version 2>/dev/null)" = "$(MERMAID_CLI_VERSION)" || { \
		echo "preview-docs: Mermaid CLI $(MERMAID_CLI_VERSION) is required; run bootstrap-tools" >&2; \
		exit 1; \
	}
	rm -rf "$(TOOLS_DIR)/docs-preview"
	@PUPPETEER_CACHE_DIR="$(TOOLS_DIR)/puppeteer" $(PYTHON) scripts/check_docs.py \
		--renderer "$(MMDC)" --puppeteer-config "$(PUPPETEER_CONFIG)" \
		--output-directory "$(TOOLS_DIR)/docs-preview" $(DOC_FILES)

update-deps:
	GOWORK=off $(GO) get \
		buf.build/go/protovalidate@$(PROTOVALIDATE_GO_VERSION) \
		buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go@$(PROTOVALIDATE_SCHEMA_GO_VERSION) \
		go.opentelemetry.io/otel@$(OTEL_VERSION) \
		go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc@$(OTEL_VERSION) \
		go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc@$(OTEL_VERSION) \
		go.opentelemetry.io/otel/sdk@$(OTEL_VERSION) \
		go.opentelemetry.io/otel/sdk/metric@$(OTEL_VERSION) \
		go.opentelemetry.io/otel/trace@$(OTEL_VERSION) \
		go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc@$(OTEL_GRPC_VERSION) \
		go.opentelemetry.io/contrib/propagators/b3@$(OTEL_B3_VERSION) \
		google.golang.org/grpc@$(GRPC_GO_VERSION) \
		google.golang.org/protobuf@$(PROTOBUF_GO_VERSION) \
		github.com/jackc/pgx/v5@$(PGX_VERSION) \
		github.com/pressly/goose/v3@$(GOOSE_VERSION) \
		github.com/testcontainers/testcontainers-go@$(TESTCONTAINERS_VERSION) \
		github.com/testcontainers/testcontainers-go/modules/postgres@$(TESTCONTAINERS_VERSION) \
		github.com/moby/go-archive@$(MOBY_GO_ARCHIVE_VERSION) \
		golang.org/x/net@$(X_NET_VERSION)
	GOWORK=off $(GO) mod tidy

.PHONY: update-goose-dependency check-schema-adoption
update-goose-dependency: verify-tools ## Resolve the pinned Goose library for the disposable adoption experiment
	GOWORK=off $(GO) get github.com/pressly/goose/v3@$(GOOSE_VERSION)
	GOWORK=off $(GO) mod tidy

check-schema-adoption: verify-tools ## Lint, scan, and run the tagged disposable Q03 prototype; requires Docker
	GOWORK=off $(GOLANGCI_LINT) run --config "$(GOLANGCI_CONFIG)" --build-tags=integration \
		internal/testing/schema_adoption_integration_test.go internal/testing/harness.go internal/testing/fixture.go
	GOWORK=off GOFLAGS=-mod=readonly $(GOVULNCHECK) -tags=integration -test ./internal/testing
	$(MAKE) test-integration INTEGRATION_SUITE=schema-adoption

.PHONY: check-administration-design
check-administration-design: PYTHON_FILES := scripts/postgres/service/test_administration_design.py services/product-service/scripts/test_administration_release.py
check-administration-design: python-toolchain-check python-format-check python-lint ## Q04 render/negative checks and disposable PostgreSQL roles; requires cached PostgreSQL image, Docker, kubectl
	@cd "$(PRODUCT_REPOSITORY_ROOT)" && PYTHONDONTWRITEBYTECODE=1 $(PYTHON) -m pytest --collect-only -q \
		scripts/postgres/service/test_administration_design.py \
		services/product-service/scripts/test_administration_release.py
	@cd "$(PRODUCT_REPOSITORY_ROOT)" && PYTHONDONTWRITEBYTECODE=1 KUBECTL="$(KUBECTL)" $(PYTHON) -m pytest -q \
		scripts/postgres/service/test_administration_design.py \
		services/product-service/scripts/test_administration_release.py

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

test-integration: python-runtime-check
	@GO="$(GO)" \
		DOCKER="$(DOCKER)" \
		POSTGRES_IMAGE="$(POSTGRES_IMAGE)" \
		INTEGRATION_SUITE="$(INTEGRATION_SUITE)" \
		FIXTURE_SET="$(FIXTURE_SET)" \
		INTEGRATION_COVERAGE_FILE="$(INTEGRATION_COVERAGE_FILE)" \
		PYTHON="$(PYTHON)" \
		./scripts/test-integration.sh

test-telemetry-continuity: python-runtime-check ## Exercise disposable Brave Java ↔ Go HTTP trace continuity
	@GO="$(GO)" PYTHON="$(PYTHON)" ./scripts/test-telemetry-continuity.sh

check-go-pr: INTEGRATION_COVERAGE_FILE = integration-coverage.txt
check-go-pr: verify-tools format-check format-proto-check lint lint-proto coverage-check test-integration build \
	check-deps check-proto-drift check-sql-drift check-proto-breaking vulncheck

check: check-go-pr check-docs

test-tooling: python-toolchain-check verify-tools
	@MODULE_ROOT="$(CURDIR)" COMMON_GO_TOOLING_DIR="$(COMMON_GO_TOOLING_DIR)" \
		MAKE_COMMAND="$(MAKE)" TOOLS_BIN="$(TOOLS_BIN)" PYTHON="$(PYTHON)" \
		COMMON_GO_TOOLING_PYTHON="$(PYTHON)" \
		PRODUCT_TOOLING_REPOSITORY_ROOT="$(PRODUCT_REPOSITORY_ROOT)" \
		./scripts/test-tooling.sh

run:
	GOWORK=off $(GO) run -mod=readonly ./cmd/server

.PHONY: bootstrap-ci-tools check-ci
bootstrap-ci-tools: python-runtime-check ## Install pinned Kubernetes clients in module-local .tools
	@bash scripts/bootstrap-ci-tools.sh

check-ci: python-runtime-check ## Validate Go workflow permissions, triggers, syntax, and boundaries
	@bash -n scripts/bootstrap-ci-tools.sh
	@sh -n scripts/check-deployment.sh
	@.tools/bin/actionlint -shellcheck= -pyflakes= ../../.github/workflows/go-release-svc.yml \
		../../.github/workflows/go-pr-mod.yml ../../.github/workflows/go-report-card.yml
	@PYTHONDONTWRITEBYTECODE=1 $(PYTHON) scripts/check_ci.py
