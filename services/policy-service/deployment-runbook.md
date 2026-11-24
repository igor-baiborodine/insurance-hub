<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Policy deployment](#policy-deployment)
  - [Candidate checks](#candidate-checks)
  - [Direct local-dev](#direct-local-dev)
  - [Flux-owned QA](#flux-owned-qa)
  - [Private smoke and recovery](#private-smoke-and-recovery)
  - [PostgreSQL storage (local-dev)](#postgresql-storage-local-dev)
    - [Target selection](#target-selection)
    - [Fresh installation order](#fresh-installation-order)
    - [Access contract](#access-contract)
    - [Populated compatibility and replay](#populated-compatibility-and-replay)
    - [Refusals and retry](#refusals-and-retry)
    - [Post-qualification disposition](#post-qualification-disposition)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Policy deployment

Use catalog key `policy`. The Go workload is `<environment>-policy-api`, selected by
`app.kubernetes.io/name=policy-api`; other Policy workloads keep their own selectors. The workload
uses Pod loopback for business HTTP and gRPC, and Pod port 8081 for kubelet health.
Operators use a loopback-bound Kubernetes API port-forward. The workload has no Kubernetes
Service, ingress route or workload token. In local-dev only, it reads Policy PostgreSQL as
`go_policy_writer`; QA receives no database settings until its own rollout (see
[PostgreSQL storage](#postgresql-storage-local-dev)).

| Target | Context | Namespace | Owner |
| --- | --- | --- | --- |
| local-dev | `kind-local-dev-insurance-hub` | `local-dev-all` | Guarded direct deployment into Kind cluster `local-dev-insurance-hub` |
| QA | `qa-insurance-hub` | `qa-svc` | `flux-system/qa-svc` reconciles `./k8s/overlays/qa/svc` from `flux-system/insurance-hub` on `main` |

## Candidate checks

Run from the repository root with the pinned Python, Go, Docker, Kind and kubectl tools.

```bash
make go-svc-deploy-check go-svc-deploy-test-tooling SVC_NAME=policy
make go-svc-deploy-render SVC_NAME=policy DEPLOY_ENV=local-dev
make go-svc-deploy-render SVC_NAME=policy DEPLOY_ENV=qa
```

The QA overlay uses an inactive release placeholder until release preparation activates the Policy
resources. An activated placeholder fails the check. Local-dev requires an explicit,
source-matched image to perform operations. For disposable qualification, build a local image as
below, then run
`make go-svc-deploy-smoke SVC_NAME=policy` with the selected image and source revision exported.
The smoke scopes its writes to one labeled Kind namespace and removes that namespace afterward.
It runs only the private workload checks (Echo, probes, identity, Pod denial and scale-to-zero).
Shared routing and coexistence checks run only through `go-svc-deploy-verify` against local-dev and
QA.

## Direct local-dev

From the clean checkout of the approved image source, build and inspect its local image.

```bash
export DEPLOY_IMAGE_SOURCE_REVISION="$(git rev-parse --verify 'HEAD^{commit}')"
export DEPLOY_IMAGE_REF="insurance-hub-policy-api:1.0.0-local.${DEPLOY_IMAGE_SOURCE_REVISION:0:12}"
GOWORK=off make go-svc-image-check SVC_NAME=policy
docker image inspect "$DEPLOY_IMAGE_REF" --format \
  '{{.Id}} {{index .Config.Labels "org.opencontainers.image.revision"}} {{index .Config.Labels "org.opencontainers.image.version"}}'
```

The image ID is content-addressed. The OCI revision must equal the full source commit and
the version must equal `git-${DEPLOY_IMAGE_SOURCE_REVISION:0:12}`. With local-dev access
authorized, select the configured context and run the guarded operations.

```bash
export DEPLOY_ENV=local-dev DEPLOY_CONTEXT=kind-local-dev-insurance-hub DEPLOY_SVC_NS=local-dev-all
kubectl config use-context "$DEPLOY_CONTEXT"
make go-svc-deploy-preflight SVC_NAME=policy
make go-svc-image-load SVC_NAME=policy
make go-svc-deploy SVC_NAME=policy
make go-svc-deploy-status SVC_NAME=policy
make go-svc-deploy-verify SVC_NAME=policy
```

## Flux-owned QA

The [release workflow](../../.github/workflows/go-release-svc.yml) builds once, pushes the
semantic and source tags, and resolves the registry digest before calling
`go-svc-release-prepare`. Release preparation records the semantic tag, pins the published
digest beside it (catalog `pinDigest`), sets the source-derived runtime version and adds
`./policy` to the parent QA Kustomization in the same release commit. The QA Pod template
therefore references `policy-api:<version>@sha256:<digest>`, and the render check rejects an
active QA release without a digest. Keep image source and Flux configuration commits separate.

From the checkout of the applied release configuration, set the full image source and applied
Flux commits from the workflow and Flux records. Set `RELEASE_VERSION` to the published
semantic version. Set `DEPLOY_IMAGE_DIGEST` to the digest recorded by the release workflow.

```bash
: "${DEPLOY_IMAGE_SOURCE_REVISION:?set the full image source commit}"
: "${DEPLOY_SOURCE_REVISION:?set the full Flux-applied configuration commit}"
: "${RELEASE_VERSION:?set the published semantic version}"
: "${DEPLOY_IMAGE_DIGEST:?set the immutable digest recorded by the release workflow}"
export DEPLOY_IMAGE_SOURCE_REVISION DEPLOY_SOURCE_REVISION DEPLOY_IMAGE_DIGEST
export DEPLOY_ENV=qa DEPLOY_CONTEXT=qa-insurance-hub DEPLOY_SVC_NS=qa-svc
unset DEPLOY_IMAGE_REF
image=ghcr.io/igor-baiborodine/insurance-hub/policy-api
observed_digest="$(docker buildx imagetools inspect "$image:$RELEASE_VERSION" \
  --format '{{json .Manifest.Digest}}' | tr -d '"')"
test "$observed_digest" = "$DEPLOY_IMAGE_DIGEST"
docker pull "$image@$DEPLOY_IMAGE_DIGEST"
kubectl config use-context "$DEPLOY_CONTEXT"
kubectl --context "$DEPLOY_CONTEXT" -n flux-system get gitrepository insurance-hub
kubectl --context "$DEPLOY_CONTEXT" -n flux-system get kustomization qa-svc
make go-svc-deploy-status SVC_NAME=policy
make go-svc-deploy-verify SVC_NAME=policy
```

Status checks the exact deployment, ready Pod, Pod UID, version and running image ID. Use a
digest-qualified image reference for OCI revision and version checks. Status and verification
inspect that pulled image, require the rendered QA image to carry the same digest, and require it
to match the ready Pod image ID.

## Private smoke and recovery

`go-svc-deploy-verify` resolves the ready Policy Pod and uses private port-forwards for all
three listeners. It checks HTTP and generated-client gRPC Echo of `hello`, management
`200 live\n` and `200 ready\n`, and the six safe startup identity fields, including the Pod
UID. It also reads the configured gateway and external Policy endpoint contract without mutation
to ensure this private workload does not change shared routing. For manual troubleshooting, forward
the exact Pod selected by status.

```bash
: "${POLICY_POD:?set the ready Go Policy Pod name from status}"
kubectl --context "$DEPLOY_CONTEXT" -n "$DEPLOY_SVC_NS" port-forward --address 127.0.0.1 \
  "pod/$POLICY_POD" 18080:8080 19090:9090 18081:8081
```

With explicit authorization for the temporary probe Pod, run
`make go-svc-deploy-verify SVC_NAME=policy DEPLOY_DENIAL_PROBE=1`. It also checks that
an ordinary workload cannot open any Policy port and removes its own probe Pod.

In another terminal, use bounded requests against the loopback ports.

```bash
curl --fail --max-time 5 -H 'Content-Type: application/json' \
  -d '{"message":"hello"}' http://127.0.0.1:18080/v1/example/echo
make -C services/policy-service _deploy-smoke-rpc POLICY_SMOKE_GRPC_ADDR=127.0.0.1:19090
curl --fail --max-time 5 http://127.0.0.1:18081/livez
curl --fail --max-time 5 http://127.0.0.1:18081/readyz
```

Local-dev permits only TCP and UDP 53 to kube-dns and disables telemetry. QA additionally
permits OTLP/gRPC to the selected Alloy Pods. Inspect the six identity fields in the Policy
startup record and verify corresponding telemetry by `service.name=policy-service`, `service.version` and `service.instance.id` through
Alloy's existing signal paths. Keep request payloads out of operational evidence.

Use the [Dashboard runbook](../dashboard-service/deployment-runbook.md) first for source,
image, Flux and transport troubleshooting. When QA Flux, DNS or registry state looks stale, apply
the [QA network recovery runbook](../../k8s/base-cluster-how-tos.md#qa-network-recovery) before
treating the failure as a Policy defect. In local-dev, `make go-svc-stop SVC_NAME=policy`
with the same coordinates and image selection scales only the Go Deployment to zero.
Redeploy the approved previous Go image by selecting its full source revision and tag and
rerunning the direct deployment steps.

If activation in QA stalls the shared owner, prepare a reviewed recovery commit that removes
`./policy` from `k8s/overlays/qa/svc/kustomization.yaml` and restores
`newTag: 0.0.0-unpublished` and `SERVICE_VERSION=git-000000000000` in the Go Policy QA overlay.
Run `make go-svc-deploy-check SVC_NAME=policy` before publishing that correction. After Flux
applies it, verify the applied revision and pruning.

```bash
kubectl --context qa-insurance-hub -n flux-system get kustomization qa-svc -o yaml
kubectl --context qa-insurance-hub -n qa-svc get deployment qa-policy-api --ignore-not-found
```

Expect the shared owner to be Ready at the reviewed correction commit, the Go Deployment to be
absent and the remaining shared inventory to stay healthy.

## PostgreSQL storage (local-dev)

The local-dev overlay injects `POLICY_DATABASE_URL` from Secret
`local-dev-postgres-policy-writer-creds` and mounts only its `ca.crt` at
`/etc/policy-service/postgres`. Egress is limited to DNS and port 5432 of the
`local-dev-postgres-policy` CNPG Pods. Readiness fails until the writer's bounded query reads every
Policy table, and the Pod logs `policy database ready` with `database_role=go_policy_writer`.
`go-svc-deploy-verify` requires both. The base and QA overlay carry no database settings, so a
release cannot break the QA workload before its own provisioning.

### Target selection

The only local-dev binding is context `kind-local-dev-insurance-hub`, namespace `local-dev-all`.
Fresh qualification deliberately replaces the deployed Policy database, storage, administration
Secrets and private Go workload in that namespace. This manual reset is limited to Policy-owned
resources and is never run in QA or against unrelated resources.

```bash
export NS=local-dev-all
export POLICY_RESOURCE_ENV=local-dev POLICY_RESOURCE_CONTEXT=kind-local-dev-insurance-hub \
  POLICY_RESOURCE_DATA_NS="$NS" POLICY_RESOURCE_SVC_NS="$NS" \
  POLICY_RESOURCE_CLUSTER=local-dev-postgres-policy \
  POLICY_RESOURCE_DB_HOST="local-dev-postgres-policy-rw.$NS.svc.cluster.local"
export DEPLOY_ENV=local-dev DEPLOY_CONTEXT=kind-local-dev-insurance-hub DEPLOY_SVC_NS="$NS"
kubectl config use-context kind-local-dev-insurance-hub
```

### Fresh installation order

1. Record the existing Policy Cluster UID, PVCs, Secrets and private Go resources. Manually delete
   only `cluster.postgresql.cnpg.io/local-dev-postgres-policy`, PVCs labeled
   `cnpg.io/cluster=local-dev-postgres-policy`, Secrets
   `local-dev-postgres-policy-user-creds`, `local-dev-postgres-policy-admin-target`,
   `local-dev-postgres-policy-migrator-creds` and
   `local-dev-postgres-policy-writer-creds`, plus Deployment
   `local-dev-policy-api`, ConfigMap `local-dev-policy-config` and NetworkPolicy
   `local-dev-policy-api`. Wait for deletion and confirm the empty Policy start. Do not turn this
   one-time reset into a Make target or delete any unrelated resource.
2. Create the bootstrap owner credential from a mode-0600 file, then the cluster:
   `make -C k8s postgres-svc-secret-create SVC_NAME=policy DATA_NS="$NS" SVC_NS="$NS"
   PG_SVC_USER_PWD_FILE=/absolute/path` and `make -C k8s postgres-svc-deploy SVC_NAME=policy
   DATA_NS="$NS" SVC_NS="$NS"`. `postgres-svc-status` tolerates diagnostic errors, so check the
   cluster directly: `kubectl -n "$NS" get cluster.postgresql.cnpg.io local-dev-postgres-policy
   -o jsonpath='{.metadata.uid} {.status.phase} {.status.currentPrimary}'` must report a new UID,
   `Cluster in healthy state` and a ready primary.
3. `make -C services/policy-service resource-inspect` reports missing roles and Secrets without
   mutation. Then `POLICY_RESOURCE_CONFIRM=ensure-local-dev-policy-resources make -C
   services/policy-service resource-ensure` publishes the enrollment and both credential Secrets,
   creates the logins and marker, and runs `resource-verify`.
4. `POLICY_MIGRATION_OPERATION=status make -C services/policy-service migration` reports
   `uninitialized`. Then `POLICY_MIGRATION_OPERATION=migrate
   POLICY_MIGRATION_CONFIRM=migrate-local-dev-policy-schema make -C services/policy-service
   migration` returns `version=1`, and status confirms it. The command runs the checkout's migrator
   through an owned port-forward to the primary. It writes the enrollment, CA and tunnel URL only
   to a private temporary directory.
5. `make -C services/policy-service resource-verify` now also proves writer reads and SQLSTATE
   42501 denials for DDL, DELETE, migration history and enrollment.
6. Build, load and deploy the source-matched image with the [direct local-dev](#direct-local-dev)
   commands, using `DEPLOY_SVC_NS="$NS"`.

### Access contract

| Principal | Holds | Allowed |
| --- | --- | --- |
| `postgres` (CNPG superuser) | Enrollment schema `policy_admin` | Runs ensure/verify through `kubectl exec`; writes the marker once |
| `policy` (CNPG owner) | Database and every Policy table | Owner privileges; DDL only through the migrator's `SET ROLE` |
| `go_policy_migrator` | Secret `local-dev-postgres-policy-migrator-creds` (operator only) | `CONNECT`; `SET ROLE policy`; reads the enrollment marker |
| `go_policy_writer` | Secret `local-dev-postgres-policy-writer-creds` (workload) | `CONNECT`; `USAGE` on `public`; `SELECT, INSERT, UPDATE` on the six business tables and `policy_outbox` |

`PUBLIC` has no database or `public` schema privileges. Migration reapplies the writer grants on
every run, so fresh and adopted schemas converge on the same access. Credentials stay in their
Secrets and temporary 0600 files; never place a value in arguments, logs or evidence.

### Populated compatibility and replay

Populated compatibility is qualified on disposable PostgreSQL rather than a serving database.
`make -C services/policy-service test-integration` loads an independent compatibility fixture,
checks its baseline and adopts it. It compares all six table fingerprints, verifies the generated
IDs `2/3/7/9`, replays without rewinding an allocator, and then exercises reads, atomic issuance
and rollback under the writer login. Applying adoption to a serving database is a separate
operation outside this runbook.

### Refusals and retry

- Status and migrate refuse a mismatched enrollment, a partial or incompatible schema, invalid
  history, or an allocator behind its table. A refused run changes no schema, data or history.
  Fix the reported cause and rerun; never repair history by hand.
- An interrupted ensure has already published its Secrets. Rerun it: it reuses those credentials
  and refuses a role that exists without its Secret. Inspect before acting on any other drift.
- If readiness stays false, run `resource-verify` and migration status, then check that the
  writer Secret's CA equals `local-dev-postgres-policy-ca` and that the NetworkPolicy reaches the
  CNPG Pods.

### Post-qualification disposition

Retain the newly qualified Policy cluster, administration resources and private Go workload for
ongoing local-dev use. Do not restore the replaced database state after qualification.
