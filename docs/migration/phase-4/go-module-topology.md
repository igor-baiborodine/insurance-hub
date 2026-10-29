<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Go module topology and onboarding](#go-module-topology-and-onboarding)
  - [Current topology](#current-topology)
  - [Supported commands and mutation boundary](#supported-commands-and-mutation-boundary)
  - [Continuous integration](#continuous-integration)
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

The repository currently owns three Go modules and no Go workspace.

| Directory | Module path | Role and owner | Consumers | Replacements | Supported mode | Owning validation | CI |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `templates/go-service` | `github.com/igor-baiborodine/insurance-hub/templates/go-service` | Reusable service scaffold; Insurance Hub repository maintainers | Itself only | None | Standalone, `GOWORK=off` | `make -C templates/go-service check-go-pr` | `.github/workflows/go-pr-mod.yml` |
| `services/product-service` | `github.com/igor-baiborodine/insurance-hub/services/product-service` | Product business service; Product service maintainers | Its current executable and support packages | None | Standalone, `GOWORK=off` | `make -C services/product-service check-go-pr` | `.github/workflows/go-pr-mod.yml` |
| `libs/servicekit` | `github.com/igor-baiborodine/insurance-hub/libs/servicekit` | Public runtime infrastructure; Insurance Hub repository maintainers | Product/scaffold allowed; zero active consumers | None | Standalone, `GOWORK=off` | `make -C libs/servicekit check-go-pr` | `.github/workflows/go-pr-shared.yml` |

The scaffold owns its Protobuf source and generated bindings. It is not a deployed business
service, shared business model, or independently distributed contract module. Product owns its
Product v1 contract, SQL reader, HTTP and gRPC adapters, and business listeners in one standalone
module. There is no approved cross-module application dependency,
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
PostgreSQL harness/reader suites invoked by `test-integration`. Product `check-docs` also validates
the service README and versioned Product migration handoff, including a real Mermaid render.

The service and scaffold include the root-owned `scripts/go/module/common.mk`. The shared scripts implement
tool verification, formatting, dependency reproduction, and exact generated-output drift checks;
module Makefiles retain their pins and explicit Protobuf, SQL, integration, and run targets. The
scaffold copy proof points its disposable service at the repository helper, so the renamed service
uses the same checked-in implementation as other repository modules. Generated services remain in
this repository and resolve the root Python declarations, root `.venv/`, and shared Go helper
through their Makefiles. The copy proof transfers no environment, lock, or helper duplicate; it
verifies those repository references and bounds every child phase and cleanup. No runtime package
import or Go module dependency points at the scaffold or shared tooling directory.

Servicekit has a separate library-only Make interface using the same formatter policy and exact
Go/tool pins. It requires no executable, Protobuf generator, database or service-image release.
See its [ownership, registration and release contract](../../../libs/servicekit/README.md).

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

`.github/workflows/go-report-card.yml` uses the same qualifying path matrix for pushes to `main`
and pull requests targeting `main`. Its manual dispatch remains available for workflow-only
validation without making workflow YAML an independent hosted Go trigger.

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
PostgreSQL integration suite. Accepted access fixtures, Java access regression, and the combined
gateway-to-Go suite remain locally owned through `make product-security-test-tooling` and
`make product-security-check`; the hosted Product security workflow has been retired. The
[change-based local validation matrix](../../../CONTRIBUTING.md#change-based-local-validation)
records when those and the other auxiliary checks are required and which evidence to retain.

For shared tooling or topology changes, use the applicable local sequence:

```sh
make go-scaffold-bootstrap-tools
make go-product-bootstrap-tools
make go-modules-check FORMAT_SCOPE=all
make go-topology-test
make go-scaffold-test-tooling
make go-product-test-tooling
make go-scaffold-check-copy
```

A local pass is local evidence. YAML inspection is configuration evidence. Only a successful run
of an active GitHub Actions workflow is hosted-CI evidence; historical results from retired
workflows remain historical evidence.

The additive `go-pr-shared.yml` runs on every PR to `main`. Its planner examines complete base/head
ownership, both rename endpoints, discovered modules and active registration before Go/tool setup.
Library changes validate all active consumers against the exact candidate; root tooling changes
conservatively validate every module. Its aggregate recomputes phase obligations and checks source,
lockfile and candidate identities. The empty active-consumer set is an explicit initial state.
Branch protection requires separate repository configuration; adding YAML does not establish it.
The `go-release-lib.yml` workflow runs on library changes pushed to `main` and owns scoped
source-tag and qualified-release publication.

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
