<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Product Go deployment and database-initialization runbook](#product-go-deployment-and-database-initialization-runbook)
  - [Ownership and release contract](#ownership-and-release-contract)
  - [Time and lifecycle budgets](#time-and-lifecycle-budgets)
  - [Repository and disposable validation](#repository-and-disposable-validation)
  - [Select and verify an environment](#select-and-verify-an-environment)
  - [Provision and verify roles, marker, and Secrets](#provision-and-verify-roles-marker-and-secrets)
  - [Build and initialize local-dev](#build-and-initialize-local-dev)
  - [Publish and stage the QA release](#publish-and-stage-the-qa-release)
    - [Advance a manifest-only QA source](#advance-a-manifest-only-qa-source)
    - [Retry the same QA release](#retry-the-same-qa-release)
  - [Activate and verify QA](#activate-and-verify-qa)
  - [Abort, stop, and restoration](#abort-stop-and-restoration)
  - [Credential rotation and revocation](#credential-rotation-and-revocation)
  - [Evidence and handoff](#evidence-and-handoff)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Product Go deployment and database-initialization runbook

This runbook covers the Product Go API and the explicit Product schema/catalog administration
introduced by issue #190. Java Product is absent from current local-dev and QA desired state. Its
historical schema and fixtures remain compatibility evidence, but no Java workload owns startup DDL,
seeding, fallback, or recovery.

The API starts with the reader Secret only. Database migration and base-catalog seeding run in a
separate bounded Job before the matching API release. Merging the administration assets or a dormant
QA release tuple does not authorize a live operation. Local-dev and QA qualification are separate,
sequential operations and require the environment scope stated in the issue #190 delivery plan.

## Ownership and release contract

| Resource or action | Owner and boundary |
| --- | --- |
| Product database and bootstrap owner | CloudNativePG/platform automation; application tooling does not create or replace the database |
| Reader, migrator, seeder, target marker, and workload Secrets | `postgres-svc-role-* SVC_NAME=product ROLE_PROFILE=administration`; explicit environment coordinates and administrative cluster access required |
| Schema/history | `/product-migrate`; the migrator login may `SET ROLE product`, while the API and seeder cannot |
| Base catalog | `/product-seed` with the packaged `/usr/share/insurance-hub/product/catalogs/base.json`; insert-only and non-production enrollment required |
| Initialization Job | Product administration overlay; migration init container then seed container, separate credentials, 300-second deadline, no TTL |
| API | Stable local/QA Go Product owner; reader Secret only |
| QA release tuple | **Go Service Release**; immutable Product source, exact image digest, release-specific prerequisite, and stable API dependency |
| QA activation | Reviewed removal of Product from the shared QA owner and addition of `./releases/product/release.yaml` to `k8s/flux/qa/kustomization.yaml` after prerequisites and explicit live scope |

Release identity is the SHA-256 of the full source commit, image digest, migration and catalog
SHA-256 values, target-contract revision, credential revision, and normalized migration timeout.
Resource names use its collision-checked 16-hex prefix. Equivalent timeout spellings such as
`120s` and `2m` produce the same identity. A changed effective input produces a new identity.

The release-specific source is `qa-product-release-<id>`. QA initialization is
`qa-product-init-<id>-a<attempt>` and the stable API owner is `qa-product-api`. Both use the same
immutable source and image digest. The API depends on that exact initialization owner. An earlier
successful Job cannot unlock a new release.

## Time and lifecycle budgets

| Operation | Bound |
| --- | --- |
| Migration overall | Default `120s`; greater than zero and at most `180s` |
| Database connection | 5 seconds within the remaining operation deadline |
| Advisory lock | 30 seconds maximum, with one-second probes |
| SQL statement / row or DDL lock | 15 seconds / 5 seconds |
| Seed overall | 60 seconds |
| Independent cleanup | 5 seconds |
| Job / termination grace | 300 seconds / 15 seconds |
| Flux prerequisite / local orchestration | 6 minutes / 7 minutes |

`product-migrate --timeout` establishes one deadline before connection and carries it through target
inspection and migration. It never extends an earlier caller deadline. Invalid, nonpositive, or
greater-than-`180s` values fail before connection. The 300-second Job budget reserves at least 60
seconds beyond the maximum migration and seed budgets for startup, cleanup, and termination.

Jobs use `restartPolicy: Never`, `backoffLimit: 0`, and no TTL. A failed or ambiguous attempt is
retained. Inspect it, correct the cause, and create a new attempt identity. Do not patch an immutable
Job, delete a failed Job automatically, or reuse an old success. Deleting the current successful Job
may cause Flux to recreate it; migration and seeding must replay safely.

## Repository and disposable validation

Run checks from the repository root. They install no tools and contact no shared database unless an
operator explicitly invokes a live role or deployment action.

```sh
make -C services/product-service check-ci
make go-svc-image-check SVC_NAME=product
make go-svc-deploy-check SVC_NAME=product
make go-svc-deploy-test-tooling SVC_NAME=product
make go-svc-release-test
make go-svc-dispatch-test
make postgres-svc-role-test
make go-svc-deploy-test-flux SVC_NAME=product
```

The Flux test creates a uniquely named Kind cluster, installs the pinned checked-in Flux components
and CloudNativePG 1.27.0, and deletes only that owned cluster. It proves failure gating, old-success
rejection, retry identity, Job recreation, safe replay, Product resource adoption, unrelated resource
preservation, and effective restricted-role SQL denial. It must not receive a shared database URL.
Rendered or fake-controller results do not replace this test and none of these results proves a live
environment.

## Select and verify an environment

The laptop runs local-dev and QA sequentially. Stop task-owned forwards and the previous environment
before switching. Recheck context, namespaces, CNPG identity and readiness, clock health, capacity,
source revision, image, catalog, roles, and unexpected writers after every switch.

| Input | local-dev | QA |
| --- | --- | --- |
| `DEPLOY_ENV` / `POSTGRES_ROLE_ENV` | `local-dev` | `qa` |
| Context | `kind-local-dev-insurance-hub` | `qa-insurance-hub` |
| Service namespace | `local-dev-all` | `qa-svc` |
| Data namespace | `local-dev-all` | `qa-data` |
| CNPG cluster | `local-dev-postgres-product` | `qa-postgres-product` |
| Database host | `local-dev-postgres-product-rw.local-dev-all.svc.cluster.local` | `qa-postgres-product-rw.qa-data.svc.cluster.local` |
| API image | Reviewed local version/revision tag | Published semantic tag and recorded digest |

Export one complete selection. For local-dev:

```sh
export DEPLOY_ENV=local-dev
export DEPLOY_CONTEXT=kind-local-dev-insurance-hub
export DEPLOY_SVC_NS=local-dev-all
export DEPLOY_DATA_NS=local-dev-all
export DEPLOY_IMAGE_REF='insurance-hub-product-api:<version>-local.<12-hex-source>'

export POSTGRES_ROLE_ENV="$DEPLOY_ENV"
export POSTGRES_ROLE_CONTEXT="$DEPLOY_CONTEXT"
export POSTGRES_ROLE_SVC_NS="$DEPLOY_SVC_NS"
export POSTGRES_ROLE_DATA_NS="$DEPLOY_DATA_NS"
export POSTGRES_ROLE_CLUSTER=local-dev-postgres-product
export POSTGRES_ROLE_SECRET=local-dev-postgres-product-reader-creds
export POSTGRES_ROLE_DB_HOST=local-dev-postgres-product-rw.local-dev-all.svc.cluster.local
```

Use the QA column for QA. Set `DEPLOY_SOURCE_REVISION` to the full revision actually reported by
Flux when running QA status/verification. The deployment tool never changes the active context or
namespace and rejects coordinates that differ from the selected environment.

Before administration, confirm that no Java Product Deployment or Service exists and no unexpected
writer/session or role membership can interfere. Stop and report a mismatch. Do not scale, fence,
delete, or repair an unexpected workload automatically.

## Provision and verify roles, marker, and Secrets

Inventory and retain a sanitized snapshot before mutation:

```sh
make postgres-svc-role-inventory SVC_NAME=product ROLE_PROFILE=administration
make postgres-svc-role-snapshot SVC_NAME=product ROLE_PROFILE=administration
```

Provisioning creates or reconciles:

- `go_product_reader`: login, no inheritance, Product table read only;
- `go_product_migrator`: login, no inheritance, may `SET ROLE product` and cannot administer it;
- `go_product_seeder`: login, no inheritance, Product/history reads plus insert-only Product access
  after migration applies grants;
- `product_admin.target_identity`: a `postgres`-owned single-row marker with environment, generated
  instance UUID, CNPG Cluster UID, and seed policy;
- separate reader, migrator, seeder, and target Secrets in the service namespace.

Each credential Secret contains only `database-url` and `ca.crt`. The target Secret contains only
`target.json` and no credential. The API references only the reader Secret.

```sh
make postgres-svc-role-provision SVC_NAME=product ROLE_PROFILE=administration
make postgres-svc-role-verify SVC_NAME=product ROLE_PROFILE=administration
make postgres-svc-role-snapshot SVC_NAME=product ROLE_PROFILE=administration
```

Verification opens new TLS connections with the actual Secrets. It proves reader access, reader
write/DDL denial, migrator owner-role assumption, seeder update/delete/DDL denial, and marker-write
denial using SQLSTATE `42501`. A role without its matching Secret, a changed CNPG UID, or a marker
that differs from its protected target is a recovery condition; provisioning does not silently
replace it.

## Build and initialize local-dev

Build and inspect the intended image, tag it with the local release convention, and load it:

```sh
make go-svc-image-check SVC_NAME=product
make go-svc-deploy-check SVC_NAME=product
make go-svc-deploy-preflight SVC_NAME=product
make go-svc-image-load SVC_NAME=product
```

The reviewed service catalog at `scripts/go/service/catalog.json` owns each deployment target.
For `product.deploymentTargets.local-dev.initialization`, set `migrationTimeout`, `targetRevision`,
`credentialRevision` and `attempt` before deploying. Increment `attempt` only after retaining and
inspecting a failed or ambiguous run. The same fields apply to every registered service and target;
there are no service-prefixed release variables.

`go-svc-deploy` verifies the administration profile, computes the full release identity from the
local image and repository assets, dry-runs and applies the release-specific Job, waits at most seven
minutes for that exact Job, re-verifies administration, then applies the API and waits for rollout.
The API step must leave the post-initialization catalog/schema identity unchanged.

```sh
make go-svc-deploy SVC_NAME=product
make go-svc-deploy-status SVC_NAME=product DEPLOY_EXPECTED_GATEWAY_PHASE=go
make go-svc-deploy-verify SVC_NAME=product DEPLOY_EXPECTED_GATEWAY_PHASE=go
```

For a fresh database, expect migration history version 1 and the four base products. For adoption,
expect the compatible table and every existing row/definition to remain while missing base products
are inserted. Extra or modified rows are preserved. Incompatible schema/history, target mismatch,
or unexpected permissions must abort before API rollout.

## Publish and stage the QA release

A qualifying canonical `main` push runs **Go Service Release**. It builds once, publishes the
semantic and source tags, resolves the immutable image digest, and prepares the service's configured
`releaseTargets`. The workflow contains no Product or QA branches. Product currently selects `qa`
in `scripts/go/service/catalog.json`; its target configuration owns the bundle path, resource names,
time budgets, target/credential revisions and retry attempt.

For a target with `initialization`, release preparation writes only its source/init/API bundle.
It does not advance the API overlay's tag while the shared owner is still active. For a target
without initialization, the same tooling updates the configured overlay's semantic image tag.
This capability choice is explicit catalog data, not a service-name special case.

The shared implementation lives in `scripts/go/service/release.py` and
`scripts/go/service/deployment.py`. Product's entry point delegates to that lifecycle engine;
`services/product-service/scripts/product_deployment_checks.py` owns only the Product workload,
SQL, reader, gateway and compatibility checks. A future service registers targets and supplies its
own checks without copying release generation or Job ordering logic.

The administration Kustomize roots are `k8s/apps/svc/product/base/administration` and
`k8s/overlays/<environment>/svc/product/administration`. Parent API kustomizations deliberately
exclude those directories from `resources`; directory nesting does not enroll a Job in API rollout.

The root `k8s/flux/qa/kustomization.yaml` includes only `./flux-system`, so the generated Product
tuple is dormant. Confirm the release source SHA, image digest, migration/catalog hashes, normalized
timeout, target and credential revisions, Job name, source references, API dependency, and absence
of mutable tags in the tuple. A successful release workflow does not prove QA pull access or grant
authorization for activation.

### Advance a manifest-only QA source

**Go Service Release** runs only when a qualifying Go service build input changes. A reviewed
change limited to Kubernetes manifests, initialization configuration, or other release assets does
not publish an image or advance the immutable QA tuple automatically. If the existing Product image
is still the intended executable, advance the tuple manually after the manifest change is merged to
canonical `main`. This creates a new release identity from the later full source commit while
retaining the previously published immutable image digest as a separate identity input.

Use a clean checkout at the exact canonical commit that owns the desired manifests. Fetch `main`,
resolve its full commit, and require that the checkout contains no tracked change:

```sh
git fetch origin main
source_commit="$(git rev-parse 'origin/main^{commit}')"
test "$(git rev-parse 'HEAD^{commit}')" = "$source_commit"
test -z "$(git status --porcelain --untracked-files=no)"
```

Select the image from the currently reviewed and qualified Product tuple, not from a mutable tag.
In `k8s/flux/qa/releases/product/release.yaml`, require the stable `qa-product-api` owner, its exact
prerequisite, and their release annotations to agree on `insurance-hub.io/image-digest`. Record the
semantic Product version that originally published that digest. Resolve the version in the registry
again and require the registry result to equal the recorded digest:

```sh
image='ghcr.io/igor-baiborodine/insurance-hub/product-api'
release_version='<existing-published-1.x-version>'
image_digest='sha256:<recorded-64-hex-digest>'
resolved_digest="$(docker buildx imagetools inspect "$image:$release_version" \
  --format '{{json .Manifest.Digest}}' | tr -d '"')"
test "$resolved_digest" = "$image_digest"
```

Stop if the active tuple is unavailable, its source/init/API annotations disagree, the registry
digest differs, the image is no longer qualified for the desired manifests, or the source commit
contains a Go build change that needs a normal published release. Do not substitute `latest`, a
source tag, or a locally built image.

Generate the new tuple through the existing generic interface. `RELEASE_VERSION` identifies the
already published image version; the initialization-enabled target continues to pin the digest:

```sh
make go-svc-release-prepare \
  RELEASE_SERVICE=product RELEASE_TARGET=qa \
  RELEASE_REPOSITORY=igor-baiborodine/insurance-hub \
  RELEASE_VERSION="$release_version" \
  RELEASE_SOURCE_COMMIT="$source_commit" \
  RELEASE_IMAGE_DIGEST="$image_digest" \
  RELEASE_OUTPUT=/tmp/product-release-paths
```

Review the complete generated bundle and its exact output path before staging anything:

```sh
git diff --check -- k8s/flux/qa/releases/product/release.yaml
git diff -- k8s/flux/qa/releases/product/release.yaml
make go-svc-deploy-check SVC_NAME=product
make go-svc-release-test
```

Require all of the following before proposing the tuple:

- the new source annotation and GitRepository `spec.ref.commit` equal `source_commit`;
- the init and API image digests equal the previously published `image_digest`;
- the migration and catalog annotation hashes equal the files at `source_commit`;
- target revision, credential revision, normalized timeout, and configured attempt have only the
  changes intended by the reviewed manifest work;
- the new full release identity is consistent on its source, init owner, and stable API owner;
- the stable API owner depends on the new exact initialization owner; and
- every prior generated source and initialization owner remains byte-equivalent in the bundle,
  including previous successes and unresolved failures, while exactly one stable API owner exists.

Regenerating with the same inputs must be idempotent. The emitted NUL-delimited path list is for
exact Git staging; it is not approval to commit, push, or activate. Those actions remain separately
reviewed and authorized. After activation, use the QA checks below to prove that Flux applied the
new source while the running image digest remained the selected immutable digest.

### Retry the same QA release

A reviewed retry increments `product.deploymentTargets.qa.initialization.attempt` in the catalog,
then regenerates the same release through the generic interface:

```sh
make go-svc-release-prepare \
  RELEASE_SERVICE=product RELEASE_TARGET=qa \
  RELEASE_REPOSITORY=igor-baiborodine/insurance-hub \
  RELEASE_VERSION='<1.x-semantic-version>' \
  RELEASE_SOURCE_COMMIT='<full-source-sha>' \
  RELEASE_IMAGE_DIGEST='sha256:<64-hex-digest>' \
  RELEASE_OUTPUT=/tmp/product-release-paths
```

`RELEASE_TARGET` optionally selects one registered target; omitting it uses `releaseTargets`.
`RELEASE_OUTPUT` receives the exact NUL-delimited path list consumed by Git staging. It is not the
bundle destination: destinations come from the validated catalog. The local prepare command writes
files only; commit/push and live activation remain separately authorized actions.

Do this only after inspecting the retained failed Job and proving the correction. Do not change
source commit, image digest, assets, revisions, or timeout while calling the result a retry of the
same release. A manifest-only advance changes the source commit and release identity; a retry keeps
those identities fixed and changes only the configured attempt.

## Activate and verify QA

Before activation, provision and verify the QA administration profile, record sanitized before
state, prove Java Product absence, verify the published digest is pullable, and render the full Flux
and QA desired state. The staged QA Product overlay marks its current resources with Flux prune
disabled so transfer cannot delete them between owners.

Activation is one reviewed change: remove `./product` from
`k8s/overlays/qa/svc/kustomization.yaml` and add the release tuple below to
`k8s/flux/qa/kustomization.yaml`:

```yaml
resources:
  - ./flux-system
  - ./releases/product/release.yaml
```

After merge, let Flux reconcile. Do not apply the Job or API directly. Observe the exact source,
prerequisite, and stable API owners:

```sh
kubectl --context qa-insurance-hub -n flux-system get gitrepository qa-product-release-<id>
kubectl --context qa-insurance-hub -n flux-system get kustomization \
  qa-product-init-<id>-a<attempt> qa-product-api
kubectl --context qa-insurance-hub -n qa-svc get job qa-product-init-<id>-a<attempt>
```

Require the source revision and applied revisions to match the tuple, the exact Job to complete,
and `qa-product-api` to report that exact prerequisite in `dependsOn`. Record the Pod image ID and
digest, role proof, schema/history, accepted catalog identity, gateway reads, and Java absence. Then
run the guarded QA status and verification commands with the complete QA selection. Direct QA
deployment, image load, and stop remain forbidden.

After both owners have reconciled and the Product Deployment, Services, and ConfigMap retain their
UIDs under `qa-product-api`, remove the temporary prune-disabled annotation from the Product overlay
through a separate reviewed Git change. Reconcile again and confirm the stable owner retains the
same resource UIDs. Do not combine that cleanup with the initial ownership transfer.

## Abort, stop, and restoration

Abort on target/Cluster UID mismatch, incomplete role/Secret pairs, unexpected writer or Java
Product resource, schema/history incompatibility, catalog drift, failed Job, image/source mismatch,
or unavailable release. Retain the failed Job and sanitized observations.

For an initialization failure:

1. Inspect Job conditions, events, redacted logs, target identity, role verification, and catalog
   snapshot. Never print Secret YAML or credential-bearing URLs.
2. Fix the external cause without destructive down migration, reset, or catalog overwrite.
3. Generate a new attempt identity with the same release inputs and review the tuple.
4. Reconcile and prove safe replay plus unchanged pre-existing catalog values.

API rollback selects a previously qualified Go image compatible with migration version 1. It does
not roll back schema, delete history, reseed over existing rows, or restart Java. Backup restoration
is a separate platform operation. Keep the current and immediately previous successful release and
all unresolved failures. Prune older release Jobs/sources only after no dependency references them
and required evidence is retained.

Local `go-svc-stop` scales only the Go API to zero and preserves Product data; it does not provide a
serving fallback. Restore the accepted Go release through the guarded deployment path. In QA, change
the reviewed release tuple or activation resource through Git and Flux; never suspend reconciliation
or manufacture rollback with direct apply.

## Credential rotation and revocation

Reader rotation continues to use `ROLE_PROFILE=reader` and a protected unused recovery file. A new
pod-template credential revision is required so new API Pods open connections with the new Secret.
Migrator/seeder rotation uses `ROLE_PROFILE=administration`, rotates both elevated identities, and
must also produce a new release credential revision and prerequisite identity.

```sh
export POSTGRES_ROLE_RECOVERY_FILE='/absolute/private-directory/admin-before-rotation.json'
make postgres-svc-role-rotate SVC_NAME=product ROLE_PROFILE=administration
make postgres-svc-role-verify SVC_NAME=product ROLE_PROFILE=administration
```

Retain the mode-0600 recovery file through successful Job/API qualification, then dispose of it
through the credential owner. Rotation attempts automatic restoration on partial failure; inspect
the result before retrying.

Administrative revocation is a deliberate decommissioning action. It requires no Product init Jobs
to exist and exact confirmation:

```sh
export POSTGRES_ROLE_CONFIRM_REVOKE="product/administration/$POSTGRES_ROLE_ENV"
make postgres-svc-role-revoke SVC_NAME=product ROLE_PROFILE=administration
```

It removes migrator/seeder membership, roles, and Secrets while preserving the reader, target marker,
schema, history, and catalog. It is not routine cleanup after a successful release.

## Evidence and handoff

Keep sanitized evidence in the resolved issue #190 artifact directory. Record UTC time, context and
namespaces, CNPG name/UID, target-contract and credential revisions, full source revision, image
reference/digest/ID, migration/catalog hashes, normalized timeout, release ID and attempt, Flux
source/applied revisions, Job UID/result, API Pod UIDs, role/SQLSTATE proof, schema/history/catalog
identity, gateway observations, Java absence, commands, outcomes, and cleanup.

Never retain passwords, tokens, credential-bearing URLs, raw Secrets, or full product definitions.
Separate offline render, fake-adapter, disposable Flux/CNPG, local-dev, and QA evidence. A later
environment cannot inherit an earlier environment's result, and an unexecuted live gate remains
open.
