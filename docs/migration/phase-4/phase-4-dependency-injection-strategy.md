# Dependency Injection Strategy for Insurance Hub Phase 4

> **Status — preliminary research.** This study is preserved as an input to the dated [Phase 4
> implementation review](phase-4-implementation-review.md), which contains the reviewed disposition,
> service scope, and adoption gates. Its recommendations are not final platform defaults by
> themselves.

## Executive recommendation

Phase 4 should use **dependency injection as a design pattern, but should not adopt a DI framework or container initially**. Standardize on explicit constructor injection, consumer-owned interfaces, and one composition root per Go service. Reassess after the first two migrations—`document-service` and `product-service`—using concrete evidence from their dependency graphs, bootstrap code, tests, and lifecycle handling.

The migration analysis currently names Google Wire as the proposed replacement for Micronaut DI, while the target-state principles also call for idiomatic Go, fewer framework abstractions, clear interfaces, and explicit composition. Those goals favor manual wiring. More importantly, Google archived Wire on August 25, 2025 and now states that the project is no longer maintained, so it should not be selected as the default for new Phase 4 services.[^1][^2]

**Recommended decision:**

- Use manual constructor injection for the Phase 4 service template.
- Keep application wiring exclusively in `cmd/<service>/main.go` or a small adjacent bootstrap package.
- Define narrow interfaces in consuming packages, not a repository-wide “interfaces” package.
- Keep constructors framework-neutral so a maintained wiring tool can be introduced later without redesigning business packages.
- Do not use Wire, `dig`, Fx, or `samber/do` in the first service unless a documented threshold is already exceeded.
- If a framework later becomes justified, evaluate **Uber Fx** first because it solves both graph assembly and process lifecycle; do not adopt raw `dig` merely to shorten constructor calls.

## What DI means in Go

Dependency injection and a dependency injection framework are separate decisions. In Go, DI can simply mean that a component receives collaborators through a constructor rather than creating database pools, storage clients, gRPC clients, loggers, or publishers internally.

Go interfaces are satisfied implicitly, and small one- or two-method interfaces are common in idiomatic Go. This allows a use-case package to describe only the behavior it consumes while the PostgreSQL, MinIO, Kafka, or gRPC adapter satisfies that contract without annotations, inheritance, or container registration.[^3][^4]

For Phase 4, the useful DI properties are:

- Dependency creation is separate from business logic.
- Required dependencies are visible in constructor signatures.
- Production adapters can be replaced with fakes in unit tests.
- Initialization errors remain explicit.
- Ownership and shutdown of resources remain visible.

None of those properties requires a framework.

## Project-specific assessment

The Insurance Hub target consists of independently deployed Go microservices rather than a single monolith. The migration introduces gRPC and optional gRPC-gateway endpoints, PostgreSQL access, Kafka, MinIO, Elasticsearch, OpenTelemetry, Prometheus, structured logging, and service-specific integrations such as `chromedp` and a pricing expression engine. Although the overall platform has many technologies, each service should own only a subset, keeping each in-process object graph moderate.[^1]

The first planned migration, `document-service`, is a good DI pilot because its graph is representative but bounded: configuration, logger, telemetry, PostgreSQL metadata repository, MinIO object store, PDF renderer, application service, gRPC handler, optional gateway, health endpoints, and server lifecycle. The next service, `product-service`, can test whether the same conventions remain understandable when JSONB persistence and other-service clients are added.[^1]

Manual wiring also fits the Strangler Fig process. Each Go binary has a deterministic assembly path that can be inspected while it runs beside its Java counterpart, and there is no hidden container behavior to complicate differential tests, startup failures, or progressive traffic cutover.

## Options

