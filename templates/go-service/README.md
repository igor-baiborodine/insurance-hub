# Go service scaffold

This standalone module is the reference structure for new Insurance Hub Go services. Its module
identity is `github.com/igor-baiborodine/insurance-hub/templates/go-service`. Run every command in
this document from `templates/go-service/` with workspace mode disabled. The module has no root
workspace, filesystem replacements, legacy dependencies, database, broker, or collector.

The scaffold provides a versioned Echo contract, authentic generated Go bindings, typed
configuration, safe structured logging, optional trace export, HTTP lifecycle health, and bounded
signal-driven shutdown.

## Prerequisites

- Go 1.27.1
- GNU Make 4.3 or newer
- Network access for initial Go module and Buf Schema Registry downloads

The Makefile owns four setup and generation operations. None is an implicit prerequisite of
another target:

| Target                   | Purpose and mutations                                                                                                                                                                                                                             |
|--------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `make bootstrap-tools`   | Installs Buf 1.73.0, protoc-gen-go 1.36.12, and protoc-gen-go-grpc 1.6.2 into ignored `.tools/bin/`. It uses the Go module cache and network when artifacts are not cached.                                                                       |
| `make update-proto-deps` | Resolves the Protovalidate schema commit declared in `buf.yaml` and updates only `buf.lock`. It requires the pinned local Buf binary and BSR access unless cached.                                                                                |
| `make gen-proto`         | Verifies all local tool versions, removes and regenerates only `gen/scaffold/v1/example_service.pb.go` and `gen/scaffold/v1/example_service_grpc.pb.go`. It may fetch declared schemas unless cached; it does not install tools or change other files under `gen/` or dependency manifests. |
| `make update-deps`       | Updates only `go.mod` and `go.sum` for generated-code imports at the pinned versions, then tidies the module. It can use the network and Go module cache.                                                                                         |

Bootstrap and generate the contract in this explicit order:

```sh
GOWORK=off make bootstrap-tools
GOWORK=off make update-proto-deps
GOWORK=off make gen-proto
GOWORK=off make update-deps
```

The generators are resolved by explicit paths under `.tools/bin/`; changing the process `PATH` is
not required. `gen-proto` removes only its two declared bindings before generation and leaves every
other path under `gen/` untouched. Generated files must never be edited by hand. The checked-in
schema keeps its Go package identity explicit, and Buf managed mode is disabled.

The broader formatting, lint, security, drift, and CI interface belongs to issue 121. Repository
module/workspace topology enforcement belongs to issue 123.

## Create an independently owned service

Follow [COPYING.md](COPYING.md) to produce a renamed service without importing this scaffold's
runtime packages or relying on a workspace or filesystem replacement. The procedure updates module,
runtime, schema, generated-code, and documentation identities together and regenerates protobuf
bindings rather than editing them.

Repository maintainers own this reference. A copied service owns its code and dependencies and
adopts later scaffold fixes through normal review; copies do not update automatically.

## Runtime configuration

Settings are read once from the process environment. An explicitly empty setting is invalid unless
the optional OTLP endpoint is empty while telemetry is disabled. Diagnostics name the setting and
do not repeat its supplied value.

| Setting                       | Default          | Validation                                                                                                                                                                                                                       |
|-------------------------------|------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `SERVICE_NAME`                | `go-service`     | Non-empty; change it when copying the scaffold.                                                                                                                                                                                  |
| `GRPC_ADDR`                   | `127.0.0.1:9090` | Valid host and numeric port; port `0` is available for tests.                                                                                                                                                                    |
| `HEALTH_ADDR`                 | `127.0.0.1:8080` | Valid host and numeric port; port `0` is available for tests.                                                                                                                                                                    |
| `LOG_LEVEL`                   | `info`           | A level accepted by `slog.Level`, such as `debug`, `info`, `warn`, `error`, or a documented level offset.                                                                                                                        |
| `SHUTDOWN_TIMEOUT`            | `10s`            | Positive Go duration.                                                                                                                                                                                                            |
| `OTEL_ENABLED`                | `false`          | Boolean. Disabled mode creates no exporter and needs no collector.                                                                                                                                                               |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | unset            | Required when telemetry is enabled. Use an `http` or `https` OTLP/gRPC URL with a host and numeric port, such as `http://127.0.0.1:4317`; do not append `/v1/traces`. Credentials, query parameters, and fragments are rejected. |
| `OTEL_EXPORTER_OTLP_TIMEOUT`  | `5s`             | Positive Go duration bounding exporter initialization, flush, and shutdown.                                                                                                                                                      |

Logs are JSON and always include `service`. When the supplied context contains a valid span, logs
also include `trace_id` and `span_id`. Common credential, token, request/response payload, customer,
policy, and payment attribute keys are redacted recursively. Callers must still keep sensitive data
out of log messages and deliberately select only safe fields.

Tracing uses W3C trace-context propagation. Enabled mode exports spans over OTLP/gRPC; disabled mode
is a local no-op. The service lifecycle owns the returned provider and must call its shutdown method
with the remaining overall shutdown budget.

## Run and stop the service

Start the executable from this module root:

```sh
GOWORK=off go run ./cmd/server
```

The default gRPC listener is `127.0.0.1:9090`. The separate management listener on
`127.0.0.1:8080` exposes only `GET /livez` and `GET /readyz`. Liveness returns 200 while the
management server accepts requests. Readiness returns 200 after both listeners start and 503 once
shutdown begins. It represents lifecycle state only and does not simulate dependency checks.

An interrupt or `SIGTERM` first withdraws readiness and asks gRPC to stop accepting new work while
in-flight RPCs drain. Half of `SHUTDOWN_TIMEOUT` is reserved for graceful gRPC drain. The remaining
budget covers forced gRPC stop when required, management shutdown, and telemetry cleanup;
management remains reachable during the drain so `/readyz` can report 503. The executable then
closes management HTTP and telemetry within the original overall deadline.

## Example RPC

The generated `scaffold.v1.ExampleService` exposes unary `Echo`. A valid request contains a UTF-8
message from 1 through 128 bytes, and the response returns the same bytes. Protovalidate enforces
the schema at the gRPC boundary before the Echo handler runs. Empty or oversized input returns
`InvalidArgument`; cancellation and deadlines keep their gRPC status; unexpected handler failures
return `Internal` with the stable message `internal error`.

The transport logs neither request/response bodies nor underlying failure details. Its server
stats handler uses the explicitly supplied tracer provider and W3C propagator, while metrics remain
disabled through an explicit no-op meter provider. Reflection, profiling, authentication, and
production exposure are not enabled by this scaffold.
