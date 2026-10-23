<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Insurance Hub Consumer Profile](#insurance-hub-consumer-profile)
  - [Composition and ownership](#composition-and-ownership)
  - [Selected house conventions](#selected-house-conventions)
  - [Go ownership and validation](#go-ownership-and-validation)
  - [Phase 4 migration and operations](#phase-4-migration-and-operations)
  - [Repository reference and adaptation limits](#repository-reference-and-adaptation-limits)
  - [Repository reference and limits](#repository-reference-and-limits)
  - [Python ownership and validation](#python-ownership-and-validation)
  - [Legacy Java selection](#legacy-java-selection)
  - [Operations and command bindings](#operations-and-command-bindings)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Insurance Hub Consumer Profile

## Composition and ownership

Read [profile.json](profile.json), [the shared consumer entry](framework/consumer.md), its core
catalog component and the selected canonical workflow. Resolve this checkout with
`git rev-parse --show-toplevel` even from a module. Discover applicable nested guides before edits.
The submodule contributor AGENTS.md governs framework contributions only. Conflicts stop dependent
work; report both sources under the root guide's hierarchy. Guidance loading needs no toolchain.

`.gitmodules` owns the source and full `refs/heads/main` target; the index gitlink owns the exact
working revision. There is no second SHA lock. Framework bodies stay unmodified. Profile v1 selects
core, Go, Python, Java, house and operations, but language rules load only for affected tasks.
Go-only work must not read Java guidance. Read all selected house/operations guidance; load the
local infrastructure runbooks only for their affected boundaries.

The root [artifact policy](../AGENTS.md#artifact-rules) owns `ai/artifacts/`, its exact local
exclusion, resolver and retained historical paths. Numbered GitHub issues use the shared
[local-authority synchronization rule](framework/rules/spec-first-rules.md#github-ticket-synchronization):
exact body comparisons on planning/resume/review/completion, explicit publication permission and
read-back after each authorized update. Ad hoc tickets are exempt. Missing access blocks dependent
issue work. Mandatory local review and optional hosted review retain their separate roles;
Continue/Copilot files are compatibility pointers, outside this adoption's support claim.

## Selected house conventions

Adopt the shared Git branch/commit, naming and Make conventions. The local
[pre-push hook](../.githooks/pre-push) owns enforcement; configuration is explicit in
[CONTRIBUTING](../CONTRIBUTING.md). Existing branches are not renamed retroactively.
Adopt Go's underscore-separated test components, Given/When/Then, gofumpt/goimports/golines
100-column policy and Python's Given/When/Then and Ruff E4/E7/E9/F/I/UP/B at 100 columns.
Owners keep pins, ordering, exclusions and local import prefixes. No incidental normalization.

Use `./scripts/markdown-toc.sh format <task-owned-files>` and then `check` for every changed
Markdown file. Node/npx and pinned doctoc are owned by that script. For prepared offline/client
trials, set `AI_DOCTOC_DIR` to the absolute initialized framework root after explicitly running
`make -C ai/framework markdown-setup`; the wrapper verifies doctoc 2.5.0 and executes local Node
without npx. An explicit `npm_config_offline` setting is retained on the compatibility npx path. Name GitHub workflows by
scope and purpose; retain filenames/job IDs unless the task changes them.

## Go ownership and validation

All Go operations, including setup, formatting, generation, tests, build and deployment, use the
corresponding owning Make targets. Missing capabilities block work unless adding them is in scope.
[go-module-topology.json](../go-module-topology.json) and the
[topology guide](../docs/migration/phase-4/go-module-topology.md) own module discovery.
The current owners are Product and the scaffold; no shared contract module or workspace is approved.
Read their Makefiles/configuration for actual pins and scope. Root `go-modules-check` uses standalone
module checks; a new module requires explicit inventory/CI onboarding. A topology check does not
prove layering within a service. Preserve service/module boundaries and never import another
service's private implementation or the scaffold as a runtime library.

Adopt the shared Go architecture package responsibilities within the existing layout. The local
Product reference and migration requirements below remain normative for this repository.
For persistence choices, the migration analysis proposes GORM; example code's pgx/database/sql
and Goose choices do not authorize an implicit stack switch. Follow each service ticket,
PostgreSQL integration requirements and Java-owned schema boundaries.

Product's JSON reporting gate is `make -C services/product-service verify-tools pilot-test-json`
with explicit `TEST_PACKAGES`; it runs readonly modules with `-count=1` and creates test/cache
output only. Adoption acceptance checks actual JSON discovery, no skips, original fixtures and
failure propagation; a zero-event launcher is not a pass. This bounded pilot does not certify
all Product packages, storage, deployment, or a complete CI gate.

## Phase 4 migration and operations

Follow [the migration analysis](../docs/system-overview-and-migration-analysis.md), especially
Phase 4 and the 8-May-2026 Alloy update.

- Before a rewrite, capture observable Java behavior from service/API modules and business flows:
  DTOs, validation, errors, permissions, events, persistence, date/time and monetary semantics.
  Define parity cases and explicitly agreed differences in the ticket-content specification.
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

## Repository reference and adaptation limits

The [Product catalog architecture](../services/product-service/README.md#product-catalog-architecture)
is the committed reference for the composition and handoff lessons delivered in issue 132.
Its [application](../services/product-service/internal/application/catalog.go) owns `ProductReader`
and the named list/get use cases; its
[composition root](../services/product-service/internal/service/service.go) injects the same use
cases into both transports and owns pool cleanup. The
[PostgreSQL reader](../services/product-service/internal/postgres/reader.go) satisfies the port
without an application dependency on PostgreSQL. Read these examples when adapting the pattern;
ignored ticket artifacts are not required to use this rule.

Generalize dependency, mapping, and ownership principles. Product's read-only behavior, pgx/sqlc
stack, exact timeouts, listener count, anonymous access, and Java schema ownership are service-specific
decisions. Future services must follow their own contracts, security requirements, persistence
ownership, and migration specifications.

## Repository reference and limits

[Product's runtime contracts](../services/product-service/README.md#runtime-contracts-and-boundaries) and
[architecture](../services/product-service/README.md#product-catalog-architecture) illustrate
direct HTTP and gRPC adapters sharing application use cases. Its
[HTTP handler](../services/product-service/internal/http/handler.go) preserves a legacy HTTP
contract independently of the [protobuf schema](../services/product-service/api/product/v1/product_service.proto).
Its [generator configuration](../services/product-service/buf.gen.yaml) currently generates Go
protobuf and gRPC code, not an HTTP gateway or OpenAPI description. This is a reference for explicit
transport separation, not evidence that OpenAPI/gateway generation or validation already exists.

## Python ownership and validation

Python remains auxiliary tooling; business services follow Java-to-Go migration. All four shared
Python rules apply to Python and its callers/configuration/tests, with PY-DEV-01–05, PY-TEST-01–03,
PY-FMT-01 and PY-RUN-01 selected by boundary. Use existing owning Make targets; do not install in
checks or silently fall back to system Python. [CONTRIBUTING](../CONTRIBUTING.md#python-auxiliary-tooling),
[toolchain.mk](../scripts/python/toolchain.mk), [.python-version](../.python-version),
[pyproject.toml](../pyproject.toml) and [uv.lock](../uv.lock) own Ubuntu 24.04 x86_64 provisioning,
CPython 3.14.8, uv 0.13.1, pytest 9.1.1, Ruff 0.17.1 and PyYAML 6.0.3. No application upgrade follows.

`python-runtime-check` verifies operational runtime; `python-toolchain-check` verifies locked dev
dependencies. Both honor explicit `PYTHON` selection across root/owner/recursive calls. Explicit
`python-toolchain-setup` provisions the environment. Use selected-file `python-format`,
`python-format-check`, `python-lint`; the existing owner fast/integration/collection/acceptance
interfaces in CONTRIBUTING retain their declared scope. Framework examples live in the submodule;
consumer example targets and default pytest discovery select that canonical source. Do not format
or modify the dependency to repair a check; change its upstream source through a separate review.

## Legacy Java selection

Select shared Java router/development/formatting/validation and affected architecture/API/test
routes together with [local Java rules](rules/java-rules.md). `styles.java` is `local`.
Google Java Style, its gates and migration are excluded throughout Insurance Hub, including new
and modified Java/tests. Keep existing module formatting, Micronaut, DTO validation, constructor
injection, API/service boundaries, JUnit nested groups, `happyPath` and existing scenario naming.
Campsite-specific Spring/Maven/underscore conventions are not adopted here. JDK/Maven/tool pins
remain in legacy modules and their existing owners. Inspect actual owner commands for each Java
task; no generic successful no-op Java binding is supplied. Java retirement is separate work.

## Operations and command bindings

[Infrastructure rules](rules/infrastructure-rules.md) retain the concrete change-to-check index,
service/role catalogs, runbooks and guarded targets. Apply the
[QA recovery runbook](../k8s/base-cluster-how-tos.md#qa-network-recovery) before diagnosing stale
Flux/DNS/registry state. No profile binding authorizes a live mutation or publication.

`profile.json` declares capability owner/cwd/argv/prerequisites/effects/scope. Commands with
explicit selections or identity arguments require the actual task arguments; placeholders are
not executable values. `snapshot`, strict `review-upstream` and `published-pr` invoke the pinned
framework scripts directly. Export `PYTHON` to the absolute verified consumer interpreter first
(normally `$PWD/.venv/bin/python` from this root); the scripts verify it before work and never
install or silently select a different environment. Strict review
fetches Git metadata only when requested and remains distinct from non-mutating dependency freshness.

Use `make ai-framework-integrity ai-framework-compose` offline after explicit preparation.
`make ai-framework-freshness` queries the configured upstream with a 20-second bound and requires
exact equality. `make ai-check AI_CHECK_TICKET_DIR=<resolved-dir>` retains artifact-aware local
checks and adds canonical source, integrity and explicit Codex/Claude installation checks.
See [adoption instructions](../CONTRIBUTING.md#shared-ai-framework) for setup, upgrade and rollback.
