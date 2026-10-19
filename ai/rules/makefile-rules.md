<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Makefile Rules](#makefile-rules)
  - [Compatibility and ownership](#compatibility-and-ownership)
  - [Entry points and included fragments](#entry-points-and-included-fragments)
  - [Defaults and target interface](#defaults-and-target-interface)
  - [Variables and paths](#variables-and-paths)
  - [Recipes and recursive Make](#recipes-and-recursive-make)
  - [Validation and adoption](#validation-and-adoption)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Makefile Rules

Apply these rules when creating or changing repository `Makefile` entry points, included `.mk`
fragments, Make targets, variables, or recipes. Inspect the affected Makefiles, callers, CI jobs,
and documentation before changing their interface or behavior. Follow [Naming rules](naming-rules.md)
for Makefile, fragment, script, and directory names.

## Compatibility and ownership

- Preserve public target names, supported variables, defaults, prerequisites, working-directory
  assumptions, mutation boundaries, and recursive invocation behavior unless the ticket explicitly
  changes them.
- Identify one owner for each target and variable. Included files share one global Make namespace;
  they do not provide encapsulation. Avoid duplicate targets, conflicting recipes, and unrelated
  fragments that mutate the same variables.
- Keep Make focused on orchestration. Put substantial parsing, branching, retry, cleanup, and
  multi-step shell behavior in an owned script with its own tests and failure handling.
- Treat Makefile composition changes as a dedicated refactoring. Do not combine them with runtime
  upgrades, scripting-language migrations, dependency updates, or unrelated target behavior.

## Entry points and included fragments

- Use `Makefile` for an independently runnable entry point with a documented working directory.
  Invoke it recursively with `$(MAKE) -C <directory> <target>` rather than including it merely to
  expose its targets through another entry point.
- Use a `.mk` extension for reusable include fragments. A fragment may depend on variables or
  helper targets supplied by its documented parent and is not expected to run independently.
- Prefer an explicit, ordered include list in the repository root for repository-wide fragments.
  Use shallow transitive includes only within a documented subsystem that owns the included files
  and their order. Keep include relationships acyclic and give each fragment one inclusion path.
- Use `include` for required fragments. Use `-include` only for optional local or generated files
  whose absence is expected; never hide a missing required fragment.
- Existing root includes of child files named `Makefile` are legacy compatibility paths, not the
  model for new composition. Do not expand that pattern. Convert or delegate them only in a scoped
  refactoring that preserves their supported standalone and root invocation paths.

An include reads targets and variables into the current Make invocation. It does not change the
working directory or create a new scope. Define stable repository paths before including fragments:

```makefile
.DEFAULT_GOAL := help

ROOT_MAKEFILE := $(abspath $(lastword $(MAKEFILE_LIST)))
ROOT_DIR := $(dir $(ROOT_MAKEFILE))

include $(ROOT_DIR)make/config.mk
include $(ROOT_DIR)make/go-tooling.mk
```

When a subsystem entry fragment owns further includes, capture its directory immediately with
`:=` because `MAKEFILE_LIST` changes as Make parses additional files:

```makefile
MONITORING_MK_DIR := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))
include $(MONITORING_MK_DIR)prometheus.mk
```

## Defaults and target interface

- Set `.DEFAULT_GOAL` explicitly to a safe, non-mutating `help` or documented aggregate target.
  Declare it before includes so the intended interface is visible and an included target cannot
  accidentally become the default.
- Mark targets that do not produce a file of the same name as `.PHONY`.
- Use lowercase kebab-case for public targets. Prefix internal helper targets with `_` and do not
  present them as supported user or CI entry points.
- Give public and non-obvious targets concise `##` help text compatible with the repository help
  output. Document required variables, prerequisites, side effects, and supported working directory.
- Build high-level checks by composing narrower targets when their ordering, failure behavior, and
  mutation boundaries are compatible. Do not hide provisioning, generation, formatting, dependency
  updates, or deployment behind a target presented as a non-mutating check.
- Avoid multiple recipes for the same target in one include graph. Treat override warnings as
  composition defects unless an explicit, documented rule intentionally extends prerequisites.

## Variables and paths

- Centralize shared tool versions and paths, but keep service policy and service-specific settings
  with their owner. Namespace fragment variables when a generic name could collide.
- Use `:=` for values that must be captured once during parsing, `?=` for supported caller
  overrides, and recursive `=` only when deferred expansion is intentional. Use `override` only
  when the Makefile must deliberately reject or replace caller control.
- Do not reset global flags, defaults, or caller-provided variables from an included fragment.
  Treat `MAKEFLAGS` changes, including `--warn-undefined-variables`, as repository compatibility
  decisions rather than universal defaults.
- Resolve paths from a documented repository, entry-point, or fragment directory. Do not assume an
  included file's relative paths are resolved from that file's directory.
- Quote paths and values at the shell boundary. Keep Make expansion, shell expansion, and escaping
  distinct; use `$$` only when the shell, rather than Make, must receive the dollar sign.

## Recipes and recursive Make

- Use `$(MAKE)`, not a literal `make`, for recursive invocations so jobserver, flags, and recursive
  behavior propagate. Use the `+` recipe prefix when a wrapped recursive command must retain Make's
  recursive execution semantics.
- Remember that each recipe line normally runs in a separate shell. Keep a dependent command group
  in one recipe or move it to a script; adopt `.ONESHELL` only as an intentional file-wide choice.
- Make stops when an unignored recipe command exits nonzero. Use `set -eu` or stricter shell options
  inside compound shell recipes when they improve that compound command's behavior; do not add
  `set -e` mechanically to every recipe.
- Validate required variables, target coordinates, and prerequisites before mutation. Separate
  inspect, check, and test targets from provisioning, publication, deployment, rotation, and
  destructive administration.
- Use command suppression with `@` deliberately. Preserve useful failure diagnostics and do not
  print success after a failed command.

## Validation and adoption

- Read target recipes and prerequisites before execution. A target name and `make -n` output do not
  prove that the target is non-mutating because parsing and expanded shell functions may have side
  effects.
- Validate every supported invocation path affected by a change: the repository root, direct
  `make -C` entry points, and callers in scripts or CI. Check the default goal, help output, target
  discovery, variable overrides, include order, duplicate-target warnings, and path resolution.
- Use existing non-mutating checks for behavior validation. Add focused disposable validation when
  changing Make tooling itself, and state any supported path or prerequisite that was not exercised.
- Apply these rules to new Makefiles and to existing Makefiles whose composition or interface is
  changed. Keep broad normalization of otherwise untouched Makefiles in a dedicated ticket.
