<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Go-service Grafana dashboards and alerts](#go-service-grafana-dashboards-and-alerts)
  - [Baseline and service judgment](#baseline-and-service-judgment)
  - [Common dashboard coverage](#common-dashboard-coverage)
  - [Common alert coverage](#common-alert-coverage)
  - [Domain and dependency extensions](#domain-and-dependency-extensions)
  - [Source ownership, provisioning and privacy](#source-ownership-provisioning-and-privacy)
  - [Validation and migration handoff](#validation-and-migration-handoff)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Go-service Grafana dashboards and alerts

Apply this baseline when specifying, implementing and accepting monitoring for any newly
implemented Go service. It defines common operational coverage, service-specific extensions,
source ownership and the evidence needed to accept dashboards and alerts.

Implementation examples illustrate the approach; verify that the selected service has the
required instrumentation, assets and validation support before claiming coverage.
Use the [Phase 4 review](phase-4-implementation-review.md#observability) for the wider signal,
security and migration gates.

## Baseline and service judgment

Every new Go service should provide a source-owned Grafana service dashboard and actionable
Prometheus alert rules covering the common operational concerns below. Add capability sections for
the dependencies and workers the service actually owns. One dashboard with clear sections is a
valid starting point; separate dashboards are useful when a domain workflow needs its own
investigation view.

**Use common sense and engineering judgment to add additional dashboards and alerts based on each
service's specific domain, business workflows, dependencies and failure modes. The common baseline
is a minimum, not an exhaustive list.** A service can be healthy at the HTTP and Pod levels while
its business work is failing or falling behind. Review those risks with the service owner and
record the selected domain signals, operator actions and acceptance evidence in its specification
and runbook. Explain any inapplicable baseline category, such as request metrics for a worker-only
service; do not satisfy it with empty panels or fabricated data.

Reuse established operational structure and validation approaches. Preserve existing services'
accepted dashboards and rules when extracting shared tooling, and derive each new service's
queries, thresholds and capabilities from its own telemetry and workload. Another service's
dependency panels, alert inventory and numeric thresholds are examples, not universal requirements.

## Common dashboard coverage

| Concern | Required view and investigation purpose |
| --- | --- |
| Identity and scope | Stable dashboard UID and service identity, with environment and implementation filters. Make the selected Go/Java cohort clear during coexistence; scope application and Kubernetes queries consistently so legacy Pods or another environment cannot contaminate results. |
| Request traffic | Rate by bounded operation and transport, with expected outcomes visible. Distinguish legitimate idle traffic from missing instrumentation. |
| Request failures | Unexpected service failures and their ratio to eligible requests, alongside total volume. Keep expected validation, not-found and business rejection outcomes distinguishable from infrastructure/internal failures. |
| Request latency | Latency percentiles and request volume, with drill-down by operation/transport. Use the actual histogram schema and units; an aggregate percentile alone can hide one slow operation. |
| Go runtime | Goroutines, heap/memory and garbage-collection signals supported by the service's accepted instrumentation. Relate changes to workload and release identity. |
| Pod resources | CPU and memory against configured requests/limits, plus throttling or other saturation signals where available. Account for replica count when interpreting totals. |
| Availability and lifecycle | Desired versus available replicas, readiness and restarts, with enough rollout context to distinguish a planned transition from an unexpected failure. |
| Telemetry delivery | Missing runtime/application signals, collector export failures/drops, queue pressure and delivery lag where supported. Label shared Alloy/backend signals as shared infrastructure health; do not imply they identify one service's loss. |
| Investigation | Safe Loki log queries and Tempo trace investigation with consistent service/environment/time selection. Include a short operator workflow from symptom to operation, dependency and representative trace. |

Panels should show units, useful legends, query windows and an explanation of no-data behavior.
An empty series must not silently display as a healthy zero. Keep healthy idle operation, an
intentionally disabled worker, missing telemetry and collection failure distinguishable.

## Common alert coverage

Cover the following outcomes with service-owned rules, or an explicitly documented shared rule
that provides the same visibility and response. Avoid duplicating a shared collector incident into
an independent page for every service.

| Concern | Alert behavior to define |
| --- | --- |
| Sustained request errors | Unexpected error ratio or count over a meaningful window, with a minimum-traffic guard and explicit outcome classification. |
| Excessive request latency | Latency above a workload-backed threshold for a sustained interval, with enough observations to interpret the percentile. |
| Unavailability | Desired service capacity is unavailable or under-replicated beyond the accepted rollout tolerance. Define deliberate scale-to-zero behavior where supported. |
| Resource pressure | Sustained CPU/memory or other supported saturation relative to the selected service's budget and replica model. |
| Unexpected restart | Unexpected container restarts, with a runbook that examines previous state, events, memory and release identity. |
| Missing telemetry | Expected live service signals disappear. Check availability and enabled state so legitimate shutdown or disabled collection has a defined outcome. |
| Delivery failure | Collector/backend export failures or dropped telemetry, with the affected signal path and shared ownership made clear. |
| Delivery backlog | Sustained queue saturation or measured delivery lag, with bounded recovery expectations and capacity investigation. |

Every alert must specify:

- Its metric/expression, bounded selectors, units, threshold, evaluation window and pending
  duration (`for`, or an explicit reason for immediate firing).
- The workload or resource-budget evidence behind the threshold, including minimum volume and
  expected rollout, idle, disabled-mode and missing-series behavior.
- Service/owner and severity labels, a useful summary and description, and a concrete
  `runbook_action` or runbook link explaining investigation, escalation and rollback conditions.
- Tests proving the expected firing time, suppression for healthy/idle/disabled cases, and recovery.

Choose thresholds from controlled healthy and failing workloads and the service's accepted
operating budget. A pilot threshold is not a production SLO. Production SLOs, burn-rate
alerts, external notifications and paging routes need their own agreed objectives and ownership.
Record whether an alert is visible only in Prometheus/Grafana
or has a tested notification destination.

## Domain and dependency extensions

Select additional dashboards and alerts by asking: **What useful work could stop or become
incorrect while requests, runtime metrics and Pods still look healthy?** The following examples
are prompts for service-specific decisions, not proof that the metrics or behavior already exist.

| Service capability or domain | Additional dashboard and alert candidates |
| --- | --- |
| PostgreSQL-backed service | Query/transaction latency and failures, pool utilization, acquisition waits and timeouts. |
| Search service | Search/index-update outcome and latency; event throughput, outcomes and duration; enabled-worker failure/stall and independently verified lag. Relate ingestion health to search freshness where a reliable signal exists. |
| Dashboard service | Domain projection/aggregation freshness, event-processing failures and backlog, plus aggregate update duration. Separate these business views from the Grafana operational dashboard itself. |
| Document generation | Event-to-document completion time, renderer failures/timeouts, queue age and object-storage operations. A successful render alone may not mean the document was stored and made available. |
| Payment and policy workflows | Failed or delayed accepted business transitions, stalled import/processing work, and persistence/event-publication outcomes where those stages exist. Keep expected business rejection separate from technical failure. |
| Pricing | Evaluation latency, evaluator failures and invalid rule/configuration outcomes. Observe safe outcome categories without exposing prices, customer answers or full expressions. |
| Chat/realtime | Connection failures, active sessions, message-delivery failures, slow consumers and disconnect/reconnect behavior where supported by the actual contract. |
| Any event consumer or scheduled worker | Processing success/failure/duration, retry or dead-letter behavior if implemented, outstanding work and progress age. Define enabled/disabled, idle and scheduled execution semantics. |

Never infer consumer lag or a stalled worker solely from an absence of events. First establish a
reliable signal for outstanding work, expected schedule or progress, with bounded labels and
controlled test results. The service's telemetry owner must resolve whether an existing
service/platform series suffices or the smallest safe metric must be added before the
dependent lag/stall rule is accepted. Adding monitoring does not implicitly authorize a new retry,
dead-letter, repair or reconciliation mechanism.

## Source ownership, provisioning and privacy

Use the existing layout under
[`k8s/observability/services/`](../../../k8s/observability/services/), with service-owned
`monitoring.json`, `dashboard.json`, `alerts.yaml`, `alerts.test.yaml`, `fixture.json` and acceptance
metadata as required by the owning checks. Keep environment coordinates in
[`k8s/observability/environments/`](../../../k8s/observability/environments/).
The [Product record](../../../k8s/observability/services/product/monitoring.json),
[dashboard](../../../k8s/observability/services/product/dashboard.json) and
[rules](../../../k8s/observability/services/product/alerts.yaml) provide concrete examples.
Use a versioned monitoring contract to describe common requirements and service capabilities.
Check the supported schema before adding metadata; extend its validation alongside any new
capability rather than assuming an undeclared field is supported.

Provision dashboard ConfigMaps and PrometheusRule resources through the existing guarded owners
in the [Kubernetes Makefile](../../../k8s/Makefile). Keep service assets out of shared Helm values.
Grafana dashboards should be provisioned and read-only: dashboard `editable: false`, provider
`allowUiUpdates: false` and `disableDeletion: true`. Verify effective UI/API behavior after apply;
source JSON alone is insufficient. Prometheus rules remain source-managed and inspectable through
Grafana. Preserve Helm/Flux ownership and restore accepted source through the owning apply paths.

Application signals continue through Alloy; dashboards query the configured Prometheus, Loki and
Tempo data sources. Use bounded allowlisted service/environment/implementation/operation/outcome
labels and variables. Keep credentials, raw SQL/search text, customer/policy/payment/chat/event
payloads, object keys and arbitrary error strings out of dashboards and unrestricted diagnostics.
Trace IDs may support safe log-to-trace investigation, but must not become metric labels or Loki
stream labels. Do not put raw topic/group identities into service dashboard variables or labels.
Review the effective exported schema and queries, including shared-platform series.

## Validation and migration handoff

Use the [infrastructure validation routes](../../../ai/rules/infrastructure-rules.md#change-to-check-index)
and the selected service's runbook. The existing
[`k8s/tests/validation.mk`](../../../k8s/tests/validation.mk) owns
`monitoring-assets-check`, `test-monitoring` and `check-observability`; service-selected targets use
explicit `ENV_NAME` and `SVC_NAME`. Confirm registration, fixtures and supported environments before
running them. A service-specific verifier, such as `verify-product-observability`, establishes
coverage only for its supported service. Add or generalize verification through the owning tooling
before claiming equivalent coverage for another service.

Before accepting a service's monitoring handoff:

1. Check the exact dashboard/rule inventory, selectors, typed metadata, safe labels, rendered
   resource identities and immutable provider settings. Preserve existing service compatibility when
   shared tooling changes.
2. Test every dashboard PromQL expression against independently collected disposable data.
   Exercise every alert's real evaluation windows, firing, suppression, no-data and recovery
   behavior; include the selected domain failure cases.
3. After explicit target authorization, perform guarded provisioning and verify the deployed
   dashboard UID, exact rule inventory, read-only behavior, actual service metrics and safe
   Loki/Tempo investigation. Use controlled tests that preserve shared dependencies.
4. Record the source/image/configuration and target identities, workload, threshold rationale,
   queries, results, limitations, owning runbook and tested restoration procedure. Distinguish
   local checks, disposable tests and actual environment observations.
5. Make the accepted monitoring evidence available before the migration's live ownership/traffic
   transfer. Define how dashboard gaps, unexpected firing alerts and domain failures affect the
   observation, abort and rollback decisions.

The [Product observability runbook](product-service/observability-runbook.md) demonstrates the
existing provisioning, investigation and evidence workflow. Adapt its target coordinates,
workload and thresholds to the new service. Passing another service's checks or opening an empty dashboard
does not establish that the new service is operationally observable.
