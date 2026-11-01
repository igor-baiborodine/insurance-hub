<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Policy Search Go deployment runbook](#policy-search-go-deployment-runbook)
  - [Ownership and exposure](#ownership-and-exposure)
  - [Repository validation](#repository-validation)
  - [Select local-dev](#select-local-dev)
  - [Deploy and verify local-dev](#deploy-and-verify-local-dev)
  - [Diagnostics and recovery](#diagnostics-and-recovery)
  - [Stop, remove and restore local-dev](#stop-remove-and-restore-local-dev)
  - [Stage and activate QA](#stage-and-activate-qa)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Policy Search Go deployment runbook

## Ownership and exposure

Policy Search runs beside the legacy Java workload. The Go owner creates only
`<environment>-policy-search-api`, its business and management Services, ConfigMap and
NetworkPolicy. It does not modify the Java `policy-search-api-legacy` Deployment or Service, create
an Ingress, initialize data, or connect to Elasticsearch or Kafka.

Business ports are reachable only from same-namespace Pods labeled
`insurance-hub.io/policy-search-business-client=true`. Management port 8081 requires
`insurance-hub.io/policy-search-management-client=true`; kubelet probe traffic follows Kubernetes
node-origin semantics. Local-dev denies Pod egress. QA permits only kube-system DNS and OTLP/gRPC
to the selected Alloy Pods in `qa-monitoring`.

## Repository validation

Run non-mutating checks from the repository root:

```sh
make go-svc-image-check SVC_NAME=policy-search
make go-svc-deploy-check SVC_NAME=policy-search
make go-svc-deploy-test-tooling SVC_NAME=policy-search
make go-svc-release-test
make go-svc-dispatch-test
```

`deploy-check` renders local-dev and QA, checks workload security, resources, probes, runtime
budgets, service exposure, telemetry and NetworkPolicy, and confirms the Go QA overlay is absent
from the shared QA root. Rendering and fake-adapter tests do not prove admission, enforcement or
signal delivery. Use `go-svc-deploy-test` after its disposable owner is registered.

## Select local-dev

Build the exact current source before selecting the target:

```sh
make go-svc-image-check SVC_NAME=policy-search

export DEPLOY_ENV=local-dev
export DEPLOY_CONTEXT=kind-local-dev-insurance-hub
export DEPLOY_SVC_NS=local-dev-all
export DEPLOY_DATA_NS=local-dev-all
export DEPLOY_IMAGE_REF="insurance-hub-policy-search-api:1.0.0-local.$(git rev-parse --short=12 HEAD)"
```

The deployment owner rejects missing or different coordinates before cluster mutation. It also
requires the local image's OCI revision to match its `1.0.0-local.<12-hex>` tag. It injects that
exact image and the image's `git-<12-hex>` source version into the rendered local manifest; the
checked-in zero tag cannot be deployed directly.
Telemetry is disabled locally and requires no collector.

## Deploy and verify local-dev

Run the guarded sequence:

```sh
make go-svc-deploy-preflight SVC_NAME=policy-search
make go-svc-image-load SVC_NAME=policy-search
make go-svc-deploy SVC_NAME=policy-search
make go-svc-deploy-status SVC_NAME=policy-search
make go-svc-deploy-verify SVC_NAME=policy-search
```

Preflight checks target identity, Java resource UIDs, image/source identity and a server-side dry
run before writes. Deploy applies only the rendered Go resources and waits up to the workload's
180-second progress deadline. Verify checks the live inventory, absence of public routes, health
and Echo over the business Service while confirming Java UIDs remain unchanged.

For independent in-cluster reachability, create short-lived probe Pods in `local-dev-all`. Label the
allowed business probe and management probe with the exact labels above. An otherwise identical
unlabeled Pod must time out on ports 8080, 9090 and 8081. Delete only those named probe Pods after
recording their results.

## Diagnostics and recovery

Inspect only the selected resources:

```sh
kubectl --context "$DEPLOY_CONTEXT" -n "$DEPLOY_SVC_NS" \
  get deployment,service,configmap,networkpolicy,pod \
  -l app.kubernetes.io/name=policy-search-api -o wide
kubectl --context "$DEPLOY_CONTEXT" -n "$DEPLOY_SVC_NS" \
  logs deployment/local-dev-policy-search-api --tail=200
kubectl --context "$DEPLOY_CONTEXT" -n "$DEPLOY_SVC_NS" \
  describe deployment/local-dev-policy-search-api
```

An interrupted or failed apply may leave reviewed Go resources present. Inspect them and rerun the
same deploy command with the same source image; server-side apply and rollout are idempotent. A
source/image mismatch requires rebuilding from the intended commit. Do not patch live selectors,
disable NetworkPolicy, scale Java, or substitute a mutable image tag. Output and evidence must not
contain credentials or request payloads; this increment accepts no deployment Secret.

## Stop, remove and restore local-dev

The normal stop owner scales only the Go Deployment to zero and confirms Java UIDs are unchanged:

```sh
make go-svc-stop SVC_NAME=policy-search
```

Verify that the Go endpoints disappear while the Deployment is scaled to zero, then rerun
`go-svc-deploy` and `go-svc-deploy-verify` to restore the accepted release. Compare Java UIDs before
and after both operations. Leave the verified Go release running when acceptance requires a usable
handoff.

## Stage and activate QA

The QA overlay publishes
`ghcr.io/igor-baiborodine/insurance-hub/policy-search-api:<semantic-version>`. The generic release
owner updates both the image tag and `SERVICE_VERSION` for a service without initialization. PR1
keeps the overlay inactive; a later reviewed activation adds exactly `./policy-search` to
`k8s/overlays/qa/svc/kustomization.yaml` after the immutable image is published and QA prerequisites
are verified.

Direct `image-load`, `deploy` and `stop` operations reject QA. Use the shared `qa-svc` Flux owner,
observe its source revision and reconciliation, and verify the exact image digest, Pod identity,
Alloy signals and NetworkPolicy from the QA target. Removal and restoration require separate Git
revisions so Flux can observe both states. Do not add an initialization Job or a service-specific
Flux source.
