<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Document lookup deployment runbook](#document-lookup-deployment-runbook)
  - [Source and candidate checks](#source-and-candidate-checks)
  - [Candidate identity and caller proof](#candidate-identity-and-caller-proof)
  - [Local development](#local-development)
  - [QA release and verification](#qa-release-and-verification)
  - [Explicit database and read-access administration](#explicit-database-and-read-access-administration)
    - [Fresh schema initialization and compatible adoption](#fresh-schema-initialization-and-compatible-adoption)
    - [Verification and focused disposable smoke](#verification-and-focused-disposable-smoke)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Document lookup deployment runbook

The private Go lookup uses catalog key `document`. Local development is directly owned at
context `kind-local-dev-insurance-hub`, namespace `local-dev-all`; QA is reconciled by
`GitRepository/insurance-hub` and `Kustomization/qa-svc` in `flux-system` into namespace `qa-svc`.
The Go Deployment is `<environment>-document-api`. Its private business Service exposes HTTP 80
and gRPC 9090; `<environment>-document-api-management` exposes management 8081. The NetworkPolicy
admits business traffic only from a same-environment Pod carrying
`insurance-hub.io/document-qualification-client=true`. It permits DNS, the Document PostgreSQL
cluster, the Document MinIO namespace and, in QA, Alloy egress. The separate
`<environment>-document-api-legacy` resources remain active and authoritative for ordinary traffic.

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
cluster. It loads the exact source-derived image, starts owned PostgreSQL and MinIO fixtures,
creates only synthetic `SYN-362` rows/object and reader credentials, and checks exact private
lookup bytes and dependency-aware health. A labeled Pod proves allowed HTTP admission; unlabeled
Pods separately prove denied HTTP, gRPC and management access. Cleanup removes only the namespace.
The image check separately exercises TLS-verified disposable PostgreSQL and the accepted pinned
MinIO image; the Kubernetes fixture uses cluster-isolated synthetic dependencies and does not
replace live trust verification.

The unchanged legacy gateway boundary has a focused isolated check:

```sh
cd legacy/agent-portal-gateway
bash ./mvnw -Dskip.surefire.tests=true -Dit.test=DocumentLookupEdgeIT verify
```

It starts a disposable gateway with a synthetic signing key and overrides only
`micronaut.http.services.document-service.urls`. It checks authenticated exact bytes,
unauthenticated denial and the existing retry/fallback result. The checked-in local-dev and QA
destinations remain the Java `*-document-api-legacy` Service.

## Candidate identity and caller proof

The local overlay selects the committed lookup source `f50eeb5bd8f65bcf473c22060ec64166e6cb8224`
as image `insurance-hub-document-api:1.0.0-local.f50eeb5bd8f6` with runtime version
`git-f50eeb5bd8f6`. Qualification must independently record the OCI revision and local image ID;
the ID is not a registry digest. Do not identify an image as a later dirty working tree or reuse
this identity after runtime source changes. Ordinary local-dev and Flux-owned QA observations
remain the live acceptance boundary.

## Local development

Keep the following coordinates and exact candidate identity in one shell. These operations load
the image, apply the ConfigMap, Deployment, two Services and NetworkPolicy, wait for rollout and
verify private access. Reader Secrets and the explicit migration/resource sequence must already
exist:

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

For a local-dev replacement, capture the current Go image before preflight and declare it until
the replacement is applied:

```sh
export DEPLOY_PREVIOUS_IMAGE_REF="$(kubectl --context "$DEPLOY_CONTEXT" \
  -n "$DEPLOY_SVC_NS" get deployment/local-dev-document-api \
  -o jsonpath='{.spec.template.spec.containers[0].image}')"
make go-svc-deploy-preflight SVC_NAME=document
make go-svc-image-load SVC_NAME=document
make go-svc-deploy SVC_NAME=document
unset DEPLOY_PREVIOUS_IMAGE_REF
make go-svc-deploy-status SVC_NAME=document
make go-svc-deploy-verify SVC_NAME=document
```

The predecessor input accepts only a source-derived local Document image. It permits that exact
image, Deployment version labels and ConfigMap `SERVICE_VERSION` only before apply. Final status
and verification require the selected replacement identity everywhere.

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

The Document overlay is already selected once in `k8s/overlays/qa/svc/kustomization.yaml`. The
main-branch `.github/workflows/go-release-svc.yml` owner builds and pushes the image, resolves its
registry digest, then `go-svc-release-prepare` updates the semantic tag and source-derived runtime
version while keeping the shared activation idempotent. Document has no catalog initialization
owner, so its dormant release-bundle metadata is not the executable deployment path. The actual
owner is the shared `GitRepository/insurance-hub` plus `Kustomization/qa-svc`; do not apply the QA
overlay directly or invent a per-service GitOps owner.

After Flux reports the shared source and `qa-svc` owner Ready at the release commit, supply the
Flux-applied revision, immutable image digest and exact coordinates for read-only status and the
bounded verification fixture:

```sh
export DEPLOY_ENV=qa
export DEPLOY_CONTEXT=qa-insurance-hub
export DEPLOY_SVC_NS=qa-svc
export DEPLOY_DATA_NS=qa-data
export DEPLOY_SOURCE_REVISION='FULL_FLUX_APPLIED_COMMIT'
export DEPLOY_IMAGE_DIGEST='sha256:64_LOWERCASE_HEX'

make go-svc-deploy-status SVC_NAME=document
make go-svc-deploy-verify SVC_NAME=document
```

Record the image source commit separately from the later Flux release commit. Stop if the selected
context, namespaces, source revision, digest, Pod image ID, runtime version, rendered inventory or
legacy separation differs. Recovery uses a reviewed release commit or Flux reconciliation; never
patch QA, suspend the shared owner or remove Java resources.

## Explicit database and read-access administration

The administrative executable and resource tooling are separate from API startup. Lookup uses the
restricted reader identities only; Java retains ordinary routes and registration authority.
`document` owns both fresh and adopted tables/sequences/history. `go_document_migrator` is a
NOINHERIT login that explicitly assumes `document`; `go_document_reader` has SELECT only and no
sequence/owner membership. MinIO uses a separate `document-reader` identity with a `document-reader`
policy limited to reads and bucket metadata on `policies`.

Use an independently verified administrative runner with Kubernetes access, psql, mc and network
reachability to the enrolled MinIO DNS endpoint. Supply mc's private `config.json` with alias
`document`; never put credentials in flags or shell history. The containing directory must be
mode 0700 and the file 0600. For QA, provision the verified public issuing CA under its
`certs/CAs/` directory; do not use `MC_INSECURE`. The tools validate the selected/running accepted
MinIO digest before resource setup. A changed image needs a new security disposition first.

All coordinates are required explicitly. For local-dev:

```sh
export DOCUMENT_RESOURCE_ENV=local-dev
export DOCUMENT_RESOURCE_CONTEXT=kind-local-dev-insurance-hub
export DOCUMENT_RESOURCE_DATA_NS=local-dev-all
export DOCUMENT_RESOURCE_SVC_NS=local-dev-all
export DOCUMENT_RESOURCE_CLUSTER=local-dev-postgres-document
export DOCUMENT_RESOURCE_DB_HOST=local-dev-postgres-document-rw.local-dev-all.svc.cluster.local
export DOCUMENT_RESOURCE_MINIO_NS=local-dev-minio-document
export DOCUMENT_RESOURCE_MINIO_ENDPOINT=http://minio.local-dev-minio-document.svc.cluster.local:80
export DOCUMENT_MINIO_ADMIN_CONFIG=/protected/document-minio/config.json
make -C services/document-service resource-inspect
```

For QA, use the same interface with every coordinate changed together:

```sh
export DOCUMENT_RESOURCE_ENV=qa
export DOCUMENT_RESOURCE_CONTEXT=qa-insurance-hub
export DOCUMENT_RESOURCE_DATA_NS=qa-data
export DOCUMENT_RESOURCE_SVC_NS=qa-svc
export DOCUMENT_RESOURCE_CLUSTER=qa-postgres-document
export DOCUMENT_RESOURCE_DB_HOST=qa-postgres-document-rw.qa-data.svc.cluster.local
export DOCUMENT_RESOURCE_MINIO_NS=qa-minio-document
export DOCUMENT_RESOURCE_MINIO_ENDPOINT=https://minio.qa-minio-document.svc.cluster.local:443
export DOCUMENT_MINIO_ADMIN_CONFIG=/protected/qa-document-minio/config.json
make -C services/document-service resource-inspect
```

Inspection reports bounded catalog/resource state without creating roles, schema, buckets or
Secrets. Ensure requires separately authorized resource mutation and the exact confirmation:

```sh
export DOCUMENT_RESOURCE_CONFIRM="ensure-${DOCUMENT_RESOURCE_ENV}-document-resources"
make -C services/document-service resource-ensure
unset DOCUMENT_RESOURCE_CONFIRM
```

Ensure validates the owned boundary before writes, enrolls the CNPG UID and a stable instance UUID,
creates only absent PostgreSQL reader/migrator and MinIO read resources, and preserves existing
credentials. It removes inherited PUBLIC database/schema access only after checking consumers;
Java's `document` ownership remains usable. In the dedicated Document CNPG cluster, it also
revokes PUBLIC CONNECT on the `postgres` and `template1` maintenance databases so service logins
cannot enter them. A foreign database, maintenance owner or unknown login blocks this operation;
no other service's database cluster is modified. No seed, Go writer, rotation, revoke or reset is provided.
It creates the following environment-prefixed Secrets in the selected service namespace:

| Secret suffix after environment prefix | Contents and purpose |
| --- | --- |
| `postgres-document-admin-target` | `target.json`: protected environment/context/namespaces/database/host/cluster UID/instance UUID enrollment |
| `postgres-document-reader-creds` | `database-url`, `password`, `ca.crt`: verify-full read login and public CNPG trust |
| `postgres-document-migrator-creds` | `database-url`, `password`, `ca.crt`: administrative login and trust, never mounted in the API |
| `minio-document-reader-creds` | `access-key`, `secret-key`, `endpoint`, `ca.crt`: separate read identity and QA public trust (empty CA for accepted local-dev HTTP) |

Pending Secrets are created before external credential/resource creation and retained if setup is
interrupted. Rerun against the same verified target: existing private credentials are reused,
missing roles/resources and policy binding can be completed, and no password is silently rotated.
Secret creation is atomic; a concurrent or conflicting writer fails rather than overwriting it.
A role/user without its recoverable owned Secret, a mismatched credential/CA/enrollment, or foreign
policy/ownership blocks setup. Inspect the protected state and resolve its provenance; do not
retry by deleting resources or generating replacement credentials. Unexpected/ambiguous outcomes
must be inspected before rerunning.

### Fresh schema initialization and compatible adoption

Build the administrative binary using the module owner:

```sh
make -C services/document-service build-migrator
export DOCUMENT_MIGRATOR="$PWD/services/document-service/.tools/document-migrate"
```

Using the protected operator Secret-export channel, place `target.json`, the migrator
`database-url`, and `ca.crt` in separate private regular files under a mode-0700 directory. Do not
print or retain these values as ticket evidence. Each file must be mode 0600. Export their paths:

```sh
export DOCUMENT_MIGRATION_TARGET_FILE=/protected/document-migration/target.json
export DOCUMENT_MIGRATION_URL_FILE=/protected/document-migration/database-url
export DOCUMENT_MIGRATION_CA_FILE=/protected/document-migration/ca.crt
export DOCUMENT_MIGRATION_OPERATION=status
make -C services/document-service migration
export DOCUMENT_MIGRATION_OPERATION=version
make -C services/document-service migration
```

Status/version are genuinely read-only, including before Goose exists. Status distinguishes
`uninitialized`, `unmanaged`, `pending` and `version=1`; version reports `uninitialized`, `0` or `1`.
An unmanaged compatible schema has no recorded migration version yet. Neither command initializes
history, advances a sequence, creates objects or repairs state. An incompatible schema is an error.

For a fresh installation, infrastructure and resource ensure must already exist; Java is not
needed to initialize the schema. The explicit operation creates the accepted nullable bytea/
policy-number table and increment-1 Hibernate sequence as `document`, records Goose version 1,
and grants reader SELECT:

```sh
export DOCUMENT_MIGRATION_OPERATION=migrate
export DOCUMENT_RESOURCE_CONFIRM="migrate-${DOCUMENT_RESOURCE_ENV}-document-schema"
make -C services/document-service migration
unset DOCUMENT_RESOURCE_CONFIRM
export DOCUMENT_MIGRATION_OPERATION=status
make -C services/document-service migration
```

For populated Java-owned storage, use the same command. It validates the complete table/column/
constraint/index/owner, sequence shape/progress and history before Goose can write. Under the
bounded Document advisory lock, adoption briefly holds a table lock against Java inserts for
sequence safety validation. Java's unpooled increment-1 nextval may advance naturally; the migrator
never calls nextval/setval on that sequence. It checks safety against existing IDs rather than
computing a replacement position from them. Compatible data, duplicates, IDs, bytes and sequence
progress are preserved; partial/foreign schema, unsafe/exhausted sequence and history drift fail.
Reader grants can be restored by repeating the operation. A failed or uncertain command needs
read-only status/catalog inspection before retry. Applied SQL is immutable; use forward migrations
for later changes. No down/reset, reseeding, Java startup dependency or ownership transfer exists.

The overall migration defaults to 120s (maximum 180s), advisory acquisition is bounded at 30s,
connection setup at 5s and lock/session queries are bounded. Cleanup has a separate 5s budget.
Private input validation happens before a connection. Errors expose safe classifications rather
than credentials, SQL or raw dependency errors. Shared QA operations require their own scope and
must not bypass Flux workload ownership; these commands do not deploy or change normal traffic.

### Verification and focused disposable smoke

After explicit migration, read verification accepts an empty metadata table and tests actual reader
login/SELECT, owner-role assumption and SQLSTATE 42501 denials. PostgreSQL forbidden-operation
probes always roll back and never call nextval; unexpected privileges fail verification. MinIO
verification authenticates the separate read identity, checks the exact policy and private bucket,
and requires an independently provisioned synthetic object `2026/02/SYN-362.pdf` with bytes
`%PDF-362`. Its creation on any shared target requires separately authorized fixture scope.
No customer object is selected for this probe. Verification downloads only that eight-byte fixture
to a private disposable file, then checks denied unrelated-bucket/admin reads without issuing
object writes/deletes. Real write/delete denials are exercised on disposable storage by the smoke.

```sh
export DOCUMENT_RESOURCE_PROBE_KEY=2026/02/SYN-362.pdf
make -C services/document-service resource-verify
unset DOCUMENT_RESOURCE_PROBE_KEY
```

Use the focused owners before live operations; no target coordinates or live credentials are used:

```sh
make -C services/document-service resource-test-fast
make -C services/document-service test-administration
make -C services/document-service resource-test-integration
```

Disposable integration requires Docker, OpenSSL, psql and mc plus the already-installed images
`postgres:17.10-alpine` and the accepted digest-qualified Chainguard MinIO image. It does not pull
images. It creates uniquely named/labeled local containers, temporary PostgreSQL storage and private
synthetic TLS/credential files; cleanup removes only those containers/files. It proves fresh
Java-independent setup, compatible adoption/replay, byte/ID/sequence preservation, subsequent safe
ID allocation, schema/history/target rejection, bounded contention, TLS hostname rejection and
actual PostgreSQL/MinIO read and denied-write/admin behavior. It is not evidence of deployed
permissions, QA CA distribution or live network policy. Retain bounded logs under the ticket's aux
directory when recording a delivery result; do not copy temporary credentials or database contents.
