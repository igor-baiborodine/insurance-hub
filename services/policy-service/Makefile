.DEFAULT_GOAL := help

GO ?= go
DOCKER ?= docker
KUBECTL ?= kubectl
INSURANCE_HUB_ROOT ?= ../..
INSURANCE_HUB_ROOT := $(abspath $(INSURANCE_HUB_ROOT))
export INSURANCE_HUB_ROOT
TOOLING_REPOSITORY_ROOT ?= $(abspath $(CURDIR)/../..)
PYTHON_TOOLING_FILES := $(wildcard scripts/check_gateway_contract.py \
	scripts/test_check_gateway_contract.py scripts/test_tooling.py \
	scripts/deployment.py scripts/test_deployment.py \
	scripts/resource_administration.py scripts/test_resource_administration.py)
PYTHON_FAST_TEST_FILES := $(wildcard scripts/test_check_gateway_contract.py scripts/test_deployment.py \
	scripts/test_resource_administration.py)

include $(INSURANCE_HUB_ROOT)/scripts/python/toolchain.mk

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

PROTO_SCHEMA := api/policy/v1/policy_service.proto
# Policy-local client bindings are generated from exact copies of the provider-owned contracts.
PROVIDER_PROTOS := api/product/v1/product_service.proto api/pricing/v1/pricing_service.proto
PROTO_GENERATION_INPUTS := $(PROTO_SCHEMA) $(PROVIDER_PROTOS)
CONTRACT_BASELINE_DIR := testdata/contract-baseline
GENERATED_PROTO_FILES := \
	gen/policy/v1/policy_service.pb.go \
	gen/policy/v1/policy_service.pb.gw.go \
	gen/policy/v1/policy_service_grpc.pb.go \
	gen/openapiv2/policy/v1/policy_service.swagger.json \
	gen/product/v1/product_service.pb.go \
	gen/product/v1/product_service_grpc.pb.go \
	gen/pricing/v1/pricing_service.pb.go \
	gen/pricing/v1/pricing_service_grpc.pb.go

GO_VERSION := 1.27.4
BUF_VERSION := v1.73.0
PROTOC_GEN_GO_VERSION := v1.36.12
PROTOC_GEN_GO_GRPC_VERSION := v1.6.2
GRPC_GATEWAY_VERSION := v2.30.0
GOLANGCI_LINT_VERSION := v2.14.0
GOVULNCHECK_VERSION := v1.8.0

PROTOBUF_GO_VERSION := v1.36.12
GRPC_GO_VERSION := v1.83.2
PROTOVALIDATE_GO_VERSION := v1.4.0
PROTOVALIDATE_SCHEMA_GO_VERSION := v1.36.12-20260825204119-511051f7f437.1
OTEL_VERSION := v1.47.0
OTEL_GRPC_VERSION := v0.71.0
OTEL_HTTP_VERSION := v0.70.0
X_NET_VERSION := v0.60.0
SERVICEKIT_VERSION := v1.1.0
GENPROTO_GOOGLEAPIS_API_VERSION := v0.0.0-20260928230214-8a89bd6388cc
PGX_VERSION := v5.11.0
GOOSE_VERSION := v3.27.0
TESTCONTAINERS_VERSION := v0.44.0
FRANZ_GO_VERSION := v1.22.2
KAFKA_IMAGE ?= apache/kafka:4.1.0@sha256:bff074a5d0051dbc0bbbcd25b045bb1fe84833ec0d3c7c965d1797dd289ec88f
SQLC_VERSION := v1.31.1
SQL_SCHEMA := internal/postgres/sql/schema.sql
SQL_QUERIES := internal/postgres/sql/queries.sql
GENERATED_SQL_FILES := internal/postgres/dbgen/db.go internal/postgres/dbgen/models.go \
	internal/postgres/dbgen/queries.sql.go
