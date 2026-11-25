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

Policy exports these application metrics, all named `insurance_hub_policy_*` so the shared QA
collector keeps them with their bounded dimensions:

- request outcome and duration by operation and transport: `insurance_hub_policy_requests_total`
  and `insurance_hub_policy_request_duration_seconds`;
- provider call outcome and duration: `insurance_hub_policy_dependencies_total` and
  `insurance_hub_policy_dependency_duration_seconds`, with `insurance_hub_policy_dependency` set to
  `product` (operation `get_product`) or `pricing` (operation `calculate_price`);
- event publication outcome and duration by event type: `insurance_hub_policy_events_total` and
  `insurance_hub_policy_event_duration_seconds`;
- one readiness gauge per dependency: `insurance_hub_policy_dependency_ready` with
  `insurance_hub_policy_dependency` set to `gateway`, `database` or `kafka`.

Go runtime signals complete the set. The dashboard adds Pod availability, resource and restart
views and shared Alloy delivery health. Labels never contain policy keys, routes, payloads,
customer values, topics, groups, credentials or trace IDs.

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
make -C k8s alloy-config-check
make -C k8s test-observability-backends OBSERVABILITY_SERVICE_DOMAIN=policy
```

The first command validates the typed inventory, the pinned chart render, Prometheus rule syntax
and the rule fixtures. The second queries every dashboard expression against collected disposable
data and requires healthy alerts to stay inactive. The third validates the reviewed shared Alloy
configuration. The fourth sends real Policy request, provider call, event and readiness signals
through that configuration into disposable backends and requires each series to arrive with its
bounded dimensions. None of them applies QA resources or proves live signal delivery. The shared `verify-service-observability` target has no Policy probe; verify live
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
monitoring assets, except the separately authorized Alloy release below.

The shared Alloy release decides which Policy metrics reach Prometheus. Check that the reviewed
shared configuration is effective before provisioning the dashboard or rules. Every read must
succeed; only then does the comparison decide between a match and drift:

```sh
test -f "$MONITORING_EVIDENCE_DIR/preflight.ok"
rm -f "$MONITORING_EVIDENCE_DIR/alloy.ok" "$MONITORING_EVIDENCE_DIR/alloy-drift.diff"
(
  set -eu
  dir="$MONITORING_EVIDENCE_DIR"
  k() { kubectl --context qa-insurance-hub --request-timeout=30s "$@"; }

  helm --kube-context qa-insurance-hub -n qa-monitoring status qa-alloy -o json \
    > "$dir/prior-alloy-helm-status.json"
  k -n qa-monitoring get deployment/qa-alloy -o json > "$dir/prior-alloy-deployment.json"
  k -n qa-monitoring get configmap/qa-alloy -o json > "$dir/prior-alloy-configmap.json"
  jq -er '.data["config.alloy"]' "$dir/prior-alloy-configmap.json" \
    > "$dir/effective-alloy-before.alloy"
  if cmp -s k8s/overlays/qa/infra/alloy/config.alloy "$dir/effective-alloy-before.alloy"; then
    : > "$dir/alloy.ok"
  else
    diff k8s/overlays/qa/infra/alloy/config.alloy "$dir/effective-alloy-before.alloy" \
      > "$dir/alloy-drift.diff" || true
  fi
)
if test -f "$MONITORING_EVIDENCE_DIR/alloy.ok"; then
  echo "Alloy: reviewed configuration is effective"
elif test -s "$MONITORING_EVIDENCE_DIR/alloy-drift.diff"; then
  echo "STOP: Alloy configuration drift; separate Alloy release required" >&2
else
  echo "STOP: Alloy read failed" >&2
