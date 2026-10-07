# Product read baseline

This directory is the versioned entry point for issue #131's Product catalog read baseline. The
baseline will describe the Java behavior that the Go pilot must preserve, the shared PostgreSQL
topology it will read, and the evidence used to qualify later implementation work.

The source inventory and scenario register are established. Runtime captures, accepted fixtures,
parity decisions, permission proofs, and replay commands remain pending their delivery steps. A
source reference in this document records what the checkout says; it is not evidence of an
effective local-dev or QA result.

## Scope and foundation

The baseline covers the Java Product service's list and get operations, the authenticated gateway
routes, current consumers, the four agreed demo products (`CAR`, `FAI`, `HSI`, and `TRI`), and the
configuration and writers that can affect comparisons. Synthetic edge and failure states will run
only against disposable test resources.

The Go pilot will read the same PostgreSQL database and `public.product` table as Java with a
separate restricted runtime role. Java remains the schema and seed owner and the destination of
existing traffic. Go implementation, deployment, traffic switching, Java retirement, new catalog
writes, new authorization rules, and Java writer-control changes are outside this baseline.

[Phase 4.1 issue #129](https://github.com/igor-baiborodine/insurance-hub/issues/129) records the
completed Go development foundation and its boundary. Its scaffold, Make/CI interface, and module
checks are available; Product-specific data, contracts, security, deployment, and qualification
evidence are still owned by issues #131 through #136.

## Authoritative layout

| Path | Ownership and purpose | Current state |
| --- | --- | --- |
| `docs/migration/phase-4/product-service/README.md` | Baseline entry point, source inventory, reproduction guidance, limits, and downstream handoff. | Source inventory established. |
| `docs/migration/phase-4/product-service/data-baseline.md` | Effective environment/schema/writer inventory, catalog provenance, shared topology, and database-permission proof. | Planned; no runtime inventory has been recorded. |
| `docs/migration/phase-4/product-service/contract-baseline.md` | Consumer and field mappings, HTTP observations, parity rules, and transport semantics. | Planned; wire behavior and decisions are pending. |
| `docs/migration/phase-4/product-service/security-baseline.md` | Observed Java access and identity behavior and the compatible future Go model. | Planned; source observations do not establish effective enforcement. |
| `legacy/product-service/src/test/resources/product-read-baseline/manifest.json` | Canonical scenario register and, in later steps, the index for the sanitized fixture corpus. | Scenario register established; fixture paths and checksums are pending capture. |
| `legacy/product-service/src/test/resources/product-read-baseline/` | Stored-row, request, raw-response, inventory, and synthetic fixtures readable without Java DTO deserialization. | No accepted captures or synthetic payloads exist yet. |
| `legacy/product-service/src/test/java/…` | Product/PostgreSQL listener, fixture, edge, failure, and permission tests. | Existing happy-path foundation only; baseline tests are planned. |
| `legacy/agent-portal-gateway/src/test/…` | Gateway listener, retry/fallback, and access tests against the shared corpus. | Planned; the module currently has no test source tree. |
| `scripts/product-baseline/` and the root `Makefile` | Capture, fixture checking, isolated replay, live verification, and aggregate validation. | Planned for step 2 onward. |

Ticket plans, execution records, step summaries, and Git snapshots stay in the ignored
`ai/artifacts/epic-4.2/issue-131/` directory. They are evidence records rather than reusable
baseline inputs.

## Source inventory

### Read routes and data path

| Boundary | Source finding | Runtime question retained by the register |
| --- | --- | --- |
| API contract | [`ProductOperations`](../../../../legacy/product-service-api/src/main/java/pl/altkom/asc/lab/micronaut/poc/product/service/api/v1/ProductOperations.java) declares list `GET` and `GET /{productCode}` returning RxJava `Single<List<ProductDto>>` and `Maybe<ProductDto>`. | Exact status, headers, media type, raw JSON, empty-`Maybe` behavior, and path decoding. |
| Direct Product HTTP | [`ProductsController`](../../../../legacy/product-service/src/main/java/pl/altkom/asc/lab/micronaut/poc/product/service/infrastructure/adapters/web/ProductsController.java) binds the contract below `/products` and has no security annotation. | Effective direct reachability and access behavior in each environment. |
| Repository | [`ProductsRepositoryImpl`](../../../../legacy/product-service/src/main/java/pl/altkom/asc/lab/micronaut/poc/product/service/infrastructure/adapters/db/ProductsRepositoryImpl.java) calls the Micronaut Data CRUD repository. `findAll()` has no explicit sort and missing lookups become an empty `Maybe`. | Effective SQL/schema, list order, lookup comparison, database errors, and their wire results. |
| Persistence | [`ProductEntity`](../../../../legacy/product-service/src/main/java/pl/altkom/asc/lab/micronaut/poc/product/service/infrastructure/adapters/db/model/ProductEntity.java) maps `product.code` as the identifier and `product.definition` through a custom JSONB type. [`ProductDefinitionJsonbType`](../../../../legacy/product-service/src/main/java/pl/altkom/asc/lab/micronaut/poc/product/service/infrastructure/adapters/db/model/ProductDefinitionJsonbType.java) uses a standalone Jackson `ObjectMapper`. | Effective table metadata, stored JSON tokens, unknown/null/subtype decoding, and failures. |
| Domain and DTO mapping | [`ProductDefinition`](../../../../legacy/product-service/src/main/java/pl/altkom/asc/lab/micronaut/poc/product/service/infrastructure/adapters/db/model/ProductDefinition.java) creates the domain object; [`ProductsAssembler`](../../../../legacy/product-service/src/main/java/pl/altkom/asc/lab/micronaut/poc/product/service/infrastructure/adapters/web/ProductsAssembler.java) creates the API DTO. | Presence/default behavior and exact stored-to-wire differences. |
| Gateway HTTP | [`ProductGatewayController`](../../../../legacy/agent-portal-gateway/src/main/java/pl/altkom/asc/lab/micronaut/poc/gateway/ProductGatewayController.java) exposes authenticated `GET /api/products` and `GET /api/products/{productCode}` and delegates to the Product client. | Authentication results, identity propagation, and direct-versus-gateway response differences. |
| Gateway client | [`ProductGatewayClient`](../../../../legacy/agent-portal-gateway/src/main/java/pl/altkom/asc/lab/micronaut/poc/gateway/client/v1/ProductGatewayClient.java) targets `/products` and declares two attempts with a two-second delay. | Actual attempt timing/count and which failures activate retry or fallback. |
| Gateway fallback | [`ProductGatewayClientFallback`](../../../../legacy/agent-portal-gateway/src/main/java/pl/altkom/asc/lab/micronaut/poc/gateway/client/v1/fallback/ProductGatewayClientFallback.java) supplies an empty list for list and an empty `Maybe` for get. | Exact gateway status, headers, and body when fallback runs. |

The source-level field path is:

| Stored value | Persistence/domain value | API value | Source constraint to test |
| --- | --- | --- | --- |
| `code` column | `ProductEntity.code` → `Product.code` | `ProductDto.code` | Entity mapping marks it non-null and non-updatable; effective type/key and lookup behavior remain runtime facts. |
| `definition.name`, `image`, `description`, `icon` | Same-named `String` fields | Same-named `String` fields | Null and absent-key results require isolated capture. |
| `definition.maxNumberOfInsured` | Primitive `int` | Primitive `int` | Absent/null/zero behavior must be distinguished by observation. |
| `definition.covers[]` | `CoverDefinition` → `Cover` | `CoverDto` | Array order is retained by the mapping code but no public guarantee is accepted yet. |
| `covers[].optional` | Primitive `boolean` | Primitive `boolean` | Missing/null/false behavior must be captured. |
| `covers[].sumInsured` | `BigDecimal` | `BigDecimal` | Decimal token, value, scale, null, and round-trip behavior must remain lossless. |
| `definition.questions[]` | Polymorphic `QuestionDefinition` | Polymorphic `QuestionDto` | Both layers use `type`; registered values are `choice`, `date`, and `numeric`. Unknown and absent values need isolated evidence. |
| `questions[].code`, `index`, `text` | Same-named fields; `index` is primitive `int` | Same-named fields | Presence, zero/default behavior, and array order require capture. |
| `questions[type=choice].choices[]` | `ChoiceDefinition` → `Choice`; an explicit null list maps to an empty domain list | `ChoiceDto`; a null domain list maps to an empty DTO list | Absent, null, empty, and ordered choices are separate scenarios. |

`ProductDefinition` ignores unknown top-level JSON properties. That annotation does not establish
wire behavior for nested unknown fields or unknown subtype values, so those cases remain synthetic
observations. The mapping code also assumes some collections are non-null. The register therefore
captures successful and failing presence cases instead of treating source-level defaults as a
contract.

### Writers and configuration sources

| Owner/source | Source finding | Evidence still required |
| --- | --- | --- |
| Java startup seed | [`DataLoader`](../../../../legacy/product-service/src/main/java/pl/altkom/asc/lab/micronaut/poc/product/service/init/DataLoader.java) runs on `ServerStartupEvent`, looks up each of `CAR`, `FAI`, `HSI`, and `TRI`, and saves a factory value when absent. [`DemoProductsFactory`](../../../../legacy/product-service/src/main/java/pl/altkom/asc/lab/micronaut/poc/product/service/init/DemoProductsFactory.java) is the source fixture, not a runtime snapshot. | Actual rows in local-dev and QA, startup revision, and data identity for every capture. |
| Hibernate | [Product configuration](../../../../legacy/product-service/src/main/resources/application.yml) enables `hibernate.hbm2ddl.auto: update` and selects PostgreSQL persistence. | Effective schema objects, startup DDL effects, and differences from mappings/configuration. Separate writer-control work is excluded. |
| Application repository | The internal `Products` adapter exposes `add`, backed by repository `save`; no production administrative Product write route was found by the step-1 source search. | Confirm deployed writers and any operational fixture/admin process during environment inventory. |
| Isolated tests | Existing [`DataLoaderIT`](../../../../legacy/product-service/src/test/java/pl/altkom/asc/lab/micronaut/poc/product/service/init/DataLoaderIT.java) and [`PostgresProductsRepositoryIT`](../../../../legacy/product-service/src/test/java/pl/altkom/asc/lab/micronaut/poc/product/service/infrastructure/adapters/db/PostgresProductsRepositoryIT.java) delete and repopulate only their Testcontainers database. [`BaseIT`](../../../../legacy/product-service/src/test/java/pl/altkom/asc/lab/micronaut/poc/product/service/BaseIT.java) starts `postgres:16.4-alpine`; this is not evidence of either deployed server version. | Extend the disposable pattern without allowing destructive test settings to address a shared database. |
| Product runtime config | The [base Kustomization](../../../../k8s/apps/svc/product/base/legacy/kustomization.yaml) supplies database defaults. The [local-dev](../../../../k8s/overlays/local-dev/svc/product/legacy/kustomization.yaml) and [QA](../../../../k8s/overlays/qa/svc/product/legacy/kustomization.yaml) overlays select environment database hosts. Their deployment patches reference environment-specific Product database Secrets. | Rendered and live configuration, Secret references without values, workload image/revision, namespace, and rollout state. |
| PostgreSQL provisioning | CloudNativePG's [base cluster](../../../../k8s/apps/infra/postgres/base/cluster.yaml) declares a PostgreSQL 17 image. Product [local-dev](../../../../k8s/overlays/local-dev/infra/postgres/product/cluster-patch.yaml) and [QA](../../../../k8s/overlays/qa/infra/postgres/product/cluster-patch.yaml) patches request the `product` database and owner. | Effective server version, database/schema/owner/grants/role membership, generated objects, and configuration drift. |
| Make deployment | [`k8s/Makefile`](../../../../k8s/Makefile) creates/copies service database Secrets, applies Product PostgreSQL and service overlays, and includes Product in aggregate deployment. Commit `5995bd8` assigns Pricing to `localhost:5482` and Product PostgreSQL to `localhost:5492` in the broad local port-forward target; the [connectivity guide](../../../../k8s/tests/infra/verify-postgres-connectivity.md) records the service-to-port map. | Which commands/resources created each running environment, whether the required forward is actually healthy, and the restart/rollout effects relevant to captures. |
| QA Flux | [`qa-svc`](../../../../k8s/flux/qa/flux-system/kustomization-qa-svc.yaml) reconciles `k8s/overlays/qa/svc` from `main` every five minutes with pruning and waiting; the QA service aggregate includes Product and gateway. PostgreSQL provisioning is outside this Flux Kustomization. | Effective Flux source revision, last reconciliation, resource inventory, and any separately managed database changes. |

The checked-in QA service patches select Product image `1.4.0` and gateway image `1.0.0`; local-dev
uses locally loaded `latest` images. These are configuration inputs only. Runtime capture must record
the resolved image IDs/digests and active revisions rather than copying these labels into evidence.

### Security sources

The gateway enables JWT security and marks both Product gateway methods as authenticated. The
[gateway configuration](../../../../legacy/agent-portal-gateway/src/main/resources/application.yml)
and [auth configuration](../../../../legacy/auth-service/src/main/resources/application.yml) contain
matching checked-in signing-secret defaults; effective trust material and validation settings must
be inspected without recording secret values. The auth service's
[`InsuranceAgentJWTClaimsSetGenerator`](../../../../legacy/auth-service/src/main/java/pl/altkom/asc/lab/micronaut/poc/auth/InsuranceAgentJWTClaimsSetGenerator.java)
adds `avatar` to the framework claims, while roles come from `UserDetails`.

No Product-specific role check appears on the gateway controller, no security annotation appears on
the direct Product controller, and the step-1 search found no explicit gateway HTTP-client filter
that forwards the caller's authorization header to Product. These are source observations. The
access and propagation scenarios retain missing, malformed, invalid-signature, expired,
not-yet-valid, issuer, audience, role, and direct-access cases so effective behavior is measured.

### Consumers

| Consumer | Catalog dependency found in source |
| --- | --- |
| Vue catalog list | [`ProductList.vue`](../../../../legacy/web-vue/src/components/ProductList.vue) calls gateway `products`; [`ProductCard.vue`](../../../../legacy/web-vue/src/components/ProductCard.vue) renders `code`, `name`, `image`, `description`, and `covers`. |
| Vue covers | [`CoverList.vue`](../../../../legacy/web-vue/src/components/CoverList.vue) iterates covers in response order and renders cover `name` and `sumInsured`. |
| Vue product detail and offer input | [`ProductDetails.vue`](../../../../legacy/web-vue/src/components/ProductDetails.vue) calls `products/{productCode}`, displays `name`, `image`, and `description`, iterates questions and choices in response order, handles `numeric` and `choice`, submits `code`, cover codes, question codes/types, and answers to offer creation. It has no `date` rendering branch. |
| Vue dashboard | [`Dashboard.vue`](../../../../legacy/web-vue/src/components/Dashboard.vue) calls the catalog list, uses `code`, `name`, and `icon` for per-product totals and filter options, and passes the selected code to dashboard queries. |
| Vue authentication boundary | [`ApiClient.js`](../../../../legacy/web-vue/src/components/http/ApiClient.js) uses `/api/` by default and adds the locally stored bearer token to every request. |
| Angular catalog list | The additional legacy [`ProductService`](../../../../legacy/web-angular/src/app/shared/product-service.ts) calls `products` and `products/{productCode}`. The list renders `code`, `name`, `image`, `description`, and ordered covers with `name` and `sumInsured`. |
| Angular product detail and offer input | [`product-details.component.ts`](../../../../legacy/web-angular/src/app/components/product-details/product-details.component.ts) consumes product identity and display fields, cover codes, question `code`/`type`/`text`, and choice `code`/`label`; it branches on `choice` and `numeric`, not `date`. |

The dashboard service itself uses product codes in sales data and queries but does not call the
Product catalog. Its catalog dependency comes through the Vue dashboard's separate Product list
request. Pricing and Policy also carry product codes, but the source search found no direct Product
catalog client in those services.

## Scenario register

[`manifest.json`](../../../../legacy/product-service/src/test/resources/product-read-baseline/manifest.json)
is the canonical register. Every entry has a stable ID, target environment, fixture provenance,
credential category, capture profile, source references, acceptance-criterion mapping, and a
`registered` status. A registered entry describes required work and never means the result was
observed.

The register separates:

- effective environment/schema/writer inventory and the four-row stored catalog;
- direct and gateway list/get success for all four products;
- empty, single-product, missing, unusual/malformed lookup, decoding, dependency, retry, and
  fallback cases;
- decimal, subtype, presence/default, unknown-field, unknown-variant, and collection-order cases;
- gateway and direct access matrices plus identity propagation;
- isolated restricted-role read and mutation-denial proofs; and
- replay, drift-detection, sanitization, safety, and acceptance-evidence checks.

The manifest's capture profiles define the required fields. HTTP evidence includes method, path,
query, sanitized request headers, credential category, status, content type, selected response
headers, raw sanitized body, environment, capture command, service revision, fixture/schema/data
identity, retry/fallback information, timestamp, and sanitization record. Stored data is captured
separately and losslessly. Volatile headers and JSON object-key order may be considered for later
normalization, but the register does not preapprove normalization of decimals, field presence,
array order, status, or errors.

## Reproduction status

The `product-baseline-*` Make interface described by the ticket does not exist yet. Step 2 will add
the owning preflight and isolated smoke foundation before any live capture. Until then there is no
supported command that reproduces this baseline, and no local-dev or QA observation is accepted.

Live capture must be read-only and must record its explicit Kubernetes context, namespace, target,
and revision. Empty catalogs, malformed rows, dependency failures, access negatives requiring
controlled credentials, and permission-denial attempts run only in disposable resources. Capture
output is written to a new run location and reviewed/sanitized before it can become an accepted
fixture; verification never rewrites its own expectation.
