<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Python Formatting](#python-formatting)
  - [Policy and scope](#policy-and-scope)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Python Formatting

Apply to changed handwritten auxiliary Python, including approved changed tests and helpers.
Also load [development](python-development.md), [testing](python-test.md), and
[validation](python-validation.md). Formatting and linting are mandatory and distinct from
behavioral testing.

## Policy and scope

- Use a pinned Ruff formatter for layout and a pinned Ruff linter with selected rules
  `E4`, `E7`, `E9`, `F`, `I`, `UP`, and `B`. Use a **100-character line length** and
  Ruff's Python 3.14 target. `I` owns import sorting; configure known first-party imports
  for each owner so local modules are grouped correctly. Owned configuration MUST declare these
  selections, exclusions, and Ruff 0.17.1 as the exact pinned version. The root `pyproject.toml`
  owns the shared configuration and direct pin; `uv.lock` owns its resolved artifacts. Do not
  hand-sort imports or substitute an IDE-only formatter. Issue-174 Step 5 implements the owning
  format and lint targets; those executable bindings remain unresolved until then.
- Owning Make targets MUST apply the same configuration to explicitly selected changed
  handwritten files, including new untracked files and intentional test changes. Exclude
  generated/vendor/third-party code and data fixtures from incidental formatting. Do not sweep
  unrelated working-tree changes into selection. Repository-wide checks are appropriate only
  when the owner declares that scope and existing failures are accounted for.
- Run formatting first, review its diff, then run non-mutating format-check and lint. Format-check
  MUST compare selected content without relying on a clean `git diff`, staging, committing,
  resetting, or stashing. It MUST reject drift in new handwritten files in an edited checkout.
  Check idempotence when introducing or changing the pipeline.

Use owning Make targets and the declared environment's Ruff executable. A missing target, pin,
configuration, or tool is a blocked check; do not substitute an arbitrary executable on `PATH`.
Ruff success proves source conformance only. Record the exact file set and tool identity in
[validation evidence](python-validation.md#evidence-limits).
