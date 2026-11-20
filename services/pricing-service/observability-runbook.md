<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Pricing observability runbook](#pricing-observability-runbook)
  - [Signal and decision boundary](#signal-and-decision-boundary)
  - [Source and disposable validation](#source-and-disposable-validation)
  - [QA preflight and stop conditions](#qa-preflight-and-stop-conditions)
  - [Provision, apply and read back](#provision-apply-and-read-back)
  - [Investigation workflow](#investigation-workflow)
    - [Missing telemetry or service failure](#missing-telemetry-or-service-failure)
    - [Reader symptoms](#reader-symptoms)
    - [Alert responses](#alert-responses)
  - [Recovery, rollback and cleanup](#recovery-rollback-and-cleanup)
  - [Evidence checklist](#evidence-checklist)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Pricing observability runbook

This runbook covers source validation, disposable delivery checks, guarded QA provisioning,
read-back, investigation and restoration for the Go Pricing monitoring assets. It applies to any
Pricing release.

| Item | Value |
| --- | --- |
| Service key | `pricing` |
| Environment | `qa` |
| Kubernetes context | `qa-insurance-hub` |
| Monitoring namespace | `qa-monitoring` |
| Workload | Deployment `qa-pricing-api` in `qa-svc`, owned by Flux Kustomization `flux-system/qa-pricing-api` |
| PrometheusRule | `qa-pricing-service` (rule group `insurance-hub-pricing-service`) |
| Dashboard ConfigMap | `qa-pricing-dashboard` |
| Grafana dashboard UID | `insurance-hub-pricing-service` |

Make targets apply the dashboard and rules from the repository, separately from the Flux-owned
Pricing workload. While following this procedure:

- Do not edit the Grafana dashboard in the UI or patch the generated resources.
- Do not suspend Flux.
- Do not change another service's monitoring assets.

For deployment, private access and release recovery, use the
[deployment runbook](deployment-runbook.md).

For any troubleshooting, first check the matching reference service:

- **Dashboard** for shared deployment, logging, tracing, metrics, alerts and Alloy delivery:
  [Dashboard observability runbook](../dashboard-service/observability-runbook.md).
- **Product** for PostgreSQL reader access and pool behavior:
  [Product observability runbook](../../docs/migration/phase-4/product-service/observability-runbook.md).

## Signal and decision boundary

Pricing exports the following signals:

- **Requests.** `insurance_hub_pricing_requests_total` and
  `insurance_hub_pricing_request_duration_seconds` for operation `calculate_price`, with transport
  `http` or `grpc`, an outcome, and the HTTP or RPC status code.
- **Reader stages.** `insurance_hub_pricing_database_operations_total` by stage
  (`acquire`, `query`, `decode`) and outcome (`success`, `storage_unavailable`, `deadline_exceeded`,
  `cancelled`, `invalid_data`).
- **PostgreSQL client.** `db_client_operation_duration_seconds`,
  `db_client_connection_wait_time_seconds`, `db_client_connection_timeouts_total`,
  `db_client_connection_count` (`used`/`idle`) and `db_client_connection_max` for pool
  `pricing-reader`.
- **Go runtime.** Memory, GC, goroutine and processor series.
- **Logs.** One structured JSON completion log per transport request.
- **Traces.** W3C traces through HTTP server → gRPC client → gRPC server → `pricing.db.acquire` →
  `SELECT tariff`.

Each gateway quote is observed once on HTTP and once on the internal RPC, and direct gRPC callers
reach the RPC as well. So the dashboard's `quotes` series counts the gRPC observations, which is
each quote exactly once.

Labels, log fields and span attributes are fixed, bounded values: operation, transport, stage,
outcome, status, route template and database metadata. They must never contain:

- request or response payloads, customer inputs or answers;
- requested dates, product or cover values, prices or tariff rules;
- SQL or parameters, raw driver errors, DSNs or credentials.

Trace and span IDs appear only in logs and traces. Alloy keeps only Pricing-domain signals whose
identity matches the running Pod: `service.version` must equal the workload's
`app.kubernetes.io/version`, and `service.instance.id` must equal the Pod UID. Alloy drops anything
else.

The client sees each failure as follows:

| Client sees | Request outcome | Database outcome | Meaning |
| --- | --- | --- | --- |
| HTTP 503 / gRPC `UNAVAILABLE`, message `service unavailable` | `internal_error` | `storage_unavailable` | PostgreSQL refused or failed the connection or read |
| HTTP 504 / gRPC `DEADLINE_EXCEEDED` | `deadline_exceeded` | `deadline_exceeded`, often with acquire timeouts | Hung database, network blackhole or pool exhaustion |
| HTTP 500 / gRPC `INTERNAL` | `internal_error` | `invalid_data` | Stored tariff rows are unusable or ambiguous |
| HTTP 4xx / gRPC `INVALID_ARGUMENT` | `invalid_request` | none | Caller error; excluded from the error alert |

The pilot thresholds and windows are recorded in
`k8s/observability/services/pricing/acceptance.json`. They are operating limits, not production
SLOs.

## Source and disposable validation

From the repository root, validate the exact candidate before any QA operation:

```sh
make alloy-config-check
make monitoring-assets-check ENV_NAME=qa SVC_NAME=pricing
make test-monitoring ENV_NAME=qa SVC_NAME=pricing
make test-observability-backends OBSERVABILITY_SERVICE_DOMAIN=pricing
```

- **`alloy-config-check`** validates the Alloy collection policy, including the `pricing` JSON-log
  registration.
- **`monitoring-assets-check`** validates the asset inventory, the pinned chart render, rule
  syntax and the alert fixtures.
- **`test-monitoring`** queries every dashboard expression against collected disposable data and
  requires healthy alerts to stay inactive.
- **`test-observability-backends`** builds Pricing against a TLS PostgreSQL that it prepares with
  Pricing's own migrate and seed commands. It sends a healthy quote, stops the database for one
  quote, restarts it and repeats the quote. It then proves request, database, pool, runtime, log
  and trace delivery through disposable Alloy, Prometheus, Loki and Tempo, and confirms that a
  synthetic sentinel reaches no backend. Each run cleans up the resources it owns.

These checks do not apply QA resources or establish live QA behavior. Qualify the application
release separately, using the [deployment runbook](deployment-runbook.md).

## QA preflight and stop conditions

Keep these commands in one shell so the evidence directory and revisions remain available.

1. Start from a clean checkout of the exact reviewed monitoring revision.
2. Resolve the release source that Flux applies to Pricing.
3. Capture the current state before any write:

```sh
set -eu

export MONITORING_SOURCE_REVISION="$(git rev-parse --verify 'HEAD^{commit}')"
export MONITORING_EVIDENCE_DIR="$(mktemp -d)"
test -z "$(git status --porcelain)"
test "$(kubectl config current-context)" = qa-insurance-hub

kubectl --context qa-insurance-hub -n flux-system get \
  kustomization/qa-pricing-api -o json \
  > "$MONITORING_EVIDENCE_DIR/flux-pricing.json"
release_source="$(jq -r '.spec.sourceRef.name' "$MONITORING_EVIDENCE_DIR/flux-pricing.json")"
kubectl --context qa-insurance-hub -n flux-system get \
  "gitrepository/$release_source" -o json \
  > "$MONITORING_EVIDENCE_DIR/flux-release-source.json"
export PRICING_RELEASE_REVISION="$(jq -r '.spec.ref.commit' \
  "$MONITORING_EVIDENCE_DIR/flux-release-source.json")"
kubectl --context qa-insurance-hub -n qa-svc get \
  deployment/qa-pricing-api -o json \
  > "$MONITORING_EVIDENCE_DIR/pricing-deployment.json"
prior_rule_tmp="$MONITORING_EVIDENCE_DIR/prior-rule.tmp"
kubectl --context qa-insurance-hub -n qa-monitoring get \
  prometheusrule/qa-pricing-service -o json --ignore-not-found \
  > "$prior_rule_tmp"
mv "$prior_rule_tmp" "$MONITORING_EVIDENCE_DIR/prior-rule.json"
prior_dashboard_tmp="$MONITORING_EVIDENCE_DIR/prior-dashboard.tmp"
kubectl --context qa-insurance-hub -n qa-monitoring get \
  configmap/qa-pricing-dashboard -o json --ignore-not-found \
  > "$prior_dashboard_tmp"
mv "$prior_dashboard_tmp" "$MONITORING_EVIDENCE_DIR/prior-dashboard.json"

if test -s "$MONITORING_EVIDENCE_DIR/prior-rule.json"; then
  jq -S '{apiVersion, kind, metadata: {name: .metadata.name, namespace: .metadata.namespace, uid: .metadata.uid}, spec}' \
    "$MONITORING_EVIDENCE_DIR/prior-rule.json" \
    > "$MONITORING_EVIDENCE_DIR/prior-rule-source.json"
  sha256sum "$MONITORING_EVIDENCE_DIR/prior-rule-source.json"
else
  : > "$MONITORING_EVIDENCE_DIR/prior-rule.absent"
fi

if test -s "$MONITORING_EVIDENCE_DIR/prior-dashboard.json"; then
  jq -S '{apiVersion, kind, metadata: {name: .metadata.name, namespace: .metadata.namespace, uid: .metadata.uid}, data}' \
    "$MONITORING_EVIDENCE_DIR/prior-dashboard.json" \
    > "$MONITORING_EVIDENCE_DIR/prior-dashboard-source.json"
  sha256sum "$MONITORING_EVIDENCE_DIR/prior-dashboard-source.json"
else
  : > "$MONITORING_EVIDENCE_DIR/prior-dashboard.absent"
fi

if test -s "$MONITORING_EVIDENCE_DIR/prior-rule.json"; then
  test -s "$MONITORING_EVIDENCE_DIR/prior-dashboard.json"
else
  test ! -s "$MONITORING_EVIDENCE_DIR/prior-dashboard.json"
fi
```

The final guard stops the procedure when only one of the two monitoring objects exists. Before any
live operation, record:

- the full monitoring revision (`MONITORING_SOURCE_REVISION`);
- the Pricing release revision applied by `qa-pricing-api` (`PRICING_RELEASE_REVISION`) and its
  ready, applied state;
- the running image reference, registry digest, Pod image ID, OCI revision and runtime
  `service.version`, gathered with the [deployment runbook](deployment-runbook.md#flux-owned-qa);
- current UIDs and serialized source hashes for `qa-pricing-service` and `qa-pricing-dashboard`,
  or their verified absence for a first installation;
- the selected context, namespaces, service and environment.

Stop before any mutation if any of these differs from the reviewed record:

- the context, namespace or service selection;
- either revision;
- a resource name or UID;
- the release image identity;
- the current asset source.

Also stop for:

- missing Pricing signals or an unexplained firing alert;
- a mutable Grafana provider;
- an unbounded or sensitive query;
- any operation that would alter shared monitoring releases, PVCs, Alloy, Prometheus, Loki, Tempo,
  PostgreSQL or another service's resources.

For a first installation, when both `.absent` markers exist, run the read-only pre-provision
verifier. It takes the Pricing release revision that Flux applies:

```sh
make verify-service-observability \
  ENV_NAME=qa \
  SVC_NAME=pricing \
  VERIFY_MODE=pre-provision \
  VERIFY_SOURCE_REVISION="$PRICING_RELEASE_REVISION"
```

Render the exact objects, keep their hashes, and ask the API server to admit them without
persisting either:

```sh
make --no-print-directory -C k8s \
  prometheus-rules-render ENV_NAME=qa SVC_NAME=pricing \
  > "$MONITORING_EVIDENCE_DIR/candidate-rule.yaml"
make --no-print-directory -C k8s \
  grafana-dashboard-render ENV_NAME=qa SVC_NAME=pricing \
  > "$MONITORING_EVIDENCE_DIR/candidate-dashboard.yaml"
sha256sum \
  "$MONITORING_EVIDENCE_DIR/candidate-rule.yaml" \
  "$MONITORING_EVIDENCE_DIR/candidate-dashboard.yaml" \
  > "$MONITORING_EVIDENCE_DIR/candidate-render.sha256"
kubectl --context qa-insurance-hub -n qa-monitoring apply --dry-run=server \
  -f "$MONITORING_EVIDENCE_DIR/candidate-rule.yaml"
kubectl --context qa-insurance-hub -n qa-monitoring apply --dry-run=server \
  -f "$MONITORING_EVIDENCE_DIR/candidate-dashboard.yaml"
```

Confirm that the render has:

- names `qa-pricing-service` and `qa-pricing-dashboard` in namespace `qa-monitoring`;
- service selector `pricing-service`;
- 30-second evaluation and refresh intervals;
- warning severity and an immutable dashboard;
- workload selectors limited to `qa-pricing-api-*` Pods with container `pricing-api`.

Rendering proves source shape only. It does not prove admission, application or ingestion.

## Provision, apply and read back

The apply targets repeat source validation, run a Kubernetes server-side dry-run, apply only the
selected object and read back its existence. Run them only with authorization for these exact QA
monitoring resources:

```sh
make -C k8s prometheus-rules-apply ENV_NAME=qa SVC_NAME=pricing
if test -f "$MONITORING_EVIDENCE_DIR/prior-rule.absent"; then
  kubectl --context qa-insurance-hub -n qa-monitoring get \
    prometheusrule/qa-pricing-service -o jsonpath='{.metadata.uid}' \
    > "$MONITORING_EVIDENCE_DIR/installed-rule-uid.txt"
  test -s "$MONITORING_EVIDENCE_DIR/installed-rule-uid.txt"
fi

make -C k8s grafana-dashboard-apply ENV_NAME=qa SVC_NAME=pricing
if test -f "$MONITORING_EVIDENCE_DIR/prior-dashboard.absent"; then
  kubectl --context qa-insurance-hub -n qa-monitoring get \
    configmap/qa-pricing-dashboard -o jsonpath='{.metadata.uid}' \
    > "$MONITORING_EVIDENCE_DIR/installed-dashboard-uid.txt"
  test -s "$MONITORING_EVIDENCE_DIR/installed-dashboard-uid.txt"
fi
```

Read back both objects, then re-render the same source and require it to match the retained
candidate byte for byte:

```sh
kubectl --context qa-insurance-hub -n qa-monitoring get \
  prometheusrule/qa-pricing-service -o yaml \
  > "$MONITORING_EVIDENCE_DIR/applied-rule.yaml"
kubectl --context qa-insurance-hub -n qa-monitoring get \
  configmap/qa-pricing-dashboard -o yaml \
  > "$MONITORING_EVIDENCE_DIR/applied-dashboard.yaml"
make --no-print-directory -C k8s \
  prometheus-rules-render ENV_NAME=qa SVC_NAME=pricing \
  > "$MONITORING_EVIDENCE_DIR/rerendered-rule.yaml"
make --no-print-directory -C k8s \
  grafana-dashboard-render ENV_NAME=qa SVC_NAME=pricing \
  > "$MONITORING_EVIDENCE_DIR/rerendered-dashboard.yaml"
cmp "$MONITORING_EVIDENCE_DIR/candidate-rule.yaml" \
  "$MONITORING_EVIDENCE_DIR/rerendered-rule.yaml"
cmp "$MONITORING_EVIDENCE_DIR/candidate-dashboard.yaml" \
  "$MONITORING_EVIDENCE_DIR/rerendered-dashboard.yaml"
```

Then verify the live service and signals:

```sh
make verify-service-observability \
  ENV_NAME=qa \
  SVC_NAME=pricing \
  VERIFY_MODE=enabled \
  VERIFY_SOURCE_REVISION="$PRICING_RELEASE_REVISION"
```

The verifier is read-only, but it creates bounded local port-forwards to the Pricing Deployment's
loopback ports and to the monitoring services. It requires and checks:

- that the Flux release source and `qa-pricing-api` both report `PRICING_RELEASE_REVISION` as
  applied;
- a fully ready Deployment with an immutable Pod image ID;
- `OTEL_ENABLED=true` with the internal Alloy endpoint;
- `/livez` and `/readyz`;
- the TRI quote `{"totalPrice":78,"coversPrices":{"C1":26,"C2":52}}`;
- the verified Pod's HTTP and gRPC request series and its `query` database series;
- two correlated completion logs;
- a Tempo trace containing `SELECT tariff`;
- zero firing Pricing alerts.

It then removes its port-forwards. Record `MONITORING_SOURCE_REVISION` separately: it identifies
the applied assets, not the running release.

Read back the effective Grafana dashboard by UID. Confirm that it is provisioned and
`editable: false`, that the provider disables UI updates and deletion, and that every variable and
query stays scoped to Pricing, QA and Go. Read back the active Prometheus group
`insurance-hub-pricing-service`: its ten alert names, expressions and pending windows. Keep render,
admission, apply, API read-back and live-signal evidence distinct. For the effective UI check, use
the owned foreground Grafana path:

```sh
make -C k8s grafana-ui
```

## Investigation workflow

Use a bounded absolute UTC range, normally 15 minutes, and work through the signals in this order:

```text
alert or unusual metric
  -> narrow the dashboard time range
  -> Request rate, Service error rate and Request latency percentiles
  -> Database outcomes, Database and pool latency, Database pool pressure
  -> Go runtime, Pod CPU and memory, Readiness and restarts, Telemetry delivery health
  -> matching completion logs -> copy trace_id -> Tempo trace
```

A successful quote trace contains:

- the HTTP server span `POST /pricing/calculate`;
- the gRPC client and server spans;
- `pricing.db.acquire`;
- the `SELECT tariff` client span.

A failed read marks its stage span with `error.type`. Check the service, environment,
implementation, version and instance identity before comparing span durations.

Canonical selectors:

```promql
sum by (insurance_hub_pricing_transport, insurance_hub_pricing_outcome) (
  rate(insurance_hub_pricing_requests_total{
    service_name="pricing-service",deployment_environment_name="qa",
    service_implementation="go"
  }[5m])
)
```

```promql
sum by (insurance_hub_pricing_database_stage, insurance_hub_pricing_database_outcome) (
  rate(insurance_hub_pricing_database_operations_total{
    service_name="pricing-service",deployment_environment_name="qa",
    service_implementation="go"
  }[5m])
)
```

```promql
sum(db_client_connection_count{service_name="pricing-service",db_client_connection_state="used"})
  / clamp_min(sum(db_client_connection_max{service_name="pricing-service"}), 1)
```

```logql
{service_name="pricing-service",deployment_environment_name="qa",
 service_implementation="go"} | json |
 outcome=~"internal_error|deadline_exceeded|cancelled"
```

```traceql
{ resource.service.name = "pricing-service" &&
  resource.insurance_hub.service.implementation = "go" &&
  resource.deployment.environment.name = "qa" }
```

For a slow-request search, append `| duration_ms >= 500` after `| json`. Copy `trace_id` from the
parsed fields; trace IDs are never metric labels.

### Missing telemetry or service failure

Start with Dashboard's common checks. Then decide which case applies:

- **Telemetry loss.** The Deployment is available, `/livez` and `/readyz` pass, and a forwarded
  private quote succeeds, but Go runtime samples are absent for two minutes
  (`PricingTelemetryMissing`). Check, in order:
  1. the effective `OTEL_ENABLED`, `OTEL_EXPORTER_OTLP_ENDPOINT` and `SERVICE_VERSION` in
     `qa-pricing-config`;
  2. the workload's `app.kubernetes.io/version` label and the current Pod UID, because Alloy drops
     signals whose identity does not match;
  3. the NetworkPolicy egress to the selected Alloy Pods;
  4. the `pricing` JSON-log registration in the QA Alloy configuration;
  5. the **Telemetry delivery health** panel.
- **Shared collection failure.** Rising exporter failures, drops, pending age or queue use. This
  does not by itself prove that Pricing lost data.
- **Service failure.** Readiness fails, Pods restart, the Deployment is unavailable, or the
  private quote fails. Use the [deployment runbook](deployment-runbook.md#stop-conditions-and-recovery).
- **Idle.** Zero request rate with current Go runtime samples is healthy idle time, not loss.

Never stop shared Alloy, Prometheus, Loki, Tempo or PostgreSQL to create an incident. Exercise
outages only in the disposable harness.

### Reader symptoms

Start with Product's PostgreSQL checks, then narrow the cause with **Database outcomes**:

- **`storage_unavailable` (HTTP 503).** Check connectivity first:
  - `/readyz`, because readiness includes the read-access check;
  - the NetworkPolicy path to the selected PostgreSQL cluster on port 5432;
  - the reader Secret's `database-url` and the CA at `/etc/pricing-service/postgres/ca.crt`.

  For TLS or permission causes, run the read-only
  `make -C services/pricing-service resource-inspect` with the explicit target variables from the
  [deployment runbook](deployment-runbook.md#postgresql-resource-administration). Run
  `resource-verify` only with authorization: it checks role identity, the CA, the reader's tariff
  access and denial of privileged SQL through a temporary port-forward, and rolls back its probe
  transactions. Startup also refuses any other role or an unverified TLS connection.
- **`deadline_exceeded` (HTTP 504) with `PricingDatabasePoolTimeouts`.** The database or network
  path is hung, or the pool is exhausted. Compare **Database pool pressure** (used against the
  maximum of 8) and pool wait p95 with query p95, then check PostgreSQL health and capacity. Do not
  raise `DB_MAX_CONNS` or the timeouts without review.
- **`invalid_data` (HTTP 500).** The stored tariff rows are unusable or ambiguous. Inspect the
  release's seed and migration evidence through the deployment runbook's administration
  procedure. Do not edit tariff data from this runbook.

**Readiness and liveness.** Readiness requires the internal gateway connection and a read-access
check within `PROBE_TIMEOUT`, so PostgreSQL loss removes the Pod from readiness. Liveness covers
only the process: a failing `/readyz` with a passing `/livez` points to a dependency, and
unexpected restarts point to liveness, memory or rollout causes. Collector failure never affects
either probe.

### Alert responses

| Alert | First checks |
| --- | --- |
| `PricingServiceErrorRate` | Split by transport and outcome, then database outcome and stage; correlate a failed trace; then follow the reader symptoms above. |
| `PricingHighLatency` | Compare request p95 with query p95 and pool wait p95; inspect one slow trace's acquire and query spans. |
| `PricingDatabasePoolPressure`, `PricingDatabasePoolTimeouts` | Pool utilization, wait, timeouts, query latency and PostgreSQL capacity; no pool change without review. |
| `PricingResourcePressure` | Load, Go runtime memory and GC, CPU throttling, Pod events and the last rollout against the 200m/128Mi limits. |
| `PricingUnavailable` | Release owner readiness, rollout, Pod events, image pull, configuration, reader Secret and database readiness. |
| `PricingUnexpectedRestart` | Prior container state, safe logs, events, memory pressure and rollout identity. |
| `PricingTelemetryMissing` | The telemetry-loss checks above. |
| `PricingTelemetryDeliveryFailure`, `PricingTelemetryBacklog` | Isolate Loki, remote write or OTLP export; check backend reachability and Alloy queues while quotes keep serving. |

Do not change thresholds or windows to hide missing or firing data.

## Recovery, rollback and cleanup

If a source, release, runtime or network mismatch appears, stop and use the
[deployment runbook's recovery procedure](deployment-runbook.md#stop-conditions-and-recovery)
before classifying it as an application defect. An application rollback does not restore
monitoring assets, and a monitoring restore does not roll back the application.

To restore prior monitoring assets, first verify that the captured UIDs still identify both
objects:

```sh
test "$(kubectl --context qa-insurance-hub -n qa-monitoring get \
  prometheusrule/qa-pricing-service -o jsonpath='{.metadata.uid}')" \
  = "$(jq -r '.metadata.uid' "$MONITORING_EVIDENCE_DIR/prior-rule.json")"
test "$(kubectl --context qa-insurance-hub -n qa-monitoring get \
  configmap/qa-pricing-dashboard -o jsonpath='{.metadata.uid}')" \
  = "$(jq -r '.metadata.uid' "$MONITORING_EVIDENCE_DIR/prior-dashboard.json")"
```

Then:

1. Check out the captured accepted revision in an isolated checkout and rerun its asset
   validation.
2. Apply only those two objects through the same guarded Make targets.
3. Read them back and compare their stable source payloads with the preflight captures:

```sh
kubectl --context qa-insurance-hub -n qa-monitoring get \
  prometheusrule/qa-pricing-service -o json \
  | jq -S '{apiVersion, kind, metadata: {name: .metadata.name, namespace: .metadata.namespace, uid: .metadata.uid}, spec}' \
  > "$MONITORING_EVIDENCE_DIR/restored-rule-source.json"
kubectl --context qa-insurance-hub -n qa-monitoring get \
  configmap/qa-pricing-dashboard -o json \
  | jq -S '{apiVersion, kind, metadata: {name: .metadata.name, namespace: .metadata.namespace, uid: .metadata.uid}, data}' \
  > "$MONITORING_EVIDENCE_DIR/restored-dashboard-source.json"
cmp "$MONITORING_EVIDENCE_DIR/prior-rule-source.json" \
  "$MONITORING_EVIDENCE_DIR/restored-rule-source.json"
cmp "$MONITORING_EVIDENCE_DIR/prior-dashboard-source.json" \
  "$MONITORING_EVIDENCE_DIR/restored-dashboard-source.json"
```

Follow a live restore with the reviewed repository revert, so that the source keeps owning the
assets.

To retire a proven first installation, use the same retained evidence directory and first require
that both resources were absent before installation and that their current UIDs identify the
objects created by that installation. Delete only these two objects, with separate authorization:

```sh
set -eu
test -f "$MONITORING_EVIDENCE_DIR/prior-rule.absent"
test -f "$MONITORING_EVIDENCE_DIR/prior-dashboard.absent"
test -s "$MONITORING_EVIDENCE_DIR/installed-rule-uid.txt"
test -s "$MONITORING_EVIDENCE_DIR/installed-dashboard-uid.txt"
test "$(kubectl --context qa-insurance-hub -n qa-monitoring get \
  prometheusrule/qa-pricing-service -o jsonpath='{.metadata.uid}')" \
  = "$(cat "$MONITORING_EVIDENCE_DIR/installed-rule-uid.txt")"
test "$(kubectl --context qa-insurance-hub -n qa-monitoring get \
  configmap/qa-pricing-dashboard -o jsonpath='{.metadata.uid}')" \
  = "$(cat "$MONITORING_EVIDENCE_DIR/installed-dashboard-uid.txt")"
kubectl --context qa-insurance-hub -n qa-monitoring delete \
  prometheusrule/qa-pricing-service
kubectl --context qa-insurance-hub -n qa-monitoring delete \
  configmap/qa-pricing-dashboard
```

After any restore or retirement, verify:

- **Assets.** The objects are exactly absent or restored, and other services' assets and the
  shared releases are unchanged.
- **Probes and quote.** `/livez`, `/readyz` and the private TRI quote, with the
  [deployment runbook's](deployment-runbook.md#private-smoke) checks.
- **Signals.** Rerun `verify-service-observability` in the applicable mode.
- **Routing.** `qa-pricing-api` still reports the reviewed release revision, and the Deployment's
  ready Pod image ID matches the release digest.

Do not delete the Grafana or Prometheus releases, PVCs, the Pricing workload, PostgreSQL resources
or another service's resources from this runbook. Stop a foreground UI or port-forward with Ctrl+C
and verify that the owning command removed its local process. After an interruption, inspect only
the exact recorded name; never clean up with broad process or Kubernetes selectors.

## Evidence checklist

Retain the following without credentials, payloads or business values:

- the monitoring revision and the Pricing release revision, recorded separately;
- the release image reference, registry digest, Pod image ID, OCI revision and runtime version;
- Flux release source and applied state, and resource UIDs;
- rendered object hashes, plus server dry-run, apply and read-back results;
- dashboard and rule inventories and Grafana immutability;
- verifier output, bounded query results, and alert firing and recovery times;
- investigation links, the recovery outcome and owned cleanup.

Nothing in this runbook alone authorizes a shared-target mutation, a release, routing changes or
data changes.
