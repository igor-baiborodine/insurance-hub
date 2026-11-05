<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Dashboard Echo Deployment Runbook](#dashboard-echo-deployment-runbook)
  - [Deployment coordinates](#deployment-coordinates)
  - [Qualify the candidate](#qualify-the-candidate)
  - [Deploy to local development](#deploy-to-local-development)
  - [Stop and return in local development](#stop-and-return-in-local-development)
  - [Stage and observe QA](#stage-and-observe-qa)
  - [Return from QA activation](#return-from-qa-activation)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Dashboard Echo Deployment Runbook

This runbook owns the temporary Go Dashboard Echo deployment introduced by issue 286. The Go
workload runs beside the Java Dashboard and has no data, event, public route or customer-data
authority. Issue 291 completes the service operating handoff. Issue 292 owns the later full
removal and restoration rehearsal.

## Deployment coordinates

Use catalog service `dashboard` only. Local development uses context
`kind-local-dev-insurance-hub`, namespace `local-dev-all` and Kind cluster
`local-dev-insurance-hub`. QA uses context `qa-insurance-hub`, namespace `qa-svc` and the shared
Flux source on branch `main` at `./k8s/overlays/qa/svc`.

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

The disposable test creates and removes its own Kind cluster and namespace. A render or fake
adapter pass does not establish live CNI behavior.

## Deploy to local development

Build and validate the image first. Record its full OCI revision, local reference and image ID.
The reference must end in the first 12 hexadecimal characters of that revision.

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

make go-svc-deploy-preflight SVC_NAME=dashboard
make go-svc-image-load SVC_NAME=dashboard
make go-svc-deploy SVC_NAME=dashboard
make go-svc-deploy-status SVC_NAME=dashboard
make go-svc-deploy-verify SVC_NAME=dashboard
```

Before and after deployment, record the Java Deployment and Service UIDs and availability. Verify
the permitted HTTP Echo, direct gRPC Echo and management endpoints, then use labeled and unlabeled
probe Pods to establish NetworkPolicy behavior. Confirm that runtime, Pod, ConfigMap and image
version identities agree and that no Elasticsearch or Kafka setting, credential, connection,
topic, group or index exists.

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
the reviewed semantic image, records its immutable digest and exact source revision, and updates
the inactive overlay before a reviewed Git change adds `./dashboard` beside the Java resource.
Flux performs the QA mutation.

Do not apply, delete, load an image, scale a workload, live-patch desired state or suspend Flux in
QA. Read-only status and verification require the exact published source revision:

```sh
export DEPLOY_ENV=qa
export DEPLOY_CONTEXT=qa-insurance-hub
export DEPLOY_SVC_NS=qa-svc
export DEPLOY_DATA_NS=qa-data
export DEPLOY_SOURCE_REVISION=<40-source-hex>
export DEPLOY_IMAGE_DIGEST=sha256:<64-hex-registry-digest>

make go-svc-deploy-status SVC_NAME=dashboard
make go-svc-deploy-verify SVC_NAME=dashboard
```

Use the immutable digest recorded by the reviewed release. Status and verification fail when the
digest is absent or malformed, when the selected ready Pod inventory differs, or when any running
`dashboard-service` container reports another image ID.

QA acceptance records the Flux source and applied revision, semantic and digest-qualified image,
Pod image ID, runtime identity, labeled and denied network probes, health and Echo results, and
Alloy delivery. Configuration or disposable evidence alone does not establish a QA result.

## Return from QA activation

If QA acceptance must stop, revert only the reviewed `./dashboard` inclusion through Git and let
Flux remove the Go resources. Keep `./dashboard/legacy`, the shared Flux source and all Java,
monitoring and data resources unchanged. Record the source/applied revisions and Java UIDs before
and after the return. A final remove-and-restore rehearsal remains issue 292 work.
