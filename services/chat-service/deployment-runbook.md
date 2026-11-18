<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Chat deployment and private qualification runbook](#chat-deployment-and-private-qualification-runbook)
  - [Exact targets and ownership](#exact-targets-and-ownership)
  - [Source and disposable qualification](#source-and-disposable-qualification)
  - [Candidate identity gate](#candidate-identity-gate)
  - [Deploy and observe local-dev](#deploy-and-observe-local-dev)
  - [Observe QA](#observe-qa)
  - [Run private real-client qualification through Nginx](#run-private-real-client-qualification-through-nginx)
  - [Stop conditions, cleanup and recovery](#stop-conditions-cleanup-and-recovery)
  - [Evidence checklist](#evidence-checklist)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Chat deployment and private qualification runbook

This runbook owns deployment preparation and loopback-only real-client qualification through the
unchanged Nginx proxy for the Go Chat candidate. Java remains the deployed Vue Chat upstream
throughout issue 390. Image publication, shared-environment mutation, port-forwarding and private
qualification each require their own authorization; source checks do not grant it. Browser tests
and manual browser observation are explicitly forfeited.

Use the companion [observability runbook](observability-runbook.md) for monitoring provisioning,
signal verification and investigation.

## Exact targets and ownership

| Environment | Mode | Context | Namespace | Go service | Public Java service |
| --- | --- | --- | --- | --- | --- |
| local-dev | direct | `kind-local-dev-insurance-hub` | `local-dev-all` | `local-dev-chat-api` | `local-dev-chat-api-legacy` |
| QA | Flux | `qa-insurance-hub` | `qa-svc` | `qa-chat-api` | `qa-chat-api-legacy` |

Both Go renders require one replica and `Recreate`. The private qualification owner reads the exact
Go Deployment, Pod image ID, runtime ConfigMap and deployed Vue ConfigMap before starting local
processes. It rejects a wrong context, namespace, service, source revision, digest, runtime version,
Pod inventory, replacement strategy or public Vue route.

## Source and disposable qualification

Run from the repository root before any live operation:

```sh
make -C services/chat-service check FORMAT_SCOPE=all
make go-svc-image-check SVC_NAME=chat
make go-svc-deploy-check SVC_NAME=chat
make go-svc-deploy-test-tooling SVC_NAME=chat
make go-svc-deploy-smoke SVC_NAME=chat
```

The smoke owner creates and removes a uniquely owned namespace in the configured local Kind
cluster. It does not mutate `local-dev-all` or QA and does not prove either shared target.

## Candidate identity gate

Record the accepted full source revision, `git-<first-12>` version, mutable image reference,
digest-qualified reference and `sha256:` registry digest. Reject zero, placeholder, development,
tag-only or mismatched values. The generic release must show the same full revision and version in
OCI labels before either environment is qualified.

Bind one environment exactly. For local-dev:

```sh
export QUALIFICATION_ENV=local-dev
export QUALIFICATION_CONTEXT=kind-local-dev-insurance-hub
export QUALIFICATION_NAMESPACE=local-dev-all
export QUALIFICATION_SERVICE=local-dev-chat-api
: "${QUALIFICATION_SOURCE_REVISION:?set the accepted full 40-character source revision}"
: "${QUALIFICATION_IMAGE_DIGEST:?set the accepted sha256 registry digest}"
```

For QA:

```sh
export QUALIFICATION_ENV=qa
export QUALIFICATION_CONTEXT=qa-insurance-hub
export QUALIFICATION_NAMESPACE=qa-svc
export QUALIFICATION_SERVICE=qa-chat-api
: "${QUALIFICATION_SOURCE_REVISION:?set the accepted full 40-character source revision}"
: "${QUALIFICATION_IMAGE_DIGEST:?set the accepted sha256 registry digest}"
```

With separately authorized target read access, run the identity preflight:

```sh
make -C services/chat-service qualification-preflight
```

The JSON result records only safe identities. In QA, separately record the full Flux source and
applied revisions and require them to identify the accepted release. The preflight does not publish,
deploy, reconcile, apply monitoring or start a forward.

## Deploy and observe local-dev

Use the generic guarded direct-deployment sequence only with separate local-dev mutation authority:

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

`go-svc-stop SVC_NAME=chat` scales only `local-dev-chat-api` to zero. It does not remove Java,
the namespace or repository assets. The predecessor input is accepted only during the guarded
preflight/load/deploy sequence, must be an exact source-derived local Chat image and never weakens
final status or verification. Use the captured predecessor and the same guarded sequence to recover
it after an unsuccessful qualification.

## Observe QA

QA desired state is Flux-owned. Do not run image-load, deploy or stop against QA and do not patch
the Deployment. After the generic release activates `./chat`, bind the exact QA coordinates, full
Flux revision and accepted image digest, then use separately authorized status and verification:

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

Repair QA through reviewed Git and Flux. Never live-patch the workload or public Vue route.

## Run private real-client qualification through Nginx

Build the unchanged checked-in Vue/Nginx image before the authorized session and record its local
image ID. The qualification workstation also requires `socat`; it creates only an owned Unix
socket that relays to the loopback Kubernetes forward:

```sh
make docker-frontend-build
docker image inspect insurance-hub-web-vue-legacy:latest \
  --format '{{.Id}}'
command -v socat
```

Start the one-shot bounded owner only after `qualification-preflight` passes:

```sh
export CHAT_QUALIFICATION_CONFIRM="run-${QUALIFICATION_ENV}-chat-private-qualification"
make -C services/chat-service qualification-run
unset CHAT_QUALIFICATION_CONFIRM
```

The owner binds the Nginx container and Kubernetes forward only to `127.0.0.1`. It uses the
unchanged checked-in Nginx template and an owned temporary Unix-socket relay so the container can
reach the loopback-only forward without exposing it on a Docker bridge interface. The disposable
container resolves the template's cluster DNS name only to local loopback; the numeric and Unix
qualification upstreams do not use that resolver. The owner runs `verify-live-java-parity` against
the loopback Nginx endpoint. That command uses real WebSocket clients to verify exact join,
unchanged/empty message and disconnect recipients and bytes against the independent checked-in Java
fixture. A mismatch fails qualification without printing payload contents.

The owner prints safe candidate identities and a passed parity marker only after the real-client
comparison succeeds. It removes its exact Docker container, Unix-socket relay, temporary directory
and port-forward after success, failure, timeout, SIGINT or SIGTERM. Interruption or cleanup failure
returns a non-success status. Do not add browser automation, request a manual browser session or
record browser-test evidence.

## Stop conditions, cleanup and recovery

Stop before starting local processes when any target coordinate, source/version/digest, Pod image
ID, one-Pod inventory, `Recreate` strategy, Go readiness, Flux identity or public Java route differs.
Stop during the scenario for wrong payload/recipient behavior, missing telemetry, a child exit,
real-client/Nginx verification failure or incomplete cleanup.

After every attempt, require the exact qualification container and Unix socket to be absent and
verify that the reported loopback ports no longer accept connections. Do not kill broad process
groups or remove another container. If cleanup reports a failure, preserve that failure, inspect
only the recorded container name/process, clean it explicitly with separate authority and rerun
preflight before a new attempt.

For local-dev recovery, restore the captured accepted image through preflight/load/deploy/status/
verify. For QA, revert or repair reviewed desired state and allow Flux to reconcile. Never remove
Java resources or automate Java-to-Go-to-Java rehearsal.

## Evidence checklist

Retain no credentials or message payloads. Record:

- intended base/head, full source revision and `git-<12>` version;
- mutable and digest-qualified image references, OCI revision/version and registry digest;
- environment, context, namespace, Go Service and unchanged public Java `CHAT_URL`;
- Flux source/applied revisions in QA;
- Deployment UID, one ready Pod UID, actual Pod image ID and runtime service version/instance;
- local unchanged Nginx image ID, loopback Nginx endpoint, owner start/end times and cleanup result;
- `verify-live-java-parity` result covering join/message/empty-message/disconnect recipients and
  bytes against the immutable Java fixture;
- service/observability check results and any stop/recovery action.

The automated real-client/Nginx result supports CHAT-C09 or CHAT-C10 only when all identity and
cleanup fields are present. Browser-test evidence is neither required nor accepted. Qualification
does not transfer the public route or authorize Java retirement.
