# Pull Request Review — <ticket-id>

## Review Summary

- Outcome: <Findings identified / No findings identified / Review incomplete>
- Change reviewed: <branch or change identifier and base, when known>
- Working tree: <clean / staged, unstaged, and untracked changes with counts and paths>
- Ticket: <ticket-id>

## Findings

List actionable findings from highest severity to lowest. Give each a unique ID. Point to an exact
file and line in the proposed change whenever possible. If no issues were found, write **None
identified** and do not invent positive findings.

- **<🔴 must fix / discuss | 🟡 should fix | 🟢 nit / observation> <F-001> — <short title>**
  File/line: `<path>:<line>`
  Problem: <concrete defect, missed requirement, test gap, or risk>
  Expected: <required behavior>
  Recommendation: <specific correction or direction>
  Evidence: <ticket criterion, repository rule, source contract, test, or established code pattern>

## Open Questions

- <Unresolved question, why it affects correctness or scope, and needed decision; or “None identified.”>

## Review Coverage and Evidence

- Requirement coverage: <criteria assessed and any gaps>
- Source and contracts: <relevant implementation, callers, schemas, or migrations inspected>
- Tests and validation: <per-check result status and execution source from recorded evidence;
  failed/blocked/not-run checks and “No execution evidence reviewed” when appropriate. Do not
  infer execution from plans.>
- Unverified areas and residual risk: <specific limits, or “None identified.”>

### Repository-Rule Compliance

List every applicable repository-rule area identified during review. Include the governing root or
nested instruction, rule, workflow, or checklist path. Use **Satisfied**, **Finding identified**, or
**Unverified** for applicable areas. Use **Not applicable** only with a concrete reason. Link
deviations to their finding IDs, and do not claim blanket compliance when any area is unverified.

- Rule area: <process, architecture, generated code, validation, documentation, artifacts, scope,
  etc.>
  Applicable source(s): `<path and section>`
  Status: <Satisfied / Finding identified / Unverified / Not applicable>
  Evidence, finding, or reason: <inspected evidence, finding ID, missing evidence, or concrete
  not-applicable reason>
