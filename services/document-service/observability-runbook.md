<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Document foundation observability runbook](#document-foundation-observability-runbook)
  - [Safe verification](#safe-verification)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Document foundation observability runbook

The foundation exports safe structured logs plus common HTTP, gRPC, process and Go runtime signals.
Echo proves transport delivery only; it is not Document business telemetry. Issue #364 owns the
complete domain dashboards, alerts and final operational qualification.

Local development sets `OTEL_ENABLED=false`. QA exports OTLP/gRPC without TLS to the in-cluster
`qa-alloy.qa-monitoring.svc.cluster.local:4317` receiver. That plaintext cluster hop has no CA or
hostname setting to configure or bypass. The workload has DNS and Alloy egress only. Alloy's
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
- fixed Echo operation/transport/outcome, HTTP route/status and gRPC method/status labels;
- common Go runtime and process instruments.

Do not query or retain request bodies, Echo messages, document identifiers, arbitrary errors,
credentials or business data. A healthy process with missing signals means delivery remains
unverified. Identity conflicts, dropped-series counters, exporter failures or absent container logs
are stop conditions; preserve sanitized timestamps and identities, inspect Alloy delivery, and fix
reviewed source rather than weakening filters.

The Step-4 local candidate emitted a structured startup record with namespace `insurance-hub`,
service `document-service`, implementation `go`, version `git-2f0bc5dd8bb7`, the configured
candidate instance and `local-dev` environment. Telemetry was disabled, so no exporter or delivery
claim applies. Current ordinary local-dev and QA delivery observations remain pending until the
Step-5 acceptance procedure records them.
