# Go Development

Apply to handwritten Go services and their contracts, persistence, and runtime configuration.
Load [Go formatting](go-formatting.md) and [Go validation](go-validation.md) alongside this rule.
Before planning or editing, discover and read the service-local `AGENTS.md` files applicable to
each affected path, following the scope, precedence, and conflict procedure in
[the root guide](../../AGENTS.md). Local Go conventions may add scoped detail; they do not silently
override canonical repository requirements. Stop and surface conflicts for resolution.

## Scope and Makefile interface

- Before editing, identify affected services, packages, `go.mod` files, any `go.work`, local
  instructions, Makefiles, tool configurations, and CI gates. Include affected contract consumers.
- **Use the corresponding Makefile targets for all Go development operations.** This includes
  tool installation, dependency changes, formatting, linting, tests, builds, execution, generation,
  migrations, containers, and deployment. Do not invoke underlying Go tools, formatters, linters,
  generators, or scripts directly, including through shell wrappers or an IDE action.
- Read target recipes and prerequisites before invoking them. Use the owning Makefile's documented
  working directory and supported variables; do not assume a root target covers nested modules.
  Read-only file inspection and Git review remain part of normal repository discovery.
- If a needed target is missing or insufficient, add or extend the smallest appropriate target
  when within the ticket's scope, following [CONTRIBUTING.md](../../CONTRIBUTING.md#makefile).
  Otherwise report the missing capability and affected validation as blocked. Never fall back to
  direct tooling, invent a target, or claim a check passed because no target exists.
- Keep versions, flags, exclusions, module selection, and tool order in Makefiles and checked-in
  configuration. Scripts may implement recipes, but agents invoke them through Make. Developer,
  editor, and CI entry points must use the same targets.
- Derive Go/toolchain versions and import prefixes from the affected module's manifests and
  tooling. Keep CI and container builds consistent with them. Do not copy example-repository
  versions, choose a workspace topology implicitly, or upgrade unrelated modules.

## Service structure and dependencies

Use the separation demonstrated by `campsite-booking-go`, adapted to each service's complexity:

| Area | Responsibility |
| --- | --- |
| `cmd/` | Thin executable entry point; process exit belongs here. |
| `internal/service` | Explicit construction, adapter wiring, startup, and shutdown. |
| `internal/domain` | Business types, invariants, errors, and required repository interfaces. |
| `internal/application` | Use cases; command/query handlers and validators where useful. |
| `internal/grpc` and other transport adapters | Request validation, DTO mapping, transport error mapping. |
| `internal/postgres` and other storage/integration adapters | Persistence and external-system implementations. |
| `internal/config`, `internal/logger` | Typed environment configuration and logging setup. |
| Versioned API definitions and generated packages | Public contracts and generated clients/servers, separate from domain types. |
| `db/migrations`, `internal/testing` | Versioned migrations and reusable test support as needed. |

- Dependencies point inward: adapters depend on application/domain; business logic must not import
  transport, protobuf DTOs, database implementations, or deployment configuration.
- Keep packages small and cohesive. Use explicit constructors and narrow interfaces owned by their
  consumers (domain/application for repository ports). Do not mirror every concrete type with an
  interface or require generic handlers/decorators where a simple function suffices.
- Preserve service/module boundaries. Never import another service's private implementation.
  Share contracts or common infrastructure only when required by the ticket; avoid a shared
  business-model package introduced just to remove duplication.
- Prefer standard-library facilities and established module libraries. New dependencies need a
  concrete purpose and validation path. Maintain dependencies only through the owning Makefile,
  only in affected modules, and review both `go.mod` and `go.sum` changes.

## Implementation and lifecycle

- Pass `context.Context` first for blocking/I/O operations and propagate it through handlers,
  repositories, and clients. Honor cancellation and deadlines; do not replace request contexts
  with background contexts or store them in business objects.
- Handle meaningful errors; add concise operation context, preserve inspectable causes with `%w`,
  and match wrapped errors with `errors.Is`/`errors.As`. Keep error strings lowercase without final
  punctuation except proper names/acronyms. Do not panic or exit for expected business failures.
- Give every goroutine an owner, cancellation path, and bounded shutdown. Synchronize shared state;
  avoid mutable package globals. Release resources on partial startup failures as well as shutdown.
- Handle termination signals, stop accepting work, drain in-flight work within a deadline, and
  close clients, pools, and telemetry exporters. Cleanup needs a usable bounded context, not an
  already-cancelled request context. Preserve the original failure when cleanup also fails.
