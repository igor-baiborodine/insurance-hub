<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Python Development](#python-development)
  - [Choose the language for the task](#choose-the-language-for-the-task)
  - [Ownership and execution interface](#ownership-and-execution-interface)
  - [Runtime, dependencies, and code style](#runtime-dependencies-and-code-style)
  - [Subprocesses and resource lifetime](#subprocesses-and-resource-lifetime)
  - [Administrative operations and secrets](#administrative-operations-and-secrets)
  - [Repository reference points](#repository-reference-points)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Python Development

Apply to auxiliary Python scripts and their Make, configuration, and test interfaces. Read
[Python validation](python-validation.md) alongside this rule. Discover applicable nested
instructions using [AGENTS.md](../../AGENTS.md) before editing.

## Choose the language for the task

| Task characteristics | Preferred approach |
| --- | --- |
| A short, linear sequence of existing CLI commands, straightforward pipes, or environment setup | Keep Bash and existing command-line tools. |
| Structured JSON/XML processing, substantial validation, branching, state tracking, recovery, or independently testable orchestration | Prefer Python when it makes the workflow clearer and easier to verify. |
| Business logic, service APIs, or long-running application components | Follow the service's existing language and migration plan; auxiliary tooling does not authorize a Python service. |

- Choose based on behavior and maintenance needs, not script length alone. Python is not
  inherently safer than Bash; explicit boundaries, subprocess handling, and tests establish safety.
- Do not rewrite working shell scripts solely for language consistency. Keep thin shell wrappers
  where useful; move complex embedded Python or shell logic into an owned, testable script when
  the task calls for it.
- Python may orchestrate established CLIs such as `kubectl` and `psql`; adopting it does not require
  replacing those tools with SDKs or database drivers.

## Ownership and execution interface

- Place shared tooling in the relevant `scripts/` subtree and service-specific helpers beside
  their existing owner. Follow neighboring layout rather than creating a generic Python platform.
  Share dispatch and environment mechanics only where needed; keep service policy with its owner.
- Keep administrative tooling, elevated credentials, and test fixtures out of application runtime
  images and startup paths. A helper must not make provisioning or migration an implicit service
  startup action.
- Use the owning Makefile targets for repeatable development, validation, and administrative
  operations. Inspect recipes, prerequisites, working directory, and supported variables first.
  Shell/Python scripts can implement recipes; developer and CI entry points should use the same
  targets. Follow [CONTRIBUTING.md](../../CONTRIBUTING.md#makefile).
- If a required target is missing, add or extend the smallest suitable target within task scope;
  otherwise report the missing capability. Do not bypass a target's guards by invoking its script
  directly. Read-only source inspection does not require a Make target.
- Keep inspect/check/test targets separate from provisioning, rotation, revocation, publication,
  and deployment. Inspect prerequisites for hidden mutations. Document temporary resources or
  transactional probes even when the target leaves no persistent change. Do not claim that a
  target name or `make -n` alone proves safety.

## Runtime, dependencies, and code style

- Use Python 3 and document the minimum supported version for the affected tool based on its
  syntax and standard-library APIs. Check the owning documentation, execution environment, and
  CI before adopting newer features; do not infer compatibility from the author's interpreter.
- Prefer the standard library for these small tools. Add third-party packages only for a concrete
  need with a reproducible, scoped dependency setup and validation path. Reuse existing manifests
  and version constraints; do not add a repository-wide package manager or framework by default,
  install into system Python, or install dependencies during an administrative operation.
- Follow local formatting; use four-space indentation, descriptive `snake_case` functions, small
  cohesive functions, and type annotations on nontrivial interfaces. Reuse existing formatter,
  linter, or type-checker configuration where present. Do not introduce a style tool merely to
  reformat unrelated scripts.
- Keep parsing, validation, and policy decisions separable from external I/O. Put execution behind
  `main()` or an `if __name__ == "__main__"` guard so imports do not perform administrative work.
- Use explicit CLI arguments or documented environment variables; validate actions, required
  values, paths, and structured inputs before mutation. Reject unsupported service/profile pairs
  and malformed catalogs instead of falling back to broader defaults.
- Define useful exit statuses and concise, actionable diagnostics. Separate machine-readable
  output from diagnostics on stderr. Handle expected failures at the CLI boundary; do not swallow
  exceptions, print success after a failed stage, or expose sensitive exception payloads.

## Subprocesses and resource lifetime

- Pass subprocess arguments as lists with `shell=False`; avoid command-string interpolation and
  `os.system`. Argument lists prevent shell expansion, but still validate values interpreted as
  options, paths, SQL, or expressions by the receiving program.
- Check return codes and use explicit timeouts for external commands and readiness loops. Treat
  timeout, command absence, malformed output, and a nonzero exit as distinct failures where useful.
- Parse structured CLI output with an appropriate parser rather than scraping human-readable
  tables. Preserve types and verify expected fields before using the result.
- Control inherited environment variables where they can change the selected target, credentials,
  or transport settings. Use `sys.executable` when starting another Python helper in the same
  execution environment.
- Give temporary files, connections, and background processes an owner and cleanup path, including
  partial setup failures and interruption. Use context managers or `try/finally`; terminate and
  wait for owned child processes. Preserve the original failure if cleanup also fails.
- Bound retries and use them only for operations known to be safe to repeat. A timeout can mean
  that a remote mutation completed; inspect state before retrying it.

## Administrative operations and secrets

- Validate explicit environment and resource coordinates before mutation, including Kubernetes
  context/namespace or database identity where applicable. An ambient current context alone is
  not sufficient authorization or target proof. Preserve existing GitOps and resource ownership.
- Define the intended state, rerun behavior, failure boundaries, and recovery procedure. For
  multi-system changes such as a database password plus a Kubernetes Secret, handle partial
  success explicitly; do not imply atomicity across those systems. Verification should establish
  the postcondition, not merely accept successful command exits.
- Keep credentials out of argv, logs, tracebacks, checked-in files, and validation artifacts. Use
  protected stdin or an established credential-file mechanism where supported. Create sensitive
  temporary files with restrictive permissions from the outset, clean them up, and document any
  intentionally retained recovery file. Base64 encoding is not redaction.
- Avoid interpolating untrusted SQL. Use parameter binding when a driver is already used, or the
  receiving tool's safe value/identifier mechanisms. Where binding is unavailable, constrain
  identifiers and isolate tested quoting for the supported values. Do not confuse shell quoting,
  SQL literals, and SQL identifiers.
- Derive privileges and probes from each role profile's contract. Reader, writer, and migrator
  profiles may legitimately require different permissions; do not generalize Product reader
  restrictions to all identities. Keep grants and consumer-impact decisions service-owned.
- Preserve existing authorization and confirmation gates for destructive operations. Adding a
  script or passing its tests does not authorize its execution against a shared environment.

## Repository reference points

The [shared role dispatcher](../../scripts/postgres/service/dispatch.py) and
[Product reader implementation](../../scripts/product-deployment/reader.py) illustrate shared
selection with service-owned administration. The
[HTTP baseline comparator](../../scripts/product-baseline/compare-http.py) illustrates auxiliary
structured-data validation. Inspect them for local conventions, not as universal templates or
proof that every requirement above is already enforced.
