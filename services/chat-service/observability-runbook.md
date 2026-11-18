<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Chat observability runbook](#chat-observability-runbook)
  - [Signal and privacy boundary](#signal-and-privacy-boundary)
  - [Source and disposable validation](#source-and-disposable-validation)
  - [QA preflight and provisioning](#qa-preflight-and-provisioning)
  - [Live verification](#live-verification)
  - [Investigation workflow](#investigation-workflow)
  - [Recovery and cleanup](#recovery-and-cleanup)
  - [Evidence checklist](#evidence-checklist)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Chat observability runbook

This runbook owns Chat monitoring source validation, guarded QA provisioning, safe investigation,
recovery and evidence. Use the [deployment runbook](deployment-runbook.md) for candidate identity,
workload operations and private real-client qualification through unchanged Nginx.

## Signal and privacy boundary

The Chat dashboard and rules cover availability, active connections including healthy idle zero,
handshake outcomes/duration, bounded broadcast outcomes, close reasons, shutdown failures, Go
runtime, Pod resources and shared telemetry delivery. Username, topic, avatar, payload, Origin,
credential and arbitrary error values must not be metric attributes or indexed log labels.
`trace_id` and `span_id` may remain structured log correlation metadata but are not metric or Loki
index labels.

## Source and disposable validation

Run from the repository root without contacting a shared environment:

```sh
make monitoring-assets-check ENV_NAME=qa SVC_NAME=chat
make test-monitoring ENV_NAME=qa SVC_NAME=chat
make test-observability-backends OBSERVABILITY_SERVICE_DOMAIN=chat
```

The asset check validates schema, selectors, dashboard queries, alert expressions and Prometheus
rule tests. The fixture proves healthy idle, failures, missing telemetry and recovery. The backend
test uses disposable local Alloy/Prometheus/Loki/Tempo components and owned cleanup; it does not
prove QA delivery.

## QA preflight and provisioning

QA monitoring coordinates are `qa-insurance-hub`, namespace `qa-monitoring`, PrometheusRule
`qa-chat-service` and Grafana ConfigMap `qa-chat-dashboard`. Application signals must identify
`chat-service`, environment `qa`, implementation `go` and the exact accepted service version.

Before any apply, record the full monitoring source revision, render both objects, preserve their
SHA-256 values and read the existing object UIDs when present. Stop for a wrong context/namespace,
foreign UID, selector that can include `qa-chat-api-legacy-*`, missing source identity or unexpected
existing ownership.

With separate QA monitoring mutation authority, apply only through the existing owners:

```sh
make -C k8s prometheus-rules-apply ENV_NAME=qa SVC_NAME=chat
make -C k8s grafana-dashboard-apply ENV_NAME=qa SVC_NAME=chat
```

Read both objects back and compare them with a fresh render. Do not edit the applied resources or
Grafana dashboard in place. Provisioning monitoring does not authorize application deployment.

## Live verification

After the exact candidate is running and with separately authorized target access, require the full
reconciled source revision:

```sh
: "${MONITORING_SOURCE_REVISION:?set the full reconciled monitoring source revision}"
make verify-service-observability \
  ENV_NAME=qa \
  SVC_NAME=chat \
  VERIFY_MODE=enabled \
  VERIFY_SOURCE_REVISION="$MONITORING_SOURCE_REVISION"
```

The verifier may create bounded local port-forwards and must remove them. A present active-
connection value of zero is healthy idle; an available Deployment with no series is telemetry loss.
Keep render, apply/read-back and live signal evidence distinct.

## Investigation workflow

Use a bounded UTC range, normally 15 minutes. Start with availability and the active connection
series, then handshakes, broadcast failures, slow-peer closes, shutdown failures and shared exporter
health. Use only fixed attributes such as handshake outcome, broadcast event/outcome and close
reason. Correlate only the `chat WebSocket handshake completed` log record to its short handshake
span with structured `trace_id` and `span_id`. Other lifecycle records intentionally omit those
fields when no active span exists. Never search by username, topic or message bytes.

Representative queries include:

```promql
insurance_hub_chat_connections_active{
  service_name="chat-service",deployment_environment_name="qa",
  service_implementation="go"
}
```

```promql
sum by (insurance_hub_chat_handshake_outcome) (
  rate(insurance_hub_chat_handshakes_total{service_name="chat-service"}[5m])
)
```

```promql
sum by (insurance_hub_chat_broadcast_event,insurance_hub_chat_broadcast_outcome) (
  increase(insurance_hub_chat_broadcast_deliveries_total{service_name="chat-service"}[5m])
)
```

```logql
{service_name="chat-service",deployment_environment_name="qa",
 service_implementation="go"} | json | level=~"WARN|ERROR"
```

```traceql
{ resource.service.name = "chat-service" &&
  resource.insurance_hub.service.implementation = "go" &&
  resource.deployment.environment.name = "qa" }
```

Handshake failure requires both the tested ratio and minimum traffic. Any queue-full delivery,
slow-peer close or shutdown failure is actionable in the accepted window. Exporter failures,
backlog or drops indicate shared delivery trouble; they do not alone prove Chat is unhealthy.
Never stop shared Alloy, Prometheus, Loki or Tempo to manufacture evidence.

## Recovery and cleanup

For an application identity, readiness or network mismatch, stop and follow the deployment
runbook. Do not lower thresholds or relabel Java signals as Go. Restore prior monitoring only after
verifying the captured object UIDs still match; use the guarded apply owners with reviewed prior
source, then read back and reverify. Do not delete shared backends or another service's assets.

Stop foreground Grafana or verifier processes normally and require their owned port-forwards to be
absent. Preserve the first failure and any cleanup failure separately.

## Evidence checklist

Record the full source revision, rendered hashes, applied object UIDs/resource versions, dashboard
UID, active Prometheus group/rules, exact service version/instance, query time range, healthy-idle
zero, representative failure/recovery results, safe log/span correlation and cleanup. Record absent
or blocked live evidence honestly; disposable validation is not a QA pass.
