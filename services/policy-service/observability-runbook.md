<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Policy observability runbook](#policy-observability-runbook)
  - [Signal and decision boundary](#signal-and-decision-boundary)
  - [Source and disposable validation](#source-and-disposable-validation)
  - [QA preflight, apply and read back](#qa-preflight-apply-and-read-back)
  - [Investigation workflow](#investigation-workflow)
    - [Troubleshooting cases](#troubleshooting-cases)
    - [Delayed outbox publication](#delayed-outbox-publication)
    - [Consumer stalled on an acknowledged record](#consumer-stalled-on-an-acknowledged-record)
    - [Alert responses](#alert-responses)
  - [Recovery, rollback and cleanup](#recovery-rollback-and-cleanup)
  - [Evidence checklist](#evidence-checklist)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Policy observability runbook

This runbook owns source validation, guarded QA provisioning, read-back, investigation and
restoration for the Go Policy monitoring assets. Use service key `policy`, environment `qa`,
Kubernetes context `qa-insurance-hub` and monitoring namespace `qa-monitoring`. The application
Deployment is `qa-policy-api` in `qa-svc`; the monitoring resources are PrometheusRule
`qa-policy-service` (group `insurance-hub-policy-service`), ConfigMap `qa-policy-dashboard` and
Grafana dashboard UID `insurance-hub-policy-service`.

The dashboard and rules are repository-owned monitoring resources applied through the Make
targets below. They are separate from the Flux-owned Policy workload described in the
[deployment runbook](deployment-runbook.md). Do not edit the Grafana dashboard in the UI, patch the
generated resources, suspend Flux, change Kafka offsets or mutate another service's assets while
following this procedure.

## Signal and decision boundary

Policy exports bounded HTTP request outcome/duration signals, Go runtime signals, outbound provider
RPC client signals for `product.v1.ProductService/GetProduct` and
`pricing.v1.PricingService/CalculatePrice`, event publication outcome/duration by event type, and
one readiness gauge per dependency: `insurance_hub_policy_dependency_ready` with
`insurance_hub_policy_dependency` set to `gateway`, `database` or `kafka`. The dashboard adds Pod
availability, resource and restart views and shared Alloy delivery health. Labels never contain
policy keys, payloads, customer values, topics, groups, credentials or trace IDs.

The thresholds and windows in `k8s/observability/services/policy/acceptance.json` are operating
limits, not production SLOs. Provider availability is not part of readiness: an unreachable
provider fails only the affected offer. The gauge has no outbox backlog age, so a pending-intent
check is a read-only outbox query. There is no consumer-lag series; consumer progress is read from
the broker.

| Area | Panels | Alerts |
| --- | --- | --- |
| Requests | Request rate; Service error rate; Request latency percentiles | `PolicyServiceErrorRate`, `PolicyHighLatency` |
| Providers | Upstream RPC outcomes; Upstream RPC latency percentiles | `PolicyUpstreamRPCFailure` |
| PostgreSQL | Database readiness | `PolicyDatabaseNotReady` |
| Kafka publication | Event publication outcomes; Event publication latency percentiles; Kafka publisher readiness | `PolicyEventPublicationFailure`, `PolicyKafkaNotReady` |
| Workload | Readiness and restarts; Pod CPU and memory; Go runtime | `PolicyUnavailable`, `PolicyUnexpectedRestart`, `PolicyResourcePressure`, `PolicyTelemetryMissing` |
| Collection | Telemetry delivery health | `PolicyTelemetryDeliveryFailure`, `PolicyTelemetryBacklog` |
| Investigation | Failed request and publication investigation logs; Policy traces; Investigation workflow | — |

The two collection alerts describe the shared Alloy pipeline, not Policy itself.

## Source and disposable validation

From the repository root, validate the exact candidate before any QA operation:

```sh
make monitoring-assets-check ENV_NAME=qa SVC_NAME=policy
make test-monitoring ENV_NAME=qa SVC_NAME=policy
```

The first command validates the typed inventory, the pinned chart render, Prometheus rule syntax
and the rule fixtures. The second queries every dashboard expression against collected disposable
data and requires healthy alerts to stay inactive. Neither applies QA resources or proves live
signal delivery. The shared `verify-service-observability` target has no Policy probe; verify live
Policy signals with the bounded queries in [Investigation workflow](#investigation-workflow).

## QA preflight, apply and read back

Follow the preflight, apply, read-back and restore sequence of the
[Dashboard observability runbook](../dashboard-service/observability-runbook.md#qa-preflight-and-stop-conditions),
substituting service key `policy`, PrometheusRule `qa-policy-service`, ConfigMap
`qa-policy-dashboard`, Deployment `qa-policy-api` and source variable
`POLICY_SOURCE_REVISION`. Skip the `verify-service-observability` steps. The Policy objects are
rendered and applied with:

```sh
make --no-print-directory -C k8s prometheus-rules-render ENV_NAME=qa SVC_NAME=policy
make --no-print-directory -C k8s grafana-dashboard-render ENV_NAME=qa SVC_NAME=policy
make -C k8s prometheus-rules-apply ENV_NAME=qa SVC_NAME=policy
make -C k8s grafana-dashboard-apply ENV_NAME=qa SVC_NAME=policy
```

Run the apply targets only with authorization for these two exact QA objects. Confirm the render
names `qa-policy-service` and `qa-policy-dashboard`, namespace `qa-monitoring`, selector
`service_name="policy-service"`, 30-second evaluation and refresh intervals, warning severity and
`editable: false`. After apply, read back the active group `insurance-hub-policy-service` with
its 12 alerts and the provisioned dashboard UID through `make -C k8s grafana-ui`.

Stop before mutation when the context, namespace, source revision, object name or UID, Policy
image identity or current asset source differs from the reviewed record, or for missing Policy
signals, unexplained firing alerts, a mutable Grafana provider or any operation that would alter
shared monitoring releases, PVCs, Alloy, Prometheus, Loki, Tempo, PostgreSQL, Kafka or another
service's assets.

## Investigation workflow

Use a bounded absolute UTC range, normally 15 minutes. Start from the dashboard's **Investigation
workflow** panel, then the request panels and the three readiness views. Copy a safe `trace_id`
from a completion log into Tempo; do not add trace IDs or business values to metric labels.

Representative queries:

```promql
min by (insurance_hub_policy_dependency) (insurance_hub_policy_dependency_ready{
  service_name="policy-service",deployment_environment_name="qa"
})
```

```promql
sum by (rpc_method, rpc_response_status_code) (rate(rpc_client_call_duration_seconds_count{
  service_name="policy-service",deployment_environment_name="qa",
  rpc_method=~"product.v1.ProductService/GetProduct|pricing.v1.PricingService/CalculatePrice"
}[5m]))
```

```promql
sum by (insurance_hub_policy_event_type, insurance_hub_policy_event_outcome) (
  increase(insurance_hub_policy_event_publications_total{
    service_name="policy-service",deployment_environment_name="qa"
  }[15m])
)
```

```logql
{service_name="policy-service",deployment_environment_name="qa",service_implementation="go"}
  | json | outcome=~"internal_error|deadline_exceeded|cancelled|error"
    or msg="policy event publication deferred"
```

```traceql
{ resource.service.name = "policy-service" &&
  resource.deployment.environment.name = "qa" &&
  resource.insurance_hub.service.implementation = "go" }
```

Zero request rate with current Go runtime samples is healthy idle time. An available Deployment
without runtime samples is telemetry loss. Rising shared exporter failures, drops or queue use are
shared collection failures. Never stop shared Alloy, Prometheus, Loki, Tempo, PostgreSQL or Kafka
to create an incident; exercise outages only in disposable harnesses.

### Troubleshooting cases

Each case starts with the guidance of a peer Go service that has the same boundary. Dashboard is
the reference for runtime, releases and Kafka. Product has no runbook of its own, so Pricing's
observability runbook is the PostgreSQL reference; for a failing `GetProduct` call, start from the
[Product README](../product-service/README.md). When the peer has no comparable behavior, the case
says so and continues with Policy.

| Case | Peer consulted first | First Policy check | Distinguishing observation | Recovery check |
| --- | --- | --- | --- | --- |
| Release/Pod mismatch | [Dashboard deployment diagnostics](../dashboard-service/deployment-runbook.md#diagnostics-stop-conditions-and-recovery) | `make go-svc-deploy-status SVC_NAME=policy` with the QA coordinates from the [deployment runbook](deployment-runbook.md#flux-owned-qa) | The ready Pod image ID, `service.version` or Pod UID differs from the Flux-applied overlay and release digest, or **Readiness and restarts** shows a stuck rollout (`PolicyUnavailable`) while all three readiness gauges are 1 | Flux `qa-svc` is Ready at the reviewed revision, status and `go-svc-deploy-verify` pass with the recorded digest, and `PolicyUnavailable` clears |
| Failed provider RPC | The failing provider's guidance: [Pricing observability runbook](../pricing-service/observability-runbook.md#missing-telemetry-or-service-failure) for `CalculatePrice`, [Product README](../product-service/README.md) for `GetProduct` | **Upstream RPC outcomes** by `rpc_method` and status (`PolicyUpstreamRPCFailure`) | One `rpc_method` ends `UNAVAILABLE`, `DEADLINE_EXCEEDED`, `INTERNAL` or `UNKNOWN` while Policy readiness stays 1; business codes such as `NOT_FOUND` are not provider failures | The provider is ready, the failure ratio for that method returns below 5% and a forwarded offer completes |
| Database readiness loss | [Pricing reader symptoms](../pricing-service/observability-runbook.md#reader-symptoms) | **Database readiness** and `/readyz` through the private port-forward in the [deployment runbook](deployment-runbook.md#private-smoke-and-recovery) | `database` readiness is 0 while `gateway` and `kafka` stay 1 (`PolicyDatabaseNotReady`); lifecycle gRPC calls end `UNAVAILABLE` | `database` readiness returns to 1, `/readyz` returns `200 ready` and pending outbox intents drain |
| Delayed outbox publication | [Dashboard deployment runbook](../dashboard-service/deployment-runbook.md#diagnostics-stop-conditions-and-recovery) for Kafka coordinates; Dashboard only consumes, so it has no outbox to compare | **Event publication outcomes**, **Kafka publisher readiness** and **Database readiness** | See [Delayed outbox publication](#delayed-outbox-publication) | Pending intents return to zero and `success` publications resume, with no manual outbox edit |
| Consumer stalled on an acknowledged record | [Dashboard observability investigation](../dashboard-service/observability-runbook.md#investigation-workflow) (`DashboardEventStalled`) | **Event publication outcomes** shows `success` and the outbox has no pending intent for the topic | Policy acknowledged the record, yet the consumer group's committed offset is stationary while the log-end offset grows | See [Consumer stalled on an acknowledged record](#consumer-stalled-on-an-acknowledged-record) |

### Delayed outbox publication

The producer sends committed intents in order. It holds a row lock through broker acknowledgement
and then sets `published_at`; a policy's later intent waits while an earlier one is pending.
Separate the causes:

- **Database loss.** `database` readiness is 0 and logs show `policy event publication deferred`,
  without an increase in `insurance_hub_policy_event_publications_total` with
  `insurance_hub_policy_event_outcome="error"`. Follow the database readiness case; publication
  resumes on its own after recovery.
- **Broker loss.** `kafka` readiness is 0 (`PolicyKafkaNotReady`) or publications end `error`
  (`PolicyEventPublicationFailure`). Check broker availability, the bootstrap address
  `qa-kafka-kafka-bootstrap.qa-data.svc:9093` and the mounted CA described in
  [Kafka producer access](deployment-runbook.md#kafka-producer-access). Keep pending intents intact.
- **Neither.** Both gauges are 1 and no publication errors appear. Count pending intents with a
  read-only query that returns no keys or payloads. Run it only with authorization for a read-only
  `kubectl exec` into the Policy primary:

```sh
primary="$(kubectl --context qa-insurance-hub -n qa-data get \
  cluster.postgresql.cnpg.io qa-postgres-policy -o jsonpath='{.status.currentPrimary}')"
kubectl --context qa-insurance-hub -n qa-data exec "$primary" -c postgres -- \
  psql -d policy -At -c "SELECT topic, count(*), min(created_at)
    FROM public.policy_outbox WHERE published_at IS NULL GROUP BY topic"
```

An empty result means nothing is pending: publication is not delayed. If consumers still miss
events, continue with the next case. Never update, delete or reinsert outbox rows to force
publication.

### Consumer stalled on an acknowledged record

This walkthrough uses a documented example, not live values:

| Coordinate | Example |
| --- | --- |
| Consumer group | `dashboard-service` |
| Topic | `policy-registered` |
| Partition | `0` |
| Stalled offset | `42` |

**Symptom.** `DashboardEventStalled` fires: the Dashboard worker is enabled and its in-flight age
keeps rising. Policy shows `success` publications for `policy-registered` and the outbox query
returns no pending row for that topic.

**Read the group position.** Describe only the affected group. Use the broker-local plain
listener from inside a broker Pod:

```sh
kafka_pod="$(kubectl --context qa-insurance-hub -n qa-data get pod \
  -l strimzi.io/cluster=qa-kafka,strimzi.io/pool-name=broker \
  -o jsonpath='{.items[0].metadata.name}')"
kubectl --context qa-insurance-hub -n qa-data exec "$kafka_pod" -c kafka -- \
  /opt/kafka/bin/kafka-consumer-groups.sh --bootstrap-server localhost:9092 \
  --describe --group dashboard-service
```

In the example, partition `0` reports `CURRENT-OFFSET 42` on repeated reads while
`LOG-END-OFFSET` and `LAG` grow. Policy has already acknowledged the record at offset 42, so the
problem is in the consumer. A publisher retry would act on the wrong boundary: it could only
append a duplicate after offset 42, and the group would stay at 42.

**Identify the failure without reading the payload.** Find the Dashboard failure log for that
period and follow its `trace_id` into Tempo. The persisted trace context links it to the Policy
command that produced the event. Dashboard skips malformed envelopes. It stops the fetched batch
without committing when a record decodes but cannot be processed, for example an envelope without
a policy or a projection write failure. Rule out transient causes first: the Elasticsearch outcome
panels in the Dashboard runbook show whether the write path is failing for every record.

**Check the batch encoding.** Policy sends record batches uncompressed, so every consumer can
decode them without native compression libraries. Confirm the stalled batch reports
`compresscodec: none` without printing record data:

```sh
kubectl --context qa-insurance-hub -n qa-data exec "$kafka_pod" -c kafka -- sh -c '
  dir="$(find /var/lib/kafka -maxdepth 3 -type d -name policy-registered-0 | head -n 1)"
  for f in "$dir"/*.log; do
    /opt/kafka/bin/kafka-dump-log.sh --files "$f"
  done' | awk '/baseOffset:/ { for (i = 1; i < NF; i++) {
    if ($i == "baseOffset:") b = $(i + 1); if ($i == "lastOffset:") l = $(i + 1) }
    if (b <= 42 && 42 <= l) print }'
```

Run it on a broker Pod that hosts partition `0`; an empty result on another broker is not
evidence. It reads only batch headers; do not add `--print-data-log`. A codec other than `none`
points to a different producer on the shared topic, not to this record's content.

**Effect of skipping the record.** Offsets belong to each group. Advancing `dashboard-service`
past offset 42 makes Dashboard permanently miss that one registration: its projection undercounts
that policy until a separately authorized correction. Other groups on `policy-registered` keep
their own offsets and still receive it. Policy will not send it again, because its intent is
already marked published.

**Authorization.** The QA Kafka TLS listener on port 9093 and the broker-local plain listener
have no client authentication or ACLs. A successful broker connection therefore shows neither
client isolation nor downstream processing, and nothing at the broker stops any reachable client
from committing offsets for any group. Only procedure limits scoped advancement. It needs explicit
authorization from the owner of group `dashboard-service` for exactly that group, topic, partition
and target offset. That authorization does not extend to any other group, partition or shared
topic. During qualification and routine investigation, leave live offsets and topics unchanged.

**Scoped advancement (owner-authorized only).** Disable the Dashboard worker through a reviewed
`EVENT_WORKERS_ENABLED=false` change reconciled by Flux, and confirm the group has no active
members. Preview, then apply only the named partition. The dry run must list exactly one row,
`policy-registered 0 43`:

```sh
kubectl --context qa-insurance-hub -n qa-data exec "$kafka_pod" -c kafka -- \
  /opt/kafka/bin/kafka-consumer-groups.sh --bootstrap-server localhost:9092 \
  --group dashboard-service --topic policy-registered:0 --reset-offsets --to-offset 43 --dry-run
```

Replace `--dry-run` with `--execute` once, then re-enable the worker through a reviewed change.
Record the skipped group, topic, partition, offset and trace ID as evidence.

**Recovery check.** Describe the group again over several minutes. `CURRENT-OFFSET` for partition
`0` must move past 43 and `LAG` must fall toward zero. Dashboard in-flight age must return to zero,
`DashboardEventStalled` must clear and Dashboard registration outcomes must show completions
again. Policy's publication signals should be unchanged throughout.

### Alert responses

| Alert | First checks |
| --- | --- |
| `PolicyServiceErrorRate` | Split by operation and route, open a failed trace, then check upstream RPC and the three readiness gauges. |
| `PolicyHighLatency` | Compare request p95 with upstream RPC p95 per method and open a slow trace. |
| `PolicyUpstreamRPCFailure` | The failed provider RPC case. |
| `PolicyDatabaseNotReady` | The database readiness case; expect deferred publication until it recovers. |
| `PolicyKafkaNotReady`, `PolicyEventPublicationFailure` | The delayed outbox publication case, broker loss branch. |
| `PolicyUnavailable`, `PolicyUnexpectedRestart` | The release/Pod mismatch case, then prior container state, Pod events and safe logs. |
| `PolicyResourcePressure` | Request and publication load, Go runtime memory, CPU throttling and the 200m/128Mi limits. |
| `PolicyTelemetryMissing` | Effective `OTEL_*` and `SERVICE_VERSION` in the Policy ConfigMap, the Pod identity, egress to Alloy and **Telemetry delivery health**. |
| `PolicyTelemetryDeliveryFailure`, `PolicyTelemetryBacklog` | Isolate Loki, remote write or OTLP export in the shared Alloy pipeline; Policy keeps serving. |

Do not change thresholds or windows to hide missing or firing data.

## Recovery, rollback and cleanup

If a source, runtime or network mismatch appears, stop and use the Policy
[deployment recovery procedure](deployment-runbook.md#private-smoke-and-recovery) before
classifying an application defect. Restore prior monitoring assets, or remove a first installation,
through the UID-guarded steps of the
[Dashboard recovery procedure](../dashboard-service/observability-runbook.md#recovery-rollback-and-cleanup)
with the Policy object names, followed by the reviewed repository revert. Delete only
`prometheusrule/qa-policy-service` and `configmap/qa-policy-dashboard` in `qa-monitoring`, and
only with separate authorization.

Do not delete the Grafana or Prometheus releases, PVCs, the Policy workload, PostgreSQL or Kafka
resources, outbox rows, consumer groups or topics from this runbook. Stop a foreground UI or
port-forward with Ctrl+C and confirm the owning command removed its local process.

## Evidence checklist

Retain the following without credentials, policy keys, payloads or business data:

- the reviewed monitoring source revision and the Policy image, digest and Pod image ID;
- rendered object hashes, server dry-run, apply and read-back results and object UIDs;
- bounded query results, alert firing and recovery times and the case followed;
- the peer reference consulted first and the Policy check that followed it;
- for a stalled consumer, the group, topic, partition, offsets before and after, owner
  authorization and the recovery observations;
- owned cleanup and the final accepted state.
