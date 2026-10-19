<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Python Development](#python-development)
  - [Language and ownership](#language-and-ownership)
  - [Runtime and program boundary](#runtime-and-program-boundary)
  - [Subprocesses, deadlines, and progress](#subprocesses-deadlines-and-progress)
  - [Resource lifetime and security](#resource-lifetime-and-security)
  - [Administrative operations](#administrative-operations)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Python Development

Apply to handwritten auxiliary Python and its CLI, configuration, Make, and administrative
interfaces. Also load [testing](python-test.md), [formatting](python-formatting.md), and
[validation](python-validation.md). Discover applicable nested instructions through
[AGENTS.md](../../AGENTS.md).

## Language and ownership

- Keep a short readable sequence of established CLI commands or caller-shell environment changes
  in shell or Make. Use Python for structured JSON/YAML/XML handling, substantive validation,
  branching orchestration, state tracking, retries, deadlines, and resource cleanup. Judge behavior
  rather than length. Retain necessary thin shell interfaces; Python need not replace `helm`,
  `kubectl`, `docker`, or `psql` with SDKs.
- Keep service policy and scripts with their owner. Share process mechanics only when demonstrated
  reuse warrants it. Do not put administrative tooling or elevated credentials in application
  images or startup, or add a Python business service.
- Use owning Make targets for repeatable setup, checks, tests, and operations. Inspect their
  prerequisites and side effects before invocation. Keep checks separate from mutations; apply
  [Makefile rules](makefile-rules.md) when changing targets.
- Use importable `snake_case.py` names. Preserve supported historical paths with thin launchers
  or update every caller with an explicit compatibility decision. Do not use dynamic file-path
  imports to work around new hyphenated module names.

## Runtime and program boundary

- The selected auxiliary baseline is managed **CPython 3.14.8** in an isolated project `.venv`.
  Setup MUST select and verify that exact version. Normal operations MUST NOT install packages,
  silently use system Python, or fall back to another release. Preserve Ubuntu's system Python.
  A baseline change requires an explicit recorded decision. Python children MUST use
  `sys.executable`. Owning Make targets MUST select an overrideable interpreter, resolve the
  project environment from their own location, and reject missing or wrong versions before
  affected work, including recursive calls. See [validation](python-validation.md).
- Declare runtime dependencies with an owner and reproducible resolution. Prefer the standard
  library; justify packages such as YAML parsers. Keep pytest and Ruff in development/test
  dependencies, not operational imports. Record the interpreter path, implementation, version,
  and managed-build identity in setup evidence. Exact uv/pytest/Ruff pins and dependency workflow
  are finalized in issue-174 Step 4. Provision with uv from declared inputs on documented
  supported OS/architecture combinations; record exact resolved transitive dependencies. A
  security update MUST deliberately revise pins/resolution, recreate or update the environment,
  rerun affected checks, and record results. Claim bit-for-bit reproducibility only when artifact
  hashes and platform inputs are controlled.
- Structure a program as **parse arguments → validate configuration → execute → report** with
  explicit `main(argv) -> int`. Imports MUST NOT start commands, contact services, or mutate state.
  Separate pure decisions from I/O, pass dependencies explicitly, use focused functions and
  meaningful type annotations, and introduce classes only for real state or ownership. Validate
  external values at runtime; type hints and `assert` do not validate operational input.
- Use `argparse` by default. Document arguments, environment, defaults, target selection, side
  effects, output schema, and exit behavior. Validate configuration before mutation. Put machine
  results on stdout and diagnostics/progress on stderr. New CLIs MUST use 0 success, 1 operational
  failure, 2 usage error, 130 SIGINT/user interruption, and 143 handled SIGTERM on supported POSIX
  systems. Document outer Make/wrapper mapping separately. Existing incompatible statuses require
  an explicit compatibility decision before changing them.
- Use `pathlib`, native structured parsers, and typed interfaces. Check fields and types; stream
  large files/output where appropriate. MUST NOT replace structured parsing with text pipelines.
  Handle expected errors at the CLI boundary with context and exception chaining; sanitize
  unexpected-error diagnostics. MUST NOT swallow a failure or report success after a failed stage.

## Subprocesses, deadlines, and progress

- Invoke commands with argument vectors and `shell=False`; validate downstream options, SQL,
  expressions, and paths. Check outcomes. Give each external command an explicit timeout and
  control inherited variables that affect target, credentials, or transport. Capture only bounded
  small structured output; stream long-running output and protect sensitive output. Own background
  processes explicitly. NEVER log secret-bearing arguments, including on timeout.
- Finite multi-step, polling, or long-running workflows MUST document an overall deadline and
  phase budgets selected from their contract. Use monotonic time and pass remaining budgets to
  child commands, HTTP requests, retries, and sleeps. Preserve required acceptance windows.
  Continuous or interactive operations need a documented exception and cancellation behavior.
  Teardown has a separate bounded budget that survives work-deadline expiry.
- Phases expected to exceed 30 seconds MUST report start/end and a stderr heartbeat at least
  every 30 seconds, including while a child runs. Include activity and elapsed time; report
  progress percentage or ETA only when measured. Document protocol/quiet-mode exceptions;
  quiet mode never hides failure diagnostics. On timeout, report phase context and the last
  relevant sanitized observation.
- Retry only transient failures when repetition is known safe. Terminating a local command does
  not prove a remote mutation failed; inspect the remote postcondition before retrying.

## Resource lifetime and security

- Own children and, where descendants are created, their process groups. On failure, deadline,
  SIGINT, or SIGTERM, terminate and reap owned processes. Bounded graceful shutdown may precede
  forced termination; bound reaping within the teardown budget. Interruption MUST NOT become
  success. Preserve an earlier operational failure and report cleanup failures separately.
- Register a uniquely identified resource before an operation that can partially create it.
  Capture useful diagnostics before teardown. Clean owned resources in reverse order using context
  managers or `try/finally`, including partial setup. Never prune unrelated resources.
- Keep secrets out of argv, logs, exception diagnostics, result artifacts, and checked-in files.
  Document the approved input channel per external CLI, preferring protected stdin or credential
  files where supported. Create sensitive temporary files with restrictive permissions from the
  outset; document retention and cleanup. Bind SQL values when a driver supports it; otherwise
  use a tested receiving-tool value mechanism. Constrain identifiers with an allowlist and
  separate quoting; shell quoting is not SQL quoting. A
  synthetic sentinel supplied as test input may appear at that input boundary but MUST NOT leak
  into diagnostic/output channels. Existing unsafe invocation requires explicit remediation;
  documentation alone does not authorize a weaker channel. Any exception needs user authorization
  under repository precedence, exposure limits, and verification evidence.

## Administrative operations

- Require explicit target coordinates, such as Kubernetes context/namespace or database identity,
  and verify postconditions. Reject wrong service/profile pairs and broad fallback defaults.
  Preserve GitOps ownership, role-specific permissions, confirmation gates, and partial-failure
  recovery. `--yes` skips confirmation only; never target or safety guards.
- Administrative `--dry-run` MUST NOT mutate the target or create disposable target resources.
  Document permitted local writes, locations, and cleanup/retention; undeclared local writes are
  prohibited. Separately named server validation MUST disclose target contact and side effects.
  Bootstrap, tests, and administrative operations MUST declare their side effects. Disposable
  resource creation is not a read-only check.
- Define rerun and recovery behavior for partial success. Shared-environment mutation still
  requires authorization and target safeguards; passing a local test does not authorize it.

Review each requirement against owner and caller contracts. The
[validation rule](python-validation.md#requirement-verification) maps requirements to automated
checks and review evidence; historical scripts are not proof of compliance.