BUILD_EXECUTABLES := server migrate
TEST_PACKAGES ?= ./...
COVERAGE_PACKAGES := ./cmd/... ./internal/...
COVERAGE_EXCLUDE_PATHS := internal/postgres/dbgen internal/testing
INTEGRATION_COVERAGE_FILE ?=
HEAD_REVISION := $(shell git -C $(INSURANCE_HUB_ROOT) rev-parse --verify HEAD 2>/dev/null)
HEAD_SHORT := $(shell git -C $(INSURANCE_HUB_ROOT) rev-parse --short=12 HEAD 2>/dev/null)
HEAD_CREATED := $(shell git -C $(INSURANCE_HUB_ROOT) show -s --format=%cI HEAD 2>/dev/null)
HEAD_VERSION := git-$(HEAD_SHORT)
IMAGE_NAME ?= insurance-hub-policy-api
SOURCE_REVISION ?= $(HEAD_REVISION)
SOURCE_CREATED ?= $(HEAD_CREATED)
SOURCE_REPOSITORY ?= https://github.com/igor-baiborodine/insurance-hub
SOURCE_VERSION ?= $(HEAD_VERSION)
IMAGE_TAG ?= 1.0.0-local.$(HEAD_SHORT)
IMAGE_REF ?= $(IMAGE_NAME):$(IMAGE_TAG)
TARGET_PLATFORM ?= linux/amd64
GO_SERVICE_TOOLING_CONFIG := $(CURDIR)/go-service-tooling.json

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

COMMON_GO_TOOLING_DIR ?= $(INSURANCE_HUB_ROOT)/scripts/go/module
COMMON_GO_TOOLING_DIR := $(abspath $(COMMON_GO_TOOLING_DIR))
include $(COMMON_GO_TOOLING_DIR)/common.mk

.PHONY: python-tooling-format-check python-tooling-lint-fix python-tooling-lint python-tooling-test-fast python-check \
	bootstrap-go-tools bootstrap-tools verify-tools format format-check update-deps check-deps \
	update-proto-deps format-proto format-proto-check lint-proto check-proto-breaking \
	gen-proto check-proto-drift check-provider-protos check-gateway-contract lint test test-race coverage coverage-validate coverage-check \
	test-coverage-tooling test-integration build vulncheck check-go-pr check test-tooling check-servicekit check-candidate-servicekit run \
	dockerfile-generate dockerfile-check image-build image-check _image-check-existing \
	shared-fast-build shared-fast-unit shared-candidate-format shared-candidate-lint \
	shared-candidate-race shared-candidate-dependency shared-candidate-vulnerability \
	shared-candidate-generated shared-candidate-breaking shared-candidate-integration shared-candidate-image \
	deploy-render deploy-check deploy-preflight deploy-status image-load deploy deploy-verify stop \
	deploy-test-tooling deploy-smoke _deploy-smoke-rpc

export IMAGE_NAME IMAGE_TAG IMAGE_REF SOURCE_CREATED SOURCE_REPOSITORY SOURCE_REVISION SOURCE_VERSION TARGET_PLATFORM

dockerfile-generate: python-runtime-check
	@PYTHONPATH="$(INSURANCE_HUB_ROOT)" PYTHONDONTWRITEBYTECODE=1 \
		"$(PYTHON)" -m scripts.go.service.dockerfile \
		--config "$(GO_SERVICE_TOOLING_CONFIG)" --repository-root "$(TOOLING_REPOSITORY_ROOT)" generate

dockerfile-check: python-runtime-check
	@PYTHONPATH="$(INSURANCE_HUB_ROOT)" PYTHONDONTWRITEBYTECODE=1 \
		"$(PYTHON)" -m scripts.go.service.dockerfile \
		--config "$(GO_SERVICE_TOOLING_CONFIG)" --repository-root "$(TOOLING_REPOSITORY_ROOT)" check

image-build: dockerfile-check python-runtime-check
	@PYTHONPATH="$(INSURANCE_HUB_ROOT)" PYTHONDONTWRITEBYTECODE=1 DOCKER="$(DOCKER)" \
		"$(PYTHON)" -m scripts.go.service.image --config "$(GO_SERVICE_TOOLING_CONFIG)" build

image-check: image-build
	@$(MAKE) --no-print-directory _image-check-existing

