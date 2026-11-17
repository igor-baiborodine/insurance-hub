<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Chat service](#chat-service)
  - [Architecture and ownership](#architecture-and-ownership)
  - [Runtime contract](#runtime-contract)
  - [Configuration](#configuration)
  - [Development and validation](#development-and-validation)
  - [Image and delivery boundary](#image-and-delivery-boundary)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Chat service

`chat-service` is the standalone Go foundation for Insurance Hub Chat. In C01 it deliberately
implements no Chat or WebSocket behavior: Java remains the only Chat route authority. The Go
business listener returns the standard-library HTTP 404 response for every request, including an
upgrade attempt to `/ws/chat/main/test`.

## Architecture and ownership

```text
cmd/server
  -> internal/config       typed environment loading and fixed identity
  -> internal/service      two listeners, health lifecycle and bounded shutdown
       -> business :8080   standard-library not-found handler
       -> management :8081 /livez and /readyz
       -> connection registry (protocol-neutral C02 seam)
  -> internal/telemetry    Servicekit telemetry provider
```

The module depends on the published `libs/servicekit` version in `go.mod`. It has no Go workspace,
filesystem replacement, template import, sibling-service private import, Java runtime dependency,
gRPC listener, protobuf surface, generated API contract, persistence or message broker.

Source-copy provenance is recorded in [COPYING.md](COPYING.md).

## Runtime contract

The process owns exactly two HTTP listeners:

| Listener | Default address | Contract |
| --- | --- | --- |
| Business | `127.0.0.1:8080` | Every request returns HTTP 404 and `404 page not found\n`. |
| Management | `127.0.0.1:8081` | `/livez` reports process liveness; `/readyz` follows lifecycle readiness. |

Readiness is published only after both listeners start. Cancellation or an unexpected server exit
withdraws readiness, rejects new business work and connection registration, stops business HTTP,
closes and joins tracked connections within the non-exporter shutdown budget, stops management and
then gives telemetry its reserved exporter budget. Cleanup errors are preserved with the initiating
failure.

The connection registry is intentionally protocol-neutral and is tested with fakes. C02 owns the
WebSocket library, route, close policy, rooms and sessions.

## Configuration

Configuration is loaded once at startup. Blank, malformed and inconsistent values fail safely
without logging raw configuration values.

| Setting | Default or rule |
| --- | --- |
| `SERVICE_NAME` | Fixed to `chat-service`. |
| `HTTP_ADDR` | `127.0.0.1:8080`; must differ from `HEALTH_ADDR`. |
| `HEALTH_ADDR` | `127.0.0.1:8081`; owns management health only. |
| `HTTP_READ_HEADER_TIMEOUT` | `5s`; positive and no greater than `HTTP_READ_TIMEOUT`. |
| `HTTP_READ_TIMEOUT` | `10s`; positive and no greater than `HTTP_WRITE_TIMEOUT`. |
| `HTTP_WRITE_TIMEOUT` | `10s`; positive. |
| `HTTP_IDLE_TIMEOUT` | `30s`; positive. |
| `HTTP_MAX_HEADER_BYTES` | `16384`; from 1024 through 1048576. |
| `PROBE_TIMEOUT` | `2s`; positive. |
| `SHUTDOWN_TIMEOUT` | `10s`; positive and greater than the exporter timeout. |
| `LOG_LEVEL` | `INFO`; a supported structured-log level. |
| `OTEL_ENABLED` | `false`. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Required only when telemetry is enabled. |
| `OTEL_EXPORTER_OTLP_TIMEOUT` | `2s`; positive and reserved from shutdown. |
| `DEPLOYMENT_ENVIRONMENT` | Defaults to `development`; explicit when telemetry is enabled. |
| `SERVICE_VERSION` | Development default locally; otherwise `git-` plus 12 nonzero-capable lowercase hexadecimal characters. |
| `SERVICE_INSTANCE_ID` | Local process identity by default; explicit when telemetry is enabled. |

The image supplies `HTTP_ADDR=0.0.0.0:8080` and `HEALTH_ADDR=0.0.0.0:8081` for its lifecycle
smoke. Kubernetes configuration is owned by the later C01 deployment step.

## Development and validation

Run commands from the repository root:

```bash
make -C services/chat-service bootstrap-tools
make -C services/chat-service test
make -C services/chat-service test-tooling
make -C services/chat-service check FORMAT_SCOPE=all
make -C services/chat-service image-check
make go-svc-deploy-check SVC_NAME=chat
make go-svc-deploy-test-tooling SVC_NAME=chat
```

`bootstrap-tools` explicitly installs only the two pinned module tools: `golangci-lint` and
`govulncheck`, plus the repository-managed Python environment. The main check covers formatting,
lint, unit/race and real-listener smoke tests, build, dependency synchronization, vulnerability
analysis, generated Dockerfile drift and Servicekit selection. Controlled defects cover only the
implemented Dockerfile, format, lint, test and dependency boundaries.

Generated-source and compatibility-breaking phases are explicit no-ops because C01 owns no
generated source or published protobuf/gateway contract. There is no active source-copy phase.

## Image and delivery boundary

`Dockerfile` is generated from `go-service-tooling.json`. It builds `/chat-service`, runs as
`65532:65532` on a distroless nonroot image, exposes only 8080 and 8081 and carries OCI source,
revision and version labels. The workload is designed for a read-only root filesystem and needs no
runtime file writes.

The generic service catalog and module-owned deployment guards render hardened, one-replica
local-dev and QA workloads with distinct business and management Services. Both environments keep
the Go workload without browser ingress; local-dev is default-denied, and QA adds only DNS and
Alloy OTLP egress. QA activates both the Go and legacy overlays, while both Vue environments retain
the legacy Chat URL as their only route authority.

`deploy-check` validates both renders, exact source-derived version mapping, legacy coexistence and
unchanged Vue authority without contacting a cluster. `deploy-test-tooling` uses fake boundaries to
exercise target selection, failure and bounded cleanup. Live deployment commands require exact
catalog coordinates and separate authorization. Until route transfer in C03, the Go service must
not be made a browser/Nginx Chat upstream. Java retirement is a separate, one-time manual C04
operation and never removes legacy source or repository manifests.
