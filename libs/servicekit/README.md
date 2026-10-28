<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Servicekit](#servicekit)
  - [Package boundary](#package-boundary)
  - [Configuration and identity](#configuration-and-identity)
  - [Health probes and ownership](#health-probes-and-ownership)
  - [Owning commands](#owning-commands)
  - [Resolution and registration](#resolution-and-registration)
  - [Release and qualification](#release-and-qualification)
  - [Compatibility and handoff](#compatibility-and-handoff)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Servicekit

`github.com/igor-baiborodine/insurance-hub/libs/servicekit` is the independently versioned
runtime library owned by Insurance Hub repository maintainers. Igor Baiborodine is the initial
accountable release maintainer. Configuration, neutral identity and health mechanics are
implemented. Logging and telemetry provider extraction remain pending. Neither Product nor the
scaffold currently depends on this module.

## Package boundary

| Public package | Intended responsibility | Remains with the consumer |
| --- | --- | --- |
| `config` | Explicit parsing and safe validation of neutral setting groups | Environment loader, required database settings, policy and startup validation |
| `identity` | Neutral identity values | Product QA/test and scaffold identity policies |
| `logger` | JSON handlers, recursive attribute redaction and trace correlation | Completion status, severity, route and business classification |
| `telemetry` | Explicit provider construction, runtime instruments and bounded cleanup | SDK ownership, scopes, views, propagation and business spans |
| `health` | State and bounded injected readiness checks | Dependencies, readiness transition and resource lifetime |

The library contains no business models, DTOs, repositories, use cases, database schemas, server
executable or generated contracts. `check-boundaries` rejects imports of other repository modules
and additional public package roots. Source/API review must also reject business declarations
that require no prohibited imports. The implemented packages use the standard library and the
module's own `identity` package. The owning service composes runtime objects and retains their
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
collision policy. Scaffold adoption will change its existing health default in its separate PR.

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

`identity.Identity` is a plain value containing namespace, service, version, instance, environment
and implementation. It has no identity validator or generator. `ValidateTelemetry` checks a
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
The config and health tests cover parsing, safe failures, default ports, explicit local validation,
real HTTP probes, deadline/cancellation and readiness withdrawal. Identity is a value-only package;
logger and telemetry remain documentation-only. These checks do not establish consumer adoption
or complete service startup/shutdown behavior.

The OTel pins reserved for extraction are v1.46.0 and otelgrpc v0.71.0, matching current consumers.
Do not add unused runtime dependencies merely to populate `go.sum`; it is intentionally empty
until implementation requires dependencies.

## Resolution and registration

Normal resolution is standalone with `GOWORK=off`, exact version requirements, checked-in checksums
and no servicekit replacement. The library owner and shared workflow inspect effective workspace
and caller/persistent flags before setting standalone flags; alternate manifests and servicekit
replacements fail. The existing consumers gain their own normal-admission hooks in their adoption
PRs. A tag alone is not eligible for adoption.

Topology schema 2 retains one module inventory and adds `activeConsumers`. The library's
`consumers` allowlist permits future Product and scaffold adoption; `activeConsumers: []` records
that neither is adopted. Registration cross-checks requirements/imports, owner/directory identity,
workflow jobs, unique phases and explicit Make bindings. A still-required/imported dependency
cannot be hidden by deleting or downgrading its record. Each adoption PR must implement every
baseline phase adapter before registration, including generated/breaking checks and applicable
copy, integration and image phases. Unsupported phases fail closed.

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
failure and handled interruption. Hard-killed runners require disposal. Consumer-specific
generation, integration, copy and image adapters remain adoption work and cannot pass yet.

The module work budget is 40 minutes; child processes have 30-second progress heartbeats and
10-second teardown, with separate controller shutdown. Reports retain original/alternate lockfile
hashes, source identity, selected graph and separate phase outcomes. The aggregate recomputes
expected obligations and rejects missing, duplicate, unexpected, failed, cancelled, skipped or
misidentified results. Zero consumers is explicit and is not adoption coverage. Planning/results
are stored under ignored `.work/servicekit`; temporary overrides never live there.

## Release and qualification

The **Go Library Release** workflow is explicitly dispatched from `main` with a canonical stable
v0 version and full independently reviewed merged SHA. Start at `v0.1.0` and tag
`libs/servicekit/v0.1.0` only after the complete runtime library has merged and release publication
has been authorized. The workflow fixes control tooling to the dispatch SHA, verifies main
ancestry and approving review evidence, and validates the exact source plus active consumers.

The `servicekit-release` environment must have independent required reviewers, self-review
prevention and no admin bypass. Repository release immutability must be enabled. The publication
jobs verify these settings; inability to read them blocks publication. Configure settings and
credential permissions separately before the first authorized hosted run. CI requires GitHub CLI
with release and artifact-attestation verification (locally qualified with 2.103.0).
If `GITHUB_TOKEN` cannot inspect protection settings, provision the protected environment secret
`SERVICEKIT_RELEASE_INSPECTION_TOKEN` with read-only Administration/Environments access to this
repository. It is used only for those two GET requests; publication uses the job's scoped token.

Only protected tag/qualification jobs receive `contents: write`; the qualification job also needs
OIDC and attestation permissions. Module-wide concurrency disables cancellation. Tags use
create-if-absent and exact readback; collisions fail, and an identical source resumes verification.
No Packages upload, consumer pin update or service-image release occurs.

Public verification uses an empty dependency/build cache, `proxy.golang.org`, `sum.golang.org`,
exact selected-version and source-content checks, and a standalone plus fresh-container smoke
build. Its overall limit is 15 minutes, proxy requests are bounded to 30 seconds, and image cleanup
has a separate 30-second budget. Only transient proxy availability is retried; checksum failure
is fatal. An unqualified tag may remain downloadable after failure, but cannot be adopted.

After verification, the workflow attests `qualification.json`, uploads it to a draft Release,
then publishes and verifies immutable release/asset attestations. The schema binds module,
version, tag/SHA, source digest, both Go checksums, trusted workflow revision/run/attempt and
validation/public-resolution identities. Admission checks actual selected graph/checksums,
immutable tag/source binding, asset and producer attestations and successful required jobs.
The caller supplies an independently approved workflow revision; a record cannot approve itself.
Network or verification failure does not disable admission. Offline cached-record admission is
not implemented and therefore fails closed. Local fake-provider tests do not establish hosted
protections, publication or a qualified release.

Provider contracts follow GitHub's [release verification](https://cli.github.com/manual/gh_release_verify-asset)
and [artifact provenance verification](https://cli.github.com/manual/gh_attestation_verify).
Consumer admission integration is required in each adoption PR. A release is not authorized by
running local checks or by merging this onboarding change.

## Compatibility and handoff

Library versions are independent of service images. During v0, patch versions preserve API and
documented behavior. Breaking minor releases require maintainer-reviewed migration notes naming
old/new versions, affected settings/APIs/consumers, before/after behavior, required checks and a
qualified rollback target with reversal limits. Consumers explicitly pin and verify exact
qualified versions; automatic minor upgrades are unsupported. Rollback restores a qualified pin
and necessary source/configuration changes in that consumer's own PR and reruns its full gate.
Never move a release tag. Review v1 readiness after both real consumers complete compatibility
qualification; publishing v1 needs separate approval.

Root validation owners are `go-shared-test`, `go-shared-test-integration`, `go-shared-check-ci`,
`go-topology-check`, `go-topology-test` and the existing Python shared-tooling suites. The additive
workflow syntax check uses Product's existing pinned Actionlint 1.7.7 binary; prepare it through
`make -C services/product-service bootstrap-ci-tools` when absent. Actual hosted release and
normal consumer/image qualification remain the later library handoff gate.
