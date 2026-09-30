# Phase 4 implementation review

**Review date: 30 September 2026. Status: reviewed documentation baseline; runtime adoption remains gated.**

This addendum supersedes the Phase 4 implementation recommendations in the
[original migration analysis](../../system-overview-and-migration-analysis.md), including its
[Architecture Pattern Migrations](../../system-overview-and-migration-analysis.md#architecture-pattern-migrations),
[component recommendations](../../system-overview-and-migration-analysis.md#component-specific-migration-details),
[observability guidance](../../system-overview-and-migration-analysis.md#system-observability), and
[Phase 4 sequence](../../system-overview-and-migration-analysis.md#phase-4-phased-service-migration-to-go-strangler-fig-pattern).
The original remains readable as historical proposal. References to Phases 1–3, 5 and 6 below
qualify dependencies on those phases; they do not replace their unrelated scope or authorize
infrastructure, identity, traffic, data or service changes.

The review covers eight business-service rewrites: document, product, dashboard, policy-search,
chat, pricing, policy and payment. The Java gateway, auth service and Vue browser are compatibility
dependencies. Gateway/auth replacement remains separate Phase 5 work. This review implements no
Go service, contract, database migration, dependency upgrade, Kubernetes change or executable spike.

Read by topic:

- [Reviewed defaults and open gates](#reviewed-defaults-and-open-gates)
- [Evidence and original-source key](#evidence-and-original-source-key)
- [Previous versus reviewed decisions](#previous-versus-reviewed-decisions)
- [Detailed assessments](#detailed-assessments)
- [Preliminary research assessment](#preliminary-research-assessment)
- [Go example comparison](#go-example-comparison)
- [Deployment comparison](#deployment-comparison)
- [Service consequences](#service-consequences)
- [Sequence and coexistence](#sequence-and-coexistence)
- [Prerequisites and adoption gates](#prerequisites-and-adoption-gates)
- [Follow-up validation scenarios](#follow-up-validation-scenarios)

## Reviewed defaults and open gates

Retain service boundaries, explicit constructors, typed startup configuration, versioned contracts,
controlled SQL migrations and Make-based validation. Provisionally prefer native pgx/v5/pgxpool
with sqlc for actual PostgreSQL adapters, Protovalidate for Protobuf request shape, IBM Sarama for
Kafka, the official Elasticsearch v8 client, and the MinIO v7 SDK for document/payment. Use
standard-library slog JSON and OpenTelemetry through Alloy. These choices require service-owned
compatible pins and tests; they are not a universal dependency list.

Replace the blanket Wire, Viper, GORM, protoc-gen-validate and olivere/elastic defaults. Defer the
pricing evaluator, chromedp adoption, unnecessary streaming/history capabilities, Keycloak/Envoy/mesh
cutovers and weighted traffic-shift mechanism until their evidence exists. Keep explicit HTTP
compatibility for Java callers and the browser. Recommend a bounded product catalog-read pilot
before event-driven or browser-dependent rewrites; this is an inference from the observed code,
not a measured risk ranking.

The most consequential open gates are legacy identity and route permissions; schema/seed ownership;
Kafka and object/DB recovery; MinIO server support/security and verified TLS trust; effective PDF
fixtures; exact decimal pricing; and demonstrated Alloy log/metric/trace delivery. A Pod receiving
zero HTTP requests may still consume Kafka or run an importer. Independent deployment and safe
shadow operation therefore require control over every side effect.

## Evidence and original-source key

Insurance Hub was inspected at
[`30007edfefe2d7bdc8e9eecd8951deb70ac50344`](https://github.com/igor-baiborodine/insurance-hub/tree/30007edfefe2d7bdc8e9eecd8951deb70ac50344)
with no staged, unstaged or non-ignored untracked modifications before this document was created.
The three preliminary research documents were already present at that revision. The read-only
Go example was inspected at
[`24594b4290365704c5d91244c48e2ba1238c6b43`](https://github.com/igor-baiborodine/campsite-booking-go/tree/24594b4290365704c5d91244c48e2ba1238c6b43),
also without local modifications. Example citations below use that immutable revision; local
Insurance Hub links refer to the recorded source baseline, whose line numbers may move later.

Code/configuration inspection establishes checked-in behavior and intent. It does **not** establish
live schemas, deployed versions, runtime access, rendered overlays, successful reconciliation,
performance, business parity or security certification. No Java/Go build, generation, database,
broker, browser, cluster or differential runtime test was executed for this review. Documentary
coverage, relative links/anchors, source paths and whitespace were checked separately.

Primary sources were reviewed on 29–30 September 2026; version observations below retain that date
and are not dependency pins. Recheck compatibility, advisories, licenses/notices and transitive
dependencies when an owning service module exists. Release availability does not prove compatibility
with the checked-in infrastructure. Published license identifiers are assessment inputs, not a
legal conclusion or an exhaustive supply-chain audit.

The matrix uses these original-source abbreviations. Line coordinates refer to the Insurance Hub
revision above; each coordinate links to the preserved revision. “Prior study” identifies research
coverage before this review, not a previously accepted decision.

| Key | Original source or existing requirement |
| --- | --- |
| A | [Original system analysis](../../system-overview-and-migration-analysis.md): target-state/component sections, architecture table, observability and Phases 1–6. |
| P | [PostgreSQL research](postgresql-data-access-strategy.md), preliminary. |
| D | [Dependency injection research](dependency-injection-strategy.md), preliminary. |
| C | [Configuration research](configuration-strategy.md), preliminary. |
| G / F / V | Canonical [Go development](../../../ai/rules/go-development.md), [formatting](../../../ai/rules/go-formatting.md) and [validation](../../../ai/rules/go-validation.md) requirements. |
| Review scope | Required example/deployment/tooling comparison documented here; not a historical technology proposal. |

Canonical repository guidance remains authoritative. The Go rule's GORM reference describes the
historical proposal and allows ticket-defined departures; a service ticket must state its reviewed
stack. Research cmd-level wiring is adapted to the canonical thin cmd/internal/service boundary.
No instruction conflict is resolved by silently overriding a rule. Existing AGENTS/ai spec-first
workflow is retained; historical editor/model suggestions neither select tooling nor certify
privacy, cost or a completed development pilot.

## Previous versus reviewed decisions

Stable inventory IDs P4-01–54 map one-to-one to decision IDs D19-01–54. **Retain** keeps a direction;
**replace** changes a default; **qualify** limits scope or adoption; **defer** withholds selection
until named evidence exists. A deferred subfeature remains deferred within a qualified row.
All runtime gates are open. Gate IDs are linked to the acceptance boundary later in this document.

“All Go” means the eight business services. “PG owners” means product, document, pricing, policy
and payment, with legacy auth included in coexistence; dashboard/chat PostgreSQL requirements are
not evidenced and policy-search uses Elasticsearch. Runtime dependencies apply only where used;
sqlc/Buf/plugins are build tools, Goose a controlled release tool, and test/format tools development
dependencies. Standard-library choices add no separate third-party runtime package.

### Service stack and contracts

| Inventory / decision | Previous proposal and source | Prior study | Disposition | Reviewed choice, rationale and affected scope | Assessment / gate |
| --- | --- | --- | --- | --- | --- |
| <a id="p4-01"></a>P4-01 / D19-01 | Idiomatic Go, interfaces, shared helpers, unspecified structure. [A:86–92, 160–163, 740–742](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L86) | D covers composition/shared boundaries | **qualify** | Service-owned inward dependencies, thin cmd and internal/service assembly; share infrastructure only after demonstrated need, never a shared business model. All Go. | [Assessment](#composition-and-lifecycle); [G19-01](#g19-01) |
| <a id="p4-02"></a>P4-02 / D19-02 | Minimal OCI containers and stateless Kubernetes Deployments. [A:94–103, 362–364](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L94) | C covers process configuration only | **qualify** | Retain containers, Services and external durable state; define per-service images, probes, resources and bounded drain. Chat has live process-local sessions and Chrome needs a browser image; minimal footprint is unmeasured. | [Assessment](#deployment-comparison); [G19-03](#g19-03) |
| <a id="p4-03"></a>P4-03 / D19-03 | Viper configuration. [A:100–103, 486](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L100) | C directly proposes replacement | **replace** | Typed startup snapshot; provisional caarlos0/env/v11 or explicit standard-library parsing. No observed layered-source need justifies Viper by default. All Go. | [Assessment](#configuration-and-secrets); [G19-02](#g19-02) |
| <a id="p4-04"></a>P4-04 / D19-04 | Wire-generated DI. [A:483](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L483) | D directly proposes replacement | **replace** | Manual constructors and consumer-owned ports; no new Wire dependency. Framework use needs measured graph/lifecycle benefit. All Go. | [Assessment](#composition-and-lifecycle); [G19-01](#g19-01) |
| <a id="p4-05"></a>P4-05 / D19-05 | Command/query handlers implemented with GORM. [A:487](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L487) | P:408 and D address adapter/interface boundaries | **qualify** | Optional use-case handlers independent of persistence; direct methods where sufficient. No mandatory generic bus/decorator abstraction. | [Assessment](#composition-and-lifecycle); [G19-01](#g19-01), [G19-04](#g19-04) |
| <a id="p4-06"></a>P4-06 / D19-06 | Replace synchronous REST with grpc-go. [A:131–139, 176–179, 231–237, 480](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L131) | None | **qualify** | Internal versioned Go RPCs with deadlines and safe errors; preserve Java HTTP callers through tested adapters or separately migrated clients. All Go/callers. | [Assessment](#transport-and-validation); [G19-05](#g19-05) |
| <a id="p4-07"></a>P4-07 / D19-07 | Protobuf and generated clients/OpenAPI. [A:135–139, 160–163, 373–374, 401–402, 415–416](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L135) | None | **qualify** | Preserve presence, numbers/names, JSON and consumers; pin generators and generate only needed outputs. Schema checks cannot prove HTTP parity. | [Assessment](#transport-and-validation); [G19-01](#g19-01), [G19-05](#g19-05) |
| <a id="p4-08"></a>P4-08 / D19-08 | grpc-gateway preserves HTTP automatically. [A:136–137, 231–237, 308–313, 327–328, 377–378, 392, 405–406, 421–422, 442–443, 481](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L136) | None | **qualify** | Choose generated v2 mapping or explicit HTTP adapter per fixture-backed route; Java gateway remains interim edge. Include offer/search, principal override and document bytes. | [Assessment](#transport-and-validation); [G19-05](#g19-05), [G19-06](#g19-06) |
| <a id="p4-09"></a>P4-09 / D19-09 | protoc-gen-validate. [A:485](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L485) | None | **replace** | Provisional Protovalidate schema/runtime for transport shape, application/domain for invariants. Archived PGV is unsuitable as a new default; cover direct HTTP and stream entry points. | [Assessment](#transport-and-validation); [G19-01](#g19-01), [G19-05](#g19-05) |
| <a id="p4-10"></a>P4-10 / D19-10 | GORM for seven PostgreSQL services. [A:307–313, 360–361, 375–376, 391–392, 403–404, 417–418, 482](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L307) | P directly proposes replacement | **replace** | Provisional sqlc with native pgx/v5/pgxpool for actual PG owners. GORM remains a justified local exception; no PG requirement inferred for chat/dashboard/search. | [Assessment](#postgresql-and-migrations); [G19-04](#g19-04) |
| <a id="p4-11"></a>P4-11 / D19-11 | New JSONB migration and automatic index/performance gains. [A:107–114, 203–214, 448–459, 694–705](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L107) | P covers JSONB and direct-query exceptions | **qualify** | Java product already has JSONB mapping/default; inspect effective rows/schema/indexes and benchmark queries before claims. No second blanket MongoDB migration. | [Assessment](#postgresql-and-migrations); [G19-04](#g19-04), [G19-14](#g19-14) |
| <a id="p4-12"></a>P4-12 / D19-12 | Kafka, Shopify Sarama or kafka-go, JSON events. [A:165–166, 221, 484](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L165) | P:335–345 touches transaction/event atomicity | **qualify** | Retain Kafka/topics/policy keys/JSON; provisional IBM Sarama, kafka-go conditional. Kafka 4.1 compatibility, partitioning, commits and external effects require proof. Policy and four consumers. | [Assessment](#kafka-and-event-processing); [G19-07](#g19-07) |
| <a id="p4-13"></a>P4-13 / D19-13 | olivere/elastic for search/analytics. [A:111, 211, 308, 314, 368–376, 466–467](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L111) | None | **replace** | Provisional official go-elasticsearch/v8 for configured 8.19.6; typed or low-level DSL inside adapters. Preserve mappings/refresh and query semantics. Search/dashboard. | [Assessment](#search-and-dashboard); [G19-08](#g19-08) |
| <a id="p4-14"></a>P4-14 / D19-14 | Gorilla WebSocket plus internal streams and persisted chat. [A:307, 350–364](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L307) | D/C mention lifecycle/configuration only | **qualify** | Retain browser WebSocket; qualify Gorilla. Defer internal streams/history without a caller/retention contract. Require authenticated handshake and approved safe browser format. | [Assessment](#realtime-chat); [G19-11](#g19-11) |
| <a id="p4-15"></a>P4-15 / D19-15 | Large document gRPC upload/download streaming. [A:389–392](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L389) | None | **defer** | Current query returns document bytes within a result; payload/consumer evidence must justify a separate streaming API. SDK streaming is an internal implementation option. | [Assessment](#object-storage); [G19-05](#g19-05), [G19-09](#g19-09) |
| <a id="p4-16"></a>P4-16 / D19-16 | Internal-only search with streamed/paged results. [A:461–474, 783–786](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L461) | None | **qualify** | Unary bounded list with HTTP bridge for Java gateway; retain 100-hit cap then application sorting. Defer pages/streams pending real consumer/load evidence. | [Assessment](#search-and-dashboard); [G19-05](#g19-05), [G19-08](#g19-08) |
| <a id="p4-17"></a>P4-17 / D19-17 | MinIO SDK replaces blobs/files, encryption/versioning assumed. [A:116–124, 185–187, 222–223, 309, 311, 384–392, 412–420](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L116) | P/C discuss adapter/configuration integration | **qualify** | Java already uses MinIO; qualify v7 SDK for document/payment and preserve mixed inline/key reads and statement keys. Server support/security, trust and storage policy block adoption. | [Assessment](#object-storage); [G19-09](#g19-09) |
| <a id="p4-18"></a>P4-18 / D19-18 | chromedp replaces jsreport during first rewrite. [A:126–129, 225, 309, 749–750, 771–774](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L126) | None | **defer** | Candidate only after effective-template/PDF/browser proof; permit interim jsreport HTTP rendering with its own support/capacity review. Document. | [Assessment](#document-rendering); [G19-10](#g19-10) |
| <a id="p4-19"></a>P4-19 / D19-19 | Unspecified Go MVEL replacement. [A:312, 429–441, 751–754, 790–794](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L312) | None | **defer** | Compare Expr, CEL-Go or restricted exact-decimal evaluator against effective rules; keep Java authority and MVEL rows until differential parity. Pricing/policy. | [Assessment](#pricing); [G19-12](#g19-12) |
| <a id="p4-20"></a>P4-20 / D19-20 | PostgreSQL tariffs, versions/effective dates/cache, internal gRPC. [A:224, 433–443](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L224) | P/C cover storage versus process configuration | **qualify** | Retain actual tariff store; order/version/cache are new design decisions, not existing guarantees. Keep HTTP pricing bridge until callers migrate and preserve decimal/date results. | [Assessment](#pricing); [G19-04](#g19-04), [G19-05](#g19-05), [G19-12](#g19-12) |

### Platform and phase dependencies

| Inventory / decision | Previous proposal and source | Prior study | Disposition | Reviewed choice, rationale and affected scope | Assessment / gate |
| --- | --- | --- | --- | --- | --- |
| <a id="p4-21"></a>P4-21 / D19-21 | Keycloak/operator replaces auth; unspecified Go JWT library. [A:306, 334–348, 810–815](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L306) | None | **defer** | Separate Phase 5 selection/cutover; first establish legacy trust/claims/permissions and verifier requirements. No presumed OIDC issuer or accepted verifier pin. | [Assessment](#security-and-phase-5); [G19-06](#g19-06) |
| <a id="p4-22"></a>P4-22 / D19-22 | Envoy replaces gateway and owns translation/JWT. [A:305, 318–332, 816–822](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L305) | None | **defer** | Current Java gateway plus explicit service HTTP adapters during Phase 4. One translation owner per route after edge/identity/WebSocket parity and rollback. | [Assessment](#security-and-phase-5); [G19-06](#g19-06), [G19-14](#g19-14) |
| <a id="p4-23"></a>P4-23 / D19-23 | Linkerd supplies universal mTLS/retries/resilience. [A:133–135, 165–168, 191–194, 824–830](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L133) | None | **defer** | No installed mesh dependency. Mixed-peer policy, workload identity and actual resilience features need separate proof; service deadlines/idempotency remain required. | [Assessment](#security-and-phase-5); [G19-06](#g19-06), [G19-14](#g19-14) |
| <a id="p4-24"></a>P4-24 / D19-24 | Kubernetes discovery replaces Consul. [A:100–101, 181–183, 220, 637–642](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L100) | None | **qualify** | Retain Service DNS, environment naming and labels; distinct Java/Go selectors and explicit HTTP/gRPC ports. Do not equate DNS with identity, weighted routing or verified Consul retirement. | [Assessment](#deployment-comparison); [G19-03](#g19-03), [G19-14](#g19-14) |
| <a id="p4-25"></a>P4-25 / D19-25 | OTel traces, Tempo/Jaeger and collector options. [A:144–145, 189–190, 499, 522–541](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L144) | P:158 and D:241 touch integration | **qualify** | OTel traces through Alloy to configured Tempo path; verify Java B3/Go W3C propagation, sampling and bounded exporter shutdown. No direct backend coupling. All Go. | [Assessment](#observability); [G19-13](#g19-13) |
| <a id="p4-26"></a>P4-26 / D19-26 | JSON stdout automatically collected/correlated. [A:146–147, 500, 545–556](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L146) | D/C discuss bootstrap and redaction | **qualify** | Retain slog JSON in deployed environments; add safe trace fields explicitly. QA stdout collection is not configured in reviewed Alloy values; no payload/debug leakage. | [Assessment](#observability); [G19-13](#g19-13) |
| <a id="p4-27"></a>P4-27 / D19-27 | Universal Prometheus metrics endpoint/scraping. [A:148–149, 501, 558–569](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L148) | P:158, D:242 touch pool/registry helpers | **qualify** | OTel metrics to Alloy direction; resolve downstream ingestion. Optional restricted scrape endpoint only with an owned collector/monitor route and duplicate policy. | [Assessment](#observability); [G19-13](#g19-13) |
| <a id="p4-28"></a>P4-28 / D19-28 | Grafana dashboards and SLA/KPI alerts. [A:150–151, 502–503, 571–583](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L150) | None | **qualify** | Retain goal; choose per-service SLOs/rollback signals after delivery, cardinality and capacity checks. Default dashboards/data sources do not prove service coverage. | [Assessment](#observability); [G19-13](#g19-13), [G19-14](#g19-14) |
| <a id="p4-29"></a>P4-29 / D19-29 | Common health/readiness/liveness. [A:504, 585–594](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L504) | D lifecycle and C startup checks | **qualify** | Readiness reflects serving ability; liveness process health. Render probes/ports and test startup/drain; collector outage must not create restart loops. | [Assessment](#composition-and-lifecycle); [G19-03](#g19-03) |
| <a id="p4-30"></a>P4-30 / D19-30 | Alloy permanently collects all signals. [A:650–687, especially 665–680](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L650) | None dedicated | **qualify** | Retain configured QA intermediary and Zipkin bridge; prove logs/metrics/traces end to end. Local-dev collector is absent; explicit disabled/local mode needed. | [Assessment](#observability); [G19-13](#g19-13) |
| <a id="p4-31"></a>P4-31 / D19-31 | Kind local, K3s QA, completed Java platform lift. [A:613–648](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L613) | C assumes local/QA composition | **qualify** | Retain checked-in environment/bootstrap pattern, not a claim of live completion. Reconcile effective versions, ownership, endpoints and Go resource suitability. | [Assessment](#deployment-comparison); [G19-03](#g19-03) |
| <a id="p4-32"></a>P4-32 / D19-32 | ArgoCD or Flux, pipelines, scanning and blue/green. [A:155–157, 643–644](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L155) | C proposes Kustomize/Flux ownership | **qualify** | Carry forward actual QA service Flux/main path and Make-managed infrastructure; add explicit Go Make/CI gates later. No demonstrated weighted/blue-green controller or blanket GitOps ownership. | [Assessment](#development-and-validation-tooling); [G19-01](#g19-01), [G19-03](#g19-03), [G19-14](#g19-14) |
| <a id="p4-33"></a>P4-33 / D19-33 | Phase 3 must first move product from MongoDB. [A:689–705, 775–778](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L689) | P:325–333 assumes Phase 3 schema | **qualify** | Validate already implemented PG default against deployed data, seeders and ownership. Residual Mongo keys/resources prevent a retirement conclusion. | [Assessment](#postgresql-and-migrations); [G19-04](#g19-04), [G19-14](#g19-14) |
| <a id="p4-34"></a>P4-34 / D19-34 | Specific local models/editors plus AI workflow pilot. [A:707–733](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L707) | None dedicated | **qualify** | Retain existing AGENTS/ai spec-first and Make workflow. No editor/model requirement, privacy/cost guarantee or historical pilot-success claim inferred from prose. | [Assessment](#development-and-validation-tooling); [G19-01](#g19-01) |
| <a id="p4-35"></a>P4-35 / D19-35 | Strangler, no production traffic, tests then decommission. [A:735–759, 763–767](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L735) | P/D/C pilot suggestions | **qualify** | Retain independent deployment and internal parity tests; isolate consumer/scheduler effects as well as HTTP traffic. Decommission only after rollback window and dependency evidence. | [Assessment](#deployment-comparison); [G19-07](#g19-07), [G19-14](#g19-14) |
| <a id="p4-36"></a>P4-36 / D19-36 | 1/10/50/100% via gateway or mesh. [A:760–767, 805–808](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L760) | None | **defer** | Percentages are illustrative; first specify/prove actual routing mechanism, thresholds, dwell and rollback. HTTP weights cannot control Kafka, scheduled imports or existing sockets. | [Assessment](#security-and-phase-5); [G19-14](#g19-14) |
| <a id="p4-37"></a>P4-37 / D19-37 | Fixed document-first low-risk order. [A:768–800](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L768) | P/D/C all assume document then product pilots | **replace** | Conditional product-first pilot; search then dashboard; document/chat gated; pricing then policy then payment as default continuation. Dependencies and passed gates may reorder work. | [Assessment](#deployment-comparison); [G19-14](#g19-14) |
| <a id="p4-38"></a>P4-38 / D19-38 | Direct Tempo, retire Zipkin and archive traces to MinIO. [A:836–852](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L836) | None | **qualify** | Retain Alloy boundary; defer Zipkin/bridge retirement and archive design until consumers, retention and restore are proven. Storage security gate also applies to any archive backend. | [Assessment](#observability); [G19-09](#g19-09), [G19-15](#g19-15) |
| <a id="p4-39"></a>P4-39 / D19-39 | Phase 6 HPA, NetworkPolicies, tuning and cleanup. [A:164–168, 854–870](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L164) | None | **qualify** | Delay HPA/tuning to measured load; move necessary access restrictions before exposure. NetworkPolicy alone is not a zero-trust mesh. Defer cleanup until rollback closes. | [Assessment](#deployment-comparison); [G19-03](#g19-03), [G19-06](#g19-06), [G19-15](#g19-15) |
| <a id="p4-40"></a>P4-40 / D19-40 | Zero trust, short-lived credentials, Vault/Secrets/mTLS. [A:158–159, 191–194](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L158) | C covers secret delivery, not complete security model | **qualify** | Keep security objective; specify endpoint/resource authorization, authenticated propagation, CA trust, credential owner/rotation and collector access. Vault/mesh are unselected future mechanisms. | [Assessment](#security-and-phase-5); [G19-02](#g19-02), [G19-06](#g19-06) |

### Research and engineering requirements

| Inventory / decision | Previous proposal and source | Prior study | Disposition | Reviewed choice, rationale and affected scope | Assessment / gate |
| --- | --- | --- | --- | --- | --- |
| <a id="p4-41"></a>P4-41 / D19-41 | sqlc/native pgx default and direct-query exceptions. [P:3–9, 99–111, 137–204, 298–311, 384–408](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/migration/phase-4/postgresql-data-access-strategy.md#L3) | P direct | **qualify** | Provisional for actual PG adapters; generated types stay private, dynamic SQL parameterized, tool/runtime pinned independently. No universal performance assertion. | [Assessment](#postgresql-and-migrations); [G19-01](#g19-01), [G19-04](#g19-04) |
| <a id="p4-42"></a>P4-42 / D19-42 | database/sql, Ent, Bun, sqlx, Jet versus GORM/pgx. [P:17–28, 47–135, 347–360](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/migration/phase-4/postgresql-data-access-strategy.md#L17) | P direct | **qualify** | database/sql/pgx stdlib remains valid; use direct pgx for small/dynamic needs. Defer other stacks until representative query/maintenance evidence shows local benefit; avoid duplicate CRUD stacks. | [Assessment](#postgresql-and-migrations); [G19-04](#g19-04) |
| <a id="p4-43"></a>P4-43 / D19-43 | Versioned release SQL, no production AutoMigrate. [P:155–161, 362–374](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/migration/phase-4/postgresql-data-access-strategy.md#L155); [G:Contracts and persistence](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/ai/rules/go-development.md) | P direct | **retain** | One controlled schema executor per DB, separate from replicas. Goose provisional; baseline populated DBs, resolve Java auto-DDL, rehearse expand/contract and forward recovery. | [Assessment](#postgresql-and-migrations); [G19-04](#g19-04) |
| <a id="p4-44"></a>P4-44 / D19-44 | Explicit transactions/retries/idempotency/outbox/tests. [P:262–296, 315–345](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/migration/phase-4/postgresql-data-access-strategy.md#L262) | P direct | **retain** | Operation-specific isolation, exact money, conflicts and real-store tests; outbox conditional on publication guarantees, never presumed SQL/Kafka/MinIO atomicity. | [Assessment](#kafka-and-event-processing); [G19-04](#g19-04), [G19-07](#g19-07), [G19-09](#g19-09) |
| <a id="p4-45"></a>P4-45 / D19-45 | Manual constructors; Fx/dig/do alternatives. [D:3–16, 42–80, 250–289](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/migration/phase-4/dependency-injection-strategy.md#L3) | D direct | **retain** | Manual DI default; conditional Fx comparison only for repeated measured graph/lifecycle problems. No container types in domain/application. | [Assessment](#composition-and-lifecycle); [G19-01](#g19-01) |
| <a id="p4-46"></a>P4-46 / D19-46 | Study composition root and signal/errgroup runner. [D:82–106, 150–248](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/migration/phase-4/dependency-injection-strategy.md#L82); [G:Service structure and dependencies](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/ai/rules/go-development.md) | D direct | **qualify** | Thin cmd and internal/service ownership per canonical rules; reverse partial cleanup and fresh bounded shutdown context, owned goroutines and no blanket shared runner. | [Assessment](#composition-and-lifecycle); [G19-01](#g19-01), [G19-03](#g19-03) |
| <a id="p4-47"></a>P4-47 / D19-47 | caarlos0/env with global required policy/wrapped errors. [C:3–17, 61–108, 146–285, 489–538](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/migration/phase-4/configuration-strategy.md#L3) | C direct | **qualify** | Typed immutable subconfigs, per-field absence/empty/default rules, semantic validation and redacted errors; optional integrations remain optional. | [Assessment](#configuration-and-secrets); [G19-02](#g19-02) |
| <a id="p4-48"></a>P4-48 / D19-48 | Alternative loaders, dotenv/direnv and flags. [C:33–44, 77–144, 393–421, 556–567](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/migration/phase-4/configuration-strategy.md#L33) | C direct | **qualify** | Standard library valid for small schemas, explicit Make-led local loading optional; richer loaders deferred until multi-source requirement. No production dotenv dependency. | [Assessment](#configuration-and-secrets); [G19-01](#g19-01), [G19-02](#g19-02) |
| <a id="p4-49"></a>P4-49 / D19-49 | SOPS/external Secrets and automatic hashed rollouts. [C:19–31, 277–391, 577–598](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/migration/phase-4/configuration-strategy.md#L19) | C direct | **replace** | Retain generated ConfigMaps; actual fixed-name Secrets are separately created/copied. Controlled restart/overlap for env and startup-read files; no automatic Secret rollout established. | [Assessment](#configuration-and-secrets); [G19-02](#g19-02) |
| <a id="p4-50"></a>P4-50 / D19-50 | Config/redaction/render tests and optional envdoc. [C:423–487, 525–538, 601–611](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/migration/phase-4/configuration-strategy.md#L423) | C direct | **qualify** | Retain tests and rendered-schema contract; defer envdoc until useful. Keep tariffs/templates/business data out of process configuration. | [Assessment](#configuration-and-secrets); [G19-01](#g19-01), [G19-02](#g19-02) |
| <a id="p4-51"></a>P4-51 / D19-51 | Document then product executable research pilots/Fx spike. [P:313–345](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/migration/phase-4/postgresql-data-access-strategy.md#L313); [D:265–277](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/migration/phase-4/dependency-injection-strategy.md#L265); [C:540–554](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/migration/phase-4/configuration-strategy.md#L540) | P/D/C direct | **qualify** | Product-first bounded evidence recommendation; document with interim jsreport only after storage/event gates. Fx spike conditional; all experiments belong to future tickets. | [Assessment](#deployment-comparison); [G19-01](#g19-01), [G19-10](#g19-10), [G19-14](#g19-14) |
| <a id="p4-52"></a>P4-52 / D19-52 | Reuse example validation/migrations/mocks/tests/logging. Review scope: example comparison; [G:Contracts and persistence, Phase 4 migration and operations, Tests](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/ai/rules/go-development.md#L4) | Three studies do not cover this comparison comprehensively | **qualify** | Reuse layer separation and real DB/listener test intent; adapt pins/coverage/drift checks. Avoid startup migration, raw payload logs and unrestricted debug exposure. | [Assessment](#development-and-validation-tooling); [G19-01](#g19-01), [G19-04](#g19-04), [G19-05](#g19-05) |
| <a id="p4-53"></a>P4-53 / D19-53 | Canonical formatting and Make-only operations. [F:Policy and Verification](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/ai/rules/go-formatting.md); [V:Required checks](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/ai/rules/go-validation.md); [G:Scope and Makefile interface](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/ai/rules/go-development.md) | P names generation checks; no dedicated tooling study | **retain** | gofumpt/goimports/golines 100-column policy, explicit pinned order, changed handwritten scope and non-mutating check; no direct-tool fallback for missing targets. | [Assessment](#development-and-validation-tooling); [G19-01](#g19-01) |
| <a id="p4-54"></a>P4-54 / D19-54 | Unspecified module/toolchain/CI/security coverage. [G:Scope and Makefile interface](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/ai/rules/go-development.md); [V:Discover the validation boundary, Required checks](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/ai/rules/go-validation.md); [A:740–742](https://github.com/igor-baiborodine/insurance-hub/blob/30007edfefe2d7bdc8e9eecd8951deb70ac50344/docs/system-overview-and-migration-analysis.md#L740) | No dedicated study | **qualify** | Service/contract owners choose compatible versions and Make/CI coverage, including cmd, consumers and nested modules. Root placeholder and example pins are insufficient. | [Assessment](#development-and-validation-tooling); [G19-01](#g19-01) |

## Detailed assessments

### Composition and lifecycle

Manual constructors keep dependencies visible to the compiler and make small application tests
easy to assemble. Follow the [canonical package responsibilities](../../../ai/rules/go-development.md#service-structure-and-dependencies):
thin `cmd/`, construction/startup/shutdown in `internal/service`, domain/application-owned ports,
and separate transport/storage adapters. Commands and queries are useful organization where
complexity warrants them; generic handlers, decorators and a repository-wide business container
are not required. Constructor wiring has reviewable boilerplate, but the service still owns all
resource acquisition and failure handling.

[Wire](https://github.com/google/wire) was archived on 25 August 2025 and says it is unmaintained
(rechecked 30 September 2026). Replace that proposed build-time dependency. Manual DI needs no
third-party DI license or version. [Fx lifecycle hooks](https://uber-go.github.io/fx/lifecycle.html)
can organize ordered startup/reverse shutdown, but a runtime graph adds registration, reflection,
diagnostic and dependency costs. The 29 September observations were Fx v1.24.0, dig v1.19.0 and
samber/do v2.1.0; each publishes an MIT-style license, while Wire publishes Apache-2.0.
See [Fx](https://github.com/uber-go/fx), [dig](https://github.com/uber-go/dig) and
[do](https://github.com/samber/do). These alternatives remain conditional: measure repeated wiring
or lifecycle defects across two different services before a bounded Fx comparison. Raw dig adds
custom lifecycle work; no requirement currently justifies do's container/scoping conventions.

Record cleanup immediately after each successful acquisition. If a later client, validator or
listener fails, clean up in reverse order and preserve the initiating error. Every goroutine,
consumer, scheduler, renderer and listener needs an owner, cancellation and bounded termination.
[`errgroup.WithContext`](https://pkg.go.dev/golang.org/x/sync/errgroup#WithContext) cancels its context
when work fails or Wait returns; cleanup must use a fresh bounded context. Mark unready, stop new
work, drain within the actual Pod budget, then flush/close clients and exporters, reporting errors.
The example's pprof shutdown uses a cancelled context; the DI study's unbounded telemetry cleanup
is also unsuitable. HTTP shutdown does not own hijacked WebSockets; chat needs explicit connection
tracking. Validate partial startup, fatal workers, repeated stop and force-stop under G19-03.

### PostgreSQL and migrations

Replace blanket GORM with provisional native `pgx/v5`/`pgxpool` plus build-time sqlc for services
that actually own PostgreSQL state. Keep generated SQL types inside adapters, map them to domain
types, and use direct parameterized pgx for a small repository or a measured dynamic-query need.
This trades ORM conventions for reviewable SQL and an explicit schema/generation maintenance cost.
The [sqlc pgx guide](https://docs.sqlc.dev/en/latest/guides/using-go-and-pgx.html),
[datatype mapping](https://docs.sqlc.dev/en/latest/reference/datatypes.html) and
[transaction support](https://docs.sqlc.dev/en/latest/howto/transactions.html) establish capabilities,
not correctness of Insurance Hub queries. `database/sql` with pgx stdlib remains viable; the example
demonstrates that approach. GORM, Bun, Ent, sqlx or Jet need representative query/relationship and
maintenance evidence before a local exception. Avoid two ordinary CRUD stacks without a reason.

Actual PostgreSQL business owners are product, pricing, policy, payment and document; auth is a
sixth legacy schema dependency. Dashboard/search use Elasticsearch, and inspected chat has no
relational adapter. Product's [entity](../../../legacy/product-service/src/main/java/pl/altkom/asc/lab/micronaut/poc/product/service/infrastructure/adapters/db/model/ProductEntity.java)
maps `product(code, definition jsonb)`, not the research's illustrative products table. Its
[application config](../../../legacy/product-service/src/main/resources/application.yml) defaults
to PostgreSQL, while the [base ConfigMap](../../../k8s/apps/svc/product/base/legacy/kustomization.yaml)
still has residual `MG_HOST`/`MG_PORT`. Neither residual keys nor code alone establish deployed data
or MongoDB retirement. [ProductDefinition](../../../legacy/product-service/src/main/java/pl/altkom/asc/lab/micronaut/poc/product/service/infrastructure/adapters/db/model/ProductDefinition.java)
and its question subtypes require JSON shape, unknown-field, discriminator, null and decimal tests.
The [controller](../../../legacy/product-service/src/main/java/pl/altkom/asc/lab/micronaut/poc/product/service/infrastructure/adapters/web/ProductsController.java)
has list/get operations, but the [loader](../../../legacy/product-service/src/main/java/pl/altkom/asc/lab/micronaut/poc/product/service/init/DataLoader.java)
seeds missing records: a read pilot must separately control startup writes.

Preserve exact money and date-only semantics. PostgreSQL [numeric](https://www.postgresql.org/docs/17/datatype-numeric.html)
is exact; binary floats are not a substitute for Java BigDecimal. Specify numeric scale/null/overflow
and JSON serialization explicitly. Policy termination uses inclusive days and scale-20 HALF_UP
proration; pricing rounds each markup to two decimals. Test SQL predicates, conflict row counts,
unique constraints, commit uncertainty and pool limits against disposable PostgreSQL matching the
configured major 17. [`pgxpool.BeginTx`](https://pkg.go.dev/github.com/jackc/pgx/v5/pgxpool#Pool.BeginTx)
does not automatically roll back on begin-context cancellation. Bound queries and pool acquisition;
close rows/connections and inspect iteration/cleanup errors. Choose isolation and retries per operation.

Retain versioned SQL under one release-controlled executor per database, with Goose provisional.
Baseline the populated schema before running migrations; inventory Java seeders and auto-DDL.
The business YAMLs use Hibernate update, while [auth YAML](../../../legacy/auth-service/src/main/resources/application.yml)
contains CREATE_DROP. Effective deployment settings require inspection, not a claim of current data
loss. Reconcile these writers before a migration tool becomes authoritative. Expand compatibly,
backfill/resume with invariants, then contract only after the Java rollback window. Goose
[SQL annotations](https://github.com/pressly/goose#sql-migrations) support transaction controls, but
locking, DDL credentials, packaging, failure repair and execution ownership must be designed.
PostgreSQL [concurrent index creation](https://www.postgresql.org/docs/17/sql-createindex.html)
cannot run inside a transaction and may leave an invalid index on failure. Rehearse recovery;
a Down script alone cannot restore lost data. golang-migrate is a bounded executor alternative
if deployment requirements warrant it. SQL transactions do not cover Kafka or MinIO effects.

Observed 29 September: [sqlc](https://github.com/sqlc-dev/sqlc/releases) v1.31.1,
[pgx](https://github.com/jackc/pgx/releases) v5.11.0,
[GORM](https://github.com/go-gorm/gorm/releases) v1.31.2 and
[Goose](https://github.com/pressly/goose/releases) v3.28.0, all publishing MIT licenses.
The reviewed pgx version requires Go 1.25 and Goose Go 1.26; the example's pgx/v4 and Goose v3.24.3
are different pins. Compatibility, query plans/indexes and performance remain G19-01/04 evidence,
not a reason to select the latest observed releases automatically.

### Configuration and secrets

Replace Viper's default with a service-owned typed startup snapshot. Provisionally prefer
`caarlos0/env/v11`, or `os.LookupEnv` with explicit standard-library parsers for a small schema.
Viper offers files, flags, remote sources and precedence rules that the checked-in Kubernetes
contract does not need. Observed 29 September: [Viper](https://github.com/spf13/viper/releases)
v1.21.0 and [caarlos0/env](https://github.com/caarlos0/env/releases) v11.4.1, both MIT;
the latter is a feature-complete parser with no external runtime dependencies. It still adds
reflection/tags and requires semantic validation. kelseyhightower/envconfig demonstrates typed
loading in the example; sethvargo/go-envconfig, cleanenv, Koanf and Confita remain deferred until
a richer lookup/source requirement exists. Their adoption would require a fresh maintenance,
version, license and security assessment. dotenv/direnv are optional explicit local tooling,
not required cluster runtime sources. Standard flags can serve a bounded command-line need.

The [parser source](https://github.com/caarlos0/env/blob/v11.4.1/env.go) distinguishes required
presence from nonempty values; defaults also apply to explicitly empty input. Classify fields
individually and precheck raw values when absent must differ from empty. Avoid global
RequiredIfNoDef, which makes conditionally optional integrations required. Validate endpoint
schemes, ranges, durations, TLS/credential pairs and readable files before opening resources.
Inject immutable subconfigs only into relevant constructors. Tariff rules, templates and business
data do not become process configuration.

Redact errors before logging: conversion errors, wrapped parser errors and errors.Join can expose
values or paths. Do not dump structs, URLs, OnSet values or credentials. `,unset` does not erase
all copies of a secret, and `,file` reads once rather than implementing reload. Test with sentinel
secrets, injected environments, malformed/absent/empty values and disabled integrations. Adapt the
example's implicit dotenv, unresolved placeholder and production-only JSON logging behavior.

The [document base](../../../k8s/apps/svc/document/base/legacy/kustomization.yaml) and
[QA overlay](../../../k8s/overlays/qa/svc/document/legacy/kustomization.yaml) use legacy PG/MinIO/Kafka
keys, not the study's sample POSTGRES_URL/MINIO_ENDPOINT contract. Map any Go schema deliberately in
both environments. Generated ConfigMap references may change with content, but credentials use
fixed-name Secret references created/copied separately. [Kubernetes environment injection](https://kubernetes.io/docs/tasks/inject-data-application/distribute-credentials-secure/)
requires restart to observe Secret changes. SOPS, ExternalSecrets, generated Secrets and automatic
rotation are not evidenced by these service resources. Verify actual ownership, restart, new-key
authentication, overlap and revocation; mounted files also need an explicit reload or restart policy.
Local-dev has no configured Alloy; mandatory telemetry there would invent an endpoint. G19-02
requires rendered key/schema checks and tested ConfigMap versus Secret rollout behavior.

### Transport and validation

Qualify grpc-go and versioned Protobuf for new Go synchronous calls; preserve an explicit HTTP
compatibility boundary while Java clients remain. The [Java policy gateway](../../../legacy/agent-portal-gateway/src/main/java/pl/altkom/asc/lab/micronaut/poc/gateway/PolicyGatewayController.java)
defaults missing `q` to `*` and replaces caller agentLogin with authenticated Principal on create.
Its external `/api/policies/create` and `/terminate` are not identical to the backend routes.
The browser's [API client](../../../legacy/web-vue/src/components/http/ApiClient.js) sends a bearer
token to `/api/`; policy [calls pricing over HTTP](../../../legacy/policy-service/src/main/java/pl/altkom/asc/lab/micronaut/poc/policy/infrastructure/adapters/restclient/PricingClient.java).
Generated RPC names or OpenAPI cannot reproduce these transformations alone. Choose a small HTTP
adapter or grpc-gateway v2 per route after capturing actual method/path/status/header/content-type,
JSON, principal and failure fixtures; migrate a Java caller to gRPC only as a tested consumer change.

[Protobuf presence](https://protobuf.dev/programming-guides/field_presence/) and
[ProtoJSON](https://protobuf.dev/programming-guides/json/) differ from Java null/default, field-name,
integer, bytes and unknown-field behavior. Use explicit presence where absent differs from zero,
keep domain types separate, reserve removed names/numbers and test binary and JSON evolution.
Product question tags and offer-answer tags differ; money/date/document/list encoding need fixtures.
Legacy `@NotNull` mainly annotates operation parameters: it does not justify making every nested
field required. Preserve business codes, safe wrapped-domain errors and observed HTTP responses;
do not copy raw SQL/CEL/internal exceptions. Offer expiry, permissions and pricing remain domain work.

Replace PGV with provisional Protovalidate schema/runtime validation. [PGV](https://github.com/bufbuild/protoc-gen-validate)
was archived on 27 May 2026 and recommends the replacement. [Protovalidate](https://protovalidate.com/migration-guides/migrate-from-protoc-gen-validate/)
uses runtime validation rather than PGV-generated Validate methods; schema imports, descriptors,
runtime and interceptor still need compatible pins. Explicit-presence required fields also need
value rules if empty is forbidden. [Standard rules](https://protovalidate.com/schemas/standard-rules/)
can check shape; richer CEL rules add evaluation/diagnostic cost and do not own business state.
Validate every entry point: direct HTTP bypasses a unary gRPC interceptor; streams need per-message
checks. Standard-library validation is viable for a small HTTP-only boundary with shared policy/tests.

Set deadlines, cancellation, payload limits and operation-specific retries; a timed-out write may
already have committed. [grpc-go deadlines](https://grpc.io/docs/guides/deadlines/) are not set by
default. Bound GracefulStop with force-stop, coordinate readiness, and choose HTTP or
[standard gRPC health](https://grpc.io/docs/guides/health-checking/) against the actual Pod probe
capabilities. Distinct HTTP/gRPC ports and Java/Go selectors prevent accidental mixed routing.
The current gateway remains the Phase 4 edge; [Envoy transcoding](https://www.envoyproxy.io/docs/envoy/latest/configuration/http/http_filters/grpc_json_transcoder_filter)
is a separate Phase 5 option with descriptor/annotation, identity and custom-route parity gates.
One route has one translation owner. Browser WebSocket and SDK object streaming do not become
gRPC streams automatically. Defer external document streams and new search pages/streams without
payload/caller evidence. Maintaining two protocol surfaces costs tests, listeners and release coordination.

Observed 29 September: [grpc-go](https://github.com/grpc/grpc-go/releases) v1.84.0 (Apache-2.0),
[grpc-gateway](https://github.com/grpc-ecosystem/grpc-gateway/releases) v2.30.0 (BSD-3-Clause),
[Protobuf compiler](https://github.com/protocolbuffers/protobuf/releases) v36.2 (BSD-style),
[Protovalidate Go](https://github.com/bufbuild/protovalidate-go/releases) v1.3.0 and
[schema](https://github.com/bufbuild/protovalidate/releases) v1.2.2 (Apache-2.0).
Compiler, Go runtime and plugins have separate version lines. The example's grpc-go 1.72.2
predates the [HTTP/2 fragmentation fix](https://github.com/grpc/grpc-go/security/advisories/GHSA-vp52-pcj8-j9qc)
in 1.83.1 and is not a suitable platform pin. G19-01/05 require current advisory checks, a real
[Buf breaking baseline](https://buf.build/docs/breaking/usage/), reproducible generation and affected
Go/Java/browser/OpenAPI consumer tests. No RPC performance gain or wire parity is demonstrated here.

### Kafka and event processing

Retain Kafka, JSON topics `policy-registered`/`policy-terminated` and policy-number keys. The
[publisher](../../../legacy/policy-service/src/main/java/pl/altkom/asc/lab/micronaut/poc/policy/infrastructure/adapters/kafka/EventPublisher.java)
and [event DTO](../../../legacy/policy-service-api/src/main/java/pl/altkom/asc/lab/micronaut/poc/policy/service/api/v1/events/dto/PolicyDto.java)
define the code contract; exact date/decimal/null bytes require captured events. Policy registration
saves and publishes inside an annotated transaction, while termination lacks the same annotation.
Neither proves SQL/broker atomicity. Both constructors currently leave event agentLogin null;
do not infer correct dashboard attribution or silently change it. Protobuf RPC adoption does not
authorize changing Kafka bodies.

Provisionally prefer [IBM Sarama](https://github.com/IBM/sarama), using its current module identity,
for explicit partitioning, acknowledgements, producer idempotence, groups, commits and rebalance
controls. [kafka-go](https://github.com/segmentio/kafka-go) is a simpler conditional alternative,
but its README's tested broker range does not certify configured Kafka 4.1; direct Writer defaults
and async delivery need deliberate handling. Use FetchMessage plus commit after durable effects
rather than ReadMessage auto-commit for these consumers. Sarama's support window also moves: select
and test the exact client/broker/Go combination. Observed 30 September:
[Sarama v1.61.1](https://github.com/IBM/sarama/releases) and
[kafka-go v0.4.51](https://github.com/segmentio/kafka-go/releases), both MIT-licensed runtime clients.
Their configuration/operational complexity and integration-test cost are justified by recovery
requirements, not a performance benchmark. Pins/transitive advisories remain G19-01/07 work.

Document, payment, dashboard and search consume registration; search also consumes termination.
EARLIEST/clientId annotations do not establish effective groups, offsets or commit timing. Capture
them before handoff. A separate Go group repeats all side effects unless isolated; joining the Java
group splits partitions and requires equivalent handling. Commit only the contiguous completed
prefix, stop intake on drain and leave unfinished work replayable. Define bounded retries, poison
quarantine/replay, lag/error alerts and durable destination deduplication. Stable policy keys do not
prove Java/Go partition assignment, and registration/termination on separate topics have no global
order: search needs stale-state protection or reconciliation.

[Kafka delivery semantics](https://kafka.apache.org/41/design/design/) do not make SQL, Elasticsearch
or MinIO effects exactly once. A transactional outbox is conditional on required durable publication;
specify relay identity/order/recovery if chosen, or explicitly accept a weaker loss window and repair
path. Account-exists checks race; document upload/DB failures may be swallowed; repeated index IDs
can overwrite newer state. G19-07 tests broker/ISR failure, ambiguous send, crashes around each
external effect and commit, rebalance/drain, replay and actual Java/Go group handoff. Mesh retries
cannot replace this processing contract.

### Search and dashboard

Replace [deprecated olivere/elastic](https://github.com/olivere/elastic), whose supported major is
7, with provisional [official go-elasticsearch/v8](https://github.com/elastic/go-elasticsearch)
for configured Elasticsearch 8.19.6. The official client publishes Apache-2.0, versus the old
library's MIT license; its reviewed release list contains an 8.19 line. Choose a compatible v8 pin,
not v9 from a newer example. Use typed requests when they express the legacy DSL clearly, or its
low-level JSON API for awkward aggregates; keep both behind adapters. Raw net/http would add
unjustified auth/retry/version/error-handling ownership. No Go example search adapter exists.

[Policy search](../../../legacy/policy-search-service/src/main/java/pl/altkom/asc/lab/micronaut/poc/policy/search/infrastructure/adapters/db/ElasticPolicyViewRepository.java)
queries `policy-views` using query_string on number/policyHolder with size 100 and no ES sort.
The [assembler](../../../legacy/policy-search-service/src/main/java/pl/altkom/asc/lab/micronaut/poc/policy/search/queries/findpolicy/PolicyQueryResultAssembler.java)
sorts only those returned hits by dateFrom descending, nulls last. Sorting all matches in Elasticsearch
before limiting would change which 100 appear. Preserve bounded unary list and HTTP bridge first;
stable cursor/point-in-time pagination is a separate specified behavior if consumers need it.

Dashboard uses `policy_stats`, policy-number IDs and refresh=true. Its
[total sales](../../../legacy/dashboard-service/src/main/java/pl/altkom/asc/lab/micronaut/poc/dashboard/infrastructure/adapters/elastic/TotalSalesQueryAdapter.java),
[trends](../../../legacy/dashboard-service/src/main/java/pl/altkom/asc/lab/micronaut/poc/dashboard/infrastructure/adapters/elastic/SalesTrendsQueryAdapter.java)
and [agent sales](../../../legacy/dashboard-service/src/main/java/pl/altkom/asc/lab/micronaut/poc/dashboard/infrastructure/adapters/elastic/AgentSalesQueryAdapter.java)
adapters differ in rounding and timezone conversion. Terms sizes are unspecified;
[Elasticsearch defaults to ten buckets](https://www.elastic.co/docs/reference/aggregations/search-aggregations-bucket-terms-aggregation).
Total/trend amounts round to two places, agent amounts do not apply the same rounding; histogram
milliseconds become dates through JVM timezone. Capture >10 products/agents, >100 search hits,
date endpoints/DST, null agent/date and decimal fixtures. Correcting incomplete totals needs an
explicit behavior decision rather than being hidden in a client rewrite.

Export real mappings/settings/aliases and JSON samples; no checked-in index provisioning proves
them. Local HTTP and QA HTTPS differ; current Java factories disable certificate/hostname checks.
Mount/trust the correct ECK CA, verify DNS and scoped credentials, and distinguish 400/401/403/404,
409/429/5xx, TLS/cancellation and malformed data. Bound retries per operation and close response
bodies. The search application has a literal HTTPS scheme despite the local overlay's HTTP value;
verify effective configuration rather than assume interpolation. Dashboard's embedded ES 6.6.2
tests do not certify 8.19.6. G19-08 requires disposable target-version query/error/rebuild tests,
refresh visibility and stale-event protection; client adoption alone proves none of them.

### Realtime chat

Retain the browser WebSocket responsibility and provisionally qualify Gorilla. Observed
29 September, [gorilla/websocket](https://github.com/gorilla/websocket) publishes v1.5.3 under
BSD-2-Clause; its [API](https://pkg.go.dev/github.com/gorilla/websocket) supplies origin/read-limit
and one-reader/one-writer primitives. [coder/websocket](https://github.com/coder/websocket), ISC,
is a credible context-oriented alternative if a bounded adapter comparison shows benefit. The
standard library does not supply an equivalent complete WebSocket implementation. Either library
adds session/backpressure/lifecycle work beyond protocol handling; no load or security test ran.

The [legacy endpoint](../../../legacy/chat-service/src/main/java/pl/altkom/asc/lab/micronaut/poc/chat/service/infrastructure/adapters/web/ChatWebSocket.java)
uses `/ws/chat/{topic}/{username}`, case-insensitive topics, sender exclusion and HTML join/leave
messages. Incoming text is rebroadcast unchanged. The [browser helper](../../../legacy/web-vue/src/components/http/WebSocket.js)
does not send the stored JWT; [Chat.vue](../../../legacy/web-vue/src/components/Chat.vue) supplies
username/avatar from local storage and renders received HTML through v-html. Path names are not
identity. Preserve observed room behavior while specifying an approved safe text/JSON or proven
sanitized transitional format with the browser. Require server-authenticated principal/topic
rights and actual proxy Origin/Host tests; normal browser WebSocket construction cannot add a
bearer Authorization header. A session or short-lived audience-bound handshake credential needs
its own contract, expiry/revocation and no-secret-logging tests.

[Vue Nginx](../../../legacy/web-vue/nginx-app.conf) proxies `/ws/chat/` directly to chat, outside
the Java API gateway, with upgrade headers and 600-second timeouts. Reconcile heartbeat/idle limits
with that path. Bound frames, queues, connections and rate; one slow reader must not block a room.
Own socket close/drain explicitly because [http.Server.Shutdown](https://pkg.go.dev/net/http#Server.Shutdown)
does not wait for hijacked connections. Current broadcast is process-local with one replica;
session affinity does not distribute messages across Pods. Shared fanout/ordering and browser
reconnect/loss policy must precede scaling. No inspected DB/history or internal streaming caller
exists: defer persistence/retention and gRPC streams until specified. G19-11 tests real browsers,
negative access/HTML payloads, race/backpressure, crash/rolling drain and route rollback.

### Object storage

Qualify `minio-go/v7` for document/payment behind narrow storage ports. Observed 29 September,
[v7.3.0](https://github.com/minio/minio-go/releases/tag/v7.3.0) is Apache-2.0 and declares Go 1.25;
its [API](https://github.com/minio/minio-go/blob/v7.3.0/docs/API.md) covers context-aware
Put/Get/Stat/Copy/Remove, streams, metadata and custom TLS transport. This is a service dependency,
not a platform-wide package. AWS SDK for Go v2 S3 (Apache-2.0) is a conditional portability fallback
with additional endpoint/configuration surface; raw signing/net/http would duplicate complex
security/retry/multipart work without a demonstrated advantage.

Document [already uploads PDFs to MinIO](../../../legacy/documents-service/src/main/kotlin/pl/altkom/asc/lab/micronaut/poc/documents/domain/PolicyDocumentService.kt),
using `policies`, `yyyy/MM/<policy>.pdf` and application/pdf, then stores UTF-8 key bytes in the
same PostgreSQL bytea column as older inline PDFs. Retrieval uses a heuristic and the
[controller](../../../legacy/documents-service/src/main/kotlin/pl/altkom/asc/lab/micronaut/poc/documents/infrastructure/adapters/web/DocumentsController.kt)
can omit failed retrievals. Preserve Java-readable rows/keys and define an unambiguous future
representation before writes; do not run another blanket blob migration. SDK streaming does not
change the existing external result API. Bound bytes and inspect Get stream read errors.

Payment [imports MinIO CSVs](../../../legacy/payment-service/src/main/java/pl/altkom/asc/lab/micronaut/poc/payment/domain/InPaymentRegistrationService.java)
named `bankStatements_Y_M_D.csv`, applies matching-account rows, copies to `_processed_...` and
deletes the source. Positional columns and decimal/date formats, skipped unknown accounts and
balance order are contracts to capture. SQL/copy/delete are not atomic; competing replicas or a
commit failure can cause double credit or lose retry input. Choose durable claim/idempotency and
recovery before handoff. Missing keys must be distinguished from TLS/permission/timeout failures;
server-side CopyObject is an alternative, not an atomic rename guarantee.

Local tenants use HTTP, QA auto-cert HTTPS; Java certificate-bypass factories must not be copied.
The [document policy](../../../k8s/apps/svc/document/minio/s3-policy-policies.json) and
[payment policy](../../../k8s/apps/svc/payment/minio/s3-policy-payments-import.json) grant only
bucket-scoped object operations and listing/location, not storage administration. Verify CA/SAN,
path-style addressing, credential rotation and denied cross-bucket/admin calls. Encryption,
versioning, retention and backup ownership are unproven. ETags are not universally content MD5;
use a deliberate [integrity contract](https://docs.aws.amazon.com/AmazonS3/latest/userguide/checking-object-integrity-upload.html).

The [configured server image](../../../k8s/apps/infra/minio/base/tenant.yaml),
RELEASE.2025-09-07T16-13-09Z, falls within the
[unsigned-trailer authentication-bypass advisory](https://github.com/minio/minio/security/advisories/GHSA-hv4r-mvr4-25vw).
The [open-source server repository](https://github.com/minio/minio) is archived and unmaintained;
the advisory identifies an AIStor patch. This is configured-image evidence, not inspection of
running images or exploitation. G19-09 requires inventory, supported remediation/mitigation,
operator compatibility and server license review before Go storage ownership. SDK maintenance
does not patch the server, and SDK Apache-2.0 does not determine server licensing. Document/payment
partial failures, recovery, stream/integrity limits and Java coexistence require real-store tests.

### Document rendering

Defer chromedp/Chrome replacement until PDF and runtime evidence exists; permit an interim jsreport
HTTP adapter with support/capacity checks. The [generator](../../../legacy/documents-service/src/main/kotlin/pl/altkom/asc/lab/micronaut/poc/documents/infrastructure/adapters/jsreport/JsReportGenerator.kt)
POSTs the policy to `/api/report`, named template POLICY and timeout option 6000. The
[provisioner](../../../legacy/documents-service/src/main/kotlin/pl/altkom/asc/lab/micronaut/poc/documents/infrastructure/adapters/jsreport/JsReportTemplateProvisioner.kt)
creates a Handlebars/chrome-pdf template only if missing; it does not update existing content.
The six-line [checked-in template](../../../legacy/documents-service/src/main/resources/policy.template)
therefore cannot establish deployed helpers/assets/fonts/layout. Export effective state and safe
reference PDFs before changing the renderer. The [jsreport deployment](../../../k8s/apps/svc/jsreport/base/resources.yaml)
pins 4.10.1, one replica and a PVC; its separate browser memory is not part of the Java document
container limit. A Go rewrite does not eliminate Chrome processes or stored-template drift.

Observed 29 September: [chromedp v0.15.1](https://github.com/chromedp/chromedp/releases/tag/v0.15.1)
is MIT and requires Go 1.26. [PrintToPDF](https://chromedevtools.github.io/devtools-protocol/tot/Page/#method-printToPDF)
can render prepared HTML; Go html/template is a trusted-template escaping option, not a PDF engine
or Handlebars-equivalent formatting guarantee. Direct CDP/Chrome CLI is a fallback only if a
specific chromedp limitation warrants owning more protocol/process code. [jsreport chrome-pdf](https://jsreport.net/learn/chrome-pdf)
already uses Chrome and supports print/font configuration; keeping it narrows simultaneous changes
but retains Node/Chrome/PVC and license costs. Reviewed upstream had a 4.14.x line; the
[licensing page](https://jsreport.net/buy) limits a free instance to five templates. Actual license,
template count, image/browser support and capacity are unverified; no upgrade is selected here.

Pin browser/OS/fonts with an update/SBOM owner; chromedp does not bundle or patch Chrome. Its
[allocator](https://github.com/chromedp/chromedp/blob/v0.15.1/allocate.go) adds no-sandbox when root,
so default flags are not a security design. Require a non-root working sandbox, inaccessible
DevTools, restricted network/file resource access, isolated profiles and no unnecessary DB/MinIO
credentials in Chrome. Bound renderer queue/pages, CPU/memory/PIDs, deadlines/output and crash
recovery. Choose Pod-local versus separate renderer only with isolation/load evidence. Compare
extracted text, dates, pagination and visual tolerances, not solely PDF byte equality. G19-10 also
tests duplicate Kafka events and MinIO/DB failures end to end; retaining jsreport does not waive
the storage or event gates and does not make document an inherently low-risk pilot.

### Pricing

Defer engine selection. Retain authoritative Java pricing and unchanged MVEL rows during coexistence.
[Tariff](../../../legacy/pricing-service/src/main/java/pl/altkom/asc/lab/micronaut/poc/pricing/domain/Tariff.java)
has ordered-in-evaluation base/markup collections without explicit persisted order columns; stable
loaded order is not proven. Base rules choose the first match per cover, markups run sequentially.
The proposal's version/effective-date fields and cache policy are new design choices. The handler
retrieves a tariff per request. [Seed tariffs](../../../legacy/pricing-service/src/main/java/pl/altkom/asc/lab/micronaut/poc/pricing/init/DemoTariffsFactory.java)
cover only a small grammar of B decimal literals, arithmetic, comparisons, strings and ternaries;
deployed rows may contain more. CAR's stored factor is **50**, not 50 percent; migration cannot
silently correct it. Capture actual rules, authoring/update history and loaded ordering first.

[Calculation](../../../legacy/pricing-service/src/main/java/pl/altkom/asc/lab/micronaut/poc/pricing/domain/Calculation.java)
binds LocalDate values, mutable cover objects and answers, with possible binding-name collisions.
[PercentMarkupRule](../../../legacy/pricing-service/src/main/java/pl/altkom/asc/lab/micronaut/poc/pricing/domain/PercentMarkupRule.java)
rounds every factor application to two decimals HALF_UP; totals add resulting prices. Base results
are not universally scaled there. MVEL 2.4.0.Final is the legacy dependency. Null base matches,
duplicate answers, missing/wrong types and arbitrary deployed expressions need characterized errors,
not invented clean semantics. Policy's authoritative offer price and exact date/money behavior
must survive the caller transition even while pricing remains Java.

| Candidate, observed 29 September 2026 | Fit and unresolved cost |
| --- | --- |
| [Expr v1.17.8](https://github.com/expr-lang/expr/releases/tag/v1.17.8), MIT, Go 1.18 module | Arithmetic/conditionals and custom hooks fit seed syntax, but native numeric behavior does not prove BigDecimal semantics. Decimal literals/operators/coercion, allowed built-ins and source/node/input/time bounds need a spike. |
| [CEL-Go v0.32.0](https://github.com/cel-expr/cel-go/releases/tag/v0.32.0), Apache-2.0, Go 1.23 module, `cel.dev/cel-go` | Typed language and cost controls are useful, but native int/uint/double are not decimal. Custom types/functions add integration/dependency cost; ordinary CEL arithmetic is insufficient. |
| Restricted decimal evaluator with an exact decimal type | Credible if effective grammar is small; precise decimal-text parsing and bounded AST can avoid unneeded language features. Own parser/security/testing cost grows with real grammar. [shopspring/decimal](https://pkg.go.dev/github.com/shopspring/decimal) is a candidate, not a chosen pin; compare its negative ties/division/scale with Java. |

Neither [Expr hooks](https://expr-lang.org/docs/language-definition) nor
[CEL's language](https://github.com/cel-expr/cel-spec/blob/master/doc/langdef.md) establishes parity.
Translate parsed syntax, not text substitutions; reject unsupported constructs before publication.
Forbid clock/I/O/reflection unless an explicitly approved bounded rule requires it. Limit source
length, nesting, digits/exponents, input cardinality and host-function work as well as evaluation
cost. Compile/check before publishing an atomic ordered rule set; bind one immutable set per request.
Cache by rule/evaluator identity, with explicit invalidation and rollback rather than TTL alone.
Store translated forms separately from Java MVEL, and shadow without serving prices or mutating
offers. G19-12 compares per-cover intermediate decimal value **and scale**, negative ties, repeated
rounding, condition edges, factor 50, errors, concurrent edits and rollback against the real Java
oracle. Native evaluator availability is not pricing parity or a performance result.

### Observability

Retain the canonical boundary: slog JSON to deployed stdout/stderr and OTel signals through Alloy,
without direct service clients for Loki/Prometheus/Tempo. The actual input to the QA Helm target is
[Alloy values.yaml](../../../k8s/overlays/qa/infra/alloy/values.yaml), chart 1.8.1, not the adjacent
standalone config.alloy. It declares Zipkin 9411 and OTLP 4317/4318 and dual trace exports to Tempo
and a Zipkin bridge. It also configures OTLP logs→Loki and OTLP metrics→Prometheus remote-write.
These are configured paths, not successful delivery.

There is no Pod stdout/file source in these active values, so JSON slog does not reach the OTLP-log
receiver automatically. [Prometheus values](../../../k8s/overlays/qa/infra/prometheus/values.yaml)
do not enable its remote-write receiver, which is
[disabled by default](https://prometheus.io/docs/prometheus/latest/command-line/prometheus/#flags).
The [Operator API](https://prometheus-operator.dev/docs/api-reference/api/#monitoring.coreos.com/v1.PrometheusSpec)
also cautions against using it as a general scrape replacement. No Go application monitor or Alloy
scrape path is evidenced. Choose/test one stdout-to-Alloy collection path or a justified OTLP log
bridge, and a supported metrics ingestion route. Do not add duplicate exports or assume an accepted
OTLP request proves a queryable backend sample. Local-dev needs explicit disabled/local diagnostics
or a separately provisioned collector; do not silently send developer traffic to QA.

[OpenTelemetry Go status](https://opentelemetry.io/docs/languages/go/), reviewed 29 September, labels
traces/metrics stable and logs release candidate. Core/contrib are Apache-2.0 runtime dependencies;
slog is standard library. Pin mutually compatible SDK/exporter/contrib modules under G19-01.
[otelhttp](https://pkg.go.dev/go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp) and
[otelgrpc](https://pkg.go.dev/go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc)
are candidates; pgx may use adapter-owned spans or a separately reviewed otelpgx version. Kafka
context belongs in compatible headers, preserving JSON bodies and commit semantics. These options
add queue, allocation, sampling, exporter and integration-test cost; none establishes measured delivery.

Set stable service/version/environment resource identity and safe trace/span fields in logs;
slog does not inject them automatically. Verify Java Zipkin/B3 and Go W3C parent continuity.
Allowlist low-cardinality labels and exclude tokens, customer/policy/payment/chat payloads, raw SQL,
object keys and identifiers from unrestricted diagnostics. Trace IDs are log fields, not Loki stream
or metric labels. Explicitly configure sampling; do not claim tail sampling from the current values.
Bound exporter buffering/timeouts, report drops and flush with a fresh shutdown context. Readiness
and liveness must not cause collector-outage restart loops. Grafana data sources/default dashboards
are not service SLOs. G19-13 requires queryable synthetic traces/logs/metrics, negative redaction,
outage/capacity and alert evidence before traffic shifts; G19-15 defers Zipkin/archive retirement
until consumers, retention and restore are proven. Go continues targeting Alloy after retirement.

### Security and Phase 5

Qualify the security objective and defer exact verifier/Keycloak/Envoy/mesh adoption. The
[auth provider](../../../legacy/auth-service/src/main/java/pl/altkom/asc/lab/micronaut/poc/auth/AuthProvider.java),
[agent details](../../../legacy/auth-service/src/main/java/pl/altkom/asc/lab/micronaut/poc/auth/InsuranceAgentDetails.java)
and [claims generator](../../../legacy/auth-service/src/main/java/pl/altkom/asc/lab/micronaut/poc/auth/InsuranceAgentJWTClaimsSetGenerator.java)
show agent login, product-code roles and avatar behavior. Auth/gateway YAML share a checked-in
symmetric signing secret; do not reproduce it. Its rotation and actual issuer/audience/algorithm,
claim enforcement and overlap policy must precede sharing trust with Go. Source annotations do not
prove effective permissions: most gateway controllers require authentication, while the
[payment route](../../../legacy/agent-portal-gateway/src/main/java/pl/altkom/asc/lab/micronaut/poc/gateway/PaymentGatewayController.java)
lacks the same annotation, and inspected backend controllers lack equivalent explicit guards.

Each receiving Go boundary needs authenticated user/workload identity and method/resource
authorization. Preserve the gateway's principal override, never a user-controlled login/header.
Kubernetes DNS/internal Services and network restrictions are not application authorization.
Specify trusted credential propagation and direct-backend bypass tests for the interim Java gateway;
do not wait for Phase 5 to secure an exposed route. Enumerate policy ownership, documents, payment
accounts, product entitlements and background jobs. Capture legacy allowances, then explicitly
agree any tightening instead of inventing existing restrictions or copying an unsafe permission.

[JWT BCP](https://datatracker.ietf.org/doc/html/rfc8725) and the
[access-token profile](https://datatracker.ietf.org/doc/html/rfc9068) require deliberate token type,
algorithm, issuer/key and audience policy. For a future Keycloak resource server, verify signature,
key type, exp/nbf/skew and required subject/client identity; resolve kid only through trusted bounded
JWKS discovery/cache. Never use unverified parsing or an ID token as an API access token. Observed
30 September candidates remain deferred: [coreos/go-oidc/v3](https://github.com/coreos/go-oidc/releases)
v3.20.0 (Apache-2.0), [golang-jwt/jwt/v5](https://pkg.go.dev/github.com/golang-jwt/jwt/v5)
(MIT, a separate key-fetch/cache policy needed), and [jwx/v4](https://github.com/lestrrat-go/jwx)
v4.2.0 (MIT, Go 1.26 constraint). Verify exact access-token APIs and pinned compatibility with real
issuer fixtures; standard crypto/JSON primitives do not justify hand-rolling JOSE. Introspection
is a future revocation option with latency/availability and client-credential cost.

[Keycloak](https://www.keycloak.org/docs/latest/server_admin/) needs realm/client/audience/role,
agent entitlement and login/MFA migration plus Java/Go overlap. [Envoy JWT](https://www.envoyproxy.io/docs/envoy/latest/configuration/http/http_filters/jwt_authn_filter.html)
needs explicit per-route audiences/providers and does not decide domain permissions. A Phase 5
translator must prove custom gateway defaults, binary responses, WebSocket and rollback.
[Linkerd mTLS](https://linkerd.io/docs/features/automatic-mtls/) and
[authorization policy](https://linkerd.io/docs/features/server-policy/) require mixed-peer and probe
tests; mesh encryption is not human authorization or universal retry/circuit-breaker equivalence.
None of these workloads/policies is established by current application overlays. Service deadlines,
retry safety and event recovery remain required without them.

Restrict reflection/profiling/metrics, preserve minimal reachable health checks and reject unsafe
debug payload logging. Name Secret/CA/issuer/collector access owners and prove rotation/restart.
Chat's separate handshake and safe browser rendering need coordinated changes. G19-06 tests
negative signatures/algorithms/issuer/audience/expiry/permissions, key refresh/outage and direct
access before exposure; Phase 5 and weighted shifts additionally require G19-14. These are
specified future behavior/security changes, not changes authorized or implemented by this review.

### Development and validation tooling

Retain [canonical Make-only development](../../../ai/rules/go-development.md),
[formatting](../../../ai/rules/go-formatting.md) and [validation](../../../ai/rules/go-validation.md).
Insurance Hub has no Go module/workspace/service at the inspected revision. The root
[Makefile](../../../Makefile) declares Go 1.24 and a go-build placeholder; the
[CONTRIBUTING minimum](../../../CONTRIBUTING.md#prerequisites) is not an adopted compatible service toolchain.
Choose module paths, contract ownership, supported Go/compiler/runtime/tool versions, generated
directories and CI/container consistency before implementation. One module per service is a
possible starting point, not a selected topology; go.work does not make root tests cover every module.

| Capability | Required ownership and evidence |
| --- | --- |
| Format/lint | Pinned gofumpt, goimports and golines with 100-column target in an explicit stable order; changed handwritten scope, generated/fixture exclusions, non-mutating format-check including new files, idempotence and vet-equivalent lint. |
| Unit/race/build | All affected packages, cmd executables and contract consumers, plus nested modules where present. Deterministic fixtures and synchronization; race detector covers exercised paths, not unexecuted concurrency. |
| Integration | Tagged separation and disposable real PostgreSQL/migrations, plus required Kafka/search/object-store/browser endpoints. Mocked repositories behind a real listener do not prove wire-to-store behavior. |
| Generation | Pinned Buf/compiler/plugins/schema/runtime and optional sqlc/mock generators; format/lint/generate, added/modified/deleted drift detection, real published breaking baseline and affected consumer tests. No hand-editing generated code. |
| Dependency/security/release | Scoped dependency maintenance only when needed; manifests/checksums, module/tool advisories, reachability-aware vulncheck, image/SBOM policy and controlled migration executor. CI calls the same owning Make targets. |

[Testcontainers](https://golang.testcontainers.org/system_requirements/docker/) adds a Docker API
runtime, image pulls and CI cost; real migration/constraint tests justify it where needed.
Testify/Mockery are optional conveniences around consumer-owned ports; standard testing and small
fakes remain valid. sqlmock cannot prove SQL/locking behavior, and random faker data cannot be the
sole parity oracle. Runtime impact is inapplicable to standalone formatters/generators; their risks
are build supply chain, drift and generated output. Test-only packages should not become production
dependencies by accident. sqlc, Buf and plugins are build tools; Goose belongs to a separately
controlled release operation, not each application startup.

Observed 30 September: [golangci-lint](https://github.com/golangci/golangci-lint/blob/main/CHANGELOG.md)
v2.14.0 versus example v2.11.4, [Testcontainers](https://github.com/testcontainers/testcontainers-go/releases)
v0.44.0 versus example v0.37.0; [Mockery v3](https://vektra.github.io/mockery/latest/v3/) is maintained
and publishes BSD-3-Clause. Record exact licenses/notices, compatible versions and advisories for
all selected tools at pin time; unselected optional tools need no platform pin.
[govulncheck](https://go.dev/doc/tutorial/govulncheck) supplies reachability evidence, not an exhaustive
security certification. No Go development tool was run in this review. Missing future Make capabilities are
blocked checks, not permission for direct-tool fallback. Predictable validation must not silently
install tools, tidy, migrate shared databases or deploy. G19-01 and DEV-18 scenarios establish this
interface; G19-14 adds internal-endpoint parity/e2e/performance and rollback evidence before cutover.

## Preliminary research assessment

| Study | Overall disposition | Accepted direction | Required corrections and unresolved adoption evidence |
| --- | --- | --- | --- |
| [PostgreSQL](postgresql-data-access-strategy.md) | qualify | SQL-first adapters, provisional pgx/v5/pgxpool plus sqlc, explicit transactions and controlled versioned SQL | Correct seven-service scope and illustrative product schema; include auth schema hazard. No startup AutoMigrate, no presumed outbox atomicity, no automatic JSONB speedup. Actual schema/types, Java auto-DDL/seeders, Goose packaging and rollback need G19-04/07. |
| [Dependency injection](dependency-injection-strategy.md) | qualify | Manual constructors and narrow ports; Fx only after measured pain | Apply thin cmd/internal/service boundary, partial-startup cleanup and fresh bounded shutdown contexts. Do not copy unbounded cleanup or mandate a framework/pilot order. G19-01/03; the Fx investigation is conditional. |
| [Configuration](configuration-strategy.md) | qualify | Typed startup settings, provisional caarlos0/env, semantic validation and constructor injection | No blanket RequiredIfNoDef, raw wrapped secrets, mandatory local Alloy or presumed SOPS/hash-based Secret rollout. Map actual keys and optional integrations per service; require G19-02/06. |

All three are **accepted only with the stated revisions/conditions**, not endorsed wholesale.
Their example code and executable pilots are not deployment instructions. D19-03/04/10 record
the specific replacements of Viper/Wire/GORM; qualifying a study does not undo those replacements.

## Go example comparison

The inspected example revision is
[`24594b4290365704c5d91244c48e2ba1238c6b43`](https://github.com/igor-baiborodine/campsite-booking-go/tree/24594b4290365704c5d91244c48e2ba1238c6b43),
with no local modifications. It supplies architectural evidence, not Insurance Hub dependencies,
versions, operational defaults or a passing migration test. The following links pin concrete files
at that revision; references to line ranges describe the inspected implementation.

| Area and original proposal | Example implementation evidence | Reuse, adapt or avoid in the reviewed choice |
| --- | --- | --- |
| Service structure / Wire | [cmd/main.go](https://github.com/igor-baiborodine/campsite-booking-go/blob/24594b4290365704c5d91244c48e2ba1238c6b43/cmd/main.go#L21) and [service.go](https://github.com/igor-baiborodine/campsite-booking-go/blob/24594b4290365704c5d91244c48e2ba1238c6b43/internal/service/service.go#L35) manually construct the graph. [Domain repository](https://github.com/igor-baiborodine/campsite-booking-go/blob/24594b4290365704c5d91244c48e2ba1238c6b43/internal/domain/booking_repository.go) is separate from adapters. | **Reuse** explicit constructors and inward dependencies. **Adapt** generic handlers/decorators only where useful; no DI container is required. D19-01/04/05/45/46. |
| GORM persistence proposal | [Repository](https://github.com/igor-baiborodine/campsite-booking-go/blob/24594b4290365704c5d91244c48e2ba1238c6b43/internal/postgres/booking_repository.go#L33) uses database/sql with pgx/v4 stdlib, handwritten scans and [SQL constants](https://github.com/igor-baiborodine/campsite-booking-go/blob/24594b4290365704c5d91244c48e2ba1238c6b43/internal/postgres/sql/queries.go). Transactions include isolation/locking/version checks. | **Reuse** explicit repository/transaction ownership. **Adapt** to provisional pgx/v5/sqlc based on actual Insurance Hub queries/types; do not copy booking isolation or cancellation-insensitive retry sleeps. D19-10/41/42/44. |
| Unspecified migration executor | [Embedded migrations](https://github.com/igor-baiborodine/campsite-booking-go/blob/24594b4290365704c5d91244c48e2ba1238c6b43/db/migrations/migrations.go) and [versioned SQL](https://github.com/igor-baiborodine/campsite-booking-go/blob/24594b4290365704c5d91244c48e2ba1238c6b43/db/migrations/003_alter_bookings_add_version.sql); service.go calls Goose Up at startup. | **Reuse** versioned SQL files; **avoid** replica-startup migration in production. Goose remains a separately pinned release candidate with schema ownership/recovery tests. D19-43. |
| Viper / environment configuration | [Config](https://github.com/igor-baiborodine/campsite-booking-go/blob/24594b4290365704c5d91244c48e2ba1238c6b43/internal/config/config.go) uses envconfig/dotenv, local defaults and unresolved placeholder substitution. [Tests](https://github.com/igor-baiborodine/campsite-booking-go/blob/24594b4290365704c5d91244c48e2ba1238c6b43/internal/config/config_test.go) emphasize happy paths and mutate process env. | **Reuse** typed boundary; **adapt** to service fields, injected test environments, missing/empty/semantic/redaction cases. **Avoid** implicit production dotenv and template credentials. D19-03/47–50. |
| Protobuf / PGV | [api.proto](https://github.com/igor-baiborodine/campsite-booking-go/blob/24594b4290365704c5d91244c48e2ba1238c6b43/campgroundspb/v1/api.proto) uses buf.validate; [server.go](https://github.com/igor-baiborodine/campsite-booking-go/blob/24594b4290365704c5d91244c48e2ba1238c6b43/internal/grpc/server.go#L29) installs unary validation and maps DTOs. [Business validators](https://github.com/igor-baiborodine/campsite-booking-go/blob/24594b4290365704c5d91244c48e2ba1238c6b43/internal/application/validator/booking_validators.go) are separate. | **Reuse** versioned contracts and layered validation; **adapt** presence, legacy HTTP and safe wrapped-error mapping. Direct handler tests bypass interceptors. Do not copy booking constraints into Insurance Hub. D19-06–09. |
| grpc-gateway / future Envoy | [Envoy config](https://github.com/igor-baiborodine/campsite-booking-go/blob/24594b4290365704c5d91244c48e2ba1238c6b43/k8s/envoy-config.yaml) proxies gRPC over HTTP/2; gateway is only an indirect module dependency. | **Avoid** treating this as HTTP/JSON transcoding, gateway auth or binary-response parity evidence. No gateway/OpenAPI output is generated by the example. D19-08/22. |
| Lifecycle / health | [Waiter](https://github.com/igor-baiborodine/campsite-booking-go/blob/24594b4290365704c5d91244c48e2ba1238c6b43/internal/waiter/waiter.go) and service.go own signals and bounded gRPC stop, but partial construction can leave DB open and pprof Shutdown uses cancelled context. [Deployment](https://github.com/igor-baiborodine/campsite-booking-go/blob/24594b4290365704c5d91244c48e2ba1238c6b43/k8s/campgrounds.yaml) lacks readiness/liveness probes. | **Reuse** bounded-drain intent; **adapt** reverse cleanup, fresh context, serving transitions, complete resource close and probe tests. D19-02/29/46. |
| Logging / observability / security | [Logger](https://github.com/igor-baiborodine/campsite-booking-go/blob/24594b4290365704c5d91244c48e2ba1238c6b43/internal/logger/logger.go) chooses JSON only for production. [Decorators](https://github.com/igor-baiborodine/campsite-booking-go/blob/24594b4290365704c5d91244c48e2ba1238c6b43/internal/application/decorator/logging.go) log whole inputs/results; gRPC debug logs payloads, reflection is unconditional, optional pprof binds :6060. No wired OTLP providers/exporters are shown. | **Reuse** slog; **adapt** deployed QA JSON, safe correlation and collector delivery. **Avoid** payload logs and ungoverned profiling/reflection. Indirect OTel entries are not implemented telemetry. D19-25–30/40/52. |
| Unit versus integration testing | [Makefile](https://github.com/igor-baiborodine/campsite-booking-go/blob/24594b4290365704c5d91244c48e2ba1238c6b43/Makefile#L109) race-tests internal/...; tagged integration lacks race. [PostgreSQL integration](https://github.com/igor-baiborodine/campsite-booking-go/blob/24594b4290365704c5d91244c48e2ba1238c6b43/internal/postgres/booking_repository_integration_test.go) applies real migrations; [gRPC integration](https://github.com/igor-baiborodine/campsite-booking-go/blob/24594b4290365704c5d91244c48e2ba1238c6b43/internal/grpc/server_integration_test.go) uses a real listener with mocked repositories. | **Reuse** layered tests/disposable DB; **adapt** full package/consumer/concurrency coverage and deterministic ports/fixtures. Neither mocks nor example tests certify Java parity. D19-44/52/54. |
| Contract/mock generation | [buf.yaml](https://github.com/igor-baiborodine/campsite-booking-go/blob/24594b4290365704c5d91244c48e2ba1238c6b43/buf.yaml), [buf.lock](https://github.com/igor-baiborodine/campsite-booking-go/blob/24594b4290365704c5d91244c48e2ba1238c6b43/buf.lock), [buf.gen.yaml](https://github.com/igor-baiborodine/campsite-booking-go/blob/24594b4290365704c5d91244c48e2ba1238c6b43/buf.gen.yaml) and [.mockery.yml](https://github.com/igor-baiborodine/campsite-booking-go/blob/24594b4290365704c5d91244c48e2ba1238c6b43/.mockery.yml) pin/configure generation; Make/CI checks tracked diffs, with no invoked Buf breaking comparison. | **Reuse** explicit generated artifacts; **adapt** published baseline and added/deleted/untracked drift checks. Configured breaking: FILE is not an executed check. D19-07/09/52–54. |
| Formatting / Make / CI | Make installs goimports but format runs golines then gofumpt on '.', with no explicit 100-column flag; [.golangci.yml](https://github.com/igor-baiborodine/campsite-booking-go/blob/24594b4290365704c5d91244c48e2ba1238c6b43/.golangci.yml) names gofmt. [CI](https://github.com/igor-baiborodine/campsite-booking-go/blob/24594b4290365704c5d91244c48e2ba1238c6b43/.github/workflows/ci.yml) invokes lint via an action; aggregate check mutates dependencies/formatting. | **Adapt** canonical formatter coverage, Make-only entry points and predictable checks. **Avoid** cleanliness checks as format proof and the mutating aggregate as default validation. D19-53/54. |
| Modules/build/security | [Main go.mod](https://github.com/igor-baiborodine/campsite-booking-go/blob/24594b4290365704c5d91244c48e2ba1238c6b43/go.mod) declares Go 1.26.5; [datagenerator module](https://github.com/igor-baiborodine/campsite-booking-go/blob/24594b4290365704c5d91244c48e2ba1238c6b43/datagenerator/go.mod) declares 1.22.4. Root internal/... misses cmd and nested module; CI container build does not supply Make build/vulncheck targets. | **Adapt** service-owned toolchain/module/build/security interface. **Avoid** copying either Go pin or treating CI success as Insurance Hub coverage. D19-01/32/54. |

The example has no implemented Kafka, Elasticsearch, WebSocket, MinIO, PDF or pricing adapter in
the inspected service packages. Its representative CreateBooking path proves an architectural
separation—proto/interceptor → DTO mapping → application/business validators → repository/SQL →
response/error mapping—but no insurance contract or integration behavior. Generated output records
protoc-gen-go 1.36.11 and protoc-gen-go-grpc 1.6.2; its Protovalidate runtime 0.12.0 and grpc-go
1.72.2 are example pins only. Update compatibility/advisory evidence in the owning service ticket.

## Deployment comparison

Use checked-in Insurance Hub chains as deployment evidence. Representative document
[base](../../../k8s/apps/svc/document/base/legacy/kustomization.yaml),
[Deployment](../../../k8s/apps/svc/document/base/legacy/deployment.yaml),
[Service](../../../k8s/apps/svc/document/base/legacy/service.yaml),
[local overlay](../../../k8s/overlays/local-dev/svc/document/legacy/kustomization.yaml) and
[QA overlay](../../../k8s/overlays/qa/svc/document/legacy/kustomization.yaml) show the naming,
configuration and environment patterns. No overlay/Helm rendering or running cluster was inspected
in this documentation review.

| Concern | Implemented source pattern | Carry forward or adapt for Go |
| --- | --- | --- |
| Names/labels/selectors | Bases use app.kubernetes.io/name/component plus part-of/managed-by; overlays add local-dev-/qa- prefixes and environment labels. Local services share local-dev-all; QA apps use qa-svc. Services are ClusterIP. | Retain conventions and environment-qualified DNS; use separate Java/Go identity/selectors and explicit release routing. Do not let a legacy selector select both implementations by accident. |
| Ports/probes/resources | Representative API Service 80→8080, one replica, /health liveness/readiness; overlays set CPU/memory requests/limits. QA patches use versioned registry images, unlike base :latest. Web Vue targets 80 and lacks those API probes. | Define named HTTP/gRPC ports and real probe semantics, immutable chosen images, Go resources and bounded grace from tests. Java sizing and shared /health do not establish Go health or browser resource budgets. |
| ConfigMaps | Nine of eleven legacy app bases generate ConfigMaps and use envFrom; chat/gateway are exceptions. No relevant hash disabling is configured. Overlays merge non-secret literals. | Retain generation/composition where applicable; render and prove changed Pod references. Derive each service schema rather than cloning document's integrations. |
| Secrets and restart | [QA document patch](../../../k8s/overlays/qa/svc/document/legacy/deployment-patch.yaml) and [local patch](../../../k8s/overlays/local-dev/svc/document/legacy/deployment-patch.yaml) use fixed secretKeyRef names. PostgreSQL/MinIO credentials and ECK elastic-user data are created/copied by [k8s Make](../../../k8s/Makefile), including qa-data→qa-svc copies. | Name each creator/copy/rotation owner and verify restart/overlap. ConfigMap hashing does not establish Secret-content rollout; no configured SOPS/ExternalSecret workflow or automatic rotation is inferred. |
| Reconciliation/ownership | [QA Flux Kustomization](../../../k8s/flux/qa/flux-system/kustomization-qa-svc.yaml) points to k8s/overlays/qa/svc with prune/wait; [source](../../../k8s/flux/qa/flux-system/gitrepository.yaml) tracks main. Infrastructure/monitoring provisioning is largely Make/Helm/kubectl. | Retain actual ownership boundaries. Place migration executor and route changes deliberately in that workflow; tested rollback must survive reconciliation. Flux presence is not a blue/green or weighted-shift controller. |
| Environment bootstrap | [Bootstrap Makefile](../../../k8s/bootstrap/Makefile) creates Kind local-dev and LXD/K3s QA; [QA script](../../../k8s/bootstrap/qa/scripts/qa-cluster-create.sh) does not pin K3s server release. | Verify effective versions, capacity, networking and storage for the owning rollout; static bootstrap does not prove phase completion or a current cluster baseline. |
| Edge/auth/mesh | Inspected application bases/overlays contain Java auth/gateway; no Envoy, Keycloak or Linkerd workload/mesh policy is established. Chat routes through Vue Nginx independently. | Keep tested interim HTTP/identity contracts and own all route classes. Phase 5 and security prerequisites are explicit; no weighted shift or mesh trust may be presumed. |
| Stateful side effects | DBs, Kafka, Elasticsearch, MinIO and jsreport have separate ownership; the application may also seed, consume or schedule at startup. | Define one schema/job/effect owner or isolate shadow stores. No incoming HTTP traffic is not sufficient isolation. Replica/HPA changes must account for rooms, import claims, consumers and pool/browser limits. |

### Configured infrastructure compatibility

| Dependency | Local-dev source baseline | QA source baseline | Required Go adaptation |
| --- | --- | --- | --- |
| PostgreSQL | [CNPG base](../../../k8s/apps/infra/postgres/base/cluster.yaml) uses PostgreSQL major 17, one instance. | [QA patch](../../../k8s/overlays/qa/infra/postgres/base/cluster-patch.yaml) sets two instances and monitoring; per-service clusters use -rw DNS. | Separate auth/document/payment/policy/pricing/product ownership, TLS/credentials and bounded aggregate pools. Actual minor/schema/history remain unknown; no dashboard/chat DB inferred. |
| Kafka | [Resources](../../../k8s/overlays/local-dev/infra/kafka/resources.yaml): 4.1.0, one ephemeral combined broker/controller, replication/min ISR 1, internal plaintext 9092 and TLS 9093, external localhost listener. | [Resources](../../../k8s/overlays/qa/infra/kafka/resources.yaml): 4.1.0, three persistent brokers and three controllers, replication 3/min ISR 2; same internal listener types. | Existing app configs use 9092. Listener TLS availability is not client authentication or a selected TLS policy. Test broker API/partition/ack/rebalance and any new CA/auth path in both topologies. |
| Elasticsearch | [ECK resource](../../../k8s/overlays/local-dev/infra/elastic/elasticsearch.yaml): 8.19.6, one node, HTTP TLS disabled. | [ECK resource](../../../k8s/overlays/qa/infra/elastic/elasticsearch.yaml): 8.19.6, two nodes, HTTPS default; app patch copies password but does not demonstrate CA mounting. | Official v8 client with explicit scheme, CA/SAN trust and scoped credentials. Capture index mappings; no insecure Java TLS bypass in Go. |
| MinIO | [Document patch](../../../k8s/overlays/local-dev/infra/minio/document/tenant-patch.yaml): one server/volume, HTTP; separate payment tenant. | [Document patch](../../../k8s/overlays/qa/infra/minio/document/tenant-patch.yaml): three servers, two volumes/server, auto-cert HTTPS; analogous payment tenant. | Base server RELEASE.2025-09-07T16-13-09Z and operator ref v7.1.1 need actual support/advisory/compatibility resolution. Keep buckets/keys, verify TLS and recovery; no encryption/versioning claim from target prose. |
| Alloy/monitoring | No configured local Alloy in inspected overlays; typical services disable Zipkin. | [Active values](../../../k8s/overlays/qa/infra/alloy/values.yaml): chart 1.8.1, single replica, OTLP/Zipkin and dual trace export; logs/metrics have the gaps above. | Explicit local mode; pin compatible SDK/exporter and prove QA end-to-end delivery, identity, safe labels, sampling, buffering/capacity and shutdown. |
| jsreport | [Base](../../../k8s/apps/svc/jsreport/base/resources.yaml): image 4.10.1, one replica and /app/data PVC; [local PVC](../../../k8s/overlays/local-dev/svc/jsreport/pvc-patch.yaml) 1 GiB. | [QA PVC](../../../k8s/overlays/qa/svc/jsreport/pvc-patch.yaml) 2 GiB; explicit service endpoint. | Interim HTTP dependency possible; inspect stored template drift/browser/license/capacity. Replacement needs its own browser image, resources and G19-10 evidence. |

Operator/tool pins observed in [k8s Make](../../../k8s/Makefile) include CNPG 1.27.0,
Strimzi chart 0.48.0 and ECK 3.2.0. They describe configured installation inputs, not a support
certification or live versions. Application buckets/users/policies are imperative setup; a script
that tolerates bucket-create failure is not proof of readiness. Source monitoring runbooks such as
[Alloy trace verification](../../../k8s/tests/infra/verify-alloy-traces/verify-alloy-traces.md) and
[Loki verification](../../../k8s/tests/infra/verify-loki-logs/verify-loki-logs.md) are useful future
procedures, but prior screenshots or direct backend pushes do not certify future Go app delivery.

## Service consequences

All eight Go services inherit G19-01–03, G19-05–06, G19-13–14 as applicable to their actual endpoints.
Table gates are additional domain/integration work. `document-service` is the analysis/deployment
name; its physical legacy module is `legacy/documents-service` (and `documents-service-api`).

| Service | Actual boundary and reviewed implementation | Compatibility and cutover evidence |
| --- | --- | --- |
| document-service | Kafka registration → renderer → MinIO PDF → PostgreSQL reference; HTTP query reads mixed inline/key rows. Provisional pgx/sqlc, Sarama and MinIO SDK; interim jsreport allowed, chromedp deferred. | Preserve effective template/bytes/date rendering, object keys and Java-readable rows. Resolve server support/TLS, swallowed failures, duplicate rows/orphans and consumer ownership before effects. G19-04/07/09/10. |
| product-service | PostgreSQL JSONB catalog, two read API operations, startup seeding. Provisional pgx/sqlc behind application ports; unary gRPC plus legacy HTTP mapping. | Capture polymorphic product/cover/question JSON, decimal/null/unknown fields, empty/not-found behavior and stored rows; separate seeding/DDL from read pilot. No MongoDB retirement inferred. G19-04 and product gate under G19-14. |
| dashboard-service | Kafka registration → Elasticsearch policy_stats → total/trend/agent POST queries. Official v8 client; no evidenced PostgreSQL settings store. | Preserve filters, refresh=true visibility, default terms bucket limits, each adapter's rounding and timezone conversion. Do not silently fix truncated totals or null agent attribution. Isolate shadow index. G19-07/08. |
| policy-search-service | Registration and termination topics → policy-views → bounded search list; internal API has an external Java gateway caller. Official v8 client, unary RPC and HTTP bridge. | Preserve query_string fields/default q, cap before sort and empty results; prevent late registration overwriting termination; capture mappings and local scheme mismatch. G19-07/08. |
| chat-service | Vue → Nginx WebSocket upgrade → process-local room broadcast. Gorilla candidate; no existing DB/history or internal stream. | Approved safe payload/rendering and authenticated handshake with topic/origin checks; one reader/writer, bounded queues and explicit socket drain. Multi-replica fanout is a prerequisite to scaling. G19-11; Nginx route rollback separate from gateway. |
| pricing-service | Policy's internal HTTP dependency; PostgreSQL tariffs evaluated through MVEL and BigDecimal. Go evaluator deferred, storage adapter provisional. | Effective grammar/order and intermediate per-cover value/scale parity, HALF_UP per factor, CAR factor 50 and failure behavior. Preserve original MVEL; one authoritative price/rule identity per offer. G19-04/12. |
| policy-service | Offer/pricing call, policy writes/versioning/termination and JSON events. Provisional pgx/sqlc and Sarama with internal RPC/HTTP bridge. | Authenticated principal override, expiry, inclusive dates and scale-20 proration; transaction/concurrency/errors and event publication recovery. May call Java pricing while engine gate remains open. G19-04/07/12 (caller parity only if Java pricing retained). |
| payment-service | Kafka account creation, PostgreSQL balances and scheduled MinIO CSV import. Provisional pgx/sqlc, Sarama and MinIO SDK. | Account uniqueness, unknown-account semantics, decimal/date/balance ordering, exact file names, single import owner and durable retry/recovery across DB/copy/delete. Verify payment route permissions. G19-04/07/09; schedule handoff under G19-14. |
| agent-portal-gateway dependency | Retain Java edge and its route/default/principal transformations. Envoy remains Phase 5; service HTTP bridge can keep Java consumers unchanged. | Route/status/JSON and authorization matrix, direct-backend bypass tests, one translation owner and tested routing rollback. WebSocket currently traverses Vue Nginx separately. G19-05/06/14. |
| auth-service dependency | Retain legacy issuer until a separate Keycloak/user/entitlement migration. Include its PostgreSQL schema/CREATE_DROP hazard in ownership audit; no ninth Go rewrite. | Sanitized claim/role/issuer/audience fixtures, signing-key rotation, service credential propagation and product-role mapping. Keycloak/JOSE choice needs actual tokens and overlap/rollback proof. G19-04/06. |

## Sequence and coexistence

The historical order remains readable in the original analysis. The recommended order below
replaces its blanket risk labels; it is conditional on the service ticket's evidence and boundaries.

1. **Establish prerequisites before exposure.** Create service-owned Make/module/toolchain and
   contract standards; inventory real environment/schema/credential ownership, define interim
   identity and HTTP contracts, and complete the relevant Alloy ingestion and rollback signals.
   Inventory product's Phase 3 state rather than scheduling another presumed database migration.
   Rendering/pricing/storage research can be planned independently; no executable work is authorized
   by this review. Security restrictions needed for a route cannot be postponed wholesale to Phase 6.
2. **Product first, bounded to catalog reads.** Its list/get surface exercises JSONB mapping,
   transport, DI, config, auth and observability without also taking Kafka/MinIO/browser ownership.
   This is an inference from the controller/entity/loader linked in the PostgreSQL assessment. Capture data and
   fixtures first, exclude automatic seeding/DDL from the shadow process, and use an isolated
   store or proven non-writing permissions. If actual schema/contract evidence is unavailable,
   pause this pilot or reassess another bounded read path; do not call it unconditionally low risk.
3. **Policy-search, then dashboard.** Reuse the Elasticsearch client/trust setup, starting with a
   bounded query surface before the dashboard's aggregation/date/rounding cases. Query validation
   can start against captured/isolated data; consumer replacement is a distinct gate. Do not let
   two shadow groups write the production index or mistake an ID upsert for ordering protection.
4. **Document, then chat when their gates clear.** Document can retain jsreport while replacing
   the Java application; the MinIO platform and SQL/object/event gates still apply. Browser
   replacement waits for PDF/runtime evidence. Chat depends on a coordinated browser security and
   payload change, not on a new relational store. These independent services may exchange order
   if one gate is unresolved; shared fanout must precede multiple active chat replicas.
5. **Pricing, then policy, then payment as the default continuation.** Pricing requires a proven
   exact-money evaluator and immutable rule publication before ownership. Policy may migrate
   first while calling authoritative Java pricing through a tested compatibility adapter; a Go
   pricing rewrite is not a technical prerequisite to a Go policy caller. Policy's producer must
   interoperate with every remaining Java/Go consumer, without waiting for all consumer rewrites.
   Payment remains late because financial writes, schedules and object moves require recovery
   proof. None of these dates or risk levels is measured by this documentation review.
6. **Phase 5 and retirement are separately gated.** Identity/edge preparation can proceed alongside
   Phase 4. Adoption waits for G19-06/14, with all current Java consumers included. Phase 6 cleanup,
   Zipkin retirement and resource tuning wait for G19-15; successful Go HTTP cutover alone cannot
   authorize deleting Java resources, old columns, tariff syntax, topics or historical data.

For every migration, distinguish these independent control planes:

| Boundary | Safe coexistence condition and rollback requirement |
| --- | --- |
| HTTP/gRPC | Distinct Go Service/selectors and no production route until fixtures/security pass. Specify a reversible route change in the resource owner's workflow; verify the reconciler will not undo it. Weighted percentages require a real controller and tested thresholds/dwell; a separately specified atomic route switch is an alternative. Java must still understand post-cutover state before routing back. |
| PostgreSQL | One schema owner/executor, compatible expanded schema and explicit shared-versus-copied topology. Prevent Java auto-DDL and new Go startup migrations/seeders from competing. Contract/delete only after rollback closes; Down SQL alone cannot recover lost data. |
| Kafka | New Pods can consume without HTTP traffic. Shadow with isolated/disabled effects and explicit groups/offsets. Real handoff defines assignments, contiguous completed offsets, replay/deduplication, cross-topic stale-event handling and producer compatibility. Rolling back routes does not rewind offsets or undo external effects. |
| MinIO and scheduled imports | One owner of a real import/job during handoff; disable the other scheduler before enabling the new one. Use isolated keys/tenants for shadow tests, preserve Java-readable formats and prove recovery for object/SQL partial success. Restore/reconcile effects before resuming Java. |
| WebSocket | Existing connections remain on their accepting Pod. Route percentage does not rebalance sockets or unify process-local rooms. Define stop-upgrade, bounded close/drain, browser reconnect and accepted message loss semantics; coordinate protocol/browser rollback. |
| Pricing | Preserve original MVEL and an authoritative rule-set/price identity. Shadow calculations cannot update offers or serve divergent prices. Rollback must select compatible rules and cached state, not simply restore a container image. |

## Prerequisites and adoption gates

These are follow-up work definitions, not new external issues or assigned people. Roles identify
the affected responsibility only. **All execution gates are open/unexecuted.** “Before” is the
point where dependent adoption must stop; it does not block unrelated documentation or isolated
investigation. Detailed validation scenarios follow this table; their stable IDs identify future work.

| Gate | Affected scope / responsibility | Bounded task and evidence needed | Blocks until evidence exists |
| --- | --- | --- | --- |
| <a id="g19-01"></a>G19-01 | All Go; service/contract/tooling owners | Choose module/contract/toolchain ownership, compatible runtime/generator/test pins and license/advisory evidence. Implement Make/CI format, lint, unit/race/integration, cmd/consumer build, vulnerability and generation/breaking checks. Show added/deleted output detection and formatter idempotence. Reuse constructor boundaries; measure lifecycle graph before optional Fx comparison. | Implementation must establish the tooling interface first; no library/framework adoption or service completion based on example pins/CI. |
| <a id="g19-02"></a>G19-02 | All Go and credentials; deployment/config owners | Render each local/QA schema-to-env mapping, test required/empty/default/optional/redacted values and startup failures. Name fixed Secret creation/copy owner; prove ConfigMap rollout separately from Secret restart, overlap, revocation and rollback. | Go startup/config acceptance and credential use/rotation. |
| <a id="g19-03"></a>G19-03 | All Go; platform/service owners | Inventory actual cluster/image/namespace/Flux/Make ownership; render distinct selectors/ports, DNS, CA mounts, probes/resources and termination budgets. Test partial-startup cleanup, dependency failures, readiness, bounded drain and force-stop. | First integrated Go deployment and route exposure; no live platform claim from YAML. |
| <a id="g19-04"></a>G19-04 | Five business PG owners plus legacy auth; DB/release owners | Export safe schema/data/seed/auto-DDL inventory; choose sharing topology and one versioned executor. Validate exact decimal/JSONB/null/date mappings and transaction/conflict/cancellation behavior with real PG. Rehearse populated baseline, expand/backfill/contract, failed nontransactional operation and forward/restore recovery. | Shared writes/schema ownership; product read pilot also needs its schema/data fixture and no-seeding boundary. |
| <a id="g19-05"></a>G19-05 | API services, Java/Vue consumers; contract/edge owners | Capture legacy success/failure/presence/date/decimal/polymorphic/binary fixtures and effective validation. Test real HTTP/gRPC mappings, wrapped safe errors and generated consumers/contract baseline. For optional document/search streams, first show payload/consumer need and specify framing/ordering/backpressure. | First mixed call and public route cutover; streams/pages remain deferred without the additional evidence. |
| <a id="g19-06"></a>G19-06 | All routes plus auth/gateway; security/identity owners | Capture sanitized legacy token/permission matrix, rotate exposed trust material with overlap, specify user/workload propagation and test direct backend bypass/negative permissions. Separately prove Keycloak/JWKS/verifier, Envoy translation/JWT and mixed-peer mesh policies if selected. | Reachable protected Go endpoint; Phase 5 cutovers wait for their specific fixtures and rollback, without blocking an independently proven interim design. |
| <a id="g19-07"></a>G19-07 | Policy producer; document/payment/search/dashboard consumers | Capture JSON/topic/partition/group/offset baseline. Test chosen client on configured Kafka, ambiguous publish and DB failure, crash/replay, rebalances, poison messages, cross-topic order, contiguous commits, drain and Java/Go handoff. Decide durable publication/outbox or explicitly accepted weaker recovery semantics. | Real event publication or side-effecting Go consumption, including Pods with zero HTTP traffic. |
| <a id="g19-08"></a>G19-08 | Search/dashboard; index/query owners | Capture 8.19 mappings/settings/fixtures and QA CA trust. Prove local scheme mapping, query cap/post-sort, refresh, aggregation limits/rounding/timezones and safe failures; rebuild/replay into isolated indices and validate stale-event guards. | Production read-model ownership and query equivalence claims; new pages/streaming needs separate caller/load proof. |
| <a id="g19-09"></a>G19-09 | Document/payment and proposed archive stores; storage/platform owners | Inspect actual MinIO image/exposure and support/advisory remediation; verify chosen server/operator/license, encryption/retention/backup policy and CA/SAN/least-privilege access. Test inline/key/object bytes, upload/DB reconciliation, CSV parsing/claims/duplicate credit and copy/delete/commit failures. | Go storage ownership; rendering retention does not waive this gate. Archive use also requires applicable server and recovery evidence. |
| <a id="g19-10"></a>G19-10 | Document; renderer/service owners | Export effective template/assets/fonts/browser/license and reference PDFs. Interim jsreport needs support/capacity and event-to-PDF proof; replacement additionally needs text/visual tolerance, pinned sandboxed browser, hostile-resource tests, bounded load/timeouts/crash/drain. | Document pilot needs its interim-path evidence; chromedp selection/replacement waits for full rendering/runtime parity. |
| <a id="g19-11"></a>G19-11 | Chat/Vue/Nginx; browser/realtime owners | Approve safe payload/rendering and handshake identity; test origin/topic denial, sender exclusion, frame limits, queues/slow readers, race/load, close/reconnect and Nginx rollback. Require explicit fanout before scaling; identify real caller/retention need before internal stream/history. | Browser chat cutover; multi-replica, history and stream adoption are independently blocked. |
| <a id="g19-12"></a>G19-12 | Pricing and policy caller; pricing/domain owners | Export effective MVEL/factors/order and safe Java oracle fixtures; compare candidate grammar, intermediate decimal values/scales, HALF_UP, invalid rules and resource limits. Specify immutable publication/cache identity, preserve MVEL and test shadow/rollback/concurrent edits. | Go evaluator selection and pricing ownership; policy may retain Java pricing with proven caller parity. |
| <a id="g19-13"></a>G19-13 | All Go and QA observability; platform/operations owners | Prove Go/Java context and OTLP traces through Alloy; choose/test stdout log ingestion and supported metrics path through the collector. Verify safe fields, labels, local disabled behavior, bounded outage/flush and queryable signals plus service dashboards/SLO alerts. | Claims of full observability and any traffic shift relying on these rollback signals. |
| <a id="g19-14"></a>G19-14 | Each service plus edge/data/event owners | Write a concrete release/runbook with schema compatibility, resource controller, isolated shadow effects, HTTP routing, consumer offsets, job ownership and socket/rule transitions. Define measured pass/abort thresholds and dwell, then run internal parity/e2e/performance and rollback scenarios through owning Make targets. Reconfirm product PG baseline before pilot. | Production cutover; no default percentages, decommission or destructive rollback assumptions. |
| <a id="g19-15"></a>G19-15 | Platform and affected service owners | After an agreed rollback window, prove no active Java/Consul/Zipkin consumers, compatible retained data and tested trace archive/restore. Size HPA/resources from measured signals and prove chat fanout/job safety under replicas. Obtain separately scoped cleanup delivery. | Legacy retirement, destructive schema/data cleanup, archive adoption and autoscaling claims. |

## Follow-up validation scenarios

These are unexecuted acceptance tasks for future service/platform tickets. Use owning Make targets
and record exact environment, fixtures, outputs and limitations. Conditional features need these
checks only if selected. No external issue numbers or assignees are implied.

### PostgreSQL validation scenarios

| Scenario | Required evidence and adoption condition |
| --- | --- |
| DB-05-01 | Per-service owner; auth, documents, product, pricing, policy, payment — Export live/deployed schema and representative safe data from each environment; compare checked-in Java mappings, startup DDL/seeders and PostgreSQL 17 assumptions. Record DB owner and Java/Go sharing topology. |
| DB-05-02 | First selected Go PostgreSQL service — Pin compatible pgx/sqlc/Go versions; generate from actual migration chain, review generated null/JSONB/decimal types, compile via Make, test real PostgreSQL writes/reads, cancellation, conflicts and pool settings. A document pilot must include both inline PDF and MinIO-key rows and upload/DB partial failure; a product pilot must include all stored polymorphic JSON variants. |
| DB-05-03 | Release/deployment owner; all six clusters — Select/pin Goose or justified alternative, package a single migration executor, baseline populated DBs, stop conflicting auto-DDL, rehearse expand/backfill/contract and a failed nontransactional index build, verify permissions, status, backup/restore and Java rollback window. |
| DB-05-04 | Policy/payment and event consumers — Reproduce Java rounding/date/version behavior, duplicate and concurrent writes, transaction failure, Kafka publish/replay and MinIO partial failures. Specify idempotency and any outbox/compensation before implementation; do not infer atomic external effects from `@Transactional`. |
| DB-05-05 | Product/pricing owners — Compare actual JSONB predicates and tariff rows against proposed SQL and Java DTOs; use `EXPLAIN (ANALYZE, BUFFERS)` on representative safe data to select indexes, and validate dynamic filtering, nulls and decimal/rule results. Pricing engine selection remains gated by G19-12. |

### Composition validation scenarios

| Scenario | Required evidence and adoption condition |
| --- | --- |
| DI-06-01 | First bounded service's owner; reassess after a different second service — Record constructor graph (nodes/depth), number of listeners/workers/closers, bootstrap functions/lines, and startup/shutdown defects. If code stays reviewable by subsystem and failure tests pass, retain manual DI. Do not assume document then product is the lowest-risk order. |
| DI-06-02 | Repeated manual wiring defects or lifecycle failures across services — Compare a bounded Fx branch against the *same* constructors and test cases. Measure graph validation, startup diagnostics, edit/review effort, binary/dependency cost, test setup and complete stop behavior. Adopt only if the reduction in recurring defects/cost outweighs runtime framework conventions; do not set a line-count-only threshold. |
| DI-06-03 | Each service implementation ticket — Through owning Make targets, run constructor/startup integration tests for invalid config, failed dependency, listener bind collision and partial cleanup; test signal/fatal-worker cancellation, readiness transitions, slow in-flight work, deadline/force-stop, resource close order and repeated stop. Use race-enabled tests for shared state. Unit tests construct use cases directly with small fakes. |
| DI-06-04 | Deployment owner with first Go service — Render both environment overlays and verify probes, lifecycle hooks, Secret delivery, replicas and effective termination grace; exercise rolling stop with real clients/consumers before traffic shift. Static legacy manifests do not validate Go behavior. |

### Configuration validation scenarios

| Scenario | Required evidence and adoption condition |
| --- | --- |
| CFG-07-01 | Each service implementation — Table-driven loader tests with injected maps: minimal valid; each required key absent, empty and whitespace; optional absent/present; malformed bool/int/duration/URL; zero/negative/out-of-range/cross-field values; disabled integrations; file missing/unreadable/empty; sentinel secret never appears in errors/logs. Include an explicitly empty value with `envDefault` to pin chosen policy. |
| CFG-07-02 | First Go deployment per service/environment — Render base plus local-dev and QA overlays through the owning Make/Kustomize workflow. Compare effective ConfigMap keys, `envFrom`, explicit Secret names/keys, downward API and optional integrations to the Go schema. Prove an intended ConfigMap edit changes the Pod template and rolls a Pod; do not print Secret contents. |
| CFG-07-03 | Deployment and credential owners before traffic shift — Test missing Secret/key startup failure, Secret value-only update while Pod runs, controlled restart, successful replacement authentication, old/new credential overlap and rollback. For mounted files, test read-only access and restart/update behavior. Record actual cluster/controller mechanism and rotation owner. |
| CFG-07-04 | Service owner with search, storage, observability and security owners — Resolve QA TLS trust and endpoint scheme, local observability optionality, Elasticsearch/MinIO credentials, redacted diagnostics and security permissions. Validate startup failure happens before any listener or worker starts. |
| CFG-07-05 | Tooling owner — Select/pin a Go-compatible parser version, check current advisories/license, expose scoped config tests and overlay rendering via Make, and decide whether generated env documentation adds value. |

### Transport validation scenarios

| Scenario | Required evidence and adoption condition |
| --- | --- |
| TR-08-01 | Each migrating service plus gateway/browser owner, before HTTP route cutover — Capture golden requests and responses from current Java endpoints for success, empty, missing/zero/null, malformed, unauthorized/forbidden, not-found and internal-failure cases. Compare path, method, query defaults, status, headers, content type, JSON keys/number/date/byte representation and safe error bodies against the Go HTTP adapter or gateway. Include offer create, policy create/terminate/search/details, product questions, dashboard POSTs, document bytes and payment accounts as applicable. |
| TR-08-02 | Contract and Java/Go consumer owners, before first mixed call — Generate and pin versioned stubs; exercise Go-to-Go and any Java-to-Go or HTTP bridge path with real listeners. Test unknown/removed fields, absent versus explicit zero, reserved field numbers/names, decimal/date cases, propagated principal, auth metadata, traces, deadline/cancellation and wrapped-domain error mapping. Record retry/idempotency behavior for writes. |
| TR-08-03 | Deployment owner, before first Go Pod — Render local-dev and QA overlays with distinct selectors and HTTP/gRPC ports, HTTP/2 upstreams where required, Service DNS, health probes and startup/readiness/liveness policy. Exercise missing dependencies, partial startup cleanup, termination drain and forced timeout, long calls/streams and rollback. Do not infer cluster behavior from YAML. |
| TR-08-04 | Phase 5 edge/security owners, before Envoy or weighted shift — Choose one translation owner per route; demonstrate descriptor/annotation and route parity, principal injection, JWT/permissions, WebSocket/binary handling, TLS and safe errors. Name the actual controller/config for weighted routing, prove sticky/stateful and rollback behavior, and run staged traffic tests. G19-06/11/14 define the corresponding adoption gates. |
| TR-08-05 | Contract/tooling owners — Pin compatible compiler/runtime/generator/gateway versions and licenses; run current advisories, generated diff/breaking checks, HTTP/OpenAPI/client generation and contract tests through owning Make targets. Current example's `buf breaking` configuration alone is not an executed gate. |

### Request validation scenarios

| Scenario | Required evidence and adoption condition |
| --- | --- |
| VAL-09-01 | First Go API service and each exposed method — A legacy-fixture matrix for absent/null/empty/zero/whitespace, malformed JSON, unknown/subtype, invalid date/decimal and nested collections; explicit Protobuf presence and shape-rule decisions, with parity or approved differences. |
| VAL-09-02 | Transport adapter owner — Tests of unary interceptor, any selected stream interceptor and direct HTTP adapter using real listeners; expected gRPC/HTTP statuses, safe structured field errors, no raw values/internal details, and startup failure for invalid validator definitions. |
| VAL-09-03 | Application/domain owner — Tests for offer/policy/pricing stateful invariants, money/date behavior, permission/identity, conflict and failure handling independent of the transport. Do not treat annotations as their replacement. |
| VAL-09-04 | Contract/tooling owner — Pinned compiler/Buf/plugins/schema/runtime, license/advisory review, format/lint/generation through Make, reproducible output including untracked files, `check-proto-breaking` against the agreed released contract, and compile/tests for affected Go plus any Java/TypeScript/HTTP/OpenAPI consumers. |
| VAL-09-05 | Edge/security owners — Per-route HTTP fixture, generated OpenAPI/client and security/error comparison before moving Java/browser traffic. Record route owner and rollback path. |

### Messaging validation scenarios

| Scenario | Required evidence and adoption condition |
| --- | --- |
| MSG-10-01 | Pinned Go client connects to local and QA-equivalent Kafka 4.1 listener; produce/consume both event types and JSON fixtures; verify TLS path if selected, broker API/metadata, topic existence, partition counts, and Java/Go interoperability. |
| MSG-10-02 | Broker unavailable, leader failover, ISR below minimum, publish timeout after append, and process crash between SQL commit and send: record caller result, duplicate/loss window, retries and recovery. If atomic publication is required, prove chosen outbox relay and re-delivery. |
| MSG-10-03 | Crash before side effect, after side effect before offset commit, and during commit; replay each event and assert one effective document/account/search state. Explicitly test concurrent account creation and document-storage failure. |
| MSG-10-04 | Interleave registration and termination on their separate topics, rebalance during a slow handler, and replay from earliest/retention boundary; assert final policy-search state, contiguous offsets, no premature commit and a defined poison-message path. |
| MSG-10-05 | Roll Java and Go consumer instances through intended group/cutover modes; inspect actual group IDs, assignments, lag/offsets and side effects. Terminate while producer/consumer work is in flight; prove drain/flush/close or replay within the grace period. |

### Search validation scenarios

| Scenario | Required evidence and adoption condition |
| --- | --- |
| SEARCH-11-01 | First Go search/dashboard service: pin `go-elasticsearch/v8` and toolchain through its Make targets; record license/advisory result; connect to local HTTP and QA-equivalent HTTPS 8.19.6 with real CA verification, scoped credentials, timeouts, cancellation and resource cleanup. |
| SEARCH-11-02 | Index owner: export effective mappings/settings/aliases for both indices, capture sample documents/JSON/date/decimal forms, define index creation/rebuild and Java/Go dual-write ownership, and test refresh visibility. Resolve the local policy-search scheme mismatch by observing rendered/runtime configuration. |
| SEARCH-11-03 | Policy-search owner: compare legacy and Go `query_string` request/result/error fixtures for default `q=*`, empty/malformed/escaped input, matching/ranking, 0/1/100/>100 hits, null dates and post-cap sort. Keep unary list initially; a page/stream contract requires separate consumer/load evidence. |
| SEARCH-11-04 | Dashboard owner: compare exact DSL, filters, inclusive/exclusive date endpoints, term limits, count/sum, decimal rounding and histogram timezone on representative data including >10 products/agents and DST boundaries. Any correction to incomplete bucket totals needs an explicit behavior decision. |
| SEARCH-11-05 | Adapter/operations owner: inject 400/401/403/404/409/429/5xx, TLS, timeout and cancellation failures; verify safe error mapping, bounded retries, metrics without high-cardinality customer query labels, readiness and replay/stale-event behavior. |

### Realtime validation scenarios

| Scenario | Required evidence and adoption condition |
| --- | --- |
| RT-12-01 | Browser/edge owner: run real browsers through local and QA-equivalent Nginx with HTTP and HTTPS origins. Verify `/ws/chat/main/<user>` upgrade, sender exclusion, topic case, join/leave content, open/close behavior, proxy timeout/heartbeat, and rollback to Java. Capture current behavior and approved format changes. |
| RT-12-02 | Security/browser owner: test no token, expired/revoked token, forged username/avatar, unauthorized topic, cross-site Origin and proxy Host behavior; assert server-side identity, rejection code and no sensitive logging. Test HTML/script payloads in input and history/rendering against the updated Vue client. Agree the handshake identity mechanism under G19-06/11 before browser cutover. |
| RT-12-03 | Chat owner: race/load test many rooms and concurrent joins/sends, oversized/invalid frames, slow/no reader, queue saturation, disconnect during broadcast, deadline and heartbeat loss; prove bounded goroutines/memory and an explicit drop/disconnect policy. |
| RT-12-04 | Chat/deployment owner: test browser reconnect after pod crash, Nginx timeout and rolling stop. Verify close/reconnect timing, duplicate/lost-message policy, readiness transition, and full socket cleanup inside rendered termination grace. Repeat with two replicas only after a shared fanout design exists. |
| RT-12-05 | Only if an internal stream/history is specified: identify real producer/consumer and measure duplex need; test stream auth, per-message validation, deadlines/cancellation, flow control, load-balancing limits and forced shutdown. For history, test durable ordering, access, retention, replay and migration from the currently transient baseline. |

### Storage validation scenarios

| Scenario | Required evidence and adoption condition |
| --- | --- |
| ST-13-01 | Document/DB owner: fixtures for inline `bytea` PDFs, UTF-8 MinIO-key rows, missing objects, malformed key-like bytes, duplicate policy event and same-key reupload. Verify Java/Go reads and HTTP bytes/content type during coexistence; choose a durable row discriminator and safe correction path. Inject object-write success/DB-fail and reverse failure and prove reconciliation, duplicate handling and no unauthorized disclosure. |
| ST-13-02 | Payment owner: run the dated CSV fixture and missing-object, malformed row, unknown account, duplicate schedule, two-replica contention, read interruption, copy success/delete failure and DB commit failure scenarios. Verify exact source/processed keys, object metadata, row effects and retry/recovery without double credit or lost source. |
| ST-13-03 | Service/deployment owner: render local-dev and QA Go overlays, check endpoint/scheme/path addressing, Secret key names and CA/SAN trust. With isolated test tenants, exercise Put/Get/Stat/Copy/Delete, forbidden cross-bucket/admin access, wrong/rotated credentials, TLS rejection, timeouts and cancellation. Test a bounded large object, truncated read, checksum mismatch and multipart cleanup. |
| ST-13-04 | Platform/security owner: inspect running image and tenant settings; resolve the archived-server/advisory gate, operator compatibility, supported patched server/license, at-rest encryption/key custody, versioning/retention and backup/restore. Verify the service policies after any selected server change. No such live check ran in this step. |
| ST-13-05 | Tooling/first service owner: pin the chosen SDK against the module's Go toolchain, check advisories/license/transitive dependencies, and measure memory, retries and streaming behavior with service-size fixtures. Record Make-target and test evidence. |

### Rendering validation scenarios

| Scenario | Required evidence and adoption condition |
| --- | --- |
| DR-14-01 | Document owner: inventory effective local/QA jsreport `POLICY` template, assets, helpers, Chrome version, fonts, print options, template count/license and representative generated PDFs without exposing customer data. Compare with `policy.template`; resolve provisioner's create-only drift and template ownership. Capture output fixtures before replacing renderer. |
| DR-14-02 | Document/browser owner: render fixtures with both implementations using the same event fields: ordinary and long policy numbers/names, HTML metacharacters, accents/non-Latin text, empty/null values, dates across year boundaries, long/multipage content, missing font/asset and malformed template. Compare extracted text, dates, page count, typography/layout and accessible/downloadable output with documented tolerances; check PDF media type and valid file structure. |
| DR-14-03 | Go/deployment owner: build a pinned non-root, sandboxed Chrome image with fonts and owned update path. Measure cold/warm render latency, memory/CPU/PIDs, queue saturation and throughput under one and multiple document replicas; size Pods and termination grace from results. Test missing browser/font, browser crash/hang, timeout, client cancellation and clean shutdown. |
| DR-14-04 | Security owner: test hostile field values, edited templates, external/`file:` resources, redirects and script/network attempts. Verify contextual escaping, blocked egress/local-file access, inaccessible DevTools, sandbox active, isolated temporary profiles, no policy data in logs/artifacts and bounded resource use. Review browser/image advisories and licenses at the pinned versions. |
| DR-14-05 | Document/event/storage owner: exercise render success/failure through Kafka, MinIO and PostgreSQL, including duplicate/replayed events and concurrent writers. Verify no document loss, silent omission, duplicate row, object overwrite or orphaned PDF across retry/rollback, and that old inline-PDF rows remain readable. Preserve public query semantics or record approved differences. |

### Pricing validation scenarios

| Scenario | Required evidence and adoption condition |
| --- | --- |
| PR-15-01 | Pricing/DB owner: read-only export and classify effective rule sets, factors, order, update history and authoring path in local-dev/QA or an agreed representative environment. Reconcile them with seeds; identify unsupported functions/date/property access and any private production-only variants. Establish a deterministic order or approve a documented behavior change. |
| PR-15-02 | Pricing/API owner: capture Java oracle fixtures for all selected covers and products, partial/empty selections, condition boundaries (`NUM_OF_CLAIM` at 1/2/3), `TRI` EUR/WORLD, `HSI` APT/HOUSE, `FAI`, `CAR` factor 50, overlapping rule predicates and two applicable markups. Compare per-cover decimal **value and scale** and total, including `TRI` EUR adults=1/children=1 C1–C3 = 98.00 and `HSI` APT area=95, flood=NO, claims=1 C1–C3 = 172.50 from existing tests. Verify actual loaded order before asserting any multi-rule expected price. |
| PR-15-03 | Pricing implementation owner: spike Expr, CEL or restricted evaluator against the exported grammar using exact decimal text. Test fractional/negative HALF_UP ties (for example synthetic `0.005` and `-0.005`), high precision, integer/decimal promotion, multiple rounding steps and overflow/precision limits; compare Java and Go at every intermediate cover amount, not just totals. If live rules use division, capture its MVEL/Java precision and zero-divisor behavior first. Record latency, allocations and bounded cache behavior with realistic rule counts. |
| PR-15-04 | Pricing/security owner: verify missing/null/wrong-type and duplicate answers, colliding date/cover names, unknown tariff/cover, no matching base rule, malformed syntax, unknown identifier/function, wrong predicate/result type, division by zero, excessive literal digits, nesting, expression length, input cardinality and expensive functions. Record Java outcomes and agree safe Go error classifications, CPU/memory/deadline bounds and allowed function set. No arbitrary Java reflection, clock, network or file access in Go rules. |
| PR-15-05 | Pricing/policy/DB owner: design rule-set identity, effective dates/order and atomic publication with single-request binding. Test Java/Go coexistence on unchanged MVEL rows, concurrent rule edits, cache refresh, stale replicas, rollback and shadow price diffs. Verify offers persist their authoritative price and rule identity; reject cutover on unexplained per-cover differences. |

### Observability validation scenarios

| Scenario | Required evidence and adoption condition |
| --- | --- |
| OB-16-01 | Platform owner: render/install the pinned QA Helm values in a disposable or controlled environment; verify Alloy Service ports, collector pipeline health, Tempo and bridge exports, and Java Zipkin ingestion. Send one identified synthetic Go OTLP trace and find it in Tempo; test Java→Go and Go→Java parent/trace IDs with real HTTP/gRPC paths. Record B3/W3C handling and sampling. |
| OB-16-02 | Platform/logging owner: choose one stdout→Alloy→Loki or approved OTLP-log route; configure collection, RBAC, parsing, label policy and retention. Emit a harmless JSON `slog` record with a trace ID from a Go Pod, then query it in Loki and prove no duplicate, missing, secret or customer fields. Test collector outage/restart and bounded buffering/loss reporting. |
| OB-16-03 | Platform/metrics owner: decide and render a supported Alloy→Prometheus ingestion route. Verify receiver/monitor settings, authentication, a sample from Go runtime and an application counter/histogram in Prometheus, bounded label cardinality, no duplicate series, and failure/dropped-sample visibility. Do not treat an OTLP receiver's acceptance as Prometheus ingestion proof. |
| OB-16-04 | Go service owner: pin SDK/contrib/instrumentation versions against the service Go toolchain under G19-01. Test route-templated HTTP/gRPC spans, SQL/pgx and Kafka boundaries as applicable, context cancellation, exporter timeouts, provider flush/shutdown, disabled/local-dev behavior, and no direct backend dependency. Verify stderr/stdout JSON and no payload logging even at debug. |
| OB-16-05 | Service/operations owner: validate liveness/readiness/startup transitions, collector outage independence, restricted debug/metrics exposure, service identity, sampling rate, safe field/metric-label allowlists, collector and backend capacity, dashboards and actionable alerts. Capture actual SLO and rollback signals before a traffic shift. |
| OB-16-06 | Platform owner: before Zipkin retirement, prove no active Java/consumer dependency, full Tempo trace retention/search parity, and a documented, tested historical trace archive/restore path. Until then keep the bridge and do not change Go exporters to point at Tempo directly. |

### Security validation scenarios

| Scenario | Required evidence and adoption condition |
| --- | --- |
| SEC-17-01 | Auth, gateway and each Go service owner before first mixed route — Capture sanitized current access-token claims/signing method and effective `iss`/`aud`/expiry/roles; identify trusted issuer and safe migration path for the checked-in shared secret. Matrix every external/internal route, role and resource check, including `/api/payments/accounts`, policy create principal override, documents and service jobs. Test absent, malformed, wrong-signature, wrong-algorithm, expired/not-yet-valid, wrong-issuer/audience and denied-role/resource cases with safe 401/403 mapping. |
| SEC-17-02 | Platform and service owner before a Go endpoint is reachable — Render actual Service/ingress/network policy and verify only intended gateway/workloads can connect; specify user versus workload credentials, gRPC metadata and HTTP adapters, token propagation, authenticated principal mapping, secret/issuer ownership and TLS on each hop. Verify direct Pod/Service attempts cannot bypass a required permission. Include QA and local-dev. |
| SEC-17-03 | Identity owner before Keycloak cutover — Specify realm/client/audience/role mappers, agent/product-code entitlement mapping, MFA/login UX, issuer/JWKS and signing-key rotation. Pin a compatible verifier and test key-set refresh, unknown `kid`, issuer outage, old/new token overlap, replay/revocation policy, Java/Go validation and rollback. Only then decide local JWT verification versus introspection. |
| SEC-17-04 | Edge/mesh owner before Phase 5 or weighted shifts — Configure and test Envoy per-route JWT and translation policy, one translation owner, WebSocket and binary paths, principal/permission equivalence, TLS and rollback. For Linkerd, test mixed meshed/unmeshed traffic, default-deny policy, health probes, workload identities and mTLS, then prove the selected traffic-shift controller works. |
| SEC-17-05 | Chat, observability and operations owners before production exposure — Test WebSocket auth/origin/topic isolation and safe rendering; assert reflection/profiling/metrics are inaccessible from untrusted networks, debug logging contains no payload/tokens, Secret rotation triggers a verified rollout, and collector outage does not leak or open access. |

### Tooling validation scenarios

| Scenario | Required evidence and adoption condition |
| --- | --- |
| DEV-18-01 | First Go service/scaffolding owner before implementation — Decide module/contract ownership and Go/toolchain baseline; add scoped Make targets for all applicable canonical capabilities, pin versions and licenses, define output paths/exclusions and invoke the same targets in CI/container builds. Show `cmd/` and consumer build coverage and report any missing target. |
| DEV-18-02 | Contract owner before a published `.proto` change — Pin Buf/plugins/protovalidate schema/runtime; run format/lint/generation/checks via Make, compare against an agreed released baseline, detect new/modified/deleted files and compile/test all affected Go, Java, browser and HTTP/OpenAPI consumers. |
| DEV-18-03 | PostgreSQL service owner before schema rollout — Pin sqlc if adopted and Goose if selected; validate generated SQL, migration ownership and version status with a disposable PostgreSQL matching deployment major, test Java/Go expand/contract and forward recovery, and prove no app-replica startup migration. |
| DEV-18-04 | Service/CI owner before completion or traffic cutover — Run all affected module unit/race/integration/build/lint/format/vulnerability targets with prerequisites, then Java-parity, internal-endpoint, end-to-end, performance, security and rollback checks required by the service ticket. Record exact Make commands, environment, output, skipped/failed checks and scope. |

DEV-18-05 (mapping tooling gates to service complexity and phase order) is fulfilled by this
documentary review. It is not a runtime pass. All other scenario IDs above remain open until
the owning ticket produces the evidence required by its applicable adoption gates.
