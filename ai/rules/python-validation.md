<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Python Validation](#python-validation)
  - [Selection and order](#selection-and-order)
  - [Changed-boundary evidence](#changed-boundary-evidence)
  - [Requirement verification](#requirement-verification)
  - [Evidence limits](#evidence-limits)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Python Validation

Apply [development](python-development.md), [testing](python-test.md), and
[formatting](python-formatting.md). Use owning Make targets after inspecting recipes,
prerequisites, selected interpreter, file selection, and side effects. Exact targets and
toolchain arrive in issue-174 Steps 4–5; missing targets are blocked checks, not passes.

## Selection and order

1. Identify changed Python, tests, wrappers, Make callers, configuration, and contracts. Record
   selected CPython executable/version. Check import safety and interpreter selection at root,
   owner-local, recursive, and supported override paths when affected.
2. Run the owning format target on changed handwritten Python, review its diff, then run
   non-mutating format-check and lint. Follow [formatting](python-formatting.md) for scope and
   exclusions. Syntax or lint success does not prove behavior.
3. Run the smallest owning fast pytest target that covers behavior and failure paths. Check
   collection separately when discovery/markers change; verify default no-path discovery excludes
   full acceptance. Run disposable integration when a real external boundary changed.
4. Run affected full acceptance only through explicit targets when harness behavior or measurements
   change; preserve workload, observation window, thresholds, and final-code evidence. Rerun
   affected evidence after a material late fix.
5. Run changed Markdown's TOC format/check and applicable Make/module checks. Review artifact
   ignore/index state for ticket work. Record unrun prerequisites and limits.

Do not execute an administrative mutation merely to validate code. Validate guards and dry-run
with fakes and use disposable resources for integration. Follow the
[testability assessment](repo-rules.md#testability) when fixture wiring becomes complicated.

## Changed-boundary evidence

| Changed boundary | Required evidence when applicable |
| --- | --- |
| Pure decisions and CLI | Valid/invalid arguments, output/status contracts, malformed data, wrong target before mutation. |
| Subprocesses and workflows | Arguments/environment, nonzero exit, timeout, remaining deadline, heartbeat, interruption, process-group cleanup. |
| State and resources | Partial setup, postconditions, safe rerun/retry, reverse cleanup, cleanup failure, dry-run writes. |
| Secrets and permissions | Synthetic sentinel absent from output/diagnostics/artifacts, approved input channel, file modes, role-specific allowed/denied operations. |
| Real integration | Disposable target, actual parser/SQL/transport/permissions as applicable, owned cleanup, bounded waits. |
| Acceptance harness | Original workload/budgets, measurement overhead, final-code full run, evidence limits. |

Fake-adapter tests do not prove remote contracts, privileges, deployed TLS, Secret delivery, or
full-duration acceptance. Authentication or read-only rejection does not prove a database
privilege denial; inspect the intended error (PostgreSQL SQLSTATE `42501`) at a disposable
boundary. Historical results are baseline only.

## Requirement verification

| ID and policy section | Verification method |
| --- | --- |
| PY-DEV-01 [Language and ownership](python-development.md#language-and-ownership) | Changed-file/caller review; inspect owner, Make boundary, shell exception, and compatibility decision. |
| PY-DEV-02 [Runtime and program boundary](python-development.md#runtime-and-program-boundary) | Import/CLI tests and review of phases, parsing, data validation, stdout/stderr, statuses, and dependencies. |
| PY-DEV-03 [Subprocesses, deadlines, and progress](python-development.md#subprocesses-deadlines-and-progress) | Budget/command review; fake-command and disposable process tests for failure, deadline, heartbeat, retry. |
| PY-DEV-04 [Resource lifetime and security](python-development.md#resource-lifetime-and-security) | Ownership/exposure review; interruption, cleanup, redaction, file-mode, and partial-setup tests. |
| PY-DEV-05 [Administrative operations](python-development.md#administrative-operations) | Target, side-effect, and recovery review; guard/dry-run tests and disposable permission checks where needed. |
| PY-TEST-01 [Files and discovery](python-test.md#files-and-discovery) | Strict-marker configuration, default no-path collection, and separate explicit fast/integration/acceptance targets. |
| PY-TEST-02 [Scenarios and assertions](python-test.md#scenarios-and-assertions) | Scenario and Given/When/Then review plus meaningful pytest results; comment count alone is insufficient. |
| PY-TEST-03 [Isolation and boundaries](python-test.md#isolation-and-boundaries) | Fixture/teardown review, fake-adapter tests, disposable integration, and acceptance evidence as applicable. |
| PY-FMT-01 [Policy and scope](python-formatting.md#policy-and-scope) | Pin/configuration and selected-file review; format, non-mutating format-check, and lint targets. |
| PY-RUN-01 [Runtime and program boundary](python-development.md#runtime-and-program-boundary) | CPython 3.14.8 setup/identity, lock/resolution, and representative root/owner/recursive/override checks. |

## Evidence limits

Record exact command, working directory, relevant variables, interpreter/tool identities, source
(local, CI, or manual), covered files/modules, result, and limitations. Mark unavailable, failed,
blocked, or unrun checks honestly; do not infer a local pass from CI or a live pass from fakes.
The [ticket post-step workflow](../skills/ticket-post-step-workflow/SKILL.md) defines the ticket
evidence table. Formatting, syntax, collection, and mock-only results are not behavioral
integration evidence.
