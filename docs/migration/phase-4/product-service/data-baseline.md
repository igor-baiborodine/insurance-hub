# Product data and environment baseline

This document records the effective Product environment, PostgreSQL schema, runtime ownership,
writer inventory, and accepted four-product snapshots for issue #131. The machine-readable records
are under `legacy/product-service/src/test/resources/product-read-baseline/`. Inventory records
contain no catalog definitions; catalog records contain only the agreed demo products. Neither
record type contains Secret values, credentials, or bearer tokens.

## Capture status

| Environment | Scenario | Status | Evidence |
| --- | --- | --- | --- |
| local-dev | `INV-LOCAL-001` | Captured and fixture-checked on 2026-10-07 at source revision `cbe608a6cfa98b38ddcf9d329a41b3e71cbc302b`. | `inventory/local-dev.json` |
| QA | `INV-QA-001` | Captured and fixture-checked on 2026-10-07 at source revision `cbe608a6cfa98b38ddcf9d329a41b3e71cbc302b`. | `inventory/qa.json` |
| local-dev | `DATA-LOCAL-001` | Captured, reviewed, fixture-checked, and round-trip tested on 2026-10-07 at source revision `eddc97efd09fecf441f37fa8fda2600afdb5da91`. | `catalog/local-dev.json` |
| QA | `DATA-QA-001` | Captured, reviewed, fixture-checked, and round-trip tested on 2026-10-07 at source revision `eddc97efd09fecf441f37fa8fda2600afdb5da91`. | `catalog/qa.json` |

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
make product-baseline-capture BASELINE_ENV=local-dev BASELINE_PART=catalog
# Review the new path printed by capture before accepting it.
scripts/product-baseline/accept-catalog.sh \
  ai/artifacts/epic-4.2/issue-131/catalog-runs/local-dev-<timestamp>.json
make product-baseline-fixtures-check BASELINE_ENV=local-dev BASELINE_PART=catalog
BASELINE_ENV=local-dev scripts/product-baseline/test-catalog-roundtrip.sh
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

Catalog capture writes a new ignored run file and never updates the accepted fixture directly.
Acceptance is a separate explicit command after review. The database query runs in one
repeatable-read, read-only transaction and records its transaction snapshot, schema checksum,
catalog checksum, ordered codes, and per-row checksums. Each `definition::text` value is stored as
a JSON string, avoiding a typed or floating-point conversion while metadata is assembled. The
round-trip check loads that raw representation into disposable PostgreSQL 16.4 and compares the
stored JSONB text checksum for every row.

## Accepted catalog snapshots

Both environments contain exactly `CAR`, `FAI`, `HSI`, and `TRI`. Their schema checksum
(`76fac669b254931c0709c236fdd7804b`), full catalog checksum
(`7ba18949c152e2979df14a28a67cba7f`), and all four row checksums match. No environment-specific
catalog difference was observed. This identity applies to stored JSONB only; direct and gateway
HTTP representations are separate Step 5 evidence.

The representative data covers non-empty product, cover, question, and choice arrays; explicit
`true` and `false` cover flags; explicit null and integer `sumInsured` values; and `choice` and
`numeric` question subtypes. It does not cover a `date` question, fractional/high-precision/
trailing-scale/zero decimals, absent or null product fields, empty/null/absent collections,
primitive null/default behavior, unknown fields or subtypes, or deterministic ordering. Those are
synthetic isolated scenarios in later steps and must not be inferred from the demo rows.

The four accepted rows are frozen against additions until Java retirement. Any row addition or
removal, row checksum change, schema checksum change, or mismatch between an observation and its
linked inventory identity invalidates that environment's downstream comparison. Recapture into a
new run, review the differences, explicitly replace the accepted fixture, then rerun fixture and
round-trip checks. Capture and verification never refresh accepted data automatically.

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
