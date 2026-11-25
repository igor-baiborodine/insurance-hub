<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Policy observability runbook](#policy-observability-runbook)
  - [Signal and decision boundary](#signal-and-decision-boundary)
  - [Source and disposable validation](#source-and-disposable-validation)
  - [QA preflight and stop conditions](#qa-preflight-and-stop-conditions)
  - [Provision, apply and read back](#provision-apply-and-read-back)
  - [Investigation workflow](#investigation-workflow)
    - [Troubleshooting cases](#troubleshooting-cases)
      - [Release/Pod mismatch](#releasepod-mismatch)
      - [Failed provider RPC](#failed-provider-rpc)
      - [Database readiness loss](#database-readiness-loss)
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
generated resources, suspend Flux, change Kafka offsets or mutate other monitoring assets while
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
check is a read-only outbox query. Policy has no consumer-lag series; consumer progress is read
from the broker.

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

## QA preflight and stop conditions

Keep these commands in one shell so the evidence directory and source revision stay available.
Start from a clean checkout of the exact reviewed revision and capture the current state before
any write. The capture runs in a fail-fast subshell; keep it as a standalone command, because combining it
with `||` or `&&` disables its fail-fast behavior. Any failed read, including an RBAC denial or
an API timeout, aborts it before either object is classified. Only a successful, empty
`--ignore-not-found` lookup of the named object records its absence, and a returned object must
carry the expected name, namespace and a UID:

```sh
export POLICY_SOURCE_REVISION="$(git rev-parse HEAD)"
export MONITORING_EVIDENCE_DIR="$(mktemp -d)"
(
  set -eu
  dir="$MONITORING_EVIDENCE_DIR"
  test -z "$(git status --porcelain)"
  test "$(kubectl config current-context)" = qa-insurance-hub
  k() { kubectl --context qa-insurance-hub --request-timeout=30s "$@"; }

  k -n flux-system get gitrepository/insurance-hub -o json > "$dir/flux-source.json"
  k -n flux-system get kustomization/qa-svc -o json > "$dir/flux-qa-svc.json"
  k -n qa-svc get deployment/qa-policy-api -o json > "$dir/policy-deployment.json"

  for pair in rule:prometheusrule/qa-policy-service dashboard:configmap/qa-policy-dashboard; do
    kind="${pair%%:*}" ref="${pair#*:}"
    prior="$dir/prior-$kind.json"
    k -n qa-monitoring get "$ref" -o json --ignore-not-found > "$prior"
    if test -s "$prior"; then
      jq -e --arg name "${ref#*/}" '.metadata.name == $name
        and .metadata.namespace == "qa-monitoring"
        and ((.metadata.uid // "") | length > 0)' "$prior" > /dev/null
      jq -S '{apiVersion, kind, metadata: {name: .metadata.name, namespace: .metadata.namespace,
        uid: .metadata.uid}, spec, data}' "$prior" > "$dir/prior-$kind-source.json"
      sha256sum "$dir/prior-$kind-source.json"
    else
      : > "$dir/prior-$kind.absent"
    fi
  done

  if test -s "$dir/prior-rule.json"; then
    test -s "$dir/prior-dashboard.json"
  else
    test ! -s "$dir/prior-dashboard.json"
  fi
  : > "$dir/preflight.ok"
)
test -f "$MONITORING_EVIDENCE_DIR/preflight.ok" || echo "STOP: preflight failed" >&2
```

The final guard stops when only one of the two objects exists. Every later block in this runbook
requires `preflight.ok`; when it is missing, discard the evidence directory, resolve the access or
identity error and rerun the whole preflight. Before a live operation, record:

- the full reviewed commit containing the monitoring assets;
- the Flux `insurance-hub` source and `qa-svc` applied revisions;
- the running Policy image reference, registry digest, Pod image ID and runtime `service.version`;
- the UIDs and source hashes of `qa-policy-service` and `qa-policy-dashboard`, or their verified
  absence for a first installation.

Stop before mutation when the context, namespace, source revision, object name or UID, Policy
image identity or current asset source differs from the reviewed record. Also stop for missing
Policy signals, unexplained firing alerts, a mutable Grafana provider or any operation that would
alter shared monitoring releases, PVCs, Alloy, Prometheus, Loki, Tempo, PostgreSQL, Kafka or other
monitoring assets.

Render the exact objects, keep their hashes and ask the API server to admit them without
persisting either:

```sh
test -f "$MONITORING_EVIDENCE_DIR/preflight.ok"
make --no-print-directory -C k8s prometheus-rules-render ENV_NAME=qa SVC_NAME=policy \
  > "$MONITORING_EVIDENCE_DIR/candidate-rule.yaml"
make --no-print-directory -C k8s grafana-dashboard-render ENV_NAME=qa SVC_NAME=policy \
  > "$MONITORING_EVIDENCE_DIR/candidate-dashboard.yaml"
sha256sum "$MONITORING_EVIDENCE_DIR"/candidate-*.yaml \
  > "$MONITORING_EVIDENCE_DIR/candidate-render.sha256"
kubectl --context qa-insurance-hub -n qa-monitoring apply --dry-run=server \
  -f "$MONITORING_EVIDENCE_DIR/candidate-rule.yaml"
kubectl --context qa-insurance-hub -n qa-monitoring apply --dry-run=server \
  -f "$MONITORING_EVIDENCE_DIR/candidate-dashboard.yaml"
```

Confirm the render names `qa-policy-service` and `qa-policy-dashboard`, namespace
`qa-monitoring`, selector `service_name="policy-service"`, 30-second evaluation and refresh
intervals, warning severity and `editable: false`. Rendering proves source shape only.

## Provision, apply and read back

The apply targets repeat source validation, run a server-side dry run, apply only the selected
object and read back its existence. Run them only with authorization for these two exact QA
objects:

```sh
test -f "$MONITORING_EVIDENCE_DIR/preflight.ok"
make -C k8s prometheus-rules-apply ENV_NAME=qa SVC_NAME=policy
make -C k8s grafana-dashboard-apply ENV_NAME=qa SVC_NAME=policy
```

Re-render the same source and require it to match the retained candidate byte for byte:

```sh
make --no-print-directory -C k8s prometheus-rules-render ENV_NAME=qa SVC_NAME=policy \
  > "$MONITORING_EVIDENCE_DIR/rerendered-rule.yaml"
make --no-print-directory -C k8s grafana-dashboard-render ENV_NAME=qa SVC_NAME=policy \
  > "$MONITORING_EVIDENCE_DIR/rerendered-dashboard.yaml"
cmp "$MONITORING_EVIDENCE_DIR/candidate-rule.yaml" "$MONITORING_EVIDENCE_DIR/rerendered-rule.yaml"
cmp "$MONITORING_EVIDENCE_DIR/candidate-dashboard.yaml" \
  "$MONITORING_EVIDENCE_DIR/rerendered-dashboard.yaml"
kubectl --context qa-insurance-hub -n qa-monitoring get prometheusrule/qa-policy-service -o yaml \
  > "$MONITORING_EVIDENCE_DIR/applied-rule.yaml"
kubectl --context qa-insurance-hub -n qa-monitoring get configmap/qa-policy-dashboard -o yaml \
  > "$MONITORING_EVIDENCE_DIR/applied-dashboard.yaml"
```

Open Grafana through the owned foreground path and confirm dashboard UID
`insurance-hub-policy-service` is provisioned with `editable: false` and that the provider
disables UI updates and deletion. Confirm the active Prometheus group
`insurance-hub-policy-service` holds 12 alerts with the reviewed expressions and pending windows.
Then run the [investigation queries](#investigation-workflow) to confirm live Policy samples.
Keep render, admission, apply, read-back and live-signal evidence distinct.

```sh
make -C k8s grafana-ui
```

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
shared collection failures. A failing `/readyz` with a passing `/livez` points to a dependency;
unexpected restarts point to liveness, memory or rollout causes. Never stop shared Alloy,
Prometheus, Loki, Tempo, PostgreSQL or Kafka to create an incident; exercise outages only in
disposable harnesses.

### Troubleshooting cases

| Case | First check | Distinguishing observation | Recovery check |
| --- | --- | --- | --- |
| Release/Pod mismatch | [Release/Pod mismatch](#releasepod-mismatch) | The running image, version or Pod differs from the Flux-applied release | Status and verification pass with the recorded digest; `PolicyUnavailable` clears |
| Failed provider RPC | **Upstream RPC outcomes** by `rpc_method` and status | One provider method fails with transport codes while Policy readiness stays 1 | The failure ratio for that method returns below 5% and an offer completes |
| Database readiness loss | **Database readiness** and `/readyz` | `database` readiness is 0 while `gateway` and `kafka` stay 1 | `database` readiness is 1, `/readyz` returns `200 ready` and pending intents drain |
| Delayed outbox publication | **Event publication outcomes** with both readiness gauges | Deferred warnings without `error` outcomes, or pending intents with both gauges at 1 | Pending intents return to zero and `success` publications resume |
| Consumer stalled on an acknowledged record | Policy `success` publications and no pending intent | The group's committed offset is stationary while the log-end offset grows | The committed offset moves past the record and lag falls |

#### Release/Pod mismatch

1. Run `make go-svc-deploy-status SVC_NAME=policy` with the QA coordinates from
   [Flux-owned QA](deployment-runbook.md#flux-owned-qa).
2. Compare the Flux `qa-svc` applied revision, the release digest, the ready Pod image ID, its
   `service.version` and Pod UID with the recorded release. Read **Readiness and restarts**.
3. A mismatch in identity, or a stuck rollout (`PolicyUnavailable`) while all three readiness
   gauges are 1, is a release problem rather than a dependency failure. When Flux, DNS or
   registry state looks stale, apply the
   [QA network recovery runbook](../../k8s/base-cluster-how-tos.md#qa-network-recovery) first.
4. Recover through the reviewed source revert or release procedure in the
   [deployment runbook](deployment-runbook.md#private-smoke-and-recovery); never patch the
   workload. Confirm Flux is Ready at the reviewed revision, `go-svc-deploy-status` and
   `go-svc-deploy-verify` pass with the recorded digest, and `PolicyUnavailable` clears.

#### Failed provider RPC

1. Read **Upstream RPC outcomes** and **Upstream RPC latency percentiles** by `rpc_method`
   (`PolicyUpstreamRPCFailure`). The method names the provider: `GetProduct` is Product and
   `CalculatePrice` is Pricing.
2. Only `UNAVAILABLE`, `DEADLINE_EXCEEDED`, `INTERNAL` and `UNKNOWN` count as provider failures.
   Business codes such as `NOT_FOUND` reflect the request. Policy readiness stays 1 because
   providers are not part of readiness.
3. Check the provider Deployment and Policy's path to it:

   ```sh
   kubectl --context qa-insurance-hub -n qa-svc get deployment qa-product-api qa-pricing-api
   kubectl --context qa-insurance-hub -n qa-svc get networkpolicy qa-policy-api -o yaml
   ```

   Egress must reach gRPC port 9090 of the provider Pods, as described in
   [Provider access](deployment-runbook.md#provider-access). Rising latency up to
   `PROVIDER_CALL_TIMEOUT` with `DEADLINE_EXCEEDED` points to a slow provider; `UNAVAILABLE` points
   to an unavailable or unreachable one.
4. After the provider is ready, confirm the failure ratio for that method returns below 5% and a
   forwarded offer completes.

#### Database readiness loss

1. Read **Database readiness** (`PolicyDatabaseNotReady`) and probe `/readyz` through the private
   port-forward in [Private smoke and recovery](deployment-runbook.md#private-smoke-and-recovery).
2. `database` readiness 0 with `gateway` and `kafka` at 1 isolates PostgreSQL. Lifecycle gRPC
   calls end `UNAVAILABLE`, and publication is deferred.
3. Check the `qa-postgres-policy` cluster in `qa-data`, the NetworkPolicy path to its Pods on
   port 5432, and the writer Secret `qa-postgres-policy-writer-creds` and its CA:

   ```sh
   kubectl --context qa-insurance-hub -n qa-data get cluster.postgresql.cnpg.io qa-postgres-policy \
     -o jsonpath='{.status.phase} {.status.currentPrimary}{"\n"}'
   ```

   For credential, TLS or grant causes, run the read-only `resource-inspect` and migration
   `status` with QA coordinates as described in
   [PostgreSQL storage](deployment-runbook.md#postgresql-storage).
4. Confirm `database` readiness returns to 1, `/readyz` returns `200 ready` and pending outbox
   intents drain.

#### Delayed outbox publication

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
events, continue with the next case. Recovery is complete when the query returns no rows and
`success` publications resume. Never update, delete or reinsert outbox rows to force publication.

#### Consumer stalled on an acknowledged record

This walkthrough uses a documented example, not live values:

| Coordinate | Example |
| --- | --- |
| Consumer group | `dashboard-service` |
| Topic | `policy-registered` |
| Partition | `0` |
| Stalled offset | `42` |

**Symptom.** Consumers report missing registrations, while Policy shows `success` publications for
`policy-registered` and the outbox query returns no pending row for that topic.

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

**Identify the record without reading the payload.** Find the consumer's failure log for that
period and follow its `trace_id` into Tempo. Policy forwards its persisted trace context as Kafka
headers, so the trace links back to the Policy command that produced the event. Rule out a
consumer-wide outage first: a group that fails on every record has a dependency problem, not a
single blocking record.

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
past offset 42 makes that group permanently miss one registration: its projection undercounts that
policy until a separately authorized correction. Other groups on `policy-registered` keep their own
offsets and still receive it. Policy will not send it again, because its intent is already marked
published.

**Authorization.** The QA Kafka TLS listener on port 9093 and the broker-local plain listener
have no client authentication or ACLs. A successful broker connection therefore shows neither
client isolation nor downstream processing, and nothing at the broker stops any reachable client
from committing offsets for any group. Only procedure limits scoped advancement. It needs explicit
authorization from the owner of group `dashboard-service` for exactly that group, topic, partition
and target offset. That authorization does not extend to any other group, partition or shared
topic. During qualification and routine investigation, leave live offsets and topics unchanged.

**Scoped advancement (owner-authorized only).** The group owner stops its consumers through a
reviewed change, and the group must report no active members. Preview, then apply only the named
partition. The dry run must list exactly one row, `policy-registered 0 43`:

```sh
kubectl --context qa-insurance-hub -n qa-data exec "$kafka_pod" -c kafka -- \
  /opt/kafka/bin/kafka-consumer-groups.sh --bootstrap-server localhost:9092 \
  --group dashboard-service --topic policy-registered:0 --reset-offsets --to-offset 43 --dry-run
```

Replace `--dry-run` with `--execute` once, then the owner restarts its consumers through a
reviewed change. Record the skipped group, topic, partition, offset and trace ID as evidence.

**Recovery check.** Describe the group again over several minutes. `CURRENT-OFFSET` for partition
`0` must move past 43 and `LAG` must fall toward zero as the group catches up. Policy's publication
signals should be unchanged throughout.

### Alert responses

| Alert | First checks |
| --- | --- |
| `PolicyServiceErrorRate` | Split by operation and route, open a failed trace, then check upstream RPC and the three readiness gauges. |
| `PolicyHighLatency` | Compare request p95 with upstream RPC p95 per method and open a slow trace. |
| `PolicyUpstreamRPCFailure` | [Failed provider RPC](#failed-provider-rpc). |
| `PolicyDatabaseNotReady` | [Database readiness loss](#database-readiness-loss); expect deferred publication until it recovers. |
| `PolicyKafkaNotReady`, `PolicyEventPublicationFailure` | [Delayed outbox publication](#delayed-outbox-publication), broker loss branch. |
| `PolicyUnavailable`, `PolicyUnexpectedRestart` | [Release/Pod mismatch](#releasepod-mismatch), then prior container state, Pod events and safe logs. |
| `PolicyResourcePressure` | Request and publication load, Go runtime memory, CPU throttling and the 200m/128Mi limits. |
| `PolicyTelemetryMissing` | Effective `OTEL_*` and `SERVICE_VERSION` in `qa-policy-config`, the Pod identity, egress to Alloy and **Telemetry delivery health**. |
| `PolicyTelemetryDeliveryFailure`, `PolicyTelemetryBacklog` | Isolate Loki, remote write or OTLP export in the shared Alloy pipeline; Policy keeps serving. |

Do not change thresholds or windows to hide missing or firing data.

## Recovery, rollback and cleanup

If a source, runtime or network mismatch appears, stop and use the
[deployment recovery procedure](deployment-runbook.md#private-smoke-and-recovery) before
classifying an application defect.

To restore prior monitoring assets, first verify that the preflight completed and that the
captured UIDs still identify the two objects:

```sh
test -f "$MONITORING_EVIDENCE_DIR/preflight.ok"
test "$(kubectl --context qa-insurance-hub -n qa-monitoring get \
  prometheusrule/qa-policy-service -o jsonpath='{.metadata.uid}')" \
  = "$(jq -r '.metadata.uid' "$MONITORING_EVIDENCE_DIR/prior-rule.json")"
test "$(kubectl --context qa-insurance-hub -n qa-monitoring get \
  configmap/qa-policy-dashboard -o jsonpath='{.metadata.uid}')" \
  = "$(jq -r '.metadata.uid' "$MONITORING_EVIDENCE_DIR/prior-dashboard.json")"
```

Check out the captured accepted revision in an isolated checkout, rerun its asset validation and
apply only those two objects through the same guarded Make targets. Read them back, rebuild their
source payloads with the `jq` filter from the preflight, and compare them with
`prior-rule-source.json` and `prior-dashboard-source.json` using `cmp`. Follow the live restore
with the reviewed repository revert.

A first installation is established only when the preflight completed and recorded both absence
markers. For a first installation that must be removed, separately authorize deletion of only:

```sh
test -f "$MONITORING_EVIDENCE_DIR/preflight.ok"
test -f "$MONITORING_EVIDENCE_DIR/prior-rule.absent"
test -f "$MONITORING_EVIDENCE_DIR/prior-dashboard.absent"
kubectl --context qa-insurance-hub -n qa-monitoring delete prometheusrule/qa-policy-service
kubectl --context qa-insurance-hub -n qa-monitoring delete configmap/qa-policy-dashboard
```

Verify exact absence and the continued health of shared monitoring. Do not delete the Grafana or
Prometheus releases, PVCs, the Policy workload, PostgreSQL or Kafka resources, outbox rows,
consumer groups or topics from this runbook. Stop a foreground UI or port-forward with Ctrl+C and
confirm the owning command removed its local process.

## Evidence checklist

Retain the following without credentials, policy keys, payloads or business data:

- the reviewed monitoring source revision and the Policy image, digest and Pod image ID;
- rendered object hashes, server dry-run, apply and read-back results and object UIDs;
- bounded query results, alert firing and recovery times and the case followed;
- for a stalled consumer, the group, topic, partition, offsets before and after, owner
  authorization and the recovery observations;
- owned cleanup and the final accepted state.