| Approach | Resolution model | Strengths | Costs and risks | Phase 4 fit |
|---|---|---|---|---|
| Manual constructor injection | Ordinary typed function calls compiled by Go | Maximum visibility; no DI dependency, reflection, generated files, or special commands; straightforward debugging and tests | Bootstrap code grows with the graph; lifecycle orchestration must be designed explicitly | **Best default** for each service |
| Google Wire | Generates ordinary Go wiring from providers and injectors; no runtime container[^5] | Compile-time graph generation; generated output is inspectable | Upstream is archived and explicitly unmaintained[^2]; adds generation workflow and generated-code maintenance | **Reject for new services** |
| Uber Fx | Runtime graph assembly over `dig`, plus modules and application lifecycle[^6][^7] | Reusable modules, managed startup/shutdown hooks, reduced repeated plumbing | Graph errors are discovered during validation/startup rather than ordinary compilation; reflection and Fx conventions reduce directness | **Conditional fallback** if measured complexity warrants it |
| Uber `dig` directly | Reflection-based directed acyclic graph resolved at process startup[^6] | Smaller scope than Fx; automatic constructor resolution | Adds runtime indirection without Fx lifecycle and module benefits | **Not recommended** |
| `samber/do` | Generics-based container with registration, scopes, health checks, lifecycle, graph inspection, and shutdown[^8] | Type-oriented API, active project, broad feature set | Introduces service-locator/container concepts and another framework convention; less compelling than manual DI for bounded services | **Watch, but do not standardize now** |

### Manual injection

Manual DI provides the strongest alignment with the migration document’s stated goal of explicit composition. It also gives the compiler a direct view of every constructor call and keeps startup behavior navigable with ordinary editor tooling.[^1]

Its main disadvantage is visible boilerplate. That is often a useful architectural signal: a very large `main` function can reveal constructors with too many dependencies, mixed responsibilities, or service boundaries that need refactoring. The response should first be to extract focused bootstrap functions—not immediately introduce a container.

### Google Wire

Wire originally offered a reasonable middle ground: dependencies are constructor parameters, and generated injector functions invoke providers in dependency order without runtime state or reflection. That explains why it appeared in the original analysis.[^5]

The situation changed materially when the repository was archived in August 2025 and marked “no longer maintained.” Existing systems may continue using frozen Wire versions, but introducing it in a new, multi-year migration would knowingly add an abandoned build-time tool to every service. The analysis should therefore replace “Wire for compile-time dependency injection” with “manual constructor injection and explicit composition; evaluate an actively maintained tool only if demonstrated complexity warrants it.”[^2]

### Uber Fx

Fx is more than a DI container: it constructs the dependency graph, supports reusable modules, eliminates global initialization, and manages application startup and shutdown. Its lifecycle runs startup hooks in registration order and shutdown hooks in reverse order with timeouts, which is relevant to long-running gRPC servers, gateway servers, database pools, telemetry providers, and background consumers.[^9][^7]

That capability is useful only if Insurance Hub develops substantial repeated lifecycle plumbing across services. Fx should therefore be treated as an application-framework decision, not a cosmetic replacement for explicit constructor calls. If selected later, keep Fx types at the composition boundary; domain, application, repository, and transport constructors should remain plain Go functions.

Fx modules can package self-contained functionality with providers, invocations, decorators, and visibility boundaries. This can eventually help with standardized observability, gRPC servers, Kafka consumers, or health endpoints, but premature “platform modules” risk coupling every service to abstractions that have not yet been validated through real migrations.[^10]

### Raw `dig`

`dig` resolves a reflected directed acyclic graph at process startup, and its documentation positions it primarily as infrastructure for an application framework such as Fx. Using it directly would hide construction ordering while still leaving signal handling, startup, readiness, shutdown, and cleanup to custom code. That trade does not solve enough Phase 4 problems to justify standardization.[^6]

### `samber/do`

`samber/do` is active and offers a generics-based API, registration, lazy or eager loading, scopes, lifecycle hooks, health checks, dependency-aware shutdown, and graph inspection. It is worth retaining on a future evaluation shortlist, especially if generics-based resolution is preferred over reflection-heavy APIs.[^8]

It should not be the initial choice because Phase 4 does not yet demonstrate a need for service scopes, lazy resolution, named registrations, or runtime container inspection. Those features can encourage a service-locator style if resolution leaks beyond the composition root.

