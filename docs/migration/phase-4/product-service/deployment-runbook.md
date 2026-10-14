<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Product Go deployment runbook](#product-go-deployment-runbook)
  - [Ownership and delivery sequence](#ownership-and-delivery-sequence)
  - [Prerequisites and validation commands](#prerequisites-and-validation-commands)
  - [Publish a merged image](#publish-a-merged-image)
  - [Environment switching and target inputs](#environment-switching-and-target-inputs)
  - [Provision and verify the reader](#provision-and-verify-the-reader)
  - [Local-dev deployment and verification](#local-dev-deployment-and-verification)
  - [QA activation and verification](#qa-activation-and-verification)
  - [Configuration and credential rollout](#configuration-and-credential-rollout)
  - [Abort, stop, and restoration](#abort-stop-and-restoration)
  - [Acceptance evidence and handoff](#acceptance-evidence-and-handoff)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Product Go deployment runbook

Product Go reads the existing `product.public.product` catalog alongside Java. Java retains
schema/seeding ownership and remains the gateway destination. Go uses a dedicated reader login,
internal HTTP/gRPC Services, and a separate management Service. This is the operational procedure
for issue #134; it does not certify that either shared environment has passed acceptance.

## Ownership and delivery sequence

| Resource or action | Owner and boundary |
| --- | --- |
| Runtime image | Product module Makefile; pinned Go 1.27.2 builder and minimal non-root runtime; `linux/amd64`, `GOWORK=off` |
| Publication | **Go Service Release** runs automatically for a canonical `main` push that changes exactly one `services/<service>-service`; it owns GHCR publication, scoped Git/GitHub release identities, and the dormant QA semantic-tag update |
| PR validation | **Go Module Validation** owns module, release-selector, image, manifest, role-tooling, baseline, and local deployment-tooling checks; **Product Security Boundary Validation** owns combined Java/Go security |
| QA desired state | The release workflow commits the generated 1.x semantic tag; activation separately adds `./product` through review, after which `flux-system/insurance-hub` and `flux-system/qa-svc` reconcile `main` |
| Local-dev desired state | Explicit `go-svc-* SVC_NAME=product` commands and the local Go overlay |
| Database login and Secret | Maintainer with Kubernetes/CNPG administrative access, through `postgres-svc-role-* SVC_NAME=product ROLE_PROFILE=reader`; outside application startup and Flux |
| Catalog and Java routing | Existing Java owner and gateway configuration; never changed by Go deployment |

Initial activation has three staged PRs. The foundation PR supplies the runtime, deployment, and
local-dev evidence with Go excluded from the QA aggregate. The release-automation PR replaces its
bootstrap dispatch path; merging it must create the automatic release and semantic-tag bot commit
while the overlay remains dormant. After the reader, Secret, image, and capacity prerequisites
pass, the activation PR adds `./product` to `k8s/overlays/qa/svc/kustomization.yaml` while retaining
`./product/legacy`. Review the complete QA render, prerequisites, and rollback before merge. Flux
then deploys the reconciled `main` state. Live QA verification and lifecycle rehearsals must pass
before closing #134. No delivery PR uses an automatic issue-closing keyword.

The pre-activation manifest checker requires disconnected QA and accepts either the initial
unpublished placeholder or a release-owned 1.x semantic tag. The activation PR must update that
phase-specific assertion and its corruption fixtures to require the reviewed semantic tag and active
inclusion. Do not bypass the checker to activate Go.

## Prerequisites and validation commands

Use the repository root as the working directory for all commands below. See
[CONTRIBUTING](../../../../CONTRIBUTING.md) for host prerequisites. The Product tools require the
module's pinned Go toolchain, Node.js >=20, Docker/Buildx, Python >=3.10, Make, jq, and
kubectl/Kustomize. Role administration also requires host `psql`, healthy CNPG, and access to
its primary Pod and CA Secret. No application-owner password is accepted as a Go runtime input.

```sh
make go-product-bootstrap-tools
make -C services/product-service bootstrap-ci-tools
make -C services/product-service check-ci
make go-svc-release-test
make go-svc-dispatch-test
make go-svc-image-check SVC_NAME=product
make go-svc-deploy-check SVC_NAME=product
make go-svc-deploy-test-tooling SVC_NAME=product
make postgres-svc-role-test
make product-baseline-fixtures-check
make product-baseline-test BASELINE_SUITE=db-permissions
```

`bootstrap-ci-tools` explicitly installs PyYAML 6.0.3 in `.tools/ci-venv` using Python's venv and
pip, and checksum-verifies Kind 0.30.0, kubectl 1.34.1, and actionlint 1.7.7 into `.tools/bin`. It
requires Python pip >=22.3 and Linux amd64. Check targets do not install tools. Hosted workflows
add `.tools/bin` to PATH; local operators may continue using their existing compatible clients.

`make go-svc-deploy-test SVC_NAME=product` requires the active local Kind cluster and a ready CNPG
operator. It creates and removes a uniquely labelled namespace and a separate database; it must
not receive `PRODUCT_DATABASE_URL`. It exercises startup failures, empty/accepted catalog reads,
dependency loss/recovery, readiness versus liveness, rollout, HTTP/gRPC drain, and Pod termination.
The generic release does not create a hosted disposable cluster; this local target remains
available for explicitly prepared, owned disposable infrastructure and keeps its existing cleanup
boundary.

Module, security, and Markdown gates retain their existing ownership. For local reproduction:

```sh
make go-modules-check
make product-security-test-tooling
make product-security-check
make -C services/product-service check-docs
```

The combined security gate additionally needs the documented Java/Maven/package prerequisites in
[security reproduction](security-baseline.md#reproduction). Do not infer a hosted pass from local
results. Disposable tests may mutate only their own infrastructure and never prove live QA TLS,
registry access, effective exposure, or Java routing.

## Publish a merged image

A canonical `main` push that changes exactly one `services/<service>-service/**` module
automatically starts **Go Service Release**. Its Kubernetes-only QA bot commit is outside that
path, so it cannot recursively publish. Releases are serialized and never cancel an in-flight
publication. Record the source push SHA and inspect the matching run:

```sh
export RELEASE_REVISION='<full-40-character-source-SHA>'
gh run list --workflow go-release-svc.yml --limit 5
```

Select the run matching that revision and record its run ID, URL, and conclusion:

```sh
export RELEASE_RUN_ID='<matching-run-id>'
gh run watch "$RELEASE_RUN_ID"
```

The workflow uses `monotag` to calculate the next scoped 1.x version, starting at `1.0.0`, under
`product-service/v<version>`. It detects the changed standard module, derives `product`,
requires its Dockerfile and conventional QA image entry, and builds the module once. It publishes
`<version>` and `sha-<short-source-SHA>` under
`ghcr.io/igor-baiborodine/insurance-hub/product-api`, commits `<version>` to the dormant QA Product
overlay, creates the annotated scoped tag on the triggering source revision, and creates a
non-draft GitHub Release from the generated notes.

The release deliberately has no manual dispatch, recovery mode, digest finalization, pullback
verification, uploaded evidence bundle, Go package publication, complete module test suite, or
hosted disposable cluster. A failed or timed-out mutation may have completed; inspect the image,
QA commit, scoped tag, and GitHub Release before deciding on a separately reviewed repair.

The release job authenticates to GHCR and GitHub with its scoped `GITHUB_TOKEN`; PR checks receive
no publishing credentials. The workflow never receives cluster or database credentials and never
applies QA resources. These boundaries follow GitHub's
[package permissions](https://docs.github.com/en/packages/managing-github-packages-using-github-actions-workflows/publishing-and-installing-a-package-with-github-actions)
contract. Local `docker login` is not required for the automatic hosted release.

Before activation, verify package visibility and pullability using QA's intended registry access.
The checked-in Go Pod has no `imagePullSecrets`; public pull access must actually work. If the
package is private, establish an explicitly reviewed pull-Secret reference and separately
provisioned registry credential before activation. The reader database Secret is not a registry
credential. A successful pull by the authenticated release runner alone does not prove QA access.
Record the published semantic and short-SHA tags, source revision, and pulled image ID/digest.
Validate that release image before enabling QA.

## Environment switching and target inputs

The laptop runs local-dev and QA sequentially. Stop task-owned forwards and the previous
environment before starting the next; use the existing
[cluster procedures](../../../../k8s/base-cluster-how-tos.md). For the maintainer's frozen QA VMs,
resume only after local Kind is stopped. Do not restart shared infrastructure to manufacture a
test failure. Recheck UTC host/node clocks, connectivity, context, namespaces, Java readiness,
CNPG primary, catalog identity, and spare resources after each switch.

The deployment tools refuse a different active context or namespace; they do not switch them.
Choose one complete column and export its values before running targets:

| Input | local-dev | QA |
| --- | --- | --- |
| `DEPLOY_ENV` | `local-dev` | `qa` |
| `DEPLOY_CONTEXT` | `kind-local-dev-insurance-hub` | `qa-insurance-hub` |
| `DEPLOY_SVC_NS` | `local-dev-all` | `qa-svc` |
| `DEPLOY_DATA_NS` | `local-dev-all` | `qa-data` |
| `DEPLOY_IMAGE_REF` | `insurance-hub-product-api:git-<12-hex-revision>` matching the local overlay | `ghcr.io/igor-baiborodine/insurance-hub/product-api:<1.x-semantic-version>` matching the QA overlay |
| `DEPLOY_SOURCE_REVISION` | Unused | Full `main` SHA actually reconciled by the Flux source and `qa-svc`, which can differ from the image's source SHA |

For example, local-dev selection is:

```sh
export DEPLOY_ENV=local-dev
export DEPLOY_CONTEXT=kind-local-dev-insurance-hub
export DEPLOY_SVC_NS=local-dev-all
export DEPLOY_DATA_NS=local-dev-all
export DEPLOY_IMAGE_REF='insurance-hub-product-api:git-<reviewed-12-hex-revision>'
kubectl config use-context "$DEPLOY_CONTEXT"
kubectl config set-context "$DEPLOY_CONTEXT" --namespace="$DEPLOY_SVC_NS"
date -u
kubectl --context "$DEPLOY_CONTEXT" get nodes -o wide
kubectl --context "$DEPLOY_CONTEXT" -n "$DEPLOY_DATA_NS" get cluster.postgresql.cnpg.io
```

Check node clock synchronization through the host/VM administration interface; Kubernetes node
readiness alone does not prove it. For QA, run the host service, bounded synchronization, and
two-second drift checks in
[Synchronize host and QA VM clocks](../../../../k8s/base-cluster-how-tos.md#synchronize-host-and-qa-vm-clocks).
If the installed host service is disabled or inactive, the required administrative correction is:

```sh
sudo systemctl enable --now systemd-timesyncd
sudo timedatectl set-ntp true
```

Do not provision credentials or activate Go until the host reports `NTPSynchronized=yes` and the
documented host-to-node drift check passes for all three QA VMs. Capacity must allow
Java/PostgreSQL plus up to two Go Pods: requests per Pod `100m`/`64Mi`, limits `300m`/`256Mi`, pool
8 (16 total during surge). A capacity failure blocks activation until a reviewed, tested rollout
strategy fits the laptop.

## Provision and verify the reader

Derive explicit role coordinates from the verified environment selection:

```sh
export POSTGRES_ROLE_ENV="$DEPLOY_ENV"
export POSTGRES_ROLE_CONTEXT="$DEPLOY_CONTEXT"
export POSTGRES_ROLE_DATA_NS="$DEPLOY_DATA_NS"
export POSTGRES_ROLE_SVC_NS="$DEPLOY_SVC_NS"
export POSTGRES_ROLE_CLUSTER="$DEPLOY_ENV-postgres-product"
export POSTGRES_ROLE_SECRET="$DEPLOY_ENV-postgres-product-reader-creds"
export POSTGRES_ROLE_DB_HOST="$DEPLOY_ENV-postgres-product-rw.$DEPLOY_DATA_NS.svc.cluster.local"
make postgres-svc-role-inventory SVC_NAME=product ROLE_PROFILE=reader
make postgres-svc-role-snapshot SVC_NAME=product ROLE_PROFILE=reader
```

Review consumers and grants first. The profile rejects unknown login/session consumers and
unexpected ownership, memberships, or objects; it revokes inherited `PUBLIC` privileges and
retains Java's owner access. Resolve unexpected consumers before provisioning. Retain the
sanitized snapshot and compare it with the accepted environment inventory/schema/catalog baseline.

```sh
make postgres-svc-role-provision SVC_NAME=product ROLE_PROFILE=reader
make postgres-svc-role-verify SVC_NAME=product ROLE_PROFILE=reader
make postgres-svc-role-snapshot SVC_NAME=product ROLE_PROFILE=reader
```

Provisioning creates `go_product_reader`, restricted to CONNECT/USAGE/SELECT on
`public.product`, and the dedicated workload Secret with exactly `database-url` and `ca.crt`.
It obtains the CNPG CA from `<environment>-postgres-product-ca` and uses `sslmode=verify-full`
with `/etc/product-service/postgres/ca.crt`. Administration runs through the existing CNPG primary;
credentials do not appear in Make arguments or logs. Ordinary reapply preserves the password.
A role without its Secret is a recovery condition, not permission to silently generate a replacement.

Verification opens a new connection using the actual Secret, checks effective identity and
privileges, performs reads and rollback-safe denial probes, and proves Java owner reads and
unchanged schema/catalog identity. Each SQL operation owns a fresh bounded loopback port-forward
and cleans it up because a cluster transport may close a tunnel after one database connection.
Destructive denial tests belong only to the disposable suite. Never use the security
fixture-loading harness against these shared databases.

## Local-dev deployment and verification

Build the intended source with `make go-svc-image-check SVC_NAME=product`. Update the local
Go overlay's `images[].newTag` to its actual `git-<12-hex-revision>` tag and set
`DEPLOY_IMAGE_REF` to match. Record the image ID and source label; a tag alone is insufficient.
With reader prerequisites verified, run:

```sh
make go-svc-deploy-check SVC_NAME=product
make go-svc-deploy-preflight SVC_NAME=product
make go-svc-image-load SVC_NAME=product
make go-svc-deploy SVC_NAME=product
make go-svc-deploy-status SVC_NAME=product
make go-svc-deploy-verify SVC_NAME=product
```

Preflight checks exact coordinates, image, Secret key names, Java ownership/routing, selectors,
Services, Pods, EndpointSlices, and route exposure. Local apply performs server dry-run and a
bounded rollout and compares sanitized before/after schema/catalog snapshots. For an intentional
image or configuration change, first record the observed current image/ConfigMap and export
`DEPLOY_PREVIOUS_IMAGE_REF` or `DEPLOY_PREVIOUS_CONFIG_MAP` respectively; undeclared drift blocks
apply. Clear those previous-state variables after successful verification.

`deploy-verify` checks the effective deployment and reader and runs anonymous HTTP/gRPC/management
checks via owned loopback forwards, cleaning them up on failure too. Internal-only Services do
not establish caller authentication or gateway-exclusive access. Follow the
[security baseline](security-baseline.md#go-deployment-exposure-gate-for-issue-134) to separately
exercise authenticated gateway `/api/products` and `/api/products/TRI` plus its negative-credential
subset. Correlate the effective Java destination with its Service endpoints; equal Java/Go JSON
does not establish which service the gateway reached. Keep tokens out of arguments and evidence.

## QA activation and verification

Before the activation PR, establish a fresh QA preflight using the explicit context, reader
inventory/proof, automatically published image availability, node capacity, and full rendered QA
diff. Require the dormant overlay semantic tag to match the release and bot commit; do not
manually retag or replace the release-owned version.

The activation PR adds only `./product` to the aggregate. Review the entire render and update the
phase-specific activation checks described above. The QA source stays on `main` with `wait` and
`prune`; existing gateway routes, the release-owned semantic tag, and `./product/legacy` remain
intact.

After merge, await reconciliation and inspect:

```sh
kubectl --context qa-insurance-hub -n flux-system get gitrepository insurance-hub
kubectl --context qa-insurance-hub -n flux-system get kustomization qa-svc
```

Record their ready conditions and actual source/applied revisions. Set all QA target variables,
including `DEPLOY_SOURCE_REVISION`, then run `go-svc-deploy-preflight`, `go-svc-deploy-status`,
and `go-svc-deploy-verify` with `SVC_NAME=product`. Repeat the independent gateway checks and
before/after accepted catalog identity comparisons. Record pod image IDs and compare with the
recorded release coordinates. Direct QA `go-svc-deploy`, image load, and stop are rejected by design.

If Flux DNS and GHCR pulls fail together, inspect the host forwarding condition described in
[cluster prerequisites](../../../../k8s/base-cluster-how-tos.md#prerequisites) before changing
repository manifests. A maintainer checks `sudo iptables -L FORWARD -v -n`, applies
`sudo iptables -P FORWARD ACCEPT` only if needed, and persists it with
`sudo netfilter-persistent save`. No Docker/LXD restart is required for that rule. After recovery,
require fresh source, reconciliation, and image-pull evidence; do not bypass Flux with direct apply.

## Configuration and credential rollout

Kustomize's generated ConfigMap hash causes a pod-template change for non-secret configuration.
Change only the intended Go configuration, render/check, and apply locally or merge its reviewed
QA change. Prove a newly ready Pod received the value and still serves reads. `OTEL_ENABLED=false`
remains explicit until #135 owns telemetry ingestion.

A mutable Secret update does not change an environment variable in an existing process.
Prepare a non-secret pod-template annotation in the selected environment's deployment patch:

```yaml
spec:
  template:
    metadata:
      annotations:
        insurance-hub.io/product-reader-credential-revision: "rotation-<UTC-change-id>"
```

Choose a new revision for each rotation/restoration. Review that change before rotating, especially
in QA where it must merge and reconcile promptly. Record the previous Pods/UIDs, generation,
image, ConfigMap, and schema/catalog identity. Use an unused recovery path in a private directory
outside the repository; the tool creates the file with mode 0600 and exclusive creation:

```sh
export POSTGRES_ROLE_RECOVERY_FILE='/absolute/private-directory/reader-before-rotation'
make postgres-svc-role-rotate SVC_NAME=product ROLE_PROFILE=reader
make postgres-svc-role-verify SVC_NAME=product ROLE_PROFILE=reader
```

Apply the annotation locally through `go-svc-deploy`, or merge/reconcile it through Flux in QA.
Verify rollout completion, replacement Pod UIDs, new reader connections, reads, safe denials,
and unchanged catalog/Java behavior. The old password can no longer open connections, but
existing sessions may survive until the old process exits. A Secret object update or an old
connection's successful query is not sufficient proof. Retain the recovery file until replacement
Pods and new connections pass. Remove it through the owner's secure credential-retention procedure.

Rotation changes the database and Secret separately. The tool attempts to restore both on a
partial failure. Inspect its result before retrying; if automatic recovery failed, reconcile the
role and Secret through the protected administrative recovery procedure first. Do not print the
Secret or fall back to Java's owner login. Once both are consistent and a fresh login works,
restoring the saved password uses the same guarded rotate target:

```sh
export POSTGRES_ROLE_RESTORE_FILE='/absolute/private-directory/reader-before-rotation'
export POSTGRES_ROLE_RECOVERY_FILE='/absolute/private-directory/reader-before-restoration'
make postgres-svc-role-rotate SVC_NAME=product ROLE_PROFILE=reader
unset POSTGRES_ROLE_RESTORE_FILE
```

That target requires the current Secret/password to authenticate; it cannot repair an already
inconsistent pair automatically. Restoration still needs a new annotation revision, Pod rollout,
and full verification. Do not repeatedly retry a failed rotation with the same recovery output
path; preserve that file and use a new path after inspecting state.

## Abort, stop, and restoration

Abort on unavailable image/Secret, unknown grant consumer, catalog drift, unexpected exposure,
Java route changes, insufficient capacity, or failed readiness/drain evidence. Retain the failed
observations and recover before continuing. Do not recapture expected fixtures automatically.

For local-dev stop and restore:

```sh
make go-svc-stop SVC_NAME=product
make postgres-svc-role-snapshot SVC_NAME=product ROLE_PROFILE=reader
# Verify Java gateway reads while Go is stopped, then restore its desired replicas.
make go-svc-deploy SVC_NAME=product
make go-svc-deploy-verify SVC_NAME=product
```

Stop scales only Go to zero, waits for its Pods/endpoints to disappear, and verifies Java and
catalog state. It does not remove the database or reader Secret. To abort a failed local image or
configuration rollout, restore the accepted overlay, declare the observed previous image/config
where required, and apply/verify it. Inspect failures that prevent guarded preflight before any
separately reviewed repair; the target is not a universal repair bypass.

In QA, remove only `./product` from the aggregate through a reviewed Git change while retaining
`./product/legacy`. Flux pruning removes Go resources. Verify reconciliation, absent Go endpoints,
Java gateway reads, and unchanged catalog. Restore the exact accepted semantic
version/configuration and re-add `./product` through review. A manual scale-down that Flux reverses
is not durable stop;
never suspend reconciliation or delete shared databases/PVCs/Java resources to stop Go. The
activation-aware checks must support and test this reviewed stopped state too.

For an image/configuration-only rollback, restore the last accepted Go desired state through Git
and verify reconciliation. For credential rollback, follow the protected restoration procedure
above; reverting Git alone cannot restore a database password.

Reader revocation is for deliberate decommissioning after the Go Deployment has been removed,
not routine scale-to-zero. The role tool refuses revocation while the Deployment exists. After
reviewing that separate operation, set `POSTGRES_ROLE_CONFIRM_REVOKE=product/reader/<environment>`
and run `make postgres-svc-role-revoke SVC_NAME=product ROLE_PROFILE=reader`. It preserves Java
and the catalog; restoration then requires explicit reprovisioning before restoring Go.

## Acceptance evidence and handoff

Keep sanitized ticket evidence in the ignored `ai/artifacts/epic-4.2/issue-134/` directory, with
UTC time, context/namespaces, source/Flux revisions, image reference/digest/ID, platform, Deployment
generation, ready EndpointSlices, Pod UIDs, configuration revision, reader proof, accepted
schema/catalog identity, Java routing observations, commands, outcomes, and CI run references.
Never retain passwords, tokens, full URLs containing credentials, or raw Secret YAML there.

Separate rendered, fake-adapter, disposable container/Kind, hosted CI, shared local-dev, and QA
results. Image tests do not prove cluster exposure. Port-forwards do not prove routes are absent.
The disposable suite owns disruptive database failures, locks, destructive SQL, and blocked-work
cases; shared environments own ordinary startup, Go-only rollout/restart/drain/stop/restoration,
safe permission checks, and Java/data preservation.

Record idle and in-flight termination against the 10-second application shutdown budget and
30-second Kubernetes grace period. Inspect loss/recovery of readiness and release of old reader
connections. Map AC134-01 through AC134-10 to evidence in the final ticket record and keep failed
or unexecuted gates open. #135 owns telemetry delivery, #136 integrated qualification and test-route
rollback, and #137 existing gateway cutover; none replaces failed #134 deployment acceptance.
