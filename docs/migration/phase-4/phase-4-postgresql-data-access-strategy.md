# PostgreSQL Data Access Strategy for Insurance Hub Phase 4

## Executive recommendation

Phase 4 should **replace GORM as the default with `pgx/v5` + `sqlc`**, backed by version-controlled SQL migrations. Use `pgxpool` for PostgreSQL connections, write explicit PostgreSQL SQL, and let `sqlc` generate typed Go methods and models from the schema and queries. Use native `pgx` directly for the small minority of operations that do not fit generated static queries, such as highly dynamic filtering, `COPY`, `LISTEN/NOTIFY`, or specialized PostgreSQL types.

Do not interpret this as “use only Go’s built-in SQL processing.” The standard `database/sql` package provides a generic connection pool, transactions, context-aware operations, and driver abstraction, but it still requires a database driver and leaves query text, row scanning, and type mapping to application code. Since Insurance Hub has explicitly standardized on PostgreSQL and JSONB, native `pgx` offers a better fit than preserving theoretical database portability; it provides a PostgreSQL-native interface, pool, type support, `COPY`, `LISTEN/NOTIFY`, and a `database/sql` adapter when compatibility is needed.[^1][^2][^3][^4][^5]

GORM remains a capable option, but it should not be adopted merely because Micronaut Data JPA/Hibernate existed in Java. Its broad ORM surface—associations, hooks, eager loading, implicit conventions, auto-migration, and query construction—is useful for entity-heavy CRUD applications, but Insurance Hub’s target principles emphasize explicit composition and PostgreSQL-specific capabilities. The Phase 4 persistence layer will benefit more from reviewable SQL, generated compile-time types, and predictable transactions than from reproducing a JPA-style programming model.[^6]

## Existing architecture

The migration analysis currently specifies GORM across `chat-service`, `dashboard-service`, `document-service`, `policy-service`, `payment-service`, `pricing-service`, and `product-service`; it also names GORM as the general replacement for Micronaut Data JPA/Hibernate. At the same time, the target architecture intentionally consolidates on PostgreSQL, uses JSONB for product definitions, favors fewer framework-level abstractions, and calls for idiomatic Go with explicit composition.[^7]

These directions are slightly inconsistent. GORM is not inherently wrong, but making it a platform-wide default introduces an ORM abstraction even though database portability is not a target and some of the most important use cases—JSONB predicates, explicit locking, transactional policy/payment workflows, batch operations, and tuned reporting queries—are naturally expressed in PostgreSQL SQL.

## Options

| Option | Query model | Type safety | PostgreSQL access | Main trade-off | Phase 4 fit |
|---|---|---|---|---|---|
| `pgx/v5` + `sqlc` | Hand-written SQL; generated Go methods | Query parameters and results generated as Go types[^8][^9] | Native pgx types and features[^4][^5] | Requires SQL literacy and a generation step; dynamic queries need a separate pattern | **Recommended default** |
| Native `pgx/v5` | Hand-written SQL and explicit scanning/collectors | Go compiler checks calls, but SQL/result correspondence is mostly runtime | Excellent; PostgreSQL-native features are first-class[^4][^5] | More repetitive scanning and mapping | Recommended escape hatch and acceptable for tiny adapters |
| `database/sql` + pgx stdlib | Hand-written SQL and explicit scanning | Similar to native pgx without generated query contracts | Generic API; pgx-specific access remains possible through the adapter[^10] | Adds portability abstraction that the target does not require | Viable, but not the best default |
| GORM | Struct-centric ORM, query API, associations, hooks | Generics API improves typed calls, but mappings and generated SQL still require runtime verification[^6] | Supports raw SQL and many SQL features, but through ORM conventions[^6][^11] | Convenient CRUD; larger implicit behavior surface | Use only if an ORM-specific need is demonstrated |
| Ent | Go schema plus generated entity/query graph | Strong generated API and typed graph traversal[^12] | General SQL abstraction; custom SQL may still be needed | Substantial generated framework and schema workflow | Consider only for relationship-heavy graph models |
| Bun | SQL-first ORM/query builder over `database/sql` | Struct-based typed API | Supports PostgreSQL and explicit SQL-oriented queries[^13][^14] | Still an ORM/query DSL and mapping convention | Reasonable alternative, weaker fit than sqlc |
| `sqlx` | Explicit SQL plus struct scanning and named parameters | SQL-to-struct mapping is runtime | Whatever the selected `database/sql` driver exposes | Low ceremony, but no query code generation | Good small-step alternative to raw `database/sql` |
| Jet | Generated schema types plus typed Go SQL builder | Compile-time builder types[^15][^16] | PostgreSQL supported | Queries live in a Go DSL and generation reads an existing database | Useful for dynamic queries, not preferred as the platform default |

