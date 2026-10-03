.DEFAULT_GOAL := bootstrap-tools

GO ?= go

TOOLS_DIR := $(CURDIR)/.tools
TOOLS_BIN := $(TOOLS_DIR)/bin
BUF := $(TOOLS_BIN)/buf
PROTOC_GEN_GO := $(TOOLS_BIN)/protoc-gen-go
PROTOC_GEN_GO_GRPC := $(TOOLS_BIN)/protoc-gen-go-grpc

BUF_VERSION := v1.73.0
PROTOC_GEN_GO_VERSION := v1.36.12
PROTOC_GEN_GO_GRPC_VERSION := v1.6.2

PROTOBUF_GO_VERSION := v1.36.12
GRPC_GO_VERSION := v1.84.0
PROTOVALIDATE_GO_VERSION := v1.4.0
PROTOVALIDATE_SCHEMA_GO_VERSION := v1.36.12-20260825204119-511051f7f437.1
OTEL_VERSION := v1.46.0
OTEL_GRPC_VERSION := v0.71.0

.PHONY: bootstrap-tools update-deps update-proto-deps gen-proto run

bootstrap-tools:
	mkdir -p "$(TOOLS_BIN)"
	GOWORK=off GOBIN="$(TOOLS_BIN)" $(GO) install \
		github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION)
	GOWORK=off GOBIN="$(TOOLS_BIN)" $(GO) install \
		google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)
	GOWORK=off GOBIN="$(TOOLS_BIN)" $(GO) install \
		google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GO_GRPC_VERSION)
	@test "$$($(BUF) --version)" = "$(patsubst v%,%,$(BUF_VERSION))"
	@test "$$($(PROTOC_GEN_GO) --version)" = \
		"protoc-gen-go $(PROTOC_GEN_GO_VERSION)"
	@test "$$($(PROTOC_GEN_GO_GRPC) --version)" = \
		"protoc-gen-go-grpc $(patsubst v%,%,$(PROTOC_GEN_GO_GRPC_VERSION))"

update-deps:
	GOWORK=off $(GO) get \
		buf.build/go/protovalidate@$(PROTOVALIDATE_GO_VERSION) \
		buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go@$(PROTOVALIDATE_SCHEMA_GO_VERSION) \
		go.opentelemetry.io/otel@$(OTEL_VERSION) \
		go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc@$(OTEL_GRPC_VERSION) \
		go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc@$(OTEL_VERSION) \
		go.opentelemetry.io/otel/sdk@$(OTEL_VERSION) \
		google.golang.org/grpc@$(GRPC_GO_VERSION) \
		google.golang.org/protobuf@$(PROTOBUF_GO_VERSION)
	GOWORK=off $(GO) mod tidy

update-proto-deps:
	@test "$$($(BUF) --version)" = "$(patsubst v%,%,$(BUF_VERSION))"
	$(BUF) dep update

gen-proto:
	@test "$$($(BUF) --version)" = "$(patsubst v%,%,$(BUF_VERSION))"
	@test "$$($(PROTOC_GEN_GO) --version)" = \
		"protoc-gen-go $(PROTOC_GEN_GO_VERSION)"
	@test "$$($(PROTOC_GEN_GO_GRPC) --version)" = \
		"protoc-gen-go-grpc $(patsubst v%,%,$(PROTOC_GEN_GO_GRPC_VERSION))"
	rm -f \
		"gen/scaffold/v1/example_service.pb.go" \
		"gen/scaffold/v1/example_service_grpc.pb.go"
	$(BUF) generate

run:
	GOWORK=off $(GO) run -mod=readonly ./cmd/server