## Proposed service pattern

Use a layered package structure with inward-facing dependencies and a single assembly boundary. Go’s module-layout guidance recommends placing non-public packages under `internal`, preventing unintended imports from other modules.[^11]

```text
cmd/document-service/
  main.go                 # composition root and process lifecycle
internal/document/
  service.go              # use cases and consumer-owned ports
  model.go
internal/adapters/postgres/
  document_repository.go
internal/adapters/minio/
  object_store.go
internal/adapters/chromedp/
  pdf_renderer.go
internal/transport/grpc/
  document_handler.go
internal/platform/
  config/
  observability/
  server/
```

Avoid generic package names such as `interfaces`, `implementations`, `utils`, or a shared `container`. Interfaces should sit beside the code that consumes them. This minimizes interface surface area and prevents adapters from dictating application contracts.

### Application constructor

```go
package document

import "context"

type Repository interface {
    Save(ctx context.Context, doc Document) error
    Find(ctx context.Context, id string) (Document, error)
}

type ObjectStore interface {
    Put(ctx context.Context, key string, content []byte) error
    Get(ctx context.Context, key string) ([]byte, error)
}

type PDFRenderer interface {
    Render(ctx context.Context, template string, data any) ([]byte, error)
}

type Service struct {
    repo     Repository
    objects  ObjectStore
    renderer PDFRenderer
}

func NewService(
    repo Repository,
    objects ObjectStore,
    renderer PDFRenderer,
) *Service {
    return &Service{
        repo: repo,
        objects: objects,
        renderer: renderer,
    }
}
```

Prefer returning a concrete type from constructors. Accept interfaces only at the consumer boundary where substitution is actually required. Be conservative when adding methods to exported interfaces because changing an interface can break implementations; Go’s compatibility guidance recommends adding a new interface when extending behavior is necessary.[^12]

### Composition root

```go
func run(ctx context.Context) error {
    cfg, err := config.Load()
    if err != nil {
        return fmt.Errorf("load config: %w", err)
    }

    log := logging.New(cfg.Log)

    shutdownTelemetry, err := observability.Setup(ctx, cfg.OTel, log)
    if err != nil {
        return fmt.Errorf("set up telemetry: %w", err)
    }
    defer shutdownTelemetry(context.Background())

    db, err := postgres.Open(ctx, cfg.Postgres)
    if err != nil {
        return fmt.Errorf("open postgres: %w", err)
    }
    defer db.Close()

    minioClient, err := objectstore.NewMinIO(cfg.MinIO)
    if err != nil {
        return fmt.Errorf("create MinIO client: %w", err)
    }

    repo := postgres.NewDocumentRepository(db)
    store := objectstore.New(minioClient, cfg.MinIO.Bucket)
    renderer := pdf.NewChromedpRenderer(cfg.Chrome)
    app := document.NewService(repo, store, renderer)
    handler := grpctransport.NewDocumentHandler(app, log)
    server := grpcserver.New(cfg.GRPC, handler, log)

    return server.Run(ctx)
}
```

The exact resource order should match actual ownership. The composition root creates process-wide resources, passes them downward, and closes them in reverse order. Request-scoped values—deadlines, trace context, authentication claims, and transaction handles—should travel through `context.Context` or explicit method parameters rather than a DI scope.

### Testing

```go
type fakeRepository struct {
    saved []document.Document
}

func (f *fakeRepository) Save(_ context.Context, doc document.Document) error {
    f.saved = append(f.saved, doc)
    return nil
}

func (f *fakeRepository) Find(_ context.Context, id string) (document.Document, error) {
    return document.Document{ID: id}, nil
}

func TestService_Generate(t *testing.T) {
    repo := &fakeRepository{}
    store := &fakeObjectStore{}
    renderer := &fakeRenderer{content: []byte("pdf")}
    service := document.NewService(repo, store, renderer)

    // Exercise the use case and assert observable behavior.
}
```