fi
```

`alloy.ok` means every read succeeded and the effective configuration matches the reviewed file;
no Alloy rollout is needed. A non-empty `alloy-drift.diff` without `alloy.ok` means the reads
succeeded and the configuration differs. Stop this workflow, review the entire shared-collector
delta from a sanitized copy of that diff and obtain separate authorization for the Alloy release.
Neither file means a read failed; resolve the access error and rerun this block. Only that
separate release may run, and it writes `alloy.ok` only after the rollout, status and read-back
all succeed:

```sh
test -f "$MONITORING_EVIDENCE_DIR/preflight.ok"
test -s "$MONITORING_EVIDENCE_DIR/alloy-drift.diff"
(
  set -eu
  dir="$MONITORING_EVIDENCE_DIR"
  k() { kubectl --context qa-insurance-hub --request-timeout=30s "$@"; }

  make -C k8s alloy-install
  k -n qa-monitoring rollout status deployment/qa-alloy --timeout=10m
  make -C k8s alloy-status
  helm --kube-context qa-insurance-hub -n qa-monitoring status qa-alloy -o json \
    > "$dir/applied-alloy-helm-status.json"
  k -n qa-monitoring get configmap/qa-alloy -o json > "$dir/applied-alloy-configmap.json"
  jq -er '.data["config.alloy"]' "$dir/applied-alloy-configmap.json" \
    > "$dir/effective-alloy-after.alloy"
  cmp k8s/overlays/qa/infra/alloy/config.alloy "$dir/effective-alloy-after.alloy"
  : > "$dir/alloy.ok"
)
test -f "$MONITORING_EVIDENCE_DIR/alloy.ok" || echo "STOP: Alloy release failed" >&2
```

After an authorized Alloy rollout, recheck the shared collector's existing routes, delivery
counters and backend signals before continuing. Stop if any regress. Rendering and provisioning
below require `alloy.ok` in addition to `preflight.ok`.

Then confirm that the Policy application series arrive from the candidate Pod itself. Take
`<service-version>` and `<pod-uid>` from the recorded running Policy `service.version` and Pod
UID; the collector sets `service_instance_id` to the Pod UID. A series from another replica or
version, such as an older Pod still serving during a rolling update, does not count.

At startup, before any traffic, the candidate exports its readiness gauge and the zero baseline of
failed event publications:

```promql
count by (__name__, insurance_hub_policy_dependency, insurance_hub_policy_event_type,
  insurance_hub_policy_outcome) ({__name__=~"insurance_hub_policy_(dependency_ready|events_total)",
  service_name="policy-service", deployment_environment_name="qa",
  service_version="<service-version>", service_instance_id="<pod-uid>"})
```

Expect `dependency_ready` for `gateway`, `database` and `kafka`, and `events_total` with outcome
`error` for `registered` and `terminated`. The successful `events_total` series and
`event_duration_seconds` appear only after a real publication; an offer does not publish, so do
not require them here.

Run this query immediately before and again after the single approved offer smoke:

```promql
sum by (__name__, insurance_hub_policy_operation, insurance_hub_policy_dependency,
  insurance_hub_policy_outcome) ({__name__=~"insurance_hub_policy_(requests_total|request_duration_seconds_count|dependencies_total|dependency_duration_seconds_count)",
  service_name="policy-service", deployment_environment_name="qa",
  service_version="<service-version>", service_instance_id="<pod-uid>"})
