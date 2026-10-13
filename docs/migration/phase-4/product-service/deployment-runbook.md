<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Product Go deployment runbook](#product-go-deployment-runbook)
  - [Target inputs](#target-inputs)
  - [Read-only checks](#read-only-checks)
  - [Local-dev apply and stop](#local-dev-apply-and-stop)
  - [QA ownership](#qa-ownership)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Product Go deployment runbook

This runbook records the guarded operations introduced for issue #134. The Go Product workload
shares the existing Product catalog with Java. Java remains the gateway destination and catalog
owner. The complete release, rotation recovery, and environment acceptance procedure will be
filled in during the later delivery steps; this file does not record a live deployment.

## Target inputs

Run all commands from the repository root. Select one environment and confirm the active
Kubernetes context and namespace before running a target. The commands refuse a different active
context or namespace; they do not switch either one.

| Input | local-dev | QA |
| --- | --- | --- |
| `DEPLOY_ENV` | `local-dev` | `qa` |
| `DEPLOY_CONTEXT` | `kind-local-dev-insurance-hub` | `qa-insurance-hub` |
| `DEPLOY_SVC_NS` | `local-dev-all` | `qa-svc` |
| `DEPLOY_DATA_NS` | `local-dev-all` | `qa-data` |
| `DEPLOY_IMAGE_REF` | Exact image in the local-dev Go overlay | Exact digest reference in the QA Go overlay after activation |
| `DEPLOY_SOURCE_REVISION` | Not used | Exact 40-character `main` commit reconciled by both Flux source and `qa-svc` |

Set `SVC_NAME=product` on every `go-svc-*` target. `DEPLOY_IMAGE_REF` must match the rendered
Deployment image exactly. The local image tag must identify the image's OCI source revision;
QA requires `ghcr.io/igor-baiborodine/insurance-hub/product-api@sha256:<digest>`.
The QA overlay is currently disconnected from the Flux aggregate and has an unpublished
placeholder, so QA deployment operations cannot pass until the reviewed activation change.

## Read-only checks

`make go-svc-deploy-check SVC_NAME=product` renders both Go overlays, the Java overlays, and a
disconnected QA activation preview without cluster access. `make go-svc-deploy-preflight
SVC_NAME=product` checks the selected live target, image, reader Secret key names, Java workload,
selectors, Services, Pods, EndpointSlices, gateway environment and checked-in Java destination,
Flux ownership in QA, and route exposure. `make go-svc-deploy-status SVC_NAME=product` requires
the Go Deployment and a ready business endpoint. These targets do not provision the reader or
apply manifests. They do not print the Secret payload.

`make go-svc-deploy-verify SVC_NAME=product` checks effective resources and routes, captures a
sanitized catalog/schema identity, invokes the existing Product reader role verifier and its
rollback-safe SQL probes, then runs the HTTP/gRPC/management security verifier through three
task-owned loopback port-forwards. It closes those forwards on success and failure and requires
the identity to remain unchanged. The checked-in gateway destination and live Java Service are
observed independently; later acceptance steps also probe the actual gateway route.

## Local-dev apply and stop

Provision and verify the dedicated reader role and workload Secret through the separate
`postgres-svc-role-* SVC_NAME=product ROLE_PROFILE=reader` interface before applying Go. With
all target inputs above set, run these commands in order:

```sh
make go-svc-deploy-check SVC_NAME=product
make go-svc-deploy-preflight SVC_NAME=product
make go-svc-image-load SVC_NAME=product
make go-svc-deploy SVC_NAME=product
make go-svc-deploy-status SVC_NAME=product
make go-svc-deploy-verify SVC_NAME=product
```

The apply uses the selected local overlay, server dry-run, and a bounded Deployment rollout. It
compares sanitized catalog/schema snapshots before and after. A planned local image change must
also set `DEPLOY_PREVIOUS_IMAGE_REF` to the observed current Deployment image; unexpected image
drift blocks apply. A planned local ConfigMap change must set `DEPLOY_PREVIOUS_CONFIG_MAP` to the
observed current generated ConfigMap name. `make go-svc-stop SVC_NAME=product` scales only the local Go Deployment to
zero and checks the catalog/schema identity. Reapplying the overlay restores one Go replica.
Neither command deletes the Java workload, Product database, or reader Secret.

## QA ownership

The `qa-svc` Flux Kustomization owns QA workloads from `main` with prune and wait enabled. The
guarded interface checks its source and applied revision. `go-svc-image-load`, `go-svc-deploy`,
and `go-svc-stop` reject QA before cluster access. QA stop, restoration, image changes, and
configuration changes require reviewed Git desired-state changes. Do not apply the QA Go overlay
directly or suspend Flux as a shortcut. Record the actual source revision, pinned image digest,
pod image IDs, ready endpoints, Java route observations, and before/after catalog/schema identity
in the issue acceptance record.