The unit test constructs the subject directly; it should not start a container. Integration tests can use real adapters assembled by a dedicated test helper, while end-to-end tests start the complete binary. This keeps tests aligned with production construction without hiding dependencies behind global replacement APIs.

## Lifecycle strategy

DI should not be used to model Kubernetes or request scope. Each process should normally have singleton-like infrastructure resources—database pool, Kafka producer, MinIO client, gRPC client connections, tracer provider—and lightweight request-specific state passed explicitly.

Adopt one small process runner for all services that provides:

- Signal-derived root context.
- Ordered startup of listeners and background consumers.
- Readiness set only after required dependencies are usable.
- Graceful stop with a bounded timeout.
- Reverse-order resource cleanup.
- Aggregation of concurrent server and consumer errors.

This runner can be ordinary Go built with `context`, `os/signal`, `errgroup`, and explicit `Close` or `Shutdown` functions. If implementing and maintaining that lifecycle repeatedly becomes a larger problem than dependency wiring itself, Fx becomes a justified candidate because lifecycle is one of its core capabilities.[^13][^9]

## Shared-code boundaries

The migration analysis proposes shared libraries for repetitive logging, metrics, and error handling. Keep these packages narrow and infrastructural; avoid a mandatory “Insurance Hub application framework” during the first migrations.[^1]

Good shared candidates include:

- Structured logger construction and standard fields.
- OpenTelemetry resource attributes, exporters, interceptors, and shutdown.
- Prometheus registration helpers where consistency is required.
- gRPC server/client interceptors.
- Health and readiness primitives.
- Error-to-gRPC-status mapping.
- Test containers and integration-test fixtures.

Do not centralize business interfaces, repository interfaces, all configuration, service constructors, or a universal dependency registry. Service-specific consumers should own those contracts.

## Adoption thresholds

A DI framework should be evaluated only when the migrated services provide evidence of recurring pain. Use the following trigger matrix after `document-service` and again after `product-service`.

| Signal | Stay with manual DI | Evaluate Fx |
|---|---|---|
| Composition root size | Clear, grouped bootstrap functions; roughly reviewable on one screen per subsystem | Repeated, difficult-to-review graph assembly despite package-level bootstrap extraction |
| Constructor graph | Mostly shallow, with explicit infrastructure-to-application-to-transport flow | Many independent modules, decorators, optional groups, or plugin-style registrations |
| Lifecycle | One or two servers plus a few closers | Multiple listeners, consumers, schedulers, coordinated hooks, and repeated shutdown bugs |
| Cross-service repetition | Small helpers remove duplication | Stable, self-contained modules are copied across several services |
| Failure detection | Compiler catches constructor mismatches; integration tests cover startup | Bootstrap changes repeatedly cause ordering or missing-provider defects |
| Team comprehension | New contributors can trace construction with “go to definition” | Wiring dominates review time and obscures service intent |

These are decision signals rather than rigid line-count rules. A 150-line composition root can be clearer than a 30-line container registration list whose behavior is distributed through annotations, tags, and lifecycle hooks.

## Phase 4 spike

Create a bounded architecture spike before standardizing the service template:

1. Implement the `document-service` skeleton with manual constructor injection.
2. Include real initialization shapes for gRPC, gRPC-gateway if needed, PostgreSQL, MinIO, `chromedp`, logging, metrics, tracing, health, readiness, and graceful shutdown.
3. Add unit tests using hand-written fakes and one integration test that assembles real adapters.
4. Record the dependency graph, composition-root size, number of managed resources, startup/shutdown complexity, and defects found during wiring.
5. Build an Fx branch only if manual assembly exposes concrete pain; keep all non-bootstrap constructors unchanged.
6. Compare the branches on readability, test ergonomics, startup diagnostics, lifecycle correctness, build workflow, binary behavior, and AI-agent edit reliability.
7. Capture the decision in an ADR and update `AGENTS.md` or the Phase 4 Go skill with the selected wiring conventions.

