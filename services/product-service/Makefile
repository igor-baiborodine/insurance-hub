.DEFAULT_GOAL := bootstrap-tools

GO ?= go
DOCKER ?= docker

TOOLS_DIR := $(CURDIR)/.tools
TOOLS_BIN := $(TOOLS_DIR)/bin
BUF := $(TOOLS_BIN)/buf
PROTOC_GEN_GO := $(TOOLS_BIN)/protoc-gen-go
PROTOC_GEN_GO_GRPC := $(TOOLS_BIN)/protoc-gen-go-grpc
GOLANGCI_LINT := $(TOOLS_BIN)/golangci-lint
GOVULNCHECK := $(TOOLS_BIN)/govulncheck
SQLC := $(TOOLS_BIN)/sqlc
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
POSTGRES_IMAGE := postgres:17.10-alpine@sha256:742f40ea20b9ff2ff31db5458d127452988a2164df9e17441e191f3b72252193
SQL_SCHEMA := internal/postgres/sql/schema.sql
SQL_QUERIES := internal/postgres/sql/queries.sql
GENERATED_SQL_FILES := internal/postgres/dbgen/db.go internal/postgres/dbgen/models.go \
	internal/postgres/dbgen/queries.sql.go internal/postgres/dbgen/querier.go
TEST_PACKAGES ?= ./...
INTEGRATION_SUITE ?= all
FIXTURE_SET ?= qa

.PHONY: bootstrap-tools verify-tools format format-check update-deps check-deps \
	update-proto-deps format-proto format-proto-check lint-proto check-proto-breaking \
	gen-proto check-proto-drift gen-sql check-sql-drift lint test test-race test-integration \
	build vulncheck check run

bootstrap-tools:
	mkdir -p "$(TOOLS_BIN)"
	GOWORK=off GOBIN="$(TOOLS_BIN)" $(GO) install \
		github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION)
	GOWORK=off GOBIN="$(TOOLS_BIN)" $(GO) install \
		google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)
	GOWORK=off GOBIN="$(TOOLS_BIN)" $(GO) install \
		google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GO_GRPC_VERSION)
	GOWORK=off GOBIN="$(TOOLS_BIN)" $(GO) install \
		github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	GOWORK=off GOBIN="$(TOOLS_BIN)" $(GO) install \
		golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	GOWORK=off GOBIN="$(TOOLS_BIN)" $(GO) install \
		github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)
	@$(MAKE) --no-print-directory verify-tools

verify-tools:
	@GO="$(GO)" \
		BUF="$(BUF)" \
		PROTOC_GEN_GO="$(PROTOC_GEN_GO)" \
		PROTOC_GEN_GO_GRPC="$(PROTOC_GEN_GO_GRPC)" \
		GOLANGCI_LINT="$(GOLANGCI_LINT)" \
		GOVULNCHECK="$(GOVULNCHECK)" \
		SQLC="$(SQLC)" \
		GOLANGCI_CONFIG="$(GOLANGCI_CONFIG)" \
		GO_VERSION="$(GO_VERSION)" \
		BUF_VERSION="$(BUF_VERSION)" \
		PROTOC_GEN_GO_VERSION="$(PROTOC_GEN_GO_VERSION)" \
		PROTOC_GEN_GO_GRPC_VERSION="$(PROTOC_GEN_GO_GRPC_VERSION)" \
		GOLANGCI_LINT_VERSION="$(GOLANGCI_LINT_VERSION)" \
		GOVULNCHECK_VERSION="$(GOVULNCHECK_VERSION)" \
		SQLC_VERSION="$(SQLC_VERSION)" \
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

check-deps: verify-tools
	@GO="$(GO)" MAKE_COMMAND="$(MAKE)" ./scripts/check-deps.sh

update-proto-deps:
	@test "$$($(BUF) --version)" = "$(patsubst v%,%,$(BUF_VERSION))"
	$(BUF) dep update

format-proto: verify-tools
	$(BUF) format --write "$(PROTO_SCHEMA)"

