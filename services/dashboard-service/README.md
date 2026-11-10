<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Dashboard service](#dashboard-service)
  - [Capabilities and request path](#capabilities-and-request-path)
  - [Package map](#package-map)
  - [Configuration](#configuration)
  - [Development commands](#development-commands)
  - [Repository and deployment ownership](#repository-and-deployment-ownership)
  - [Generated inventory and provenance](#generated-inventory-and-provenance)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Dashboard service

`dashboard-service` is the private Go Dashboard process. It exposes total-sales, agent-sales and
sales-trends queries and optionally runs the registration worker. Local-dev and QA use the single
ordinary `policy_stats` index, `policy-registered` topic and `dashboard-service` consumer group.
The retained Java Dashboard source and manifests are separate legacy assets and are not used by the
Go service workflow.

The standalone module is
`github.com/igor-baiborodine/insurance-hub/services/dashboard-service`. It resolves the published
Servicekit `v1.0.1` dependency without a workspace, local replacement or import from another
service.

## Capabilities and request path

The editable protobuf source is `api/dashboard/v1/dashboard_service.proto`; generated gRPC, gateway
and Swagger 2.0 outputs live under `gen/dashboard/v1` and `gen/openapiv2/dashboard/v1`.

- Direct gRPC: `dashboard.v1.DashboardService/GetTotalSales`,
  `dashboard.v1.DashboardService/GetAgentSales` and
  `dashboard.v1.DashboardService/GetSalesTrends`.
- Private HTTP gateway: `POST /dashboard/totalsales` with date-only `saleDateFrom` and
  `saleDateTo` strings plus optional exact `productCode`.
- Private HTTP gateway: `POST /dashboard/agentssales` with the same required dates plus independent
  optional exact `agentLogin` and `productCode` filters.
- Private HTTP gateway: `POST /dashboard/trends` with the same required dates, optional exact
  `productCode`, and required `DAY`, `WEEK`, `MONTH` or `YEAR` `aggregationUnitCode`.
- Management: `GET /livez` and `GET /readyz` on the separate management listener.

The HTTP adapter validates the accepted 4,096-byte request and media contract, then calls the
actual local gRPC listener. Total sales maps exact RPC minor units to its legacy two-place numeric
envelope. Agent sales preserves canonical decimal tokens through RPC and emits them as legacy JSON
numbers without rounding. Sales trends converts the Elasticsearch aggregate once with
Java-compatible HALF_UP rounding and emits a two-place legacy JSON number. A trend `periodDate`
array uses the configured IANA time zone, while `period` retains the independent UTC backend label.
Diagnostics never include request values, periods, agent logins, amounts, credentials, query bodies
or upstream response content.

```mermaid
flowchart LR
    caller["Private HTTP caller"] -->|":8080"| gateway["internal/gateway"]
    gateway -->|"generated client over loopback"| listener["gRPC listener :9090"]
    direct["Private RPC caller"] -->|":9090"| listener
    listener --> query["internal/grpc three sales reads"]
    query --> usecase["internal/application"]
    usecase --> reader["internal/elasticsearch"]
    reader --> es["policy_stats"]
    broker[("Kafka TLS")] --> worker["internal/kafka registration worker"]
    worker --> projection["internal/application projection"]
    projection --> writer["separate Elasticsearch writer"]
    writer --> selected["selected policy_stats index"]
    probes["Kubelet or management caller"] -->|":8081"| health["livez and readyz"]
    entry["cmd/server"] --> composition["internal/service"]
    composition --> gateway
    composition --> listener
    composition --> health
    composition --> telemetry["internal/telemetry"]
```

Solid arrows show runtime calls. `cmd/server` loads settings and delegates resource construction to
`internal/service`. The composition owner acquires listeners, the gateway channel and telemetry,
then conditionally acquires the separate writer and one Kafka worker. Shutdown stops and commits
the worker before the documented HTTP-first RPC drain.

## Package map

| Package or path | Responsibility |
| --- | --- |
| `cmd/server` | Load settings, construct identity-aware logging and telemetry, own signals and exit |
| `internal/config` | Compose typed common settings, Dashboard identity and Elasticsearch policy |
| `internal/gateway` | Strict HTTP decoding, generated client registration, metadata stripping and safe errors |
| `internal/grpc` | Register all three sales operations, enforce request validation and map safe gRPC statuses |
| `internal/application` | Own three query use cases plus the received registration projection and narrow ports |
| `internal/domain` | Own date/filter presence, aggregation units, periods, counts, projection values and exact decimal tokens |
| `internal/elasticsearch` | Execute bounded analytics reads and refreshed whole-document replacement through separate clients on the ordinary index |
| `internal/kafka` | Inspect ordinary progress before group acquisition and consume the registration topic with bounded commit and stop behavior |
| `internal/service` | Compose listeners/providers/query dependencies and the conditional worker, readiness, rollback and drain |
| `internal/logger` | Emit safe completion records with Dashboard runtime identity |
| `internal/telemetry` | Own bounded HTTP/RPC, Elasticsearch, registration, commit-failure and worker-state signals over Servicekit providers |
| `api/dashboard/v1` | Editable protobuf and HTTP annotation source |
| `gen/dashboard/v1` | Generated Go, gRPC and gateway code; never edit manually |
| `gen/openapiv2/dashboard/v1` | Generated Swagger 2.0 contract; never edit manually |

## Configuration

Settings are read once from the environment. Empty values fail unless the optional telemetry
endpoint is empty while telemetry is disabled. Diagnostics name a setting without printing its
value.

| Group | Settings and accepted behavior |
| --- | --- |
| Identity | `SERVICE_NAME=dashboard-service`, `SERVICE_NAMESPACE=insurance-hub`, `IMPLEMENTATION=go`, `SERVICE_VERSION`, `SERVICE_INSTANCE_ID`, `DEPLOYMENT_ENVIRONMENT` |
| Listeners | `HTTP_ADDR=127.0.0.1:8080`, `GRPC_ADDR=127.0.0.1:9090`, `HEALTH_ADDR=127.0.0.1:8081`; nonzero values must be distinct |
| Request and probes | `REQUEST_TIMEOUT=8s`, `PROBE_TIMEOUT=2s`, `SHUTDOWN_TIMEOUT=10s` |
| HTTP limits | Header/body `16384`/`4096` bytes; read-header/read/write/idle `5s`/`10s`/`10s`/`30s` |
| gRPC limits | Receive/send `1048576`/`8388608` bytes |
| Elasticsearch | `ELASTICSEARCH_URL=http://127.0.0.1:9200`, fixed `ELASTICSEARCH_INDEX=policy_stats`, optional paired `ELASTICSEARCH_USERNAME`/`ELASTICSEARCH_PASSWORD`, HTTPS-only `ELASTICSEARCH_CA_FILE`, `ELASTICSEARCH_TIMEOUT=5s` maximum |
| Registration worker | `EVENT_WORKERS_ENABLED=false|true`; when enabled, the fixed coordinates are `policy_stats`, `policy-registered` and `dashboard-service`, with ordinary reader/writer Secrets. Startup requires stable commits, complete in-range progress and no active member before group acquisition; new log-end arrivals may increase the backlog. |
| Sales trends | Required `SALES_TRENDS_TIME_ZONE`; exact available IANA name loaded once at startup. Empty, `Local`, whitespace-altered and unknown values fail. Local-dev and QA use `GMT`. |
| Telemetry | `OTEL_ENABLED=false`, optional `OTEL_EXPORTER_OTLP_ENDPOINT`; exporter timeout defaults to `2s` and accepts positive Go duration syntax. Deployed configuration omits `OTEL_EXPORTER_OTLP_TIMEOUT`, and deployment guards reject an override. |
| Logging | `LOG_LEVEL=info`; safe structured fields only |

`git-000000000000`, `unconfigured` and `development` are unresolved templates. Image, deployment
and runtime acceptance owners must replace them with the verified source-derived version, Pod or
process identity and target environment. Wildcard binds are normalized to loopback for the
process-owned gateway. Readiness requires the gateway channel and read-only visibility of the exact
Elasticsearch index; liveness remains independent of Elasticsearch and an optional collector.

## Development commands

Prerequisites are Go 1.27.3, managed CPython 3.14.8, uv 0.13.1, GNU Make 4.3 or newer, Git and a C
compiler for race tests. All operations use this module's Makefile. `GO`, `PYTHON` and `UV` may
select verified executables.

Explicit setup and source mutations:

```sh
make -C services/dashboard-service bootstrap-tools
make -C services/dashboard-service update-proto-deps
make -C services/dashboard-service gen-proto
make -C services/dashboard-service update-deps
make -C services/dashboard-service format FORMAT_SCOPE=all
```

`bootstrap-tools` installs pinned tools only under ignored `.tools/`. Normal checks never install
tools, update dependencies, rewrite generated output or deploy.

```sh
make -C services/dashboard-service check FORMAT_SCOPE=all
make -C services/dashboard-service test-tooling
make -C services/dashboard-service image-check

make go-svc-deploy-check SVC_NAME=dashboard
make go-svc-deploy-test-tooling SVC_NAME=dashboard
make go-svc-deploy-smoke SVC_NAME=dashboard
```

`check` covers exact pins, Go/protobuf formatting and lint, race-enabled tests, build, dependency
consistency, generated-output drift, protobuf compatibility, gateway/Swagger agreement, Python
tooling, Servicekit selection and reachable vulnerabilities. `test-tooling` uses disposable copies
to show that controlled defects fail the owning gates. `image-check` builds the exact HEAD image,
checks its OCI source identity and non-root runtime, and exercises all three sales operations
against an owned Elasticsearch fixture. It proves the reader cannot write, administer the cluster
or read another index; dependency loss withdraws readiness while liveness remains available and
the business response stays safe; shutdown remains bounded.

`make -C services/dashboard-service test-event-integration` owns disposable TLS Kafka and
Elasticsearch, creates only the ordinary topic/group/index, runs the production registration
worker, and exercises the resulting document through direct RPC and the generated HTTP-to-RPC
gateway for all three cumulative queries. Its index setup and cleanup are fixture administration;
normal serving never creates, seeds or remaps state.

Registration telemetry follows the Policy Search provider pattern with Dashboard-specific names
and one fixed `registered` series. It exports bounded outcome/duration, worker enabled/in-flight
age and commit-failure metrics. Idle means enabled state with zero in-flight age and no event-rate
increase; disabled reports enabled zero; a stalled worker has increasing in-flight age. Missing
runtime samples indicate telemetry loss. Logs, traces and labels omit event bodies, policy/customer
values, amounts, topics, groups, credentials and raw dependency errors.

`go-svc-deploy-smoke` loads that exact image plus digest-pinned Elasticsearch and Kafka images into
the configured Kind cluster, then creates one uniquely owned namespace. It proves the disabled
query deployment, enables the ordinary registration path, checks reader/writer permissions and
Kafka TLS/hostname handling, and registers three synthetic events including a same-ID replacement.
It exercises all three query operations, restarts the Go deployment, verifies retained documents
and offsets, reruns setup to prove it preserves populated state, and registers one follow-up event.
Its fixture setup is explicit test administration; serving never creates or seeds state. The smoke
removes its namespace and does not establish a local-dev or QA deployment result. The guarded
deployment stage requires the exact target ECK Service, reader Secret and QA public-CA copy before
mutation. `go-svc-deploy-verify` creates and removes bounded qualification Pods and one untrusted
namespace to prove allowed and denied caller identities plus the reader's effective read-only
privileges. The
[deployment runbook](deployment-runbook.md) records
those side effects, coordinates and cleanup.

Run locally with accepted identity values:

```sh
SERVICE_VERSION=git-123456789abc \
SERVICE_INSTANCE_ID=dashboard-local \
DEPLOYMENT_ENVIRONMENT=local-dev \
SALES_TRENDS_TIME_ZONE=GMT \
make -C services/dashboard-service run
```

## Repository and deployment ownership

Repository topology, CI selection, active Servicekit-consumer coverage and generic service
dispatch/release registration are owned at the repository root. The service owns its image,
render/check and guarded direct local-dev operations. QA remains Flux-owned; direct QA apply,
image load, stop and Flux suspension are forbidden.

Before either shared target is deployed or verified, its environment administrator provisions the
dedicated Elasticsearch reader through the Dashboard owner. `reader-ensure` is idempotent for
fully absent or fully owned state, `reader-verify` is read-only, and `reader-rotate` is the explicit
recovery path when an owned reader identity exists without a recoverable workload Secret. Each
mutation requires the environment-specific `DASHBOARD_READER_CONFIRM` value documented in the
[deployment runbook](deployment-runbook.md). The
owner never creates or changes `policy_stats`, prints credentials, or places credentials on a host
command line.

Ordinary registration administration is owned by the service-local `registration-inspect`,
`registration-ensure` and `registration-verify` targets. It inspects the existing index and
stable Kafka topic/group progress, reuses the issue-287 reader, and provisions the issue-293
exact-index writer plus ordinary workload and Kafka-trust Secrets. Ensure creates the exact empty
mapping, topic, roles and trust only when absent, preserves compatible populated state, and rejects
foreign ownership or incompatible state. It never seeds documents.

The guarded local-dev sequence uses catalog service `dashboard`, context
`kind-local-dev-insurance-hub` and namespace coordinate `local-dev-all`:

```sh
export DEPLOY_ENV=local-dev
export DEPLOY_CONTEXT=kind-local-dev-insurance-hub
export DEPLOY_SVC_NS=local-dev-all
export DEPLOY_DATA_NS=local-dev-all
: "${DEPLOY_IMAGE_REF:?set the exact source-derived local Dashboard image reference}"
make go-svc-deploy-preflight SVC_NAME=dashboard
make go-svc-image-load SVC_NAME=dashboard
make go-svc-deploy SVC_NAME=dashboard
make go-svc-deploy-status SVC_NAME=dashboard
make go-svc-deploy-verify SVC_NAME=dashboard
```

When replacing an existing local release, export its exact running image as
`DEPLOY_PREVIOUS_IMAGE_REF` before preflight. Keep it set through `go-svc-deploy`, then unset it
before status and verification. The deployment runbook contains the complete command sequence.

`go-svc-stop` scales only the Go Dashboard Deployment to zero. The Go workload uses the non-surging
`Recreate` strategy, and the edge route remains independently owned. The
[Dashboard deployment runbook](deployment-runbook.md) owns the complete procedure and budgets.

The [Dashboard observability runbook](observability-runbook.md) owns monitoring validation,
guarded QA provisioning/read-back, bounded investigation and restoration. It preserves the
accepted pilot thresholds and the limitation that worker state, refreshed reads and broker
offsets do not prove continuous backlog or projection completeness.

## Generated inventory and provenance

Generation owns exactly:

```text
gen/dashboard/v1/dashboard_service.pb.go
gen/dashboard/v1/dashboard_service.pb.gw.go
gen/dashboard/v1/dashboard_service_grpc.pb.go
gen/openapiv2/dashboard/v1/dashboard_service.swagger.json
```

`COPYING.md` intentionally retains source-template identities as historical provenance. It is not a
runtime interface. This service owns later changes through normal review; the source template does
not update it automatically.
