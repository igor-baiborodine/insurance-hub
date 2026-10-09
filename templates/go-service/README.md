# Go service scaffold

This standalone module is the reference structure for new Insurance Hub Go services. Its module
identity is `github.com/igor-baiborodine/insurance-hub/templates/go-service`. Run every command in
this document from `templates/go-service/` with workspace mode disabled. The module has no root
workspace, filesystem replacements, legacy dependencies, database, broker, or collector.

The scaffold provides a versioned Echo contract, authentic generated Go bindings, typed
configuration, safe structured logging, optional trace export, HTTP lifecycle health, and bounded
signal-driven shutdown.

## Prerequisites

- Go 1.27.2 exactly
- GNU Make 4.3 or newer
- Git for the default changed-file formatting scope; `FORMAT_SCOPE=all` also works outside Git
- GCC on Linux for `test-race`
- Network access when the Go module proxy/checksum service, source-based tool installation, Buf
  Schema Registry dependencies, or `vuln.go.dev` are not already cached

All Go and tooling operations use this module's Makefile. Run module targets from
`templates/go-service/`, or use `make -C templates/go-service <target>` from the repository root.
Tool installation, dependency maintenance, formatting, linting, tests, builds, vulnerability
analysis, and execution set `GOWORK=off`; normal validation uses readonly module resolution and
never depends on a root workspace or filesystem replacement. `GO` may override the Go executable.

`bootstrap-tools` installs these exact binaries under ignored `.tools/bin/`:

- Buf 1.73.0
- protoc-gen-go 1.36.12
- protoc-gen-go-grpc 1.6.2
- golangci-lint 2.14.0
- govulncheck 1.8.0

### Mutating and runtime targets

These operations are explicit and are never hidden inside `check`:

| Target                   | Mutation or side effect                                                                                                    | Prerequisite and network behavior                                                              |
|--------------------------|----------------------------------------------------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------|
| `make bootstrap-tools`   | Installs or replaces only binaries under ignored `.tools/bin/`; it does not change source, generated output, or manifests. | Go 1.27.2 and source/module download access unless cached; finishes by running `verify-tools`. |
| `make update-deps`       | Updates only `go.mod` and `go.sum` to the Makefile's pinned runtime/generated-code dependencies, then tidies.              | Go proxy and checksum access unless cached.                                                    |
| `make update-proto-deps` | Updates only `buf.lock` for the schema dependency declared in `buf.yaml`.                                                  | Verified pinned Buf binary and BSR access unless cached.                                       |
| `make format`            | Rewrites only the selected eligible handwritten Go files through gofumpt, goimports, then golines at 100 columns.          | Verified pinned tools; file selection is described below.                                      |
| `make format-proto`      | Rewrites only `api/scaffold/v1/example_service.proto` with pinned Buf.                                                     | Verified pinned tools.                                                                         |
| `make gen-proto`         | Removes and regenerates only the two declared files under `gen/scaffold/v1/`.                                              | Pinned Buf and generators already installed; BSR access unless cached.                         |
| `make run`               | Starts `./cmd/server`; it changes no repository file.                                                                      | Go 1.27.2; readonly modules; runtime settings below.                                           |

Generated bindings must never be edited by hand. `gen-proto` owns only:

```text
gen/scaffold/v1/example_service.pb.go
gen/scaffold/v1/example_service_grpc.pb.go
```

### Non-mutating validation targets

These targets preserve the module outside ignored `.tools/`. Targets that invoke pinned tools
require `bootstrap-tools` to have been run explicitly.