- Keep retries bounded and cancellation-aware, and only retry operations known to be safe. Preserve
  idempotency for writes; do not stack application and infrastructure retries blindly.

## Contracts and persistence

- Use versioned protobuf contracts for internal gRPC and the ticket's agreed HTTP mapping at the
  edge. Validate request shape at the transport boundary (as with Buf/protovalidate in the example)
  and business invariants in application/domain code. Keep domain and wire types separate.
- Map known domain errors consistently to gRPC/HTTP statuses, including wrapped errors. Return
  safe internal errors for unexpected failures; do not expose SQL, secrets, or internal details.
- Preserve field numbers, presence/default semantics, validation, error responses, authentication,
  and authorization unless the specification changes them. Reserve removed protobuf names/numbers.
  Update callers, mocks, generated clients, gateway/OpenAPI output, and compatibility tests together.
- Never edit generated files manually. Change definitions or interfaces, then invoke the owning
  Make targets for generation and checks. Pin generator versions in repository tooling.
- Keep database details behind repository ports. The migration analysis proposes GORM; the example
  uses `database/sql` with pgx and Goose. Reuse the architectural boundary, not an implicit switch
  of persistence stack. Follow the service ticket and record any agreed departure from the plan.
- Make transaction boundaries, isolation, concurrency/version checks, and conflict behavior explicit.
  Use parameterized queries or safe ORM bindings, honor contexts, and handle commit/rollback,
  resource cleanup, and row-iteration errors. Test database-enforced behavior against PostgreSQL.
- Use versioned migrations and preserve compatibility while Java and Go coexist. Do not assume
  application startup may migrate production schemas just because the example does so. The ticket
  must define migration ownership, execution, and rollback/forward-recovery behavior.

## Phase 4 migration and operations

Follow [the migration analysis](../../docs/system-overview-and-migration-analysis.md), especially
Phase 4 and the 8-May-2026 Alloy update.

- Before a rewrite, capture observable Java behavior from service/API modules and business flows:
  DTOs, validation, errors, permissions, events, persistence, date/time and monetary semantics.
  Define parity cases and explicitly agreed differences in the ticket description.
- Keep each rewrite independently deployable beside Java. Validate internal endpoints before
  production traffic, retain a rollback path, and treat traffic shifts and Java decommissioning as
  separate specified delivery actions. Do not assume Phase 5 authentication/mesh work is complete.
- Preserve service-specific requirements: document generation and object storage, database-backed
  pricing expressions and their results, PostgreSQL JSONB, search, and messaging where applicable.
  A generic scaffold is not evidence of business parity.
- Parse environment configuration into typed settings at startup; validate required values and
  fail clearly. Use managed secrets and keep service instances stateless. Do not copy local `.env`
  defaults or credentials into production configuration.
- Use `log/slog` with structured JSON to stdout/stderr in deployed services, and propagate trace
  context across transport and integration boundaries. Emit OpenTelemetry signals through Alloy's
  configured endpoints/pipelines; do not couple services directly to Loki, Prometheus, or Tempo.
  Include service identity and trace correlation, with bounded metric label cardinality.
- Do not copy the example's full command/query/result or RPC payload logging. Exclude credentials,
  tokens, and customer/policy/payment data; log only deliberately selected safe fields.
- Provide meaningful readiness/liveness behavior and bounded server/client timeouts. Enable debug
  profiling or reflection only under the service's documented exposure/access policy.

## Tests

Use the [Go testing examples](../examples/go-testing/README.md) for concrete patterns; adapt their
illustrative contracts to the ticket and actual module APIs.

- Test changed observable behavior, failures, and boundaries. Use table-driven cases when helpful,
  clear arrange/act/assert structure, and the module's established assertion conventions.
- Follow the example's unit-test separation, generated Mockery/Testify mocks, and tagged
  integration tests where those tools are adopted. Mock dependency boundaries, not implementation
  details; regenerate mocks through Make when interfaces change.
- Use isolated disposable dependencies (such as PostgreSQL Testcontainers) for integration tests,
  with the actual migrations and reliable cleanup. Never point tests at shared production data.
- Keep tests deterministic: control time and test data, use synchronization rather than sleeps,
  register cleanup, and call `t.Helper()` in helpers. Do not weaken tests to accommodate regressions.
- Include contract/status mapping, cancellation, rollback/conflicts, and legacy parity cases as
  appropriate. See [Go validation](go-validation.md) for required Make-based checks and evidence.
