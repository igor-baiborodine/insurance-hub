<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Dashboard observability runbook](#dashboard-observability-runbook)
  - [Signal and decision boundary](#signal-and-decision-boundary)
  - [Source and disposable validation](#source-and-disposable-validation)
  - [QA preflight and stop conditions](#qa-preflight-and-stop-conditions)
  - [Provision, apply and read back](#provision-apply-and-read-back)
  - [Investigation workflow](#investigation-workflow)
  - [Recovery, rollback and cleanup](#recovery-rollback-and-cleanup)
  - [Evidence and later-ticket boundaries](#evidence-and-later-ticket-boundaries)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Dashboard observability runbook

This runbook owns source validation, disposable delivery checks, guarded QA provisioning,
read-back, investigation and restoration for the Go Dashboard monitoring assets. Use service key
`dashboard`, environment `qa`, Kubernetes context `qa-insurance-hub` and namespace
`qa-monitoring`. The application Deployment is `qa-dashboard-api`; the monitoring resources are
PrometheusRule `qa-dashboard-service`, ConfigMap `qa-dashboard-dashboard` and Grafana dashboard UID
`insurance-hub-dashboard-service`.

The dashboard and rules are repository-owned monitoring resources applied through the Make
targets below. They are separate from the Flux-owned Dashboard workload. Do not edit the Grafana
dashboard in the UI, patch the generated resources, suspend Flux or mutate Product/Policy Search
assets while following this procedure.

## Signal and decision boundary

Dashboard exports fixed request, Elasticsearch and registration outcome/duration signals; Go
runtime signals; one `registered` worker enabled/in-flight-age pair; and registration offset-commit
failures. The dashboard adds Pod availability/resource/restart views and shared Alloy delivery
health. Labels are bounded by operation, transport, outcome, dependency and the fixed event type.
They never contain dates, periods, agent logins, product codes, amounts, policy/customer values,
event bodies, Kafka topics/groups, credentials, arbitrary errors or trace IDs.

The pilot thresholds and windows are recorded in
`k8s/observability/services/dashboard/acceptance.json`. They are operating limits, not production
SLOs. Worker enabled with zero in-flight age is healthy idle. A refreshed Elasticsearch read and
broker offsets are separate bounded facts; neither proves continuous freshness, backlog clearance
or projection completeness. There is no Dashboard consumer-lag, processing-pending or continuous
projection-freshness series or alert.

## Source and disposable validation

From the repository root, validate the exact candidate before any QA operation:

```sh
make monitoring-assets-check ENV_NAME=qa SVC_NAME=dashboard
make test-monitoring ENV_NAME=qa SVC_NAME=dashboard
make test-observability-backends OBSERVABILITY_SERVICE_DOMAIN=dashboard
```

The first command validates typed inventory, the pinned chart render, Prometheus rule syntax and
the full rule fixtures. The second queries all 31 Prometheus dashboard expressions against
collected disposable data and requires healthy alerts to remain inactive. The third uses the
Dashboard probe with disposable Alloy, Prometheus, Loki and Tempo resources; it proves actual
signal delivery and owned cleanup. These checks do not apply QA resources or establish live QA
behavior.

Also qualify the application candidate through the owning commands in
[deployment-runbook.md](deployment-runbook.md). A monitoring-only revision reuses a separately
verified compatible runtime image and records the application source/image identity separately
from the monitoring source revision.

## QA preflight and stop conditions

Keep the commands in this section in one shell so the evidence directory and source revision remain
available to later steps. Start from a clean checkout of the exact reviewed revision, then capture
the current Flux, workload and monitoring-object state before any write:

```sh
export DASHBOARD_SOURCE_REVISION="$(git rev-parse HEAD)"
export MONITORING_EVIDENCE_DIR="$(mktemp -d)"
test -z "$(git status --porcelain)"
test "$(kubectl config current-context)" = qa-insurance-hub

kubectl --context qa-insurance-hub -n flux-system get \
  gitrepository/insurance-hub -o json \
  > "$MONITORING_EVIDENCE_DIR/flux-source.json"
kubectl --context qa-insurance-hub -n flux-system get \
  kustomization/qa-svc -o json \
  > "$MONITORING_EVIDENCE_DIR/flux-qa-svc.json"
kubectl --context qa-insurance-hub -n qa-svc get \
  deployment/qa-dashboard-api -o json \
  > "$MONITORING_EVIDENCE_DIR/dashboard-deployment.json"
kubectl --context qa-insurance-hub -n qa-monitoring get \
  prometheusrule/qa-dashboard-service -o json --ignore-not-found \
  > "$MONITORING_EVIDENCE_DIR/prior-rule.json"
kubectl --context qa-insurance-hub -n qa-monitoring get \
  configmap/qa-dashboard-dashboard -o json --ignore-not-found \
  > "$MONITORING_EVIDENCE_DIR/prior-dashboard.json"

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

The final guard stops when only one monitoring object exists. Before a live operation, record all
of the following:

- the full reviewed repository commit containing the monitoring assets;
- the current Flux `insurance-hub` source and `qa-svc` applied revisions;
- the running Dashboard image reference, registry digest, Pod image ID, OCI source revision and
  runtime `service.version`;
- current UIDs and serialized source hashes for `qa-dashboard-service` and
  `qa-dashboard-dashboard`, or verified absence for first installation;
- the Grafana dashboard UID and current read-only provider behavior;
- the selected context `qa-insurance-hub`, namespace `qa-monitoring`, service `dashboard` and
  environment `qa`.

Stop before mutation when the context, namespace, source revision, service selection, resource
name/UID, Dashboard image identity or current asset source differs from the reviewed record. Also
stop for missing Dashboard signals, unexplained firing alerts, a mutable Grafana provider, an
unbounded/sensitive query or any operation that would alter shared monitoring releases, PVCs,
Alloy, Prometheus, Loki, Tempo, Elasticsearch, Kafka, Java resources or another service's assets.

For a first installation, when both `.absent` markers exist, run the selected-service read-only
pre-provision verifier before creating either object:

```sh
make verify-service-observability \
  ENV_NAME=qa \
  SVC_NAME=dashboard \
  VERIFY_MODE=pre-provision \
  VERIFY_SOURCE_REVISION="$DASHBOARD_SOURCE_REVISION"
```

Render the exact objects into the evidence directory, retain their hashes, and ask the API server
to admit them without persisting either object:

```sh
make --no-print-directory -C k8s \
  prometheus-rules-render ENV_NAME=qa SVC_NAME=dashboard \
  > "$MONITORING_EVIDENCE_DIR/candidate-rule.yaml"
make --no-print-directory -C k8s \
  grafana-dashboard-render ENV_NAME=qa SVC_NAME=dashboard \
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

Confirm the render names `qa-dashboard-service` and `qa-dashboard-dashboard`, namespace
`qa-monitoring`, service selector `dashboard-service`, 30-second evaluation/refresh intervals,
warning severity, immutable dashboard setting and the exclusion of `qa-dashboard-api-legacy-*`.
Rendering proves source shape only; it does not prove server admission, application or ingestion.

## Provision, apply and read back

The apply targets repeat source validation, perform Kubernetes server-side dry-run, apply only the
selected object and read back its existence. Run them only with authorization for these exact QA
monitoring resources:

```sh
make -C k8s prometheus-rules-apply ENV_NAME=qa SVC_NAME=dashboard
make -C k8s grafana-dashboard-apply ENV_NAME=qa SVC_NAME=dashboard
```

Read back each applied object before verifying live signals. Re-render the same source and require
it to match the retained candidate byte for byte:

```sh
kubectl --context qa-insurance-hub -n qa-monitoring get \
  prometheusrule/qa-dashboard-service -o yaml \
  > "$MONITORING_EVIDENCE_DIR/applied-rule.yaml"
kubectl --context qa-insurance-hub -n qa-monitoring get \
  configmap/qa-dashboard-dashboard -o yaml \
  > "$MONITORING_EVIDENCE_DIR/applied-dashboard.yaml"
make --no-print-directory -C k8s \
  prometheus-rules-render ENV_NAME=qa SVC_NAME=dashboard \
  > "$MONITORING_EVIDENCE_DIR/rerendered-rule.yaml"
make --no-print-directory -C k8s \
  grafana-dashboard-render ENV_NAME=qa SVC_NAME=dashboard \
  > "$MONITORING_EVIDENCE_DIR/rerendered-dashboard.yaml"
cmp "$MONITORING_EVIDENCE_DIR/candidate-rule.yaml" \
  "$MONITORING_EVIDENCE_DIR/rerendered-rule.yaml"
cmp "$MONITORING_EVIDENCE_DIR/candidate-dashboard.yaml" \
  "$MONITORING_EVIDENCE_DIR/rerendered-dashboard.yaml"
sha256sum \
  "$MONITORING_EVIDENCE_DIR/rerendered-rule.yaml" \
  "$MONITORING_EVIDENCE_DIR/rerendered-dashboard.yaml" \
  > "$MONITORING_EVIDENCE_DIR/rerendered.sha256"
```

After apply, use the full reconciled source revision rather than a branch name or short SHA:

```sh
: "${MONITORING_SOURCE_REVISION:?set the full reconciled monitoring source revision}"
make verify-service-observability \
  ENV_NAME=qa \
  SVC_NAME=dashboard \
  VERIFY_MODE=enabled \
  VERIFY_SOURCE_REVISION="$MONITORING_SOURCE_REVISION"
```

Use `VERIFY_MODE=disabled` when the Go workload is intentionally disabled and
`VERIFY_MODE=pre-provision` before the monitoring objects are expected to exist. The verifier is
read-only but creates bounded local port-forwards; it requires the Flux source identity, exact
Dashboard resources, application probes and Prometheus/Loki/Tempo results appropriate to the
selected mode, then cleans up its owned processes.

Read back the effective Grafana dashboard by UID and confirm it is provisioned, `editable: false`,
the provider disables UI updates/deletion and all variables/queries remain scoped to Dashboard,
QA and Go. Read back the active Prometheus group `insurance-hub-dashboard-service`, its 13 alert
names, expressions and pending windows. Keep render, admission, apply, API read-back and live
signal evidence distinct. Use the owned foreground Grafana path for the effective UI/API check:

```sh
make -C k8s grafana-ui
```

## Investigation workflow

Use a bounded absolute UTC range, normally 15 minutes. Start with the dashboard's request rate,
error ratio and p95 panels, then split Elasticsearch outcomes and duration by the fixed operation.
For worker symptoms, compare completed registration outcomes, commit failures, enabled state and
in-flight age. Copy a safe `trace_id` from a completion log into Tempo; do not add trace IDs or
business values as metric labels.

Representative Prometheus queries are:

```promql
sum by (insurance_hub_dashboard_operation,insurance_hub_dashboard_outcome) (
  rate(insurance_hub_dashboard_requests_total{
    service_name="dashboard-service",deployment_environment_name="qa",
    service_implementation="go"
  }[5m])
)
```

```promql
histogram_quantile(0.95,
  sum by (le,insurance_hub_dashboard_operation) (
    rate(insurance_hub_dashboard_dependency_duration_seconds_bucket{
      service_name="dashboard-service",insurance_hub_dashboard_dependency="elasticsearch"
    }[5m])
  )
)
```

```promql
insurance_hub_dashboard_event_worker_enabled{
  service_name="dashboard-service",deployment_environment_name="qa"
}
```

```promql
insurance_hub_dashboard_event_worker_in_flight_age_seconds{
  service_name="dashboard-service",deployment_environment_name="qa"
}
```

```promql
sum(increase(insurance_hub_dashboard_event_commit_failures_total{
  service_name="dashboard-service",deployment_environment_name="qa"
}[5m]))
```

```logql
{service_name="dashboard-service",deployment_environment_name="qa",
 service_implementation="go"} | json |
 outcome=~"internal_error|deadline_exceeded|unavailable|malformed|error|cancelled"
```

```traceql
{ resource.service.name = "dashboard-service" &&
  resource.insurance_hub.service.implementation = "go" &&
  resource.deployment.environment.name = "qa" }
```

For request errors or latency, verify the minimum 20-request gate, then correlate transport status
and Elasticsearch duration. Elasticsearch alerts require at least 20 operations. Event-failure
alerts require ten completions and an enabled worker; stall requires enabled state and in-flight
age above 30 seconds for two minutes. Commit failure fires on any five-minute increase. Resource,
availability, telemetry absence/delivery/backlog and restart responses follow their accepted
windows in `acceptance.json`.

Missing request/event samples can be healthy idle. An available Deployment without bounded Go
runtime samples is telemetry loss. Rising shared exporter failures, drops, pending age or queue use
is shared collection failure and does not by itself prove Dashboard loss. Never stop shared Alloy,
Prometheus, Loki, Tempo, Elasticsearch or Kafka to generate an incident; exercise outages only in
the disposable harness.

## Recovery, rollback and cleanup

If a source, runtime or network mismatch appears, stop the acceptance attempt and use the
Dashboard [deployment recovery procedure](deployment-runbook.md#diagnostics-stop-conditions-and-recovery)
before classifying an application defect. Do not change thresholds to hide missing or firing data.

To restore prior monitoring assets, first verify that the captured UIDs still identify
`qa-dashboard-service` and `qa-dashboard-dashboard`:

```sh
test "$(kubectl --context qa-insurance-hub -n qa-monitoring get \
  prometheusrule/qa-dashboard-service -o jsonpath='{.metadata.uid}')" \
  = "$(jq -r '.metadata.uid' "$MONITORING_EVIDENCE_DIR/prior-rule.json")"
test "$(kubectl --context qa-insurance-hub -n qa-monitoring get \
  configmap/qa-dashboard-dashboard -o jsonpath='{.metadata.uid}')" \
  = "$(jq -r '.metadata.uid' "$MONITORING_EVIDENCE_DIR/prior-dashboard.json")"
```

Check out the captured accepted repository revision in an isolated checkout, rerun its asset
validation, then apply only those two exact objects through the same guarded Make targets. Read the
restored objects back and compare their stable source payloads with the preflight captures:

```sh
kubectl --context qa-insurance-hub -n qa-monitoring get \
  prometheusrule/qa-dashboard-service -o json \
  > "$MONITORING_EVIDENCE_DIR/restored-rule.json"
jq -S '{apiVersion, kind, metadata: {name: .metadata.name, namespace: .metadata.namespace, uid: .metadata.uid}, spec}' \
  "$MONITORING_EVIDENCE_DIR/restored-rule.json" \
  > "$MONITORING_EVIDENCE_DIR/restored-rule-source.json"
kubectl --context qa-insurance-hub -n qa-monitoring get \
  configmap/qa-dashboard-dashboard -o json \
  > "$MONITORING_EVIDENCE_DIR/restored-dashboard.json"
jq -S '{apiVersion, kind, metadata: {name: .metadata.name, namespace: .metadata.namespace, uid: .metadata.uid}, data}' \
  "$MONITORING_EVIDENCE_DIR/restored-dashboard.json" \
  > "$MONITORING_EVIDENCE_DIR/restored-dashboard-source.json"
cmp "$MONITORING_EVIDENCE_DIR/prior-rule-source.json" \
  "$MONITORING_EVIDENCE_DIR/restored-rule-source.json"
cmp "$MONITORING_EVIDENCE_DIR/prior-dashboard-source.json" \
  "$MONITORING_EVIDENCE_DIR/restored-dashboard-source.json"
sha256sum \
  "$MONITORING_EVIDENCE_DIR/restored-rule-source.json" \
  "$MONITORING_EVIDENCE_DIR/restored-dashboard-source.json"
```

Confirm the restored rule group, dashboard UID and provider policy, and confirm Product/Policy
Search resources and shared releases are unchanged. Follow the live restore with the reviewed
repository revert required for durable source ownership. For a first installation, use the
separately authorized exact-object deletion below instead of this prior-state procedure.

For a proven first installation that must be retired, separately authorize deletion of only:

```sh
kubectl --context qa-insurance-hub -n qa-monitoring delete \
  prometheusrule/qa-dashboard-service
kubectl --context qa-insurance-hub -n qa-monitoring delete \
  configmap/qa-dashboard-dashboard
```

Verify exact absence and the continued health of shared monitoring and other service assets. Do
not delete the Grafana/Prometheus releases, PVCs, Dashboard workload, Java resources, data services
or any issue-290 index/topic/identity from this runbook. Stop a foreground UI or port-forward with
Ctrl+C and verify the owning command removed its local process. Inspect an exact recorded name
after interruption; do not use broad process or Kubernetes selectors for cleanup.

## Evidence and later-ticket boundaries

Retain source/base/head identities, rendered object hashes, server dry-run/apply/read-back results,
resource UIDs, full image/source/digest chain, Flux source/applied revisions, dashboard/rule
inventories, Grafana immutability, bounded query results, alert firing/suppression/recovery times,
diagnostic links, recovery outcome and owned cleanup. Redact credentials, business data and raw
event bodies.

Issue 291 owns these monitoring assets and operating procedures. Issue 292 owns full-service
practical load, application downgrade/removal/reinstallation/restoration and isolated authority
handover. Later tickets own route/traffic transfer and Java retirement. This runbook does not
authorize those actions.
