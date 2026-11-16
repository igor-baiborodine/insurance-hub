<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Document observability runbook](#document-observability-runbook)
  - [Signal and truthful-state boundary](#signal-and-truthful-state-boundary)
  - [Source and disposable validation](#source-and-disposable-validation)
  - [QA identity preflight and stop conditions](#qa-identity-preflight-and-stop-conditions)
  - [Guarded provisioning and read-back](#guarded-provisioning-and-read-back)
  - [Investigation and alert response](#investigation-and-alert-response)
  - [Recovery, restoration and cleanup](#recovery-restoration-and-cleanup)
  - [Evidence limits](#evidence-limits)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Document observability runbook

This runbook owns source validation, disposable signal-delivery checks, guarded QA monitoring
provisioning, read-back, investigation and restoration for the Go Document service. Use service
key `document`, environment `qa`, Kubernetes context `qa-insurance-hub` and monitoring namespace
`qa-monitoring`. The application Deployment is `qa-document-api`; the monitoring resources are
PrometheusRule `qa-document-service`, ConfigMap `qa-document-dashboard` and Grafana dashboard UID
`insurance-hub-document-service`.

The monitoring objects are applied through the selected-service Make owners below. They are
separate from the Flux-owned workload. Do not edit the dashboard in Grafana, patch the Deployment
or generated monitoring objects, suspend Flux, reset Kafka progress, mutate Dashboard, or remove
Java or shared resources while following this procedure. Every live write needs authorization for
that exact operation and a fresh read-only identity preflight.

Local development sets `OTEL_ENABLED=false`. QA exports OTLP/gRPC without TLS to the in-cluster
`qa-alloy.qa-monitoring.svc.cluster.local:4317` receiver. That plaintext cluster hop has no CA or
hostname setting to configure or bypass. Alloy reads `qa-document-api` container logs through its
existing Kubernetes API and RBAC path; the workload exposes no log ingress.

## Signal and truthful-state boundary

Document exports bounded request, lookup, dependency and registration signals plus Go runtime and
process instruments. Generation is a sequence, not a single success state:

1. `render` proves that JSReport returned a PDF;
2. `upload` proves that the PDF was stored in MinIO;
3. `metadata` proves that its object key was inserted in PostgreSQL;
4. a successful owned `lookup` proves that the stored bytes are retrievable.

Registration success follows upload and metadata insertion, but it does not prove later lookup.
A metadata failure after upload leaves an observable orphan risk. Lookup may be complete, partial
with omitted documents, or failed. Never describe render, upload, metadata registration or an
available Deployment as proof of retrievability.

Worker state is also explicit. Disabled means `enabled=0`; enabled idle means `enabled=1` with no
in-flight age; in-flight means the age grows; failed means `failed=1`. Missing samples are not an
idle state. A failed handler may already be weakly committed, while a commit failure may replay the
record. These signals do not prove exactly-once processing, backlog clearance or completeness.
The final local-dev and QA desired states require `enabled=1`. Source validation does not activate a
shared worker, and the first shared generation observation requires a separately authorized event.

Labels contain only fixed operations, stages, dependencies, transports, outcomes and event type.
Do not query or retain request bodies, policy numbers, object keys, PDF bytes, event values,
credentials, arbitrary errors or other business data. The limits in
`k8s/observability/services/document/acceptance.json` are learning-project operating limits, not
production SLOs or capacity guarantees.

## Source and disposable validation

From the repository root, validate the exact source before any QA operation:

```sh
make -C services/document-service check-go-pr FORMAT_SCOPE=all
make go-svc-image-check SVC_NAME=document
make go-svc-deploy-check SVC_NAME=document
make monitoring-assets-check ENV_NAME=qa SVC_NAME=document
make test-monitoring ENV_NAME=qa SVC_NAME=document
make test-observability-backends OBSERVABILITY_SERVICE_DOMAIN=document
```

The monitoring asset owner checks typed inventory, chart render, rule syntax and firing,
suppressed and recovered fixtures. The collected-data owner runs every Document Prometheus query
against disposable samples. The backend owner sends synthetic Document metrics, logs and traces
through owned Alloy, Prometheus, Loki and Tempo processes, then removes them. These results are
source, fixture and disposable evidence; they neither apply QA objects nor prove live QA delivery.

The image check proves a source-derived startup identity with telemetry disabled. It does not prove
Alloy delivery. The disposable probe may enable telemetry and its owned worker only inside its
isolated fixture. It does not authorize a shared event, group or writer identity.

## QA identity preflight and stop conditions

A fresh read-only identity preflight is required immediately before a separately authorized
monitoring write or live-signal acceptance. Use the
[deployment runbook](deployment-runbook.md#qa-release-and-verification) first to establish the
exact Flux source/applied revision, image reference and digest, Pod image ID, runtime
`service.version`, Pod UID and worker-enabled configuration. Do not read Secret values.

Capture the monitoring objects read-only. Keep the commands in one shell so the source revision
and private evidence directory remain bound to the attempt:

```sh
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
  deployment/qa-document-api -o json \
  > "$MONITORING_EVIDENCE_DIR/document-deployment.json"
kubectl --context qa-insurance-hub -n qa-monitoring get \
  prometheusrule/qa-document-service -o json --ignore-not-found \
  > "$MONITORING_EVIDENCE_DIR/prior-rule.json"
kubectl --context qa-insurance-hub -n qa-monitoring get \
  configmap/qa-document-dashboard -o json --ignore-not-found \
  > "$MONITORING_EVIDENCE_DIR/prior-dashboard.json"

test "$(jq -er '.status.artifact.revision' \
  "$MONITORING_EVIDENCE_DIR/flux-source.json")" = \
  "$(jq -er '.status.lastAppliedRevision' \
  "$MONITORING_EVIDENCE_DIR/flux-qa-svc.json")"
export MONITORING_SOURCE_REVISION="$(jq -er \
  '.status.lastAppliedRevision | capture("sha1:(?<sha>[0-9a-f]{40})$").sha' \
  "$MONITORING_EVIDENCE_DIR/flux-qa-svc.json")"
test "$MONITORING_SOURCE_REVISION" = "$(git rev-parse HEAD)"

if test -s "$MONITORING_EVIDENCE_DIR/prior-rule.json"; then
  test -s "$MONITORING_EVIDENCE_DIR/prior-dashboard.json"
else
  test ! -s "$MONITORING_EVIDENCE_DIR/prior-dashboard.json"
fi
```

If both monitoring objects are absent, run the read-only pre-provision verifier. If both exist,
record their UIDs and stable `spec`/`data` hashes and verify that they came from the reviewed source.
Partial, foreign, placeholder or mismatched state stops the operation.

```sh
make verify-service-observability \
  ENV_NAME=qa \
  SVC_NAME=document \
  VERIFY_MODE=pre-provision \
  VERIFY_SOURCE_REVISION="$MONITORING_SOURCE_REVISION"
```

Stop for an unexpected context, namespace, Flux revision, Deployment/Pod/configuration/image
identity, a worker mode other than the final enabled state, monitoring name/UID/source, mutable
Grafana provider,
unexplained firing alert, missing Document signal, sensitive or unbounded query, or any operation
that would modify shared Alloy, Prometheus, Loki, Tempo, Kafka, Java, Dashboard or another service.
Repeat the preflight after any source, image, configuration, Flux or monitoring-object change.

## Guarded provisioning and read-back

Render and validate the exact candidates before any live write:

```sh
make --no-print-directory -C k8s \
  prometheus-rules-render ENV_NAME=qa SVC_NAME=document \
  > "$MONITORING_EVIDENCE_DIR/candidate-rule.yaml"
make --no-print-directory -C k8s \
  grafana-dashboard-render ENV_NAME=qa SVC_NAME=document \
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

Confirm the exact names and namespace, dashboard immutability, rule group
`insurance-hub-document-service`, service selector `document-service`, QA/Go filters and exclusion
of `qa-document-api-legacy-*`. Rendering is source evidence and server dry-run is admission
evidence; neither is application or delivery evidence.

Only with separate authorization for these two QA monitoring objects, apply through the guarded
owners:

```sh
make -C k8s prometheus-rules-apply ENV_NAME=qa SVC_NAME=document
make -C k8s grafana-dashboard-apply ENV_NAME=qa SVC_NAME=document
```

Read back both objects, record their UIDs, re-render the reviewed source and compare the candidate
and re-rendered bytes:

```sh
kubectl --context qa-insurance-hub -n qa-monitoring get \
  prometheusrule/qa-document-service -o yaml \
  > "$MONITORING_EVIDENCE_DIR/applied-rule.yaml"
kubectl --context qa-insurance-hub -n qa-monitoring get \
  configmap/qa-document-dashboard -o yaml \
  > "$MONITORING_EVIDENCE_DIR/applied-dashboard.yaml"
make --no-print-directory -C k8s \
  prometheus-rules-render ENV_NAME=qa SVC_NAME=document \
  > "$MONITORING_EVIDENCE_DIR/rerendered-rule.yaml"
make --no-print-directory -C k8s \
  grafana-dashboard-render ENV_NAME=qa SVC_NAME=document \
  > "$MONITORING_EVIDENCE_DIR/rerendered-dashboard.yaml"
cmp "$MONITORING_EVIDENCE_DIR/candidate-rule.yaml" \
  "$MONITORING_EVIDENCE_DIR/rerendered-rule.yaml"
cmp "$MONITORING_EVIDENCE_DIR/candidate-dashboard.yaml" \
  "$MONITORING_EVIDENCE_DIR/rerendered-dashboard.yaml"
```

Inspect the active Prometheus group and immutable Grafana dashboard by UID. The repository's
foreground Grafana access owner is `make -C k8s grafana-ui`; stop it with Ctrl+C after the bounded
read-only inspection. Reuse the full reconciled `MONITORING_SOURCE_REVISION` established by the
preflight, never a branch or short SHA:

```sh
: "${MONITORING_SOURCE_REVISION:?set the full reconciled monitoring source revision}"
make verify-service-observability \
  ENV_NAME=qa \
  SVC_NAME=document \
  VERIFY_MODE=enabled \
  VERIFY_SOURCE_REVISION="$MONITORING_SOURCE_REVISION"
```

`VERIFY_MODE=enabled` means the verifier expects OTel export and the final enabled worker state; it
does not change either one. The verifier remains read-only against QA but owns bounded local
port-forwards and cleans them up. It must confirm worker-enabled mode, the exact monitoring objects
and bounded application, Prometheus, Loki and Tempo identities. Keep render, admission, apply,
Kubernetes read-back,
Grafana/Prometheus reconciliation and live-signal results as separate evidence.

## Investigation and alert response

Use a bounded absolute UTC range, normally 15 minutes. For a common request, runtime, Pod,
release-identity, telemetry-delivery, Kafka worker/commit or monitoring-owner symptom, first inspect
Dashboard service source, `k8s/observability/services/dashboard/` and the Dashboard deployment and
observability runbooks. Do not query or mutate Dashboard runtime state. Once the common contract is
confirmed, continue with Document.

Start with request rate, error ratio, p95, availability/restarts and telemetry delivery. Separate
HTTP from gRPC and expected from unexpected outcomes. Correlate a safe completion log's `trace_id`
with Tempo; never add it as a metric label. An available Deployment with no bounded Go runtime
sample is telemetry loss. Rising Alloy failures, drops, pending age or queue utilization identify
shared delivery trouble; they do not by themselves prove Document loss.

For generation, follow the fixed path and stop at the first failed stage:

1. `render` / `jsreport`: confirm bounded outcome/duration and the correlated client span;
2. `upload` / `object_store`: confirm stored completion and preserve the object identity only in
   protected operational state;
3. `metadata` / `postgresql`: confirm insertion; on failure, preserve orphan-risk evidence before
   retrying and do not delete or deduplicate data from this runbook;
4. `lookup`: separate PostgreSQL metadata from object-store fetch outcomes, then distinguish
   complete, partial/omitted and failed results.

For Kafka or worker symptoms, compare Dashboard's configuration/TLS/topic/group, connectivity and
assignment, weak commit, readiness/cancellation and bounded worker-state patterns first. Then
inspect Document's render, upload and metadata path. Do not join or reset Dashboard's group. Do not
change Document worker state from this runbook; activation and generation remain separately
authorized operations.

Respond to alert families without changing thresholds to hide the symptom:

- request error/latency: confirm the 20-request gate, split transport/status, then inspect lookup
  dependencies and a representative trace;
- generation/lookup: identify the fixed stage and dependency, preserve orphan/omission evidence,
  and do not infer retrievability from registration;
- worker failure/stall/commit: require enabled state, distinguish idle/in-flight/failed, preserve
  bounded offset and stage evidence, and account for possible replay;
- resource/unavailable/restart: inspect the exact Pod, rollout, readiness, events and image identity
  before workload recovery;
- telemetry missing/delivery/backlog: keep the application available, inspect the Alloy-to-backend
  route and require recovery within the accepted window before closing the alert.

Exercise failures only in the disposable harness. Never stop a shared dependency to generate an
incident.

## Recovery, restoration and cleanup

For application source, image, configuration or network mismatch, stop acceptance and use the
[deployment recovery procedure](deployment-runbook.md#qa-release-and-verification). QA workload
recovery is a reviewed Git change plus Flux reconciliation; never patch or scale the Deployment,
reset progress or treat monitoring restoration as workload rollback.

For monitoring mismatch, verify that captured UIDs still identify only
`qa-document-service` and `qa-document-dashboard`. In an isolated checkout of the captured accepted
revision, rerun asset validation, render the prior objects, apply only those two resources through
the same guarded owners under separate restore authorization, and compare their stable `spec` and
`data` payloads with the preflight captures. Follow the live restoration with the reviewed source
revert needed for durable ownership.

For a proven first installation, deletion requires separate authorization for the exact
PrometheusRule and ConfigMap. Verify their absence and the health of shared monitoring and other
service assets afterward. Do not delete releases, PVCs, the Document workload, Java resources,
dependencies, ordinary data or another service's assets. Stop foreground UI/port-forward commands
with Ctrl+C and verify that their owned local processes ended. Never use broad process, namespace
or Kubernetes selectors for cleanup.

## Evidence limits

Retain reviewed base/head/source identities, rendered hashes, server admission and apply/read-back
results, monitoring UIDs, full application image/source/digest/runtime/Pod chain, Flux
source/applied revisions, worker-enabled mode, bounded query/alert results, recovery outcome and
owned cleanup. Record source, fixture, disposable and live evidence separately.

Do not retain Secrets, credentials, raw errors, event bodies, policy/customer values, object keys
or PDF bytes. A source check, image startup record, another service's pass or an enabled-worker
observation is not proof of live Document generation. A separately authorized synthetic event and
bounded lookup proof are required; this runbook grants no live authority.
