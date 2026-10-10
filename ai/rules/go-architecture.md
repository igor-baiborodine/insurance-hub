<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Go Design and Architecture](#go-design-and-architecture)
  - [Business purpose and proportional design](#business-purpose-and-proportional-design)
  - [Responsibilities and dependency direction](#responsibilities-and-dependency-direction)
  - [Consumer-owned ports and adapter mappings](#consumer-owned-ports-and-adapter-mappings)
  - [Composition and resource ownership](#composition-and-resource-ownership)
  - [Architecture handoff and review](#architecture-handoff-and-review)
  - [Repository reference and adaptation limits](#repository-reference-and-adaptation-limits)
  - [Sources](#sources)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Go Design and Architecture

Apply when designing, implementing, changing, or reviewing Go service boundaries and composition.
Follow [Go development](go-development.md) for implementation, lifecycle, contract, migration, and
Makefile requirements; [Go testing](go-test.md) for test design; and
[Go validation](go-validation.md) for required checks and evidence.

## Business purpose and proportional design

- Apply Clean/Onion principles by keeping source dependencies directed toward business policy.
  Apply screaming architecture by making the service's business capabilities visible in its
  module, package, file, type, and use-case names. These are complementary requirements:
  generic layer names alone do not communicate business purpose.
- Preserve the repository's package conventions and existing service boundaries. Use concrete
  business names such as `Product`, `ListProducts`, and `GetProduct` within those conventions;
  do not reorganize directories merely to resemble an architecture diagram.
- Keep packages small and cohesive. Use ordinary Go structs, functions, explicit constructors,
  and narrow interfaces. Add command/query separation, buses, event sourcing, generic handlers,
  decorators, or additional layers only when the service's requirements justify them.
- Adapt the structure to the service's complexity. A scaffold does not need invented business
  packages, and a small service does not need one package or interface for every type.

## Responsibilities and dependency direction

Use these responsibilities within the existing layout, creating packages only when needed:

| Area | Responsibility and dependency boundary |
| --- | --- |
| `cmd/` | Thin process entry point; configuration bootstrap, signals, and process exit. Delegates construction and lifecycle. |
| `internal/service` | Composition root: knows concrete adapters, constructs dependencies, wires use cases, and owns startup and shutdown. |
| `internal/domain` | Business values, invariants, and domain errors. Does not depend on application, adapters, or process wiring. |
| `internal/application` | Named use cases, orchestration, application errors, and ports consumed by those use cases. Depends on domain, not concrete adapters. |
| `internal/http`, `internal/grpc`, other inbound adapters | Protocol validation, input/output mapping, and safe transport errors. Invoke application behavior. |
| `internal/postgres`, other outbound adapters | Implement inward-owned ports; map external/storage representations to core values. Own driver and generated storage dependencies. |
| `internal/config`, `internal/health`, `internal/logger`, `internal/telemetry` | Configuration and operational support, assembled at the service boundary; do not pull runtime infrastructure into business policy. |
| Versioned API definitions and generated packages | Public wire contracts and clients, separate from domain/application models. |
| `db/migrations`, `internal/testing` | Migrations where the service owns them and reusable test support where needed. Test provisioning must not enter the production dependency closure. |

- Domain and application must not import transport adapters, HTTP/protobuf DTOs, generated SQL
  row types, database drivers, concrete persistence implementations, or deployment/composition
  configuration. Do not hide these dependencies inside aliases, embedded fields, or port signatures.
- Adapters depend toward the core. The composition root may import concrete adapters to construct
  them; this does not permit the core to import the composition root or use it as a service locator.
- Preserve service/module boundaries. Never import another service's private implementation or
  depend on the scaffold as a runtime library. Share public contracts or infrastructure only when
  required and explicitly onboarded; do not introduce a shared business-model package merely to
  remove duplication. Follow the [module topology guide](../../docs/migration/phase-4/go-module-topology.md).
- Go's `internal` visibility restriction does not enforce inward layering within a service.
  Inspect actual imports and types at boundaries; package names alone do not prove separation.

## Consumer-owned ports and adapter mappings

- Define interfaces in the package that consumes them, with only the operations that consumer
  needs. Application-owned reader/repository ports are appropriate for application use cases;
  place an interface in domain only when domain behavior actually consumes it. Do not collect
  every interface in domain or a generic `interfaces` package.
- Return concrete adapter types from constructors where practical. Go's implicit interface
  satisfaction lets the composition root inject them without a core import of their implementation.
  Function parameters are also valid small boundaries; do not mirror every concrete type with an
  interface or introduce an interface solely for mocking.
- Inbound adapters own protocol validation and wire mappings; domain/application own business
  invariants and use-case decisions. Multiple transports exposing the same capability must reuse
  that application behavior. Do not duplicate business logic in handlers or add a loopback protocol
  hop solely to reuse a use case.
- Outbound adapters own SQL/driver/generated-row use, external payload decoding, and conversion
  into core-owned values or results. Inbound adapters convert those results into their own DTOs or
  protobuf messages. Preserve specified presence, precision, ordering, and error semantics at each
  boundary; one transport's serializer is not automatically another transport's contract.
- Translate infrastructure failures into errors meaningful to the consuming port while preserving
  inspectable causes. Transport adapters own safe protocol status/body mapping. See
  [contracts and persistence](go-development.md#contracts-and-persistence) for detailed requirements.

## Composition and resource ownership

- Make dependency construction and lifetime ownership explicit in the composition root. Separate
  acquiring shared resources from constructing adapters that borrow them. Document who closes each
  resource; business ports must not acquire lifecycle methods solely because an adapter uses a pool.
- Inject the required dependencies directly. Avoid hidden global registries, service locators,
  and business objects that construct infrastructure internally. Shared pools/clients may remain
  concrete types in infrastructure wiring; dependency inversion does not require abstracting every
  infrastructure-to-infrastructure interaction.
- Reuse appropriately scoped resources and use cases across adapters. Product's single process-owned
  pool is an example, not a requirement that every service have exactly one pool or the same listeners.
- Ensure construction failures release acquired resources, and successful construction has a clear
  shutdown owner. Readiness must reflect the service's required dependencies and lifecycle; keep
  health probing outside business use cases. Follow
  [implementation and lifecycle](go-development.md#implementation-and-lifecycle) for cancellation,
  bounded drain/cleanup, and preservation of original failures.

## Architecture handoff and review

- For a new service or a material boundary/composition change, maintain a service README architecture
  section with business capabilities, an actual package/file responsibility map, a labeled Mermaid
  diagram, and one representative request walkthrough. Update affected portions as implementation
  changes; do not present a proposed diagram as evidence of delivered behavior.
- Distinguish source dependencies, interface satisfaction, startup injection, and runtime calls.
  Runtime dispatch from application to an adapter does not imply an application import of that
  adapter. Label arrow meanings; identify component diagrams separately from deployment diagrams.
- Verify documentation against actual imports, port signatures, constructors, mappings, and resource
  owners. Validate links and rendering through the owning Make documentation targets, and inspect
  diagram readability. Follow the development rule's missing-target procedure when needed.
- Verify core behavior independently of live infrastructure, then exercise relevant production
  composition and adapter paths through integration tests. Follow the testing and validation rules;
  a diagram or an import inspection does not prove runtime behavior.
- Record architectural inspection separately from executed checks. The current repository topology
  checker enforces cross-module boundaries, not domain/application/adapter layering within a module.
  Do not claim an automated architecture gate exists unless an owning Make target actually checks
  those restrictions. Business visibility and appropriate abstraction still require review.

## Repository reference and adaptation limits

The [Product catalog architecture](../../services/product-service/README.md#product-catalog-architecture)
is the committed reference for the composition and handoff lessons delivered in issue 132.
Its [application](../../services/product-service/internal/application/catalog.go) owns `ProductReader`
and the named list/get use cases; its
[composition root](../../services/product-service/internal/service/service.go) injects the same use
cases into both transports and owns pool cleanup. The
[PostgreSQL reader](../../services/product-service/internal/postgres/reader.go) satisfies the port
without an application dependency on PostgreSQL. Read these examples when adapting the pattern;
ignored ticket artifacts are not required to use this rule.

Generalize dependency, mapping, and ownership principles. Product's read-only behavior, pgx/sqlc
stack, exact timeouts, listener count, anonymous access, and Java schema ownership are service-specific
decisions. Future services must follow their own contracts, security requirements, persistence
ownership, and migration specifications.

## Sources

- [Clean Architecture](https://blog.cleancoder.com/uncle-bob/2012/08/13/the-clean-architecture.html): inward source dependencies and boundary separation.
- [Screaming Architecture](https://blog.cleancoder.com/uncle-bob/2011/09/30/Screaming-Architecture.html): organization that communicates business purpose and use cases.
- [Go interface guidance](https://go.dev/wiki/CodeReviewComments#interfaces): consumer-owned interfaces and concrete implementation types.
