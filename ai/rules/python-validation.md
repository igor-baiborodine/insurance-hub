<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Python Validation](#python-validation)
  - [Select checks from changed behavior](#select-checks-from-changed-behavior)
  - [Behavioral and failure coverage](#behavioral-and-failure-coverage)
  - [Integration and permission checks](#integration-and-permission-checks)
  - [Current examples and evidence](#current-examples-and-evidence)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Python Validation

Apply [Python development](python-development.md) and use the owning Makefile's documented
targets. Inspect their actual coverage and side effects before execution. No universal Python
lint, formatting, or test target is implied by this rule.
Apply the repository-wide [testability assessment](repo-rules.md#testability) when implementing
unit or integration tests with complicated fixture wiring or test setup.

## Select checks from changed behavior

- Identify the scripts, callers, supported interpreter, configuration inputs, external tools,
  Make recipes, and CI jobs affected by the change. A Python helper may be exercised through a
  shell harness or service target rather than a standalone Python test suite.
- Run the narrowest meaningful checks for the changed behavior, then applicable existing gates.
  Reuse configured syntax, format, lint, and type checks when available. Syntax checks alone do
  not establish runtime compatibility or behavioral correctness; avoid leaving bytecode caches
  and generated files in the checkout.
- Prefer standard-library testing facilities for standalone tools unless their owner already
  uses another framework. Test behavior at stable interfaces rather than mirroring implementation.
  Documentation-only edits need documentation checks, not unrelated database lifecycle tests.
- If required coverage or a Make target is missing, add it within scope or record it as unresolved.
  Do not claim unavailable tools or unexecuted checks passed. Do not add new dependencies just to
  satisfy an assumed repository-wide Python toolchain.

## Behavioral and failure coverage

Use the relevant cases below, proportional to the tool's behavior and risk:

| Changed boundary | Evidence to obtain |
| --- | --- |
| Parsing and dispatch | Valid inputs work; malformed/missing inputs, unsupported actions/profiles, unsafe paths, and wrong targets fail before mutation. |
| Structured data | Compare meaningful fields and types; cover invalid schemas, missing fields, encoding, and precision where the contract depends on them. |
| CLI and subprocesses | Check exit status and output contracts, command failure, timeout, and safe diagnostics; verify the child receives the intended arguments/environment. |
| State changes | Verify the postcondition, idempotent reruns where promised, pre-existing state, partial failure, recovery, and safe retry behavior. |
| Resource lifetime | Check temporary-file and background-process cleanup after success, failure, and interruption; include setup failures. |
| Credentials | Use synthetic secrets to prove they do not leak through argv, output, exceptions, or artifacts; check sensitive-file permissions and retention policy. |

Mocks or fake adapters help test orchestration and failure paths. They do not prove that SQL,
Kubernetes permissions, or a remote system behaves as assumed; verify those contracts at the
appropriate real boundary when they change.

## Integration and permission checks

- Use disposable databases, containers, namespaces, and synthetic credentials for automated
  mutation tests. Declare prerequisites and pinned images where applicable, bound waits, and
  clean up only resources owned by that run. Never default tests to a shared environment.
- Test allowed and denied operations for the selected role profile using its actual credential.
  Inspect effective privileges, including memberships, ownership, and inherited/default grants
  when relevant. Authentication failure, syntax error, or a read-only transaction rejection does
  not prove privilege denial; assert the intended error, such as PostgreSQL SQLSTATE `42501`.
- Use rollback-safe probes for transactional operations and verify preserved state afterward.
  Operations that cannot be rolled back need disposable resources or a separately authorized
  procedure; do not use destructive live probes as a routine validation shortcut.
- Distinguish disposable results, fake-adapter checks, and live-environment evidence. Passing a
  local harness does not establish deployed TLS, Secret delivery, RBAC, or consumer compatibility.

## Current examples and evidence

Run from the repository root after inspecting the owning recipes:

| Existing target | Scope and limit |
| --- | --- |
| `make postgres-svc-role-test` | Shared dispatch guards and Product reader lifecycle/permissions using fake Kubernetes interactions and disposable PostgreSQL; requires Docker. It does not test future role profiles or a live Kubernetes environment. |
| `make product-baseline-test BASELINE_SUITE=comparator` | HTTP baseline comparator fixtures through the existing shell harness; does not prove a deployed service's parity. |

These are examples, not a mandatory pair for every Python edit. Extend the owning tests when
adding another profile or workflow; do not reuse a reader-only result as evidence for a writer
or migrator.

Record the working directory, exact target and supported variables, interpreter/tool versions
when relevant, tested scope, result, and limitations. Follow the existing ticket post-step evidence
format for ticket work; redact secrets and report blocked or unrun checks separately from passes.