_image-check-existing: dockerfile-check python-runtime-check
	@PYTHONPATH="$(INSURANCE_HUB_ROOT)" PYTHONDONTWRITEBYTECODE=1 DOCKER="$(DOCKER)" \
		"$(PYTHON)" -m scripts.go.service.image --config "$(GO_SERVICE_TOOLING_CONFIG)" check

python-tooling-format-check: python-toolchain-check
	@cd "$(CURDIR)" && RUFF_CACHE_DIR="$(TOOLS_DIR)/ruff-cache" \
		PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m ruff format --check \
		--config "$(PYTHON_REPOSITORY_ROOT)/pyproject.toml" -- $(PYTHON_TOOLING_FILES)

python-tooling-lint: python-toolchain-check
	@cd "$(CURDIR)" && RUFF_CACHE_DIR="$(TOOLS_DIR)/ruff-cache" \
		PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m ruff check \
		--config "$(PYTHON_REPOSITORY_ROOT)/pyproject.toml" -- $(PYTHON_TOOLING_FILES)

python-tooling-lint-fix: python-toolchain-check
	@cd "$(CURDIR)" && RUFF_CACHE_DIR="$(TOOLS_DIR)/ruff-cache" \
		PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m ruff check --fix \
		--config "$(PYTHON_REPOSITORY_ROOT)/pyproject.toml" -- $(PYTHON_TOOLING_FILES)

python-tooling-test-fast: python-toolchain-check
	@cd "$(CURDIR)" && PYTHONPATH="$(INSURANCE_HUB_ROOT)" PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m pytest -q -p no:cacheprovider \
		-c "$(PYTHON_REPOSITORY_ROOT)/pyproject.toml" --rootdir "$(CURDIR)" \
		-m 'not integration and not acceptance' $(PYTHON_FAST_TEST_FILES)

python-check: python-tooling-format-check python-tooling-lint python-tooling-test-fast

bootstrap-go-tools bootstrap-tools: python-toolchain-setup

update-deps:
	GOWORK=off $(GO) get \
		buf.build/go/protovalidate@$(PROTOVALIDATE_GO_VERSION) \
		buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go@$(PROTOVALIDATE_SCHEMA_GO_VERSION) \
		github.com/grpc-ecosystem/grpc-gateway/v2@$(GRPC_GATEWAY_VERSION) \
		github.com/jackc/pgx/v5@$(PGX_VERSION) \
		github.com/pressly/goose/v3@$(GOOSE_VERSION) \
		github.com/testcontainers/testcontainers-go/modules/postgres@$(TESTCONTAINERS_VERSION) \
		github.com/twmb/franz-go@$(FRANZ_GO_VERSION) \
		go.opentelemetry.io/otel@$(OTEL_VERSION) \
		go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc@$(OTEL_GRPC_VERSION) \
		go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp@$(OTEL_HTTP_VERSION) \
		go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc@$(OTEL_VERSION) \
		go.opentelemetry.io/otel/sdk@$(OTEL_VERSION) \
		go.opentelemetry.io/otel/sdk/metric@$(OTEL_VERSION) \
		go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc@$(OTEL_VERSION) \
		google.golang.org/grpc@$(GRPC_GO_VERSION) \
		google.golang.org/protobuf@$(PROTOBUF_GO_VERSION) \
		google.golang.org/genproto/googleapis/api@$(GENPROTO_GOOGLEAPIS_API_VERSION) \
		golang.org/x/net@$(X_NET_VERSION)
	GOWORK=off $(GO) get github.com/igor-baiborodine/insurance-hub/libs/servicekit@$(SERVICEKIT_VERSION)
	GOWORK=off $(GO) mod tidy

check-go-pr: INTEGRATION_COVERAGE_FILE = integration-coverage.txt
check-go-pr: verify-tools dockerfile-check format-check format-proto-check lint lint-proto coverage-check test-integration build \
	check-deps check-proto-drift check-provider-protos check-sql-drift check-proto-breaking check-gateway-contract vulncheck python-check check-servicekit

