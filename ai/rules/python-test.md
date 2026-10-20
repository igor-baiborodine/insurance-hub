<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Python Testing](#python-testing)
  - [Files and discovery](#files-and-discovery)
  - [Scenarios and assertions](#scenarios-and-assertions)
  - [Isolation and boundaries](#isolation-and-boundaries)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Python Testing

Apply to new auxiliary Python tests and approved changed tests. Also load
[development](python-development.md), [formatting](python-formatting.md), and
[validation](python-validation.md). Use pytest through the owning Make target and the selected
CPython 3.14.8 environment via `python -m pytest`. Operational CLIs MUST run without pytest
installed. Start with pinned pytest alone; add a plugin only for a documented concrete need.

## Files and discovery

- Use descriptive `test_*.py` files and `test_*` functions. Preserve owner-local locations
  and supported callers; a rename MUST NOT silently drop coverage or break fixture lookup.
- Configure discovery and registered `integration` and `acceptance` markers centrally in
  `pyproject.toml` with strict marker validation. Imports and collection MUST NOT start
  infrastructure or mutate targets. Default discovery with no explicit paths MUST exclude full
  acceptance wrappers through explicit configuration; markers alone are insufficient. Verify
  collection separately from execution.
- Fast, disposable integration, and full acceptance suites MUST have separate owning Make
  targets. Fast targets MUST select their scope explicitly. Slow suites require explicit selection
  and documented prerequisites. A long-running acceptance runner may remain an ordinary CLI;
  test its constituent behavior with pytest. The shared interfaces are `python-test-collect` for
  no-path default collection, owner-specific fast targets such as `python-examples-test-fast`,
  explicit disposable integration targets such as `python-examples-test-integration`, and the
  guarded `python-test-acceptance` interface with an owner path and confirmation.

## Scenarios and assertions

- Test observable contracts, not internal call sequences. Use descriptive names and
  `pytest.mark.parametrize` for related case matrices. Derive expected values independently of
  the production transformation. Ordinary `assert`, `pytest.raises`, and `pytest.approx`
  are appropriate in tests; production input validation MUST NOT rely on `assert`.
- For each nontrivial scenario, separate setup, triggering action, and observations with exact
  lowercase `# given`, `# when`, and `# then` comments when those phases occur in the test
  body. Put inputs, configuration, fakes, and expected values under `# given`; the call under
  `# when`; assertions, side effects, and call verification under `# then`.
- A fixture or parametrization may supply the given phase without a placeholder comment or
  duplicate setup. Exception assertions may use `# when / then` around `pytest.raises`; add
  `# then` when checking exception attributes or further outcomes. Omit markers in shared
  helpers and trivial tests where they add no clarity. Do not invent variables or empty blocks.
- Keep cleanup in fixture teardown, context managers, or `try/finally` so it runs when an
  assertion fails. Review MUST confirm meaningful phases and justified omissions; comment counts
  or lint success cannot establish this. The [Python testing examples](../examples/python-testing/README.md)
  illustrate this rule without adding policy.

## Isolation and boundaries

- Use `tmp_path` for isolated files, `monkeypatch` for reversible environment and boundary
  substitutions, and focused fixtures for shared setup/teardown. Mock external boundaries rather
  than every internal function. Control time and scheduling instead of fragile sleeps; bound
  blocking work and clean it up reliably.
- Cover wrong targets, invalid inputs, malformed output, command failure, deadlines, partial
  setup, interruption, cleanup failures, synthetic-secret redaction, and administrative dry-run
  where applicable. A synthetic sentinel may be supplied as test input; assert it is absent from
  diagnostics and result artifacts.
- Pure/fake-adapter tests prove decisions and orchestration. Disposable integration with actual
  external tools establishes SQL, permissions, transport, and resource behavior. Declare
  prerequisites and side effects; never default to a shared environment. Preserve full
  acceptance workloads and thresholds. A skip or absent prerequisite is not a pass for a
  required gate.
