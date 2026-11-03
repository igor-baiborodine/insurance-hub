<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Policy Search Go deployment runbook](#policy-search-go-deployment-runbook)
  - [Ownership and exposure](#ownership-and-exposure)
  - [Repository validation](#repository-validation)
  - [Select local-dev](#select-local-dev)
  - [Set up and verify the local-dev fixture](#set-up-and-verify-the-local-dev-fixture)
  - [Set up and verify isolated event resources](#set-up-and-verify-isolated-event-resources)
  - [Deploy and verify local-dev](#deploy-and-verify-local-dev)
  - [Diagnostics and recovery](#diagnostics-and-recovery)
  - [Stop, remove and restore local-dev](#stop-remove-and-restore-local-dev)
  - [Stage and activate QA](#stage-and-activate-qa)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Policy Search Go deployment runbook

This runbook owns service images, Elasticsearch and Kafka fixtures, worker activation and
application rollback. Use the [observability runbook](observability-runbook.md) for monitoring
validation, QA rule/dashboard provisioning, Grafana investigation and monitoring rollback.

## Ownership and exposure

Policy Search runs beside the legacy Java workload. The Go owner creates only
`<environment>-policy-search-api`, its business and management Services, ConfigMap and
NetworkPolicy. A separate, explicitly selected fixture owner creates only
`policy-views-issue-229`, native role/user `go_policy_search_reader` and the workload-owned
Elasticsearch Secrets. Neither owner modifies the Java `policy-search-api-legacy` Deployment or
Service, creates an Ingress, or accesses shared application indices. A separately invoked event
fixture owner creates only the two `*-issue-230` Kafka topics, `go_policy_search_writer`, its
workload Secret and a cert-only Kafka CA Secret. The checked-in local-dev render keeps workers
disabled and references none of those event resources. The active QA render enables the workers
and references only those separately owned issue-230 inputs.

Business ports are reachable only from same-namespace Pods labeled
`insurance-hub.io/policy-search-business-client=true`. Management port 8081 requires
`insurance-hub.io/policy-search-management-client=true`; kubelet probe traffic follows Kubernetes
node-origin semantics. Egress permits cluster DNS and TCP 9200 only to the selected environment's
ECK Elasticsearch Pods. QA additionally permits OTLP/gRPC only to the selected Alloy Pods in
`qa-monitoring`.

## Repository validation

Run non-mutating checks from the repository root:

```sh
make go-svc-image-check SVC_NAME=policy-search
make go-svc-deploy-check SVC_NAME=policy-search
make go-svc-deploy-test-tooling SVC_NAME=policy-search
make go-svc-deploy-test SVC_NAME=policy-search
make go-svc-release-test
make go-svc-dispatch-test
```

`deploy-check` renders local-dev and QA, checks workload security, resources, probes, runtime
budgets, exact Secret/trust references, service exposure, telemetry and NetworkPolicy, and confirms
the active Go QA overlay remains in the shared QA root. Rendering and fake-adapter tests do not
prove admission, enforcement or signal delivery. `deploy-test` first uses a security-enabled
Elasticsearch 8.19.6 Docker node to verify native reader permissions and actual denial responses.
The same permission test proves the separate event writer can replace a document in the accepted
index while reads, deletes, unrelated indices and security administration are denied. The event
integration target uses a generated CA and hostname-verified TLS with disposable Kafka. The
deployment test then uses a uniquely named namespace on the configured Kind cluster with a
security-enabled Elasticsearch node, generated reader and index-only writer identities, and a
hostname-verified TLS Kafka broker. The event-enabled deployment must project the independent
registration oracle and return it through the query API while preserving the complete composed
NetworkPolicy. The run also verifies search transports, policy, dependency recovery, restart and
restoration. It removes the Docker node, Kind namespace and scratch qualification image, leaving
the reviewed runtime image loaded for the later local-dev operation.

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

`kubectl config current-context` must also equal `kind-local-dev-insurance-hub`. If the operator's
shared kubeconfig intentionally has no active context, copy it to a protected task-local file, set
the required current context in that copy, and export its path through `KUBECONFIG` for the bounded
operation. Do not change the shared kubeconfig implicitly. Remove the task-local copy through the
operator's normal protected-file cleanup after retaining required evidence.

## Set up and verify the local-dev fixture

The setup operation reads the ECK `elastic` password only from
`local-dev-elasticsearch-es-elastic-user` in `local-dev-all`. It sends credentials in process
memory, never in command arguments or evidence. It idempotently creates the exact mapping and 108
synthetic documents, the native reader role/user, and
`local-dev-policy-search-elasticsearch`. Normal render, preflight, status and deployment checks do
not run setup.

With the exact target variables above and the required current context selected:

```sh
export FIXTURE_CONFIRM=setup-policy-views-issue-229
make go-svc-fixture-setup SVC_NAME=policy-search
unset FIXTURE_CONFIRM
make go-svc-fixture-verify SVC_NAME=policy-search
```

Verification uses only the workload Secret. It proves `read` and `view_index_metadata` on
`policy-views-issue-229`, no cluster privileges, and no write, mapping/index-management, other-index
or security-administration access. A failure leaves the fixture for inspection and safe replay.

## Set up and verify isolated event resources

Event setup is separate from query setup and from normal checks or application startup. With the
same exact target variables and current context selected, create only the issue-230 resources:

```sh
export FIXTURE_CONFIRM=event-setup-policy-views-issue-229
make go-svc-event-fixture-setup SVC_NAME=policy-search
unset FIXTURE_CONFIRM
make go-svc-event-fixture-verify SVC_NAME=policy-search
```

Local-dev uses `local-dev-kafka-kafka-bootstrap:9093`, one-replica isolated topics and the public
`ca.crt` from `local-dev-kafka-cluster-ca-cert`. QA uses
`qa-kafka-kafka-bootstrap.qa-data.svc:9093`, three-replica isolated topics and only `ca.crt` from
`qa-kafka-cluster-ca-cert`. Verification checks topic cluster/owner labels, the workload CA and the
writer's exact-index `index` privilege while read, index administration, other-index and security
privileges remain denied. It does not reset the query fixture or inspect Java topics/groups.

## Deploy and verify local-dev

When replacing an existing accepted Policy Search image, capture its exact reference before
preflight and keep it declared through the rollout:

```sh
export DEPLOY_PREVIOUS_IMAGE_REF="$(kubectl --context "$DEPLOY_CONTEXT" \
  -n "$DEPLOY_SVC_NS" get deployment/local-dev-policy-search-api \
  -o jsonpath='{.spec.template.spec.containers[0].image}')"
```

Leave `DEPLOY_PREVIOUS_IMAGE_REF` unset for the first deployment. The guard permits only that
declared predecessor before apply and requires the new `DEPLOY_IMAGE_REF` after rollout. When the
declared predecessor is the accepted Echo release, preflight permits only its exact historical
differences: the Elasticsearch username/password variables and the DNS/Elasticsearch egress rules
may be absent. This lets the reviewed apply stage query configuration with the query image. Any
extra environment value, ingress change or other resource drift still stops before mutation.

Run the guarded sequence:

```sh
make go-svc-deploy-preflight SVC_NAME=policy-search
make go-svc-image-load SVC_NAME=policy-search
make go-svc-deploy SVC_NAME=policy-search
unset DEPLOY_PREVIOUS_IMAGE_REF
make go-svc-deploy-status SVC_NAME=policy-search
make go-svc-deploy-verify SVC_NAME=policy-search
```

To activate the reviewed local-dev event boundary, declare both sides of the mode transition. The
owner composes the event ConfigMap, writer Secret references, public Kafka CA mount and broker
egress as one rendered state. It rejects event mode for QA because that target remains Git/Flux
owned:

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

Set `DEPLOY_PREVIOUS_IMAGE_REF` as described above when activation also changes the image. Keep
`DEPLOY_EVENT_MODE=enabled` for status and verification, then unset it after the bounded operation.

Preflight checks target identity, Java resource UIDs, image/source identity and a server-side dry
run before writes. Deploy applies only the rendered Go resources and waits up to the workload's
180-second progress deadline. Verify checks the live inventory, absence of public routes, health
and a synthetic query over the business Service while confirming Java UIDs remain unchanged.

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
contain credentials or request payloads. Loss of Elasticsearch or the exact index makes readiness
fail while liveness remains process-only; restoring the dependency recovers readiness without a
Policy Search restart.

For an enabled event release, add only bounded resource observations:

```sh
kubectl --context "$DEPLOY_CONTEXT" -n "$DEPLOY_DATA_NS" \
  get kafkatopic policy-registered-issue-230 policy-terminated-issue-230
kubectl --context "$DEPLOY_CONTEXT" -n "$DEPLOY_SVC_NS" \
  get secret "${DEPLOY_ENV}-policy-search-elasticsearch-writer" \
  "${DEPLOY_ENV}-policy-search-kafka-ca" \
  -o jsonpath='{range .items[*]}{.metadata.name}{"\\n"}{end}'
make go-svc-event-fixture-verify SVC_NAME=policy-search
```

Inspect safe worker logs for the fixed messages `policy event completed`, `malformed policy event
skipped`, `policy event projection failed` and `policy event fetch failed`. Do not record payloads,
policy/customer values, credentials or Secret data. QA signals use the configured Alloy OTLP/gRPC
endpoint and bounded labels `registered|terminated` and
`success|malformed|error|cancelled`. A collector failure may emit only the shared rate-limited safe
diagnostic; it does not relax worker readiness or dependency trust.

## Stop, remove and restore local-dev

The normal stop owner scales only the Go Deployment to zero and confirms Java UIDs are unchanged:

```sh
make go-svc-stop SVC_NAME=policy-search
```

Verify that the Go endpoints disappear while the Deployment is scaled to zero, then rerun
`go-svc-deploy` and `go-svc-deploy-verify` to restore the accepted release. Compare Java UIDs before
and after both operations. Leave the verified Go release running when acceptance requires a usable
handoff.

For an Echo rollback rehearsal, retain the accepted query and Echo image references and reverse
the declared transition through the same owner:

```sh
export QUERY_IMAGE_REF="$DEPLOY_IMAGE_REF"
export ECHO_IMAGE_REF="insurance-hub-policy-search-api:1.0.0-local.<echo-source-hex>"

export DEPLOY_PREVIOUS_IMAGE_REF="$QUERY_IMAGE_REF"
export DEPLOY_IMAGE_REF="$ECHO_IMAGE_REF"
make go-svc-deploy-preflight SVC_NAME=policy-search
make go-svc-image-load SVC_NAME=policy-search
make go-svc-deploy SVC_NAME=policy-search
unset DEPLOY_PREVIOUS_IMAGE_REF
make go-svc-deploy-status SVC_NAME=policy-search
```

The normal `deploy-verify` probe is query-specific, so verify the rollback through the Echo HTTP
and direct RPC contract plus `/livez` and `/readyz`; confirm `/policies` is absent and compare Java
UIDs. The Echo process must remain healthy with the staged query Secret references and egress
policy. Restore query by setting `DEPLOY_PREVIOUS_IMAGE_REF="$ECHO_IMAGE_REF"` and
`DEPLOY_IMAGE_REF="$QUERY_IMAGE_REF"`, then run preflight, image-load, deploy, status and the normal
query verify. Record both images' full OCI source revisions and leave the verified query image
running.

The accepted fixture stays in place across Echo/query rollback and restoration. Remove it only for
an explicit abort or retirement after the workload no longer references its Secrets:

```sh
export FIXTURE_CONFIRM=retire-policy-views-issue-229
make go-svc-fixture-retire SVC_NAME=policy-search
unset FIXTURE_CONFIRM
```

Retirement deletes only `policy-views-issue-229`, `go_policy_search_reader` and the two possible
workload-owned Secrets. It does not delete or inspect shared indices.

For an event-to-query return, first roll out the complete query predecessor state: workers disabled,
no writer/Kafka Secret references or CA mount, and no Kafka egress rule. Confirm the two consumers
leave `policy-search-service-issue-230`, query/health checks pass and the issue-230 topics, offsets
and synthetic documents remain. Restore the event release with its exact image, configuration,
trust mounts and egress, then confirm consumption resumes without resetting offsets. Image-only
rollback is not a valid return rehearsal.

Use the same owner in both directions:

```sh
export EVENT_IMAGE_REF="$DEPLOY_IMAGE_REF"
export QUERY_IMAGE_REF="insurance-hub-policy-search-api:1.1.0-local.<query-source-hex>"

export DEPLOY_IMAGE_REF="$QUERY_IMAGE_REF"
export DEPLOY_PREVIOUS_IMAGE_REF="$EVENT_IMAGE_REF"
export DEPLOY_EVENT_MODE=disabled
export DEPLOY_PREVIOUS_EVENT_MODE=enabled
make go-svc-deploy-preflight SVC_NAME=policy-search
make go-svc-image-load SVC_NAME=policy-search
make go-svc-deploy SVC_NAME=policy-search
unset DEPLOY_PREVIOUS_IMAGE_REF DEPLOY_PREVIOUS_EVENT_MODE
make go-svc-deploy-verify SVC_NAME=policy-search

export DEPLOY_IMAGE_REF="$EVENT_IMAGE_REF"
export DEPLOY_PREVIOUS_IMAGE_REF="$QUERY_IMAGE_REF"
export DEPLOY_EVENT_MODE=enabled
export DEPLOY_PREVIOUS_EVENT_MODE=disabled
make go-svc-deploy-preflight SVC_NAME=policy-search
make go-svc-image-load SVC_NAME=policy-search
make go-svc-deploy SVC_NAME=policy-search
unset DEPLOY_PREVIOUS_IMAGE_REF DEPLOY_PREVIOUS_EVENT_MODE
make go-svc-deploy-verify SVC_NAME=policy-search
```

Retire event resources only after an explicit abort or final retirement, after no Pod references
their Secrets:

```sh
export FIXTURE_CONFIRM=event-retire-policy-views-issue-229
make go-svc-event-fixture-retire SVC_NAME=policy-search
unset FIXTURE_CONFIRM
```

Event retirement removes only the two issue-230 topics, writer role/user and event Secrets. It
preserves `policy-views-issue-229`, the query reader and all Java resources.

## Stage and activate QA

The QA overlay is active, enables the issue-230 workers and publishes
`ghcr.io/igor-baiborodine/insurance-hub/policy-search-api:<semantic-version>`. The generic release
owner updates the image tag from the semantic release version and sets `SERVICE_VERSION` to
`git-<12-source-hex>` from the full release source commit. Before an event-activation revision is
reconciled, run the separately authorized setup and verification operations with `DEPLOY_ENV=qa`,
context `qa-insurance-hub`, service namespace `qa-svc` and data namespace `qa-data`. Setup copies
only `ca.crt` from `qa-elasticsearch-es-http-certs-public` into
`qa-policy-search-elasticsearch-ca`; the administrative credential never enters `qa-svc`.

Prove the staged endpoint, workload Secret, CA mount and egress policy remain compatible with the
accepted query predecessor before activating the workers. Kubernetes requires every referenced
Secret to exist even while a predecessor process ignores event settings. A failed compatibility
gate stops activation and requires revising the PR allocation. Deployment checks retain an exact
disabled render for predecessor and return-path verification without changing the active QA
Kustomization.

Direct `image-load`, `deploy` and `stop` operations reject QA. Use the shared `qa-svc` Flux owner,
observe its source revision and reconciliation, and export both the exact 40-character reconciled
commit as `DEPLOY_SOURCE_REVISION` and the published `sha256:<64-hex>` registry digest as
`DEPLOY_IMAGE_DIGEST`. QA status and verify reject a missing digest or a running Policy Search Pod
whose runtime `imageID` differs. Also verify Pod identity, Alloy signals and NetworkPolicy from the
QA target. Removal and restoration require separate Git revisions so Flux can observe both states.
Do not add an initialization Job or a service-specific Flux source.