The point of an Fx branch is not to prove that Fx can assemble the service—it can—but to test whether it reduces total maintenance cost enough to offset hidden runtime graph resolution and framework conventions.

## ADR proposal

**Title:** Use explicit constructor injection for Phase 4 Go services

**Status:** Proposed

**Context:** The Java system uses Micronaut annotation-based DI. The target Go architecture emphasizes idiomatic Go, explicit composition, independent microservices, and testability. The existing migration analysis proposes Google Wire, but Wire is now archived and unmaintained.[^2][^1]

**Decision:** All Phase 4 Go services will initially use manual constructor injection. Each executable will have one composition root. Interfaces will be defined by consuming packages and kept narrow. Infrastructure clients will be created at startup and injected into application services and transports. No DI container may be resolved from domain or application code.

**Consequences:** Wiring is explicit and compile-checked, tests construct components directly, and no DI framework becomes a platform dependency. Bootstrap code will be longer and lifecycle helpers must be maintained. Uber Fx may be reconsidered after two migrated services if documented graph or lifecycle complexity exceeds the adoption thresholds.

## Migration-document change

Replace the current architecture-pattern row:

```markdown
| Dependency Injection | Micronaut DI (Annotations, JSR-330) | Wire for compile-time dependency injection |
```

with:

```markdown
| Dependency Injection | Micronaut DI (Annotations, JSR-330) | Explicit constructor injection with consumer-owned interfaces and a per-service composition root; evaluate Uber Fx only if measured wiring or lifecycle complexity justifies a framework |
```

Add the following under **Phase 4 → Establish Go Development Standards**:

```markdown
- **Dependency injection and composition:** Use explicit constructor injection as the default. Define narrow interfaces in consuming packages and assemble each service in a single composition root under `cmd/<service>`. Keep business and adapter constructors independent of any DI framework. Do not adopt Google Wire because its upstream project is archived and no longer maintained. After migrating `document-service` and `product-service`, review dependency-graph and lifecycle complexity; evaluate Uber Fx only if manual composition has produced recurring, documented maintenance problems.
```

## Final position

Phase 4 **does need disciplined dependency injection**, because the new services must isolate business logic from PostgreSQL, MinIO, Kafka, Elasticsearch, gRPC clients, telemetry, and other adapters. It **does not currently need a DI framework**. Manual constructor injection is the best architectural baseline, Wire should be removed from the target design, and Fx should remain a measured escape hatch for lifecycle-heavy or genuinely complex service graphs rather than a default inherited from Java framework habits.

---

## References

