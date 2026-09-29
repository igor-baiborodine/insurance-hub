# AI Examples

Use this directory for committed examples that demonstrate preferred ticket content (specifications), delivery plans, verification notes, or implementation patterns.

Examples should be realistic, concise, and clearly labeled as examples. Do not store ticket working artifacts here; use `ai/artifacts/<ticket>/` instead.

## Go testing examples

Start with the [Go testing guide](go-testing/README.md), then load only the relevant examples:

| Pattern                                            | Example                                                                  |
|----------------------------------------------------|--------------------------------------------------------------------------|
| Deterministic boundaries and environment isolation | [Unit tests](go-testing/unit-tests.md)                                   |
| Generated mocks, wrapped errors, and cancellation  | [Application tests](go-testing/application-tests.md)                     |
| Transaction failure injection                      | [SQL adapter tests](go-testing/sql-adapter-tests.md)                     |
| Real migrations and optimistic concurrency         | [PostgreSQL integration tests](go-testing/postgres-integration-tests.md) |
| Real gRPC middleware and wire responses            | [gRPC tests](go-testing/grpc-tests.md)                                   |
