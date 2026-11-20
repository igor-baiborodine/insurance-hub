<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Go module topology and onboarding](#go-module-topology-and-onboarding)
  - [Current topology](#current-topology)
  - [Supported commands and mutation boundary](#supported-commands-and-mutation-boundary)
  - [Continuous integration](#continuous-integration)
  - [Shared library validation and release](#shared-library-validation-and-release)
    - [Resolution and consumer registration](#resolution-and-consumer-registration)
    - [Shared PR validation](#shared-pr-validation)
    - [Library release](#library-release)
  - [Import and consumer policy](#import-and-consumer-policy)
  - [Onboard a module or workspace](#onboard-a-module-or-workspace)
  - [Evidence](#evidence)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Go module topology and onboarding

This document defines the supported Go module resolution and validation boundary for Insurance
Hub. The machine-readable source of truth is the root
[`go-module-topology.json`](../../../go-module-topology.json). A directory that merely contains a
`go.mod` file, appears in a local workspace, or passes a direct Go command is not covered until its
inventory, owning Make target, consumers, and CI path agree.

## Current topology

The repository currently owns seven Go modules and no Go workspace.

| Directory | Module path | Role and owner | Consumers | Replacements | Supported mode | Owning validation | CI |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `templates/go-service` | `github.com/igor-baiborodine/insurance-hub/templates/go-service` | Reusable service scaffold; Insurance Hub repository maintainers | Itself only | None | Standalone, `GOWORK=off` | `make -C templates/go-service check-go-pr` | `.github/workflows/go-pr-mod.yml` |
| `services/product-service` | `github.com/igor-baiborodine/insurance-hub/services/product-service` | Product business service; Product service maintainers | Its current executable and support packages | None | Standalone, `GOWORK=off` | `make -C services/product-service check-go-pr` | `.github/workflows/go-pr-mod.yml` |
| `services/policy-search-service` | `github.com/igor-baiborodine/insurance-hub/services/policy-search-service` | Policy Search Echo business service; Policy Search service maintainers | Its current executable, generated Echo contract and support packages | None | Standalone, `GOWORK=off` | `make -C services/policy-search-service check-go-pr` | `.github/workflows/go-pr-mod.yml` |
| `services/dashboard-service` | `github.com/igor-baiborodine/insurance-hub/services/dashboard-service` | Dashboard Echo business service; Dashboard service maintainers | Its current executable, generated Echo contract and support packages | None | Standalone, `GOWORK=off` | `make -C services/dashboard-service check-go-pr` | `.github/workflows/go-pr-mod.yml` |
| `services/document-service` | `github.com/igor-baiborodine/insurance-hub/services/document-service` | Private Document Echo foundation; Document service maintainers | Its current executable, generated Echo contract and support packages | None | Standalone, `GOWORK=off` | `make -C services/document-service check-go-pr` | `.github/workflows/go-pr-mod.yml` |
| `services/chat-service` | `github.com/igor-baiborodine/insurance-hub/services/chat-service` | Behaviorless Chat business foundation; Chat service maintainers | Its current executable, two-listener lifecycle and support packages | None | Standalone, `GOWORK=off` | `make -C services/chat-service check-go-pr` | `.github/workflows/go-pr-mod.yml` |
| `libs/servicekit` | `github.com/igor-baiborodine/insurance-hub/libs/servicekit` | Public runtime infrastructure; Insurance Hub repository maintainers | Product, Policy Search, Dashboard, Document, Chat and scaffold | None | Standalone, `GOWORK=off` | `make -C libs/servicekit check-go-pr` | `.github/workflows/go-pr-shared.yml` |

The scaffold owns its Protobuf source and generated bindings. It is not a deployed business
service, shared business model, or independently distributed contract module. Product owns its
Product v1 contract, SQL reader, HTTP and gRPC adapters, and business listeners in one standalone
module. Policy Search owns the temporary Policy Search Echo contract, generated gateway, runtime
composition and deployment image; it has no Elasticsearch or Kafka dependency in this increment.
Dashboard and Document each own their temporary Echo contract, generated gateway, runtime
composition and deployment image. Document remains private and has no database, messaging, object
storage, scan or workflow dependency in this foundation increment.
Chat owns one business not-found listener, one management listener and bounded lifecycle/image
interfaces. It deliberately owns no WebSocket, Chat payload, protobuf, generated contract,
persistence or broker behavior in C01.
There is no approved cross-module application dependency,
private-module prerequisite, `go.work`, `go.work.sum`, or filesystem `replace`. The
`services/example-copy` tree created by `check-copy` is a disposable reuse fixture and is excluded
from topology.

Workspace inventory is currently fail-closed. `go-module-topology.json` must retain an empty
`workspaces` collection; `go-topology-check` rejects every non-empty workspace record before it can
be represented as approved. A future workspace approval must first add enforcement for actual
`go.work` membership and replacements plus its owning validation target and controlled evidence.

Inventory exclusions cover repository metadata, ignored ticket artifacts, module tool caches,
vendored or third-party dependency trees, test data, and the generated scaffold copy. The topology
checker reports encountered exclusions; exclusions cannot be used to hide a supported module.

## Supported commands and mutation boundary

Install pinned tools explicitly before running the full local job:

```sh
make go-scaffold-bootstrap-tools
make go-product-bootstrap-tools
make -C services/policy-search-service bootstrap-go-tools
make -C services/dashboard-service bootstrap-go-tools
make -C services/document-service bootstrap-go-tools
make -C services/chat-service bootstrap-go-tools
make -C libs/servicekit bootstrap-go-tools
```

The focused Product `bootstrap-go-tools` target installs only pinned Go validation tools. Its
broader local `bootstrap-tools` target also requires Node.js 20 or newer plus npm and installs
Mermaid CLI under the module's ignored `.tools/` directory. Product documentation validation uses
Python 3 to check local links/anchors and render every Mermaid block to a temporary SVG. Product's
`check-go-pr` target requires Docker daemon access and runs the race-enabled disposable PostgreSQL
harness with Testcontainers Go v0.44.0 and
`postgres:17.10-alpine` pinned at
`sha256:742f40ea20b9ff2ff31db5458d127452988a2164df9e17441e191f3b72252193`; it never uses
`PRODUCT_DATABASE_URL` or another shared database.

The supported non-mutating repository checks are:

```sh
make go-topology-check
make go-modules-check FORMAT_SCOPE=all
make go-topology-test
```

`go-topology-check` compares discovered manifests with the inventory, checks module identity and
owner targets, compares filesystem replacements with their approved inventory records, validates CI
qualifying paths, inline selection, selected-module cache and Make wiring, loads every package graph
with `GOWORK=off` and
readonly module resolution, and enforces repository import boundaries.

`go-modules-check` runs topology validation first and then visits every inventory module exactly
once. It forces standalone mode, rejects unapproved filesystem replacements, forwards only the
module's declared validation variables, and verifies that module/workspace manifest paths and bytes
remain unchanged. `FORMAT_SCOPE=all` covers every eligible handwritten Go file in all modules.
The scaffold and Product `./...` scopes cover all their current packages. Product coverage includes
`cmd/server`, the test-owned HTTP boundary, generated Product v1 bindings, sqlc-generated queries,
the bounded PostgreSQL reader, contract tests, the authoritative QA fixture loader, and the tagged
PostgreSQL harness/reader suites invoked by `test-integration`.

The scaffold and services reuse root-owned scripts under `scripts/go/module/` for the capabilities
they implement. Their module Makefiles retain pins and explicit transport, generated, SQL,
integration and run targets. Chat deliberately invokes only the format, dependency and build
helpers because it owns no Buf/protobuf or generated-contract surface. The
scaffold copy proof points its disposable service at the repository helper, so the renamed service
uses the same checked-in implementation as other repository modules. Generated services remain in
this repository and resolve the root Python declarations, root `.venv/`, and shared Go helper
through their Makefiles. The copy proof transfers no environment, lock, or helper duplicate; it
verifies those repository references and bounds every child phase and cleanup. No runtime package
import or Go module dependency points at the scaffold or shared tooling directory.

Public shared libraries ([Servicekit](../../../libs/servicekit/README.md) and
[MVEL](../../../libs/mvel/README.md)) have separate library-only Make interfaces using the same
formatter policy and exact Go/tool pins. They require no executable, Protobuf generator, database
or service-image release. Their shared registration, validation and release mechanics are
described in [Shared library validation and release](#shared-library-validation-and-release).

`go-topology-test` creates local temporary repositories with no network requirement. It proves
malformed and drifted inventory failures, workspace and replacement masking, allowed contract
sharing, prohibited private, scaffold, undeclared, and unknown-owner imports, and missing CI
triggers, selection roots, version-checked setup, stale-report cleanup, scaffold upload exclusion,
or required Make invocations. It removes its fixtures after success,
failure, or interruption and checks that real source and manifest bytes did not change.

These checks do not install tools, format source, tidy or upgrade dependencies, regenerate output,
advance compatibility baselines, create a workspace, deploy, or contact shared infrastructure.
Product validation may pull its pinned public PostgreSQL image and creates only disposable local
containers. Mutating module operations remain explicit owning Make targets and must be reviewed
with their generated and manifest changes.

## Continuous integration

`.github/workflows/go-pr-mod.yml` runs only for pull requests targeting `main` whose changed paths
match the qualifying inputs for the scaffold or any immediate `services/*-service` directory.
Qualifying inputs include Go source and generated output, `go.mod`/`go.sum`, owned Protobuf and SQL
inputs, contract baselines, and module lint/generator configuration. Scripts, Makefiles,
documentation, workflow YAML, topology metadata, Codecov configuration, legacy baseline fixtures,
Dockerfiles, and deployment assets do not independently trigger that selected-module workflow.
The additive shared workflow below covers module auxiliary files and root validation tooling.

`.github/workflows/go-report-card.yml` uses the same service and scaffold qualifying path matrix for
pushes to `main` and pull requests targeting `main`, and additionally runs for changes under
`libs/servicekit/`. It evaluates every registered business service, public shared library and
service scaffold. Its manual dispatch remains available for workflow-only validation without making
workflow YAML an independent hosted Go trigger.

Once triggered, the workflow compares the explicit pull-request base and head revisions and maps
the complete diff to the scaffold or conventionally named service roots. Zero supported modules is
an explicit successful no-op. One module runs its `bootstrap-go-tools` and `check-go-pr` targets.
More than one module fails before setup and asks the contributor to split the pull request,
including when the second module change was not independently qualifying. A newly added
`services/<name>-service` module receives the same selection, cache, validation, and coverage
behavior without a workflow edit. Superseded runs for the same pull request are cancelled.

CI uses `ubuntu-24.04`, checkout v6 with full history, and setup-go v7 with the exact Go version
from the selected module's `go.mod`. Go dependencies and pinned tool binaries are cached for the
selected module, while every run still executes version-verifying bootstrap. The selected module
directory supplies its Make invocation, `go.sum`, coverage report path, and Codecov flag. Product
retains its 75% coverage gate and `product-service` report identity. The scaffold's
`service-scaffold` selector role prevents
coverage collection and upload. The workflow does not install documentation or Java tools and does
not run topology, tooling-fixture, copy, release, deployment, image, database-role, or combined
security checks.

Product's selected-module validation owns the direct HTTP/gRPC access matrix and disposable
PostgreSQL integration suite. The retired Java access and combined gateway-to-Go scripts remain
historical evidence only; current Product tooling checks are service-local. The
[change-based local validation matrix](../../../CONTRIBUTING.md#change-based-local-validation)
records when those and the other auxiliary checks are required and which evidence to retain.

For shared tooling or topology changes, use the applicable local sequence:

```sh
make go-scaffold-bootstrap-tools
make go-product-bootstrap-tools
make -C services/policy-search-service bootstrap-go-tools
make -C services/dashboard-service bootstrap-go-tools
make -C services/document-service bootstrap-go-tools
make -C services/chat-service bootstrap-go-tools
make go-modules-check FORMAT_SCOPE=all
make go-topology-test
make go-scaffold-test-tooling
make go-product-test-tooling
make go-scaffold-check-copy
```

A local pass is local evidence. YAML inspection is configuration evidence. Only a successful run
of an active GitHub Actions workflow is hosted-CI evidence; historical results from retired
workflows remain historical evidence.

The additive `go-pr-shared.yml` runs on PRs to `main` that touch the shared library, Go source or
module manifests that can affect consumer registration, or root-owned Go/Python orchestration,
inventory, lock and workflow inputs. Its path filter excludes unrelated documentation, including
library Markdown, while retaining both sides of relevant renames and deletions. Once triggered,
the planner examines complete base/head ownership, discovered modules and active registration
before Go/tool setup. Library changes validate the library and every active consumer against the
exact candidate. Hosted consumer-only changes rely on `go-pr-mod.yml` for their normal module gate
and create no duplicate shared module obligation. Root tooling changes run the shared tooling and
topology checks; the full local profile additionally validates every module. When hosted planning
creates no module work, the planner completes the workflow without validation or
result-reconciliation runners. For plans with module work, the aggregate recomputes phase
obligations and checks source, lockfile and candidate identities. The empty active-consumer set is
an explicit initial state. Branch protection requires separate repository configuration; adding
YAML does not establish it. The `go-release-lib.yml` workflow runs on library changes pushed to
`main` and owns scoped source-tag publication and a non-draft GitHub Release marked Latest.

## Shared library validation and release

This section covers the repository mechanics shared by every `public-shared` library under
`libs/`. Each library's README owns its API, owning commands and release identity.

### Resolution and consumer registration

Libraries resolve standalone with `GOWORK=off`, exact version requirements, checked-in checksums
and no library replacement. Library owners and the shared workflow inspect the effective workspace
and caller or persistent flags before setting standalone flags. Alternate manifests and library
replacements fail. Consumers enforce exact versions and normal checksums.

Topology schema 2 keeps one module inventory and adds `activeConsumers`. Servicekit's `consumers`
allowlist and `activeConsumers` registry include Product and the scaffold; MVEL has none yet.
Registration cross-checks requirements and imports, owner and directory identity, workflow jobs,
unique phases and explicit Make bindings. A dependency that is still required or imported cannot be
hidden by deleting or downgrading its record. Every active registration implements the baseline
phase adapters plus its applicable generated, breaking, copy, integration and image phases.
Unsupported phases fail closed.

### Shared PR validation

From the repository root, `go-shared-plan` takes full `PR_BASE` and `PR_HEAD` SHAs. It checks the
complete diff, including both ends of renames and the union of base and head module roots. A
library change selects that library's gate and every active consumer against the candidate.
Changes to more than one service module fail before setup.

`go-shared-validate` works in a verified Git extraction in a private temporary directory. It keeps
alternate manifests only in disposable copies. A live private socket issues phase contexts, so
normal flags or a copied context file cannot enable candidate mode. Each candidate phase records
the selected graph before and after execution. The controller checks original manifests, source
content and the full candidate SHA. It removes its temporary tree on success, failure and handled
interruption; hard-killed runners require manual disposal. Candidate integration coverage is
written to controller-owned disposable storage, never to a consumer source tree.

The module work budget is 40 minutes. Child processes have 30-second progress heartbeats and a
10-second teardown, with separate controller shutdown. The aggregate recomputes expected
obligations and rejects missing, duplicate, unexpected, failed, cancelled, skipped or misidentified
results. Zero consumers is explicit and does not count as adoption coverage. Plans and results are
stored under the ignored `.work/servicekit` directory. For each selected library, the validate job
runs the library's `coverage` target and uploads the report to Codecov under a flag named after
its directory. **Go Main Coverage** publishes the same flags from `main`.

### Library release

The **Go Library Release** workflow runs when a non-Markdown change to a registered library reaches
`main`, including a direct push. It plans the exact triggering source, compares it with the
previous `main` revision and selects each changed library. Each library gets its own validation
entries, Monotag tag prefix and publication job. Monotag calculates a scoped stable v1 version from
conventional commits and that library's tags, starting at `v1.0.0`. Servicekit validates every
active consumer in the release matrix. At most four validation jobs run at once, and publication
jobs run one at a time.

Planning and validation have read-only repository access; only publication has `contents: write`.
One workflow-wide concurrency group serializes releases and never cancels an in-progress release.
The workflow needs no separate approval, qualification asset, attestation, package upload,
consumer pin edit or service-image release. Tag creation is create-if-absent with exact SHA
read-back. A collision fails, while the same source may resume verification, and a corrected source
uses a new version. A library's first release has a fixed version (MVEL `v1.0.0`); later releases
require existing release history for that library.

Public verification uses an empty dependency and build cache, `proxy.golang.org`,
`sum.golang.org`, exact selected-version and source-content checks, and standalone plus
fresh-container smoke builds. Its overall limit is 15 minutes. Proxy requests are bounded to 30
seconds, and image cleanup has a separate 30-second budget. Only transient proxy availability is
retried; a checksum failure is fatal. A tag may remain downloadable after a failed run, so a tag
alone does not establish a release. The workflow then creates a non-draft GitHub Release marked
Latest, with the same settings as Go service releases.

On 2026-10-30, the serial Servicekit baseline on GitHub-hosted `ubuntu-24.04` runners with two
active consumers took 12 min 26 s for `v0.2.2` and 13 min 27 s for `v1.0.0`, mostly validation.
Validation is now split by module, so wall-clock time follows the slowest module in each four-job
wave while total runner minutes still grow with every consumer. A move to a paid CI provider must
reassess the four-job cap from measured runs.

The root validation owners are `go-shared-test`, `go-shared-test-integration`,
`go-topology-check`, `go-topology-test` and the shared Python tooling suites.

## Import and consumer policy

Imports within one module and public external dependencies are allowed. Cross-module imports are
allowed only when the owner is classified as `public-contract` or `public-shared` and the importing
module is listed in that owner's `consumers`. For servicekit, this is an allowed-consumer list;
schema-2 `activeConsumers` separately records actual dependencies and complete phase support.
Workspace membership or local replacement does not
grant permission.

The checker rejects sibling `internal` imports, scaffold runtime imports, non-public module edges,
unapproved consumers, unknown repository-local owners, and incomplete package graphs. A change to
a public contract/shared module must run the owning target and every recorded consumer in each
supported resolution mode. Update the consumer list and CI coverage in the same reviewed change;
do not claim consumer coverage from a root `./...` command across nested modules.

## Onboard a module or workspace

Onboarding is one reviewed topology change. Complete all applicable items together:

1. Give the module an approved purpose, repository-relative directory, exact module path, role,
   owner, supported resolution modes, package scope, prerequisites, and non-mutating owning
   validation target.
2. Record every public contract/shared consumer. Keep business implementation private and do not
   introduce a shared business-model package to avoid duplication.
3. Record each filesystem replacement with its distribution reason, or remove it. Standalone
   modules must resolve without an ambient parent workspace or undeclared local path.
4. Add the module to `go-module-topology.json`. Do not add a workspace record under the current
   fail-closed policy. A future workspace change must first implement comparison of its actual
   membership/replacements and execution of its owning target, with positive and mismatch fixtures;
   only then may its owner, validation target, and CI purpose be approved in the inventory.
5. Place a business service directly under `services/` with a `-service` suffix and retain the
   standard `bootstrap-go-tools` and `check-go-pr` targets. The Go PR workflow's generic paths,
   selector, cache metadata, and coverage identity then apply without a workflow edit. Do not make
   auxiliary paths independent triggers in the existing selected-module workflow; register its
   supplemental coverage in the shared planner instead.
6. Add positive boundary coverage for any approved public edge and negative fixtures for private,
   scaffold, unapproved-consumer, workspace-masking, or replacement risks introduced by the new
   topology.
7. Run the module owner target, all affected consumers, `make go-topology-check`,
   `make go-modules-check`, and `make go-topology-test`. Record exact local and hosted evidence
   separately.

A copied scaffold is future work until every applicable item passes. Creating the directory or
adding a workflow path alone does not establish repository support.

## Evidence

For each check, record the exact Make invocation, working directory, resolution mode, selected
module/package/consumer scope, prerequisites, source (`local`, `CI`, or `manual`), result, and any
blocked or unexecuted follow-up. Future modules and hosted jobs are future work until execution
evidence exists. Never infer standalone portability from workspace success or hosted success from a
local run.
