# Go Testing Examples

These are **Markdown adaptation templates**, not an executable service or Insurance Hub business
specification. Each example states the production API it assumes. Replace the illustrative
`example.com/policy` module path, types, fixtures, and expectations with the ticket's real contracts.
Do not implement a new business rule just to make an example applicable. No generated mock or
production implementation is supplied, and the snippets have not been compiled or runtime-tested.

Apply the [Go development](../../rules/go-development.md),
[formatting](../../rules/go-formatting.md), and [validation](../../rules/go-validation.md) rules.
Use the module's pinned Go and library versions; verify API compatibility before adaptation.

## Choose the test boundary

| Example                                                 | What it proves                                                    | What it cannot prove                                                     |
|---------------------------------------------------------|-------------------------------------------------------------------|--------------------------------------------------------------------------|
| [Unit tests](unit-tests.md)                             | Pure boundary rules, deterministic dates, config parsing          | Persistence or transport wiring                                          |
| [Application tests](application-tests.md)               | Port arguments, results, error causes, cancellation               | Real storage, network deadlines, authorization middleware                |
| [SQL adapter tests](sql-adapter-tests.md)               | Handling of begin/execute/commit failures and rollback            | Valid SQL, database isolation, real constraints                          |
| [PostgreSQL integration](postgres-integration-tests.md) | Actual migrations, stored state, stale-write rejection            | Cross-service behavior or simultaneous-writer races                      |
| [gRPC tests](grpc-tests.md)                             | Serialization, configured middleware, status and response mapping | Database correctness, deployed TLS, or a complete migration parity suite |

## Before adapting

1. Read the ticket and actual implementation. Use real acceptance criteria and legacy parity cases;
   these examples' policy fields and date rules are fictional.
2. Choose the smallest useful boundary. Use an external `_test` package for public behavior; use
   the package itself when adapter internals are the intended boundary. Keep unit tests untagged
   unless the module requires otherwise; reserve `integration` for tests needing that explicit gate.
3. Use fixed data and independent expected results. Do not build the expected response by calling
   the production mapper being tested. Use `require` for prerequisites before dereferencing values,
   and `assert` for independent comparisons. Match wrapped errors by cause/type, not whole strings.
4. Allocate mocks and mutable fixtures per subtest. Parallelize only tests without shared process
   state or shared storage. Use `t.Setenv`, `t.TempDir`, `t.Helper`, and `t.Cleanup` where appropriate.
5. Register resource cleanup immediately, including partial setup failure paths. Send worker errors
   to the test goroutine; do not call `Fatal` or `require` from workers. Use channels for ordering
   and timeouts only as failure bounds, not sleeps as synchronization.

These practices use Go's [test lifecycle and isolation APIs](https://pkg.go.dev/testing) and
Testify's [fatal assertions](https://pkg.go.dev/github.com/stretchr/testify/require).

## Execute through Make

Inspect the owning Makefile before running anything. Once the service defines these capabilities,
use its documented equivalents of `make gen-mock`, `make format`, `make format-check`, `make lint`,
`make test`, and `make test-integration`. Run a race-enabled target for concurrency changes.
Generation/dependency installation also goes through Make; do not call underlying tools directly.

These target names are illustrative: no service test targets are supplied by this examples folder.
Add missing targets within the implementation ticket's scope or report the missing capability.
Integration targets must enable the integration build tag, provision disposable dependencies, and
bound the overall run. A missing container runtime is a failed prerequisite, not a silently skipped
test. Record executed targets, scope, outcomes, and unrun checks in the ticket artifacts.

## Source and deliberate adaptations

The local `campsite-booking-go` reference was inspected at commit `24594b4`. Relevant paths:

- `internal/application/validator/booking_validators_test.go` and
  `internal/application/query/get_booking_test.go`: table cases and mocked repository ports.
- `internal/postgres/campsite_repository_test.go`: sqlmock transaction failure paths.
- `internal/postgres/booking_repository_integration_test.go` and
  `internal/testing/bootstrap/testcontainers.go`: real PostgreSQL and migration setup.
- `internal/grpc/server_test.go` and `server_integration_test.go`: mapping and real gRPC calls.
- `internal/config/config_test.go`: environment-driven configuration.

The examples preserve those useful boundaries but use fixed dates, per-subtest mocks, strict
argument/call-count expectations, cleanup-backed environment changes, ephemeral ports, RPC
deadlines, and cleanup registered during setup. They avoid reference-test patterns such as raw
environment mutation, error-text substring matching, fixed test ports, or fatal assertions in
server goroutines. Agents do not need a sibling checkout to read these examples.
