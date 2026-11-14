<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Document lookup observability runbook](#document-lookup-observability-runbook)
  - [Safe verification](#safe-verification)
  - [Kafka and generation troubleshooting order](#kafka-and-generation-troubleshooting-order)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Document lookup observability runbook

The lookup candidate exports safe structured logs plus HTTP, gRPC, PostgreSQL, object-store,
lookup, process and Go runtime signals. Issue #364 owns the complete domain dashboards, alerts and
final operational qualification.

Local development sets `OTEL_ENABLED=false`. QA exports OTLP/gRPC without TLS to the in-cluster
`qa-alloy.qa-monitoring.svc.cluster.local:4317` receiver. That plaintext cluster hop has no CA or
hostname setting to configure or bypass. The workload egress is limited to DNS, its selected
PostgreSQL cluster, its MinIO namespace and Alloy in QA. Alloy's
repository registration reads `qa-document-api` container logs through its existing Kubernetes API
and RBAC path; the workload exposes no log ingress.

## Safe verification

Validate source and the candidate first:

```sh
make -C services/document-service check-go-pr FORMAT_SCOPE=all
make go-svc-image-check SVC_NAME=document
make go-svc-deploy-check SVC_NAME=document
```

For a verified QA release, use the deployment runbook to establish the exact Pod UID, runtime
version, image digest and Flux-applied revision. Then query the existing observability backends for
only these bounded identities:

- `service.namespace=insurance-hub`, `service.name=document-service`, implementation `go`;
- `service.version=git-<image-source-short-sha>` and `service.instance.id=<actual Pod UID>`;
- container `document-api` in namespace `qa-svc`;
- fixed `find_documents` operation/transport/outcome, HTTP route/status and gRPC method/status;
- bounded complete/partial lookup and PostgreSQL/object-store dependency outcomes and durations;
- bounded registration outcomes/durations, commit failures and worker enabled/in-flight/failed
  state;
- common Go runtime and process instruments.

Do not query or retain request bodies, policy numbers, object keys, document bytes, arbitrary errors,
credentials or business data. A healthy process with missing signals means delivery remains
unverified. Identity conflicts, dropped-series counters, exporter failures or absent container logs
are stop conditions; preserve sanitized timestamps and identities, inspect Alloy delivery, and fix
reviewed source rather than weakening filters.

## Kafka and generation troubleshooting order

Start every common Kafka or worker investigation with Dashboard service evidence. Compare its
implementation and operating guidance first, in this order:

1. configuration, TLS trust, topic and group coordinates;
2. broker connectivity, inactive-group preflight and stable partition assignment;
3. committed progress, weak auto-commit and shutdown-commit signals;
4. readiness, cancellation and resource-lifecycle behavior;
5. fixed event outcomes, duration and worker-state signals.

Do not change, join or reset Dashboard's group while troubleshooting Document. Dashboard is a
reference implementation, not a fallback runtime dependency. After the shared Kafka path is shown
healthy or a Document-specific difference is isolated, inspect Document's fixed `POLICY` render,
MinIO upload and PostgreSQL insert in that order. A failed handler is a terminal consumed outcome
and may already be weakly committed; a missing commit can replay it. Never infer exactly-once
delivery, delete an orphan object, deduplicate a row or weaken trust as an incident response.

Readiness may be unavailable while liveness remains healthy when Kafka assignment/connectivity or
the most recent generation attempt failed. Distinguish disabled (`worker.enabled=0`), enabled idle
(`enabled=1`, zero in-flight age), in-flight (age grows), and failed (`failed=1`) without querying
payload fields. A later successful event clears the failed state. Retain only bounded timestamps,
fixed outcomes and source/runtime identities; do not retain record values, policy numbers, object
keys, PDF bytes, credentials or raw dependency errors.

The image check requires a structured startup record with namespace `insurance-hub`, service
`document-service`, implementation `go`, the exact source-derived version, configured candidate
instance and `local-dev` environment. Telemetry is disabled for that fixture, so no exporter or
delivery claim follows from it. Ordinary local-dev and QA delivery observations remain pending
until the live acceptance procedure records them.
