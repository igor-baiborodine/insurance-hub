<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Document lookup deployment runbook](#document-lookup-deployment-runbook)
  - [Source and candidate checks](#source-and-candidate-checks)
  - [Candidate identity and caller proof](#candidate-identity-and-caller-proof)
  - [Worker qualification boundary](#worker-qualification-boundary)
  - [Local development](#local-development)
  - [QA release and verification](#qa-release-and-verification)
  - [Monitoring coordinates and guarded QA operation](#monitoring-coordinates-and-guarded-qa-operation)
  - [Explicit database, writer and template administration](#explicit-database-writer-and-template-administration)
    - [Fresh schema initialization and compatible adoption](#fresh-schema-initialization-and-compatible-adoption)
    - [Verification and focused disposable smoke](#verification-and-focused-disposable-smoke)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Document lookup deployment runbook

The private Go lookup uses catalog key `document`. Local development is directly owned at
context `kind-local-dev-insurance-hub`, namespace `local-dev-all`; QA is reconciled by
`GitRepository/insurance-hub` and `Kustomization/qa-svc` in `flux-system` into namespace `qa-svc`.
The Go Deployment is `<environment>-document-api`. Its private business Service exposes HTTP 80
and gRPC 9090; `<environment>-document-api-management` exposes management 8081. The local-dev
final-state source admits the explicitly labeled gateway, enables the worker with group
`documents-service`, and permits only the required Kafka and JSReport egress in addition to DNS,
Document PostgreSQL and Document MinIO. Both overlays also render the Java Deployment at zero
replicas and route the gateway to Go. The final QA source enables the ordinary
`documents-service` worker, mounts separate writer and Kafka trust inputs, admits only the labeled
gateway and permits the required Kafka/JSReport egress. These checked-in desired states grant no
permission to mutate either environment; QA changes only when a separately authorized commit is
merged and reconciled by Flux.

Use the companion [observability runbook](observability-runbook.md) for Document monitoring source
validation, guarded QA provisioning/read-back, investigation and restoration. The two procedures
share application and source identity, but workload reconciliation remains Flux-owned while the
Document PrometheusRule and Grafana ConfigMap use their separate guarded monitoring owners.

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

Use JDK 14, matching the legacy CI owner. If only JDK 17 is available, the old Lombok compiler
requires these module openings before the same Maven command:

```sh
export MAVEN_OPTS="${MAVEN_OPTS:-} \
 --add-opens=jdk.compiler/com.sun.tools.javac.code=ALL-UNNAMED \
 --add-opens=jdk.compiler/com.sun.tools.javac.comp=ALL-UNNAMED \
 --add-opens=jdk.compiler/com.sun.tools.javac.file=ALL-UNNAMED \
 --add-opens=jdk.compiler/com.sun.tools.javac.jvm=ALL-UNNAMED \
 --add-opens=jdk.compiler/com.sun.tools.javac.main=ALL-UNNAMED \
 --add-opens=jdk.compiler/com.sun.tools.javac.model=ALL-UNNAMED \
 --add-opens=jdk.compiler/com.sun.tools.javac.parser=ALL-UNNAMED \
 --add-opens=jdk.compiler/com.sun.tools.javac.processing=ALL-UNNAMED \
 --add-opens=jdk.compiler/com.sun.tools.javac.tree=ALL-UNNAMED \
 --add-opens=jdk.compiler/com.sun.tools.javac.util=ALL-UNNAMED"
```

It starts a disposable gateway with a synthetic signing key and overrides only
`micronaut.http.services.document-service.urls`. It checks authenticated exact bytes,
unauthenticated denial and the existing retry/fallback result. The checked-in local-dev and QA
overrides select their private Go Document Services. The baked Java gateway defaults still select
the retained legacy Services, but the rendered overlay configuration is the ordinary route.

## Candidate identity and caller proof

The local overlay selects the committed application source `0e53205ebf0ba43322e202e4485b4555fd59f9f5`
as image `insurance-hub-document-api:1.0.0-local.0e53205ebf0b` with runtime version
`git-0e53205ebf0b`. Qualification must independently record the OCI revision and local image ID;
the ID is not a registry digest. Do not identify an image as a later dirty working tree or reuse
this identity after runtime source changes. Ordinary local-dev and Flux-owned QA observations
remain the live acceptance boundary.

## Worker qualification boundary

The checked-in local-dev and QA overlays are the final transfer state: each enables one Go worker in
the existing `documents-service` group, mounts dedicated writer and Kafka trust inputs, and adds
only Kafka and JSReport egress. The deployment owner rejects the obsolete singular group, reader
credentials in writer slots, foreign renderer destinations and non-gateway business ingress. The
source change does not itself activate either environment; activation still requires a fresh
preflight and separate authorization for the exact handover.

Following the Dashboard reference, Kafka generation qualification exists only inside the owned
disposable boundary. The source-level smoke creates an isolated group and real TLS Kafka, JSReport,
MinIO and PostgreSQL fixtures, verifies that an event becomes a PDF retrievable through the real
HTTP-to-RPC lookup, and removes the whole owned namespace/resources. Companion focused cases cover
duplicate/restart, failures, disabled lifecycle, trust and least privilege.

The disposable qualification defines no local-dev or QA qualification group, one-shot worker
binary, Job or shared-effect cleanup procedure. Source validation checks the final worker
configuration, but it must not publish an event, initialize shared progress or claim live
authority. A separately authorized handover owns ordinary `documents-service` activation, the
route switch and the first bounded shared-environment generation observation.

Shared deployment verification is non-mutating. The lookup probe accepts the contractually valid
empty `{"documents":[]}` result when no synthetic fixture is provisioned. If `SYN-362` exists, it
must match the exact three-document synthetic fixture; any foreign or partial non-empty result
fails. Exact object/reader permission verification remains the separately authorized
`resource-verify` path below and still requires its explicitly provisioned eight-byte fixture.

Start troubleshooting with the Dashboard comparison in the
[observability runbook](observability-runbook.md#investigation-and-alert-response); do
not inspect or mutate Dashboard runtime state.

## Local development

Keep the following coordinates and exact candidate identity in one shell. The Go deployment owner
loads the image and applies only the Go ConfigMap, Deployment, two Services and NetworkPolicy. It
does not quiesce Java or reconcile the gateway and therefore is not, by itself, an authority
handover. Reader and writer Secrets, Kafka trust and the explicit migration/resource sequence must
already exist:

```sh
export DEPLOY_ENV=local-dev
export DEPLOY_CONTEXT=kind-local-dev-insurance-hub
export DEPLOY_SVC_NS=local-dev-all
export DEPLOY_DATA_NS=local-dev-all
export DEPLOY_IMAGE_REF="insurance-hub-document-api:1.0.0-local.0e53205ebf0b"

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

After an authorized transfer, recovery must return one ready `local-dev-document-api` Pod with the
accepted image/version, `EVENT_WORKERS_ENABLED=true` and group `documents-service`; Java must stay
quiesced and the gateway must stay routed to Go. Do not reset or seed Kafka progress or improvise
credentials, routes or dependency destinations as recovery actions. A different context,
namespace, image or live inventory is a stop condition; do not mutate shared or Java resources
outside the separately authorized handover owner.

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

## Monitoring coordinates and guarded QA operation

The registered monitoring selection is environment `qa`, service `document`, context
`qa-insurance-hub` and namespace `qa-monitoring`. It renders PrometheusRule
`qa-document-service`, ConfigMap `qa-document-dashboard` and Grafana dashboard UID
`insurance-hub-document-service`. The application remains Deployment `qa-document-api` in
`qa-svc`, reconciled by `GitRepository/insurance-hub` and `Kustomization/qa-svc` in `flux-system`.
Monitoring operations do not transfer workload ownership away from Flux.

Validate the candidate without contacting QA before considering a live operation:

```sh
make monitoring-assets-check ENV_NAME=qa SVC_NAME=document
make test-monitoring ENV_NAME=qa SVC_NAME=document
make test-observability-backends OBSERVABILITY_SERVICE_DOMAIN=document
```

These owners validate source/rules, collected disposable Prometheus queries and actual disposable
Alloy-to-backend delivery respectively. They do not apply QA resources or establish live QA
behavior.

Immediately before any separately authorized QA monitoring write or live-signal acceptance,
perform a fresh read-only identity preflight. Read only the exact context/namespaces, Flux
source/applied revision, Document Deployment/Pod/config/image identity, worker-enabled setting and
the two monitoring objects or their verified absence. Record full application image source,
semantic reference, registry digest,
Pod image ID, runtime `service.version`, Pod UID and full monitoring source revision. Do not read
Secret values. Partial, foreign, placeholder or mismatched state is a stop condition; repeat the
preflight after any source, image, configuration, Flux or monitoring-object change.

For a first installation, both monitoring objects must be absent. Run the selected-service
read-only pre-provision check before creating either one:

```sh
: "${MONITORING_SOURCE_REVISION:?set the full reviewed monitoring source revision}"
make verify-service-observability \
  ENV_NAME=qa \
  SVC_NAME=document \
  VERIFY_MODE=pre-provision \
  VERIFY_SOURCE_REVISION="$MONITORING_SOURCE_REVISION"
```

Render the exact objects, retain their hashes and request server admission without persistence:

```sh
export MONITORING_EVIDENCE_DIR="$(mktemp -d)"
make --no-print-directory -C k8s \
  prometheus-rules-render ENV_NAME=qa SVC_NAME=document \
  > "$MONITORING_EVIDENCE_DIR/candidate-rule.yaml"
make --no-print-directory -C k8s \
  grafana-dashboard-render ENV_NAME=qa SVC_NAME=document \
  > "$MONITORING_EVIDENCE_DIR/candidate-dashboard.yaml"
sha256sum \
  "$MONITORING_EVIDENCE_DIR/candidate-rule.yaml" \
  "$MONITORING_EVIDENCE_DIR/candidate-dashboard.yaml"
kubectl --context qa-insurance-hub -n qa-monitoring apply --dry-run=server \
  -f "$MONITORING_EVIDENCE_DIR/candidate-rule.yaml"
kubectl --context qa-insurance-hub -n qa-monitoring apply --dry-run=server \
  -f "$MONITORING_EVIDENCE_DIR/candidate-dashboard.yaml"
```

Confirm exact resource names, namespace, service/QA/Go filters, immutable dashboard setting,
30-second intervals and the legacy Pod exclusion. Rendering is source evidence; server dry-run is
admission evidence. Neither proves application, reconciliation or live delivery.

Every write requires authorization for the exact resources. When granted, apply only through the
guarded selected-service owners:

```sh
make -C k8s prometheus-rules-apply ENV_NAME=qa SVC_NAME=document
make -C k8s grafana-dashboard-apply ENV_NAME=qa SVC_NAME=document
```

Read back the PrometheusRule and ConfigMap, record their UIDs, and compare a fresh render with the
retained candidate. Inspect the active `insurance-hub-document-service` Prometheus group and the
Grafana dashboard by UID. Only then run the separately authorized read-only live verifier with the
full reconciled monitoring source revision:

```sh
make verify-service-observability \
  ENV_NAME=qa \
  SVC_NAME=document \
  VERIFY_MODE=enabled \
  VERIFY_SOURCE_REVISION="$MONITORING_SOURCE_REVISION"
```

`VERIFY_MODE=enabled` means the verifier expects OTel export and the final
`EVENT_WORKERS_ENABLED=true` state. It remains read-only and cannot enable the worker. Shared
generation evidence requires a separately authorized synthetic event.

Stop before or after a monitoring operation for an unexpected context, namespace, Flux/source,
application identity, worker mode, resource name/UID/source, mutable dashboard, missing signal,
unexplained alert, sensitive query or incomplete cleanup. Preserve the first failure and the
sanitized identities before retrying. Do not weaken filters or thresholds to make the check pass.

Application recovery and monitoring restoration are different procedures:

- restore the QA workload only through a reviewed release/source change and Flux reconciliation;
  never patch, scale or directly apply the Deployment;
- restore monitoring from a captured accepted source through the same guarded selected-service
  owners, after verifying the captured resource UIDs still match, under separate restore
  authorization;
- for a proven first installation, removal needs separate authorization for only
  `qa-document-service` and `qa-document-dashboard`, followed by exact absence and shared-stack
  health checks.

Cleanup owns only the attempt's private evidence directory and local verifier processes. Do not
delete shared monitoring releases/PVCs, workload or Java resources, dependency data, Kafka
progress, Dashboard assets or another service's monitoring objects. Retain source/base/head,
render hashes, admission/application/read-back results, UIDs, application/Flux identities, bounded
signal results, restoration and owned cleanup. Keep source, disposable and live evidence distinct
and retain no credentials, event bodies, policy/customer values, object keys, PDF bytes or raw
dependency errors. The companion observability runbook contains the complete identity capture and
investigation sequence.

## Explicit database, writer and template administration

The administrative executable and resource tooling are separate from API startup. Lookup uses the
restricted reader identities only; the final Go worker uses the separate writer identities and
owns ordinary registration after the authorized handover.
`document` owns both fresh and adopted tables/sequences/history. `go_document_migrator` is a
NOINHERIT login that explicitly assumes `document`; `go_document_reader` has SELECT only.
`go_document_writer` has INSERT plus sequence USAGE and no read, update, delete, DDL or owner
membership. MinIO uses separate `document-reader` and `document-writer` identities. The writer is
limited to PutObject, AbortMultipartUpload and ListMultipartUploadParts on `policies/*`. This same
explicit interface creates the fixed jsreport `POLICY` Handlebars/chrome-pdf template only when it
is absent; any existing content, engine or recipe difference blocks without replacement.

Use an independently verified administrative runner with Kubernetes access, psql, mc and network
reachability to the enrolled MinIO DNS endpoint. As with Dashboard administration, the owner reads
the exact platform administrator Secret from Kubernetes only in process memory, supplies the
credential to the child process environment rather than arguments, and sends owned workload Secret
values through Kubernetes API stdin. It reads QA public trust from the target namespace's exact
`kube-root-ca.crt` ConfigMap into a private temporary mc directory and removes that directory after
the operation. Do not supply a separate mc configuration or use `MC_INSECURE`. The tools validate
the selected Tenant configuration Secret and selected/running accepted MinIO digest before resource
setup. A changed image or administrator source needs a new security disposition first.

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
export DOCUMENT_RESOURCE_JSREPORT_ENDPOINT=http://local-dev-jsreport.local-dev-all.svc.cluster.local:5488
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
export DOCUMENT_RESOURCE_JSREPORT_ENDPOINT=http://qa-jsreport.qa-svc.svc.cluster.local:5488
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
creates only absent PostgreSQL reader/migrator/writer, MinIO read/write and jsreport template
resources, and preserves existing
credentials. It removes inherited PUBLIC database/schema access only after checking consumers;
Java's `document` ownership remains usable. In the dedicated Document CNPG cluster, it also
revokes PUBLIC CONNECT on the `postgres` and `template1` maintenance databases so service logins
cannot enter them. A foreign database, maintenance owner or unknown login blocks this operation;
no other service's database cluster is modified. No seed, rotation, revoke or reset is provided.
It creates the following environment-prefixed Secrets in the selected service namespace:

| Secret suffix after environment prefix | Contents and purpose |
| --- | --- |
| `postgres-document-admin-target` | `target.json`: protected environment/context/namespaces/database/host/cluster UID/instance UUID enrollment |
| `postgres-document-reader-creds` | `database-url`, `password`, `ca.crt`: verify-full read login and public CNPG trust |
| `postgres-document-migrator-creds` | `database-url`, `password`, `ca.crt`: administrative login and trust, never mounted in the API |
| `postgres-document-writer-creds` | `database-url`, `password`, `ca.crt`: verify-full INSERT/sequence-USAGE login for generation only |
| `minio-document-reader-creds` | `access-key`, `secret-key`, `endpoint`, `ca.crt`: separate read identity and QA public trust (empty CA for accepted local-dev HTTP) |
| `minio-document-writer-creds` | `access-key`, `secret-key`, `endpoint`, `ca.crt`: separate `policies/*` upload identity and the same explicit trust |

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
print or retain these values as ordinary operational evidence. Each file must be mode 0600. Export
their paths:

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
`uninitialized`, `unmanaged`, `pending`, `version=1` and `version=2`; version reports
`uninitialized`, `0`, `1` or `2`.
An unmanaged compatible schema has no recorded migration version yet. Neither command initializes
history, advances a sequence, creates objects or repairs state. An incompatible schema is an error.

For a fresh installation, infrastructure and resource ensure must already exist; Java is not
needed to initialize the schema. The explicit operation creates the accepted nullable bytea/
policy-number table and increment-1 Hibernate sequence as `document`, records Goose versions 1 and
2, grants reader SELECT, and grants the separate writer INSERT plus sequence USAGE:

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
Reader grants can be restored by repeating the operation. Writer object grants remain owned by the
immutable version-2 migration. A failed or uncertain command needs
read-only status/catalog inspection before retry. Applied SQL is immutable; use forward migrations
for later changes. No down/reset, reseeding, Java startup dependency or ownership transfer exists.

The overall migration defaults to 120s (maximum 180s), advisory acquisition is bounded at 30s,
connection setup at 5s and lock/session queries are bounded. Cleanup has a separate 5s budget.
Private input validation happens before a connection. Errors expose safe classifications rather
than credentials, SQL or raw dependency errors. Shared QA operations require their own scope and
must not bypass Flux workload ownership; these commands do not deploy or change normal traffic.

### Verification and focused disposable smoke

After explicit migration, verification accepts an empty metadata table and tests actual reader and
writer logins, required grants, owner-role assumption and SQLSTATE 42501 denials. PostgreSQL forbidden-operation
probes always roll back and never call nextval; unexpected privileges fail verification. MinIO
verification authenticates the separate read identity, checks the exact policy and private bucket,
and requires an independently provisioned synthetic object `2026/02/SYN-362.pdf` with bytes
`%PDF-362`. Its creation on any shared target requires separately authorized fixture scope.
No customer object is selected for this probe. Verification downloads only that eight-byte fixture
to a private disposable file, then checks denied unrelated-bucket/admin reads without issuing
object writes/deletes. Writer verification uploads one fixed administration probe through the
writer identity, checks it through the administrator and removes it through the administrator;
writer read/list/delete/admin and unrelated-resource requests must be real authorization denials.
The jsreport definition must remain exactly 183 bytes with SHA-256
`2074d32d049fb12d4d3cbacb9f47d27cfcc133ae5ac7ac37f4f8c434ba13db4d`.

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

Disposable integration requires Docker, OpenSSL and psql. PDF text extraction uses the pinned
Poppler image through a network-disabled, read-only disposable container. The complete
`resource-test-integration` suite additionally requires host `mc`; the lookup selected by
`test-integration` and `image-check` instead uses the pinned containerized MinIO client. Missing
pinned PostgreSQL, Chainguard MinIO, MinIO-client, JSReport, Kafka and Poppler images are pulled by
digest.
The suite creates uniquely named/labeled local containers, temporary PostgreSQL storage and private
synthetic TLS/credential files; cleanup removes only those containers/files. It proves fresh
Java-independent setup, compatible adoption/replay, byte/ID/sequence preservation, subsequent safe
ID allocation, schema/history/target rejection, bounded contention, TLS hostname rejection and
actual PostgreSQL/MinIO read and denied-write/admin behavior. It is not evidence of deployed
permissions, QA CA distribution or live network policy. Retain bounded logs in the operation's
private evidence directory when recording a delivery result; do not copy temporary credentials or
database contents.
