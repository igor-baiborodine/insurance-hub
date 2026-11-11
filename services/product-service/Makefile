.DEFAULT_GOAL := help

GO ?= go
DOCKER ?= docker
KUBECTL ?= kubectl
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
PROTOC_GEN_GRPC_GATEWAY := $(TOOLS_BIN)/protoc-gen-grpc-gateway
PROTOC_GEN_OPENAPIV2 := $(TOOLS_BIN)/protoc-gen-openapiv2
GOLANGCI_LINT := $(TOOLS_BIN)/golangci-lint
GOVULNCHECK := $(TOOLS_BIN)/govulncheck
SQLC := $(TOOLS_BIN)/sqlc
GOLANGCI_CONFIG := .golangci.yml
PROTO_SCHEMA := api/product/v1/product_service.proto
CONTRACT_BASELINE_DIR := testdata/contract-baseline
GENERATED_PROTO_FILES := \
	gen/product/v1/product_service.pb.go \
	gen/product/v1/product_service.pb.gw.go \
	gen/product/v1/product_service_grpc.pb.go \
	gen/openapiv2/product/v1/product_service.swagger.json

GO_VERSION := 1.27.4
BUF_VERSION := v1.73.0
PROTOC_GEN_GO_VERSION := v1.36.12
PROTOC_GEN_GO_GRPC_VERSION := v1.6.2
GRPC_GATEWAY_VERSION := v2.30.0
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
SERVICEKIT_VERSION := v1.0.2
GENPROTO_GOOGLEAPIS_API_VERSION := v0.0.0-20260819154853-08b0e4226688
MOBY_GO_ARCHIVE_VERSION := v0.3.0
POSTGRES_IMAGE := postgres:17.10-alpine@sha256:742f40ea20b9ff2ff31db5458d127452988a2164df9e17441e191f3b72252193
HEAD_REVISION := $(shell git -C $(CURDIR) rev-parse --verify HEAD 2>/dev/null)
HEAD_SHORT := $(shell git -C $(CURDIR) rev-parse --short=12 HEAD 2>/dev/null)
HEAD_CREATED := $(shell git -C $(CURDIR) show -s --format=%cI HEAD 2>/dev/null)
HEAD_VERSION := git-$(HEAD_SHORT)
IMAGE_NAME ?= insurance-hub-product-api
SOURCE_REVISION ?= $(HEAD_REVISION)
SOURCE_CREATED ?= $(HEAD_CREATED)
SOURCE_REPOSITORY ?= https://github.com/igor-baiborodine/insurance-hub
SOURCE_VERSION ?= $(HEAD_VERSION)
IMAGE_TAG ?= $(SOURCE_VERSION)
IMAGE_REF ?= $(IMAGE_NAME):$(IMAGE_TAG)
TARGET_PLATFORM ?= linux/amd64
GO_SERVICE_TOOLING_CONFIG := $(CURDIR)/go-service-tooling.json
SQL_SCHEMA := internal/postgres/sql/schema.sql
SQL_QUERIES := internal/postgres/sql/queries.sql
GENERATED_SQL_FILES := internal/postgres/dbgen/db.go internal/postgres/dbgen/models.go \
	internal/postgres/dbgen/queries.sql.go internal/postgres/dbgen/querier.go
TEST_PACKAGES ?= ./...
BUILD_EXECUTABLES := server migrate seed
COVERAGE_FILE ?= coverage.txt
INTEGRATION_COVERAGE_FILE ?=
COVERAGE_MINIMUM ?= 75
COVERAGE_EXCLUDE_PATHS := gen internal/postgres/dbgen internal/testing
INTEGRATION_SUITE ?= all
FIXTURE_SET ?= qa
SECURITY_HARNESS_OUTPUT ?=
TELEMETRY_ACCEPTANCE_OUTPUT ?=
EXTRA_TOOL_PACKAGES := \
	github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@$(GRPC_GATEWAY_VERSION) \
	github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2@$(GRPC_GATEWAY_VERSION) \
	github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)
