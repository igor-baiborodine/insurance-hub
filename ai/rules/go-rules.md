<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Go Rules](#go-rules)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Go Rules

For Go work, read and apply all five canonical rules:

- [Go architecture](go-architecture.md): business visibility, dependency boundaries, ports, composition, and architecture handoff.
- [Go development](go-development.md): Makefile interface, implementation, lifecycle, contracts, and Phase 4 migration.
- [Go testing](go-test.md): readable names, test structure, assertions, isolation, and integration boundaries.
- [Go formatting](go-formatting.md): mandatory formatter policy and scope.
- [Go validation](go-validation.md): module-aware checks and evidence.

All Go development operations must use the corresponding Makefile targets; do not invoke the
underlying tools directly. See the development rule for target discovery and missing-target handling.

This file remains the Go entry point for existing references. It adds no separate policy or
instruction precedence.
