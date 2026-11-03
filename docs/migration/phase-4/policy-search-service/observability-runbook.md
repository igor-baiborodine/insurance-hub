<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Policy Search observability runbook](#policy-search-observability-runbook)
  - [Signal paths and ownership](#signal-paths-and-ownership)
  - [Configuration and identity](#configuration-and-identity)
  - [Source and disposable validation](#source-and-disposable-validation)
  - [QA preflight and stop conditions](#qa-preflight-and-stop-conditions)
  - [Staged render, dry-run, apply and read-back](#staged-render-dry-run-apply-and-read-back)
  - [Grafana workflow](#grafana-workflow)
  - [Investigation queries](#investigation-queries)
  - [Idle, missing data, loss and alert response](#idle-missing-data-loss-and-alert-response)
  - [Rollback and cleanup](#rollback-and-cleanup)
  - [Evidence and issue #235 handoff](#evidence-and-issue-235-handoff)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Policy Search observability runbook

This runbook owns Policy Search monitoring validation, QA provisioning, investigation and
rollback. The [deployment runbook](deployment-runbook.md) remains authoritative for the service
image, Elasticsearch fixture, Kafka event fixture, worker activation and application rollback.
Run commands from the repository root unless a command uses `make -C k8s`.

## Signal paths and ownership

The Go workload exports OTLP/gRPC to `qa-alloy.qa-monitoring.svc:4317`. Alloy associates only the
selected `qa-svc` Pod identity, rejects an inconsistent service identity, keeps approved bounded
fields and routes metrics to Prometheus, logs to Loki and traces to Tempo plus the transitional
Zipkin bridge. Policy Search owns the meaning and dimensions of its signals; Alloy owns shared
identity enforcement, filtering, queues and delivery. Prometheus owns recording and alert state,
Grafana owns the provisioned read-only view, and Loki and Tempo own retained logs and traces.

Policy Search emits request count and duration; Elasticsearch search/replace count, duration and
client spans; registered/terminated event count and duration; worker enabled and in-flight-age
gauges; Go runtime metrics; safe completion logs; and transport/dependency/event traces. The
Elasticsearch service remains the query and projection dependency. Kafka remains the event source
when workers are enabled, but this monitoring contract does not expose topic, group or record
identity.

Durable source is under `k8s/observability/services/policy-search/`, the QA registration is in
`k8s/observability/environments/qa.json`, and shared collection policy is in
`k8s/overlays/qa/infra/alloy/config.alloy`. The rendered QA objects are
`PrometheusRule/qa-policy-search-service` and immutable
`ConfigMap/qa-policy-search-dashboard` in `qa-monitoring`. Never edit those live objects or the
Grafana database by hand.

Workers disabled means both enabled gauges are zero, the service owns no Kafka clients and event
alerts stay suppressed. Workers enabled and idle means enabled is one, in-flight age is zero and
event rate may be zero. Q03 deliberately uses the fixed `registered` and `terminated` enabled and
in-flight-age gauges: an enabled worker with age above 30 seconds for two minutes indicates a
stalled in-flight record. Completion resets age to zero. No reliable bounded consumer-group lag
series exists, so this runbook makes no Kafka lag claim and there is no Kafka lag alert.

## Configuration and identity

The accepted QA identity is:

| Field | Required value |
| --- | --- |
| `service.namespace` | `insurance-hub` |
| `service.name` | `policy-search-service` |
| `deployment.environment.name` | `qa` |
| `insurance_hub.service.implementation` | `go` |
| `service.version` | immutable `git-<12-hex>` source version |
| `service.instance.id` | current Pod UID |
| `k8s.cluster.name` | `qa-insurance-hub` |
| `k8s.namespace.name` | `qa-svc` |
| `k8s.container.name` | `qa-policy-search-api` |

The workload must have `OTEL_ENABLED=true`, its explicit service/environment/version/instance
identity and the internal OTLP endpoint. The read-only verifier checks the effective ConfigMap,
Deployment, Pod UID/image ID and Flux revision before querying signals. Do not put query text,
policy/customer data, event bodies, topic/group names, credentials, arbitrary error text, trace IDs
or Pod names into metric labels or Loki index labels. Grafana variables are limited to
`deployment_environment_name` and `service_implementation`; select `qa` and `go` during QA work.

## Source and disposable validation

Prerequisites are the repository-pinned Python environment, Go 1.27.3, Docker, Helm, kubectl and
the pinned backend images. Validate all registered assets and shared collection policy:

```sh
make monitoring-assets-check-all
make monitoring-assets-check ENV_NAME=qa SVC_NAME=policy-search
make monitoring-assets-check ENV_NAME=qa SVC_NAME=product
make test-monitoring ENV_NAME=qa SVC_NAME=policy-search
make test-monitoring ENV_NAME=qa SVC_NAME=product
make check-observability
```

Run the real Policy Search probe through disposable Alloy, Prometheus, Loki, Tempo and Zipkin:

```sh
make -C k8s test-observability-backends \
  OBSERVABILITY_SERVICE_DOMAIN=policy-search
```

Run the Product proof separately to preserve the shared-pipeline compatibility baseline:

```sh
make -C k8s test-observability-backends \
  OBSERVABILITY_SERVICE_DOMAIN=product
```

These checks prove different things. Rendering proves deterministic Kubernetes resources. Fake
adapter tests prove context, target and cleanup guards. Collected-data tests execute dashboard
queries and rules against committed fixture scrapes. The disposable backend proof sends the
service-owned query and event paths through real exporters and backends. None of those results
proves the current QA state; the service-selected verifier and read-back below provide that live
evidence.

After a Go or image-affecting source change, also run:

```sh
make -C services/policy-search-service check-go-pr FORMAT_SCOPE=all
make go-svc-image-check SVC_NAME=policy-search
make go-svc-deploy-check SVC_NAME=policy-search
```

## QA preflight and stop conditions

Use a clean checkout of the exact reviewed and merged revision. Record its full SHA and the
previous rule/dashboard state before any write:

```sh
export POLICY_SEARCH_SOURCE_REVISION="$(git rev-parse HEAD)"
test -z "$(git status --porcelain)"
test "$(kubectl config current-context)" = qa-insurance-hub
kubectl config set-context --current --namespace=qa-svc
kubectl --context qa-insurance-hub -n flux-system get gitrepository insurance-hub
kubectl --context qa-insurance-hub -n flux-system get kustomization qa-svc
kubectl --context qa-insurance-hub -n qa-svc get \
  deployment/qa-policy-search-api -o json
kubectl --context qa-insurance-hub -n qa-monitoring get \
  prometheusrule/qa-policy-search-service \
  configmap/qa-policy-search-dashboard -o json --ignore-not-found
```

Require the GitRepository and `qa-svc` Kustomization to be Ready at the same exact merged SHA.
Capture the Policy Search image ID, image digest/tag, Deployment generation, ConfigMap identity,
Pod UID, replica readiness and event-worker mode. Capture the existing Product rule/dashboard
hashes and alert state so continuity can be checked after each stage.

Before provisioning, run the selected-service read-only verifier:

```sh
make verify-service-observability \
  ENV_NAME=qa SVC_NAME=policy-search VERIFY_MODE=pre-provision \
  VERIFY_SOURCE_REVISION="$POLICY_SEARCH_SOURCE_REVISION"
```

This pre-provision mode checks the exact target, Flux revision, OTEL-enabled Deployment, immutable
Pod image identity and safe service workload without requiring monitoring resources that have not
been installed yet. It does not inspect PrometheusRule, dashboard or backend signals.

Stop if the checkout SHA is not the reviewed merge, Flux source and applied revisions differ, the
image/source identity is stale, the Deployment or either dependency is unhealthy, the active
context/namespace differs, existing Product monitoring is unhealthy, resource names collide, the
render differs from the reviewed candidate, or any credential/raw event/policy data appears in
evidence. A verifier failure is a stop, not permission to bypass its guard.

## Staged render, dry-run, apply and read-back

Render the exact objects and retain their SHA-256 hashes with the source revision:

```sh
make -C k8s prometheus-rules-render ENV_NAME=qa SVC_NAME=policy-search \
  > /tmp/issue-267-policy-search-rule.yaml
make -C k8s grafana-dashboard-render ENV_NAME=qa SVC_NAME=policy-search \
  > /tmp/issue-267-policy-search-dashboard.yaml
sha256sum /tmp/issue-267-policy-search-rule.yaml \
  /tmp/issue-267-policy-search-dashboard.yaml
kubectl --context qa-insurance-hub -n qa-monitoring apply --dry-run=server \
  -f /tmp/issue-267-policy-search-rule.yaml
kubectl --context qa-insurance-hub -n qa-monitoring apply --dry-run=server \
  -f /tmp/issue-267-policy-search-dashboard.yaml
```

The guarded apply targets repeat source validation and the server-side dry-run, select only
`qa-monitoring`, apply one object and require a successful read-back. Apply rules first, check them,
then apply the dashboard:

```sh
make -C k8s prometheus-rules-apply ENV_NAME=qa SVC_NAME=policy-search
kubectl --context qa-insurance-hub -n qa-monitoring get \
  prometheusrule/qa-policy-search-service -o yaml

make -C k8s grafana-dashboard-apply ENV_NAME=qa SVC_NAME=policy-search
kubectl --context qa-insurance-hub -n qa-monitoring get \
  configmap/qa-policy-search-dashboard -o yaml

make verify-service-observability \
  ENV_NAME=qa SVC_NAME=policy-search VERIFY_MODE=enabled \
  VERIFY_SOURCE_REVISION="$POLICY_SEARCH_SOURCE_REVISION"
```

Require exactly 12 Policy Search alerts, dashboard UID
`insurance-hub-policy-search-service`, `immutable: true`, the expected sidecar labels and byte-for-
byte dashboard JSON. Re-render and compare hashes after read-back. Confirm Product resources still
exist unchanged, no unrelated rule/dashboard was added or removed, Prometheus accepted the rule,
Grafana provisioned the dashboard, and no selected-service alert is unexpectedly firing.

## Grafana workflow

Confirm the QA context, then start the repository-owned foreground forward:

```sh
make -C k8s grafana-ui
```

Open `http://localhost:3000`, select **Policy Search service**, set environment to `qa` and
implementation to `go`, and begin with a 15-minute range. Expand to one hour only when the event or
rollout window requires it. Keep auto-refresh at 30 seconds; broad multi-day ranges can hide Pod
identity changes and create expensive backend queries.

Start with request rate/error/latency and readiness. Compare Elasticsearch outcome and duration
with request latency. Check event rate/duration plus both fixed worker-state series. Use the failed
request/worker log panel to copy one safe `trace_id`, then open the matching Tempo trace and compare
the gateway, RPC and Elasticsearch spans. Never paste query text, policy identifiers or raw event
bodies into dashboard variables or evidence.

## Investigation queries

Use these bounded selectors, normally over 15 minutes.

```promql
sum by (insurance_hub_policy_search_outcome) (
  rate(insurance_hub_policy_search_requests_total{
    service_name="policy-search-service",
    deployment_environment_name="qa",
    service_implementation="go"
  }[5m])
)
```

```promql
histogram_quantile(0.95,
  sum by (le) (rate(insurance_hub_policy_search_request_duration_seconds_bucket{
    service_name="policy-search-service",deployment_environment_name="qa"
  }[5m])))
```

```promql
sum by (insurance_hub_policy_search_operation,insurance_hub_policy_search_outcome) (
  rate(insurance_hub_policy_search_dependencies_total{
    service_name="policy-search-service",
    insurance_hub_policy_search_dependency="elasticsearch"
  }[5m])
)
```

```promql
insurance_hub_policy_search_event_worker_enabled{
  service_name="policy-search-service",deployment_environment_name="qa"
}
```

```promql
insurance_hub_policy_search_event_worker_in_flight_age_seconds{
  service_name="policy-search-service",deployment_environment_name="qa"
}
```

```promql
sum by (insurance_hub_policy_search_event_type,insurance_hub_policy_search_outcome) (
  rate(insurance_hub_policy_search_events_total{
    service_name="policy-search-service",deployment_environment_name="qa"
  }[5m])
)
```

```promql
go_goroutine_count{service_name="policy-search-service",deployment_environment_name="qa"}
```

```promql
kube_deployment_status_replicas_available{
  namespace="qa-svc",deployment="qa-policy-search-api"
}
```

```promql
increase(kube_pod_container_status_restarts_total{
  namespace="qa-svc",container="qa-policy-search-api"
}[15m])
```

```logql
{service_name="policy-search-service",deployment_environment_name="qa",
 service_implementation="go"} | json | level=~"WARN|ERROR"
```

```traceql
{ resource.service.name = "policy-search-service" &&
  resource.insurance_hub.service.implementation = "go" &&
  resource.deployment.environment.name = "qa" }
```

For delivery, inspect `prometheus_remote_storage_samples_failed_total`,
`prometheus_remote_storage_samples_dropped_total`, pending samples and highest-sent timestamp,
`loki_write_dropped_entries_total`, and OTLP exporter failure/queue metrics. Keep the
`service_name="policy-search-service"` application selectors and exact QA infrastructure targets;
do not run unbounded Loki or Tempo searches.

## Idle, missing data, loss and alert response

Healthy idle has ready replicas, present runtime samples, zero request/event rate, enabled gauges
matching configuration and zero in-flight age. Missing request samples alone can mean idle traffic.
An available replica without `go_goroutine_count` for the alert window is missing telemetry.
Rising shared exporter failures, drops, pending age or queue utilization is collection failure.
Worker enabled with age above 30 seconds is an in-flight stall; disabled workers or enabled workers
with age zero suppress the stall rule.

For request error or latency alerts, verify the minimum 20-request gate, split outcome/transport,
then follow a trace into Elasticsearch. For Elasticsearch failure or latency, check dependency
readiness and the service deployment runbook before changing timeouts. Event failure requires at
least ten completions; inspect safe worker logs, readiness, assignment and Elasticsearch writer
health. For resource pressure, unavailable or restart alerts, correlate Pod events, previous state,
limits and the exact rollout. For telemetry delivery failure/backlog, keep the service available
while isolating Alloy, Prometheus, Loki or Tempo.

Exercise collector/backend loss only with the disposable harness. Expect bounded queues to recover,
new log/trace correlation within 15 seconds, new metrics within 30 seconds and delivery backlog
within 120 seconds. QA testing may stop only the ticket-owned temporary forward or workload; do not
stop shared Alloy, Prometheus, Loki, Tempo, Elasticsearch or Kafka.

Cardinality is bounded by operation, transport, outcome, HTTP/RPC status, dependency and the two
fixed event types. The worker gauges add exactly two series each per active instance. Query text,
policy/customer data, topics/groups, Pod names and trace IDs must never create metric or Loki index
dimensions. Any new label, event type, span name or sampling policy requires a reviewed contract
change.

## Rollback and cleanup

If a prior rule existed, check out its captured accepted source revision and reapply only that rule
through the guarded target. An immutable dashboard cannot be changed in place: verify the live UID
and resource UID still match this operation, delete only that exact ConfigMap, then apply the
captured prior render through the guarded target. If this was the first installation and rollback
is required, delete only resources proven to have been created by this operation:

```sh
kubectl --context qa-insurance-hub -n qa-monitoring delete \
  prometheusrule/qa-policy-search-service
kubectl --context qa-insurance-hub -n qa-monitoring delete \
  configmap/qa-policy-search-dashboard
```

Read back absence or restored hashes, verify Product resources remain unchanged and confirm
Prometheus/Grafana are healthy. Follow a live rollback with the reviewed repository revert needed
to preserve the durable state. Never delete or roll back shared monitoring releases, PVCs,
Elasticsearch, Kafka, Java resources, Product assets or Policy Search deployment resources from
this runbook.

Stop `make -C k8s grafana-ui` with Ctrl+C. The verifier and disposable harness own and verify
cleanup of every forward, process group, container, network and temporary resource they create.
After interruption, inspect for the exact recorded names before retrying; never use a broad process
kill or namespace cleanup.

## Evidence and issue #235 handoff

Keep sanitized evidence under the ignored `ai/artifacts/epic-4.3/issue-267/` directory. Record:

- UTC start/end, operator, exact command, result and whether evidence is source, fake adapter,
  collected fixture, disposable backend or shared QA;
- full merged Git SHA, Flux source/applied revision, Policy Search image tag/digest/ID, Deployment
  generation, ConfigMap identity, Pod UID, ready replicas and effective worker/telemetry mode;
- rendered and live rule/dashboard hashes, resource UID/resourceVersion, dashboard UID and
  immutability, exact 12-alert inventory, Product continuity and rollback coordinates;
- bounded workload, query/time range, series counts, thresholds, alert firing/recovery times,
  request/dependency/event totals, latency/resource observations and delivery loss/recovery;
- correlated safe log/trace evidence, idle/disabled/missing-data interpretation, cleanup result,
  limitations and the acceptance criterion supported by each record.

For issue #235, hand off the exact merged source revision, image identity, QA Deployment and Pod
identity, worker mode, rule/dashboard hashes and UIDs, verifier result, alert inventory/state,
controlled workload and recovery results, Product continuity, rollback point, remaining
limitations, and links to the sanitized Step 11/12 evidence. Do not claim Kafka consumer lag
coverage. Updating or closing #235 is a separate GitHub publication action.

Exclude passwords, tokens, Secret YAML, credential-bearing URLs, raw query/policy/event payloads
and arbitrary error text. Source, hosted CI, Flux reconciliation, live QA and disposable evidence
remain distinct; one does not imply another.
