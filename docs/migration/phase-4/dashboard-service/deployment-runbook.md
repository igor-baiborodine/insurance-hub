<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Dashboard Sales Deployment Runbook](#dashboard-sales-deployment-runbook)
  - [Deployment coordinates](#deployment-coordinates)
  - [Qualify the candidate](#qualify-the-candidate)
  - [Provision reader prerequisites](#provision-reader-prerequisites)
  - [Prepare isolated registration resources](#prepare-isolated-registration-resources)
  - [Deploy to local development](#deploy-to-local-development)
  - [Stop and return in local development](#stop-and-return-in-local-development)
  - [Stage and observe QA](#stage-and-observe-qa)
  - [Return from QA activation](#return-from-qa-activation)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Dashboard Sales Deployment Runbook

This runbook owns qualification and guarded operation of the private Go Dashboard queries and its
disabled-by-default isolated registration candidate. The default workload reads `policy_stats`.
The candidate uses `policy_stats_issue_290`, `policy-registered-issue-290` and
`dashboard-service-issue-290`; Java retains canonical writer, setup, edge retry, public route and
event authority.
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
make monitoring-assets-check ENV_NAME=qa SVC_NAME=dashboard
make test-monitoring ENV_NAME=qa SVC_NAME=dashboard
make -C services/dashboard-service test-event-integration
```

The disposable test loads the candidate image plus pinned Elasticsearch and Kafka images into the
existing configured `local-dev-insurance-hub` Kind cluster, then creates and removes one uniquely
owned namespace. It proves the disabled query deployment first. It then enables only the
disposable issue-290 reader/writer/Kafka inputs, verifies exact-index permissions and the accepted
writer mapping-authority limit, checks Kafka CA/hostname TLS behavior, consumes the isolated topic
and proves the canonical topic/group remain untouched. It retains the existing HTTP/RPC,
dependency-loss, liveness, recovery, network-denial and replacement checks. It does not create or
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

## Prepare isolated registration resources

PR1 keeps both shared overlays at `EVENT_WORKERS_ENABLED=false`. The checked-in activation patches
are guarded inputs for the later authorized activation step; the active kustomizations do not
reference them. Disabled renders keep the canonical reader, omit writer credentials and Kafka
trust, and have no Kafka egress.

The event setup owner targets only `policy_stats_issue_290`,
`policy-registered-issue-290` and the future `dashboard-service-issue-290` consumer group. It
creates the index only when absent, adopts compatible empty or populated state without seeding or
rewriting it, and provisions the separate `go_dashboard_issue_290_reader` and
`go_dashboard_issue_290_writer` identities. The writer's native Elasticsearch `index` privilege
can submit compatible mapping updates; this is the accepted issue-290 limit. The shared broker has
no topic/group ACL, so exact configuration and NetworkPolicy are guards rather than broker
authorization proof.

After separately authorizing setup on the selected environment, export the same exact coordinates
used by deployment. For local development:

```sh
export DEPLOY_ENV=local-dev
export DEPLOY_CONTEXT=kind-local-dev-insurance-hub
export DEPLOY_SVC_NS=local-dev-all
export DEPLOY_DATA_NS=local-dev-all
export DASHBOARD_EVENT_CONFIRM=setup-local-dev-dashboard-event-issue-290
make go-svc-event-fixture-setup SVC_NAME=dashboard
unset DASHBOARD_EVENT_CONFIRM
make go-svc-event-fixture-verify SVC_NAME=dashboard
```

For QA, use `qa-insurance-hub`, `qa-svc`, `qa-data` and confirmation
`setup-qa-dashboard-event-issue-290`. Setup and verify fail before effects when coordinates,
existing mappings, roles, Secrets, Kafka cluster/listener, topic ownership or CA material differ.
Normal deployment/status/verification never invokes setup.

Retirement is deliberately separate because issue 290 retains isolated documents and consumer
progress for later handoff and return evidence. When a later ticket explicitly authorizes removal, use
`retire-<environment>-dashboard-event-issue-290` as `DASHBOARD_EVENT_CONFIRM` and run
`make go-svc-event-fixture-retire SVC_NAME=dashboard`. The owner first verifies every present
resource belongs to issue 290, then removes only the exact index, identities, Secrets and topic.
Setup and verification never reset the consumer group; any later group-offset removal needs its own
broker-state check and authorization.

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

Before and after deployment, record the Java Deployment and Service UIDs and availability. Verify
the permitted HTTP total-sales, agent-sales and sales-trends requests, all three direct sales RPCs
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
attempt a write that could mutate canonical state if permissions drift.

## Stop and return in local development

The bounded stop command scales only `local-dev-dashboard-api` to zero:

```sh
make go-svc-stop SVC_NAME=dashboard
```

Confirm that all Go endpoints disappear and that the Java Deployment and Service UIDs and
availability remain unchanged. Restore the same accepted image with the guarded deploy, status and
verify commands above. Leave the Go workload ready after the issue 286 stop scenario.

## Stage and observe QA

The shared QA aggregate contains both `./dashboard` and `./dashboard/legacy`. The release owner
publishes the reviewed semantic image, records its immutable digest and exact image source revision,
and updates the Go overlay through the guarded release workflow. Flux performs the persistent QA
mutation while the Java resource and route remain authoritative.

Do not apply, delete, load an image, scale a workload, live-patch desired state or suspend Flux in
QA. Status is read-only. Verification requires separate authorization for its short-lived probe
Pods and namespace, plus the full commit reported by the shared `qa-svc` Flux source and applied
revision:

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

## Return from QA activation

If QA acceptance must stop, revert only the reviewed `./dashboard` inclusion through Git and let
Flux remove the Go resources. Keep `./dashboard/legacy`, the shared Flux source and all Java,
monitoring and data resources unchanged. Record the source/applied revisions and Java UIDs before
and after the return. A final remove-and-restore rehearsal remains issue 292 work.
