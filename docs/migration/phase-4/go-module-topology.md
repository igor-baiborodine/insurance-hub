# Go module topology and onboarding

This document defines the supported Go module resolution and validation boundary for Insurance
Hub. The machine-readable source of truth is the root
[`go-module-topology.json`](../../../go-module-topology.json). A directory that merely contains a
`go.mod` file, appears in a local workspace, or passes a direct Go command is not covered until its
inventory, owning Make target, consumers, and CI path agree.

## Current topology

The repository currently owns two Go modules and no Go workspace.

| Directory | Module path | Role and owner | Consumers | Replacements | Supported mode | Owning validation | CI |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `templates/go-service` | `github.com/igor-baiborodine/insurance-hub/templates/go-service` | Reusable service scaffold; Insurance Hub repository maintainers | Itself only | None | Standalone, `GOWORK=off` | `make -C templates/go-service check` | `.github/workflows/go-modules.yml` |
| `services/product-service` | `github.com/igor-baiborodine/insurance-hub/services/product-service` | Product business service; Product service maintainers | Its current executable and support packages | None | Standalone, `GOWORK=off` | `make -C services/product-service check` | `.github/workflows/go-modules.yml` |

The scaffold owns its Protobuf source and generated bindings. It is not a deployed business
service, shared business model, or independently distributed contract module. Product currently has
a management-only shell; its Product v1 contract, SQL reader, and business listeners join the same
module in later issue-132 steps. There is no approved cross-module application dependency,
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
make -C services/product-service bootstrap-tools
```

The Product validation also requires Docker daemon access. Its owning `check` target runs the
race-enabled disposable PostgreSQL harness with Testcontainers Go v0.44.0 and
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
triggers and root invocations, loads every package graph with `GOWORK=off` and readonly module
resolution, and enforces repository import boundaries.

`go-modules-check` runs topology validation first and then visits every inventory module exactly
once. It forces standalone mode, rejects unapproved filesystem replacements, forwards only the
module's declared validation variables, and verifies that module/workspace manifest paths and bytes
remain unchanged. `FORMAT_SCOPE=all` covers every eligible handwritten Go file in both modules.
The scaffold and Product `./...` scopes cover all their current packages. Product coverage includes
`cmd/server`, the test-owned HTTP boundary, generated Product v1 bindings, contract tests, the
authoritative fixture loader, and the tagged PostgreSQL harness invoked by `test-integration`.
Later Product adapters must enter this aggregate.

`go-topology-test` creates local temporary repositories with no network requirement. It proves
malformed and drifted inventory failures, workspace and replacement masking, allowed contract
sharing, and prohibited private, scaffold, undeclared, and unknown-owner imports. It removes its
fixtures after success, failure, or interruption and checks that real source and manifest bytes did
not change.

These checks do not install tools, format source, tidy or upgrade dependencies, regenerate output,
advance compatibility baselines, create a workspace, deploy, or contact shared infrastructure.
Product validation may pull its pinned public PostgreSQL image and creates only disposable local
containers. Mutating module operations remain explicit owning Make targets and must be reviewed
with their generated and manifest changes.

## Continuous integration

`.github/workflows/go-modules.yml` is the current topology workflow. Pushes and pull requests to
`main` trigger it for both modules, the shared Product baseline corpus, every `go.mod`, `go.sum`,
`go.work`, and `go.work.sum`, the canonical inventory, topology scripts, this guide, root Makefile,
and workflow itself. The topology checker fails if an inventory module's workflow lacks its module
path triggers or does not call
both `make go-modules-check` and `make go-topology-test` through the restricted inline step-level
`run` forms used by this repository. Block scalars, comments, and values under unrelated workflow
keys do not count as execution evidence.

CI uses `ubuntu-24.04`, checkout v6 with full history, setup-go v7 with Go 1.27.2, both module
`go.sum` cache inputs, and `contents: read`. It bootstraps each module's pinned tools through Make,
resolves changed formatting scope from the pull-request base or push predecessor, runs the aggregate
module and
topology targets, then retains the scaffold tooling-failure and renamed-copy proofs. It requires no
secret or live service.

The equivalent full-file local sequence is:

```sh
make go-scaffold-bootstrap-tools
make -C services/product-service bootstrap-tools
make go-modules-check FORMAT_SCOPE=all
make go-topology-test
make go-scaffold-test-tooling
make go-scaffold-check-copy
```

A local pass is local evidence. YAML inspection is configuration evidence. Only a successful
GitHub Actions run is hosted-CI evidence.

## Import and consumer policy

Imports within one module and public external dependencies are allowed. Cross-module imports are
allowed only when the owner is classified as `public-contract` or `public-shared` and the importing
module is listed in that owner's `consumers`. Workspace membership or local replacement does not
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
5. Update CI module path triggers, tool bootstrap, cache dependency paths, and the existing root
   aggregate job. Do not create a competing workflow that bypasses topology validation.
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
