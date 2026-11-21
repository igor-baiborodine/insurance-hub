<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Pricing service deployment](#pricing-service-deployment)
  - [Candidate checks](#candidate-checks)
  - [Direct local-dev](#direct-local-dev)
    - [Fresh local-dev installation](#fresh-local-dev-installation)
  - [Flux-owned QA](#flux-owned-qa)
  - [Private smoke](#private-smoke)
  - [Stop conditions and recovery](#stop-conditions-and-recovery)
  - [PostgreSQL resource administration](#postgresql-resource-administration)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Pricing service deployment

Use catalog key `pricing`. The Go workload is `<environment>-pricing-api`. Go Pricing privately
serves PostgreSQL-backed quotes. Its cluster-internal `<environment>-pricing-api` Service exposes
HTTP port 8080 to Pods with the Java Policy API labels and gRPC port 9090 to Pods with the reserved
Go Policy API labels in the same namespace. Each caller identity is limited to its own transport.
Health remains available only on Pod port 8081 for kubelet probes. No Ingress, HTTPRoute or
GRPCRoute may reference the Service. Operators use a loopback-bound Kubernetes API port-forward
for direct verification. The workload has no service-account token. The API receives only the
Pricing reader Secret and its CA; migration and seed credentials are mounted only into the
retained administration Job. For monitoring assets, signal investigation and their restoration,
use the [observability runbook](observability-runbook.md).

| Target | Context | Namespace | Owner |
| --- | --- | --- | --- |
| local-dev | `kind-local-dev-insurance-hub` | `local-dev-all` | Guarded direct deployment into Kind cluster `local-dev-insurance-hub` |
| QA | `qa-insurance-hub` | `qa-svc` | `flux-system/qa-pricing-api` owns the reviewed release after its release-specific initialization succeeds. |

## Candidate checks

Run from the repository root with the pinned Python, Go, Docker, Kind and kubectl tools.

```bash
make go-svc-deploy-check go-svc-deploy-test-tooling SVC_NAME=pricing
make go-svc-release-test
make -C services/pricing-service test TEST_PACKAGES=./internal/service
make go-svc-deploy-render SVC_NAME=pricing DEPLOY_ENV=local-dev
make go-svc-deploy-render SVC_NAME=pricing DEPLOY_ENV=qa
```

The shared QA `qa-svc` owner does not deploy the Go Pricing API. The staged Pricing release bundle
keeps the previous API image and suspends predecessor initialization, so merging these manifests
cannot run new commands against the old image. Release preparation creates an immutable source, a
migration-before-seed prerequisite, and the stable API owner in one reviewed bundle. Local-dev
requires an explicit, source-matched image to perform operations. For disposable qualification,
build a local image as below, then run
`make go-svc-deploy-smoke SVC_NAME=pricing` with the selected image and source revision exported.
The smoke copies only the existing two-key reader Secret into one labeled Kind namespace, scopes
all writes to that namespace, and removes the namespace afterward. A mode-`0600` temporary
minimized kubeconfig selects the guarded Kind context without changing the user's current context.
The Pricing module's Go manifest-contract test parses the base Service, listener settings and both
environment policies. For this change, its direct use of the already pinned YAML module moves that
module into the direct `go.mod` requirements. The unchanged shared release workflow and selector
already treat Pricing's `go.mod` as a standard release input, so they select Pricing, build the
source revision once and pin the QA bundle to that commit and image digest without a
Pricing-specific workflow path or shared-classifier exception.

## Direct local-dev

From the clean checkout of the approved image source, build and inspect its local image.

```bash
export DEPLOY_IMAGE_SOURCE_REVISION="$(git rev-parse --verify 'HEAD^{commit}')"
export DEPLOY_IMAGE_REF="insurance-hub-pricing-api:1.0.0-local.${DEPLOY_IMAGE_SOURCE_REVISION:0:12}"
GOWORK=off make go-svc-image-check SVC_NAME=pricing
docker image inspect "$DEPLOY_IMAGE_REF" --format \
  '{{.Id}} {{index .Config.Labels "org.opencontainers.image.revision"}} {{index .Config.Labels "org.opencontainers.image.version"}}'
```

The image ID is content-addressed. The OCI revision must equal the full source commit and
the version must equal `git-${DEPLOY_IMAGE_SOURCE_REVISION:0:12}`. With local-dev access
authorized, select the configured context and run the guarded operations.

```bash
export DEPLOY_ENV=local-dev DEPLOY_CONTEXT=kind-local-dev-insurance-hub
export DEPLOY_SVC_NS=local-dev-all DEPLOY_DATA_NS=local-dev-all
kubectl config use-context "$DEPLOY_CONTEXT"
make go-svc-deploy-preflight SVC_NAME=pricing
make go-svc-image-load SVC_NAME=pricing
make go-svc-deploy SVC_NAME=pricing
make go-svc-deploy-status SVC_NAME=pricing
make go-svc-deploy-verify SVC_NAME=pricing
```

### Fresh local-dev installation

Issue 412 uses one authorized maintenance window to discard the shared local-dev Pricing database
and reconstruct only the reviewed baseline. There is no backup. Java Pricing and Policy pricing
are unavailable during this procedure. Keep Java Pricing quiesced until Go migration and seeding
have completed. Do not use this procedure for QA.

Before the window, record the exact workload, initialization Job, CNPG cluster, PVCs, database
consumers, current Policy destination, image identities and a sanitized tariff summary. Confirm
that only Java and Go Pricing consume `local-dev-postgres-pricing`. Stage the reviewed Go image and
Policy configuration first. These inspection commands do not authorize the later deletions.

```bash
export DEPLOY_ENV=local-dev DEPLOY_CONTEXT=kind-local-dev-insurance-hub
export DEPLOY_SVC_NS=local-dev-all DEPLOY_DATA_NS=local-dev-all
kubectl config use-context "$DEPLOY_CONTEXT"
make go-svc-deploy-preflight SVC_NAME=pricing
make -C services/pricing-service resource-inspect
kubectl --context "$DEPLOY_CONTEXT" -n local-dev-all get \
  deployment/local-dev-pricing-api deployment/local-dev-pricing-api-legacy \
  job/local-dev-pricing-init cluster.postgresql.cnpg.io/local-dev-postgres-pricing \
  --ignore-not-found -o wide
kubectl --context "$DEPLOY_CONTEXT" -n local-dev-all get pvc \
  -l cnpg.io/cluster=local-dev-postgres-pricing -o wide
```

After authorization for the observed inventory and permanent data loss, quiesce Java Pricing,
remove the old Go resources and initialization Job, then purge the old CNPG cluster and its
bootstrap Secret. Wait until the old cluster and PVC identities are absent before provisioning.

```bash
kubectl --context "$DEPLOY_CONTEXT" -n local-dev-all scale \
  deployment/local-dev-pricing-api-legacy --replicas=0
kubectl --context "$DEPLOY_CONTEXT" -n local-dev-all delete \
  -k k8s/overlays/local-dev/svc/pricing/administration --ignore-not-found
kubectl --context "$DEPLOY_CONTEXT" -n local-dev-all delete \
  -k k8s/overlays/local-dev/svc/pricing --ignore-not-found
make -C k8s postgres-svc-purge SVC_NAME=pricing
kubectl --context "$DEPLOY_CONTEXT" -n local-dev-all get \
  cluster.postgresql.cnpg.io/local-dev-postgres-pricing --ignore-not-found
kubectl --context "$DEPLOY_CONTEXT" -n local-dev-all get pvc \
  -l cnpg.io/cluster=local-dev-postgres-pricing
```

Create a new protected bootstrap-password file outside the repository, provision the CNPG cluster,
and wait for a new cluster and storage identity. Remove the password file through the operator's
normal protected-file procedure after the Secret owner has consumed it.

```bash
make -C k8s postgres-svc-secret-create SVC_NAME=pricing \
  PG_SVC_USER_PWD_FILE=</absolute/mode-0600-file>
make -C k8s postgres-svc-deploy SVC_NAME=pricing
make -C k8s postgres-svc-status SVC_NAME=pricing
```

Set the local-dev resource variables from the table below. Run `resource-inspect`, then the
explicitly confirmed `resource-ensure` and `resource-verify`. The fresh target must have a new CA,
target marker and separate reader, migrator and seeder credentials before deployment. Continue
with the guarded local image load and deployment above. Accept the installation only after the
retained Job proves migration before seed, migration status is `version=1`, first seed reports
`inserted=4 existing=0`, and TRI, HSI, FAI and CAR match the reviewed order. Preserve the first
failed Job or command observation before an explicit retry.

## Flux-owned QA

The [release workflow](../../.github/workflows/go-release-svc.yml) builds once, pushes the
semantic and source tags, and resolves the registry digest before calling
`go-svc-release-prepare`. Release preparation records the immutable digest and source-derived
runtime version in `k8s/flux/qa/releases/pricing/release.yaml`. Its initialization owner runs the
target-guarded migrator first and seeder second with `backoffLimit: 0`, bounded deadlines and
retained failure evidence. Inspect a failed Job and prepare an explicit next attempt; do not delete
or automatically retry it. The stable API owner depends on that exact successful attempt. Keep
image source and Flux configuration commits separate.

From the checkout of the applied release configuration, set `DEPLOY_IMAGE_SOURCE_REVISION`
and `DEPLOY_SOURCE_REVISION` to the full release source commit recorded in the bundle's
`insurance-hub.io/source-revision` annotation. Record the Flux-applied configuration commit
separately; it is not an input. Set `RELEASE_VERSION` to the published semantic version and
`DEPLOY_IMAGE_DIGEST` to the digest recorded by the release workflow. Status and verification
require the ready Pod's `imageID` to be that digest, optionally repository-qualified.

```bash
: "${DEPLOY_IMAGE_SOURCE_REVISION:?set the full image source commit}"
: "${DEPLOY_SOURCE_REVISION:?set the full release source commit from the bundle}"
: "${RELEASE_VERSION:?set the published semantic version}"
: "${DEPLOY_IMAGE_DIGEST:?set the immutable digest recorded by the release workflow}"
export DEPLOY_IMAGE_SOURCE_REVISION DEPLOY_SOURCE_REVISION DEPLOY_IMAGE_DIGEST
export DEPLOY_ENV=qa DEPLOY_CONTEXT=qa-insurance-hub
export DEPLOY_SVC_NS=qa-svc DEPLOY_DATA_NS=qa-data
image=ghcr.io/igor-baiborodine/insurance-hub/pricing-api
export DEPLOY_IMAGE_REF="$image@$DEPLOY_IMAGE_DIGEST"
observed_digest="$(docker buildx imagetools inspect "$image:$RELEASE_VERSION" \
  --format '{{json .Manifest.Digest}}' | tr -d '"')"
test "$observed_digest" = "$DEPLOY_IMAGE_DIGEST"
docker pull "$image@$DEPLOY_IMAGE_DIGEST"
kubectl config use-context "$DEPLOY_CONTEXT"
kubectl --context "$DEPLOY_CONTEXT" -n flux-system get gitrepository -l \
  insurance-hub.io/release-identity
kubectl --context "$DEPLOY_CONTEXT" -n flux-system get kustomization qa-pricing-api
make go-svc-deploy-status SVC_NAME=pricing
make go-svc-deploy-verify SVC_NAME=pricing
```

Status checks the exact deployment, ready Pod, Pod UID, version and running image ID. Use a
digest-qualified image reference for OCI revision and version checks. Status and verification
inspect that pulled image and require the published digest to match the ready Pod image ID.

## Private smoke

`go-svc-deploy-verify` resolves the ready Pricing Pod and uses private port-forwards for all
three listeners. It checks the TRI C1/C2 quote and invalid-date rejection through HTTP and the
generated gRPC client, management `200 live\n` and `200 ready\n`, and the six safe startup identity
fields, including the Pod UID. The verifier also proves the named direct-RPC smoke was selected.
For manual troubleshooting, forward the exact Pod selected by status.

```bash
: "${PRICING_POD:?set the ready Go Pricing Pod name from status}"
kubectl --context "$DEPLOY_CONTEXT" -n "$DEPLOY_SVC_NS" port-forward --address 127.0.0.1 \
  "pod/$PRICING_POD" 18080:8080 19090:9090 18081:8081
```

With explicit authorization for the temporary probe Pods, run
`make go-svc-deploy-verify SVC_NAME=pricing DEPLOY_DENIAL_PROBE=1`. It also checks that
an ordinary workload cannot open any Pricing port, a Policy-labeled Pod can call the Service over
HTTP but cannot open gRPC or management, and a reserved Go Policy-labeled Pod can open Service
gRPC but cannot open HTTP or management. It removes all three probe Pods.

In another terminal, use bounded requests against the loopback ports.

```bash
curl --fail --max-time 5 -H 'Content-Type: application/json' \
  -d '{"productCode":"TRI","policyFrom":"2017-04-16","policyTo":"2018-04-15","selectedCovers":["C1","C2"],"answers":[{"questionCode":"NUM_OF_ADULTS","answer":1},{"questionCode":"NUM_OF_CHILDREN","answer":1},{"questionCode":"DESTINATION","answer":"EUR"}]}' \
  http://127.0.0.1:18080/pricing/calculate
curl --silent --show-error --max-time 5 -w '\n%{http_code}\n' \
  -H 'Content-Type: application/json' \
  -d '{"productCode":"TRI","policyFrom":"invalid","policyTo":"2018-04-15","selectedCovers":["C1","C2"],"answers":[{"questionCode":"NUM_OF_ADULTS","answer":1},{"questionCode":"NUM_OF_CHILDREN","answer":1},{"questionCode":"DESTINATION","answer":"EUR"}]}' \
  http://127.0.0.1:18080/pricing/calculate
make -C services/pricing-service _deploy-smoke-rpc PRICING_SMOKE_GRPC_ADDR=127.0.0.1:19090
curl --fail --max-time 5 http://127.0.0.1:18081/livez
curl --fail --max-time 5 http://127.0.0.1:18081/readyz
```

The first response must be `{"totalPrice":78,"coversPrices":{"C1":26,"C2":52}}`. The invalid-date
request returns `{"code":3,"message":"invalid request","details":[]}` followed by status `400`.
The generated direct client must receive exact strings `78`, `26` and `52`; the verifier
requires one matching structured test run and pass event.

Both environments permit HTTP ingress only from the same-namespace Java Policy API labels
`policy-api-legacy`/`api` and gRPC ingress only from the reserved same-namespace Go Policy labels
`policy-api`/`api`. Local-dev permits only TCP and UDP 53 to kube-dns and PostgreSQL 5432 to the
selected local Pricing cluster. QA permits the same exact database path plus OTLP/gRPC to the
selected Alloy Pods. The API mounts only `database-url` and `ca.crt` from the environment reader
Secret. Inspect the six identity fields in the Pricing startup record and verify corresponding
telemetry by `service.name=pricing-service`, `service.version` and `service.instance.id` through
Alloy's existing signal paths. Keep request payloads out of operational evidence.

Readiness requires the internal gateway connection and a Pricing reader access check within
`PROBE_TIMEOUT`, so PostgreSQL loss makes `/readyz` fail while `/livez` keeps passing. Liveness
covers only the process. Telemetry export never affects either probe. A refused or failed database
connection returns HTTP 503 `service unavailable` (gRPC `UNAVAILABLE`); a hung database or an
exhausted pool returns HTTP 504 (gRPC `DEADLINE_EXCEEDED`). Use the
[observability runbook](observability-runbook.md#reader-symptoms) to identify the failed reader
stage.

## Stop conditions and recovery

Use the [Dashboard runbook](../dashboard-service/deployment-runbook.md) first for source,
image, Flux and transport troubleshooting, and the
[Product runbook](../../docs/migration/phase-4/product-service/observability-runbook.md) first for
PostgreSQL reader access and pool behavior.

Stop before a target mutation when any of these differs from the recorded release:

- the context or namespace;
- the Deployment or release owner name;
- the source revision, image reference or digest;
- the reader Secret shape;
- the current Flux owner.

Stop after a mutation for:

- a placeholder or mismatched runtime identity;
- an unexpected ready Pod;
- a failed private quote or probe;
- reader privilege expansion;
- an unexplained readiness failure;
- incomplete owned cleanup.

Preserve the first failure and the current source, image and resource identities before retrying.

In local-dev, `make go-svc-stop SVC_NAME=pricing` with the same coordinates and image selection
scales only the Go Deployment to zero. Redeploy the approved previous Go image by selecting its
full source revision and tag and rerunning the direct deployment steps.

In QA, never patch the workload:

- **Failed initialization.** Retain the Job and inspect its safe status and logs. Correct the
  cause, increment the reviewed release attempt, and publish a new bundle. Never point the stable
  API owner at an incomplete attempt.
- **Failed API reconciliation after successful initialization.** Restore the previous reviewed
  Pricing release bundle. Run `make go-svc-deploy-check SVC_NAME=pricing` before publishing the
  correction.

```bash
kubectl --context qa-insurance-hub -n flux-system get kustomization qa-pricing-api -o yaml
kubectl --context qa-insurance-hub -n flux-system get gitrepository -l \
  insurance-hub.io/release-identity
kubectl --context qa-insurance-hub -n qa-svc get deployment qa-pricing-api --ignore-not-found
```

After recovery, verify:

- **Release owner.** It reports the reviewed release source as applied and ready.
- **Status.** `make go-svc-deploy-status SVC_NAME=pricing` matches the ready Pod's image ID to the
  release digest.
- **Probes and quote.** `make go-svc-deploy-verify SVC_NAME=pricing` passes the probes, the TRI
  quote, invalid-date rejection, the direct RPC and the identity fields.
- **Signals.** The live `verify-service-observability` check from the
  [observability runbook](observability-runbook.md#provision-apply-and-read-back) passes for the
  same release revision.

Restoring the application does not restore monitoring assets; use the observability runbook for
those.

## PostgreSQL resource administration

Set all target variables explicitly before running a resource command. Use the same context
as the selected target.

| Variable | local-dev | QA |
| --- | --- | --- |
| `PRICING_RESOURCE_ENV` | `local-dev` | `qa` |
| `PRICING_RESOURCE_CONTEXT` | `kind-local-dev-insurance-hub` | `qa-insurance-hub` |
| `PRICING_RESOURCE_DATA_NS` | `local-dev-all` | `qa-data` |
| `PRICING_RESOURCE_SVC_NS` | `local-dev-all` | `qa-svc` |
| `PRICING_RESOURCE_CLUSTER` | `local-dev-postgres-pricing` | `qa-postgres-pricing` |
| `PRICING_RESOURCE_READER_SECRET` | `local-dev-postgres-pricing-reader-creds` | `qa-postgres-pricing-reader-creds` |
| `PRICING_RESOURCE_DB_HOST` | `local-dev-postgres-pricing-rw.local-dev-all.svc.cluster.local` | `qa-postgres-pricing-rw.qa-data.svc.cluster.local` |

From the repository root, run `make -C services/pricing-service resource-inspect`
to check roles, enrollment and Secret presence. For an authorized change use
`resource-ensure` with `PRICING_RESOURCE_CONFIRM=ensure-<environment>-pricing-resources`.
Ensure publishes missing managed Secrets before enabling roles. Retry using the same Secrets
after a failure. An existing role without a recoverable Secret needs inspection before retry.
Use `resource-verify` to verify the enrolled role identities, CA, and denial of privileged
SQL using the workload Secrets. Effective checks use a temporary loopback port-forward and
local `psql`. Probe transactions roll back.

Set `PRICING_DATABASE_URL` from the reader Secret's `database-url` key, following Product's
`secretKeyRef` pattern. Mount the reader CA at `/etc/pricing-service/postgres/ca.crt`.
Startup recognizes only the Pricing reader role, verifies PostgreSQL TLS and tariff reads, and
uses a read-only pool. The migrator and seeder use separate credentials and remain explicit
commands. Kubernetes startup requires the reader dependency; local standalone development may
omit it.

The reader uses the same defaults as Product. `DB_MAX_CONNS` (8) bounds the pool,
`DB_CONNECT_TIMEOUT` (3s) bounds connection setup, `DB_ACQUIRE_TIMEOUT` (2s) bounds pool
acquisition, and `DB_QUERY_TIMEOUT` (5s) bounds the query. When the reader is configured,
both acquisition and probe timeouts must fit within the query budget, which must fit
within `REQUEST_TIMEOUT`. Connection setup must fit within `STARTUP_TIMEOUT` (10s),
which also bounds gateway startup.
