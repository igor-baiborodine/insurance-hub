<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Servicekit](#servicekit)
  - [Package boundary](#package-boundary)
  - [Public API](#public-api)
  - [Owning commands](#owning-commands)
  - [Release](#release)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Servicekit

`github.com/igor-baiborodine/insurance-hub/libs/servicekit` is the independently versioned runtime
library for Insurance Hub Go services. It provides configuration parsing, neutral identity, health
probes, structured logging and OpenTelemetry mechanics. Insurance Hub repository maintainers own
it. Product and the service scaffold are its active consumers and pin published releases.

Each consumer composes the runtime objects it needs and keeps policy, lifecycle ordering and
cleanup ownership. No package reads the environment or initializes process state on import.

## Package boundary

| Package | Responsibility | Remains with the consumer |
| --- | --- | --- |
| `config` | Explicit parsing and safe validation of neutral setting groups | Environment loading, service-specific settings and startup validation |
| `identity` | Neutral identity values and opt-in deployment validation | Identity loading and instance generation |
| `logger` | JSON handlers, recursive attribute redaction and trace correlation | Message wording, severity and business classification |
| `telemetry` | Explicit provider construction, runtime instruments and bounded cleanup | Scope, view and propagation choices, global installation and business spans |
| `health` | Readiness state and bounded injected dependency checks | Dependencies, readiness transitions and resource lifetime |

The library contains no business models, DTOs, repositories, database schemas, server executable
or generated contracts. `check-boundaries` rejects imports of other repository modules and
additional public package roots.

## Public API

- **`config`:** `Runtime`, `DefaultRuntime`, `DefaultGRPC`, `DefaultHTTPServer` and
  `DefaultTelemetry` return fresh value-only defaults. The defaults are business HTTP
  `127.0.0.1:8080`, health `127.0.0.1:8081` and gRPC `127.0.0.1:9090`. A service's own loader
  supplies a `config.Lookup` such as `os.LookupEnv`, parses settings with helpers like
  `LoadAddress`, `LoadPositiveDuration` and `OptionalEndpoint`, and then calls `ValidateRuntime`,
  `ValidateTelemetry` and its own validation. `ValueOrDefault` falls back only when a value is
  absent; an explicitly empty value fails. Parse errors carry only schema names and fixed reasons.
  `ErrorDetails` extracts them without rendering wrapped error text.
- **`identity`:** `Identity` is a plain value with namespace, service, version, instance,
  environment and implementation. `ValidateDeployment` is an explicit policy for services that use
  Product's deployment identity rules. It returns `ValidationError` with fixed setting and reason
  fields.
- **`health`:** `NewState(timeout, check)` starts not ready. The composition root calls
  `SetReady(true)` after startup and `SetReady(false)` before drain. `NewHandler` serves `GET /livez`
  (always 200) and `GET /readyz`. The readiness probe returns 503 before startup, on dependency
  failure or cancellation, or after readiness is withdrawn during the probe. Probe bodies are
  fixed and never include dependency errors. `NewManagementServer` returns an unstarted
  `*http.Server` with header and idle timeouts. The caller owns its listener, exposure and shutdown.
- **`logger`:** `New` and `NewIdentity` create JSON loggers with UTC timestamps, trace and span
  correlation, and recursive redaction of sensitive attributes. Messages are not rewritten.
  `LogRequired` writes a caller-owned contract record that bypasses the diagnostic level.
- **`telemetry`:** `New` builds providers from validated `config.Telemetry` and explicit `Options`.
  `RuntimeScope` is required, and propagators, views and the sampler are caller choices. Disabled
  telemetry creates no exporter or connection. Enabled telemetry uses OTLP/gRPC with bounded queues,
  batching, retry and export timeouts. `ForceFlush` and idempotent `Shutdown` honor the shorter of
  the caller context and the exporter timeout. `InstallDiagnostics` is the only process-global
  action. It returns a cleanup that callers must run in LIFO order after providers shut down. The
  library never installs a global tracer provider, meter provider or propagator.

The OpenTelemetry packages are pinned at v1.46.0. The library has no B3 or transport
instrumentation dependency. Package documentation covers parsing rules, defaults and limits in
detail.

## Owning commands

Run commands from the repository root after `make python-toolchain-setup`:

```sh
make -C libs/servicekit bootstrap-go-tools
make -C libs/servicekit check-go-pr FORMAT_SCOPE=all
make -C libs/servicekit coverage
```

Prerequisites are Go 1.27.4, managed CPython 3.14.8, GCC for race tests, and access to public Go,
tool and vulnerability services unless they are cached. `bootstrap-go-tools` installs
golangci-lint v2.14.0 and govulncheck v1.8.0 under `.tools/bin`; checks never install tools.
`check-go-pr` covers tool pins, formatting, lint, race tests, build, dependency consistency,
vulnerabilities and the package boundary. `format` and `update-deps` are the explicit mutating
targets. Shared PR CI also validates every active consumer against a Servicekit change, and uploads
coverage to Codecov under the `servicekit` flag.

## Release

Servicekit releases through the shared
[Go library release workflow](../../docs/migration/phase-4/go-module-topology.md#shared-library-validation-and-release)
with tags `libs/servicekit/vX.Y.Z`. A release validates the library and every active consumer at
the exact source, creates the tag, and verifies public resolution through `proxy.golang.org` and
`sum.golang.org` before creating the GitHub Release. A tag alone does not establish a successful
release. Before upgrading a consumer, check the hosted run, the tag's source SHA and public module
resolution.

Library versions are independent of service images. During v1, minor and patch releases preserve
backward compatibility. A breaking change requires a separately reviewed major release with
migration notes covering affected APIs, settings and consumers, and a working rollback target.
Consumers pin exact versions with normal Go checksums and no local replacement, and upgrade
through their own PRs. Rollback restores the previous pin in that consumer's PR and reruns its full
gate. The release workflow never moves a tag.
