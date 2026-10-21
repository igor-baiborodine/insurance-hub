<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Contributing to Insurance Hub](#contributing-to-insurance-hub)
  - [Getting Started](#getting-started)
    - [Prerequisites](#prerequisites)
    - [Clone Repository](#clone-repository)
    - [Configure Repository Hooks](#configure-repository-hooks)
    - [Install Dependencies](#install-dependencies)
      - [Web Development](#web-development)
      - [Kubernetes](#kubernetes)
    - [Create Kubernetes Cluster](#create-kubernetes-cluster)
  - [Style Guides](#style-guides)
    - [Go](#go)
      - [Change-Based Local Validation](#change-based-local-validation)
    - [Python Auxiliary Tooling](#python-auxiliary-tooling)
      - [Python Toolchain Setup](#python-toolchain-setup)
      - [Python Checks and Test Suites](#python-checks-and-test-suites)
    - [Branch Names](#branch-names)
    - [Commit Messages](#commit-messages)
    - [Makefile](#makefile)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Contributing to Insurance Hub

## Getting Started

Please note that the following instructions were tested on **Ubuntu 24.04.2 LTS** only. Please feel
free to implement the same steps for other operating systems, like **macOS** or **Windows**. Your pull
requests are welcome and will be reviewed and merged as soon as possible.

### Prerequisites

Please note that the Kubernetes cluster dependencies can be installed by using the Make
`k8s/Makefile prereq-all` target after cloning the repository.

* **Git** `>=2.43.0`,
  see [Install Git](https://git-scm.com/book/en/v2/Getting-Started-Installing-Git).
* **Go** `1.27.3` for Product and `1.27.2` for the service scaffold, see
  [Install Go](https://go.dev/doc/install). Each module requires its exact `go.mod` version.
* **CPython 3.14.8** for auxiliary tooling governed by the current Python policy; use the owning
  isolated environment and Make targets described in
  [Python Auxiliary Tooling](#python-auxiliary-tooling).
* **Node.js** `12.22.12`, see [Install Node.js](https://nodejs.org/en/download/).
* **Yarn** `1.22.22`, see [Install Yarn](https://classic.yarnpkg.com/en/docs/install).
* **jq** `>=1.6`, see [Install jq](https://stedolan.github.io/jq/download/).
* **Make** `>=4.3`, see [Install Make](https://www.gnu.org/software/make/).
* **Docker Engine** `>=24.0.0`,
  see [Install Docker Engine](https://docs.docker.com/engine/install/).
* **kubectl** `>=1.29.2`, see [Install kubectl](https://kubernetes.io/docs/tasks/tools/#kubectl).
* **kubectx + kubens** `>=0.9.1`, see [Install kubectx](https://github.com/ahmetb/kubectx#installation).
* **Kind** `>=0.20.0`,
  see [Install Kind](https://kind.sigs.k8s.io/docs/user/quick-start/#installation).
* **LXD** `>=4.22`, see [Install LXD](https://linuxcontainers.org/lxd/getting-started-cli/).
* **Helm** `>=3.14.0`, see [Install Helm](https://helm.sh/docs/intro/install/).
* **PostgreSQL** `>=15.0`, see [Install PostgreSQL](https://www.postgresql.org/download/).
* **CloudNativePG kubectl plugin** `>=1.26.0`,
  see [Install CloudNativePG Plugin](https://cloudnative-pg.io/documentation/current/installation_upgrade/#kubectl-plugin).
* **MongoDB Shell** `>=2.1.1`,
  see [Install MongoDB Shell](https://www.mongodb.com/docs/mongodb-shell/install/).
* **MinIO mc** `>=RELEASE.2025-08-13T08-35-41Z`,
  see [Install MinIO mc](https://docs.min.io/enterprise/aistor-object-store/reference/cli/).

### Clone Repository

```bash
git clone git@github.com:your-org/insurance-hub.git
cd insurance-hub
```

### Configure Repository Hooks

This repository provides a pre-commit hook that validates the table of contents in staged Markdown
files and a pre-push hook that validates branch names. Enable both hooks from the repository root:

```bash
git config core.hooksPath .githooks
```

Before setting the path, inspect `git config --get core.hooksPath`. If it names another custom hook
directory, integrate the repository hooks there instead of replacing that setup.

The pre-commit hook checks added, modified, copied, and renamed Markdown files from the staged
snapshot. It does not format files or change the index or working tree. If it reports a stale or
missing table of contents, format the affected working-tree files and stage the result:

```bash
./scripts/markdown-toc.sh format path/to/file.md
git add path/to/file.md
```

The hook uses the repository's managed CPython 3.14.8 runtime and requires `npx` only when staged
Markdown needs validation. Complete [Python Toolchain Setup](#python-toolchain-setup) before using
the hook. Run `make markdown-toc-test` to exercise partial staging, cleanup, and controlled failure
cases in a disposable Git repository.

### Install Dependencies

#### Web Development

<details>
  <summary>Node.js and Yarn Setup</summary>

⚠️ This legacy Vue 2 project requires **specific versions** due to `node-sass@4.9.2` compatibility:

```
✅ Node.js v12.22.12 (required)
✅ Yarn v1.22.22 (required)
❌ Modern Node 16+ or Yarn 4+ will fail
```

1. Install NVM (Node Version Manager)

```bash
curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.39.7/install.sh | bash
source ~/.bashrc  # Reload shell
```

2. Install Exact Node Version

```bash
nvm install 12.22.12
nvm use 12.22.12
nvm alias default 12.22.12
```

3. Install Yarn 1.22.22

```bash
npm install -g yarn@1.22.22
hash -r  # Refresh PATH
```

4. Verify Versions

```bash
node -v  # v12.22.12
yarn -v  # 1.22.22
```

5. Install Dependencies

From `insurance-hub/legacy` root:
```bash
yarn --cwd web-vue install --ignore-engines
```

6. Build and Serve

```bash
# Production build
yarn --cwd web-vue build

# Development server
yarn --cwd web-vue serve
```

7. Switching Node Versions

```bash
nvm use 25          # Modern Node for other projects
nvm use 12.22.12    # Back to web-vue project
```

</details>

#### Kubernetes

1. Install Kubernetes Dependencies
- `make -C k8s k8s-prereq-all`

### Create Kubernetes Cluster

TODO

## Style Guides

### Go

Phase 4 Go services follow the repository's [architecture rules](ai/rules/go-architecture.md),
[API and transport rules](ai/rules/go-api-transport.md),
[Go development rules](ai/rules/go-development.md),
[testing rules](ai/rules/go-test.md),
[formatting policy](ai/rules/go-formatting.md), and [validation rules](ai/rules/go-validation.md).
Use the owning Makefile targets for every Go operation, including tool installation, dependency
maintenance, formatting, linting, tests, builds, execution, and generation. Do not replace them
with direct Go or tool commands. Read the owning Makefile for supported targets and module scope.

The current [Go service scaffold](templates/go-service/README.md) requires exact Go 1.27.2 and
managed CPython 3.14.8. It pins Go tools under ignored `templates/go-service/.tools/` and uses the
repository's root Python declarations, lock, and ignored `.venv/`. Run `bootstrap-tools`,
`update-deps`, `update-proto-deps`, `format`, `format-proto`, and `gen-proto` only when their
documented mutations are intended. `check`, `test-tooling`, and `check-copy` preserve the source
module; `check` does not install tools, reformat source, tidy manifests, regenerate real output,
update compatibility baselines, run shared infrastructure, or deploy.

The root [`go-module-topology.json`](go-module-topology.json) records every supported Go module and
approved workspace. Run `make go-topology-check` with the managed Python runtime and Go toolchain to
verify inventory, ownership, imports, replacements, and CI coverage. Run `make go-modules-check`
after bootstrapping the owning module tools to execute every recorded non-mutating validation target
in its required resolution mode. `make go-topology-test` proves 34 controlled inventory, workflow,
workspace, replacement, and import cases in disposable local fixtures. These targets do not install
tools, update dependencies, format source, regenerate output, create a workspace, or deploy.

Shared module commands select CPython through `COMMON_GO_TOOLING_PYTHON`; it defaults to the
including Makefile's verified `PYTHON` value. The scaffold and generated services remain in this
repository and share its root `.python-version`, `pyproject.toml`, `uv.lock`, `.venv/`, and
`scripts/go/module` helpers. Their Makefiles resolve the root without shell activation and preserve
an explicit interpreter override that satisfies the root runtime and lock checks.

The current inventory contains `templates/go-service` and
[`services/product-service`](services/product-service/README.md), both supported standalone with
`GOWORK=off`; no workspace or filesystem replacement is approved. Bootstrap Product's module-owned
tools with `make -C services/product-service bootstrap-tools` before running the aggregate. The six
`go-scaffold-*` root delegates remain narrow scaffold conveniences. The
[Go module topology guide](docs/migration/phase-4/go-module-topology.md) defines exact local and hosted
commands, exclusions, mutation boundaries, consumer validation, and the reviewed onboarding
process for future modules or workspaces.

#### Change-Based Local Validation

**Go Module PR Check** runs only for qualifying module inputs and validates one selected module.
Auxiliary scripts, Makefiles, documentation, workflow configuration, topology metadata, deployment
assets, database-role tooling, images, and the combined Product security boundary remain local review
responsibilities. Select checks from the changed behavior; do not run every command for every pull
request or treat a local result as hosted evidence.

| Changed behavior | Local commands from the repository root | Prerequisites and review evidence |
| --- | --- | --- |
| Go topology, inventory, ownership, resolution, or shared module tooling | `make python-shared-tooling-check`; `make python-shared-tooling-test-integration`; `make go-topology-check`; `make go-topology-test`; bootstrap affected module tools, then `make go-modules-check FORMAT_SCOPE=all` when the aggregate is applicable | Managed CPython 3.14.8, exact module Go/tool pins, and Docker for Product integration. Record affected modules, standalone resolution, and controlled failures. |
| Scaffold or Product validation tooling | `make python-scaffold-tooling-check`; `make go-scaffold-test-tooling`; `make python-product-tooling-check`; `make go-product-test-tooling` | Provision the managed Python environment and bootstrap the corresponding Go tools. Record fast Python behavior, controlled defects, bounded child handling, and fixture/source cleanup. |
| Scaffold reuse or copy behavior | `make go-scaffold-check-copy` | Provision and bootstrap scaffold tools. Record renamed-copy identity, repository Python/Go tooling resolution, generation, validation, deadlines, and cleanup. |
| Go PR, release, report-card, or service-dispatch workflow tooling | `make -C services/product-service bootstrap-ci-tools`; `make -C services/product-service check-ci`; `make go-svc-release-test`; `make go-svc-dispatch-test`; `make python-shared-tooling-check` | Managed CPython 3.14.8, pinned local workflow clients, and any documented public downloads. Record configuration validation separately from any actual hosted run. |
| Product deployment assets or tooling | `make go-svc-deploy-check SVC_NAME=product`; `make go-svc-deploy-test-tooling SVC_NAME=product` | Pinned Product CI/deployment tools. These checks use rendered or fake-adapter inputs and do not prove a live environment. |
| Product reader role, permission fixtures, or baseline behavior | `make postgres-svc-role-test-fast`; `make postgres-svc-role-test`; `make product-baseline-fixtures-check`; `make product-baseline-test BASELINE_SUITE=db-permissions` | Managed CPython 3.14.8, Java 14/Maven where selected, Docker, and disposable PostgreSQL as documented by the owning target. Record fast fake-adapter results separately from disposable permission evidence and the selected fixture scope. |
| Product runtime image or container lifecycle | `make go-svc-image-check SVC_NAME=product` | Docker/Buildx and bootstrapped Product Go tools. Record image identity, contents, startup/read behavior, and cleanup. |
| Gateway/Product access behavior, access fixtures, or combined security tooling | `make product-security-test-tooling`; `make product-security-test-tooling-integration`; `make product-security-check`; retain `make -C services/product-service check-go-pr FORMAT_SCOPE=all` for direct Go access and vulnerability coverage when Product Go inputs are affected | Managed CPython 3.14.8, Java 14, Maven, Go 1.27.3, Docker, and bootstrapped Product Go tools. Record fast validator results separately from the real Java/Go/Docker boundary, exact access cases/reports, bounded process cleanup, and disposable cleanup; never supply a live database URL. |
| Product documentation renderer or covered links | `make -C services/product-service bootstrap-doc-tools`; `make -C services/product-service check-docs` | Managed CPython 3.14.8, Node.js 20 or newer, npm, and the pinned module-local Mermaid tools. Record link and render counts. |
| Markdown content | `./scripts/markdown-toc.sh format <file>...`; stage the result; the pre-commit hook runs `check` on the staged snapshot | Managed CPython 3.14.8; `npx` is needed only when Markdown is selected. Record the exact task-owned files checked. |

Review evidence identifies the revision, exact command and working directory, changed scope,
prerequisites, execution source (`local`, `CI`, or `manual`), result, and any blocked or unexecuted
follow-up. Heavy security, deployment, image, database, topology, and tooling checks do not belong in
the pre-commit hook. Live-target commands retain their explicit environment, context, credential,
and mutation safeguards in their owning runbooks.

### Python Auxiliary Tooling

Use Python for administrative automation, deployment helpers, structured-data validation, and
test harnesses when substantial parsing, branching, state management, or recovery makes Bash
difficult to maintain. Keep short, straightforward CLI sequences in Bash. Python supports the
repository's tooling; service implementation follows the existing Java-to-Go migration.

Before implementing or reviewing auxiliary Python or its tests, configuration, Make callers, or
administrative interfaces, read the [Python entry point](ai/rules/python-rules.md) and all four
linked rules: [development](ai/rules/python-development.md),
[testing](ai/rules/python-test.md), [formatting](ai/rules/python-formatting.md), and
[validation](ai/rules/python-validation.md). Keep scripts with their existing owner. Prefer the
standard library for operational dependencies where it meets the contract; pytest and Ruff are
required development tools under the canonical rules. Install declared dependencies only through
reproducible, isolated setup; do not install them into system Python.

Use the owning Makefile targets for repeatable development, validation, and administrative
operations. Read their prerequisites, supported variables, and side effects before running them.
Keep explicit target selection, secret handling, timeouts, cleanup, and partial-failure recovery
in the implementation. Administrative tools and elevated credentials belong outside application
runtime images and startup paths.

Test changed behavior and failure paths through the relevant Make targets, using disposable
resources for mutation tests. For database administration, derive allowed and denied operations
from each role profile's contract, including reader, writer, or migrator profiles. Record the
limits of fake-adapter and disposable tests separately from live-environment evidence.

#### Python Toolchain Setup

The supported auxiliary Python setup is Ubuntu 24.04 on x86_64. It uses user-local uv 0.13.1 to
install managed CPython 3.14.8 alongside Ubuntu's system interpreter and creates the repository
environment at ignored `.venv/`. The tracked `.python-version`, `pyproject.toml`, and `uv.lock`
declare the interpreter, direct development dependencies, and resolved transitive artifacts.
Shell activation is not required.

Install the pinned uv release as your user; this requires `curl`, TLS certificates, and standard
archive utilities:

```bash
curl --proto '=https' --tlsv1.2 -LsSf https://astral.sh/uv/0.13.1/install.sh |
  env UV_INSTALL_DIR="$HOME/.local/bin" INSTALLER_NO_MODIFY_PATH=1 sh
export PATH="$HOME/.local/bin:$PATH"
uv --version  # uv 0.13.1
```

Provisioning is an explicit, networked setup operation. It installs the managed interpreter,
creates `.venv/` only when absent, and synchronizes the frozen lock:

```bash
make python-toolchain-setup
make python-toolchain-info
```

Setup does not delete or replace an existing environment. If `.venv/` uses another interpreter,
inspect and preserve its dependency declarations first, then deliberately move or remove that
environment and rerun setup. Never use `sudo pip`, change `/usr/bin/python3`, or install these
packages into Ubuntu's Python.

Normal checks never provision or install. The runtime check verifies only the selected CPython,
so operational tools do not require pytest or Ruff. The toolchain check also verifies the frozen
environment and exact development-tool versions:

```bash
make python-runtime-check
make python-toolchain-check
make -C k8s python-toolchain-check
make -C services/product-service python-toolchain-check
```

The `PYTHON` and `UV` variables support explicit executable overrides without activation. Every
override is version-checked before use:

```bash
make python-toolchain-check PYTHON=/absolute/path/to/python UV=/absolute/path/to/uv
```

For a security or maintenance update, change the direct version declarations and Make constants
together, regenerate `uv.lock` with the selected uv release, deliberately recreate or synchronize
the environment, and rerun the affected static, fast, integration, and acceptance checks. Record
the interpreter build, platform, tool versions, lock change, and results. CPython 3.14.8 remains
the baseline until an explicitly approved update.

#### Python Checks and Test Suites

Run the mutating formatter only with an explicit repository-relative file selection. The paired
format and lint checks inspect the same selection without changing source:

```bash
make python-format PYTHON_FILES='path/to/tool.py path/to/test_tool.py'
make python-format-check PYTHON_FILES='path/to/tool.py path/to/test_tool.py'
make python-lint PYTHON_FILES='path/to/tool.py path/to/test_tool.py'
make python-selection-check
```

Selections accept portable repository-relative paths. Absolute paths, `.` or `..` components,
empty components, unsupported characters, missing paths, and paths that resolve outside the
repository through symlinks are rejected before Ruff or pytest starts.

Use `make python-scope-list` to inspect the tracked and unignored untracked `*.py` files selected
by `python-repository-format-check` and `python-repository-lint`. These repository-wide checks keep
existing violations visible while owner batches migrate; a failure must be recorded rather than
reported as a passing foundation check.

Use the owner aggregates for the supported auxiliary-tooling boundaries:

```bash
make python-shared-tooling-check
make python-scaffold-tooling-check
make python-product-tooling-check
make python-k8s-admin-check
make python-observability-check
make python-legacy-check
```

These targets apply the repository's 80/20 direction to non-core tooling tests: they emphasize
likely paths and focused contract coverage while retaining applicable target, failure, deadline,
interruption, cleanup, redaction, permission, and dry-run safety checks. Inspect each recipe first;
some aggregates create owned disposable resources. They do not imply live cluster,
host-administration, release-publication, or full-duration acceptance evidence.

The committed examples provide a bounded local gate and separate test-suite entry points:

```bash
make python-examples-check
make python-examples-test-integration
```

`python-examples-check` performs non-mutating example format/lint checks, no-path default
collection, and the fast example tests. Default collection is restricted to the configured fast
test path. Disposable integration requires its explicit target. Full owner acceptance requires an
explicit existing repository-relative path and confirmation:

```bash
make python-test-acceptance \
  PYTHON_ACCEPTANCE_CONFIRM=yes \
  PYTHON_ACCEPTANCE_PATH=path/to/owner/acceptance
```

Inspect the owning recipe and prerequisites before an acceptance run. The confirmation selects
the interface; it does not authorize a live mutation or replace owner-specific environment and
credential safeguards. The [Python testing examples](ai/examples/python-testing/README.md) show
the executable CLI, fake boundary, scenario structure, and disposable integration pattern.

### Branch Names

Follow the canonical [Git branch rules](ai/rules/git-rules.md#branch-names). Branch names use an
allowed Conventional Commit type, an optional `issue-<number>-` prefix, and a concise lowercase
hyphen-separated description.

The Git [pre-push hook](.githooks/pre-push) validates branch names and blocks a push when the name
does not comply. Enable it as described in
[Configure Repository Hooks](#configure-repository-hooks).

### Commit Messages

Follow the canonical [Git commit-message rules](ai/rules/git-rules.md#commit-messages). Commit
messages use the repository's allowed Conventional Commit types, an optional bracketed issue
reference such as `[issue-123]`, and a concise imperative description. Authors and reviewers check
this policy because the repository does not provide a `commit-msg` hook.

### Makefile

Follow the canonical [Makefile rules](ai/rules/makefile-rules.md) when creating or changing a
`Makefile`, included `.mk` fragment, target, variable, or recipe. The rules define entry-point and
fragment ownership, include composition, safe defaults, public and helper targets, variables and
paths, recursive Make, recipe failure behavior, mutation boundaries, and validation.

Inspect the owning Makefile before invoking a target. Use its documented working directory,
prerequisites, and supported variables, and preserve existing public interfaces unless the change
explicitly includes a migration.