## Why sqlc with pgx

`sqlc` follows a simple model: write SQL, run the generator, and call generated, idiomatic, type-safe Go methods. It supports `pgx/v5`, and the generated package can work with `pgxpool` in production. This is not an ORM: SQL remains the source of truth and is directly reviewable by developers and DB tooling.[^8][^17][^9]

That model aligns well with the Strangler Fig migration. Existing Java behavior can be translated into explicit SQL and compared against the current service, while query plans and PostgreSQL semantics stay visible. Generated methods also reduce the repetitive `QueryContext`, `Scan`, and nullability code that otherwise makes raw SQL tedious.

For Insurance Hub specifically, it offers these advantages:

- PostgreSQL JSONB operators, path extraction, containment predicates, generated columns, and index-aware queries can be written directly instead of expressed through an ORM API.
- Policy and payment operations can define transaction boundaries explicitly and use locking clauses visibly.
- Queries are named artifacts that can be reviewed, tested, explained, and traced.
- Input and output types are generated from actual SQL and schema rather than inferred from ORM conventions.
- There is no runtime identity map, lazy-loading model, or implicit association traversal.
- The generated package can be hidden behind repository implementations, so business code does not depend on sqlc types.

`sqlc` supports generated query forms for one row, many rows, execution, row counts, and PostgreSQL batches. Generated `Queries` instances can also be bound to a transaction through `WithTx`, retaining the same generated query API inside an explicit transaction.[^18][^19]

## Why not raw database/sql

`database/sql` is production-grade infrastructure, not a complete persistence strategy. It provides a concurrency-safe `DB` handle, automatic pooling, transactions, dedicated connections, prepared statements, and context-aware operations. It does not parse or validate application SQL at compile time, generate result models, remove repetitive scans, or understand PostgreSQL JSONB beyond what the driver supplies.[^2][^3][^1]

Using only `database/sql` would maximize transparency but impose recurring code such as:

```go
rows, err := db.QueryContext(ctx, query, productType)
if err != nil {
    return nil, fmt.Errorf("query products: %w", err)
}
defer rows.Close()

var products []ProductRow
for rows.Next() {
    var row ProductRow
    if err := rows.Scan(
        &row.ID,
        &row.Name,
        &row.Definition,
        &row.CreatedAt,
    ); err != nil {
        return nil, fmt.Errorf("scan product: %w", err)
    }
    products = append(products, row)
}
if err := rows.Err(); err != nil {
    return nil, fmt.Errorf("iterate products: %w", err)
}
```

That is acceptable for a handful of queries but becomes low-value repetition across seven services. If a standard-library-shaped API is desired, `sqlc` can generate against `database/sql`; however, native `pgx/v5` is preferable because PostgreSQL—not cross-database portability—is the declared target.

## Why not GORM by default

GORM is actively documented and feature-rich. It supports associations, lifecycle hooks, eager loading, transactions, savepoints, context propagation, prepared-statement caching, batching, raw SQL, upserts, locking, hints, migrations, logging, plugins, and a generics API. For a conventional CRUD product with a rich object relationship graph, those capabilities can accelerate development.[^20][^21][^6]

The issue is not that GORM cannot implement Insurance Hub. The issue is whether its benefits justify a project-wide abstraction and convention layer. Several characteristics reduce its relative value here:

