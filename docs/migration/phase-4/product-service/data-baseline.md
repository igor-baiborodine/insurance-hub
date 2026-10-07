# Product data and environment baseline

This document records the effective Product environment, PostgreSQL schema, runtime ownership, and
writer inventory for issue #131. The machine-readable records are under
`legacy/product-service/src/test/resources/product-read-baseline/inventory/` and contain no Secret
values, bearer tokens, or catalog definitions.

## Capture status

| Environment | Scenario | Status | Evidence |
| --- | --- | --- | --- |
| local-dev | `INV-LOCAL-001` | Captured and fixture-checked on 2026-10-07 at source revision `cbe608a6cfa98b38ddcf9d329a41b3e71cbc302b`. | `inventory/local-dev.json` |
| QA | `INV-QA-001` | Captured and fixture-checked on 2026-10-07 at source revision `cbe608a6cfa98b38ddcf9d329a41b3e71cbc302b`. | `inventory/qa.json` |

The initial QA capture failed because the restored snapshot exposed Product `1.0.0` with MongoDB
configuration and had no `public.product` table. Both Product tags `1.0.0` and `1.4.0` resolved at
the expected GHCR path, so this was runtime drift rather than an invalid image coordinate. The
cluster also lacked the `flux-system` namespace and Flux custom resources.

After maintainer authorization, the complete QA service diff was reviewed: only the Product
Deployment and its generated ConfigMap differed. The checked-in Flux `2.8.5` controllers, public
`GitRepository/insurance-hub` source, and `qa-svc` Kustomization were installed. Reconciliation of
`main@453afd77041d380dea7a6435e4c3e5d4e94d496f` changed Product to `1.4.0` with PostgreSQL Secret
references. Existing Hibernate/DataLoader startup behavior created `public.product` and seeded the
four missing rows. The subsequent QA preflight and inventory fixture check passed.

## Reproduction

Run from the repository root with exactly one approved environment active. Start a Product-only
port-forward in another terminal, using the environment's context, namespace, and service:

```shell
kubectl --context=kind-local-dev-insurance-hub --namespace=local-dev-all \
  port-forward service/local-dev-postgres-product-rw 5492:5432
make product-baseline-capture BASELINE_ENV=local-dev BASELINE_PART=inventory
make product-baseline-fixtures-check BASELINE_ENV=local-dev BASELINE_PART=inventory
```

For QA, replace the context, namespace, service, and environment with `qa-insurance-hub`, `qa-data`,
`qa-postgres-product-rw`, and `qa`. Capture runs the environment preflight first and fails before
writing a fixture if workloads, endpoints, the database identity, `public.product`, or read access
cannot be verified. Run the fixture check without `BASELINE_ENV` only after both records exist.

The capture uses the environment's existing Product database identity in a read-only session. It
records Secret names and keys but never writes Secret values to disk or output. Stored JSON product
definitions remain owned by Step 4 and are deliberately excluded from inventory records.
`capturedAt` comes from the target PostgreSQL server in the same metadata query so the record is
internally ordered with environment events even when the operator host clock is not synchronized.

## Effective local-dev inventory

The accepted local-dev record observed:

- Kubernetes context `kind-local-dev-insurance-hub`, with service and data resources in
  `local-dev-all`;
- ready Product and gateway deployments using locally loaded `latest` images, with their resolved
  image IDs captured;
- a healthy one-instance CloudNativePG cluster using
  `ghcr.io/cloudnative-pg/postgresql:17` and PostgreSQL server `17.10`;
- database `product`, schema `public`, table owner and current runtime role `product`;
- `public.product(code varchar(255) not null, definition jsonb not null)` with primary key
  `product_pkey`, its backing unique B-tree index, and no non-internal triggers;
- exactly four visible codes: `CAR`, `FAI`, `HSI`, and `TRI`;
- no observed difference from the checked-in local-dev image, database-host, PostgreSQL-image, or
  schema expectations captured by the tool.

The Java database identity is the `product` owner and effectively has database/schema creation plus
all table write privileges. This is the existing Java writer identity, not evidence for the future
Go runtime. Step 9 must still prove a separate restricted role can read while write and schema
changes are denied.

## Effective QA inventory

The accepted QA record observed:

- Kubernetes context `qa-insurance-hub`, with services in `qa-svc` and data resources in `qa-data`;
- ready Product `1.4.0` at digest
  `sha256:633d645b45696349ef8e3e2fbe3f25aec0997ce9698c371f31535ca4da5bae8d` and gateway
  `1.0.0` at its captured digest;
- a healthy two-instance CloudNativePG cluster using
  `ghcr.io/cloudnative-pg/postgresql:17` and PostgreSQL server `17.9`;
- the same database, schema, owner, columns, nullability, primary key, index, absence of triggers,
  role membership, and effective privileges as local-dev;
- exactly four visible codes: `CAR`, `FAI`, `HSI`, and `TRI`;
- healthy `qa-svc` Flux reconciliation from the public `insurance-hub` source at
  `main@453afd77041d380dea7a6435e4c3e5d4e94d496f`;
- no remaining difference from the checked-in QA image, database-host, PostgreSQL-image, or schema
  expectations selected by the capture.

The environment differences relevant to later steps are the image identities, PostgreSQL patch
versions (`17.10` local-dev and `17.9` QA), database instance counts (one and two), tracing
configuration, and Flux ownership in QA. The Product schema and visible code set match.

## Schema and writer comparison

The observed local-dev schema matches the maintained Java persistence mapping and the supplied
two-column DDL. The following writer and deployment owners remain relevant:

| Owner | Effective or checked-in behavior | Baseline consequence |
| --- | --- | --- |
| Java `DataLoader` | On `ServerStartupEvent`, inserts missing `CAR`, `FAI`, `HSI`, and `TRI`. | Every data capture must record the active Java image and catalog identity. |
| Java Hibernate startup | `hibernate.hbm2ddl.auto=update`. | Java remains schema owner; a service restart can change schema before a capture. |
| CloudNativePG bootstrap/admin | Creates the `product` database and owner from an environment Secret reference. | Runtime and provisioning identities must remain distinct in the future Go design. |
| Kustomize/Make | Defines Product/gateway deployments, database host, Secret references, and local forwarding. | Checked-in configuration is comparison input rather than runtime proof. |
| QA Flux configuration | Reconciles `k8s/overlays/qa/svc` from the public `main` source. | The recovered controllers and `qa-svc` Kustomization are healthy at the captured revision; this tokenless recovery is not a credential-backed, self-managing bootstrap. |

The capture records deployment generation/revision, rollout strategy, probes, image policy,
services/endpoints, sanitized environment and ConfigMap data, database cluster status, effective
privileges and memberships, table metadata, relevant public objects, and differences from selected
checked-in expectations. Capture timestamps and source revisions make environment switches and
later drift explicit.