| Target                      | Coverage                                                                                                                                                                                                                                                                   | Network or other prerequisite                                                                                                                                                      |
|-----------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `make verify-tools`         | Exact Go/tool versions, golangci-lint schema and formatter order, module/import prefix, 100-column settings, readonly loading, all-test lint scope, and vet-equivalent coverage.                                                                                           | All five pinned binaries already installed; no installation.                                                                                                                       |
| `make format-check`         | The same ordered formatter pipeline and selected handwritten files as `format`, reported without rewriting them.                                                                                                                                                           | Verified tools; file selection below.                                                                                                                                              |
| `make check-deps`           | Reproduces `update-deps` in a temporary standalone module and compares `go.mod` and `go.sum`.                                                                                                                                                                              | Go proxy/checksum access unless cached.                                                                                                                                            |
| `make format-proto-check`   | Buf formatting for the one owned schema.                                                                                                                                                                                                                                   | Verified tools.                                                                                                                                                                    |
| `make lint-proto`           | Buf lint for the one owned schema.                                                                                                                                                                                                                                         | Verified tools and locked BSR dependencies.                                                                                                                                        |
| `make check-proto-breaking` | Current schema against `testdata/contract-baseline/` with Buf `FILE` rules. The baseline is fixed and never promoted automatically.                                                                                                                                        | Verified tools and readable checked-in baseline/lock data.                                                                                                                         |
| `make check-proto-drift`    | Exact generated path set and bytes against two isolated regenerations, detecting added, modified, missing, deleted, and non-reproducible output.                                                                                                                           | Verified tools and BSR access unless cached.                                                                                                                                       |
| `make lint`                 | golangci-lint over `./...`, including tests and vet-equivalent analysis; generated, vendor, third-party, fixture, and tool paths are excluded by configuration.                                                                                                            | Verified tools; readonly modules.                                                                                                                                                  |
| `make test`                 | Every package under `./...`, including `cmd/server`, generated consumers, configuration, transport, health, logging, service lifecycle, and telemetry.                                                                                                                     | Go 1.27.2; readonly modules.                                                                                                                                                       |
| `make test-race`            | The same complete package/test scope as `test`, with race instrumentation.                                                                                                                                                                                                 | Go 1.27.2, readonly modules, and a C compiler.                                                                                                                                     |
| `make build`                | Every package under `./...` and an executable `server`, with build output in a removed temporary directory.                                                                                                                                                                | Go 1.27.2; readonly modules.                                                                                                                                                       |
| `make vulncheck`            | Reachability-aware analysis of every package under `./...`.                                                                                                                                                                                                                | Verified govulncheck; access to `vuln.go.dev` unless cached.                                                                                                                       |
| `make check`                | In order: tool/configuration verification, Go and Protobuf format checks, Go and Protobuf lint, race tests, build, dependency reproduction, generation drift/reproducibility, compatibility, and vulnerability analysis. It does not repeat the equivalent non-race suite. | All prerequisites and network access inherited from its checks. It never installs tools, formats, tidies, regenerates real output, updates baselines, starts services, or deploys. |
| `make test-tooling`         | Git-free disposable copies containing controlled Go-format, lint, test, dependency, schema, compatibility, and generated-output defects; each intended gate must reject its defect without mutation.                                                                       | Verified tools; temporary storage and the network/cache prerequisites of nested checks.                                                                                            |
| `make check-copy`           | Source-scaffold maintainer proof that creates a Git-free `services/example-copy`, validates its renamed identities and Make checks, removes scaffold-only copy tooling from the result, compares inventories, and cleans up.                              | Verified source tools, temporary storage, and all public network endpoints listed above. The target is removed from produced service copies.                                       |

Database/broker integration, mock generation, migrations, containers, deployment, workspace
validation, and live infrastructure are not applicable to this standalone scaffold. It has no such
dependencies or placeholder targets.

### Go file selection

`format` and `format-check` share one stable ordered selector:

- `FORMAT_FILES="path/a.go path/b.go"` has highest precedence and accepts only module-relative
  handwritten Go files.
- `FORMAT_SCOPE=all` selects every eligible handwritten Go file and works without Git.
- The default `FORMAT_SCOPE=changed` compares tracked added/copied/modified/renamed files against
  `FORMAT_BASE` when supplied, or against `HEAD`, then includes eligible untracked files.