- The target database is deliberately PostgreSQL, so cross-database abstraction has limited value.
- Product definitions depend on PostgreSQL JSONB rather than portable relational mappings.
- The high-risk services require explicit, auditable transaction and locking behavior.
- Migration correctness matters more than rapid greenfield CRUD scaffolding.
- The target architecture already prefers explicit Go code over Java-style framework behavior.
- Service models, persistence rows, protobuf messages, and domain types should remain separate; GORM’s struct-centric convenience can encourage one model to serve all four roles.

GORM executes writes inside default transactions unless configured otherwise, and its documentation notes a performance trade-off if that safety is disabled. This is an example of behavior that every service team must know and configure consistently. It is manageable, but less direct than writing the intended transaction boundary explicitly.[^11][^22]

GORM’s `AutoMigrate` creates missing objects and changes some column definitions but intentionally does not remove unused columns. This makes it useful for prototypes and tests, not a sufficient production migration strategy for controlled, backward-compatible Strangler deployments. Even if GORM were selected for queries, production schema changes should use reviewed, versioned migrations executed separately from service startup.[^23]

## Alternative assessment

### Native pgx

`pgx` is both a PostgreSQL driver/toolkit and a `database/sql`-compatible driver. Its native API offers PostgreSQL-specific functionality and safer row helpers such as `CollectRows` and `ForEachRow` in addition to the familiar query style. It should be the foundational driver whether sqlc is used or not.[^4][^5]

Use native pgx directly when:

- A query is assembled dynamically from many optional predicates.
- Bulk ingestion should use PostgreSQL `COPY`.
- A component needs `LISTEN/NOTIFY`, pipeline mode, or specialized type handling.
- A tiny repository has so few queries that generation provides little value.
- A sqlc parser limitation blocks a required PostgreSQL construct.

Keep these direct queries in the same repository adapter and test them against PostgreSQL; do not scatter `pgxpool.Pool` throughout application services.

### Ent

Ent defines schema in Go and generates entity packages, predicates, and graph traversal APIs. It is strongest when the principal problem is maintaining a large, relationship-heavy entity graph and when teams value a generated query graph more than direct SQL visibility.[^12]

Insurance Hub is organized as bounded microservices with separate databases or schemas and explicit service APIs, not as one large in-process entity graph. Ent would therefore add more schema and generated-code machinery than the current problem appears to require. Its documentation recommends versioned migrations for mission-critical environments, which is consistent with the broader recommendation even if Ent is not chosen.[^24][^12]

### Bun

Bun positions itself as a SQL-first ORM built on `database/sql`, with struct models, a typed query builder, relationship support, migrations, hooks, scanning, and OpenTelemetry integration. It exposes SQL concepts more directly than a traditional entity-centric ORM, making it a credible compromise for teams that reject code-generated SQL methods.[^13][^14]

It still replaces literal SQL with a library DSL for many queries. Since Insurance Hub will use PostgreSQL-specific SQL and has an explicit, code-generation-friendly workflow already through Protobuf, sqlc offers a smaller and more transparent abstraction.

### sqlx

`sqlx` extends `database/sql` with struct scanning, named parameters, and `Get`/`Select` helpers while leaving the underlying standard interfaces available. It is a pragmatic option when the desired change is simply “raw SQL with less scanning boilerplate.”[^25][^26]

Its mappings and SQL correctness remain runtime concerns, so it gives up sqlc’s main advantage: generated contracts derived from schema and queries. Prefer it only if the team explicitly wants to avoid a generation step or if sqlc cannot process a substantial portion of required queries.

### Jet and query builders

Jet generates schema types and provides a type-safe SQL builder with result mapping. It can be valuable for highly dynamic filtering because composable predicates are easier to express in a Go builder than as a large collection of static SQL variants.[^15][^16]

Its generator normally introspects an already-running database, whereas sqlc can generate directly from version-controlled migrations. Migration-first generation is a better fit for deterministic CI and GitOps. If dynamic dashboard or product-search filters become unwieldy, evaluate a query builder locally within that adapter rather than changing the platform default.[^27][^28]

## Recommended stack

Use this baseline for PostgreSQL-backed Phase 4 services:

