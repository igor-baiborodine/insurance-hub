<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Dashboard Total-Sales Deployment Runbook](#dashboard-total-sales-deployment-runbook)
  - [Deployment coordinates](#deployment-coordinates)
  - [Qualify the candidate](#qualify-the-candidate)
  - [Deploy to local development](#deploy-to-local-development)
  - [Stop and return in local development](#stop-and-return-in-local-development)
  - [Stage and observe QA](#stage-and-observe-qa)
  - [Return from QA activation](#return-from-qa-activation)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Dashboard Total-Sales Deployment Runbook

This runbook owns qualification and guarded operation of the private Go Dashboard total-sales
reader. The Go workload runs beside the Java Dashboard and reads the existing `policy_stats`
Elasticsearch index. Java retains writer, setup, edge retry, public route and event authority.
Issue 291 completes the service operating handoff. Issue 292 owns the later full removal and
restoration rehearsal.

## Deployment coordinates

Use catalog service `dashboard` only. Local development uses context
`kind-local-dev-insurance-hub`, service/data namespace `local-dev-all` and Kind cluster
`local-dev-insurance-hub`. QA uses context `qa-insurance-hub`, service namespace `qa-svc`, data
namespace `qa-data` and the shared Flux source on branch `main` at `./k8s/overlays/qa/svc`.

The Go resources are `dashboard-api`, `dashboard-api-management`, `dashboard-config` and the
`dashboard-api` NetworkPolicy after the environment prefix is applied. The protected Java
Deployment and Service are both named `dashboard-api-legacy` after the same prefix.

## Qualify the candidate

Run these read-only or disposable checks from the repository root:

```sh
make go-svc-deploy-check SVC_NAME=dashboard
make go-svc-deploy-test-tooling SVC_NAME=dashboard
make go-svc-deploy-test SVC_NAME=dashboard
```

The disposable test loads the candidate image into the existing configured
`local-dev-insurance-hub` Kind cluster, then creates and removes one uniquely owned namespace. It
also loads the pinned Elasticsearch image, creates a fixture index through an administrator test
identity, and gives Dashboard an exact-index reader with no cluster privileges. It proves total
sales, reader write/admin/unrelated-index denial, readiness and safe response behavior during
dependency loss, independent liveness, recovery, network denial and pod replacement. It does not
create or delete the cluster. A render, fake adapter or disposable pass does not establish a
local-dev or QA deployment result.

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
export DEPLOY_IMAGE_REF=insurance-hub-dashboard-api:1.0.0-local.<12-source-hex>
export DEPLOY_JAVA_DEPLOYMENT_UID=<recorded-local-dev-Java-Deployment-UID>
export DEPLOY_JAVA_SERVICE_UID=<recorded-local-dev-Java-Service-UID>

make go-svc-deploy-preflight SVC_NAME=dashboard
make go-svc-image-load SVC_NAME=dashboard
make go-svc-deploy SVC_NAME=dashboard
make go-svc-deploy-status SVC_NAME=dashboard
make go-svc-deploy-verify SVC_NAME=dashboard
```

Before and after deployment, record the Java Deployment and Service UIDs and availability. Verify
the permitted HTTP total-sales request, direct `GetTotalSales` RPC and management endpoints, then
use labeled and unlabeled probe Pods to establish NetworkPolicy behavior. Confirm that OCI,
runtime, Pod and ConfigMap versions agree; retain the full OCI revision, image tag and immutable
digest separately. Verify exact-index reads, write/admin/unrelated-index denials, readiness during
Elasticsearch loss and independent liveness. No Kafka setting, credential, topic or group belongs
to the Go reader.

## Stop and return in local development

The bounded stop command scales only `local-dev-dashboard-api` to zero:

```sh
make go-svc-stop SVC_NAME=dashboard
```

Confirm that all Go endpoints disappear and that the Java Deployment and Service UIDs and
availability remain unchanged. Restore the same accepted image with the guarded deploy, status and
verify commands above. Leave the Go workload ready after the issue 286 stop scenario.

## Stage and observe QA

PR1 leaves `k8s/overlays/qa/svc/dashboard` inactive. The shared QA aggregate must continue to
contain `./dashboard/legacy` and must not contain `./dashboard`. The release owner later publishes
the reviewed semantic image, records its immutable digest and exact image source revision, and
updates the inactive overlay before a reviewed Git change adds `./dashboard` beside the Java
resource. Flux performs the QA mutation.

Do not apply, delete, load an image, scale a workload, live-patch desired state or suspend Flux in
QA. Read-only status and verification require the full commit reported by the shared `qa-svc` Flux
source and applied revision:

```sh
export DEPLOY_ENV=qa
export DEPLOY_CONTEXT=qa-insurance-hub
export DEPLOY_SVC_NS=qa-svc
export DEPLOY_DATA_NS=qa-data
export DEPLOY_SOURCE_REVISION=<40-flux-source-applied-hex>
export DEPLOY_IMAGE_DIGEST=sha256:<64-hex-registry-digest>
export DEPLOY_JAVA_DEPLOYMENT_UID=<recorded-QA-Java-Deployment-UID>
export DEPLOY_JAVA_SERVICE_UID=<recorded-QA-Java-Service-UID>

make go-svc-deploy-status SVC_NAME=dashboard
make go-svc-deploy-verify SVC_NAME=dashboard
```

`DEPLOY_SOURCE_REVISION` is the Git commit reconciled by the shared Flux owner; it is not the image's
OCI source revision after a later release-preparation or activation commit. Record the published
image source revision separately with its semantic reference and immutable digest. Status and
verification fail when the digest is absent or malformed, when the selected ready Pod inventory
differs, or when any running `dashboard-service` container reports another image ID.

QA acceptance records the Flux source and applied revision, semantic and digest-qualified image,
Pod image ID, runtime identity, labeled and denied network probes, health and total-sales results,
reader permission denials, dependency behavior and Alloy delivery. Configuration or disposable
evidence alone does not establish a QA result.

## Return from QA activation

If QA acceptance must stop, revert only the reviewed `./dashboard` inclusion through Git and let
Flux remove the Go resources. Keep `./dashboard/legacy`, the shared Flux source and all Java,
monitoring and data resources unchanged. Record the source/applied revisions and Java UIDs before
and after the return. A final remove-and-restore rehearsal remains issue 292 work.