```

A series absent before the offer counts as zero. Expect each `create_offer` request series with
outcome `success`, and each `product`/`get_product` and `pricing`/`calculate_price` dependency
series with outcome `success`, to increase across the offer window. Stop when a series is missing,
does not increase or appears only for another Pod.

Authorization to apply the dashboard and rules does not authorize the Alloy release.

Render the exact objects, keep their hashes and ask the API server to admit them without
persisting either:

```sh
test -f "$MONITORING_EVIDENCE_DIR/preflight.ok"
test -f "$MONITORING_EVIDENCE_DIR/alloy.ok"
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
test -f "$MONITORING_EVIDENCE_DIR/alloy.ok"
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
sum by (insurance_hub_policy_dependency, insurance_hub_policy_outcome) (
  rate(insurance_hub_policy_dependencies_total{
    service_name="policy-service",deployment_environment_name="qa",
    insurance_hub_policy_dependency=~"product|pricing"
  }[5m])
)
```

```promql
sum by (insurance_hub_policy_event_type, insurance_hub_policy_outcome) (
  increase(insurance_hub_policy_events_total{
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
| Failed provider RPC | **Upstream RPC outcomes** by dependency and outcome | One provider ends `error` or `deadline_exceeded` while Policy readiness stays 1 | The failure ratio for that provider returns below 5% and an offer completes |
| Database readiness loss | **Database readiness** and `/readyz` | `database` readiness is 0 while `gateway` stays 1; `kafka` is unchecked by that probe | `database` and `kafka` readiness are 1, `/readyz` returns `200 ready` and pending intents drain |
| Delayed outbox publication | **Event publication outcomes** with both readiness gauges | Deferred warnings without `error` outcomes, or pending intents with both gauges at 1 | Pending intents return to zero and `success` publications resume |
| Consumer stalled on an acknowledged record | Policy `success` publications and no pending intent | The committed offset is stationary while the log-end offset grows, and an active assigned member repeatedly fails at that offset | The committed offset moves past the record and lag falls |

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

1. Read **Upstream RPC outcomes** and **Upstream RPC latency percentiles** by
   `insurance_hub_policy_dependency` (`PolicyUpstreamRPCFailure`): `product` is the Product
   `GetProduct` call and `pricing` is the Pricing `CalculatePrice` call.
2. Only the `error` outcome and `deadline_exceeded` count as provider failures. `error` covers
   every status the provider is not expected to return, such as `UNAVAILABLE`, `INTERNAL` or
   `UNIMPLEMENTED`. `invalid_request` covers only each provider's expected business status:
   Product `NOT_FOUND` for an unknown product and Pricing `INVALID_ARGUMENT` for a rejected
   quote. `cancelled` reflects the caller. Policy readiness stays 1 because providers are not part
   of readiness.
3. Check the provider Deployment and Policy's path to it:

   ```sh
   kubectl --context qa-insurance-hub -n qa-svc get deployment qa-product-api qa-pricing-api
   kubectl --context qa-insurance-hub -n qa-svc get networkpolicy qa-policy-api -o yaml
   ```

   Egress must reach gRPC port 9090 of the provider Pods, as described in
   [Provider access](deployment-runbook.md#provider-access). Rising latency up to
   `PROVIDER_CALL_TIMEOUT` with `deadline_exceeded` points to a slow provider; `error` points to an
   unavailable, unreachable or failing one.
4. After the provider is ready, confirm the failure ratio for that provider returns below 5% and
   a forwarded offer completes.

#### Database readiness loss

1. Read **Database readiness** (`PolicyDatabaseNotReady`). The single QA replica is unready
   during the outage, so a procedure that selects the ready Pod cannot reach it. Select the
   running Pod by namespace, Deployment ownership and UID instead, and forward only its
   management port:

   ```sh
   k() { kubectl --context qa-insurance-hub -n qa-svc "$@"; }
   deploy="$(k get deployment/qa-policy-api -o json)"
   selector="$(jq -r '.spec.selector.matchLabels | to_entries
     | map("\(.key)=\(.value)") | join(",")' <<<"$deploy")"
   owners="$(k get replicaset -l "$selector" -o json | jq -c \
     --arg uid "$(jq -r .metadata.uid <<<"$deploy")" '[.items[]
       | select(any(.metadata.ownerReferences[]?; .uid == $uid)) | .metadata.uid]')"
   k get pod -l "$selector" -o json | jq -r --argjson owners "$owners" '.items[]
     | select(.status.phase == "Running")
     | select(any(.metadata.ownerReferences[]?; .uid as $u | $owners | index($u)))
     | "\(.metadata.name) \(.metadata.uid)"'
   ```

   Require exactly one line, record its Pod UID, then forward that Pod:

   ```sh
   : "${POLICY_POD:?set the running Go Policy Pod name from the previous command}"
   kubectl --context qa-insurance-hub -n qa-svc port-forward --address 127.0.0.1 \
     "pod/$POLICY_POD" 18081:8081
   curl -sS --max-time 5 -w ' %{http_code}\n' http://127.0.0.1:18081/livez
   curl -sS --max-time 5 -w ' %{http_code}\n' http://127.0.0.1:18081/readyz
   ```

   Run the `curl` commands in another terminal. During the outage expect `/livez` 200 and
   `/readyz` 503. Repeat them during recovery and confirm the Pod UID is unchanged: a new UID
   means a restart, not a recovery of the same process.
2. `database` readiness 0 with `gateway` at 1 isolates PostgreSQL. The probe checks the gateway,
   then the database, and stops at the first failure, so a failing database probe never checks
   Kafka. Treat `kafka` as unknown while `database` is 0: the series may be absent or show an
   earlier probe. Confirm Kafka on the first complete probe after recovery. Lifecycle gRPC calls
   end `UNAVAILABLE`, and publication is deferred.
3. Check the `qa-postgres-policy` cluster in `qa-data`, the NetworkPolicy path to its Pods on
   port 5432, and the writer Secret `qa-postgres-policy-writer-creds` and its CA:

   ```sh
   kubectl --context qa-insurance-hub -n qa-data get cluster.postgresql.cnpg.io qa-postgres-policy \
     -o jsonpath='{.status.phase} {.status.currentPrimary}{"\n"}'
   ```

   For credential, TLS or grant causes, run the read-only `resource-inspect` and migration
   `status` with QA coordinates as described in
   [PostgreSQL storage](deployment-runbook.md#postgresql-storage).
4. Confirm `database` readiness returns to 1, a later `kafka` sample is 1, `/readyz` returns
   `200 ready` and pending outbox intents drain.

#### Delayed outbox publication

The producer sends committed intents in order. It holds a row lock through broker acknowledgement
and then sets `published_at`; a policy's later intent waits while an earlier one is pending.
Separate the causes:

- **Database loss.** `database` readiness is 0 and logs show `policy event publication deferred`,
  without an increase in `insurance_hub_policy_events_total` with
  `insurance_hub_policy_outcome="error"`. Follow the database readiness case; publication
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
`LOG-END-OFFSET` and `LAG` grow. Policy has acknowledged its records, so a publisher retry would act
on the wrong boundary: it could only append a duplicate after offset 42. A stationary offset alone
does not show that the record at offset 42 blocks the group. A stopped consumer, a lost assignment
or a fetch failure produces the same picture.

**Establish that the consumer is running and stuck at that position.** Require all of the
following before considering any skip:

1. The group has an active member assigned partition `0`:

   ```sh
   kubectl --context qa-insurance-hub -n qa-data exec "$kafka_pod" -c kafka -- \
     /opt/kafka/bin/kafka-consumer-groups.sh --bootstrap-server localhost:9092 \
     --describe --group dashboard-service --members --verbose
   ```

   One member's `ASSIGNMENT` must include `policy-registered(0)`. No member, or no owner for
   partition `0`, means the consumer is stopped or rebalancing; the group owner restores it, and
   offsets stay unchanged.
2. The consuming workload is Ready, is not restarting, and logs the same failure repeatedly
   across several poll intervals while offset 42 stays stationary.
3. The failure is attributable to that position, through one of two paths:
   - **Record-handling failure.** The consumer decoded the record and logs a failure for
     partition `0`, offset 42. Follow its `trace_id`, when present, into Tempo. Policy forwards its
     persisted trace context as Kafka headers, so the trace links back to the Policy command that
     produced the event. Do not read the payload.
   - **Fetch failure.** The client fails before it has a record, so no record trace ID exists.
     Attribute the failure from the group assignment above, the stationary partition and offset,
     and the broker batch metadata for the batch containing offset 42 (below).

Rule out a consumer-wide outage: a group that fails on every record, partition or topic has a
dependency problem, not a single blocking record. When any requirement is missing or the
attribution is inconclusive, leave offsets unchanged and hand the evidence to the group owner.

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
evidence. The printed `baseOffset`, `lastOffset` and codec are the batch metadata for the
fetch-failure path. It reads only batch headers; do not add `--print-data-log`. A codec other than `none`
points to a different producer on the shared topic, not to this record's content.

**Effect of skipping the record.** Offsets belong to each group. Advancing `dashboard-service`
past offset 42 makes that group permanently miss one registration: its projection undercounts that
policy until a separately authorized correction. Other groups on `policy-registered` keep their own
offsets and still receive it. Policy will not send it again, because its intent is already marked
published.

**Authorization.** The QA Kafka TLS listener on port 9093 and the broker-local plain listener
have no client authentication or ACLs. A successful broker connection therefore shows neither
client isolation nor downstream processing, and nothing at the broker stops any reachable client
from committing offsets for any group. Only procedure limits scoped advancement. It applies only after the diagnosis above is
established, and it needs explicit
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
reviewed change. Record the skipped group, topic, partition, offset, the failure path and the trace ID when one
exists as evidence.

**Recovery check.** Describe the group again over several minutes. `CURRENT-OFFSET` for partition
`0` must move past 43 and `LAG` must fall toward zero as the group catches up. Policy's publication
signals should be unchanged throughout.

### Alert responses

| Alert | First checks |
| --- | --- |
| `PolicyServiceErrorRate` | Split by operation, open a failed trace, then check upstream RPC and the three readiness gauges. |
| `PolicyHighLatency` | Compare request p95 with upstream RPC p95 per provider and open a slow trace. |
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