```text
PostgreSQL
  └── versioned SQL migrations
        ├── applied by a dedicated migration job/tool
        └── parsed by sqlc
              └── generated Go queries for pgx/v5
                    └── repository adapter
                          └── application-owned interface
```

Recommended components:

- **Driver and pool:** `github.com/jackc/pgx/v5/pgxpool`.
- **Query generation:** `sqlc` with `sql_package: pgx/v5`.[^9]
- **Schema evolution:** versioned SQL migrations using the project’s chosen migration tool.
- **Domain boundary:** repository interfaces owned by consuming application packages.
- **Testing:** repository integration tests against real PostgreSQL; unit tests use narrow fakes.
- **Observability:** instrument pool/query boundaries with OpenTelemetry and expose pool statistics.
- **Exceptions:** direct pgx or a local query builder where static generated SQL is objectively unsuitable.

`sqlc` reads migration files but does not apply them, so migration execution must remain a separate deployment concern. In Kubernetes, run migrations as an explicit release step or controlled Job before shifting traffic; do not let every replica race to mutate the schema at process startup.[^29][^27]

## Suggested layout

```text
cmd/product-service/
  main.go
internal/product/
  service.go                 # application logic and repository interface
  product.go                 # domain type
internal/adapters/postgres/
  repository.go              # maps sqlc rows to domain types
  tx.go                      # explicit transaction orchestration where needed
  db/
    queries/
      products.sql
    migrations/
      000001_products.up.sql
      000001_products.down.sql
    generated/               # sqlc output; no hand edits
sqlc.yaml
```

Do not expose generated row types outside the PostgreSQL adapter. A generated database row reflects storage nullability and column shapes; it should not become the domain model or protobuf message by convenience.

## Example configuration

```yaml
version: "2"
sql:
  - engine: "postgresql"
    schema: "internal/adapters/postgres/db/migrations"
    queries: "internal/adapters/postgres/db/queries"
    gen:
      go:
        package: "dbgen"
        out: "internal/adapters/postgres/db/generated"
        sql_package: "pgx/v5"
        emit_interface: true
        emit_json_tags: true
        emit_empty_slices: true
```

This configuration should be pinned and invoked through repository-standard commands such as `make generate`, `make sql-vet`, and `make verify-generated`. Generated code should be committed so local builds, CI, code review, and AI-assisted edits all operate from the same artifacts.

## Example query

```sql
-- name: GetProduct :one
SELECT id, code, name, definition, version, created_at, updated_at
FROM products
WHERE id = $1;

-- name: FindActiveProductsByCoverage :many
SELECT id, code, name, definition, version, created_at, updated_at
FROM products
WHERE active = true
  AND definition @> jsonb_build_object('coverage', $1::text)
ORDER BY name, id
LIMIT $2 OFFSET $3;

-- name: UpdateProductDefinition :one
UPDATE products
SET definition = $2,
    version = version + 1,
    updated_at = now()
WHERE id = $1
  AND version = $3
RETURNING id, code, name, definition, version, created_at, updated_at;
```

The repository maps generated storage types to the application model:

```go
package postgres

type Repository struct {
    pool    *pgxpool.Pool
    queries *dbgen.Queries
}

func NewRepository(pool *pgxpool.Pool) *Repository {
    return &Repository{
        pool:    pool,
        queries: dbgen.New(pool),
    }
}

func (r *Repository) Get(ctx context.Context, id uuid.UUID) (product.Product, error) {
    row, err := r.queries.GetProduct(ctx, id)
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return product.Product{}, product.ErrNotFound
        }
        return product.Product{}, fmt.Errorf("get product %s: %w", id, err)
    }

    return toDomain(row)
}
```

## Transaction pattern

Transaction ownership belongs at the operation that knows the atomic business boundary—not inside each individual CRUD method. `sqlc` supports rebinding generated queries to a transaction via `WithTx`.[^19]