- An unresolved supplied base fails. A legitimate zero-file selection is reported as zero and does
  not broaden scope.
- Generated headers and paths under `gen/`, `vendor/`, `third_party/`, `testdata/`, and `.tools/`
  are excluded. Escaping the module or selecting an excluded file explicitly fails.

### Setup and normal validation

Bootstrap and refresh owned generated/dependency content explicitly:

```sh
make bootstrap-tools
make update-proto-deps
make gen-proto
make update-deps
make format FORMAT_SCOPE=all
```

Run normal non-mutating validation after tools are installed:

```sh
make check FORMAT_SCOPE=all
make test-tooling
make check-copy
```

### Root scaffold delegates

The repository root exposes only these narrowly scoped delegates. They cover
`templates/go-service` and do not discover or validate future Go modules.

| Root target                        | Owning module target                           |
|------------------------------------|------------------------------------------------|
| `make go-scaffold-bootstrap-tools` | `make -C templates/go-service bootstrap-tools` |
| `make go-scaffold-build`           | `make -C templates/go-service build`           |
| `make go-scaffold-check`           | `make -C templates/go-service check`           |
| `make go-scaffold-test-tooling`    | `make -C templates/go-service test-tooling`    |
| `make go-scaffold-check-copy`      | `make -C templates/go-service check-copy`      |
| `make go-scaffold-run`             | `make -C templates/go-service run`             |

### Continuous integration

[Go modules CI](../../.github/workflows/go-modules.yml) runs on pull requests to `main`
and pushes to `main` when this module, any Go module/workspace manifest, the canonical topology,
topology scripts, the workflow, or the root Makefile changes. It uses `ubuntu-24.04`, checkout v6
with full history, setup-go v7 with exact Go 1.27.2, the nested `go.sum` cache key, and read-only
repository permissions. It requires no secret, database, broker, container runtime, deployment
environment, or telemetry collector.

CI bootstraps the scaffold tools, runs the repository-wide `go-modules-check` and
`go-topology-test`, then retains the scaffold-specific `test-tooling` and `check-copy` proofs. It
invokes only documented Make targets. Pull requests use the base SHA for `FORMAT_BASE`; pushes use
the before SHA. A missing, all-zero, or unresolvable event base visibly falls back to
`FORMAT_SCOPE=all`.

The equivalent full-file local job is:

```sh
make go-scaffold-bootstrap-tools
make go-modules-check FORMAT_SCOPE=all
make go-topology-test
make go-scaffold-test-tooling
make go-scaffold-check-copy
```

A local pass is local equivalent-job evidence. Workflow inspection is configuration evidence. Only
a successful GitHub Actions run on a pushed branch or pull request is hosted-CI evidence.

### Repository topology

The root [Go module topology guide](../../docs/migration/phase-4/go-module-topology.md) defines
repository-wide discovery, resolution, import boundaries, CI coverage, and onboarding. This
scaffold is the only current inventory module. Its supported mode is standalone `GOWORK=off`, it
has no filesystem replacement or cross-module runtime dependency, and the repository has no
approved workspace. Future modules are uncovered until their inventory, owning target, consumers,
CI triggers, and controlled fixtures are reviewed together.

## Create an independently owned service

Follow [COPYING.md](COPYING.md) to produce a renamed service without importing this scaffold's
runtime packages or relying on a workspace or filesystem replacement. The procedure updates module,
runtime, schema, generated-code, and documentation identities together and regenerates protobuf
bindings rather than editing them. It also removes the source-scaffold-only `check-copy` target and
script and replaces root-delegate/CI claims with the copied module's actual onboarding status.

Repository maintainers own this reference. A copied service owns its code and dependencies and
adopts later scaffold fixes through normal review; copies do not update automatically.

## Runtime configuration

