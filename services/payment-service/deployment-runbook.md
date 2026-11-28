<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Payment deployment](#payment-deployment)
  - [Candidate checks](#candidate-checks)
  - [Direct local-dev](#direct-local-dev)
  - [Flux-owned QA](#flux-owned-qa)
  - [Private verification and recovery](#private-verification-and-recovery)
    - [Registration worker activation and held records](#registration-worker-activation-and-held-records)
    - [Statement importer qualification](#statement-importer-qualification)
  - [Dependency access](#dependency-access)
    - [PostgreSQL login](#postgresql-login)
    - [MinIO reader](#minio-reader)
    - [Kafka trust](#kafka-trust)
    - [Access checks](#access-checks)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Payment deployment

Use catalog key `payment`. The Go workload is `<environment>-payment-api`, selected by
`app.kubernetes.io/name=payment-api`; the existing Payment workload keeps its own
`app.kubernetes.io/name=payment-api-legacy` selector, Service and gateway route. The workload uses
Pod-reachable business HTTP, loopback-only gRPC and Pod port 8081 for kubelet health. Its
ClusterIP Service exposes only business HTTP, and the environment NetworkPolicy admits only
same-namespace gateway Pods labelled `insurance-hub.io/payment-business-client: "true"`. Operators
use loopback-bound Kubernetes API port-forwards for private qualification. The workload has no
public ingress or workload token. Before it becomes ready it runs one bounded read-only check per
dependency: `SELECT 1` on Payment PostgreSQL as `go_payment_writer`, a one-key list of
`payments-import` on the Payment MinIO tenant and a metadata request for `policy-registered` on
Kafka. It joins no consumer group and reads, writes or commits nothing.

| Target | Context | Namespace | Owner |
| --- | --- | --- | --- |
| local-dev | `kind-local-dev-insurance-hub` | `local-dev-all` | Guarded direct deployment into Kind cluster `local-dev-insurance-hub` |
| QA | `qa-insurance-hub` | `qa-svc` | `flux-system/qa-svc` reconciles `./k8s/overlays/qa/svc` from `flux-system/insurance-hub` on `main` |

## Candidate checks

Run from the repository root with the pinned Python, Go, Docker, Kind and kubectl tools.

```bash
make go-svc-deploy-check go-svc-deploy-test-tooling SVC_NAME=payment
make go-svc-deploy-render SVC_NAME=payment DEPLOY_ENV=local-dev
make go-svc-deploy-render SVC_NAME=payment DEPLOY_ENV=qa
```

The initial QA overlay contains an inactive release placeholder, and the parent Kustomization
does not list `./payment` until release preparation adds it. An activated placeholder, an active
release without a digest, a missing caller label or a selector that reaches another Payment
workload fails the check. Local-dev requires an explicit, source-matched image to perform
operations. For disposable qualification, build a local image as below, then run
`make go-svc-deploy-smoke SVC_NAME=payment` with the selected image and source revision exported.
The smoke copies the three workload Secrets into one labeled Kind namespace and performs only
read-only checks: startup dependencies, account lists, probes, identity, Pod denial, caller
admission and scale-to-zero. It does not migrate, seed or reset environment data. Cleanup verifies
ownership before removing the disposable namespace.

Run `make -C services/payment-service test-integration` for account-list, account-detail,
registration transaction and worker-recovery coverage. Those tests provision their own TLS
PostgreSQL and Kafka containers, apply their own schema and fixtures, and remove the containers
afterward; they do not use local-dev or QA data or the serving Kafka group.

## Direct local-dev

The [dependency access](#dependency-access) Secrets must exist in `local-dev-all` first. From the
clean checkout of the approved image source, build and inspect its local image.

```bash
export DEPLOY_IMAGE_SOURCE_REVISION="$(git rev-parse --verify 'HEAD^{commit}')"
export DEPLOY_IMAGE_REF="insurance-hub-payment-api:1.0.0-local.${DEPLOY_IMAGE_SOURCE_REVISION:0:12}"
GOWORK=off make go-svc-image-check SVC_NAME=payment
docker image inspect "$DEPLOY_IMAGE_REF" --format \
  '{{.Id}} {{index .Config.Labels "org.opencontainers.image.revision"}} {{index .Config.Labels "org.opencontainers.image.version"}}'
```

The image ID is content-addressed. The OCI revision must equal the full source commit and the
version must equal `git-${DEPLOY_IMAGE_SOURCE_REVISION:0:12}`. With local-dev access authorized,
select the configured context and run the guarded operations.

```bash
export DEPLOY_ENV=local-dev DEPLOY_CONTEXT=kind-local-dev-insurance-hub DEPLOY_SVC_NS=local-dev-all
kubectl config use-context "$DEPLOY_CONTEXT"
make go-svc-deploy-preflight SVC_NAME=payment
make go-svc-image-load SVC_NAME=payment
make go-svc-deploy SVC_NAME=payment
make go-svc-deploy-status SVC_NAME=payment
make go-svc-deploy-verify SVC_NAME=payment
```

## Flux-owned QA

The [release workflow](../../.github/workflows/go-release-svc.yml) builds once, pushes the
semantic and source tags, and resolves the registry digest before calling
`go-svc-release-prepare`. Release preparation records the semantic tag, pins the published digest
beside it (catalog `pinDigest`), sets the source-derived runtime version and adds `./payment` to
the parent QA Kustomization in the same release commit. The QA Pod template therefore references
`payment-api:<version>@sha256:<digest>`. Flux can apply that commit immediately, and `qa-svc`
waits for the workload to become ready.

**Before merging any change that releases Payment, create every QA input below.** A missing
Secret leaves the Pod unable to start, a missing login, MinIO identity or trust root fails its
startup check, and either outcome stalls the shared `qa-svc` owner.

| Input | Where | Contents |
| --- | --- | --- |
| Login `go_payment_writer` | `qa-postgres-payment` in `qa-data` | `LOGIN`, `CONNECT` on `payment`; no ownership, membership or `CREATE` (see [PostgreSQL login](#postgresql-login)) |
| Secret `qa-postgres-payment-writer-creds` | `qa-svc` | `database-url`, `ca.crt` from `qa-postgres-payment-ca` |
| MinIO user and policy `payment-reader` | `qa-minio-payment` tenant | Get, put and delete objects plus list and location on `payments-import` only (see [MinIO reader](#minio-reader)) |
| Secret `qa-minio-payment-reader-creds` | `qa-svc` | `endpoint`, `access-key`, `secret-key`, `ca.crt` from the tenant namespace `kube-root-ca.crt` |
| Secret `qa-payment-kafka-ca` | `qa-svc` | `ca.crt` copied from `qa-kafka-cluster-ca-cert` in `qa-data` |

Run the [access checks](#access-checks) against QA and record their outcome before the merge. The
same change adds the caller label to the gateway Pod template, so the gateway rolls once when Flux
applies it.

From the checkout of the applied release configuration, set the full image source and applied
Flux commits from the workflow and Flux records. Set `RELEASE_VERSION` to the published semantic
version. Set `DEPLOY_IMAGE_DIGEST` to the digest recorded by the release workflow.

```bash
: "${DEPLOY_IMAGE_SOURCE_REVISION:?set the full image source commit}"
: "${DEPLOY_SOURCE_REVISION:?set the full Flux-applied configuration commit}"
: "${RELEASE_VERSION:?set the published semantic version}"
: "${DEPLOY_IMAGE_DIGEST:?set the immutable digest recorded by the release workflow}"
export DEPLOY_IMAGE_SOURCE_REVISION DEPLOY_SOURCE_REVISION DEPLOY_IMAGE_DIGEST
export DEPLOY_ENV=qa DEPLOY_CONTEXT=qa-insurance-hub DEPLOY_SVC_NS=qa-svc
unset DEPLOY_IMAGE_REF
image=ghcr.io/igor-baiborodine/insurance-hub/payment-api
observed_digest="$(docker buildx imagetools inspect "$image:$RELEASE_VERSION" \
  --format '{{json .Manifest.Digest}}' | tr -d '"')"
test "$observed_digest" = "$DEPLOY_IMAGE_DIGEST"
docker pull "$image@$DEPLOY_IMAGE_DIGEST"
kubectl config use-context "$DEPLOY_CONTEXT"
kubectl --context "$DEPLOY_CONTEXT" -n flux-system get gitrepository insurance-hub
kubectl --context "$DEPLOY_CONTEXT" -n flux-system get kustomization qa-svc
make go-svc-deploy-status SVC_NAME=payment
make go-svc-deploy-verify SVC_NAME=payment
```

Status checks the exact deployment, ready Pod, Pod UID, version and running image ID. Status and
verification inspect the pulled image, require the rendered QA image to carry the same digest, and
require it to match the ready Pod image ID.

## Private verification and recovery

`go-svc-deploy-verify` resolves the ready Payment Pod and uses private port-forwards for all three
listeners. It checks the HTTP and generated-client gRPC account lists, management `200 live\n` and
`200 ready\n`, the six safe startup identity fields including the Pod UID, and the
`payment database ready` record with `database_role=go_payment_writer`. It then confirms, without
mutation, that each ready gateway Pod has no `payment-service` upstream override, that the
`<environment>-payment-api-legacy` Service still selects only its own ready Pods, and that the
`<environment>-payment-api` Service has a single ready endpoint pointing to the Go Pod.

With explicit authorization for temporary probe Pods, run
`make go-svc-deploy-verify SVC_NAME=payment DEPLOY_DENIAL_PROBE=1`. It checks that an ordinary
workload cannot open any Payment port. A Pod carrying the gateway identity and caller label then
requests the account list, a listed account's detail and a synthetic missing account from the
private Service; an unlabeled Pod must remain unable to reach port 80. The probes retain only
sanitized pass/fail lines and remove their owned Pods.

For manual troubleshooting, forward the exact Pod selected by status and send bounded requests.

```bash
: "${PAYMENT_POD:?set the ready Go Payment Pod name from status}"
kubectl --context "$DEPLOY_CONTEXT" -n "$DEPLOY_SVC_NS" port-forward --address 127.0.0.1 \
  "pod/$PAYMENT_POD" 18080:8080 19090:9090 18081:8081
curl --max-time 5 -sS -w '\n%{http_code}\n' \
  http://127.0.0.1:18080/payment/accounts
make -C services/payment-service _deploy-smoke-rpc PAYMENT_SMOKE_GRPC_ADDR=127.0.0.1:19090
curl --fail --max-time 5 http://127.0.0.1:18081/livez
curl --fail --max-time 5 http://127.0.0.1:18081/readyz
```

Local-dev permits only TCP and UDP 53 to kube-dns, port 5432 of its Payment CNPG Pods, port 9000
in its Payment MinIO tenant namespace and TLS port 9093 of its Strimzi Kafka Pods. QA permits the
same scoped data-plane access and additionally permits OTLP/gRPC to the selected Alloy Pods. A
startup failure names the dependency (`open/verify PostgreSQL`, `initialize/verify MinIO`,
`configure/verify Kafka`); check its Secret, the access checks below and the NetworkPolicy before
anything else.

### Registration worker activation and held records

Current local-dev and QA manifests intentionally leave `EVENT_WORKERS_ENABLED=false`; the Go
workload performs metadata-only Kafka checks while another consumer owns serving group
`payment-service`. Do not enable both consumers: the Go preflight rejects an active serving group,
and changing `KAFKA_GROUP` is not supported.

Enable the Go worker only as part of a separately reviewed and authorized direct cutover after the
current consumer has stopped and the serving group's committed positions have been preserved. Set
`EVENT_WORKERS_ENABLED=true`; leave `KAFKA_GROUP` unset to select its fixed
`payment-service` default. Do not initialize or reset group progress. The disposable qualification
below proves recovery with group `payment-service-qualification` and never touches local-dev or QA:

```bash
make -C services/payment-service test-integration
```

A held registration or uncertain Kafka acknowledgement withdraws readiness and terminates the
serving process. Diagnose it from the fixed `policy registration completed` outcome (`held` or
`commit_uncertain`), `policy registration offset commit failed`, the three bounded registration
metrics documented in the service README, the group's committed next offset and the corresponding
Payment database state. Diagnostics intentionally contain no event body, policy/account identifier,
premium, broker detail or underlying error. Do not print the record, credentials or database URL;
do not delete, edit, skip or manually commit the held offset. Resolve the source or storage
condition through a reviewed change, leave the committed position unchanged, and restart so the
same record is retried. Escalate an uncertain commit for broker/database comparison before any
operator action because the database write may already be durable.

### Statement importer qualification

Current local-dev and QA manifests intentionally leave `STATEMENT_IMPORTS_ENABLED=false`. They bind
the candidate interval explicitly: local-dev uses `STATEMENT_IMPORT_INTERVAL=3m` and QA uses `8h`.
Enable the statement schedule only through a reviewed workload change.

The Payment integration target qualifies the composed scheduler only against disposable
PostgreSQL and a disposable MinIO tenant whose isolated bucket is named `payments-import`. It
controls the UTC clock, overlaps a tick, crosses midnight, resumes earlier sources and posted
receipts, and drains the worker without reaching configured deployment buckets or scheduled files:

```bash
make -C services/payment-service test-integration
```

Use only the bounded `bank statement import completed` outcomes `idle`, `archived`, `review`,
`posted_awaiting_archive` and `dependency_failure` for diagnosis. Logs, metrics and traces omit
object keys, statement rows, account identifiers, amounts and underlying dependency messages.

For source, image, Flux and transport problems, start with
`make go-svc-deploy-status SVC_NAME=payment` and compare the Flux source and applied revisions,
the release digest and the running Pod image ID with the recorded candidate. When QA Flux, DNS or
registry state looks stale, apply the
[QA network recovery runbook](../../k8s/base-cluster-how-tos.md#qa-network-recovery) before
treating the failure as a Payment defect. In local-dev, `make go-svc-stop SVC_NAME=payment` with
the same coordinates and image selection scales only the Go Deployment to zero.

If activation in QA stalls the shared owner, prepare a reviewed recovery commit that removes
`./payment` from `k8s/overlays/qa/svc/kustomization.yaml` and restores
`newTag: 0.0.0-unpublished` and `SERVICE_VERSION=git-000000000000` in the Go Payment QA overlay.
Run `make go-svc-deploy-check SVC_NAME=payment` before publishing that correction, then verify
that `qa-svc` is Ready at the correction commit and `qa-payment-api` is absent.

## Dependency access

The workload reads three Secrets in its service namespace and nothing else. Create each value in a
private directory (`umask 077`) and pass it with `--from-file`, never as a command argument; remove
the directory afterwards. A missing or partial Secret blocks deployment; do not start the workload
with a substitute credential. Set the coordinates once.

```bash
export ENV=local-dev CTX=kind-local-dev-insurance-hub DATA_NS=local-dev-all SVC_NS=local-dev-all
# QA: export ENV=qa CTX=qa-insurance-hub DATA_NS=qa-data SVC_NS=qa-svc
export MINIO_NS="$ENV-minio-payment"
umask 077 && work="$(mktemp -d)"
```

### PostgreSQL login

`go_payment_writer` is a managed login that can connect to `payment` and nothing more: it owns no
object, is a member of no role and cannot create schemas or objects. The database owner `payment`
keeps all schema authority. Run the statements on the primary as the cluster superuser.

```bash
primary="$(kubectl --context "$CTX" -n "$DATA_NS" get cluster.postgresql.cnpg.io \
  "$ENV-postgres-payment" -o jsonpath='{.status.currentPrimary}')"
openssl rand -hex 24 > "$work/password"
printf "CREATE ROLE go_payment_writer LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS NOINHERIT PASSWORD '%s';\nGRANT CONNECT ON DATABASE payment TO go_payment_writer;\n" \
  "$(cat "$work/password")" | kubectl --context "$CTX" -n "$DATA_NS" exec -i "$primary" \
  -c postgres -- psql -v ON_ERROR_STOP=1 -d payment -q
printf 'postgresql://go_payment_writer:%s@%s-postgres-payment-rw.%s.svc.cluster.local:5432/payment?sslmode=verify-full&sslrootcert=/etc/payment-service/postgres/ca.crt' \
  "$(cat "$work/password")" "$ENV" "$DATA_NS" > "$work/database-url"
kubectl --context "$CTX" -n "$DATA_NS" get secret "$ENV-postgres-payment-ca" \
  -o jsonpath='{.data.ca\.crt}' | base64 -d > "$work/postgres-ca.crt"
kubectl --context "$CTX" -n "$SVC_NS" create secret generic "$ENV-postgres-payment-writer-creds" \
  --from-file=database-url="$work/database-url" --from-file=ca.crt="$work/postgres-ca.crt"
```

If the role already exists without its Secret, stop and inspect it; do not reuse an unknown
password or widen its privileges.

### MinIO reader

`payment-reader` may list and locate `payments-import`, get and put its objects, and delete exact
object versions. The pinned MinIO server requires both `s3:DeleteObjectVersion` and
`s3:DeleteObject` for that exact-version request and does not enforce its `s3:versionid` condition.
The policy therefore grants both actions on `payments-import/*`. This accepted learning-project
compatibility risk also permits current-name deletion within that prefix; the Payment importer
must always send the captured version ID and must never issue a current-name delete. The bucket
must have versioning enabled so a source replacement cannot be removed by an in-flight archive.
Configure a private `mc` alias for the tenant administrator from the
tenant's `<tenant namespace>-storage-config`
Secret, entering the credentials at the prompt, through a loopback port-forward to Service
`minio`. In QA, trust the tenant namespace `kube-root-ca.crt` and keep hostname verification.
Keep `endpoint` on the in-cluster Service port for the workload Secret. The administrative alias
uses the local forward's port instead; `mc --resolve` accepts a bare IP on the right and retains
port `19000` from that alias URL.

```bash
port=80 scheme=http; [ "$ENV" = qa ] && port=443 scheme=https
host="minio.$MINIO_NS.svc.cluster.local"
endpoint="$scheme://$host:$port"
admin_endpoint="$scheme://$host:19000"
kubectl --context "$CTX" -n "$MINIO_NS" port-forward --address 127.0.0.1 svc/minio 19000:"$port" &
mkdir -p "$work/mc/certs/CAs" && : > "$work/minio-ca.crt"
[ "$ENV" = qa ] && kubectl --context "$CTX" -n "$MINIO_NS" get configmap kube-root-ca.crt \
  -o jsonpath='{.data.ca\.crt}' > "$work/minio-ca.crt" && cp "$work/minio-ca.crt" "$work/mc/certs/CAs/"
mc() { command mc --config-dir "$work/mc" --resolve "$host:19000=127.0.0.1" "$@"; }
mc alias set payment-admin "$admin_endpoint"
mc version enable payment-admin/payments-import
mc version info payment-admin/payments-import
mc admin policy create payment-admin payment-reader \
  k8s/apps/svc/payment/minio/s3-policy-payments-import.json
openssl rand -hex 20 | tr -d '\n' > "$work/secret-key"
test "$(wc -c < "$work/secret-key")" -eq 40
printf '%s' payment-reader > "$work/access-key"
mc admin user add payment-admin   # enter payment-reader and the contents of $work/secret-key
mc admin policy attach payment-admin payment-reader --user payment-reader
printf '%s' "$endpoint" > "$work/endpoint"
kubectl --context "$CTX" -n "$SVC_NS" create secret generic "$ENV-minio-payment-reader-creds" \
  --from-file=endpoint="$work/endpoint" --from-file=access-key="$work/access-key" \
  --from-file=secret-key="$work/secret-key" --from-file=ca.crt="$work/minio-ca.crt"
```

The local-dev `ca.crt` is empty because local-dev MinIO serves HTTP; the workload reads it only
in QA through `MINIO_CA_FILE`.

### Kafka trust

```bash
kubectl --context "$CTX" -n "$DATA_NS" get secret "$ENV-kafka-cluster-ca-cert" \
  -o jsonpath='{.data.ca\.crt}' | base64 -d > "$work/kafka-ca.crt"
kubectl --context "$CTX" -n "$SVC_NS" create secret generic "$ENV-payment-kafka-ca" \
  --from-file=ca.crt="$work/kafka-ca.crt"
```

### Access checks

Check role attributes, membership and ownership, not only that the login connects. Every row must
match before deployment.

```bash
kubectl --context "$CTX" -n "$DATA_NS" exec -i "$primary" -c postgres -- \
  psql -v ON_ERROR_STOP=1 -d payment -At <<'SQL'
SELECT rolcanlogin, rolsuper, rolcreatedb, rolcreaterole, rolreplication, rolbypassrls
  FROM pg_roles WHERE rolname = 'go_payment_writer';
SELECT count(*) FROM pg_auth_members m JOIN pg_roles r ON r.oid = m.member
  WHERE r.rolname = 'go_payment_writer';
SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname = 'payment';
SELECT (SELECT count(*) FROM pg_class WHERE relowner = 'go_payment_writer'::regrole)
     + (SELECT count(*) FROM pg_namespace WHERE nspowner = 'go_payment_writer'::regrole);
SELECT has_database_privilege('go_payment_writer', 'payment', 'CONNECT'),
       has_database_privilege('go_payment_writer', 'payment', 'CREATE'),
       has_schema_privilege('go_payment_writer', 'public', 'CREATE');
SQL
mc admin user info payment-admin payment-reader
mc admin policy info payment-admin payment-reader --policy-file "$work/payment-reader-effective.json"
jq -S '(.Statement[].Action) |= sort | (.Statement[].Resource) |= sort |
  .Statement |= sort_by(.Resource | join(","))' \
  k8s/apps/svc/payment/minio/s3-policy-payments-import.json > "$work/payment-reader-expected.json"
jq -S '(.Statement[].Action) |= sort | (.Statement[].Resource) |= sort |
  .Statement |= sort_by(.Resource | join(","))' \
  "$work/payment-reader-effective.json" > "$work/payment-reader-observed.json"
cmp "$work/payment-reader-expected.json" "$work/payment-reader-observed.json"
kubectl --context "$CTX" -n "$SVC_NS" get secret "$ENV-postgres-payment-writer-creds" \
  "$ENV-minio-payment-reader-creds" "$ENV-payment-kafka-ca" \
  -o go-template='{{range .items}}{{.metadata.name}}:{{range $key, $_ := .data}} {{$key}}{{end}}{{"\n"}}{{end}}'
kill %1; rm -rf -- "$work"
```

Expect `t|f|f|f|f|f`, `0` memberships, owner `payment`, `0` owned objects and `t|f|f`. The MinIO
user must be enabled with only policy `payment-reader`, and the normalized effective policy must
match the checked-in six-action policy byte for byte. Bucket versioning must report `Enabled`.
Effective qualification must then use the workload Secret—not the administrative alias—to
exercise GetBucketLocation, ListBucket, PutObject, GetObject and DeleteObjectVersion on one
uniquely prefixed synthetic object. Capture the version ID returned by PutObject and delete that
exact version. Verify the object is absent afterward and that ListBucket is denied on a known
outside bucket. Do not repeat the known current-name delete capability; remove every owned
synthetic resource before recording success. The Secrets must carry exactly
`database-url,ca.crt`, `endpoint,access-key,secret-key,ca.crt` and `ca.crt`; read the key names
only, never print the values. The workload's own startup checks then prove the connection, bucket
list and topic metadata from inside the Pod.