VERIFY_EXTRA_TOOL_SPECS := \
	protoc-gen-grpc-gateway|$(PROTOC_GEN_GRPC_GATEWAY)|Version $(GRPC_GATEWAY_VERSION), commit unknown, built at unknown|--version;\
	protoc-gen-openapiv2|$(PROTOC_GEN_OPENAPIV2)|Version $(GRPC_GATEWAY_VERSION), commit unknown, built at unknown|--version;\
	sqlc|$(SQLC)|$(SQLC_VERSION)|version
PROTO_GENERATION_TOOLS := \
	buf protoc-gen-go protoc-gen-go-grpc protoc-gen-grpc-gateway protoc-gen-openapiv2
GENERATED_PROTO_PATTERN := gen/*

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
	coverage-validate coverage-check test-integration \
	build build-security-harness build-security-verifier build-lifecycle-client build-telemetry-acceptance image-build image-check deploy-render deploy-check \
	dockerfile-generate dockerfile-check _image-check-existing security-verify \
	deploy-preflight deploy-status image-load deploy deploy-verify stop deploy-test-tooling deploy-test \
	vulncheck check-servicekit check-candidate-servicekit \
	resource-inspect resource-ensure resource-verify resource-rotate resource-revoke \
	resource-test-fast resource-test-integration \
	shared-fast-build shared-fast-unit shared-candidate-format shared-candidate-lint \
	shared-candidate-race shared-candidate-dependency shared-candidate-vulnerability \
	shared-candidate-generated shared-candidate-breaking shared-candidate-integration \
	shared-candidate-image check-go-pr check test-tooling run

export IMAGE_NAME IMAGE_TAG IMAGE_REF SOURCE_CREATED SOURCE_REPOSITORY SOURCE_REVISION SOURCE_VERSION TARGET_PLATFORM

dockerfile-generate: python-runtime-check
	@PYTHONPATH="$(PRODUCT_REPOSITORY_ROOT)" PYTHONDONTWRITEBYTECODE=1 \
		"$(PYTHON)" -m scripts.go.service.dockerfile \
		--config "$(GO_SERVICE_TOOLING_CONFIG)" --repository-root "$(PRODUCT_REPOSITORY_ROOT)" generate

dockerfile-check: python-runtime-check
	@PYTHONPATH="$(PRODUCT_REPOSITORY_ROOT)" PYTHONDONTWRITEBYTECODE=1 \
		"$(PYTHON)" -m scripts.go.service.dockerfile \
		--config "$(GO_SERVICE_TOOLING_CONFIG)" --repository-root "$(PRODUCT_REPOSITORY_ROOT)" check

image-build: dockerfile-check python-runtime-check ## Build the pinned linux/amd64 Product runtime image
	@PYTHONPATH="$(PRODUCT_REPOSITORY_ROOT)" PYTHONDONTWRITEBYTECODE=1 DOCKER="$(DOCKER)" \
		"$(PYTHON)" -m scripts.go.service.image --config "$(GO_SERVICE_TOOLING_CONFIG)" build

image-check: image-build ## Build and validate Product image identity, contents, startup, reads, and SIGTERM
	@$(MAKE) --no-print-directory _image-check-existing

_image-check-existing: dockerfile-check python-runtime-check
	@PYTHONPATH="$(PRODUCT_REPOSITORY_ROOT)" PYTHONDONTWRITEBYTECODE=1 DOCKER="$(DOCKER)" \
		"$(PYTHON)" -m scripts.go.service.image --config "$(GO_SERVICE_TOOLING_CONFIG)" inspect
	@PYTHONPATH="$(PRODUCT_REPOSITORY_ROOT):$(CURDIR)/scripts" PYTHONDONTWRITEBYTECODE=1 \
		DOCKER="$(DOCKER)" "$(PYTHON)" scripts/image_check.py

deploy-render: ## Render Product Go overlays without applying them. Usage: make deploy-render [DEPLOY_ENV=local-dev|qa]
	@PYTHONPATH="$(PRODUCT_REPOSITORY_ROOT):$(CURDIR)/scripts" PYTHONDONTWRITEBYTECODE=1 \
		KUBECTL="$(KUBECTL)" DEPLOY_ENV="$(DEPLOY_ENV)" "$(PYTHON)" scripts/deployment_check.py render

deploy-check: python-runtime-check ## Validate Product Go overlays, Java isolation, and active QA inclusion
	@PYTHONPATH="$(PRODUCT_REPOSITORY_ROOT):$(CURDIR)/scripts" PYTHONDONTWRITEBYTECODE=1 \
		KUBECTL="$(KUBECTL)" "$(PYTHON)" scripts/deployment_check.py check

deploy-preflight deploy-status image-load deploy deploy-verify stop: python-runtime-check ## Guarded Product deployment operation; requires exact DEPLOY_* target coordinates
	@PYTHONPATH="$(PRODUCT_REPOSITORY_ROOT):$(CURDIR)/scripts" PYTHONDONTWRITEBYTECODE=1 \
		$(PYTHON) scripts/deployment.py '$@'

deploy-test-tooling: python-toolchain-check ## Exercise guarded Product deployment operations with fake cluster adapters
	@PYTHONPATH="$(PRODUCT_REPOSITORY_ROOT):$(CURDIR)/scripts" PYTHONDONTWRITEBYTECODE=1 \
		$(PYTHON) -m pytest -q scripts/test_deployment_operations.py

deploy-test: python-toolchain-check image-build ## Exercise the image and rendered pod in an owned disposable Kind namespace
	@PYTHONPATH="$(PRODUCT_REPOSITORY_ROOT):$(CURDIR)/scripts" PYTHONDONTWRITEBYTECODE=1 \
		IMAGE_REF="$(IMAGE_REF)" $(PYTHON) -m pytest -q \
		-m integration scripts/test_deployment_lifecycle.py

resource-inspect: ## Inspect Product PostgreSQL resources without mutation
resource-ensure: ## Ensure Product PostgreSQL roles and workload Secrets
resource-verify: ## Verify Product resources and effective workload permissions
resource-rotate: ## Rotate Product PostgreSQL credentials with recovery
resource-revoke: ## Revoke inactive Product PostgreSQL resources
resource-inspect resource-ensure resource-verify resource-rotate resource-revoke: python-runtime-check
	@PYTHONPATH="$(PRODUCT_REPOSITORY_ROOT):$(CURDIR)/scripts" PYTHONDONTWRITEBYTECODE=1 KUBECTL="$(KUBECTL)" \
		"$(PYTHON)" scripts/resource_administration.py '$(@:resource-%=%)'

resource-test-fast: python-toolchain-check ## Exercise Product resource guards with fake adapters
	@cd "$(PRODUCT_REPOSITORY_ROOT)" && PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m pytest -q \
		-m 'not integration and not acceptance' \
		services/product-service/scripts/test_administration.py \
		services/product-service/scripts/test_resource_administration.py

resource-test-integration: python-toolchain-check ## Exercise Product permissions in disposable PostgreSQL
	@cd "$(PRODUCT_REPOSITORY_ROOT)" && PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m pytest -q \
		-m integration services/product-service/scripts/test_administration.py \
		services/product-service/scripts/test_resource_administration.py

build-security-harness: verify-tools ## Build the disposable Product security helper at SECURITY_HARNESS_OUTPUT
	@test -n "$(strip $(SECURITY_HARNESS_OUTPUT))" || { echo "SECURITY_HARNESS_OUTPUT is required" >&2; exit 1; }
	GOWORK=off $(GO) build -mod=readonly -race -o "$(SECURITY_HARNESS_OUTPUT)" \
		./internal/testing/cmd/securityharness

build-security-verifier: verify-tools ## Build the read-only Product security verifier at SECURITY_VERIFIER_OUTPUT
	@test -n "$(strip $(SECURITY_VERIFIER_OUTPUT))" || { echo "SECURITY_VERIFIER_OUTPUT is required" >&2; exit 1; }
	GOWORK=off $(GO) build -mod=readonly -o "$(SECURITY_VERIFIER_OUTPUT)" \
		./internal/testing/cmd/securityverify

build-lifecycle-client: verify-tools ## Build disposable HTTP/gRPC lifecycle client at LIFECYCLE_CLIENT_OUTPUT
	@test -n "$(strip $(LIFECYCLE_CLIENT_OUTPUT))" || { echo "LIFECYCLE_CLIENT_OUTPUT is required" >&2; exit 1; }
	GOWORK=off $(GO) build -mod=readonly -o "$(LIFECYCLE_CLIENT_OUTPUT)" \
		./internal/testing/cmd/lifecycleclient

build-telemetry-acceptance: verify-tools ## Build the disposable Product telemetry acceptance helper
	@test -n "$(strip $(TELEMETRY_ACCEPTANCE_OUTPUT))" || { echo "TELEMETRY_ACCEPTANCE_OUTPUT is required" >&2; exit 1; }
	GOWORK=off $(GO) build -mod=readonly -o "$(TELEMETRY_ACCEPTANCE_OUTPUT)" \
		./internal/testing/cmd/telemetryacceptance

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
		github.com/grpc-ecosystem/grpc-gateway/v2@$(GRPC_GATEWAY_VERSION) \
		google.golang.org/genproto/googleapis/api@$(GENPROTO_GOOGLEAPIS_API_VERSION) \
		google.golang.org/protobuf@$(PROTOBUF_GO_VERSION) \
		github.com/jackc/pgx/v5@$(PGX_VERSION) \
		github.com/pressly/goose/v3@$(GOOSE_VERSION) \
		github.com/testcontainers/testcontainers-go@$(TESTCONTAINERS_VERSION) \
		github.com/testcontainers/testcontainers-go/modules/postgres@$(TESTCONTAINERS_VERSION) \
		github.com/moby/go-archive@$(MOBY_GO_ARCHIVE_VERSION) \
		golang.org/x/net@$(X_NET_VERSION)
	GOWORK=off $(GO) get github.com/igor-baiborodine/insurance-hub/libs/servicekit@$(SERVICEKIT_VERSION)
	GOWORK=off $(GO) mod tidy

.PHONY: build-migrator build-seeder
build-migrator: verify-tools ## Build standalone Product migrator; requires MIGRATOR_OUTPUT
	@test -n "$(strip $(MIGRATOR_OUTPUT))" || { echo "MIGRATOR_OUTPUT is required" >&2; exit 1; }
	GOWORK=off $(GO) build -mod=readonly -o "$(MIGRATOR_OUTPUT)" ./cmd/migrate

build-seeder: verify-tools ## Build standalone Product seeder; requires SEEDER_OUTPUT
	@test -n "$(strip $(SEEDER_OUTPUT))" || { echo "SEEDER_OUTPUT is required" >&2; exit 1; }
	GOWORK=off $(GO) build -mod=readonly -o "$(SEEDER_OUTPUT)" ./cmd/seed

gen-sql:
	@test "$$($(SQLC) version)" = "$(SQLC_VERSION)"
	rm -f $(GENERATED_SQL_FILES)
	$(SQLC) generate

.PHONY: check-migration-schema
check-migration-schema: INTEGRATION_SUITE := migration
check-migration-schema: test-integration ## Compare real migrated Product schema with accepted baseline and sqlc input

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
	$(MAKE) check-migration-schema

test-integration: python-runtime-check
	@GO="$(GO)" \
		DOCKER="$(DOCKER)" \
		POSTGRES_IMAGE="$(POSTGRES_IMAGE)" \
		INTEGRATION_SUITE="$(INTEGRATION_SUITE)" \
		FIXTURE_SET="$(FIXTURE_SET)" \
		INTEGRATION_COVERAGE_FILE="$(INTEGRATION_COVERAGE_FILE)" \
		PYTHONPATH="$(PRODUCT_REPOSITORY_ROOT):$(CURDIR)/scripts" PYTHONDONTWRITEBYTECODE=1 \
		"$(PYTHON)" scripts/integration_test.py

check-go-pr: INTEGRATION_COVERAGE_FILE = integration-coverage.txt
check-go-pr: verify-tools format-check format-proto-check lint lint-proto coverage-check test-integration build \
	check-deps check-proto-drift check-sql-drift check-proto-breaking vulncheck check-servicekit

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

shared-fast-build:
	@if [ -n "$$SERVICEKIT_CONTEXT_SOCKET" ]; then \
		SERVICEKIT_PHASE=build PYTHONPATH="$(PRODUCT_REPOSITORY_ROOT)/scripts/go" "$(PYTHON)" "$(PRODUCT_REPOSITORY_ROOT)/scripts/go/shared_cli.py" phase; \
	else $(MAKE) --no-print-directory build; fi

shared-fast-unit:
	@if [ -n "$$SERVICEKIT_CONTEXT_SOCKET" ]; then \
		SERVICEKIT_PHASE=unit PYTHONPATH="$(PRODUCT_REPOSITORY_ROOT)/scripts/go" "$(PYTHON)" "$(PRODUCT_REPOSITORY_ROOT)/scripts/go/shared_cli.py" phase; \
	else $(MAKE) --no-print-directory test; fi

shared-candidate-format:
shared-candidate-lint:
shared-candidate-race:
shared-candidate-dependency:
shared-candidate-vulnerability:
shared-candidate-format shared-candidate-lint shared-candidate-race shared-candidate-dependency shared-candidate-vulnerability:
	@phase="$(@:shared-candidate-%=%)"; \
	if [ -n "$$SERVICEKIT_CONTEXT_SOCKET" ]; then \
		SERVICEKIT_PHASE="$$phase" PYTHONPATH="$(PRODUCT_REPOSITORY_ROOT)/scripts/go" "$(PYTHON)" "$(PRODUCT_REPOSITORY_ROOT)/scripts/go/shared_cli.py" phase; \
	else case "$$phase" in format) target=format-check;; race) target=test-race;; dependency) target=check-deps;; vulnerability) target=vulncheck;; *) target="$$phase";; esac; \
		$(MAKE) --no-print-directory "$$target"; fi

shared-candidate-generated:
shared-candidate-breaking:
shared-candidate-integration:
shared-candidate-image:
shared-candidate-generated shared-candidate-breaking shared-candidate-integration shared-candidate-image:
	@phase="$(@:shared-candidate-%=%)"; \
	if [ -n "$$SERVICEKIT_CONTEXT_SOCKET" ]; then \
		PYTHONPATH="$(PRODUCT_REPOSITORY_ROOT)" PYTHONDONTWRITEBYTECODE=1 \
			"$(PYTHON)" -m scripts.go.service.shared_phase \
			--config "$(GO_SERVICE_TOOLING_CONFIG)" "$$phase"; \
	else case "$$phase" in generated) target=check-proto-drift;; breaking) target=check-proto-breaking;; integration) target=test-integration;; image) target=image-check;; esac; \
		$(MAKE) --no-print-directory "$$target"; fi

test-tooling: python-toolchain-check verify-tools
	@MODULE_ROOT="$(CURDIR)" COMMON_GO_TOOLING_DIR="$(COMMON_GO_TOOLING_DIR)" \
		MAKE_COMMAND="$(MAKE)" TOOLS_BIN="$(TOOLS_BIN)" PYTHON="$(PYTHON)" \
		COMMON_GO_TOOLING_PYTHON="$(PYTHON)" \
		PRODUCT_TOOLING_REPOSITORY_ROOT="$(PRODUCT_REPOSITORY_ROOT)" \
		./scripts/test-tooling.sh

security-verify: python-runtime-check ## Run read-only Product HTTP/gRPC probes against explicit target endpoints
	@PYTHONPATH="$(PRODUCT_REPOSITORY_ROOT)" PYTHONDONTWRITEBYTECODE=1 \
		"$(PYTHON)" scripts/security_verify.py

run:
	GOWORK=off $(GO) run -mod=readonly ./cmd/server

.PHONY: help
help: ## Show explicit setup and validation entry points without installing tools
	@printf '%s\n' 'Product service commands (see README.md for the full interface):' \
		'  make bootstrap-tools  Install pinned local tools explicitly' \
		'  make check            Validate the module without source mutation' \
		'  make run              Start Product with environment configuration'
