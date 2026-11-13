<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Document foundation deployment runbook](#document-foundation-deployment-runbook)
  - [Source and candidate checks](#source-and-candidate-checks)
  - [Qualified foundation candidate](#qualified-foundation-candidate)
  - [Local development](#local-development)
  - [QA release and verification](#qa-release-and-verification)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Document foundation deployment runbook

The private Go foundation uses catalog key `document`. Local development is directly owned at
context `kind-local-dev-insurance-hub`, namespace `local-dev-all`; QA is reconciled by
`GitRepository/insurance-hub` and `Kustomization/qa-svc` in `flux-system` into namespace `qa-svc`.
The Go Deployment is `<environment>-document-api`, its only Service is
`<environment>-document-api-management`, and its NetworkPolicy denies workload ingress. The
separate `<environment>-document-api-legacy` resources remain active and authoritative.

## Source and candidate checks

From the repository root, use the registered owners:

```sh
make go-svc-deploy-render SVC_NAME=document DEPLOY_ENV=local-dev
make go-svc-deploy-render SVC_NAME=document DEPLOY_ENV=qa
make go-svc-deploy-check SVC_NAME=document
make go-svc-deploy-test-tooling SVC_NAME=document
make go-svc-image-check SVC_NAME=document
make go-svc-deploy-smoke SVC_NAME=document
```

The disposable smoke creates and removes one labeled namespace in the configured local Kind
cluster. It loads the exact source-derived image, checks health and the temporary Echo through a
Kubernetes API port-forward, and proves an untrusted Pod cannot reach management. It does not
create a cluster, business data, credentials or backend resources.

## Qualified foundation candidate

The foundation candidate from source
`2f0bc5dd8bb7db84f382ad443a7d630f42d4e715` was built as
`insurance-hub-document-api:1.0.0-local.2f0bc5dd8bb7` with OCI version
`git-2f0bc5dd8bb7` and local image ID
`sha256:0c0705b885678fae50374751242d390b1686f66c1d24ebc5b5f57ae0fcf64058`.
The registered image check passed its non-root runtime, identity, liveness and bounded termination
contract. The disposable smoke passed rollout, Pod/config identity, health, temporary Echo and
untrusted-Pod denial in `kind-local-dev-insurance-hub`, then removed its owned namespace. This is
candidate qualification; the ordinary `local-dev-all` and Flux-owned QA observations remain the
Step-5 acceptance boundary.

## Local development

Keep the following coordinates and exact candidate identity in one shell. These operations load
the image, apply only the four Go resources, wait for rollout and verify private access:

```sh
export DEPLOY_ENV=local-dev
export DEPLOY_CONTEXT=kind-local-dev-insurance-hub
export DEPLOY_SVC_NS=local-dev-all
export DEPLOY_DATA_NS=local-dev-all
export DEPLOY_IMAGE_REF="insurance-hub-document-api:1.0.0-local.$(git rev-parse --short=12 HEAD)"

make go-svc-deploy-preflight SVC_NAME=document
make go-svc-image-load SVC_NAME=document
make go-svc-deploy SVC_NAME=document
make go-svc-deploy-verify SVC_NAME=document
```

`deploy`, `image-load` and `stop` reject QA. The bounded stop owner scales only
`local-dev-document-api` to zero and waits up to 90 seconds for the Deployment to report no running
replicas:

```sh
make go-svc-stop SVC_NAME=document
```

`go-svc-deploy-status` deliberately rejects this stopped state because it verifies the ordinary
ready workload and configuration; that failure does not mean the bounded stop failed. Recover only
the Go foundation with the same accepted image and the standard guarded sequence:

```sh
make go-svc-deploy-preflight SVC_NAME=document
make go-svc-image-load SVC_NAME=document
make go-svc-deploy SVC_NAME=document
make go-svc-deploy-status SVC_NAME=document
make go-svc-deploy-verify SVC_NAME=document
```

Recovery must return one ready `local-dev-document-api` Pod with the accepted image/version while
`local-dev-document-api-legacy` remains available. A different context, namespace, image or live
inventory is a stop condition; do not scale or patch shared or Java resources.

## QA release and verification

The source overlay remains absent from `k8s/overlays/qa/svc/kustomization.yaml` until publication.
The main-branch `.github/workflows/go-release-svc.yml` owner builds and pushes the image, resolves
its registry digest, then `go-svc-release-prepare` updates the semantic tag, source-derived runtime
version and adds `./document` to the shared QA Kustomization in one release commit. Do not apply the
QA overlay directly.

After Flux reports the shared source and `qa-svc` owner Ready at the release commit, supply the
Flux-applied revision, immutable image digest and exact coordinates for read-only status and the
bounded verification fixture:

```sh
export DEPLOY_ENV=qa
export DEPLOY_CONTEXT=qa-insurance-hub
export DEPLOY_SVC_NS=qa-svc
export DEPLOY_DATA_NS=qa-svc
export DEPLOY_SOURCE_REVISION='FULL_FLUX_APPLIED_COMMIT'
export DEPLOY_IMAGE_DIGEST='sha256:64_LOWERCASE_HEX'

make go-svc-deploy-status SVC_NAME=document
make go-svc-deploy-verify SVC_NAME=document
```

Record the image source commit separately from the later Flux release commit. Stop if the selected
context, namespaces, source revision, digest, Pod image ID, runtime version, rendered inventory or
legacy separation differs. Recovery uses a reviewed release commit or Flux reconciliation; never
patch QA, suspend the shared owner or remove Java resources.
