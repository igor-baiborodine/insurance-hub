<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Python Testing Examples](#python-testing-examples)
  - [Example map](#example-map)
  - [Run the examples](#run-the-examples)
  - [Scenario structure](#scenario-structure)
  - [Adaptation boundaries](#adaptation-boundaries)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Python Testing Examples

These executable examples show the repository's auxiliary Python conventions without defining an
Insurance Hub business contract. Read the canonical [development](../../rules/python-development.md),
[testing](../../rules/python-test.md), [formatting](../../rules/python-formatting.md), and
[validation](../../rules/python-validation.md) rules before adapting them.

## Example map

| Path | Boundary demonstrated | Evidence limit |
| --- | --- | --- |
| `example_cli.py` | Import-safe CLI, validated configuration, owned process group, timeout, interruption, redaction, atomic output, and cleanup | Example policy only; no real service or administrative target |
| `tests/conftest.py` | Focused pytest fixtures and a recording fake command boundary | Fake calls do not prove subprocess or operating-system behavior |
| `tests/test_example_cli.py` | Parametrization, `tmp_path`, `monkeypatch`, exception assertions, failure cleanup, and Given/When/Then phases | Fast in-process behavior only |
| `integration/test_disposable_cli.py` | Explicitly selected real subprocess and filesystem boundary using disposable paths | No network, database, cluster, or full acceptance evidence |

Every Python file in this directory is executable or collected by an owning Make target. Shell
commands and placeholder paths in this README are illustrative instructions, not behavioral
evidence. There are no syntax-only Python fragments in this example set.

## Run the examples

Provision the pinned environment first. The aggregate is non-mutating and runs formatting check,
lint, default no-path collection, and the explicit fast suite:

```bash
make python-toolchain-setup
make python-examples-check
```

The disposable integration boundary is separate and must be selected explicitly:

```bash
make python-examples-test-integration
```

Default collection uses `pyproject.toml`'s explicit fast `testpaths`. It does not visit the
integration directory or any owner acceptance wrapper. `python-test-acceptance` additionally
requires both `PYTHON_ACCEPTANCE_CONFIRM=yes` and an explicit repository-relative
`PYTHON_ACCEPTANCE_PATH`; this examples folder supplies no substitute for a real owner's full
acceptance workload.

## Scenario structure

Nontrivial tests use exact lowercase `# given`, `# when`, and `# then` comments. Parametrization
supplies the given phase in the report-contract cases, so those bodies begin at `# when`.
`pytest.raises` examples use the permitted combined `# when / then` form. The trivial callable
test omits artificial phase comments. Cleanup lives in `finally` or pytest-owned temporary paths;
`test_cleanup_runs_when_an_assertion_fails` demonstrates cleanup after a controlled assertion
failure.

The fast suite uses a recording fake at the command boundary. The integration suite invokes the
CLI and its disposable helper with the selected CPython interpreter, protected stdin, bounded
timeouts, isolated files, redacted diagnostics, and cleanup checks. Its interruption cases send
SIGINT and SIGTERM while the helper is active, then verify status 130 or 143, process-group
shutdown, reaping, and partial-file cleanup. Neither import nor collection starts a subprocess or
creates infrastructure.

## Adaptation boundaries

- Keep the owner's paths, CLI statuses, outputs, time budgets, and side effects. Do not copy the
  example's fictional inspector contract into production tooling.
- Use the smallest fast fake that proves decisions and orchestration. Add an explicitly selected
  disposable integration test for the real parser, process, storage, or transport boundary.
- Preserve full acceptance commands, workloads, observation windows, and thresholds. Run them only
  through their owning explicit target and record their evidence separately.
- Operational modules must import and run without pytest or Ruff installed. Tests and developer
  tools remain development dependencies supplied by the repository environment.
