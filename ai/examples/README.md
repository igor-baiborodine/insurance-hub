<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [AI Examples](#ai-examples)
  - [Go testing examples](#go-testing-examples)
  - [Python testing examples](#python-testing-examples)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# AI Examples

Use this directory for committed examples that demonstrate preferred ticket content (specifications), delivery plans, verification notes, or implementation patterns.

Examples should be realistic, concise, and clearly labeled as examples. Store ticket working
artifacts in the resolved `<ticket-dir>` under `ai/artifacts/`, following
[AGENTS.md Artifact Rules](../../AGENTS.md#artifact-rules), rather than in this examples directory.

## Go testing examples

Start with the [Go testing guide](go-testing/README.md), then load only the relevant examples:

| Pattern                                            | Example                                                                  |
|----------------------------------------------------|--------------------------------------------------------------------------|
| Deterministic boundaries and environment isolation | [Unit tests](go-testing/unit-tests.md)                                   |
| Generated mocks, wrapped errors, and cancellation  | [Application tests](go-testing/application-tests.md)                     |
| Transaction failure injection                      | [SQL adapter tests](go-testing/sql-adapter-tests.md)                     |
| Real migrations and optimistic concurrency         | [PostgreSQL integration tests](go-testing/postgres-integration-tests.md) |
| Real gRPC middleware and wire responses            | [gRPC tests](go-testing/grpc-tests.md)                                   |

## Python testing examples

Start with the [Python testing guide](python-testing/README.md). Its executable CLI, fast pytest
suite, and explicitly selected disposable integration suite demonstrate the canonical Python
development, testing, formatting, and validation rules without defining business behavior.
