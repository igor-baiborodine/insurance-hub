# Go service scaffold

This standalone module is the reference structure for new Insurance Hub Go services. Its module
identity is `github.com/igor-baiborodine/insurance-hub/templates/go-service`. Run every command in
this document from `templates/go-service/` with workspace mode disabled. The module has no root
workspace, filesystem replacements, legacy dependencies, database, broker, or collector.

Issue 119 builds the runnable reference and its copy workflow incrementally. This first delivery
step establishes the versioned Echo contract and authentic generated Go bindings. Runtime wiring,
validation, lifecycle behavior, telemetry, tests, and the complete copy procedure follow in later
steps of the same issue.

## Prerequisites

- Go 1.27.1
- GNU Make 4.3 or newer
- Network access for initial Go module and Buf Schema Registry downloads

The Makefile owns four setup and generation operations. None is an implicit prerequisite of
another target:

| Target | Purpose and mutations |
| --- | --- |
| `make bootstrap-tools` | Installs Buf 1.73.0, protoc-gen-go 1.36.12, and protoc-gen-go-grpc 1.6.2 into ignored `.tools/bin/`. It uses the Go module cache and network when artifacts are not cached. |
| `make update-proto-deps` | Resolves the Protovalidate schema commit declared in `buf.yaml` and updates only `buf.lock`. It requires the pinned local Buf binary and BSR access unless cached. |
| `make gen-proto` | Verifies all local tool versions and regenerates `gen/scaffold/v1/example_service.pb.go` and `gen/scaffold/v1/example_service_grpc.pb.go`. It may fetch declared schemas unless cached; it does not install tools or change dependency manifests. |
| `make update-deps` | Updates only `go.mod` and `go.sum` for generated-code imports at the pinned versions, then tidies the module. It can use the network and Go module cache. |

Bootstrap and generate the contract in this explicit order:

```sh
GOWORK=off make bootstrap-tools
GOWORK=off make update-proto-deps
GOWORK=off make gen-proto
GOWORK=off make update-deps
```

The generators are resolved by explicit paths under `.tools/bin/`; changing the process `PATH` is
not required. `gen-proto` cleans its configured `gen/` output before generation, so generated files
must never be edited by hand. The checked-in schema keeps its Go package identity explicit and Buf
managed mode is disabled.

The broader formatting, lint, security, drift, and CI interface belongs to issue 121. Repository
module/workspace topology enforcement belongs to issue 123.
