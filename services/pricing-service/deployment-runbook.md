<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Pricing service deployment](#pricing-service-deployment)
  - [Candidate checks](#candidate-checks)
  - [Direct local-dev](#direct-local-dev)
  - [Flux-owned QA](#flux-owned-qa)
    - [Advance a manifest-only QA source](#advance-a-manifest-only-qa-source)
  - [Private smoke](#private-smoke)
  - [Stop conditions and recovery](#stop-conditions-and-recovery)
  - [PostgreSQL resource administration](#postgresql-resource-administration)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Pricing service deployment

Use catalog key `pricing`. The Go workload is `<environment>-pricing-api`. Go Pricing privately
serves PostgreSQL-backed quotes. Its cluster-internal `<environment>-pricing-api` Service exposes
HTTP port 8080 to the authorized HTTP quote-caller role and gRPC port 9090 to the authorized direct
RPC quote-caller role in the same namespace. Each caller role is limited to its own transport. The
Pricing NetworkPolicy base and selected environment overlay are the source of truth for deployed
caller selectors. Do not copy selector values into operational commands.
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
make go-svc-deploy-render SVC_NAME=pricing DEPLOY_ENV=local-dev
make go-svc-deploy-render SVC_NAME=pricing DEPLOY_ENV=qa
```

The Pricing release bundle owns its immutable source, migration-before-seed prerequisite and stable
API workload. Local-dev requires an explicit, source-matched image to perform operations. For
disposable qualification, build a local image as below, then run
`make go-svc-deploy-smoke SVC_NAME=pricing` with the selected image and source revision exported.
The smoke copies only the existing two-key reader Secret into one labeled Kind namespace, scopes
all writes to that namespace, and removes the namespace afterward. A mode-`0600` temporary
minimized kubeconfig selects the guarded Kind context without changing the user's current context.

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

## Flux-owned QA

The [release workflow](../../.github/workflows/go-release-svc.yml) builds once, pushes the
semantic and source tags, and resolves the registry digest before calling
`go-svc-release-prepare`. Release preparation records the immutable digest and source-derived
runtime version in `k8s/flux/qa/releases/pricing/release.yaml`. Its initialization owner runs the
target-guarded migrator first and seeder second with `backoffLimit: 0`, bounded deadlines and
retained failure evidence. Inspect a failed Job and prepare an explicit next attempt; do not delete
or automatically retry it. The stable API owner depends on that exact successful attempt. Keep
image source and Flux configuration commits separate.

The release workflow starts automatically only for a `main` push that changes Pricing build inputs.
Every Pricing QA owner reads manifests from the release source commit. A manifest-only Pricing
change therefore reaches QA only when the bundle is advanced to a later source commit. See
[Advance a manifest-only QA source](#advance-a-manifest-only-qa-source).

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

### Advance a manifest-only QA source

Use this when a reviewed change merged to `main` touches only Pricing manifests, initialization
configuration or release assets, and the published image is still the intended executable. It
generates a new release identity from the later source commit and keeps the published image
digest. No image is built and no semantic version is published.

Start from a clean checkout at the canonical commit that owns the desired manifests. Read the
current image from the stable API owner in `k8s/flux/qa/releases/pricing/release.yaml`. Its source
GitRepository, initialization owner and `qa-pricing-api` must agree on
`insurance-hub.io/image-digest`. Take the semantic version that published that digest from the
matching `chore(k8s): update pricing Go release to <version>` commit or GitHub release. Confirm
that the registry still resolves that version to the digest, and record the image's own source
commit from its OCI label:

```bash
git fetch origin main
source_commit="$(git rev-parse 'origin/main^{commit}')"
test "$(git rev-parse 'HEAD^{commit}')" = "$source_commit"
test -z "$(git status --porcelain --untracked-files=no)"
image=ghcr.io/igor-baiborodine/insurance-hub/pricing-api
release_version='<existing-published-1.x-version>'
image_digest='sha256:<digest-from-the-current-bundle>'
test "$(docker buildx imagetools inspect "$image:$release_version" \
  --format '{{json .Manifest.Digest}}' | tr -d '"')" = "$image_digest"
docker pull "$image@$image_digest"
image_source="$(docker image inspect "$image@$image_digest" \
  --format '{{index .Config.Labels "org.opencontainers.image.revision"}}')"
```

Stop if the owners disagree, the registry digest differs, or `origin/main` contains a Pricing
build-input change. A build-input change needs the normal automatic release instead. Generate
the bundle through the generic interface, then review it:

```bash
make go-svc-release-prepare \
  RELEASE_SERVICE=pricing RELEASE_TARGET=qa \
  RELEASE_REPOSITORY=igor-baiborodine/insurance-hub \
  RELEASE_VERSION="$release_version" \
  RELEASE_SOURCE_COMMIT="$source_commit" \
  RELEASE_IMAGE_DIGEST="$image_digest" \
  RELEASE_OUTPUT=/tmp/pricing-release-paths
git diff --check -- k8s/flux/qa/releases/pricing/release.yaml
git diff -- k8s/flux/qa/releases/pricing/release.yaml
make go-svc-deploy-check SVC_NAME=pricing
make go-svc-release-test
```

The new GitRepository `spec.ref.commit` and every new `insurance-hub.io/source-revision` must equal
`source_commit`. The init and API images must keep `image_digest`. Asset hashes must match the
files at `source_commit`. `PRICING_SERVICE_VERSION` must become `git-<first 12 hex of source_commit>`.
`qa-pricing-api` must depend on the new initialization owner. Earlier sources and initialization
owners must remain byte-equivalent. Commit only the emitted bundle path through a reviewed pull
request. Merging it is a separately authorized QA activation.

After Flux applies the bundle, the new initialization Job reruns against the existing QA database.
The migration should report schema `version=1` with no new migration, and the first seed result
should be `inserted=0 existing=4`. Then run the QA checks above with the split identity:

```bash
export DEPLOY_IMAGE_SOURCE_REVISION="$image_source"
export DEPLOY_SOURCE_REVISION="$source_commit"
export RELEASE_VERSION="$release_version" DEPLOY_IMAGE_DIGEST="$image_digest"
```

The OCI revision and version labels keep the image's source, while the workload version label,
startup record and telemetry report `git-<first 12 hex of source_commit>`. Record both commits,
the unchanged digest and the Flux-applied configuration commit as the release identity, with the
retained digest named as a scoped exception.

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
an untrusted workload cannot open any Pricing port, the authorized HTTP caller can open only HTTP,
and the authorized direct RPC caller can open only gRPC. The verifier obtains caller selectors
from the Pricing deployment configuration and removes all three probe Pods.

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

Both environments permit ingress only from the same-namespace caller roles selected by
`k8s/apps/svc/pricing/base/network-policy.yaml` and the active Pricing environment overlay.
Local-dev permits only TCP and UDP 53 to kube-dns and PostgreSQL 5432 to the selected local Pricing
cluster. QA permits the same exact database path plus OTLP/gRPC to the selected Alloy Pods. The API
mounts only `database-url` and `ca.crt` from the environment reader Secret. Inspect the six identity
fields in the Pricing startup record and verify corresponding telemetry by
`service.name=pricing-service`, `service.version` and `service.instance.id` through Alloy's existing
signal paths. Keep request payloads out of operational evidence.

Readiness requires the internal gateway connection and a Pricing reader access check within
`PROBE_TIMEOUT`, so PostgreSQL loss makes `/readyz` fail while `/livez` keeps passing. Liveness
covers only the process. Telemetry export never affects either probe. A refused or failed database
connection returns HTTP 503 `service unavailable` (gRPC `UNAVAILABLE`); a hung database or an
exhausted pool returns HTTP 504 (gRPC `DEADLINE_EXCEEDED`). Use the
[observability runbook](observability-runbook.md#reader-symptoms) to identify the failed reader
stage.

## Stop conditions and recovery

Classify a failure from Pricing-owned evidence before changing the target:

- Run `make go-svc-deploy-status SVC_NAME=pricing` to compare the release source, workload version,
  image ID, ready Pod and private exposure with the recorded release.
- Inspect `flux-system/qa-pricing-api` and its referenced GitRepository with the read-only commands
  in [Flux-owned QA](#flux-owned-qa) when QA source or ownership differs.
- Run `make -C services/pricing-service resource-inspect` with the exact variables in
  [PostgreSQL resource administration](#postgresql-resource-administration) when readiness or quote
  reads fail. Use `resource-verify` only with authorization for its bounded database probes.
- Use the loopback port-forward in [Private smoke](#private-smoke) to distinguish HTTP, direct RPC,
  management and reader symptoms without changing cluster routing.
- Use the [observability runbook](observability-runbook.md) to correlate request, database, pool,
  runtime and telemetry signals for the same release identity.

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

Set `PRICING_DATABASE_URL` from the reader Secret's `database-url` key. Mount the reader CA at
`/etc/pricing-service/postgres/ca.crt`.
Startup recognizes only the Pricing reader role, verifies PostgreSQL TLS and tariff reads, and
uses a read-only pool. The migrator and seeder use separate credentials and remain explicit
commands. Kubernetes startup requires the reader dependency; local standalone development may
omit it.

`DB_MAX_CONNS` (8) bounds the Pricing reader pool, `DB_CONNECT_TIMEOUT` (3s) bounds connection
setup, `DB_ACQUIRE_TIMEOUT` (2s) bounds pool acquisition, and `DB_QUERY_TIMEOUT` (5s) bounds the
query. When the reader is configured, both acquisition and probe timeouts must fit within the query
budget, which must fit within `REQUEST_TIMEOUT`. Connection setup must fit within
`STARTUP_TIMEOUT` (10s), which also bounds gateway startup.