check-provider-protos: ## Verify the provider client contracts match their owning services
	@for proto in $(PROVIDER_PROTOS); do \
		owner="$${proto#api/}"; owner="$${owner%%/*}-service"; \
		source="$(INSURANCE_HUB_ROOT)/services/$$owner/$$proto"; \
		cmp -s "$$proto" "$$source" || { \
			echo "check-provider-protos: $$proto differs from services/$$owner/$$proto" >&2; exit 1; }; \
	done
	@echo "check-provider-protos: provider client contracts match their owning services"

check-gateway-contract: python-runtime-check
	@PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" scripts/check_gateway_contract.py \
		--contract testdata/http-contract.json \
		--swagger gen/openapiv2/policy/v1/policy_service.swagger.json

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

# Include the migration smoke in the module gate so the coverage threshold measures its SQL path.
coverage: private export GOFLAGS := $(strip $(GOFLAGS) -tags=integration)

test-integration: ## Run the service composition and disposable PostgreSQL/TLS Kafka recovery smoke tests (Docker and OpenSSL required)
	@if [ -n "$(strip $(INTEGRATION_COVERAGE_FILE))" ]; then \
		mkdir -p "$(dir $(INTEGRATION_COVERAGE_FILE))"; \
	fi
	POLICY_TEST_KAFKA_IMAGE="$(KAFKA_IMAGE)" GOWORK=off $(GO) test -mod=readonly -count=1 -race -tags=integration -v \
		-run '^TestRun_(ProductionComposition.*|PolicyEventPublisherRecovery|PolicyOutboxOrdering|PolicyMigrationFreshTarget|PolicyMigrationAdoptsUnmanagedData|PolicyWriterAccess|PolicyStorageOnAdoptedData|PolicyTerminationAppendsHistory|PolicyWriterReadiness)$$' \
		./internal/service ./internal/administration ./internal/postgres ./internal/kafka \
		$(if $(strip $(INTEGRATION_COVERAGE_FILE)),-covermode=atomic -coverprofile="$(INTEGRATION_COVERAGE_FILE)")

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
		PYTHONPATH="$(INSURANCE_HUB_ROOT)" PYTHONDONTWRITEBYTECODE=1 \
			"$(PYTHON)" -m scripts.go.service.shared_phase \
			--config "$(GO_SERVICE_TOOLING_CONFIG)" "$$phase"; \
	else case "$$phase" in generated) target=check-proto-drift;; breaking) target=check-proto-breaking;; integration) target=test-integration;; image) target=image-check;; esac; \
		$(MAKE) --no-print-directory "$$target"; fi


deploy-render deploy-check: python-runtime-check
	@PYTHONPATH="$(INSURANCE_HUB_ROOT)" PYTHONDONTWRITEBYTECODE=1 KUBECTL="$(KUBECTL)" \
		"$(PYTHON)" scripts/deployment.py '$(@:deploy-%=%)'

deploy-preflight deploy-status image-load deploy deploy-verify stop: python-runtime-check
	@PYTHONPATH="$(INSURANCE_HUB_ROOT)" PYTHONDONTWRITEBYTECODE=1 KUBECTL="$(KUBECTL)" \
		DOCKER="$(DOCKER)" GO="$(GO)" MAKE_COMMAND="$(MAKE)" "$(PYTHON)" scripts/deployment.py '$@'

deploy-test-tooling: python-toolchain-check
	@PYTHONPATH="$(INSURANCE_HUB_ROOT)" PYTHONDONTWRITEBYTECODE=1 KUBECTL="$(KUBECTL)" \
		"$(PYTHON)" -m pytest -q -p no:cacheprovider -m 'not integration' scripts/test_deployment.py

deploy-smoke: python-toolchain-check
	@PYTHONPATH="$(INSURANCE_HUB_ROOT)" PYTHONDONTWRITEBYTECODE=1 KUBECTL="$(KUBECTL)" \
		DOCKER="$(DOCKER)" GO="$(GO)" MAKE_COMMAND="$(MAKE)" \
		"$(PYTHON)" -m pytest -q -s -p no:cacheprovider -m integration scripts/test_deployment.py

_deploy-smoke-rpc:
	GOWORK=off $(GO) test -mod=readonly -count=1 -tags=deployment \
		-run '^TestDeployedDirectRPC_CreateOffer$$' -json ./internal/service

