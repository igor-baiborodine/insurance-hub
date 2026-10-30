<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Servicekit](#servicekit)
  - [Package boundary](#package-boundary)
  - [Configuration and identity](#configuration-and-identity)
  - [Health probes and ownership](#health-probes-and-ownership)
  - [Logging and telemetry](#logging-and-telemetry)
  - [Owning commands](#owning-commands)
  - [Resolution and registration](#resolution-and-registration)
  - [Release](#release)
  - [Compatibility and handoff](#compatibility-and-handoff)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Servicekit

`github.com/igor-baiborodine/insurance-hub/libs/servicekit` is the independently versioned
runtime library owned by Insurance Hub repository maintainers. Igor Baiborodine is the initial
accountable release maintainer. Configuration, neutral identity, health, logging and telemetry
mechanics are implemented. Product and the scaffold are active consumers that pin published
releases with normal Go checksums and no filesystem replacement.

## Package boundary

| Public package | Intended responsibility | Remains with the consumer |
| --- | --- | --- |
| `config` | Explicit parsing and safe validation of neutral setting groups | Environment loader, required database settings, policy and startup validation |
| `identity` | Neutral identity values, development defaults and opt-in deployment validation | Identity loading, test-instance generation and the scaffold's separate policy |
| `logger` | JSON handlers, recursive attribute redaction and trace correlation | Completion status, severity, route and business classification |
| `telemetry` | Explicit provider construction, runtime instruments and bounded cleanup | Scope/view/propagation selection, process-global installation, transport and business spans |
| `health` | State and bounded injected readiness checks | Dependencies, readiness transition and resource lifetime |

The library contains no business models, DTOs, repositories, use cases, database schemas, server
executable or generated contracts. `check-boundaries` rejects imports of other repository modules
and additional public package roots. Source/API review must also reject business declarations
that require no prohibited imports. The owning service composes runtime objects and retains their
cleanup ownership.

## Configuration and identity

`config.Runtime` contains value-only request, probe and shutdown timeouts plus the diagnostic log
level. It supports named composition or embedding, but has no promoted loading or validation
method. A service's `internal/config.Load` supplies `config.Lookup` (normally `os.LookupEnv`),
explicitly parses settings and invokes `config.ValidateRuntime`, `config.ValidateTelemetry` and
its own validation before creating runtime dependencies. No package reads the environment or
initializes process state on import.

`DefaultRuntime`, `DefaultGRPC`, `DefaultHTTPServer` and `DefaultTelemetry` return fresh defaults.
The address defaults are business HTTP `127.0.0.1:8080`, health `127.0.0.1:8081` and gRPC
`127.0.0.1:9090`. Address constants do not create listeners or a business API. `LoadAddress`
preserves explicit overrides, IPv6 and port zero; callers use `Address.Port` to apply their own
collision policy. The scaffold uses the shared health default.

`ValidHost` exposes the host-syntax check used by listener and OTLP endpoint parsing. Pass an
unbracketed host without a port or URL text. It accepts IP literals and ASCII DNS-style names
with labels up to 63 bytes and a total length up to 253 bytes; it rejects empty hosts, ASCII
space/tab/CR/LF, empty labels, leading or trailing label hyphens and other characters. It
returns a boolean without including rejected input in diagnostics and performs no DNS lookup.
Product's PostgreSQL URL parsing and credential-redaction policy remain in Product; it calls this
predicate only for the URL host.

`ValueOrDefault` uses a fallback only when a value is absent; an explicitly empty value fails.
`LoadPositiveDuration`, `LoadBoundedInt`, `ParseLogLevel` and `ParseBool` retain the consumer
parsing semantics. `OptionalEndpoint` is the deliberate exception: absent or empty means no
endpoint when export is disabled, and fails when enabled. Nonempty endpoints are validated even
when disabled. OTLP/gRPC endpoints require HTTP(S), a host and nonzero numeric port; credentials,
query values, fragments and OTLP/HTTP paths are rejected. A trailing slash is normalized away.

Parsing errors retain only schema names and fixed reasons. All name arguments and both arguments
to `Invalid` must be trusted schema text, never environment values or raw parser errors.
`ErrorDetails` extracts those fields through wrappers and returns a constant category for unknown
errors; it never renders the wrapper. It is not a general sanitizer for arbitrary message text.

`identity.ValidateDeployment` is an explicit policy for services using the Product deployment
identity rules. It validates the caller's expected name when telemetry is enabled, accepts
development only with telemetry disabled, and test identities under their existing rules. It applies
a SHA-version and UUID-shaped instance rule to every other valid lowercase environment name.
It does not load or generate values. Identity failures expose fixed setting/reason fields through
`identity.ValidationError`; `config.ErrorDetails` extracts them through wrappers without
rendering arbitrary error text. The scaffold retains its own identity policy.

`identity.Identity` is a plain value containing namespace, service, version, instance, environment
and implementation. It has no promoted validator or instance generator. `ValidateTelemetry` checks a
positive timeout and endpoint mechanics, without constraining identity or enforcing Product's
enabled exactly-2s timeout. Product retains required database settings and credential redaction,
pool bounds, QA/test identity, timeout relationships and listener collision rules. The scaffold
retains its explicit non-development identity policy and ephemeral-listener behavior. These
policies must still be called explicitly by each consumer; passing common validation is not a
complete service validation result. Treat parsed settings, including endpoint URLs, as immutable
after startup.

## Health probes and ownership

`health.NewState(timeout, check)` rejects nonpositive budgets and nil checks and starts not ready.
The composition root calls `SetReady(true)` only after startup and `SetReady(false)` before drain.
`Ready` reports lifecycle state only; `/readyz` also checks dependencies. The injected `Check` may
compose several dependencies and must honor cancellation and return promptly. It runs synchronously
under the shorter of the caller deadline and probe budget; the library creates no detached worker
to hide a check that ignores cancellation. Clients and their cleanup remain caller-owned.

`health.NewHandler` accepts a state returned by `NewState`. A `GET /readyz` returns 503 before
startup, on dependency failure or cancellation, or after readiness withdrawal during the probe;
bounded success returns 200. It rechecks cancellation and state after the dependency returns.
`GET /livez` always returns 200 independently of dependencies. Bodies are fixed `live\n`,
`ready\n` and `not ready\n`, with `text/plain; charset=utf-8` and `Cache-Control: no-store`.
The standard HTTP mux provides method/path rejection. Dependency errors are never returned in
responses. Listener creation, authentication/exposure policy, startup rollback and the overall
shutdown budget remain with the service composition root.

`health.NewManagementServer(state)` optionally returns a fresh, unstarted `*http.Server` with
the same probe handler, a 5-second header timeout and a 30-second idle timeout. Read and write
timeouts remain zero. A service may set different timeout fields before serving; it still owns
the listener address and exposure, readiness transitions, serving and shutdown.

## Logging and telemetry

`logger.New` creates a JSON logger with a short service label. `logger.NewIdentity` binds the six
caller-owned identity fields. Both use UTC timestamps, add valid trace/span IDs from the logging
context and recursively redact sensitive attribute keys in groups, maps, structs and lists.
Errors and unsupported structured values are replaced with `[REDACTED]`. Bound attributes are
sanitized when the logger is derived. Attribute redaction does not inspect or rewrite arbitrary
message strings; callers must keep messages safe.

`logger.LogRequired` bypasses the diagnostic threshold for a caller-owned contract record when
the logger comes from `logger.New` or `logger.NewIdentity`, including `With` and `WithGroup`
derivatives. A logger using another `slog.Handler` may filter the record. Its message, severity and
classification remain explicit arguments, so completion wording and Product/route/business
policy stay in the consumer. Migrate a consumer's logger constructor and required-record call
together to retain the bypass. Logs are written only to the supplied writer; executables select
stdout or stderr.

`telemetry.New` accepts caller-validated `config.Telemetry` and explicit `telemetry.Options`.
`RuntimeScope` is required. HTTP/gRPC propagators, metric views and the trace sampler are caller
choices; nil propagators use W3C trace context and a nil sampler uses parent-based always-sample.
`HistogramView` preserves caller-owned signal names and buckets. Product retains its W3C+B3 HTTP
policy, W3C-only gRPC policy, business/database views and transport completion behavior. The
scaffold retains its own request signal names. This module intentionally has no B3 or transport
instrumentation dependency.

Disabled telemetry creates no exporter, reader, callback or network connection and returns no-op
providers while retaining the selected propagators. Enabled telemetry validates all six resource
identity values before exporter startup, uses OTLP/gRPC, cumulative metrics, caller-bounded export
timeouts, a 2048-span nonblocking queue, 256-span batches, one-second batching and bounded retry.
Runtime instruments cover memory, allocation, GC, goroutine and processor state under the exact
caller scope. Provider construction closes already-created resources when a later exporter or
runtime step fails. `ForceFlush` and idempotent `Shutdown` use the shorter of the caller context
and exporter timeout; the composition root retains the overall lifecycle budget.

`telemetry.InstallDiagnostics` is an explicit process-global action. It replaces raw SDK errors
with fixed, rate-limited warnings and returns an idempotent cleanup that restores the previous
handler and detaches the supplied logger. Installations must be restored in LIFO order after
providers have shut down. On the first installation, OpenTelemetry's default handler retains a
delegate permanently; after cleanup it emits only a fixed, rate-limited fallback warning without
using the released logger. The library does not install a global tracer provider, meter provider
or propagator.

## Owning commands

Run from this directory or use `make -C libs/servicekit` from the repository root. Prerequisites
are Go 1.27.3, managed CPython 3.14.8, GCC for race tests, and access to public Go/tool/vulnerability
services unless cached. Prepare Python through the root `python-toolchain-setup` target first.

```sh
make bootstrap-go-tools
make format FORMAT_SCOPE=all
make check-go-pr FORMAT_SCOPE=all
```

Setup explicitly installs golangci-lint v2.14.0 and govulncheck v1.8.0 under `.tools/bin`.
Checks never install tools. `TOOLS_BIN`, `GO` and `PYTHON` support explicit compatible overrides.
`test TEST_PACKAGES=./config` selects unit packages; `test-race` selects all packages.
`build` compiles packages without assuming an executable. `check-deps` tidies only a disposable
copy and compares both manifests. `update-deps` is the explicit manifest-maintenance operation.
Formatting reuses the repository's gofumpt/goimports/golines pipeline and 100-column policy.
The gate includes format, lint/vet, race, build, dependency, vulnerability and boundary checks.
The tests cover parsing, safe failures, default ports, explicit local validation, real HTTP probes,
deadline/cancellation, readiness withdrawal, recursive logging redaction, trace correlation,
resource/scope/view preservation, disabled mode, bounded export queues and partial-startup cleanup.
Identity is a value-only package. These checks do not establish consumer adoption, transport
final-status behavior or complete service startup/shutdown behavior.

The OTel packages are pinned at v1.46.0, matching current consumers. Both consumers retain
otelgrpc v0.71.0 because transport instrumentation remains local.

## Resolution and registration

Normal resolution is standalone with `GOWORK=off`, exact version requirements, checked-in checksums
and no servicekit replacement. The library owner and shared workflow inspect effective workspace
and caller/persistent flags before setting standalone flags; alternate manifests and servicekit
replacements fail. The active consumers enforce exact versions and normal checksums. A tag alone
is not eligible for a consumer upgrade.

Topology schema 2 retains one module inventory and adds `activeConsumers`. The library's
`consumers` allowlist and `activeConsumers` registry include Product and the scaffold. Registration
cross-checks requirements/imports, owner/directory identity, workflow jobs, unique phases and
explicit Make bindings. A still-required/imported dependency cannot be hidden by deleting or
downgrading its record. Every active registration implements the baseline phase adapters plus its
applicable generated, breaking, copy, integration and image phases. Unsupported phases fail closed.

From the repository root, `go-shared-plan` takes full `PR_BASE` and `PR_HEAD` SHAs and checks the
complete diff, including both ends of renames and the union of base/head module roots. More than
one changed module fails before setup. Shared changes select every active consumer against the
candidate; root tooling changes conservatively select all modules normally. The supplemental
workflow also validates auxiliary module edits. The original Go PR and Report Card workflows
remain unchanged. The supplemental gate currently repeats a normal gate if both workflows are
triggered; it never substitutes an unverified external workflow result for a required phase.

`go-shared-validate` uses a verified Git extraction in a private temporary directory, with
alternate manifests only in disposable copies. A live private socket issues phase contexts;
normal flags or a copied context file cannot enable candidate mode. Each simple candidate phase
uses the actual selected graph before/after execution. Composite owning targets may report
several distinct phases in one invocation. Original manifests/source, alternate files, source
content and full candidate SHA are checked. The controller removes its temporary tree on success,
failure and handled interruption. Hard-killed runners require disposal. The scaffold supplies its
generated, breaking and copy adapters; Product supplies generated, breaking, integration and image
adapters. Both registrations declare why their inapplicable phase is omitted.

The module work budget is 40 minutes; child processes have 30-second progress heartbeats and
10-second teardown, with separate controller shutdown. Reports retain original/alternate lockfile
hashes, source identity, selected graph and separate phase outcomes. The aggregate recomputes
expected obligations and rejects missing, duplicate, unexpected, failed, cancelled, skipped or
misidentified results. Zero consumers is explicit and is not adoption coverage. Planning/results
are stored under ignored `.work/servicekit`; temporary overrides never live there.

## Release

The **Go Library Release** workflow runs after a `libs/servicekit` change is pushed to
`main`, including a direct push. Monotag calculates a scoped stable v0 version from
conventional commits and existing library tags, starting at `v0.1.0`. Because the Go
module is in `libs/servicekit`, its tag uses `libs/servicekit/vX.Y.Z`. The single release
job validates the triggering source SHA and active consumers, creates the scoped tag, verifies
public Go proxy and checksum resolution with fresh standalone and container builds, then
creates a non-draft GitHub Release with notes marked Latest. Release creation uses the same
action and settings as Go service releases.

The workflow needs no separate approval, release-immutability setting, qualification asset,
attestation, package upload, consumer pin edit or service-image release. Its job has
`contents: write` and module-wide concurrency without cancellation. Tag creation is
create-if-absent with exact SHA readback; a collision fails, while the same source may resume
verification. A corrected source uses a new version.

Public verification uses an empty dependency/build cache, `proxy.golang.org`,
`sum.golang.org`, exact selected-version and source-content checks, and standalone plus
fresh-container smoke builds. Its overall limit is 15 minutes, proxy requests are bounded
to 30 seconds and image cleanup has a separate 30-second budget. Only transient proxy
availability is retried; checksum failure is fatal. A tag may remain downloadable after
a failed run. Before a consumer upgrade, inspect the hosted run, tag/source SHA and public module
resolution. Consumers pin exact versions, retain normal Go checksums and run their owning
build/test gates. Local tests do not establish hosted publication.

## Compatibility and handoff

Library versions are independent of service images. During v0, patch versions preserve API and
documented behavior. Breaking minor releases require maintainer-reviewed migration notes naming
old/new versions, affected settings/APIs/consumers, before/after behavior, required checks and a
previously working rollback target with reversal limits. Consumers explicitly pin and verify exact
versions; automatic minor upgrades are unsupported. Rollback restores the previous pin
and necessary source/configuration changes in that consumer's own PR and reruns its full gate.
The release workflow never moves a tag. Both active consumers have completed compatibility
validation; a v1 readiness review and v1 publication still require separate approval.

Root validation owners are `go-shared-test`, `go-shared-test-integration`, `go-shared-check-ci`,
`go-topology-check`, `go-topology-test` and the existing Python shared-tooling suites. The additive
workflow syntax check uses Product's existing pinned Actionlint 1.7.7 binary; prepare it through
`make -C services/product-service bootstrap-ci-tools` when absent. Future releases repeat the
applicable hosted release and normal consumer/image qualification gates.