1. [system-overview-and-migration-analysis.md](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/62895318/e482fa40-2b67-4529-9335-b46b94524a53/system-overview-and-migration-analysis.md?AWSAccessKeyId=ASIA2F3EMEYEYYMGRDZD&Signature=kb%2B4YlkG6fhJZHBU%2Bjn4V4YFdJE%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEIb%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEaCXVzLWVhc3QtMSJHMEUCIHMCU88NCq%2B2y6RKJTWcBFiDzhaV9UKiE0WYDDGjWkh2AiEApQ2AgAnpK%2FhSbMLjMBFkONxi3bsf8sVj%2BVdut8EpVU0q8wQIThABGgw2OTk3NTMzMDk3MDUiDNkco56H%2FjTEhpkvuCrQBGAGFpbtQUL3EgpiIs%2B6DjxqyBUwDRH%2F6e3Ff3ptYdbitJ5ZcvcD81vgylMFp0MMluIT1Z4wLIh0xV2lOsffQiNloziXLJXKNFDyASl%2Bk%2BL6aKdsF4ej75QluB98FVqtEzeATGgh6RrQG7tk3GxUY5nFecq157wSEAvvWVsJoU%2Bb1I1Rs4OKfwd8DCba3foa1TnYWYChOQCbWq%2FmzQq8f1ufV4gH%2BVgOm8hY1gYqaBlj208vMz4Ok2aruuUkRGNJtu%2Fi2mKf0GEVMiXDn0GmYk0O2g3bmfUoCw4WMlm%2FgRoUq14Gt0lfvZDoMNtNPnVx4aYy7Fg0D0Um8PKp%2FeP2bYhVAQzmMU4xybHchmfSC63kAjS2OWwlB4cRPjznThe7%2FQZRecfJfbjFWqMH8svMAqwEtrqPVYcfhD1OR3Qwqlkg7Z1D8PsENpWnPfvUxkroWPHN79%2BukoI9TvknOFTJVfZswWO3TYG%2BmxcNuK9MI7kaC%2BWL8V0RbjRD9dIH3jgzmsVOZnQmjzLxUwXfKJezeiBBLKhx%2FPO4IbUyLqqstPiDTUtBEB9qwKwA3ba41prT2JLi%2FVXxSz%2BlNV%2FB0OgOomom7uQEPQ4cEotvkuSP9dG17PNV2uD%2FYe5KQywcSQBTpmx1Cq6eByCLyawfmrS%2FL8TuCgtlhNH9%2Bx5cM65y3s8SiyOtDkQCtXnUdSLfcqhXQ6wUiV%2B7StiJY%2Fl6iOiVzuAq%2F4IHr0blVPMgD44itjKKfgo9iHJ2Y6vQQVUKil3oS5BkCMleWYqXQ2mnsSrlirQww4Tv1QY6mAEKWS4H6GLIlzJ1Ilqxn9OZPS1tk98ptLgCP%2BUZRFyaorWAi7MuDve9NmqLm0vFIRsy%2BV%2F%2FxHSq57lBq781nAoz1F%2FxJOJc3vFam%2FCGogBr%2BiLQhHsVtPr5cRsv%2FReIaFQS6QbMIWeDPf16yp0byTrZRxxzALGLC8a66m2d8kbS9SJzeIgDE%2FlGBWUBCjhUAnAXELfUXt4xrQ%3D%3D&Expires=1790693398) - !-- START doctoc generated TOC please keep comment here to allow auto update -- !-- DONT EDIT THIS S...

2. [google/wire: Compile-time Dependency Injection for Go](https://github.com/google/wire) - Compile-time Dependency Injection for Go. Contribute to google/wire development by creating an accou...

3. [Effective Go - The Go Programming Language](https://go.dev/doc/effective_go)

4. [Frequently Asked Questions (FAQ) - The Go Programming Language](https://go.dev/doc/faq)

5. [Compile-time Dependency Injection With Go Cloud's Wire](https://go.dev/blog/wire) - Dependency injection tools like Wire aim to simplify the management of initialization code. You desc...

6. [dig package - go.uber.org/dig - Go ...](https://pkg.go.dev/go.uber.org/dig) - A reflection based dependency injection toolkit for Go. A constructor can declare a dependency on an...

7. [Fx](https://uber-go.github.io/fx/index.html) - Fx lets teams within your organization build loosely-coupled and well-integrated shareable component...

8. [samber/do: ⚙️ A dependency injection toolkit based on Go ...](https://github.com/samber/do) - This library implements the Dependency Injection design pattern. Releases20 (20) v2.1.0Latest 2 mont...

9. [Application lifecycle - Fx](https://uber-go.github.io/fx/lifecycle.html) - The lifecycle of an Fx application has two high-level phases: initialization and execution. Both of ...

10. [Modules - Fx](https://uber-go.github.io/fx/modules.html) - Lifecycle Modules Modules. An Fx module is a shareable Go library or package that provides self-cont...

11. [Organizing a Go module](https://go.dev/doc/modules/layout) - Go projects can include packages, command-line programs or a combination of the two. This guide is o...

12. [Keeping Your Modules Compatible](https://go.dev/blog/module-compatibility) - If you have an exported struct type, you can almost always add a field or remove an unexported field...

13. [Add an HTTP server - Fx](https://uber-go.github.io/fx/get-started/http-server.html) - Lifecycle Modules Features Features. Add a lifecycle hook to the application with the fx.Lifecycle o...
