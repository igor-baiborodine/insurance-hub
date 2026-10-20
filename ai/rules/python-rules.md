<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Python Rules](#python-rules)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Python Rules

Python is an auxiliary language for repository maintenance, deployment and administrative
helpers, structured-data validation, and test harnesses. It does not change the Java-to-Go
business-service migration or module ownership.

For auxiliary Python implementation or review, read and apply **all four** linked rules:

- [Python development](python-development.md): language choice, ownership, program and CLI
  contracts, process lifetime, security, and administration.
- [Python testing](python-test.md): pytest design, discovery, scenario structure, isolation, and
  unit/integration/acceptance boundaries.
- [Python formatting](python-formatting.md): mandatory Ruff format and lint policy and scope.
- [Python validation](python-validation.md): changed-boundary checks, execution order, and evidence.

Use the owning Make targets for repeatable Python development and validation. A missing target or
toolchain is an unresolved prerequisite, never evidence of a pass. The repository selects uv
0.13.1, pytest 9.1.1, Ruff 0.17.1, and CPython 3.14.8 through `pyproject.toml`, `uv.lock`, and
`.python-version`. Use `python-toolchain-setup` only for explicit provisioning,
`python-runtime-check` for operational-runtime selection, and `python-toolchain-check` for the
locked development environment. Issue-174 Step 5 adds the owning format, lint, collection, and
test targets; do not claim those checks exist or pass before that step is delivered.

Apply [Naming rules](naming-rules.md) to new or renamed paths, and applicable language and
[Makefile rules](makefile-rules.md) when those boundaries change. This entry point creates no
separate instruction precedence; follow [AGENTS.md](../../AGENTS.md).
