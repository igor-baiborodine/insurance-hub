<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Go Rules](#go-rules)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Go Rules

For Go work, select rules by the affected boundary. Read the entry point and these core rules
for implementation, migration, or tooling changes:

- [Go development](go-development.md): Makefile interface, implementation, lifecycle, contracts, and Phase 4 migration.
- [Go formatting](go-formatting.md): mandatory formatter policy and scope.
- [Go validation](go-validation.md): module-aware checks and evidence.

Load the additional rules when the task crosses their boundary:

- [Go architecture](go-architecture.md): new or changed service structure, dependencies, ports,
  composition, resource ownership, or architecture handoff.
- [Go API and transport](go-api-transport.md): HTTP/gRPC APIs, contracts, OpenAPI, transport
  mappings, security, or compatibility.
- [Go testing](go-test.md): writing or reviewing Go tests, fixtures, and test design.

Read multiple additional rules when a change crosses multiple boundaries. For example, an HTTP
handler and its tests require the API and transport and testing rules; a new service also requires
architecture. Validation still selects the checks required for the changed module, even when a
detail rule does not apply.

All Go development operations must use the corresponding Makefile targets; do not invoke the
underlying tools directly. See the development rule for target discovery and missing-target handling.
Apply [Naming rules](naming-rules.md) when creating or renaming Go source files, package
directories, or surrounding repository paths.

This file remains the Go entry point for existing references. It adds no separate policy or
instruction precedence.
