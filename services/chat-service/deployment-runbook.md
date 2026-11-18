<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Chat deployment runbook](#chat-deployment-runbook)
  - [Targets](#targets)
  - [Validate source](#validate-source)
  - [Deploy local-dev](#deploy-local-dev)
  - [Observe QA](#observe-qa)
  - [Failure and recovery](#failure-and-recovery)
  - [Evidence](#evidence)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Chat deployment runbook

This runbook covers normal Go Chat validation, local deployment, QA observation, recovery and
evidence. Image publication, shared-environment access and mutations require separate authorization.
Use the companion [observability runbook](observability-runbook.md) for monitoring operations.

## Targets

| Environment | Mode | Context | Namespace | Service |
| --- | --- | --- | --- | --- |
| local-dev | direct | `kind-local-dev-insurance-hub` | `local-dev-all` | `local-dev-chat-api` |
| QA | Flux | `qa-insurance-hub` | `qa-svc` | `qa-chat-api` |

Both renders require one replica and the `Recreate` strategy.

## Validate source

Run the focused owners from the repository root:

```sh
make -C services/chat-service check FORMAT_SCOPE=all
make go-svc-image-check SVC_NAME=chat
make go-svc-deploy-check SVC_NAME=chat
make go-svc-deploy-test-tooling SVC_NAME=chat
make go-svc-deploy-smoke SVC_NAME=chat
```

The smoke target owns and removes its disposable Kind namespace. It does not mutate `local-dev-all`
or QA.

Record the accepted full source revision, `git-<first-12>` version, digest-qualified image reference
and `sha256:` registry digest. Reject placeholder, tag-only or mismatched values. OCI labels, runtime
configuration and the running image must identify the same accepted source.

## Deploy local-dev

Use the guarded direct-deployment sequence only with local-dev mutation authority:

```sh
export DEPLOY_ENV=local-dev
export DEPLOY_CONTEXT=kind-local-dev-insurance-hub
export DEPLOY_SVC_NS=local-dev-all
export DEPLOY_DATA_NS=local-dev-all
: "${DEPLOY_IMAGE_REF:?set the exact accepted source-derived image reference}"
export DEPLOY_PREVIOUS_IMAGE_REF="$(kubectl --context "$DEPLOY_CONTEXT" \
  -n "$DEPLOY_SVC_NS" get deployment/local-dev-chat-api \
  -o jsonpath='{.spec.template.spec.containers[0].image}')"
make go-svc-deploy-preflight SVC_NAME=chat
make go-svc-image-load SVC_NAME=chat
make go-svc-deploy SVC_NAME=chat
unset DEPLOY_PREVIOUS_IMAGE_REF
make go-svc-deploy-status SVC_NAME=chat
make go-svc-deploy-verify SVC_NAME=chat
```

`go-svc-stop SVC_NAME=chat` scales the Go Deployment to zero. It does not remove the namespace or
repository assets.

## Observe QA

QA desired state is Flux-owned. Do not run image-load, deploy or stop against QA, and do not patch
the Deployment. After the reviewed release is reconciled, use authorized status and verification:

```sh
export DEPLOY_ENV=qa
export DEPLOY_CONTEXT=qa-insurance-hub
export DEPLOY_SVC_NS=qa-svc
export DEPLOY_DATA_NS=qa-data
: "${DEPLOY_SOURCE_REVISION:?set the full applied Flux revision}"
: "${DEPLOY_IMAGE_DIGEST:?set the accepted sha256 registry digest}"
make go-svc-deploy-status SVC_NAME=chat
make go-svc-deploy-verify SVC_NAME=chat
```

Repair QA through reviewed Git and Flux. Never live-patch the workload or public route.

## Failure and recovery

Stop for a wrong target, source, version, digest, Pod inventory, strategy, readiness state or public
route. Preserve the first failure before recovery.

For local-dev, restore the captured accepted image through the same preflight, load, deploy, status
and verify targets. For QA, repair or revert reviewed desired state and allow Flux to reconcile.
Do not broaden a recovery action beyond Chat-owned resources.

## Evidence

Record only safe operational data:

- intended source revision and `git-<12>` version;
- mutable and digest-qualified image references, OCI identity and registry digest;
- environment, context, namespace and Service;
- Flux source and applied revisions for QA;
- Deployment UID, ready Pod UID, running image identity and runtime service version; and
- validation, deployment, verification, recovery and cleanup results.

Do not record credentials or message payloads. Report blocked or unavailable live evidence honestly.