```go
func (r *Repository) ReplaceDefinition(
    ctx context.Context,
    id uuid.UUID,
    expectedVersion int64,
    definition []byte,
) (product.Product, error) {
    tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
    if err != nil {
        return product.Product{}, fmt.Errorf("begin transaction: %w", err)
    }
    defer tx.Rollback(ctx)

    qtx := r.queries.WithTx(tx)
    row, err := qtx.UpdateProductDefinition(ctx, dbgen.UpdateProductDefinitionParams{
        ID:         id,
        Definition: definition,
        Version:    expectedVersion,
    })
    if err != nil {
        return product.Product{}, mapDatabaseError(err)
    }

    if err := tx.Commit(ctx); err != nil {
        return product.Product{}, fmt.Errorf("commit transaction: %w", err)
    }
    return toDomain(row), nil
}
```

Always call rollback defensively, check commit errors, pass context, and map PostgreSQL errors to application-level errors at the adapter boundary. For payment and policy workflows, define isolation and lock behavior deliberately rather than inheriting defaults accidentally.

## Service-by-service fit

| Service | Recommended access | Rationale |
|---|---|---|
| `document-service` | sqlc + pgxpool | Metadata CRUD is a low-risk pilot; object bytes remain in MinIO, not PostgreSQL |
| `product-service` | sqlc + pgxpool; direct pgx/query builder for genuinely dynamic JSONB filters | Explicit SQL exposes JSONB operators and index-sensitive predicates |
| `dashboard-service` | sqlc + pgxpool for PostgreSQL configuration; separate Elasticsearch adapter | Reporting queries benefit from visible joins and projections rather than hydrated entities |
| `chat-service` | sqlc + pgxpool | Explicit pagination, ordering, retention, and insert paths; add batch/COPY only if measured |
| `pricing-service` | sqlc + pgxpool | Versioned/effective tariff-rule queries should be explicit and auditable |
| `policy-service` | sqlc + pgxpool | Critical workflows need visible transactions, constraints, locking, and optimistic concurrency |
| `payment-service` | sqlc + pgxpool | Financial state transitions require explicit atomicity and idempotency; MinIO remains a separate adapter |
| `policy-search-service` | No PostgreSQL abstraction unless a relational dependency is introduced | Keep Elasticsearch integration independent |

Using one baseline does not require every service to expose identical repositories or share generated database packages. Each service should own its schema, migrations, queries, generated code, and adapter contracts.

## Migration strategy

### First pilot

Use `document-service` as the initial persistence pilot because its relational metadata is bounded and the broader migration already identifies it as the first low-risk service. Implement its repository twice only if needed for an evidence-based decision:

1. Create versioned metadata-schema migrations.
2. Implement the required queries using `sqlc` + `pgxpool`.
3. Add repository integration tests against the same PostgreSQL version used in Kubernetes.
4. Verify generated query code, transaction behavior, pool configuration, graceful shutdown, and telemetry.
5. Measure development friction and generated-code stability; do not focus only on microbenchmarks.

### Second validation

Use `product-service` to validate JSONB and dynamic-query behavior:

1. Reuse the PostgreSQL schema established in Phase 3.
2. Implement known API use cases as named SQL queries.
3. Inspect plans for containment and path queries against production-like data.
4. Test the required GIN or expression indexes through versioned migrations.
5. Introduce direct pgx or a query builder only if optional filter combinations make static queries unmaintainable.

### Critical services

Do not wait until `policy-service` or `payment-service` to establish transaction conventions. Before migrating them, define and test:

- Transaction ownership and propagation.
- Isolation-level selection.
- Optimistic version columns and conflict handling.
- Idempotency-key persistence.
- Constraint-name-to-domain-error mapping.
- Retry policy for serialization/deadlock failures.
- Outbox or event-publication atomicity if database writes and Kafka events must be coordinated.

## Decision triggers

Continue with sqlc + pgx unless evidence shows one of these sustained problems:

| Signal | Response |
|---|---|
| Most operations are stable SQL with known result shapes | Keep sqlc + pgx |
| A small number of dynamic searches cannot be represented cleanly | Use direct pgx or a local query builder for those methods |
| Numerous rich relationship traversals dominate multiple services | Evaluate Ent, but only within the affected service |
| The team rejects generation but wants less scan boilerplate | Evaluate sqlx |
| CRUD speed clearly outweighs SQL visibility and ORM conventions are accepted | Reconsider GORM or Bun for that bounded service |
| A service requires cross-database portability | Reassess native pgx; use `database/sql`-based tooling if portability is real |

