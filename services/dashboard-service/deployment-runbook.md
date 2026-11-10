<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Dashboard deployment runbook](#dashboard-deployment-runbook)
  - [Deployment coordinates](#deployment-coordinates)
  - [Qualify the candidate](#qualify-the-candidate)
  - [Provision reader prerequisites](#provision-reader-prerequisites)
  - [Prepare ordinary registration resources](#prepare-ordinary-registration-resources)
  - [Deploy to local development](#deploy-to-local-development)
  - [Stop in local development](#stop-in-local-development)
  - [Stage and observe QA](#stage-and-observe-qa)
  - [Diagnostics, stop conditions and recovery](#diagnostics-stop-conditions-and-recovery)
  - [Evidence checklist and later ownership](#evidence-checklist-and-later-ownership)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Dashboard deployment runbook

This runbook owns qualification and guarded operation of the private Go Dashboard queries and its
registration worker. Local-dev and QA use `policy_stats`, `policy-registered` and
`dashboard-service` with ordinary Elasticsearch and Kafka trust Secrets. The retained Java source
and manifests are outside this source step's removal scope and are not deployment prerequisites.

Use the companion [observability runbook](observability-runbook.md) for Dashboard monitoring
source validation, QA provisioning/read-back, investigation and monitoring restoration. The two
procedures share service and image identity, but workload reconciliation remains Flux-owned while
the Dashboard PrometheusRule and Grafana ConfigMap use their guarded monitoring owners.

## Deployment coordinates

Use catalog service `dashboard` only. Local development uses context
`kind-local-dev-insurance-hub`, service/data namespace `local-dev-all` and Kind cluster
`local-dev-insurance-hub`. QA uses context `qa-insurance-hub`, service namespace `qa-svc`, data
namespace `qa-data` and the shared Flux source on branch `main` at `./k8s/overlays/qa/svc`.

The Go resources are `dashboard-api`, `dashboard-api-management`, `dashboard-config` and the
`dashboard-api` NetworkPolicy after the environment prefix is applied. The protected Java
Deployment and Service are both named `dashboard-api-legacy` after the same prefix.

The Go Deployment uses `Recreate`; registration ownership must never use a surging rollout. The
edge route remains a separate setting.

## Qualify the candidate

Run these read-only or disposable checks from the repository root:

```sh
make go-svc-deploy-check SVC_NAME=dashboard
make go-svc-deploy-test-tooling SVC_NAME=dashboard
make go-svc-deploy-smoke SVC_NAME=dashboard
make monitoring-assets-check ENV_NAME=qa SVC_NAME=dashboard
make test-monitoring ENV_NAME=qa SVC_NAME=dashboard
make -C services/dashboard-service test-event-integration
```

The default disposable command builds and qualifies the current committed source. To qualify an
existing immutable registry candidate without rebuilding it, pass its digest-qualified reference,
the full matching OCI source revision and an absolute JSON report path:

```sh
make go-svc-deploy-smoke SVC_NAME=dashboard \
  QUALIFICATION_IMAGE_REF='ghcr.io/igor-baiborodine/insurance-hub/dashboard-api:VERSION@sha256:DIGEST' \
  QUALIFICATION_SOURCE_REVISION='FULL_40_CHARACTER_REVISION' \
  QUALIFICATION_REPORT_FILE="$PWD/dashboard-qualification.json"
```

The disposable test loads the candidate image plus pinned Elasticsearch and Kafka images into the
existing configured `local-dev-insurance-hub` Kind cluster, then creates and removes one uniquely
owned namespace. It proves the disabled query deployment first, enables the ordinary registration
path, verifies exact-index permissions and Kafka CA/hostname TLS behavior, and sends three
registrations including a same-ID replacement. It exercises all three query operations, snapshots
documents, mapping and committed offsets, restarts only the Go Deployment, compares retained state,
reruns setup to prove it preserves populated state, and proves one new event advances state and
offsets. The JSON report contains only synthetic state and identity evidence. It does not create or
delete the cluster. A render, fake adapter or disposable pass does not establish a local-dev or QA
deployment result.

The monitoring checks validate Dashboard request, Elasticsearch and registration panels, alerts,
Prometheus rule tests and the collected disposable fixture. The registration owner distinguishes
disabled, enabled-idle and stalled state and reports fixed outcome/duration and commit failures.
It does not infer broker lag from these signals. These checks do not prove QA Alloy delivery.

## Provision reader prerequisites

The Dashboard environment administrator owns one guarded reader operation before deployment. It
uses the target ECK administrator Secret only in host memory, sends workload Secret values through
Kubernetes API stdin, and emits sanitized identity/status output. It never creates, seeds, remaps
or writes `policy_stats`.

Set the exact target coordinates and make that context current. For local development:

```sh
export DEPLOY_ENV=local-dev
export DEPLOY_CONTEXT=kind-local-dev-insurance-hub
export DEPLOY_SVC_NS=local-dev-all
export DEPLOY_DATA_NS=local-dev-all
export DASHBOARD_READER_CONFIRM=ensure-local-dev-dashboard-reader

make go-svc-reader-ensure SVC_NAME=dashboard
unset DASHBOARD_READER_CONFIRM
make go-svc-reader-verify SVC_NAME=dashboard
```

For QA:

```sh
export DEPLOY_ENV=qa
export DEPLOY_CONTEXT=qa-insurance-hub
export DEPLOY_SVC_NS=qa-svc
export DEPLOY_DATA_NS=qa-data
export DASHBOARD_READER_CONFIRM=ensure-qa-dashboard-reader

make go-svc-reader-ensure SVC_NAME=dashboard
unset DASHBOARD_READER_CONFIRM
make go-svc-reader-verify SVC_NAME=dashboard
```

Ensure verifies the concrete compatible `policy_stats` mapping before mutation, creates the exact
`go_dashboard_total_sales_reader` role/user, and grants only `read` plus
`view_index_metadata` on that index. It creates the namespace-local workload Secret with only
`username` and `password`. In QA it also copies only `ca.crt` from the ECK public CA Secret to
`qa-dashboard-elasticsearch-ca` and verifies exact equality. Existing identities or Secrets not
owned by issue 287 fail closed.

If an interrupted first attempt leaves an owned reader role/user without a recoverable workload
Secret, use explicit rotation instead of rerunning ensure:

```sh
export DASHBOARD_READER_CONFIRM=rotate-${DEPLOY_ENV}-dashboard-reader
make go-svc-reader-rotate SVC_NAME=dashboard
unset DASHBOARD_READER_CONFIRM
make go-svc-reader-verify SVC_NAME=dashboard
```

Rotation generates a new credential, updates the dedicated identity, applies the workload Secret
and verifies the effective privilege boundary. When an earlier owned Secret exists, a failed Secret
apply or verification restores its credential. Do not hand-create, copy or print credential data.
After QA ensure succeeds, leave the shared Flux owner active and wait for it to reconcile; do not
apply the Dashboard workload directly.

## Prepare ordinary registration resources

Registration inspection and verification are read-only. Ensure creates only the accepted empty mapping, ordinary reader and writer identities, workload Secrets, Kafka CA Secret and topic when they are absent. It preserves compatible populated state, rejects foreign ownership or incompatible mappings and never seeds documents. Bind the exact catalog coordinates before running:

```sh
make -C services/dashboard-service registration-inspect
export DASHBOARD_REGISTRATION_CONFIRM="ensure-${DEPLOY_ENV}-dashboard-issue-293"
make -C services/dashboard-service registration-ensure
unset DASHBOARD_REGISTRATION_CONFIRM
make -C services/dashboard-service registration-verify
```

The owners require two matching Kafka metadata/progress snapshots, every topic partition, in-range committed offsets and the exact ordinary index and identities. Provisioning must preserve the initial document count. Registration administration has no destructive command.

## Deploy to local development

Use this guarded sequence only after the target Elasticsearch Secret, exact-index reader and
NetworkPolicy resources have passed the target-specific deployment gate. Candidate and disposable
qualification alone do not authorize or prepare the shared local-dev namespace.

Build and validate the image first. Record its full OCI revision, source-derived version, local
reference and image ID. The version is `git-<first-12-source-hex>` and the local reference ends in
the same 12 hexadecimal characters. Reject zero, `unconfigured`, `development`, mismatched or
tag-only evidence.

```sh
make go-svc-image-check SVC_NAME=dashboard
```

Set the exact accepted reference without shell placeholders, then run the guarded sequence. These
coordinates are intentionally explicit so a changed current context or namespace fails before
mutation.

```sh
export DEPLOY_ENV=local-dev
export DEPLOY_CONTEXT=kind-local-dev-insurance-hub
export DEPLOY_SVC_NS=local-dev-all
export DEPLOY_DATA_NS=local-dev-all
: "${DEPLOY_IMAGE_REF:?set the exact source-derived local Dashboard image reference}"
make go-svc-deploy-preflight SVC_NAME=dashboard
make go-svc-image-load SVC_NAME=dashboard
make go-svc-deploy SVC_NAME=dashboard
make go-svc-deploy-status SVC_NAME=dashboard
make go-svc-deploy-verify SVC_NAME=dashboard
```

For a local-dev replacement, capture the running image before preflight and keep it declared until
the new image is applied:

```sh
export DEPLOY_PREVIOUS_IMAGE_REF="$(kubectl --context "$DEPLOY_CONTEXT" \
  -n "$DEPLOY_SVC_NS" get deployment/local-dev-dashboard-api \
  -o jsonpath='{.spec.template.spec.containers[0].image}')"
make go-svc-deploy-preflight SVC_NAME=dashboard
make go-svc-image-load SVC_NAME=dashboard
make go-svc-deploy SVC_NAME=dashboard
unset DEPLOY_PREVIOUS_IMAGE_REF
make go-svc-deploy-status SVC_NAME=dashboard
make go-svc-deploy-verify SVC_NAME=dashboard
```

The predecessor input accepts only a source-derived local Dashboard image. It permits that exact
image, Deployment version labels and ConfigMap `SERVICE_VERSION` only before apply. Final status
and verification require the selected replacement identity everywhere.

Preflight requires the namespace-local `local-dev-dashboard-elasticsearch` Secret to contain only
the exact reader username and password, requires the reader identity
`go_dashboard_total_sales_reader`, and verifies the selected ECK HTTP Service before image or
workload mutation. It never prints or passes credential values on a host command line.

Verify the permitted HTTP total-sales, agent-sales and sales-trends requests, all three direct sales RPCs
and management endpoints, then
use labeled and unlabeled probe Pods to establish NetworkPolicy behavior. Confirm that OCI,
runtime, Pod and ConfigMap versions agree; retain the full OCI revision, image tag and immutable
digest separately. Verify exact-index reads, write/admin/unrelated-index denials, readiness during
Elasticsearch loss and independent liveness. No Kafka setting, credential, topic or group belongs
to the Go reader.

`go-svc-deploy-verify` is a bounded target operation, not a read-only status command. It creates
fifteen fixed-name qualification Pods and one fixed-name untrusted namespace, requires each name to
be absent first, and removes every created resource. The probes use the pinned curl image, exercise
all three allowed business operations, gRPC-listener and management identities, marker-only and
business-to-management denial, and copied complete HTTP/gRPC identities in the untrusted
namespace. The verifier also calls the deployed `GetTotalSales`, `GetAgentSales` and
`GetSalesTrends` RPCs through the business Service. Shared-state checks require successful
operation envelopes without assuming fixture totals; the disposable owner retains the exact seeded
result assertions. The reader probe mounts the workload Secret inside Kubernetes, verifies
exact-index count/mapping and the authenticated effective-privilege response, and checks
unrelated-index, cluster-health and security-role denial with read-only requests. It does not
attempt a write that could mutate ordinary state if permissions drift.

## Stop in local development

The bounded Go stop command scales only `local-dev-dashboard-api` to zero:

```sh
make go-svc-stop SVC_NAME=dashboard
```

Confirm that the Go endpoints disappear. Restore the same accepted image with the guarded deploy, status and verify commands above.

## Stage and observe QA

The shared QA aggregate retains both `./dashboard` and `./dashboard/legacy` under the explicit
source constraint for this step. The Go overlay directly selects ordinary event processing.
The release owner publishes a reviewed semantic image, records its immutable digest and exact image
source revision, and updates the Go overlay through the guarded release workflow. Flux performs
every persistent QA mutation; direct status and verification commands do not own desired state.

Do not apply, delete, load an image, scale a workload, live-patch desired state or suspend Flux in
QA. Status is read-only. Verification requires separate authorization for its short-lived probe
Pods and namespace, plus the full commit reported by the shared `qa-svc` Flux source and applied
revision:

```sh
export DEPLOY_ENV=qa
export DEPLOY_CONTEXT=qa-insurance-hub
export DEPLOY_SVC_NS=qa-svc
export DEPLOY_DATA_NS=qa-data
: "${DEPLOY_SOURCE_REVISION:?set the full Flux source applied revision}"
: "${DEPLOY_IMAGE_DIGEST:?set the sha256 registry digest}"
make go-svc-deploy-status SVC_NAME=dashboard
make go-svc-deploy-verify SVC_NAME=dashboard
```

QA preflight and verification require `qa-dashboard-elasticsearch` with only the exact reader
username/password and `qa-dashboard-elasticsearch-ca` with only `ca.crt`. The latter must equal the
`ca.crt` value in `qa-data/qa-elasticsearch-es-http-certs-public`. The workload verifies
`qa-elasticsearch-es-http.qa-data.svc` with TLS 1.2 or newer; no insecure or server-name override is
supported.

`DEPLOY_SOURCE_REVISION` is the Git commit reconciled by the shared Flux owner; it is not the image's
OCI source revision after a later release-preparation or activation commit. Record the published
image source revision separately with its semantic reference and immutable digest. Status and
verification fail when the digest is absent or malformed, when the selected ready Pod inventory
differs, or when any running `dashboard-service` container reports another image ID.

QA acceptance records the Flux source and applied revision, semantic and digest-qualified image,
Pod image ID, runtime identity, labeled and denied network probes, health and three-operation sales
results, reader permission denials, dependency behavior and Alloy delivery. Configuration or
disposable evidence alone does not establish a QA result.


## Diagnostics, stop conditions and recovery

Stop before a target mutation when any declared environment, context, namespace,
Dashboard resource name, source revision, image reference/digest, Secret shape, Elasticsearch
index/identity, Kafka topic/group or current Flux owner differs from the recorded candidate. Stop
after mutation for a placeholder/mismatched runtime identity, an unexpected ready Pod, failed
business/management probe, ownership overlap, reader privilege expansion, a writer or worker that
does not match the ordinary registration coordinates, unstable or out-of-range committed progress,
an unexplained readiness failure or incomplete owned cleanup. Preserve the first failure and
current source/image/resource identities before retrying.

Use `go-svc-deploy-status SVC_NAME=dashboard` for the selected target before classifying stale
source or image state. Check the exact Deployment, ConfigMap, Services, NetworkPolicy, Pod image ID
and runtime identity; use the [observability runbook](observability-runbook.md#investigation-workflow)
for bounded metrics/logs/traces. Do not print Secrets, raw events or Elasticsearch responses. A
collector failure must not make liveness depend on Alloy.

For local development, recovery uses the captured accepted image and the same guarded preflight,
image-load, deploy, status and verify sequence. `go-svc-stop` may scale only
`local-dev-dashboard-api`; it does not remove dependency resources.

For QA, do not patch the workload or suspend Flux. Revert the reviewed Dashboard source change,
let `qa-svc` reconcile, and record both source and applied revisions. Verify the exact prior image
digest, Pod identity, reader permissions and monitoring behavior after reconciliation.
If monitoring source differs from application source, restore it through the separate guarded
procedure rather than treating an application rollback as monitoring restoration.

Clean up only resources owned by the failed operation. The verifier owns its fixed probe Pods and
untrusted namespace; the disposable deployment owns its unique namespace. Before retrying after an
interruption, check those exact recorded names and preserve an unexpected resource for diagnosis.
Do not use broad namespace, Pod, process or consumer-group cleanup.

Reader rotation remains an explicit administrative action with its own confirmation. Removing group
offsets, ordinary data, shared Kafka/Elasticsearch/Alloy resources, Java authority or persistent
QA Flux ownership is outside this runbook.

## Evidence checklist and later ownership

Retain the following without credentials or business data:

- intended base/head and full candidate source revision;
- semantic/local image reference, full OCI source revision, source-derived build version,
  immutable registry digest and actual Pod image ID;
- selected environment/context/namespaces, Flux source/applied revisions and exact resource UIDs;
- configuration identity, worker mode, Elasticsearch index/reader/writer identities and Kafka
  topic/group without secret values;
- render/preflight/status/verify results, all three HTTP/RPC outcomes, readiness/liveness,
  dependency fault/recovery and allowed/denied network/reader observations;
- monitoring dashboard/rule identities, bounded investigation results and any stop/recovery path;
- owned disposable/probe cleanup and final accepted state.

Issue 291 owns the monitoring and service-root operating procedures. Issue 292 owns cumulative
practical load and full application restoration evidence. Nothing in this runbook alone authorizes
a shared-target mutation.
