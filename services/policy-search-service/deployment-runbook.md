<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Policy Search Go deployment runbook](#policy-search-go-deployment-runbook)
  - [Ownership and exposure](#ownership-and-exposure)
  - [Repository validation](#repository-validation)
  - [Select the environment and target](#select-the-environment-and-target)
  - [Provision and verify dependency resources](#provision-and-verify-dependency-resources)
  - [Deploy or reconcile the release](#deploy-or-reconcile-the-release)
  - [Verify the selected deployment](#verify-the-selected-deployment)
  - [Diagnostics and stop conditions](#diagnostics-and-stop-conditions)
  - [Rollback and restoration](#rollback-and-restoration)
  - [Retire service-owned dependency resources](#retire-service-owned-dependency-resources)
  - [Evidence checklist](#evidence-checklist)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Policy Search Go deployment runbook

This runbook owns Policy Search dependency provisioning, release verification, diagnostics,
application rollback and resource retirement for QA and local-dev. QA supplies the concrete
examples because it is the shared acceptance environment. Each environment-sensitive section
explains the local-dev substitution, including direct deployment without Flux. Use the
[observability runbook](observability-runbook.md) for QA monitoring validation, rule/dashboard
provisioning, Grafana investigation and monitoring rollback. Run commands from the repository root.

The exact fixture, topic, group and confirmation identifiers in this document are source-owned
runtime coordinates. Some retain historical issue-number suffixes because changing them would
change deployed state. They are not ticket references and must not be shortened or normalized in
commands.

## Ownership and exposure

Policy Search runs beside the legacy Java workload. The Go owner creates only
`<environment>-policy-search-api`, its business and management Services, ConfigMap and
NetworkPolicy. The
query fixture owner creates only `policy-views-issue-229`, native role/user
`go_policy_search_reader` and workload-owned Elasticsearch Secrets. The event fixture owner
creates only `policy-registered-issue-230`, `policy-terminated-issue-230`,
`go_policy_search_writer`, its workload Secret and a cert-only Kafka CA Secret. Neither owner
modifies the Java `policy-search-api-legacy` Deployment or Service, creates an Ingress, or accesses
shared application indices.

Business ports are reachable only from same-namespace Pods labeled
`insurance-hub.io/policy-search-business-client=true`. Management port 8081 requires
`insurance-hub.io/policy-search-management-client=true`; kubelet probe traffic follows Kubernetes
node-origin semantics. Egress permits cluster DNS and the selected environment's ECK Elasticsearch
service. When event workers are enabled, it also permits the selected Kafka bootstrap service. QA
adds OTLP/gRPC egress to the selected Alloy Pods in `qa-monitoring`; local-dev telemetry is disabled
and has no collector dependency.

The active QA render enables both event workers and references only the separately owned query and
event inputs. The checked-in local-dev render keeps workers disabled by default and references no
event resources. In either environment, disabled mode must remove the writer and Kafka Secret
references, Kafka CA mount, Kafka egress and consumer authority as one reviewed state.

## Repository validation

Run the non-mutating owners before proposing a release:

```sh
make go-svc-image-check SVC_NAME=policy-search
make go-svc-deploy-check SVC_NAME=policy-search
make go-svc-deploy-test-tooling SVC_NAME=policy-search
make go-svc-deploy-test SVC_NAME=policy-search
make go-svc-release-test
make go-svc-dispatch-test
```

`deploy-check` renders local-dev and QA and checks workload security, resources, probes, runtime
budgets, exact Secret/trust references, service exposure, telemetry and NetworkPolicy. The
disposable deployment test verifies the Elasticsearch reader and writer permission boundaries,
TLS Kafka integration, query and event behavior, readiness recovery, restart and restoration in an
isolated Kind namespace. Rendering, fake adapters and disposable tests do not prove QA admission,
Flux reconciliation or signal delivery.

## Select the environment and target

Use a clean checkout of the exact reviewed and merged source for QA. Record the immutable registry
digest before contacting the cluster:

```sh
export DEPLOY_ENV=qa
export DEPLOY_CONTEXT=qa-insurance-hub
export DEPLOY_SVC_NS=qa-svc
export DEPLOY_DATA_NS=qa-data
export DEPLOY_SOURCE_REVISION="$(git rev-parse HEAD)"
export DEPLOY_IMAGE_DIGEST='sha256:<64-hex-registry-digest>'

test -z "$(git status --porcelain)"
test "$(kubectl config current-context)" = "$DEPLOY_CONTEXT"
test "${#DEPLOY_SOURCE_REVISION}" -eq 40
```

Replace the digest placeholder with the published image digest. Do not derive it from a mutable
tag. The deployment owners reject missing or different coordinates, a source mismatch, a running
Pod whose `imageID` differs from `DEPLOY_IMAGE_DIGEST`, and a target outside the service catalog.

Before any mutation, capture the current GitRepository and Kustomization revisions, Deployment
generation, image tag/digest/ID, ConfigMap identity, Pod UIDs and ready replicas:

```sh
kubectl --context "$DEPLOY_CONTEXT" -n flux-system get \
  gitrepository/insurance-hub kustomization/qa-svc
kubectl --context "$DEPLOY_CONTEXT" -n "$DEPLOY_SVC_NS" get \
  deployment/qa-policy-search-api -o json
```

For QA, stop if the checkout is not the reviewed merge, Flux source and applied revisions differ,
the current workload identity is unknown, dependencies are unhealthy, or rollback coordinates
have not been recorded.

For local-dev, select the Kind context and the shared `local-dev-all` namespace, build from the
current source, and use the source-derived local image identity:

```sh
make go-svc-image-check SVC_NAME=policy-search

export DEPLOY_ENV=local-dev
export DEPLOY_CONTEXT=kind-local-dev-insurance-hub
export DEPLOY_SVC_NS=local-dev-all
export DEPLOY_DATA_NS=local-dev-all
export DEPLOY_IMAGE_REF="insurance-hub-policy-search-api:1.0.0-local.$(git rev-parse --short=12 HEAD)"
unset DEPLOY_SOURCE_REVISION DEPLOY_IMAGE_DIGEST

test "$(kubectl config current-context)" = "$DEPLOY_CONTEXT"
```

Local-dev has no Flux source or Kustomization to inspect. Capture
`Deployment/local-dev-policy-search-api`, its current image and Pod identities directly. The owner
requires the local image's OCI source revision to match the `1.0.0-local.<12-hex>` tag and injects
the corresponding `git-<12-hex>` service version. It rejects a checked-in zero tag or a target that
does not match these explicit local-dev coordinates.

## Provision and verify dependency resources

Provisioning is separate from normal deployment checks and application startup. It reads ECK and
Strimzi administrative inputs only through their owning Kubernetes resources. Credentials stay in
process memory and must not appear in commands or evidence.

Create and verify the query fixture on the selected target:

```sh
export FIXTURE_CONFIRM=setup-policy-views-issue-229
make go-svc-fixture-setup SVC_NAME=policy-search
unset FIXTURE_CONFIRM
make go-svc-fixture-verify SVC_NAME=policy-search
```

The setup creates the exact mapping, synthetic fixture, reader role/user and workload-owned
Secrets. Verification uses only the workload identity and proves read access to
`policy-views-issue-229` while write, index administration, other-index and security administration
remain denied.

Create and verify the event resources separately:

```sh
export FIXTURE_CONFIRM=event-setup-policy-views-issue-229
make go-svc-event-fixture-setup SVC_NAME=policy-search
unset FIXTURE_CONFIRM
make go-svc-event-fixture-verify SVC_NAME=policy-search
```

QA uses `qa-kafka-kafka-bootstrap.qa-data.svc:9093`, three-replica isolated topics and only
`ca.crt` from `qa-kafka-cluster-ca-cert`. Local-dev uses
`local-dev-kafka-kafka-bootstrap:9093`, one-replica isolated topics and `ca.crt` from
`local-dev-kafka-cluster-ca-cert`. The same setup and verification commands resolve the correct
coordinates from `DEPLOY_ENV`. Verification checks topic ownership, cert-only Kafka trust and the
writer's exact-index document-write boundary while read, index administration, other-index and
security privileges remain denied. It does not reset the query fixture or inspect Java topics or
groups.

For QA, provision and verify the dependency resources before merging configuration that references
their workload-owned Secrets. For local-dev, provision them before a direct event-mode rollout.
Kubernetes requires each referenced Secret to exist before an enabled worker first consumes an
event.

## Deploy or reconcile the release

The QA release uses
`ghcr.io/igor-baiborodine/insurance-hub/policy-search-api:<semantic-version>`. The release owner
updates the QA overlay image tag from the semantic version and sets `SERVICE_VERSION` to
`git-<12-source-hex>` from the full release source commit. Review the rendered Deployment,
ConfigMap, Services and NetworkPolicy together with the dependency and event mode they select.

Merge the reviewed Git change and let the shared `qa-svc` Flux Kustomization reconcile it. Direct
image load, deployment and stop operations reject QA. Do not patch the live Deployment, selectors,
ConfigMap, NetworkPolicy or image tag. Do not add a service-specific Flux source or initialization
Job.

Observe the owning reconciliation:

```sh
kubectl --context "$DEPLOY_CONTEXT" -n flux-system get \
  gitrepository/insurance-hub kustomization/qa-svc -o wide
kubectl --context "$DEPLOY_CONTEXT" -n "$DEPLOY_SVC_NS" rollout status \
  deployment/qa-policy-search-api --timeout=180s
```

Require the GitRepository and Kustomization to be Ready at `DEPLOY_SOURCE_REVISION`. An ambiguous
or interrupted reconciliation must be inspected before retrying or starting recovery.

Local-dev does not use Flux. The guarded direct owner renders the selected overlay, verifies the
current target and Java resource identities, performs a server-side dry run, loads the exact image
into Kind, applies only the Go resources and verifies the rollout:

```sh
make go-svc-deploy-preflight SVC_NAME=policy-search
make go-svc-image-load SVC_NAME=policy-search
make go-svc-deploy SVC_NAME=policy-search
make go-svc-deploy-status SVC_NAME=policy-search
make go-svc-deploy-verify SVC_NAME=policy-search
```

For a local-dev replacement, capture the running image before preflight and keep it declared until
the new image is applied:

```sh
export DEPLOY_PREVIOUS_IMAGE_REF="$(kubectl --context "$DEPLOY_CONTEXT" \
  -n "$DEPLOY_SVC_NS" get deployment/local-dev-policy-search-api \
  -o jsonpath='{.spec.template.spec.containers[0].image}')"
make go-svc-deploy-preflight SVC_NAME=policy-search
make go-svc-image-load SVC_NAME=policy-search
make go-svc-deploy SVC_NAME=policy-search
unset DEPLOY_PREVIOUS_IMAGE_REF
make go-svc-deploy-verify SVC_NAME=policy-search
```

To enable local-dev event workers, first provision the event resources, then set
`DEPLOY_EVENT_MODE=enabled` and declare `DEPLOY_PREVIOUS_EVENT_MODE=disabled` through the apply.
The owner composes worker settings, Secret references, the Kafka CA mount and Kafka egress as one
state:

```sh
export DEPLOY_EVENT_MODE=enabled
export DEPLOY_PREVIOUS_EVENT_MODE=disabled
make go-svc-deploy-preflight SVC_NAME=policy-search
make go-svc-image-load SVC_NAME=policy-search
make go-svc-deploy SVC_NAME=policy-search
unset DEPLOY_PREVIOUS_EVENT_MODE
make go-svc-deploy-status SVC_NAME=policy-search
make go-svc-deploy-verify SVC_NAME=policy-search
```

Keep `DEPLOY_EVENT_MODE=enabled` for later status and verification, then unset it after the bounded
operation. Set `DEPLOY_PREVIOUS_IMAGE_REF` as described above when activation also changes the
image. QA rejects these direct apply operations and changes event mode only through reviewed Git
and Flux reconciliation.

## Verify the selected deployment

Run the catalog-selected status and verification owners with the selected environment variables
still exported. QA requires its source revision and digest; local-dev requires its image reference
and the selected event mode:

```sh
make go-svc-deploy-status SVC_NAME=policy-search
make go-svc-deploy-verify SVC_NAME=policy-search
make go-svc-fixture-verify SVC_NAME=policy-search
# Run this only when event resources are selected.
make go-svc-event-fixture-verify SVC_NAME=policy-search
```

Status and verification check exact live inventory, source/image identity, private exposure,
health, reader and writer boundaries, event configuration and a synthetic query through the
business Service while confirming the Java resource identities remain unchanged.

For independent reachability, create bounded probe Pods in `$DEPLOY_SVC_NS`. Label allowed
business and management probes with the exact labels in this runbook. Otherwise identical
unlabeled probes must time out on ports 8080, 9090 and 8081. Delete only the named probes after
recording their results.

For QA, use the [observability runbook](observability-runbook.md) to verify Alloy delivery,
dashboard data, alert state and safe log/trace correlation for the same source, image and Pod
identities. Local-dev has telemetry disabled by default; use the module's owned loopback telemetry
tests for source behavior and deployment health/readiness for the local cluster. Do not treat a
local-dev deployment as QA observability evidence.

## Diagnostics and stop conditions

Inspect only the selected resources:

```sh
kubectl --context "$DEPLOY_CONTEXT" -n "$DEPLOY_SVC_NS" \
  get deployment,service,configmap,networkpolicy,pod \
  -l app.kubernetes.io/name=policy-search-api -o wide
kubectl --context "$DEPLOY_CONTEXT" -n "$DEPLOY_SVC_NS" \
  logs deployment/"${DEPLOY_ENV}-policy-search-api" --tail=200
kubectl --context "$DEPLOY_CONTEXT" -n "$DEPLOY_SVC_NS" \
  describe deployment/"${DEPLOY_ENV}-policy-search-api"
# Run this only when event resources are selected.
kubectl --context "$DEPLOY_CONTEXT" -n "$DEPLOY_DATA_NS" \
  get kafkatopic policy-registered-issue-230 policy-terminated-issue-230
```

Safe worker messages are `policy event completed`, `malformed policy event skipped`,
`policy event projection failed` and `policy event fetch failed`. Do not record payloads,
queries, policy/customer values, credentials, Secret data or arbitrary raw errors.

Stop when source, image, Pod, dependency, Secret/trust, selector, exposure or Java-owner identity
differs from the reviewed state. In QA, also stop on digest or Flux drift. In local-dev, stop when
the loaded image revision or declared predecessor differs. Loss of Elasticsearch or the exact index
makes readiness fail while liveness remains process-only. Collector failure may emit only the
shared rate-limited diagnostic and does not relax worker readiness or dependency trust. Do not
disable NetworkPolicy, scale Java, reset consumer offsets or substitute a mutable image tag to
bypass a failure.

## Rollback and restoration

Rollback is a reviewed Git change through the same QA overlay and Flux owner. Identify the
compatible predecessor source, semantic image tag, immutable digest, configuration and event mode
before changing Git. For an event-to-query return, disable workers and remove the writer/Kafka
Secret references, Kafka CA mount and Kafka egress together. Preserve the topics, group offsets,
query fixture and synthetic documents.

After the rollback revision reconciles, set `DEPLOY_SOURCE_REVISION` and `DEPLOY_IMAGE_DIGEST` to
that reviewed predecessor and repeat status, deployment verification and applicable fixture
verification. Confirm the consumers leave `policy-search-service-issue-230`, query and health checks
pass, and Java identities remain unchanged.

Restore the candidate through a second reviewed Git revision. Reset the exported source and digest
to the restored candidate, wait for exact Flux reconciliation, then repeat all QA verification and
observability checks. Image-only rollback or a live patch does not prove the return path. Record
both revisions, both image digests and the resource identities before and after each transition.

Local-dev rollback uses the same direct owner instead of Git/Flux. Set `DEPLOY_IMAGE_REF` to the
compatible predecessor, declare the currently running candidate through
`DEPLOY_PREVIOUS_IMAGE_REF`, then run preflight, image load, deploy and verification. Restore by
reversing those two image values and repeating the sequence. When event mode changes, declare both
`DEPLOY_EVENT_MODE` and `DEPLOY_PREVIOUS_EVENT_MODE` so worker settings, Secrets, trust and egress
move together. For a first-install removal/redeployment check, `make go-svc-stop
SVC_NAME=policy-search` scales only the local Go Deployment to zero; rerun the guarded deploy and
verification sequence to restore it. Direct stop is rejected for QA.

## Retire service-owned dependency resources

Retirement requires separate authorization and must occur only after no Pod references the owned
Secrets and no consumer uses the owned topics or group. Retire event resources first:

```sh
export FIXTURE_CONFIRM=event-retire-policy-views-issue-229
make go-svc-event-fixture-retire SVC_NAME=policy-search
unset FIXTURE_CONFIRM
```

This removes only the two configured topics, writer role/user and event Secrets. It preserves the
query index, reader and all Java resources. Retire the query fixture only after the query workload
no longer uses it:

```sh
export FIXTURE_CONFIRM=retire-policy-views-issue-229
make go-svc-fixture-retire SVC_NAME=policy-search
unset FIXTURE_CONFIRM
```

This removes only `policy-views-issue-229`, `go_policy_search_reader` and the workload-owned query
Secrets. Read back the exact resource absence and verify that shared indices, Java resources and
unrelated Secrets remain unchanged.

## Evidence checklist

Record sanitized evidence in the operation's approved evidence location:

- UTC start/end, operator, exact command and result;
- full source SHA, desired and running image identities and, for QA, the Flux source/applied
  revision, semantic image tag and immutable digest;
- Deployment generation, ConfigMap identity, Pod UIDs, ready replicas and effective worker mode;
- query/event resource identities and least-privilege verification results;
- private reachability, health, query/event outcome and Java-owner continuity;
- rollback predecessor, restored candidate, both reconciliation results and cleanup state; and
- limitations, failed or blocked checks and the acceptance outcome each record supports.

Exclude credentials, tokens, Secret YAML, credential-bearing URLs, raw queries, policy/event
payloads and arbitrary errors. Repository, disposable, Flux and live QA evidence remain distinct;
one does not imply another.