Avoid mixing GORM and sqlc for ordinary CRUD within the same service. Multiple data-access mechanisms complicate transactions, connection pooling, telemetry, error mapping, and contributor expectations. A localized direct-pgx exception alongside sqlc is less problematic because both use the same driver, pool, types, and SQL-first model.

## Schema migration policy

Regardless of query technology:

- Store forward and rollback migrations in version control.
- Review generated or hand-written SQL before deployment.
- Never run GORM `AutoMigrate` in production service startup.
- Apply schema changes independently from application replica startup.
- Design expand-and-contract changes so Java and Go versions can coexist during traffic shifting.
- Include indexes, constraints, data backfills, and compatibility windows in the migration ticket.
- Test migrations from a copy or representative snapshot of the previous schema state.

This separation is especially important during Strangler cutovers: the Java and Go implementations may access the same logical data while traffic moves gradually. Schema changes must therefore remain compatible with both implementations until the old service is decommissioned.

## ADR proposal

**Title:** Use sqlc and pgx for PostgreSQL access in Phase 4 Go services

**Status:** Proposed

**Context:** The migration plan currently proposes GORM as the general replacement for Micronaut Data JPA/Hibernate. The target architecture standardizes on PostgreSQL, including JSONB, and favors explicit, idiomatic Go with limited framework abstraction. The service portfolio includes simple metadata stores, query-heavy dashboards, JSONB product definitions, versioned pricing rules, and transaction-critical policy and payment workflows.[^7]

**Decision:** PostgreSQL-backed Go services will use `pgx/v5` with `pgxpool` and sqlc-generated query code by default. SQL and schema migrations remain version-controlled source artifacts. Repository adapters hide generated database types from application and transport layers. Native pgx may be used inside an adapter when a query cannot be represented cleanly by sqlc. Production schema migration is independent from application startup.

**Consequences:** SQL remains explicit and PostgreSQL features remain accessible; many query/schema mismatches become generation or compilation failures; and row-scanning boilerplate is reduced. The team must maintain a code-generation step, write SQL directly, and separately operate a migration tool. Highly dynamic queries may require direct pgx or a localized builder.

## Migration-document changes

Replace:

```markdown
| ORM/Data Access | Micronaut Data JPA / Hibernate | GORM for PostgreSQL interaction |
```

with:

```markdown
| SQL/Data Access | Micronaut Data JPA / Hibernate | PostgreSQL-native access using pgx/v5 and pgxpool, with sqlc generating type-safe Go code from version-controlled SQL queries; use native pgx inside repository adapters for exceptional dynamic or PostgreSQL-specific operations |
```

Add under **Phase 4 → Establish Go Development Standards**:

```markdown
- **PostgreSQL access:** Use `pgx/v5` with `pgxpool` and `sqlc` as the default persistence stack. Keep SQL queries and schema migrations explicit and version-controlled, generate typed Go query methods with `sqlc`, and map generated database rows to application-domain types inside repository adapters. Use native `pgx` only for operations that do not fit static generated queries, such as highly dynamic filtering, `COPY`, `LISTEN/NOTIFY`, or specialized PostgreSQL features. Apply production migrations through a dedicated migration step rather than application startup; do not use GORM `AutoMigrate` in production.
```

Across the component descriptions, replace “GORM models and queries” with “sqlc-generated queries over pgx/v5,” and replace “GORM native JSONB support” with “explicit PostgreSQL JSONB queries and types over pgx/v5.” The command/query-bus row should refer to handlers calling application ports implemented by PostgreSQL repository adapters, not handlers implemented directly with a database library.

## Final position

GORM is a valid, maintained ORM, but it is not the strongest architectural default for this migration. Raw `database/sql` is transparent and dependable, yet too repetitive as the sole project-wide approach. **The best balance for Insurance Hub is explicit SQL plus generated Go contracts: `sqlc` + native `pgx/v5` + `pgxpool`, with versioned migrations and narrow repository adapters.**