test-tooling: verify-tools python-toolchain-check
	@MODULE_ROOT="$(CURDIR)" INSURANCE_HUB_ROOT="$(INSURANCE_HUB_ROOT)" \
		COMMON_GO_TOOLING_DIR="$(COMMON_GO_TOOLING_DIR)" \
		MAKE_COMMAND="$(MAKE)" TOOLS_BIN="$(TOOLS_BIN)" \
		GO="$(GO)" UV="$(UV)" \
		COMMON_GO_TOOLING_PYTHON="$(COMMON_GO_TOOLING_PYTHON)" \
		PYTHONPATH="$(INSURANCE_HUB_ROOT)" PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" \
		-m scripts.go.service.tooling_runner --config "$(GO_SERVICE_TOOLING_CONFIG)" \
		--repository-root "$(TOOLING_REPOSITORY_ROOT)" --tooling-root "$(INSURANCE_HUB_ROOT)" suite
	@$(MAKE) --no-print-directory test-coverage-tooling


run:
	GOWORK=off $(GO) run -mod=readonly ./cmd/server

.PHONY: help
help: ## Show the explicit setup and validation entry points without installing tools
	@printf '%s\n' 'Go service commands (see README.md for the full interface):' \
		'  make bootstrap-tools  Install pinned local tools explicitly' \
		'  make check            Validate the module without source mutation' \
		'  make run              Start the service with environment configuration' \
		'  make build            Compile the server and migrator' \
		'  make test-integration Run service composition and disposable PostgreSQL migration smoke tests' \
		'  make migration        Run migrate/status through a tunnel to the enrolled target' \
		'  make gen-sql          Regenerate sqlc queries; check-sql-drift verifies them' \
		'  make resource-inspect Inspect Policy database and Kafka trust resources' \
		'  make resource-ensure  Confirmed role, grant, enrollment and Secret setup' \
		'  make resource-verify  Verify effective grants, Kafka trust and SQL denials'

.PHONY: migration gen-sql check-sql-drift

gen-sql: ## Regenerate sqlc Policy queries with the pinned sqlc
	@test "$$($(SQLC) version)" = "$(SQLC_VERSION)"
	rm -f $(GENERATED_SQL_FILES)
	$(SQLC) generate

check-sql-drift: verify-tools ## Verify sqlc output matches its schema and query inputs
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

# The migrator runs from this checkout, never from the serving image or during startup. It reaches
# the enrolled CNPG primary through an owned loopback port-forward with TLS name verification.
migration: python-runtime-check ## Run POLICY_MIGRATION_OPERATION=migrate|status against the enrolled Policy target
	@PYTHONPATH="$(INSURANCE_HUB_ROOT)" PYTHONDONTWRITEBYTECODE=1 KUBECTL="$(KUBECTL)" GO="$(GO)" \
		"$(PYTHON)" scripts/resource_administration.py migration

.PHONY: resource-inspect resource-ensure resource-verify resource-test-fast

resource-inspect: ## Inspect Policy database and Kafka trust resources without mutation
resource-ensure: ## Ensure Policy database roles, grants, enrollment and workload Secrets (confirmed)
resource-verify: ## Verify Policy grants, Kafka trust and SQL denials
resource-inspect resource-ensure resource-verify: python-runtime-check
	@PYTHONPATH="$(INSURANCE_HUB_ROOT)" PYTHONDONTWRITEBYTECODE=1 KUBECTL="$(KUBECTL)" \
		"$(PYTHON)" scripts/resource_administration.py '$(@:resource-%=%)'

resource-test-fast: python-toolchain-check ## Exercise Policy resource guards with fake adapters
	@cd "$(CURDIR)" && PYTHONPATH="$(INSURANCE_HUB_ROOT)" PYTHONDONTWRITEBYTECODE=1 "$(PYTHON)" -m pytest -q \
		-p no:cacheprovider -c "$(PYTHON_REPOSITORY_ROOT)/pyproject.toml" --rootdir "$(CURDIR)" \
		-m 'not integration and not acceptance' scripts/test_resource_administration.py
