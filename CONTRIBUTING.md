# Contributing to Insurance Hub

<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Getting Started](#getting-started)
  - [Prerequisites](#prerequisites)
  - [Clone Repository](#clone-repository)
  - [Install Dependencies](#install-dependencies)
- [Style Guides](#style-guides)
  - [Go](#go)
  - [Branch Names](#branch-names)
  - [Commit Messages](#commit-messages)
  - [Makefile](#makefile)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

## Getting Started

Please note that the following instructions were tested on **Ubuntu 24.04.2 LTS** only. Please feel
free to implement the same steps for other operating systems, like **macOS** or **Windows**. Your pull
requests are welcome and will be reviewed and merged as soon as possible.

### Prerequisites

Please note that the Kubernetes cluster dependencies can be installed by using the Make
`k8s/Makefile prereq-all` target after cloning the repository.

* **Git** `>=2.43.0`,
  see [Install Git](https://git-scm.com/book/en/v2/Getting-Started-Installing-Git).
* **Go** `>=1.24`, see [Install Go](https://go.dev/doc/install).
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

### Install Dependencies

1. Kubernetes Cluster Dependencies
- `make -C k8s k8s-prereq-all`

### Create Kubernetes Cluster

TODO

## Style Guides

### Go

TODO: add Go style guide before starting Phase 4

### Branch Names

All branch names **must** follow a consistent structure based on
the [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) types.  
This ensures clarity, easier traceability to issues, and automation support.

1. Branch Name Format

    ```text
    <type>/[issue-<number>]<very-short-description>
    ```

    - `type` — one of the allowed conventional commit types (`feat`, `fix`, `docs`, `chore`,
      `refactor`,
      `perf`, `test`, `style`, etc.).
    - `[issue-<number>]` — the related GitHub issue number in the form `issue-7`. This part is *
      *optional**
      but strongly recommended when the branch corresponds to a specific ticket.
    - `<very-short-description>` — a short, lowercase, dash-separated description of the work being
      done.
    - **Use `-` (dash)** to separate words inside the description.
    - **Do not** use spaces, underscores, or uppercase letters.

2. Examples

    - With the issue number:
        - `feat/issue-6-provision-kind-and-microk8s-clusters`
        - `fix/issue-14-handle-null-pointer-in-payment-service`
        - `docs/issue-22-update-api-specification`

    - Without the issue number:
        - `feat/add-basic-authentication`
        - `refactor/cleanup-database-migrations`
        - `chore/update-dependencies`

3. Rules & Best Practices

    * **Match your branch type to your commit type**: for example, a branch starting with `feat/`
      should
      contain commits whose main type is `feat`.
    * **Keep description short and clear**: maximum ~5 words helps avoid overly long branch names.
    * **Lowercase only**: use lowercase letters and dashes to separate words.
    * **Avoid generic words**: be specific (`feat/add-jwt-auth` instead of `feat/add-auth`).
    * **One purpose per branch**: Avoid mixing unrelated changes in the same branch.

4. Enforcing Branch Names

   A Git [pre-push hook](.githooks/pre-push) is used to validate branch names to ensure they follow
   the
   above convention. If the branch name does not comply with the convention, the **push will be
   blocked**
   until it's renamed.

### Commit Messages

To keep our commit history clear, consistent, and automatable, all commit messages **must** follow
the [Conventional Commits specification (v1.0.0)](https://www.conventionalcommits.org/en/v1.0.0/).

1. Commit Message Format

   Your commit message **must** use the following structure:

    ```text
    <type>: [issue-<number>] <very short description>
    ```

    - `<type>` is a lowercase conventional commit type (e.g., `feat`, `fix`, `docs`, `chore`,
      `refactor`,
      etc.).
    - The **GitHub issue reference** (for example, `issue-6`) is optional but recommended when the
      commit relates to a specific issue or task.
    - The **description** should be very short, concise, and in lowercase without a period at the
      end.
    - Use imperative mood (like a command) for the description, e.g., "add logging support" not "
      added
      logging support."

2. Examples

    - `feat: issue-12 add new user login endpoint`
    - `fix: handle null pointer on empty request`
    - `docs: update README with installation steps`
    - `chore: update dependencies to latest versions`
    - `refactor: issue-6 cleanup old authentication code`

3. Details

    - Always write commit messages in **lowercase**.
    - Commit types commonly used include:
        - `feat`: a new feature
        - `fix`: a bug fix
        - `docs`: documentation only changes
        - `style`: changes that do not affect the meaning of the code (white-space, formatting)
        - `refactor`: code change that neither fixes a bug nor adds a feature
        - `perf`: a code change that improves performance
        - `test`: adding missing tests or correcting existing tests
        - `chore`: changes to the build process or auxiliary tools
    - For **breaking changes**, include `!` after the type, and provide details in the commit body
      or
      footer as per the official spec.

4. Why Follow This Convention?

    - Enables automatic generation of changelogs.
    - Supports semantic versioning automation.
    - Provides a clearer commit history and task traceability.
    - Facilitates review and collaboration.

For full details and examples, see
the [Conventional Commits specification](https://www.conventionalcommits.org/en/v1.0.0/).

### Makefile

This guide outlines the best practices to write, organize, and maintain Makefiles. Following these
recommendations ensures automation is consistent, maintainable, and easy to extend across the team.

1. Use Modular Includes

    - Split complex automation into multiple Makefile fragments by domain (e.g., Docker, Kubernetes,
      testing).
    - Use conditional includes with `-include` to keep the root Makefile clean and composable.
    - Example:
      ```makefile
      -include docker/Makefile
      -include k8s/Makefile
      -include tests/Makefile
      ```

2. Declare `.PHONY` for Non-File Targets

    - Always mark targets that don’t correspond to actual filenames as `.PHONY`.
    - This avoids conflicts if files with the same names appear and guarantees targets run on each
      invocation.
      ```makefile
      .PHONY: build test clean
      ```

3. Use Variables for Versions and Paths

    - Centralize version numbers, tool binaries, and directory paths as variables at the top.
    - This enhances readability and ease of upgrades.
      ```makefile
      GO_VERSION = 1.24
      GOLANGCI_LINT_VERSION = v2.1.6
      ```

4. Provide a Helpful Default or `help` Target

    - The default `all` target or a dedicated `help` target should list available commands or
      instructions.
    - Makes the Makefile self-documenting and eases onboarding.
      ```makefile
      .PHONY: help
      help:
          @echo "Available targets:"
          @echo "  build    - Build the project"
          @echo "  test     - Run unit tests"
      ```

5. Group Related Targets With Comments

    - Organize your Makefile targets with clear comment headers by their purpose to enhance clarity.
      ```makefile
      ################################################################################
      # Build-related tasks
      ################################################################################
      ```

6. Support Target Composition

    - Create high-level targets that group multiple subtasks for consistent workflows (e.g., full
      checks).
    - This improves automation usability and testing consistency.
      ```makefile
      .PHONY: check
      check: format lint test
      ```

7. Avoid Hardcoded Paths

    - Use variables or discovery methods for directories or file lists to improve portability and
      ease
      changes.
      ```makefile
      SRC_DIR := ./internal
      ```

8. Minimize Complex Shell Logic in Makefiles

    - Complex commands or multistep logic should be delegated to external scripts.
    - The Makefile should orchestrate rather than script-heavy logic to keep it readable.

9. Document Custom Targets

   Add descriptive comments for each target covering:
    - What it does
    - When to run it
    - Any important prerequisites or notes

    ```makefile
    # Run Go formatters and linters
    .PHONY: format
    ```

10. Keep it DRY (Don’t Repeat Yourself)

    - Extract repeated commands or values into variables or reusable snippets.
    - This makes updates easier and avoids divergence.

11. Fail Early When Appropriate

    - Enable fail-fast behavior in scripts using `set -e` or ensure Make runs halt on errors.
    - Optionally, enable warnings on undefined variables to catch typos:
      ```makefile
      MAKEFLAGS += --warn-undefined-variables
      ```
12. Prefix Helper Targets with an Underscore

    - All helper or internal-use targets (not intended to be called directly by users, but used by other
      targets) must be prefixed with an underscore (`_`), e.g. `_prepare`, `_cleanup`.
    - This convention makes the Makefile easier to read and maintain, and distinguishes between public
      (user-facing) and private (internal helper) targets.
    - Document the purpose of helper targets in comments as with any other target.
