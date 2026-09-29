# Go Rules

For Go work, read and apply all three canonical rules:

- [Go development](go-development.md): service boundaries, implementation, contracts, and Phase 4 migration.
- [Go formatting](go-formatting.md): mandatory formatter policy and scope.
- [Go validation](go-validation.md): module-aware checks and evidence.

All Go development operations must use the corresponding Makefile targets; do not invoke the
underlying tools directly. See the development rule for target discovery and missing-target handling.

This file remains the Go entry point for existing references. It adds no separate policy or
instruction precedence.