Settings are read once from the process environment. An explicitly empty setting is invalid unless
the optional OTLP endpoint is empty while telemetry is disabled. Diagnostics name the setting and
do not repeat its supplied value.

| Setting                       | Default          | Validation                                                                                                                                                                                                                       |
|-------------------------------|------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `SERVICE_NAME`                | `go-service`     | Non-empty; change it when copying the scaffold.                                                                                                                                                                                  |
| `GRPC_ADDR`                   | `127.0.0.1:9090` | Valid host and numeric port; port `0` is available for tests.                                                                                                                                                                    |
| `HEALTH_ADDR`                 | `127.0.0.1:8080` | Valid host and numeric port; port `0` is available for tests.                                                                                                                                                                    |
| `LOG_LEVEL`                   | `info`           | A level accepted by `slog.Level`, such as `debug`, `info`, `warn`, `error`, or a documented level offset.                                                                                                                        |
| `SHUTDOWN_TIMEOUT`            | `10s`            | Positive Go duration.                                                                                                                                                                                                            |
| `OTEL_ENABLED`                | `false`          | Boolean. Disabled mode creates no exporter and needs no collector.                                                                                                                                                               |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | unset            | Required when telemetry is enabled. Use an `http` or `https` OTLP/gRPC URL with a host and numeric port, such as `http://127.0.0.1:4317`; do not append `/v1/traces`. Credentials, query parameters, and fragments are rejected. |
| `OTEL_EXPORTER_OTLP_TIMEOUT`  | `5s`             | Positive Go duration bounding exporter initialization, flush, and shutdown.                                                                                                                                                      |

Logs are JSON and always include `service`. When the supplied context contains a valid span, logs
also include `trace_id` and `span_id`. Common credential, token, request/response payload, customer,
policy, and payment attribute keys are redacted recursively. Callers must still keep sensitive data
out of log messages and deliberately select only safe fields.

Tracing uses W3C trace-context propagation. Enabled mode exports spans over OTLP/gRPC; disabled mode
is a local no-op. The service lifecycle owns the returned provider and must call its shutdown method
with the remaining overall shutdown budget.

## Run and stop the service

Start the executable from this module root:

```sh
make run
```

The owning target disables workspace mode and uses readonly module resolution. Override runtime
settings in the environment when needed; for example, this selects ephemeral loopback ports:

```sh
GRPC_ADDR=127.0.0.1:0 HEALTH_ADDR=127.0.0.1:0 make run
```

The default gRPC listener is `127.0.0.1:9090`. The separate management listener on
`127.0.0.1:8080` exposes only `GET /livez` and `GET /readyz`. Liveness returns 200 while the
management server accepts requests. Readiness returns 200 after both listeners start and 503 once
shutdown begins. It represents lifecycle state only and does not simulate dependency checks.

An interrupt or `SIGTERM` first withdraws readiness and asks gRPC to stop accepting new work while
in-flight RPCs drain. Half of `SHUTDOWN_TIMEOUT` is reserved for graceful gRPC drain. The remaining
budget covers forced gRPC stop when required, management shutdown, and telemetry cleanup;
management remains reachable during the drain so `/readyz` can report 503. The executable then
closes management HTTP and telemetry within the original overall deadline.

## Example RPC

The generated `scaffold.v1.ExampleService` exposes unary `Echo`. A valid request contains a UTF-8
message from 1 through 128 bytes, and the response returns the same bytes. Protovalidate enforces
the schema at the gRPC boundary before the Echo handler runs. Empty or oversized input returns
`InvalidArgument`; cancellation and deadlines keep their gRPC status; unexpected handler failures
return `Internal` with the stable message `internal error`.

The transport logs neither request/response bodies nor underlying failure details. Its server
stats handler uses the explicitly supplied tracer provider and W3C propagator, while metrics remain
disabled through an explicit no-op meter provider. Reflection, profiling, authentication, and
production exposure are not enabled by this scaffold.
