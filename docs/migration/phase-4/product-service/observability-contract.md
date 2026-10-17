<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Product Observability Design Contract](#product-observability-design-contract)
  - [Environment and ownership contract](#environment-and-ownership-contract)
  - [Decisions and readiness gates](#decisions-and-readiness-gates)
  - [Java trace-context inspection and decision](#java-trace-context-inspection-and-decision)
  - [Identity and field-schema decision](#identity-and-field-schema-decision)
    - [Resource identity](#resource-identity)
    - [Span names and attributes](#span-names-and-attributes)
    - [Log fields and Loki labels](#log-fields-and-loki-labels)
    - [Metric names, units, buckets, and dimensions](#metric-names-units-buckets-and-dimensions)
    - [Alloy version and exporter identity mapping](#alloy-version-and-exporter-identity-mapping)
    - [Cardinality and backend contract](#cardinality-and-backend-contract)
  - [Metrics route inspection and accepted decision](#metrics-route-inspection-and-accepted-decision)
    - [Alloy versus direct Prometheus delivery for Insurance Hub](#alloy-versus-direct-prometheus-delivery-for-insurance-hub)
      - [What the alternatives mean](#what-the-alternatives-mean)
      - [Project facts that determine the tradeoff](#project-facts-that-determine-the-tradeoff)
      - [A: Through Alloy benefits and costs](#a-through-alloy-benefits-and-costs)
      - [B: Direct OTLP/HTTP benefits and costs](#b-direct-otlphttp-benefits-and-costs)
      - [C: Direct scrape benefits and costs](#c-direct-scrape-benefits-and-costs)
      - [Failure, security, and measurement implications](#failure-security-and-measurement-implications)
      - [Recommendation and conditions for revisiting it](#recommendation-and-conditions-for-revisiting-it)
  - [Pod log collection decision and QA revalidation](#pod-log-collection-decision-and-qa-revalidation)
  - [Pilot acceptance budget decision](#pilot-acceptance-budget-decision)
    - [Workload and sampling](#workload-and-sampling)
    - [Application and collector delivery limits](#application-and-collector-delivery-limits)
    - [Healthy, failure, recovery, and resource budgets](#healthy-failure-recovery-and-resource-budgets)
    - [Pilot alert rules and no-data policy](#pilot-alert-rules-and-no-data-policy)
  - [Operational ownership decision](#operational-ownership-decision)
    - [Historical current-state inspection and handoff gate](#historical-current-state-inspection-and-handoff-gate)
    - [Source and control-plane ownership](#source-and-control-plane-ownership)
    - [Staged delivery and reconciliation](#staged-delivery-and-reconciliation)
    - [Rollback and abort ownership](#rollback-and-abort-ownership)
    - [Evidence handoff](#evidence-handoff)
  - [Dependencies and completion boundaries](#dependencies-and-completion-boundaries)
  - [Repository paths and verification interface](#repository-paths-and-verification-interface)
  - [Requirements traceability](#requirements-traceability)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Product Observability Design Contract

**Issue:** [#135 — Complete Product observability through Alloy](https://github.com/igor-baiborodine/insurance-hub/issues/135).

**Status:** Normative detailed design for issue #135. All recorded technical decisions are
accepted, including option A (Alloy) explicitly selected by the maintainer on 2026-10-16.
The metrics-route design gate is closed; implementation, deployment, and AC135-01–07 evidence
remain outstanding.

The GitHub issue owns the objective, M01–M11 requirements, acceptance criteria, exclusions, and
dependencies. This contract owns the detailed propagation policy, telemetry identity and field
schemas, ingestion routes, cardinality and resource budgets, alert thresholds, and operational
ownership incorporated by that issue. Any change that affects scope, security, acceptance, or an
out-of-scope boundary must update both the issue and this document.

Historical repository and QA observations below are dated discovery evidence. Revalidate effective
versions, resources, routes, clocks, and control-plane state before implementation. Runtime results
belong in issue acceptance evidence rather than this design contract.

## Environment and ownership contract

| Item | local-dev | QA |
| --- | --- | --- |
| Kubernetes context | `kind-local-dev-insurance-hub` | `qa-insurance-hub` |
| Product namespace | `local-dev-all` | `qa-svc` |
| Normal Go telemetry mode | Explicit disabled/local diagnostics; disposable enabled tests | Enabled through Alloy for deployed acceptance |
| Legacy Product tracing | Overlay explicitly disables Zipkin tracing | Overlay enables tracing to `qa-alloy.qa-monitoring.svc.cluster.local:9411` |
| Monitoring destination | No QA dependency | Alloy in `qa-monitoring`; Loki, Tempo, and Prometheus backends |
| Service deployment owner | Explicit Make/Kustomize | #134's release/Flux procedure |
| Collector configuration owner | Owned disposable test resources | Existing monitoring Make/Helm procedure, verified before changes |

These are configuration coordinates, not proof of current live state. Verify reachable contexts,
effective versions, service endpoints, ownership, and resource capacity before environment work.
Do not switch or start environments implicitly. Ensure the previous environment and task-owned
forwards are stopped before running the next environment.

## Decisions and readiness gates

Mandatory scope and the recorded design decisions are accepted. The maintainer accepted the
Alloy metrics route on 2026-10-16. The table records the remaining implementation and verification
obligations; design acceptance does not supply runtime evidence.

| Decision                  | Starting direction                                                                                               | Required resolution                                                                                                                                                                                                                                                                  |
|---------------------------|------------------------------------------------------------------------------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Log route                 | **Reaffirmed:** container stdout/stderr→Alloy Kubernetes API reader→target deduplication→JSON processing→existing Loki writer | Implement the Product-only route and replace Alloy's broad ClusterRole with the rendered `qa-svc` Role. Then validate the deployed Go target, parsing, correlation, duplication, volume, restart, and loss behavior described below. No application OTLP-log exporter is proposed. |
| Metrics route             | **Accepted 2026-10-16, option A:** retain OTLP/gRPC→Alloy→Prometheus remote write and enable only Prometheus's remote-write receiver | Implement the bounded label/temporality rules and prove an identified counter and histogram through Alloy, including outage, restart, loss, and duplicate behavior. Design acceptance is complete; implementation and runtime evidence remain required. |
| Java context              | **Decided:** retain W3C and add B3 single/multi extraction plus B3 multi injection at the Go HTTP compatibility boundary; valid B3 wins conflicts | Implement the policy below, correct the Java QA sampler property, and prove the controlled HTTP/gRPC matrix without changing production gateway routing or adding a production Go→Java dependency.                                                                                   |
| Identity and field schema | **Decided:** retain logical `product-service`; distinguish `go|java` with an application-owned resource attribute; consume #154's Alloy chart `1.13.1`/Alloy `v1.20.1` baseline | Verify #154's accepted Alloy baseline before applying the Product schema, then implement the resource, span, log, metric, backend-mapping, and cardinality contract below. Correct mutable version labels, replace deprecated collector attributes, and prove Kubernetes enrichment and exact backend fields before acceptance. |
| Pilot acceptance budgets  | **Decided:** use the fixed healthy-load, failure/recovery, buffering, resource, overhead, and alert budgets below | Implement the selected SDK/Alloy limits and alert rules, then execute the paired disposable and deployed-QA checks. Treat every number as a pass/fail ceiling fixed before measurement; a failed result requires a reviewed design or budget change, not a post-run exception. |
| Operational ownership     | **Decided:** Product application state remains generic-release/Flux-owned; monitoring remains Make/Helm-owned | Follow the source, staged delivery, reconciliation, rollback, and evidence matrix below. Do not directly apply Product resources or move monitoring under Flux. Revalidate #134's completed handoff before QA telemetry enablement. |

Read-only discovery and disposable measurements may establish these decisions. Record and resolve
any new contract, security, retention, or scope change before implementation. Numeric limits must
be explicit before acceptance testing; observed results must not retroactively define a pass.

## Java trace-context inspection and decision

**Decision:** Treat the legacy Java HTTP path as B3 and the modern Go path as W3C Trace Context.
The Go Product HTTP boundary must extract W3C `traceparent`/`tracestate` and both B3 encodings, with
B3 taking precedence when a request contains valid contexts from both families. A test-only Go
HTTP consumer must inject W3C and B3 multi headers from the same span context when calling Java.
Keep the production Go gRPC boundary W3C-only because no legacy Java Product gRPC caller exists.
Do not change the Java propagation factory, production gateway routing, or authorization behavior.

Use a transport-specific Go HTTP propagator equivalent to:

```go
propagation.NewCompositeTextMapPropagator(
    propagation.TraceContext{},
    b3.New(b3.WithInjectEncoding(b3.B3MultipleHeader)),
)
```

Pin `go.opentelemetry.io/contrib/propagators/b3` to the version aligned with the Product's
OpenTelemetry core modules. The composite order is part of the contract: extraction runs W3C
first and B3 second, so a valid B3 context replaces a valid conflicting W3C context. Injection
writes W3C and B3 multi headers from one current span, so their trace ID, span ID, and sampling
decision agree. The B3 propagator accepts 16- or 32-character trace IDs, pads 64-bit B3 IDs for
OpenTelemetry's 128-bit representation, and accepts single and multi encodings. Within B3, a valid
single `b3` header takes precedence; if it is malformed, extraction may fall back to valid B3
multi headers.

The precedence is required for the migration path. The legacy gateway's Brave client filter
injects B3 multi headers, while a direct or forwarding caller can also leave `traceparent` on the
HTTP request. Choosing W3C in that conflict could detach the Go server span from the Java client
span. This policy is only trace correlation: trace headers remain untrusted telemetry input and
must never supply caller identity, roles, authentication, or authorization.

Apply these malformed and sampling rules:

- A valid B3 context wins over W3C. If B3 is absent or malformed and W3C is valid, retain W3C.
  If neither family contains a valid context, start a new trace; do not reject or fail the request.
  Do not log raw propagation header values.
- Honor an explicit remote sampled decision. `X-B3-Sampled: 1`, B3 single sampled `1`, or a W3C
  sampled flag records the child subject to exporter health; a remote unsampled decision remains
  unsampled. Treat B3 debug as sampled. Java emits a sampling decision on its actual client path.
- B3 identifiers without a sampling decision are outside the observed Java client contract. The
  selected OpenTelemetry propagator exposes that remote context as unsampled while retaining an
  internal deferred marker; the parent-based SDK therefore does not record the Go child. Make this
  behavior explicit in a unit test; do not add a custom tri-state sampler solely for that unobserved
  case.
- Use an explicit parent-based Go sampler. Set the root ratio to `1.0` in QA and disposable enabled
  acceptance tests so absent/malformed-context cases are deterministic. Keep normal local telemetry
  disabled. A later production ratio is outside this pilot and must not alter an incoming parent
  decision.
- Correct the legacy gateway and Product QA sampler setting to
  `tracing.zipkin.sampler.probability=1.0` through their owned configuration. Their checked-in YAML
  currently nests `sampler` below `tracing.zipkin.http`, which Micronaut does not bind. Verify the
  effective property after rollout. Do not infer sampling from the comment beside the misplaced
  key.
- Retain Java's current 64-bit root trace-ID default. The Go B3 extractor must prove 64-bit
  normalization and 128-bit pass-through against the backend. Do not change Java trace-ID width
  merely to simplify a test.
- Account for Brave's default shared-span model. On Java→Go, the Go server creates a child of the
  Java client span. On Go→Java, Brave may join the incoming B3 client span ID instead of creating a
  distinct server span ID. Validate the trace tree according to those semantics rather than
  requiring a distinct Java server child.

The inspection at repository revision `c37ffdc` and live QA context `qa-insurance-hub` established
the following basis:

| Area | Observed evidence | Decision consequence |
| --- | --- | --- |
| Java libraries | Both legacy modules use Micronaut tracing `2.4.0`; the resolved and shaded runtime classes match Brave `5.13.3`. Micronaut creates `Tracing.newBuilder()` without a custom propagation bean, and Brave defaults to `B3Propagation.FACTORY`. | Java HTTP extraction is B3 single then B3 multi; HTTP client injection is B3 multi. Java does not extract W3C `traceparent`. |
| Live configuration | The legacy gateway image `1.2.0` and Product image `1.5.0` export Zipkin spans to `qa-alloy.qa-monitoring.svc.cluster.local:9411`. Alloy delivers the controlled spans to both Tempo and Zipkin. | Reuse the existing Java→Alloy route. Propagation compatibility is required in Go, not in the collector. |
| Product runtime probe | B3 multi and B3 single requests with known 128-bit trace IDs each produced the expected `product-service` server span in Tempo and Zipkin. A W3C-only known trace ID was absent. With conflicting valid B3/W3C IDs, only the B3 trace ID was stored. All controlled requests, including malformed B3, returned HTTP 200; an explicit B3 unsampled trace was absent from both backends. | Runtime behavior confirms B3 support, B3-over-W3C precedence, non-failing malformed handling, and honored deny sampling. |
| Gateway runtime probe | Unauthenticated `/api/products` requests returned HTTP 401. Known B3 IDs were stored, W3C-only IDs were absent, and the B3 ID won a B3/W3C conflict. The backend returned two gateway records sharing the supplied ID for each sampled probe. | Gateway extraction matches Product. The duplicate gateway records are not proof of two logical operations; identify the Java client span by IDs/kind during the isolated continuity test and record any remaining duplicate instrumentation separately. |
| Effective Java root sampling | Micronaut metadata defines `tracing.zipkin.sampler.probability` and a `0.1` default. Both repository YAML files use the ineffective `tracing.zipkin.http.sampler.probability` nesting. In QA, only one of 20 valid B3 contexts without a supplied decision was stored, consistent with the default rather than the intended deterministic setting. | Correct and verify the QA property before acceptance. Supplied parent decisions remain authoritative. |
| Existing gateway test | `GatewayProductSecurityIT` sends B3 and W3C together while tracing is disabled, and its custom observer copies all non-hop headers. | The test proves header preservation in that security harness. It does not prove Brave extraction, precedence, injection, or a traced Java→Go parent relationship. |
| Current Go implementation | `telemetry.Provider` returns only `propagation.TraceContext{}`; the gRPC server consumes it, while the Product HTTP listener is not yet traced. | Keep the gRPC contract W3C. Add the composite propagator to HTTP instrumentation and use the same policy in the isolated Go→Java HTTP consumer. |

The controlled acceptance matrix is:

| Direction and transport | Required carrier | Expected proof |
| --- | --- | --- |
| Java gateway→Go Product HTTP, isolated route | Java-generated B3 multi | Same backend trace ID; Go server parent is the Java client span; authentication and normal Java routing remain unchanged outside the isolated test. |
| W3C client→Go Product HTTP | `traceparent` and optional `tracestate` | Go server is a child in the supplied trace and preserves the sampled decision. |
| Conflicting W3C+B3→Go Product HTTP | Valid, intentionally different contexts | B3 trace and parent win. Repeat with malformed B3 plus valid W3C and prove W3C wins. |
| B3 compatibility→Go Product HTTP | B3 single and multi; 64- and 128-bit trace IDs | Both encodings continue the supplied trace; valid single wins a B3 single/multi conflict; malformed input does not change the HTTP contract. |
| Go test consumer→Java Product HTTP | W3C plus B3 multi generated from one Go client span | Java uses B3 and reports the same trace; account for Brave shared-span/join semantics. Remove temporary deployed test routing/resources after the controlled run while retaining reproducible test tooling. |
| Go test client→Go Product gRPC | W3C trace context in gRPC metadata | Go server is a child in the supplied trace. B3-over-gRPC and Java gRPC consumers are not required acceptance paths. |
| Sampling cases on applicable HTTP/gRPC rows | Sampled, unsampled, B3 without a decision, no context, malformed context | Parent decisions are honored; explicit unsampled and deferred B3 cases produce no backend span; QA root cases use the explicit `1.0` sampler and remain request-successful. |

Use trace-ID lookup in Tempo and Zipkin for the continuity assertions; timestamps remain supporting
evidence because the QA nodes were still about four minutes ahead of the host. The
[B3 propagation specification](https://github.com/openzipkin/b3-propagation) defines its single/
multi encodings, sampling states, gRPC mapping, and single-header precedence. The
[OpenTelemetry Go B3 propagator](https://pkg.go.dev/go.opentelemetry.io/contrib/propagators/b3)
supports both extraction formats and configurable multi-header injection.

## Identity and field-schema decision

**Decision:** Keep `service.name=product-service` as the logical service name for both Product
implementations. Distinguish them with the resource attribute
`insurance_hub.service.implementation=go|java`. Use the resource identity below on traces and
metrics and equivalent flattened fields on container logs. This preserves existing service-name
queries while making implementation, environment, version, and instance explicit.

Use the OpenTelemetry semantic-convention keys supported by the pinned modules. Application-owned
fields use the `insurance_hub.*` namespace. The schema is an allowlist: adding a field, value, Loki
index label, or metric dimension requires a reviewed ticket-specification change and updated
cardinality/redaction tests.

### Resource identity

| Resource attribute | QA Go | QA Java trace | Log JSON field |
| --- | --- | --- | --- |
| `service.namespace` | `insurance-hub` | `insurance-hub` | `service_namespace` |
| `service.name` | `product-service` | `product-service` | `service_name` |
| `service.version` | Immutable build/image identity such as `sha-<git-sha>` | Actual image tag, currently `1.5.0` | `service_version` |
| `service.instance.id` | Pod UID | Pod UID | `service_instance_id` |
| `deployment.environment.name` | `qa` | `qa` | `deployment_environment_name` |
| `insurance_hub.service.implementation` | `go` | `java` | `service_implementation` |
| `k8s.cluster.name` | `qa-insurance-hub` | `qa-insurance-hub` | `k8s_cluster_name` |
| `k8s.namespace.name` | `qa-svc` | `qa-svc` | `k8s_namespace_name` |
| `k8s.pod.uid` | Pod UID | Pod UID | `k8s_pod_uid` |
| `k8s.container.name` | `product-api` | `product-api-legacy` | `k8s_container_name` |

The Go application emits its six non-Kubernetes values on trace and metric resources and in every
JSON log. Its QA Deployment supplies `service.version` and the Pod UID through explicit settings
and the Downward API. Use `deployment.environment.name=test`, a `test-<revision>` version, and a
generated UUIDv4 instance ID for disposable enabled tests. Normal local telemetry remains disabled;
local process logs use `development`, with Kubernetes fields omitted.

Alloy asserts the QA environment and cluster and enriches both receiver paths with the approved
Kubernetes fields. Route Java Zipkin input through a namespace-scoped Kubernetes-attributes
processor associated by connection, obtain version and Pod UID, then add
`service.namespace=insurance-hub` and `implementation=java`. Correct the Java Pod version label
from `latest` to its actual immutable image version. Replace the current span attributes
`deployment.environment` and `cluster.name`; do not emit old and new keys together.

Do not enable QA telemetry with blank or mutable versions such as `latest`, `unconfigured`, or
`sha-unpublished`. Do not silently overwrite a conflicting service/version/instance/implementation
identity. If Java Kubernetes association fails, preserve its existing trace delivery, expose the
enrichment miss, and fail acceptance rather than inventing `unknown`, a Pod IP, or a constant
instance ID.

### Span names and attributes

| Boundary | Allowed span names | Kind |
| --- | --- | --- |
| HTTP server | `GET /products`, `GET /products/{productCode}` | Server |
| gRPC server | `product.v1.ProductService/ListProducts`, `product.v1.ProductService/GetProduct` | Server |
| Application | `product.list`, `product.get` | Internal |
| Pool acquisition | `product.db.acquire` | Internal |
| PostgreSQL query | `SELECT products`, `SELECT product` | Client |

A successful read has one server span, one application span, one acquisition span, and one query
span. Decoding stays within the application span. Management probes and requests rejected before a
Product route is selected do not produce application/database spans.

The span-attribute allowlist is:

| Scope | Allowed attributes and values |
| --- | --- |
| Product | `insurance_hub.product.operation=list|get`; `insurance_hub.product.transport=http|grpc`; `insurance_hub.product.outcome=success|not_found|invalid_request|cancelled|deadline_exceeded|storage_unavailable|invalid_data|internal_error` |
| HTTP | `http.request.method=GET|_OTHER`; `http.route=/products|/products/{productCode}`; `http.response.status_code=200|400|404|405|500`; `url.scheme=http|https`; bounded `network.protocol.version` |
| gRPC | `rpc.system.name=grpc`; the two `rpc.method` values above; `rpc.response.status_code=OK|INVALID_ARGUMENT|NOT_FOUND|CANCELLED|DEADLINE_EXCEEDED|RESOURCE_EXHAUSTED|UNAVAILABLE|INTERNAL`; pinned instrumentation may emit one local `server.address`/`server.port` pair per instance, which Alloy removes before backend storage |
| Database | `db.system.name=postgresql`; `db.namespace=product`; `db.collection.name=public.product`; `db.operation.name=SELECT`; `db.query.summary=list products|get product`; `insurance_hub.product.database.stage=acquire|query` |
| Failure | `error.type=invalid_request|cancelled|deadline_exceeded|storage_unavailable|invalid_data|internal_error`; omit on success and expected not-found application spans |

Enable the stable HTTP/RPC/database conventions in the pinned instrumentation and do not emit the
old and stable conventions together. Filter gRPC server instrumentation to the two registered
Product methods so arbitrary method names cannot create spans. Preserve framework protocol status
semantics; dashboard error classification uses the bounded Product outcome so an expected
not-found is not a service failure. A span-status description may contain only the existing fixed
safe gRPC messages (`internal error`, `service unavailable`, `context canceled`, or
`context deadline exceeded`); never attach a wrapped error.

On the Java Zipkin path, normalize `get /products` and the single-product variant to the same HTTP
span names/routes above, convert `http.method` to `http.request.method`, and remove `http.path`,
`net.host.ip`, `deployment.environment`, and `cluster.name` before either backend export. Normalize
any other Java Product server span to `HTTP _OTHER` with no route instead of preserving a raw path
or unbounded span name. Exclude product codes, raw paths/query strings, request/trace IDs, caller
identity, headers, database addresses/URLs, SQL text/parameters, row bodies, arbitrary exceptions,
stack traces, and baggage.

### Log fields and Loki labels

Every Go JSON log contains `time` in UTC RFC3339Nano, `level`, a code-owned constant `msg`, and the
six flattened non-Kubernetes identity fields. Alloy adds the four Kubernetes fields after parsing
and checks deployment identity against the record.

Emit exactly one completion record for each routed Product request, including success. The
request-field allowlist is:

| Field | Allowed value or type |
| --- | --- |
| `operation` | `list|get` |
| `transport` | `http|grpc` |
| `outcome` | The eight Product outcomes above |
| `duration_ms` | Non-negative numeric milliseconds |
| `http_status_code` | `200|400|404|405|500`; omit when no response was written |
| `rpc_status_code` | The eight gRPC status names above |
| `error_type` | The six failure types above; omit for success/not-found |
| `trace_id`, `span_id` | Lowercase valid OpenTelemetry IDs; omit without a valid active context |

Use `INFO` for success, not-found, and invalid request; `WARN` for cancellation/deadline; and
`ERROR` for unavailable storage, invalid data, and internal failure. Do not log business completion
records for probes or unmatched paths. Safe lifecycle records may add `component`, `state`, and
validated listener-address fields. The legacy `error` field is allowed only with literal
`[REDACTED]` during bootstrap/cleanup diagnostics.

The Loki index-label allowlist is exactly:

```text
service_namespace
service_name
deployment_environment_name
service_implementation
service_version
k8s_namespace_name
k8s_container_name
level
```

Keep instance/Pod IDs, trace/span IDs, operation, outcome, status, duration, message, filename, Pod
name, and stream as JSON fields or structured metadata. They must not become index labels.

### Metric names, units, buckets, and dimensions

Use cumulative monotonic sums and explicit-bucket histograms. Record application request metrics
once at the common Product boundary; keep automatic transport meters disabled where they would
duplicate these instruments. Management probes remain outside Product business metrics.

| OTLP instrument | Type/unit | Dimensions | Expected Prometheus name |
| --- | --- | --- | --- |
| `insurance_hub.product.requests` | Counter, `{request}` | Product operation, transport, outcome; conditional `http.response.status_code` or `rpc.response.status_code` | `insurance_hub_product_requests_total` |
| `insurance_hub.product.request.duration` | Histogram, `s` | Same as request counter | `insurance_hub_product_request_duration_seconds` |
| `insurance_hub.product.database.operations` | Counter, `{operation}` | Product operation; database stage `acquire|query|decode`; database outcome | `insurance_hub_product_database_operations_total` |
| `db.client.operation.duration` | Histogram, `s` | `db.system.name=postgresql`; `db.namespace=product`; `db.collection.name=public.product`; `db.operation.name=SELECT`; Product operation; bounded `error.type` | `db_client_operation_duration_seconds` |
| `db.client.connection.count` | Observable up/down counter, `{connection}` | `db.client.connection.pool.name=product-reader`; `db.client.connection.state=idle|used` | `db_client_connection_count` |
| `db.client.connection.max` | Observable up/down counter, `{connection}` | Pool name | `db_client_connection_max` |
| `db.client.connection.timeouts` | Counter, `{timeout}` | Pool name | `db_client_connection_timeouts_total` |
| `db.client.connection.wait_time` | Histogram, `s` | Pool name | `db_client_connection_wait_time_seconds` |

Database outcomes are `success|cancelled|deadline_exceeded|storage_unavailable|invalid_data`. A
missing row is a successful database query and becomes `not_found` only at the Product boundary.

Request histogram boundaries in seconds are
`0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10`.
Database-operation and pool-wait boundaries are
`0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 10`.

Emit `go.memory.used`, `go.memory.limit`, `go.memory.allocated`, `go.memory.allocations`,
`go.memory.gc.goal`, `go.memory.gc.cycles`, `go.goroutine.count`, and `go.processor.limit` with
their semantic-convention units. `go.memory.used` has only `go.memory.type=stack|other`.
Implement `go.memory.gc.cycles` from `/gc/cycles/total:gc-cycles`; the pinned runtime helper does
not emit it. Detailed GC pauses remain deferred under `issue-135-N05`.

Before Prometheus conversion, copy only these resource fields to datapoints:

```text
service_name
service_namespace
service_version
service_instance_id
deployment_environment_name
service_implementation
k8s_cluster_name
k8s_namespace_name
```

Configure the Alloy Prometheus exporter with `resource_to_telemetry_conversion=false`,
`include_scope_info=false`, `include_scope_labels=false`, `include_target_info=true`,
`keep_identifying_resource_attributes=true`, and `add_metric_suffixes=true`. The explicit datapoint
transform remains required because `keep_identifying_resource_attributes` affects only
`target_info`, not ordinary metric series. Continue using
`container_cpu_usage_seconds_total`, `container_memory_working_set_bytes`,
`kube_pod_container_status_restarts_total`, and `kube_pod_status_ready` for platform resources.

### Alloy version and exporter identity mapping

**Decision:** Issue #154 owns the in-place upgrade from Helm chart `1.8.1`/Alloy `v1.16.1` to the
stable pair verified on 2026-10-14: chart `1.13.1`/Alloy `v1.20.1`. Issue #135 consumes and verifies
that accepted baseline; it does not repeat or broaden the platform upgrade. Keep the chart and
image pinned. The upgraded baseline removes the installed parser limitation and makes
`keep_identifying_resource_attributes=true` part of the Product metric contract.

Enable `target_info` and retain `service.name`, `service.namespace`, and `service.instance.id` on
it. Count one `target_info` series per active Product resource within the existing Prometheus
series ceilings. Ordinary Product series must still receive only the eight explicitly approved
resource fields through the datapoint transform, so queries do not require a PromQL join and the
upgrade does not authorize all-resource label conversion.

Apply the Product pipeline changes through the existing Helm/Make owner in a controlled sequence:
capture #154's release revision and effective values, render and dry-run chart `1.13.1`, validate
the complete final Alloy configuration with `v1.20.1`, then roll out and verify
Alloy readiness plus the existing Java Zipkin, Tempo, Loki, and Prometheus paths before Product
acceptance. The final Kubernetes-attribute processor must list the required metadata explicitly;
it must not depend on defaults changed in Alloy `v1.20.0`, including the singular Kubernetes label
keys and `container.image.tags` mapping.

Rollback on configuration rejection, an unavailable Alloy Deployment, existing-route regression,
unexpected duplicate/loss behavior, or a resource-budget breach. Restore the recorded #154 Helm
revision and revert the desired #135 configuration together so the next installation does not
immediately reapply the failed configuration. Record the before/after revisions, images, rendered
diff, health and delivery counters, and rollback command in the acceptance evidence.

### Cardinality and backend contract

| Area | Predeclared ceiling |
| --- | --- |
| Product values | 2 operations, 2 transports, 8 request outcomes, 3 database stages, 5 database outcomes, 6 error types, 2 HTTP routes, 5 HTTP status codes, 8 gRPC status codes |
| Go Product span names | 9 names from the table above |
| Active identity | 2 versions and 2 instances per implementation/environment, matching one replica plus one surge Pod |
| Tempo/Loki `7d` churn | 6 versions and 20 instance IDs per implementation/environment |
| Prometheus `10d` churn | 8 versions and 20 instance IDs per implementation/environment |
| Prometheus series | 800 application/runtime series per Go instance, including one `target_info` series; 1,600 during rollout; 16,000 retained over `10d` |
| Loki streams | 8 active and 24 retained Product Go streams |

Pod UID remains stable across a container restart, so PromQL must use `rate`/`increase` to handle a
counter reset on the same label set. A new rollout Pod gets a new UID, preventing duplicate series
from two simultaneous instances.

Use these canonical backend selectors:

```traceql
{ resource.service.name = "product-service" &&
  resource.insurance_hub.service.implementation = "go" &&
  resource.deployment.environment.name = "qa" }
```

```logql
{service_name="product-service", deployment_environment_name="qa",
 service_implementation="go"} | json
```

```promql
insurance_hub_product_requests_total{
  service_name="product-service",
  deployment_environment_name="qa",
  service_implementation="go"
}
```

Keep Zipkin's local endpoint `serviceName=product-service`; verify that the OTLP-to-Zipkin bridge
retains enriched resource identity as bounded tags, while Tempo remains the canonical resource
assertion. Acceptance must show one known real request with matching identity across Tempo, Loki,
and Prometheus, and prove that varied product codes and trace IDs do not change span names, metric
labels, or Loki streams.

The inspection at revision `c37ffdc` found only `service.name` on the live Java trace, mutable
version placeholders, deprecated Alloy signal attributes, and `pod`/`stream` in the current Loki
index. Prometheus showed three Java Product Pod UIDs over its `10d` retention. Installed Alloy
`v1.16.1` accepted the proposed namespace-scoped Kubernetes association, resource transforms, and
eight-key metric mapping, but rejected `keep_identifying_resource_attributes`. The selected target
Alloy `v1.20.1` accepts that exporter option. Runtime enrichment and the complete upgraded
configuration remain implementation proofs. These observations are time-bound discovery evidence;
revalidate them against the accepted #154 baseline before implementation.

## Metrics route inspection and accepted decision

**Accepted decision — option A, 2026-10-16:** The maintainer explicitly selected the Alloy route
after reviewing the project-specific comparison below. Keep the existing Product-to-Alloy
OTLP/gRPC boundary and complete this route:

```text
Product Go OpenTelemetry SDK
  -> OTLP/gRPC to qa-alloy:4317
  -> otelcol.receiver.otlp
  -> resource attributes and an explicit metric-label transform
  -> otelcol.processor.batch
  -> otelcol.exporter.prometheus
  -> prometheus.remote_write
  -> qa-prometheus ... /api/v1/write
```

The decision retains cumulative sums, explicit-bucket histograms, the eight-field resource-label
allowlist, and the existing cardinality, buffering, loss, and resource budgets. It closes the
outstanding route-design gate and allows implementation planning. It does not claim that the
receiver is enabled or that any implementation, deployment, or acceptance test has completed.

Enable the final receiver with
`prometheus.prometheusSpec.enableRemoteWriteReceiver: true` in the checked-in QA Prometheus
`values.yaml`. Keep Prometheus's native OTLP receiver disabled. Do not add a Product `/metrics`
scrape endpoint or send Product metrics directly to Prometheus: either choice would bypass Alloy
and create a second application-metrics ingestion design.

Use cumulative temporality and explicit-bucket histograms for Product OTLP metrics. These are the
OpenTelemetry OTLP exporter defaults; verify that no Product configuration overrides them. The
Alloy Prometheus exporter drops delta metrics, so add `otelcol.processor.deltatocumulative` only if
a later, explicitly accepted producer must emit delta temporality. Do not enable native histograms
or Remote Write 2.0 for this pilot without separate compatibility evidence.

Keep `resource_to_telemetry_conversion` disabled. Before the Prometheus exporter, copy only the
eight resource attributes fixed by the identity decision into metric datapoint attributes. This
includes service/environment/implementation/version/instance plus cluster and namespace, within
the fixed churn and series budgets. Do not synthesize fallbacks or turn every resource attribute
into a label. On the upgraded Alloy, also emit `target_info` with the three identifying service
attributes retained; ordinary metric queries must continue to work without joining it.

This route is internal: the live Alloy and Prometheus services are `ClusterIP` services. Enabling
the receiver does not authorize public ingress. The existing Alloy remote-write WAL and retry
metrics remain the delivery-health interface. Alloy currently stores its data under `/tmp/alloy`
without a persistent volume, so buffered metrics may be lost when its pod is replaced. That is
compatible with the ticket's non-lossless pilot boundary only if loss is bounded, visible, and
covered by the required outage/restart tests.

The accepted route builds on the following historical read-only inspection at repository revision
`c37ffdc`; these observations are not a fresh live-state report:

| Area | Observed evidence | Decision consequence |
| --- | --- | --- |
| Pinned components | The pre-#154 inspection found Alloy chart `1.8.1`/Alloy `v1.16.1`, kube-prometheus-stack `79.3.0`, and Prometheus `v3.7.3`. Issue #154 owns the transition to Alloy chart `1.13.1`/Alloy `v1.20.1`, kube-prometheus-stack `92.6.0`/Prometheus `v3.15.0-distroless`, a clean Loki chart `18.15.1`/Loki `3.7.8` deployment, and a clean Tempo chart `3.2.0`/Tempo `3.1.0` deployment. | Treat #154's accepted effective versions as the starting baseline. Stop if live QA or the handoff differs; do not silently repeat, float, or broaden platform upgrades in #135. |
| Effective Alloy route | Installed Helm values match the checked-in OTLP metrics branch and remote-write URL. Alloy is ready. | Reuse this route; no new collector or metrics backend is needed. |
| Effective Prometheus receiver | The Prometheus custom resource reports `enableRemoteWriteReceiver: false`; `/api/v1/status/flags` reports `web.enable-remote-write-receiver=false`. A POST to `/api/v1/write` returns HTTP 404 with `remote write receiver needs to be enabled`. | The missing receiver flag is the exact current blocker. Receiver acceptance and backend storage have not been proved. |
| Route activity | Alloy reports zero sent, failed, pending, retried, and enqueue-retry samples for `prometheus.remote_write.qa_prometheus`. No Product metric provider exists yet. | Zero failures currently mean no metric traffic, not a healthy delivery proof. |
| Narrow configuration change | On #154's pinned kube-prometheus-stack baseline, render `prometheus.prometheusSpec.enableRemoteWriteReceiver=true` and confirm the resulting manifest change remains limited to the intended Prometheus custom-resource behavior. | Implement the receiver fix in Prometheus values through the existing Helm owner; the operator will perform the resulting Prometheus rollout. Re-establish the narrow-diff evidence after #154 rather than relying on the earlier `79.3.0` render. |
| Retention and exposure | The live Prometheus reports `10d` retention. Alloy and Prometheus are cluster-internal services; no `qa-monitoring` NetworkPolicy was present. | Retention is sufficient for the current evidence window. Preserve ClusterIP exposure and record the existing cluster-internal trust boundary. |

The [Alloy Prometheus exporter reference](https://grafana.com/docs/alloy/latest/reference/components/otelcol/otelcol.exporter.prometheus/)
supports this exporter-to-remote-write composition, recommends explicit resource-attribute
mapping over converting all resource attributes, and documents the delta-temporality limitation.
The [OpenTelemetry OTLP metrics exporter specification](https://opentelemetry.io/docs/specs/otel/metrics/sdk_exporters/otlp/)
defines cumulative temporality and explicit-bucket histograms as defaults. Prometheus documents
[`/api/v1/write` as its remote-write receiver endpoint](https://prometheus.io/docs/prometheus/latest/storage/#remote-storage-integrations)
and requires the receiver to be explicitly enabled.

Implementation must still prove all of the following before AC135-03 can pass:

1. Apply the checked-in receiver setting through the Prometheus Helm owner and verify the new pod,
   effective flag, internal exposure, normal scrape ingestion, and rollback.
2. Verify #154's Alloy chart `1.13.1`/Alloy `v1.20.1` handoff and live image before applying #135
   configuration; verify the resulting effective values/configuration, existing routes, resource
   use, reload behavior, and rollback without repeating the platform upgrade.
3. Add the explicit Alloy metric-label transform and bounded `target_info` settings in both owned
   configuration representations, then render and validate the effective configuration.
4. Send a uniquely named cumulative counter and explicit-bucket histogram over OTLP/gRPC through
   Alloy. Verify receiver acceptance, Alloy sent/failure/pending/retry deltas, Prometheus queries,
   units, buckets, approved labels, matching `target_info`, and absence of duplicate series.
5. Repeat with real Product request, database/pool, and Go runtime metrics from the verified #134
   deployment. A synthetic metric proves only the platform route.
6. Exercise Prometheus rejection/unavailability, Alloy restart and pod replacement, recovery, and
   queued data. Record bounded loss and duplicate behavior; do not infer losslessness from the WAL.

No shared QA configuration was changed during this inspection. The receiver remained disabled, so
sending a synthetic metric would only queue/retry or drop it and could not prove backend ingestion.
The resumed QA nodes were also about four minutes ahead of the host despite reporting NTP
synchronization. Correct and recheck the clocks before using timestamp windows as acceptance
evidence.

### Alloy versus direct Prometheus delivery for Insurance Hub

**Decision rationale, 2026-10-16:** Retain the accepted Alloy route for the current Product pilot.
The main reasons are its fit with the existing deployment, shared identity enforcement, and bounded
backend-outage buffering. Direct delivery is technically viable and offers useful isolation from
Alloy failures. This comparison supports the maintainer's explicit acceptance of option A above;
options B and C remain alternatives for a future reviewed design change.

The assessment uses checkout `956b6345debdeefbb7e0ef52a882e7a427d9f757`, the completed dependency
handoffs recorded in the issue-135 reassessment, and the official references linked below. It is
source/documentation analysis, not a fresh QA inspection or a performance comparison. Current
upstream documentation explains capabilities; exact configuration must still be validated against
the repository's pinned Alloy `v1.20.1`, Prometheus `v3.15.0`, and Product SDK versions.

#### What the alternatives mean

There are two architectural choices: route Product metrics through Alloy or bypass Alloy. The
direct choice has two materially different implementations:

| Route | Metric data path | Required ingestion change |
| --- | --- | --- |
| A — Accepted Alloy route | Product OTLP/gRPC → Alloy conversion/label processing → Prometheus remote write `/api/v1/write` | Enable the remote-write receiver; complete the existing Alloy branch and Product metric provider. Keep native OTLP disabled. |
| B — Direct OTLP push | Product OTLP/HTTP → Prometheus `/api/v1/otlp/v1/metrics` | Enable native OTLP ingestion; add a metrics-specific HTTP exporter, destination, and backend translation policy. Remote-write receive is unnecessary for this Product route. |
| C — Direct scrape | Product exposes internal `/metrics`; Prometheus fetches it on a schedule | Add an exporter/handler and a selected ServiceMonitor or PodMonitor, including discovery and label mapping. Neither push receiver is needed for this route. |

Prometheus's native OTLP receiver accepts **HTTP**, so B is not achieved by pointing Product's
existing OTLP/gRPC exporter at Prometheus. It requires `--web.enable-otlp-receiver` and the metrics
path above. Keep trace export independently directed to Alloy; changing a global exporter endpoint
or protocol could otherwise redirect traces as well.
[Prometheus OTLP guide](https://prometheus.io/docs/guides/opentelemetry/).

Remote write is a different wire protocol with its own receiver flag. In A, Alloy translates OTLP
before sending remote write; Product does not implement remote write itself.
[Prometheus remote-storage interfaces](https://prometheus.io/docs/prometheus/latest/storage/#remote-storage-integrations).
For C, OpenTelemetry Go provides a Prometheus exporter, so the instrumentation can still use OTel;
scraping does not require replacing every instrument with a Prometheus client-library instrument.
[OpenTelemetry Go exporters](https://opentelemetry.io/docs/languages/go/exporters/#prometheus-experimental).

#### Project facts that determine the tradeoff

- **Alloy already has other required jobs.** Java Zipkin traces, Go traces, and the selected
  Kubernetes JSON-log route still use Alloy under B or C. Bypassing it for metrics saves incremental
  metrics processing, not the entire collector Deployment or its resource reservation.
- **The Product metrics path is unfinished under every option.** The current service has a trace
  provider and a no-op gRPC meter provider. All three routes need real request, database/pool, and
  runtime instrumentation. A is not merely a receiver-flag fix: its bounded transforms, delivery
  settings, dashboard/rules, and real-service acceptance proofs are also outstanding.
- **The topology already favors A.** The checked-in
  [Alloy values](../../../../k8s/overlays/qa/infra/alloy/values.yaml) and
  [adjacent configuration](../../../../k8s/overlays/qa/infra/alloy/config.alloy) contain OTLP
  ingestion, conversion, and a Prometheus remote-write destination. The
  [Prometheus values](../../../../k8s/overlays/qa/infra/prometheus/values.yaml) explicitly disable
  remote-write receive. The #154 handoff preserved this idle branch; its successful scrape checks
  do not establish Product metric delivery.
- **This is a constrained pilot.** QA runs on the maintainer's laptop, sequentially with local-dev.
  The contract budgets one Go replica plus one surge Pod, 800 application/runtime series per
  instance, a ten-second metric interval, and a five-request/second healthy workload. At the series
  ceiling, one sample per series per interval is approximately 80 samples/second, or 160 during
  rollout, excluding platform/collector metrics and protocol overhead. This arithmetic describes
  the intended load; it is not measured ingestion capacity.
- **Identity and cardinality matter more than business volume alone.** Java and Go share logical
  `product-service` identity. Dashboards must distinguish implementation, environment, version, and
  instance. Product codes, raw paths, trace IDs, customer/policy/payment data, and error text must
  never create metric labels. Histogram buckets multiply series even at low request rates.

#### A: Through Alloy benefits and costs

| Aspect | Benefit for Insurance Hub | Cost or limitation for Insurance Hub |
| --- | --- | --- |
| Delivery effort | Extends an installed branch and the existing OTLP/gRPC application boundary. Fits M04 and AC135-03 without redesigning their route requirement. | Still requires a shared Prometheus rollout and substantive Alloy configuration/testing. The installed idle branch is not a working Product telemetry implementation. |
| Identity and safety | One place can assert QA/Kubernetes identity, enforce allowed fields, and produce the exact eight dashboard labels across future Go services. | The collector must actually enforce the policy; using Alloy alone does not make labels safe. Source instrumentation must avoid sensitive values before export. A shared transform error can affect several services. |
| Backend interruption | Remote-write buffering/retry separates a short Prometheus interruption from the application's export loop. Product retains only bounded SDK work. | Adds a WAL, queues, retry policy, and delivery-health signals to operate. The current ephemeral collector storage does not survive Pod replacement reliably. |
| Failures and debugging | Central sent/failed/pending/retry evidence helps identify whether an export stopped before Prometheus. Backend configuration changes can stay outside service deployments. | Alloy is another failure point. Its failure or bad rollout can interrupt Product metrics alongside logs/traces; accepted OTLP input does not prove stored samples. |
| Resources and freshness | Batching and backend retries are shared infrastructure work. No new Product scrape handler is needed. | Conversion, batching, WAL I/O, and a network hop cost CPU, memory, disk, and latency on the laptop. Their incremental cost must fit the existing Alloy budget. |
| Migration | A consistent OTLP service boundary supports incremental Java/Go migration and later backend changes without exposing backend-specific URLs to each service. | This is future flexibility, not a requirement to migrate all existing Java/platform scrape metrics through Alloy. Collector/backend schema changes still need compatibility tests. |

Alloy's exporter supports explicit resource-to-datapoint mapping and drops delta-temporality
metrics. Therefore the existing cumulative and explicit-bucket contract is a compatibility choice,
and `resource_to_telemetry_conversion=false` plus the allowlist is meaningful protection against
uncontrolled label expansion.
[Alloy Prometheus exporter](https://grafana.com/docs/alloy/latest/reference/components/otelcol/otelcol.exporter.prometheus/).

The remote-write component writes a WAL and exposes configurable queues, retry, and sample-age
limits. This supplies a bounded buffer, not an exactly-once or lossless guarantee.
[Alloy remote write](https://grafana.com/docs/alloy/latest/reference/components/prometheus/prometheus.remote_write/).
For this pilot, the contract's five-minute sample-age limit and ephemeral storage remain material
constraints even though WAL maximum retention is 15 minutes. Neither a longer nominal WAL
retention nor collector readiness proves that delayed samples will reach Prometheus.

#### B: Direct OTLP/HTTP benefits and costs

| Aspect | Benefit for Insurance Hub | Cost or limitation for Insurance Hub |
| --- | --- | --- |
| Path simplicity | Removes Alloy conversion, batching, and forwarding from metrics delivery. Product and Prometheus are the two metric-path components to diagnose. | Logs and traces retain their Alloy path. Operators must maintain two signal-delivery configurations and distinguish independent failures. |
| Failure isolation | Product metrics can continue during an Alloy outage or rollout, helping diagnose a collector incident while logs/traces are interrupted. | Prometheus outages and rejections reach each Product metrics exporter directly. Reads/readiness must remain independent through bounded asynchronous export. |
| Application integration | Retains OTel instruments and a periodic metric reader; avoids a new scrape endpoint. | Requires a separate HTTP metric exporter/configuration alongside gRPC tracing. The current shared OTLP configuration cannot be assumed to support this without changes and tests. |
| Resource use | Avoids incremental Alloy metric conversion and WAL work; may reduce metric delivery delay. | Moves translation to Prometheus and backend retry/error handling to the service exporter. Alloy still runs. No CPU/memory or latency improvement has been measured. |
| Identity and safety | Prometheus can promote selected resource attributes; direct OTLP is compatible with bounded labels in principle. | Resource promotion is not Kubernetes enrichment or arbitrary field normalization. Product/Prometheus must take ownership of the policy currently assigned to Alloy, including safe resource and datapoint contents. |
| Delivery resilience | Cumulative instruments can retain totals in a surviving Product process across missed exports. | There is no intermediary WAL in this route. Historical gauge values and missed sample timestamps are not recreated by the next export; a Product restart can lose unexported cumulative observations. |

Prometheus supports selected resource promotion, translation settings, and retaining identifying
attributes on `target_info`. These controls make B feasible but do not establish identical output
to A automatically.
[Prometheus OTLP configuration](https://prometheus.io/docs/prometheus/latest/configuration/configuration/#configuration-file).
For example, the contract maps `insurance_hub.service.implementation` to `service_implementation`;
ordinary dot-to-underscore translation instead yields `insurance_hub_service_implementation`.
B needs an explicit equivalent mapping and the required Kubernetes identity from deployment or
application configuration. Test the exact metric names, suffixes, buckets, ordinary labels, scope
labels, and `target_info` against the pinned backend before reusing dashboards.

Resource promotion controls which resource values are copied to ordinary series; it is not a
general sensitive-data filter. Prometheus also places resource attributes on `target_info`.
[Prometheus resource handling](https://prometheus.io/docs/guides/opentelemetry/#including-resource-attributes-at-query-time).
Consequently, B must prevent forbidden data in the emitted resource and datapoints, not merely
omit it from the promoted-label list. The same source-side responsibility applies to A and C.

#### C: Direct scrape benefits and costs

| Aspect | Benefit for Insurance Hub | Cost or limitation for Insurance Hub |
| --- | --- | --- |
| Fit with Prometheus | Reuses the monitoring stack's existing discovery/scrape operating model and avoids enabling an inbound push receiver. | Adds an internal application endpoint, exporter, and monitor resource, with selector, port, routing, and exposure tests. Existing platform scraping does not automatically discover Product. |
| Failure isolation | Metrics remain independent of Alloy. Product needs no outbound metrics destination or metrics push retry loop. | Prometheus must reach every selected Pod. Discovery failures or unsuccessful scrapes create gaps. Logs and traces still depend on Alloy. |
| Diagnosis | Per-target scrape status and timing make missing-target versus failed-scrape problems easier to inspect. | A successful scrape proves endpoint reachability and valid exposition, not correct business instrumentation or healthy reads. |
| Local investigation | A developer can inspect a local metrics response without an ingestion backend when that explicit diagnostic mode is enabled. | Normal disabled telemetry must still satisfy M09. Adding a handler does not justify a persistent local monitoring stack or public metrics exposure. |
| History and identity | Cumulative counters/histograms can span a missed scrape if the Product process survives; Kubernetes discovery can supply bounded target labels. | No intermediary stores missed gauge samples. Pod discovery labels, OTel resource labels, and scrape `job`/`instance` labels need a deliberate mapping to the existing query contract. |

Prometheus records `up`, scrape duration, and sample counts for each scraped instance.
[Prometheus jobs and instances](https://prometheus.io/docs/concepts/jobs_instances/).
Routes A and B do not create equivalent Product scrape-health evidence merely by receiving pushed
samples. They need the contract's freshness, expected-series, exporter, and collector checks; a
healthy scrape of Alloy itself is insufficient. C still needs business-metric absence and
cardinality checks in addition to `up`.

#### Failure, security, and measurement implications

**A 60-second Prometheus outage:** A has the contract's explicit requirement to preserve the
logical cumulative counter/histogram delta through Alloy's WAL while Alloy stays alive. B and C
can also retain cumulative totals in the live Product process, but this does not restore the
original ten-second samples or transient pool/memory gauge peaks. Under every route, restarting
Product before the next successful collection/export can lose observations not yet delivered.
Replay can restore stored history after an outage; it cannot make an alert fire while Prometheus
itself is down. Compare totals, timestamp gaps, gauge loss, and alert timing separately.

**An Alloy rollout or failure:** B and C preserve the metrics path if Product and Prometheus stay
healthy. A must demonstrate the specified recovery/convergence and expose loss during ephemeral
Pod replacement. This is the strongest operational argument for bypassing Alloy in this project;
the pilot explicitly excludes a highly available collector design.

**Internal exposure:** A enables a shared remote-write receiver; B enables a shared OTLP receiver;
C exposes per-service scrape endpoints. ClusterIP keeps these within the cluster's network scope
but does not authenticate writers/readers or ensure only Alloy can send metrics. The historical
inspection found no monitoring NetworkPolicy. Centralizing the intended writer under A is useful
operationally, but is not an enforced security boundary. All routes must preserve M10's internal
exposure and M05's safe fields; any new isolation policy needs its own scoped design.

**Performance and cardinality:** No route is inherently exempt from the Product `300m/256Mi`
limits, the telemetry-overhead ceilings, or the 800-series-per-instance budget. At the same final
label/bucket schema, bypassing Alloy does not reduce Prometheus's stored series. For example, the
14 finite request-duration boundaries produce 15 classic bucket series including `+Inf`, plus
`_sum` and `_count`: 17 series per populated dimension set before the request counter. The bounded
operation/transport/outcome/status combinations therefore matter under every route. Benchmark
actual overhead and query freshness rather than treating one fewer hop as proof of improvement.

**Availability and correlation:** Direct backend delivery can remain asynchronous and independent
of request success; it need not make Prometheus a readiness dependency. Conversely, Alloy does not
automatically guarantee request independence. Both need the existing outage and shutdown tests.
Shared trace/log/metric identity is possible with all routes; passing through one collector does
not itself establish Java/Go parent continuity or correlate an aggregate metric to a single request.

#### Recommendation and conditions for revisiting it

For issue #135, **option A is accepted** because Alloy is already part of the required logs/traces topology,
its metrics branch exists, and this route keeps bounded backend buffering and identity enforcement
outside the Product process. The pilot's modest declared series/sample budget makes completing and
measuring that route a reasonable next step. This is an engineering judgment based on the existing
project, not a claim that a collector is always preferable to direct Prometheus ingestion.

Reconsider **B** if keeping OTel push while making metrics independent of Alloy becomes the higher
priority, and the project accepts separate exporter configuration, relocated label/enrichment
policy, and revised outage evidence. Reconsider **C** if the intended long-term operating model becomes
Prometheus scraping each service, with standard discovery and per-target scrape diagnostics.
For a metrics-only service built from scratch, C's operational simplicity would be a stronger
argument; Insurance Hub already has broader Alloy responsibilities.

Revisit A if measured collector contention breaches the existing overhead/freshness ceilings,
Alloy incidents repeatedly hide required diagnostic metrics, or a deliberate platform decision
standardizes direct scraping/OTLP. A failed budget is a reason to investigate or review the design,
not to relax its threshold after measurement.

Accepting A closes the route-design gate, while leaving implementation and all seven ACs
outstanding. A future switch to B or C requires revising the issue's M04 and AC135-03 “through Alloy”
requirement together with this contract, and reviewing M06/M08/M11, configuration ownership,
identity mapping, delivery-health alerts, outage/recovery budgets, rollout, and rollback. Keep the
existing safety and local-independence requirements. Use one authoritative Product metrics route;
parallel exports of the same instruments would need separate controls to prevent double counting
or conflicting series. Recording this decision does not change deployed infrastructure.

## Pod log collection decision and QA revalidation

**Revalidated decision:** Use `discovery.kubernetes` → `discovery.relabel` →
`loki.source.kubernetes` → `loki.process` → `loki.write.qa_loki` for Go Product stdout/stderr.
This preserves the application's local JSON diagnostics, uses the installed single Alloy
Deployment, and leaves Java trace ingestion unchanged. Do not add a second application OTLP-log
exporter.

The initial inspection at repository revision `c37ffdc` used local-dev evidence and Helm rendering
because QA was unreachable. The live QA revalidation at the same revision confirmed the installed
topology, access, and destination. No application, collector, RBAC, or Helm configuration was
changed. One sanitized OTLP log was sent through the existing Alloy log exporter solely to verify
the shared writer and Loki destination; it remains under Loki's normal `168h` retention.

| Area | Live QA evidence | Decision consequence |
| --- | --- | --- |
| Producer and target | At the inspection, Go wrote JSON to stderr with default service identity `product-service`. The rendered QA Deployment had pod labels `app.kubernetes.io/name=product-api` and `environment=qa`, container `product-api`, and three declared ports; only `product-api-legacy` was live. | Keep container-log collection. The labels support a precise selector. #134 later deployed Go; revalidate real Product discovery and parsing during implementation. |
| Collector version and topology | The installed release is Alloy chart `1.8.1`, image `v1.16.1`, one Deployment replica using ServiceAccount `qa-monitoring/qa-alloy`. Its only storage path is `/tmp/alloy`; rolling updates can overlap old and new pods. | The Kubernetes API reader is supported without a DaemonSet or node mounts. Treat positions as ephemeral and test overlap/replay/loss. |
| Missing source branch | The live ConfigMap contains `loki.write.qa_loki` and the OTLP-to-Loki exporter, but no `discovery.kubernetes`, `loki.source.kubernetes`, or `loki.process`. Their component metrics are consequently absent. | The route is feasible but not installed. Add one Product-only source/processing branch to both owned configuration representations. |
| Candidate route validation | The installed Alloy `v1.16.1` successfully validated a non-applied candidate containing namespace/label discovery, container/`http`-port keeps, controlled labels, JSON timestamp/severity parsing, trace/span structured metadata, the Kubernetes source, and the existing writer. | The proposed components and syntax are compatible with the installed binary. Runtime behavior still requires the deployed target and applied configuration. |
| Effective permissions | The installed `qa-alloy` ClusterRoleBinding grants cluster-wide pod/log reads and unrelated reads including Secrets. Impersonating that ServiceAccount successfully listed a QA Product pod and read a bounded five-line log tail; only line and byte counts were retained. | Access is sufficient but violates the ticket's scoped-permission requirement. Narrowing RBAC is mandatory, not optional. |
| Scoped RBAC | Rendering the installed chart with `rbac.namespaces=["qa-svc"]` and explicit rules produces a Role/RoleBinding granting only `pods: get/list/watch` and `pods/log: get`. No other live Alloy component uses Kubernetes discovery; the Zipkin bridge is a separate Deployment. | Replace the Helm-owned ClusterRole/ClusterRoleBinding with this Role/RoleBinding. After rollout, prove Product access remains and Secrets plus logs outside `qa-svc` are denied. |
| Destination health | Loki `3.6.7` is ready behind a `ClusterIP` service with `168h` retention. Its three canaries produced 900 queryable entries in five minutes; the inspected canary reported zero missing, unexpected, or out-of-order entries. | Reuse the existing internal Loki writer and retention. No logging backend or retention change is needed. |
| Alloy writer proof | A single safe OTLP test record returned HTTP 200 from Alloy, incremented converter processed/total and writer sent-entry counters to one, produced one Loki HTTP 204 write, and was queryable by its unique service label and marker. Retry, drop, and converter-failure counters remained zero. | The shared `loki.write.qa_loki`→Loki segment works. This does not prove the proposed Kubernetes source, JSON processing, or real Product logs. |
| Competing collection | A cluster-wide workload/image inspection found the installed Alloy and the trace-only Zipkin bridge, with no Promtail, Fluent Bit, Vector, Filebeat, or second Alloy workload. Existing Loki streams before the smoke record were canary streams. | No competing pod-log collector was found. Retain one Product source and verify again during rollout to prevent duplicate collection. |

Required collection and processing boundaries:

- Restrict discovery to namespace `qa-svc`, pod labels
  `app.kubernetes.io/name=product-api,environment=qa`, and container `product-api`. This excludes
  `product-api-legacy`, other services, and Alloy. Do not require readiness, because startup/failure
  logs must be collected.
- Kubernetes pod discovery creates one target per declared container port. Use
  `discovery.relabel` to keep the Product container's stable `http` port target, yielding one source
  target per Product pod/container while preserving the Kubernetes metadata required by
  `loki.source.kubernetes`. Verify exactly one effective target per pod after deployment. The
  [Alloy pod discovery reference](https://grafana.com/docs/alloy/latest/reference/components/discovery/discovery.kubernetes/#pod-role)
  documents the per-port behavior.
- Parse the existing JSON line without replacing the original record body. Promote only approved,
  bounded fields to the eight index labels fixed by the identity decision. Derive those stable
  service/deployment labels from controlled configuration and Kubernetes metadata. Put trace/span
  IDs and instance/Pod identity in Loki structured metadata so they remain searchable without
  creating streams. Keep raw paths, product codes, caller identities, request/trace IDs, and
  arbitrary errors out of index labels. The
  [Loki label guidance](https://grafana.com/docs/loki/latest/get-started/labels/) and
  [`loki.process` reference](https://grafana.com/docs/alloy/latest/reference/components/loki/loki.process/)
  support this division.
- The Product selector is a collection filter, not an authorization boundary. Replace the existing
  Helm-owned ClusterRoleBinding; merely adding a Role leaves the broader permission effective
  because Kubernetes RBAC grants are additive. Namespace scope still permits the Alloy account to
  read other `qa-svc` pod logs, which is the narrow practical boundary for dynamically named pods.
- Preserve one log path. Validate a normal rollout, forced container restart, Alloy restart, and
  Alloy pod replacement for gaps and duplicates. The single desired replica can temporarily
  overlap during rolling updates.
- Keep retry/drop metrics from `loki.write.qa_loki` as delivery-health signals. Alloy stores source
  positions under ephemeral `/tmp/alloy`, and the current writer has no enabled persistent buffer,
  so replacement can replay or lose records. Define numeric retry, volume, freshness, and bounded
  loss budgets before acceptance; do not claim exactly-once or lossless delivery.
- Parsing does not make unsafe application fields safe. Retain application-side redaction and test
  emitted plus stored records with sentinel values. Define malformed-JSON, timestamp, successful
  request volume, and field allowlist behavior before implementation. Correct the observed QA
  node/host clock skew before testing timestamp freshness.

**Remaining verification:** Deploy the verified #134 Go Product image, implement the source,
processing, and scoped RBAC changes through the Alloy Helm owner, then prove exactly one target and
one Loki record for each controlled Product log. Exercise JSON and malformed-line parsing,
trace-field lookup, sentinel redaction, healthy-run volume, permissions after Helm reconciliation,
rollout/restart duplication, outage/backpressure, recovery, and ephemeral-position loss. The writer
smoke test and bounded legacy pod-log read do not close AC135-03 or AC135-05.

## Pilot acceptance budget decision

**Decision:** Use the following fixed workload and budgets for issue #135. They are pilot
acceptance ceilings, not production SLOs or a capacity claim. Run the full suite only after clocks
are synchronized, the #134 Go Product candidate is deployed with one replica, the selected Alloy
upgrade and telemetry routes are effective, and the service and telemetry backends report ready.
Do not tune a threshold from the result it is judging. A proposed threshold change requires its
own measured evidence and specification review before rerunning acceptance.

### Workload and sampling

- Establish a five-minute no-load baseline without rollouts or unrelated load. Warm the candidate
  for two minutes at `2 requests/second`; exclude those 240 requests from acceptance results.
- Measure for 15 minutes at a fixed arrival rate of `5 requests/second`, with at most eight requests
  in flight and a nine-second client deadline. Send exactly 4,500 successful known-fixture reads:
  1,125 each to HTTP list, HTTP get, gRPC list, and gRPC get. Keep probes and failure scenarios out
  of this healthy workload.
- Use `ParentBased(root=AlwaysOn)` in QA and the disposable acceptance environment. A valid remote
  sampled or unsampled parent still wins. Do not add tail sampling. Normal local development keeps
  telemetry disabled unless a disposable telemetry test explicitly enables it.
- The healthy workload must yield exactly 4,500 request-completion logs and 18,000 Product spans
  (server, application, pool acquisition, and query), plus 90 scheduled metric exports at the
  ten-second interval. Backend counts must respect the field, series, stream, and churn ceilings
  already fixed in this specification.

Run the telemetry-overhead comparison in a disposable environment using the same image, node,
database fixture, workload, and resource limits. Alternate telemetry-disabled and telemetry-enabled
order, discard a two-minute warm-up, and collect three ten-minute measurement runs per mode. Compare
the median of the run-level percentiles; do not compare one favorable run with another.

### Application and collector delivery limits

The Go process must make these OpenTelemetry SDK values explicit rather than relying on library
defaults:

| Component | Fixed limit |
| --- | --- |
| Trace batch processor | Queue 2,048 spans; batch 256 spans; one-second schedule delay; two-second export timeout; non-blocking drop when full |
| OTLP trace retry | Initial 250 ms; maximum one second; maximum elapsed time two seconds |
| Metric reader | Ten-second interval; two-second export timeout; cumulative sums and explicit-bucket histograms |
| Shutdown | Preserve the single ten-second application shutdown budget and two-second forced-cleanup reserve; telemetry may use only the remaining time within that budget |

At the healthy rate, 20 spans/second enter the trace processor, so its queue holds more than 100
seconds of this workload. Queue-full, timeout, and SDK drop counters must remain zero during
healthy acceptance, and the separate Product memory budget verifies the queue's actual cost.

Apply these limits to the upgraded Alloy pipeline:

| Component | Fixed limit |
| --- | --- |
| Memory limiter | Check every second; 800 MiB hard limit; 160 MiB spike allowance, producing a 640 MiB soft limit; place it before batching |
| OTLP batch processor | One-second timeout; 256 preferred items; 512 maximum items |
| Tempo and Zipkin OTLP exporters | Two-second request timeout; retry from one to five seconds for at most one minute; queue 2,048 items with two consumers and non-blocking full-queue behavior |
| Prometheus remote write | Per-endpoint queue capacity 1,000 samples, one to two shards, 500 samples/send, one-second send deadline, 100 ms-to-two-second backoff, retry HTTP 429, and five-minute sample-age limit |
| Remote-write WAL | Five-minute truncation, five-minute minimum retention, 15-minute maximum retention; storage remains ephemeral for this pilot |
| Loki writer | At most eight streams; 256 KiB batches or one-second wait; two-second request timeout; five retries with 500 ms-to-five-second backoff, including HTTP 429 |

Do not enable Alloy's experimental Loki queue or WAL. Its stability requirement and the current
ephemeral storage mean the pilot must expose and bound loss instead of claiming durable log
delivery. The candidate Alloy `v1.20.1` configuration containing these limits has passed `alloy
validate`; its effective installed configuration still requires rollout verification.

### Healthy, failure, recovery, and resource budgets

| Area | Pass budget |
| --- | --- |
| Request behavior | All 4,500 healthy requests return the expected success status; no unexpected application error, deadline, pool timeout, pod restart, or readiness loss occurs |
| Request latency | For each operation/transport combination, p95 is at most 250 ms and p99 at most 500 ms |
| Database pool | Acquisition p95 is at most 50 ms; pool timeouts are zero; p95 used connections is at most six of eight |
| Product resources | CPU p95 at most 200 millicores and every one-minute maximum at most 250 millicores; memory p95 at most 128 MiB and maximum at most 192 MiB; CPU throttling ratio below 5%; no OOM or restart |
| Telemetry overhead | For each operation/transport, enabled p95 minus disabled p95 is at most the greater of 20% of disabled p95 or 10 ms; enabled CPU p95 increases by at most 50 millicores and maximum working set by at most 32 MiB |
| Alloy resources | During the run, CPU p95 at most 400 millicores, maximum working set at most 640 MiB, and increase over the preceding 30-minute baseline at most 100 millicores CPU and 128 MiB memory; no memory-limiter refusal, drop, OOM, or restart |
| Healthy delivery | No missing or duplicate expected logs or spans; no missing metric series; no exporter failure, retry exhaustion, queue-full drop, or firing Product pilot alert |
| Freshness | From request completion, trace and log queryability p95 at most 10 seconds and maximum 30 seconds; metric queryability p95 at most 20 seconds and maximum 45 seconds |

Exercise exactly one controlled 60-second backend outage and one Alloy restart/replacement at the
same `5 requests/second` rate. Stop a backend only in the disposable environment; use a safe
configuration rollout for the shared QA replacement check. Product reads and health endpoints must
continue to meet their normal contracts while telemetry is unavailable.

After restoration, a new trace and log must be queryable within 15 seconds and a new metric within
30 seconds. Exporter queues, pending samples, and delivery lag must return to the healthy state
within 120 seconds. Outside the exact fault window, loss is zero. Within it, the non-durable ceiling
is 300 request logs and 1,200 spans, with duplicates no greater than three logs and 12 spans (1%).
For a backend outage while Alloy stays alive, Prometheus must retain the full logical cumulative
counter and histogram delta through its WAL. For an Alloy pod replacement, cumulative application
metrics must converge on the next successful export, while platform metrics must resume within the
freshness budget. Report the actual loss and duplication separately for the backend outage and pod
replacement; success does not turn either route into an exactly-once guarantee.

### Pilot alert rules and no-data policy

Install the following rules at a 30-second evaluation interval with `severity=warning`. Issue #135
must prove each rule fires and recovers against its actual window in a controlled environment;
external notification delivery remains outside the ticket.

| Alert | Expression threshold and window | `for` |
| --- | --- | --- |
| `ProductServiceErrorRate` | Unexpected outcomes exceed 5% over five minutes and at least 20 Product requests occurred | 2 minutes |
| `ProductHighLatency` | Aggregate Product request p95 exceeds 500 ms over five minutes and at least 20 requests occurred | 5 minutes |
| `ProductDatabasePoolPressure` | Used connections exceed 80% of configured maximum | 5 minutes |
| `ProductDatabasePoolTimeouts` | Any pool timeout increase over five minutes | Immediate |
| `ProductResourcePressure` | Product memory exceeds 80% of 256 MiB or CPU exceeds 80% of 300 millicores | 5 minutes |
| `ProductUnavailable` | Desired Product replicas exceed available replicas | 2 minutes |
| `ProductTelemetryMissing` | At least one Product replica is available but `go_goroutine_count` has no sample for two minutes | 1 minute |
| `ProductTelemetryDeliveryFailure` | Any five-minute increase in Loki dropped entries, Prometheus failed/dropped remote-write samples, or OTLP failed spans | Immediate |
| `ProductTelemetryBacklog` | Remote-write samples are pending with more than 30 seconds of send lag, or an OTLP exporter queue exceeds 80% | 2 minutes |
| `ProductUnexpectedRestart` | Any Product container restart increase over 15 minutes | Immediate |

The error-rate and latency alerts stay inactive below 20 requests in their five-minute windows.
Zero Product request traffic with a present runtime metric is legitimate idle time. Missing runtime
metrics alert only while the Product Deployment has an available replica; deployment availability
has its own rule. Prometheus cannot report its own complete outage, so the existing monitoring-stack
watchdog/platform owner retains that concern. The candidate file containing all ten expressions
passes `promtool v3.7.3 check rules`; runtime presence of every source metric remains an
implementation gate.

These values fit the #134 pod request/limit contract (`100m/64Mi` requests, `300m/256Mi` limits),
eight-connection pool, ten-second application shutdown, and 30-second Kubernetes termination
grace period. The live QA pre-run sample found ready nodes with ample CPU, while Alloy's preceding
30-minute peak was about 14 millicores and 281 MiB and its Loki/remote-write delivery counters
showed no failures. At that inspection no Go Product Deployment existed in QA, so the observations
were not a loaded Go baseline. #134 has since delivered the deployment, but the paired run and
deployed acceptance must still prove the budgets without relaxing them. The fixed values below are
the accepted design contract. Implementation evidence must record the actual results separately.

## Operational ownership decision

**Decision:** Preserve the repository's two existing QA control planes. Product code, image
identity, configuration, and Kubernetes resources remain owned by the Product release workflow and
Flux reconciliation from canonical `main`. Alloy and the Prometheus/Grafana stack remain owned by
their pinned Helm releases through `k8s/Makefile`. Issue #135 must coordinate them through the
staged gates below; it must not directly apply Product manifests, point Flux at a feature branch,
suspend reconciliation, or introduce a second monitoring deployment path.

### Historical current-state inspection and handoff gate

The 2026-10-14 inspection at repository revision `c37ffdc` found the following pre-#134 state:

- remote `main`, `flux-system/insurance-hub`, and `flux-system/qa-svc` all resolve to
  `f7f50f7b08addb6af91f0bdb9ab110b146e50a6d` and report ready;
- `k8s/overlays/qa/svc/kustomization.yaml` still includes only `./product/legacy`, the dormant Go
  overlay still contains `sha-unpublished`, and no `qa-product-api` Deployment exists;
- the legacy `qa-product-api-legacy` Deployment is available at image `1.5.0`;
- issue #134's bootstrap image exists, but its delivery tracker still leaves automatic release,
  QA prerequisites/activation, reconciled deployment, lifecycle proof, and final handoff open;
- the latest visible Product Image Release run is failed, and subsequent recovery commits have not
  produced the required verified digest update while GitHub Actions is unavailable.

At that inspection #134 had **not** handed an active QA Go Product to #135. These bullets are
retained as time-bound discovery evidence, not current state. #134 later completed the generic
release, active QA deployment, reader, lifecycle, stop/restoration, and final handoff. Before #135
enables QA telemetry, revalidate all of the following against current canonical `main`:

1. the active `./product` inclusion on reconciled `main` while retaining `./product/legacy`;
2. the generic release's semantic and short-SHA image coordinates, immutable running digest, and
   matching source/tag/GitHub Release evidence;
3. a ready `qa-product-api`, endpoints, reader identity/Secret, and successful Product verification;
4. preserved Java routing and catalog/schema identity; and
5. a tested Go-only restore path and final healthy Flux revision.

### Source and control-plane ownership

| Concern | Authoritative source | Apply/reconciliation owner | Required effective proof |
| --- | --- | --- | --- |
| Go instrumentation and telemetry configuration contract | `services/product-service/`, its Makefile/tests, and committed Product documentation | Product implementation PR; the generic Go service release publishes the merged source | Module checks pass; release artifact, scoped tag, full source revision, semantic/short-SHA images, immutable digest, and pulled image labels agree |
| Go QA desired state and telemetry enablement | `k8s/apps/svc/product/base/`, `k8s/overlays/qa/svc/product/`, and the QA service aggregate | Flux `GitRepository/insurance-hub` and `Kustomization/qa-svc`, reading `main`; `make -C k8s flux-reconcile` may only accelerate that owner | Source/applied revisions match the reviewed merge; Deployment generation, ConfigMap hash, Pod UID/image ID, endpoints, and effective non-secret settings match |
| Product image advancement | `.github/workflows/go-release-svc.yml` and `scripts/go/service/release.py` | Generic Go service release on canonical `main`; its bot commit owns the QA semantic image tag | Successful run; scoped source tag and GitHub Release; semantic and short-SHA images; QA bot commit; registry and live Pod digest match |
| Alloy chart, RBAC, and pipelines | `k8s/Makefile`, chart settings in `k8s/overlays/qa/infra/alloy/values.yaml`, and the Alloy program in `k8s/overlays/qa/infra/alloy/config.alloy` | Maintainer with QA Helm access through `make -C k8s alloy-install`; Helm release `qa-alloy` | Chart `1.13.1`, image `v1.20.1`, expected manifest/RBAC/config checksum, ready Deployment, and all old/new routes verified |
| Prometheus/Grafana base stack and remote-write receiver | `k8s/Makefile` and `k8s/overlays/qa/infra/prometheus/values.yaml`; the values file owns chart settings, persistent Grafana storage, and the receiver under `prometheus.prometheusSpec`, without service dashboards or rules | Maintainer with QA Helm access through `make -C k8s prometheus-stack-install`; Helm release `qa-prometheus` | Pinned chart/effective flags, ready Prometheus/Grafana, 1 GiB Grafana claim, default dashboards/rules, receiver, and scrape health match |
| Product alert rules | `k8s/observability/services/product/alerts.yaml`, its `monitoring.json` contract and behavior fixture, the selected environment file under `k8s/observability/environments/`, and generic tooling in `k8s/Makefile` and `k8s/scripts/monitoring/` | Maintainer with QA Kubernetes access through `make -C k8s prometheus-rules-apply ENV_NAME=qa SVC_NAME=product`; Prometheus Operator consumes the repository-owned `PrometheusRule` | Source passes generic contract validation, `promtool` syntax/behavior checks, server-side dry-run, exact `insurance-hub-product-service` group and ten alerts, and active rule/alert inspection |
| Product Grafana dashboard | `k8s/observability/services/product/dashboard.json`, its `monitoring.json` contract and metric fixture, the selected environment file, and generic tooling in `k8s/Makefile` and `k8s/scripts/monitoring/` | Maintainer with QA Kubernetes access through `make -C k8s grafana-dashboard-apply ENV_NAME=qa SVC_NAME=product`; Grafana's dashboard sidecar consumes the repository-owned `ConfigMap` | Source contract, labeled `ConfigMap`, immutable rendered provider, fetched provisioned dashboard, all 26 Prometheus queries, Loki/Tempo panels, and unrelated-dashboard preservation match |
| Monitoring CRDs | #154's exact chart `32.0.1` pin in `k8s/Makefile` | Existing `prometheus-operator-crds` Helm release | #154 handoff is accepted; no version drift or CRD mutation beyond that reviewed baseline; release remains deployed |
| Reader role and Secret | #134's `postgres-svc-role-* SVC_NAME=product ROLE_PROFILE=reader` procedure | Maintainer with Kubernetes/CNPG administrative access, outside application startup, Flux, and monitoring Helm | Fresh restricted-login verification and Secret/Pod consistency; no credential material in issue #135 evidence |
| Operational procedure | New committed `docs/migration/phase-4/product-service/observability-runbook.md` | Issue #135 implementation and reviewer | Commands, prerequisites, normal apply, verification, failure decisions, rollback, and evidence checklist match delivered targets |
| Acceptance evidence | Ignored `ai/artifacts/epic-4.2/issue-135/` records | Issue #135 implementer gathers; reviewer audits | UTC time, context, Git/image/Flux/Helm revisions, sanitized commands/results, signal queries, budgets, failure/recovery, and residual blockers are mapped to AC135-01–07 |

The Prometheus Helm values own base chart settings only; validation rejects embedded service
dashboards and alert rules. Service assets live under `k8s/observability/services/<service>/` and
are selected together with `k8s/observability/environments/<environment>.json`. `config.alloy`
owns the Alloy program. Every owning Alloy Helm render and install injects that file through
`--set-file alloy.configMap.content=...`; validation rejects an embedded copy in `values.yaml`, any
owning command that omits the injection, and any rendered ConfigMap content that differs from the
source. Do not edit the live Alloy ConfigMap, Prometheus custom resource, PrometheusRule, or Grafana
dashboard by hand. Use the guarded generic Make targets so the external files remain the durable
source. A successful manual live edit would be unowned drift and would not satisfy
configuration-as-code acceptance.

Grafana's sidecar discovers repository dashboard `ConfigMap` resources and presents their JSON
through its file provider. The rendered provider must set `disableDeletion: true` and
`allowUiUpdates: false`; the dashboard JSON must also set `editable: false`. Together these make
the provisioned dashboard inspectable but prevent UI modification and deletion. The base values
retain a 1 GiB `ReadWriteOnce` Grafana PVC for other Grafana state, while the repository asset and
`ConfigMap` can reconstruct the dashboard after replacement. Step 20 must prove the accepted
storage class/binding, sidecar provisioning, read-only UI/API behavior, and dashboard survival
across a Grafana pod replacement; an unbound claim blocks the apply rather than justifying
ephemeral storage. Product rules remain Prometheus data-source-managed rules, which Grafana exposes
read-only; updates and deletion go only through the guarded `PrometheusRule` apply path.

Issue #154 pins the Prometheus Operator CRD chart at `32.0.1` and kube-prometheus-stack at
`92.6.0`. Before issue #135 uses `prometheus-stack-install`, verify those exact accepted versions
and the #154 handoff; stop on drift rather than changing either application chart here. This ticket
keeps base Prometheus/Grafana settings on that baseline and owns Product monitoring in external
service assets. Retain #154's bounded `--atomic --wait --timeout` behavior and its non-mutating
render/config checks. The generic asset checks must give Product rules and dashboard validation a
finite, reviewable outcome before either Kubernetes API write target runs.

### Staged delivery and reconciliation

Use this order. Each stage requires its evidence before the next stage starts:

1. **Implement and validate with telemetry disabled.** Add the Product instrumentation and tests,
   Alloy/Prometheus base values, external dashboard/rules, runbook, and guarded generic Make
   validation. Keep the base and local-dev normal mode at `OTEL_ENABLED=false`; do not combine QA
   enablement with the first instrumented image release.
2. **Merge and release the instrumented image.** Require all Product and deployment checks, then
   require the canonical-main generic Go service release to publish the exact merged source under
   semantic and short-SHA image tags, create the scoped source tag/GitHub Release, and update the
   QA semantic tag. Flux may roll the new image only while telemetry remains explicitly disabled.
   A failed or unavailable release blocks later QA stages.
3. **Apply Prometheus/Grafana.** From a clean checkout of the recorded reviewed `main` revision,
   capture Helm revision/values/manifest, Prometheus flags, active rules/alerts, dashboard state,
   readiness, and scrape health. Run the guarded `prometheus-stack-install` base owner, then
   `prometheus-rules-apply ENV_NAME=qa SVC_NAME=product`, then
   `grafana-dashboard-apply ENV_NAME=qa SVC_NAME=product`. Require the remote-write receiver,
   Product rules, and immutable sidecar-provisioned dashboard before Alloy sends
   Product metrics. Prove normal scrape ingestion and that unrelated dashboards/rules remain.
4. **Apply Alloy.** Capture the same before-state plus route counters and a sanitized route sample,
   then run the guarded `alloy-install` owner. Require exact chart/image/config/RBAC identity and
   prove Java Zipkin input, Tempo and transitional Zipkin outputs, existing Loki delivery, the
   Prometheus path, reload/restart behavior, and the synthetic Product signal checks. Do not enable
   Product telemetry if any existing route regresses.
5. **Enable QA Product telemetry through Flux.** After the #134 handoff is revalidated and the
   monitoring gates pass, merge a separate QA configuration change that sets `OTEL_ENABLED=true`,
   the internal Alloy
   OTLP/gRPC endpoint, and the fixed service/environment/version settings. Let the generated
   ConfigMap hash roll the Go Pod. Record the merge, Flux source/applied revision, old/new Pod UIDs,
   image digest, effective safe settings, readiness, and signal identity.
6. **Run issue #135 acceptance.** Execute the fixed healthy/failure/recovery profile, dashboard and
   alert checks, restart/replacement exercises, field/cardinality audit, and Java/Go continuity
   matrix. Issue #136 may consume this evidence but does not complete missing #135 gates.

The pre-#154 kube-prometheus-stack `79.3.0` render was checked with the ten validated Product rules
embedded under `additionalPrometheusRulesMap.product-service` and a dashboard under
`grafana.dashboards.default.product-service`. It produced a Helm-owned `PrometheusRule` and mounted
Grafana dashboard ConfigMap; all 6,369 rendered lines passed Kubernetes client-side dry-run. That
historical evidence describes the superseded embedded-asset approach only. The final `92.6.0`
implementation keeps service assets out of Helm values, validates them against the base render,
applies the generated `PrometheusRule` and labeled dashboard `ConfigMap` through Kubernetes, and
lets Grafana's immutable file provider load the fixed dashboard UID. Historical rendering does not
prove either final API result or live use.

### Rollback and abort ownership

- Abort before enablement on a dirty/wrong checkout, unexpected Helm or Flux revision, chart/image
  mismatch, unavailable or drifted #134 handoff, insufficient capacity, unsynchronized clocks,
  render or validation failure, unrelated manifest drift, or a failing existing telemetry route.
- For a Product configuration or image regression, restore `OTEL_ENABLED=false` or the last
  accepted semantic image tag/configuration through a reviewed Git change on `main`, then let Flux
  reconcile and verify Java, Product health, endpoints, image/config identity, and telemetry stop.
  Direct `kubectl apply`, manual scaling, or Flux suspension is not a durable rollback.
- For an Alloy regression, use `helm rollback qa-alloy <captured-revision> --namespace
  qa-monitoring --wait --timeout 5m`, then verify the prior image/config and every pre-existing
  route. For a Prometheus/Grafana base regression, use the equivalent rollback for `qa-prometheus`.
  A Helm rollback does not restore the separately managed Product rules or dashboard; restore their
  last accepted repository revisions with the guarded rule and dashboard apply targets,
  then verify flags, scrapes, rules, dashboards, and readiness. Revision `1` was historical
  discovery evidence before #154; capture the actual rollback revision and asset state immediately
  before each change.
- A Helm rollback restores live state but not repository intent. Follow it with a reviewed revert
  of the failed chart/values/assets before another install. Do not roll back or delete shared
  Loki, Tempo, Zipkin, databases, PVCs, Java resources, or the reader Secret to recover Product
  telemetry.
- If both control planes must be restored, disable Product telemetry through the reviewed Flux path
  and roll back the affected monitoring release. Restore the final intended state only after the
  failing stage is corrected and its earlier gates pass again.

### Evidence handoff

Record separate evidence for source validation, hosted release, Flux reconciliation, Helm rollout,
and backend acceptance; none implies another. The implementation artifact must identify the exact
Git source and applied revisions, Product release run/digest/image ID, Helm revisions/chart/app
versions, configuration checksums, Pod UIDs, route counters, queries, dashboard UID, alert states,
failure windows, recovery results, and budget verdicts. Keep credentials, raw Secret content,
caller tokens, and payloads out of evidence.

Issue #135 owns Product telemetry implementation, its narrowly scoped monitoring configuration,
staged enablement, and all AC135-01–07 evidence. Issue #154 owns the preceding shared-platform
upgrade and baseline handoff. Issue #134 owns the initial active Go
deployment/reader/Java-preservation handoff. The existing monitoring maintainer executes Helm-owned
QA changes; the Product release
workflow and Flux execute their respective state transitions. Issue #136 owns later integrated
qualification and rollback rehearsal against its own scope, and issue #137 alone owns gateway
traffic cutover. Revalidate each control-plane identity immediately before implementation.

## Dependencies and completion boundaries

Issue #154 is complete and supplies the accepted platform baseline. Recheck its recorded chart/image
matrix, effective values, Helm revisions, routes, retention, resource observations, and rollback
points. Treat any live-version drift or unresolved Alloy, Loki, Tempo, Prometheus, or Grafana
failure as a blocker; do not absorb another platform upgrade into #135.

Issue #134 is complete and supplies the active Go Product, restricted reader, internal endpoints,
resource/lifecycle configuration, Java-routing preservation, generic release, and Flux handoff.
Recheck its recorded final revision, semantic image tag and running digest, reader/Secret,
endpoints, catalog identity, and stop/restoration state before deployed verification. Stop on drift;
do not bypass the generic release or Flux owners.

Issue #156 is complete and supplies selected-module Go PR validation through `check-go-pr`,
including the 75% atomic race coverage threshold, plus change-based local checks documented in
[CONTRIBUTING.md](../../../../CONTRIBUTING.md#change-based-local-validation). Integrate applicable
Product telemetry tests into that path. Validate auxiliary tooling, monitoring configuration,
security, deployment, images, and documentation locally according to changed behavior; monitoring-only
changes do not trigger Go validation. Generic release remains separate. Retired broad hosted jobs
are not required evidence, and earlier CI/security limitations do not establish a current outage.

Unavailable applicable hosted checks or an unreachable QA control plane do not reduce acceptance scope.
Record unavailable checks as blocked. Issue #135 owns telemetry-specific failure, lifecycle,
correlation, and delivery tests; #136 adds integrated differential/performance/rollback qualification.
Only #137 changes existing gateway traffic.

## Repository paths and verification interface

- [Product module](../../../../services/product-service/README.md) and
  [Makefile](../../../../services/product-service/Makefile): own implementation and verification.
  Relevant code is under `internal/{telemetry,logger,http,grpc,postgres,service,config,testing}`.
- [Alloy values](../../../../k8s/overlays/qa/infra/alloy/values.yaml),
  [collector configuration](../../../../k8s/overlays/qa/infra/alloy/config.alloy),
  [Prometheus/Grafana values](../../../../k8s/overlays/qa/infra/prometheus/values.yaml), and
  [Kubernetes Makefile](../../../../k8s/Makefile): narrowly scoped ingestion/provisioning changes.
- [Product deployment runbook](deployment-runbook.md),
  [security baseline](security-baseline.md), and
  [contract baseline](contract-baseline.md):
  preserve deployment, exposure, and behavior contracts. Place the committed observability runbook
  alongside these documents and follow existing provisioning conventions for dashboard/rule assets.
- [Java gateway](../../../../legacy/agent-portal-gateway/src/main/resources/application.yml) and
  [Java Product configuration](../../../../legacy/product-service/src/main/resources/application.yml):
  inspect effective propagation and limit changes to required compatibility/test consumers.
- Existing validation entry points include `make -C services/product-service check`,
  `make -C services/product-service test-tooling`, and `make product-security-check`, selected
  according to changed code and their documented prerequisites. Add missing telemetry/asset/live
  verification through owning Makefiles; do not present proposed targets as existing commands.
- [Alloy trace verification](../../../../k8s/tests/infra/verify-alloy-traces/verify-alloy-traces.md)
  supplies a collector/Java smoke reference. Extend it with actual Product/backend evidence and
  explicit target preflight. A healthy collector or synthetic span is not application acceptance.
- Format and check every changed Markdown file with `./scripts/markdown-toc.sh`.
  Keep this specification, future delivery plan, summaries, and sanitized evidence in
  `ai/artifacts/epic-4.2/issue-135/`, excluded from Git. Never commit credentials or raw payloads.

## Requirements traceability

The GitHub issue preserves the accepted M01–M11 scope and AC135-01–07. This contract supplies
the incorporated technical decisions; a change that affects scope, acceptance, security, or an
out-of-scope boundary must update the issue as well.

| Accepted requirement | Specification section | Acceptance criteria |
| --- | --- | --- |
| M01 Actual read path | Description 1 | AC135-01 |
| M02 Java/Go continuity | Description 2 | AC135-02 |
| M03 Correlated logs | Description 3 | AC135-01, AC135-03 |
| M04 Metrics | Description 4 | AC135-01, AC135-03, AC135-04 |
| M05 Identity and telemetry safety | Description 5 | AC135-01, AC135-02, AC135-03, AC135-05 |
| M06 Ingestion configuration | Description 6 | AC135-03, AC135-07 |
| M07 Dashboard and alerts | Description 7 | AC135-04 |
| M08 Bounded failures | Description 8 | AC135-05 |
| M09 Local independence | Description 9 | AC135-06 |
| M10 Product boundaries | Description 10 | AC135-02, AC135-05 |
| M11 Repeatable handoff | Description 11 | AC135-07 |

Apply the Product-relevant G19-13 and OB-16-01 through OB-16-05 gates in the
[Phase 4 implementation review](../phase-4-implementation-review.md#observability-validation-scenarios).
OB-16-06 keeps Zipkin/bridge retirement separately gated. No optional ledger entry is promoted
into this specification by reference alone.