format-proto-check: verify-tools
	$(BUF) format --diff --exit-code "$(PROTO_SCHEMA)"

lint-proto: verify-tools
	$(BUF) lint "$(PROTO_SCHEMA)"

check-proto-breaking: verify-tools
	@for path in \
		"$(CONTRACT_BASELINE_DIR)/buf.yaml" \
		"$(CONTRACT_BASELINE_DIR)/buf.lock" \
		"$(CONTRACT_BASELINE_DIR)/$(PROTO_SCHEMA)"; do \
		if test ! -f "$$path" || test ! -r "$$path"; then \
			echo "check-proto-breaking: missing or unreadable baseline file: $$path" >&2; \
			exit 1; \
		fi; \
	done
	$(BUF) breaking "$(PROTO_SCHEMA)" --against \
		"$(CONTRACT_BASELINE_DIR)/$(PROTO_SCHEMA)"

gen-proto:
	@test "$$($(BUF) --version)" = "$(patsubst v%,%,$(BUF_VERSION))"
	@test "$$($(PROTOC_GEN_GO) --version)" = "protoc-gen-go $(PROTOC_GEN_GO_VERSION)"
	@test "$$($(PROTOC_GEN_GO_GRPC) --version)" = \
		"protoc-gen-go-grpc $(patsubst v%,%,$(PROTOC_GEN_GO_GRPC_VERSION))"
	rm -f $(GENERATED_PROTO_FILES)
	$(BUF) generate

check-proto-drift: verify-tools
	@MAKE_COMMAND="$(MAKE)" \
		TOOLS_BIN="$(TOOLS_BIN)" \
		PROTO_SCHEMA="$(PROTO_SCHEMA)" \
		GENERATED_PROTO_FILES="$(GENERATED_PROTO_FILES)" \
		./scripts/check-proto-drift.sh

gen-sql:
	@test "$$($(SQLC) version)" = "$(SQLC_VERSION)"
	rm -f $(GENERATED_SQL_FILES)
	$(SQLC) generate

check-sql-drift: verify-tools
	@MAKE_COMMAND="$(MAKE)" \
		TOOLS_BIN="$(TOOLS_BIN)" \
		SQL_SCHEMA="$(SQL_SCHEMA)" \
		SQL_QUERIES="$(SQL_QUERIES)" \
		GENERATED_SQL_FILES="$(GENERATED_SQL_FILES)" \
		./scripts/check-sql-drift.sh

lint: verify-tools
	GOWORK=off $(GOLANGCI_LINT) run --config "$(GOLANGCI_CONFIG)" ./...

test:
	@test -n "$(strip $(TEST_PACKAGES))" || { echo "TEST_PACKAGES must not be empty" >&2; exit 1; }
	GOWORK=off $(GO) test -mod=readonly -count=1 $(TEST_PACKAGES)

test-race:
	GOWORK=off $(GO) test -mod=readonly -count=1 -race ./...

test-integration:
	@GO="$(GO)" \
		DOCKER="$(DOCKER)" \
		POSTGRES_IMAGE="$(POSTGRES_IMAGE)" \
		INTEGRATION_SUITE="$(INTEGRATION_SUITE)" \
		FIXTURE_SET="$(FIXTURE_SET)" \
		./scripts/test-integration.sh

build:
	@set -eu; \
	output_dir=$$(mktemp -d); \
	trap 'rm -rf "$$output_dir"' EXIT HUP INT TERM; \
	GOWORK=off $(GO) build -mod=readonly -o "$$output_dir/" ./...; \
	test -x "$$output_dir/server"

vulncheck: verify-tools
	GOWORK=off GOFLAGS=-mod=readonly $(GOVULNCHECK) ./...

check: verify-tools format-check format-proto-check lint lint-proto test-race test-integration build \
	check-deps check-proto-drift check-sql-drift check-proto-breaking vulncheck

run:
	GOWORK=off $(GO) run -mod=readonly ./cmd/server
