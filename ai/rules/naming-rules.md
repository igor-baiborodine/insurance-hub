<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Naming Rules](#naming-rules)
  - [General Principles](#general-principles)
  - [File Names](#file-names)
  - [Directory Names](#directory-names)
  - [Renames](#renames)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Naming Rules

Apply these rules whenever creating or renaming repository-owned files or directories. Use the
language, tool, or framework convention when one exists; use the repository fallback only when no
more specific convention applies.

## General Principles

- Use lowercase kebab-case for repository-owned files and directories that have no more specific
  convention, such as Markdown guides, service roots, and infrastructure directories.
- Preserve names required by languages, tools, frameworks, generated output, and external
  contracts. Examples include `Makefile`, `README.md`, `AGENTS.md`, `go.mod`, and `__init__.py`.
- Existing paths do not need to be renamed solely to conform to this rule. Rename existing paths
  only when the ticket explicitly includes the rename, and its compatibility works.
- Prefer concise names that describe ownership or purpose. Avoid temporary implementation details
  and redundant parent-directory context; retain sequence identifiers when an established format,
  such as a migration scheme, requires them.

## File Names

| File category | Convention | Examples |
| --- | --- | --- |
| Java source | PascalCase matching the top-level type | `ProductService.java`, `ProductServiceTest.java` |
| Go source | Short lowercase names; snake_case for multiple words | `catalog.go`, `http_handler.go`, `http_handler_test.go` |
| Python source | snake_case | `backend_harness.py`, `test_backend_harness.py` |
| Shell script | snake_case | `bootstrap_cluster.sh`, `check_dependencies.sh` |
| Markdown | kebab-case, except recognized entry points | `python-development.md`, `migration-plan.md`, `README.md` |
| Included Make fragment | kebab-case with `.mk` | `go-tooling.mk`, `release-checks.mk` |
| Make entry point | Tool-defined name | `Makefile` |
| Other repository-owned file | kebab-case unless its ecosystem requires another form | `local-config.yaml`, `coverage-policy.json` |

Additional requirements:

- Preserve Java special names such as `package-info.java` and `module-info.java`.
- Preserve Go suffixes that affect discovery or selection, including `_test.go`, `_GOOS.go`,
  `_GOARCH.go`, and `_GOOS_GOARCH.go`. Keep descriptive components before those suffixes, for
  example `request_telemetry_test.go` and `transport_linux_amd64.go`.
- Preserve Python framework entry points such as `__init__.py`, `__main__.py`, and `conftest.py`.
- Use snake_case for every new shell script, including executable scripts. Do not copy the
  repository's historical kebab-case shell filenames into new work.
- Do not rename generated files directly. Change the owning schema, generator configuration, or
  generation workflow when generated naming must change.

## Directory Names

- Use kebab-case for general repository, service, documentation, deployment, and infrastructure
  directories, such as `product-service` and `local-dev`.
- Use short lowercase single-word names for directories that directly define Go packages, such as
  `config`, `postgres`, and `telemetry`. Keep the directory and package name aligned when practical.
- Use Java package directory segments that match their lowercase package declaration.
- Use short lowercase names for importable Python package directories; use underscores only when
  needed for readability. Do not use hyphens in an importable Python package name.
- Preserve conventional and tool-recognized directories such as `.github`, `cmd`, `internal`,
  `testdata`, and `node_modules`.

## Renames

When a ticket includes a file or directory rename:

- update imports, package declarations, Make includes, scripts, CI configuration, documentation
  links, generated-source inputs, fixtures, and test-selection references together
- preserve public command paths or provide an explicit compatibility and migration plan
- account for case-insensitive filesystems when changing only letter case
- validate discovery-sensitive names, including Go tests and build-constrained files, Java types,
  Python imports, Make entry points, and executable scripts
- keep broad normalization of otherwise untouched paths in a dedicated ticket