---

## References

1. [Accessing relational databases - The Go Programming Language](https://go.dev/doc/database/)

2. [Managing connections - The Go Programming Language](https://go.dev/doc/database/manage-connections)

3. [Types](https://pkg.go.dev/database/sql) - Package sql provides a generic interface around SQL (or SQL-like) databases.

4. [pgx/doc.go at master · jackc/pgx](https://github.com/jackc/pgx/blob/master/doc.go) - PostgreSQL driver and toolkit for Go. Contribute to jackc/pgx development by creating an account on ...

5. [pgx package - github.com/jackc/pgx/v5](https://pkg.go.dev/github.com/jackc/pgx/v5) - Package pgx is a PostgreSQL database driver.

6. [Docs](https://gorm.io/docs/) - The fantastic ORM library for Golang aims to be developer friendly. Overview Full-Featured ORM Assoc...

7. [system-overview-and-migration-analysis.md](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/62895318/e482fa40-2b67-4529-9335-b46b94524a53/system-overview-and-migration-analysis.md?AWSAccessKeyId=ASIA2F3EMEYEYPWWZS6L&Signature=4bHTJV%2BrrFP6JEiDTssByOf6zHs%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEIb%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEaCXVzLWVhc3QtMSJIMEYCIQCLphFr4uvvwgonasQARGPXVPFhfQuTsvP20pe%2FuksQHgIhAKHRYjIDo3a8kEwYrLNvRlvNWYSDAGplHoH06M%2F1RmVtKvMECE8QARoMNjk5NzUzMzA5NzA1Igy0xkS4o5NujWWNPhYq0ASl9tPkrrWDxNPQpgeHaEnVekdTw5rjnI733cZzJuKskmT%2BjkWoWAlecRhMqz5bIoc4CMCD2vTkSWhhGzQU%2F86EvCGnFdzBsVfQLiX%2BhVhkdVN5IgHbd7NCtfDh%2B9c9p%2FPbNkxjHMCLUSUwGs221SKC7Cpo9sKrRqv1MDP1sB9J5kviwcM1mb2RMWhudbT6lhOREZ%2BzUpF9lkf53J0fHb6MYTVIE6vJlSF1jNeqj3dYjbN29mEmiHWEB3ko%2FSQkgrC7UfGo8ntinGzqFLIDWDEoH0P%2FkimfPWOTIzC2xbXfSK2cylqbd%2FpFzDhY4p4G0REG2Gp8bfnbHaxlFnQhC3a3TgHmCVs%2BGgeFhXM0wQZDSSHtWtq4SlKUOIPvU4U9M1Q%2B1il232kjHPUQ8HKbuF7uI0pjrVNZEdxxLDBEjZxrZ%2B7uLiCImzc23ktqJsRDFG5IirLIAreVoZhmiLh92AnRUFk6GJFC8HpXZTEVEc%2BMflScHBV2CHaWSIG%2B6hVVyf6k7NQambRPpGhHZ9aB28vdHGvfIVTNUVLdeqvrIty3SMztDYiTkloRYAzRPZzZ0IKxQQ1bEy7rOhzfNC5vPRjq0y6XdAIyIERwVUbFlnrIiu1w71yoNWKUzct0l5lR3MR4g4kzJqd0DsUkgON55QecnJTM4Ft%2BtI3MksTP7i0xX93LMMSegBZ%2FUzPgDiBkjGXTjJjmPQw36wLDA%2BHHg9KMyvS2l0f5uRwsHR0Qt%2BU%2FWyRKTgKolwAXlCrcIIIvlSBkfceunPX2hmCPGUxEmlQkMMCD79UGOpcB1BEDbh5GWDd0xgGPpfzn64BdeS4Q5gZBVeIi4STVOoPwO0eOjwhh2H9f1kky6PenCm5Pb1SMfesd5aVwBppwyyy8lLAQKUmrZRASd52TQEWxYq5PxqLhtdhABpinS1Dreq4vDMnNnnn2JxIBescKY0TKlT5%2Bi%2BJGJKuHhFkvd%2BdZlDr9FCq6eaZ1DakgBEAu4HV3igqY4g%3D%3D&Expires=1790693267) - !-- START doctoc generated TOC please keep comment here to allow auto update -- !-- DONT EDIT THIS S...

8. [Sqlc Documentation](https://docs.sqlc.dev/en/latest/)

9. [Using Go and pgx - sqlc Documentation](https://docs.sqlc.dev/en/latest/guides/using-go-and-pgx.html)

10. [stdlib package - github.com/jackc/pgx/v5/stdlib - Go Packages](https://pkg.go.dev/github.com/jackc/pgx/v5/stdlib) - Package stdlib is the compatibility layer from pgx to database/sql.

11. [Performance](https://gorm.io/docs/performance.html) - Caches Prepared Statement. Creates a prepared statement when executing any SQL and caches them to sp...

12. [Quick Introduction | ent](https://entgo.io/docs/getting-started/) - ent is a simple, yet powerful entity framework for Go, that makes it easy to build

13. [uptrace/bun: SQL-first Golang ORM - GitHub](https://github.com/uptrace/bun) - SQL-first Golang ORM. Contribute to uptrace/bun development by creating an account on GitHub.

14. [Golang ORM for PostgreSQL and MySQL - Bun - Uptrace](https://bun.uptrace.dev/guide/golang-orm.html) - Bun is a Golang ORM for PostgreSQL and MySQL that is based on database/sql APIs.

15. [jet/doc.go at master · go-jet/jet](https://github.com/go-jet/jet/blob/master/doc.go) - Type safe SQL builder with code generation and automatic query result data mapping - go-jet/jet

16. [GitHub - go-jet/jet: Type safe SQL builder with code generation and](https://github.com/go-jet/jet) - Type safe SQL builder with code generation and automatic query result data mapping - go-jet/jet

17. [sqlc Documentation — sqlc 1.31.1 documentation](https://docs.sqlc.dev/en/stable/index.html)

18. [docs.sqlc.dev · en · latestQuery annotations — sqlc](https://docs.sqlc.dev/en/latest/reference/query-annotations.html)

19. [sqlc/docs/howto/transactions.md at main · sqlc-dev/sqlc](https://github.com/sqlc-dev/sqlc/blob/main/docs/howto/transactions.md) - Generate type-safe code from SQL. Contribute to sqlc-dev/sqlc development by creating an account on ...

20. [Skip Hooks](https://gorm.io/docs/session.html) - GORM provides Session method, which is a New Session Method, it allows to create a new session mode ...

21. [GORM 2.0 Release Note](https://gorm.io/docs/v2_release_note.html) - Prepared Statement Mode. Prepared Statement Mode creates prepared stmt and caches them to speed up f...

22. [gorm.io · docs · transactionsTransactions | GORM - The fantastic ORM library for Golang ...](https://gorm.io/docs/transactions.html) - Disable Default TransactionGORM perform write (create/update/delete) operations run inside a transac...

23. [Views](https://gorm.io/docs/migration.html) - Auto MigrationAutomatically migrate your schema, to keep your schema up to date. NOTE: AutoMigrate w...

24. [Versioned Migrations | ent](https://entgo.io/docs/versioned-migrations/) - Quick Guide

25. [Use sqlx with go-mssqldb - SQL Server | Microsoft Learn](https://learn.microsoft.com/en-us/sql/connect/golang/use-go-sql-extensions-library-with-go-mssqldb?view=sql-server-ver17) - Use the sqlx library with the go-mssqldb driver for struct scanning, named parameters, and query hel...

26. [go-sqlx/sqlx: general purpose extensions to golang's ...](https://github.com/go-sqlx/sqlx) - general purpose extensions to golang's database/sql - go-sqlx/sqlx

27. [Modifying the database schema](https://docs.sqlc.dev/en/latest/howto/ddl.html?highlight=migr)

28. [Home](https://github.com/go-jet/jet/wiki) - Type safe SQL builder with code generation and automatic query result data mapping - go-jet/jet

29. [Modifying the database schema — sqlc 1.31.1 documentation](https://docs.sqlc.dev/en/stable/howto/ddl.html)

