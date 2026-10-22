---
name: ticket-post-step-workflow
description: Use after a concrete ticket delivery step has been completed. This workflow updates the delivery tracker, creates a per-step summary, and refreshes the ticket git diff artifact.
---

<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Ticket Post-Step Workflow](#ticket-post-step-workflow)
  - [Required Inputs](#required-inputs)
  - [Required Post-Step Actions](#required-post-step-actions)
  - [Constraints](#constraints)
    - [Validation evidence record](#validation-evidence-record)
  - [Output Checklist](#output-checklist)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Ticket Post-Step Workflow

Use this skill only after a concrete delivery step has been completed.

## Required Inputs

- Ticket ID, for example `issue-45`
- `<ticket-dir>` resolved using [AGENTS.md Artifact Rules](../../../AGENTS.md#artifact-rules)
- Current step number
- Current branch name
- Intended PR base ref from the delivery plan or actual PR target
- Short step summary or title

## Required Post-Step Actions

Reuse the resolved directory from implementation, including its epic grouping, and apply the root
artifact preflight before writing. Perform these actions in order:

1. Update `<ticket-dir>/<ticket>-delivery-steps.md`
   - mark the completed step as done
   - record the step artifact filename
   - keep the `Progress` section limited to status, step title, planned pull request when applicable,
     and the step summary artifact reference; record implementation details, findings, decisions,
     and validation results in the per-step summary instead

2. Create the per-step markdown summary:
   - `<ticket-dir>/<ticket>-step-<nn>-<summary>.md`
   - include what changed or was inspected
   - include key decisions
   - include blockers or follow-up for the next step
   - include a `Validation Evidence` section with one row for each applicable check, including
     relevant checks that were not run or are blocked; use the record format below
   - for behavior changes, identify the specification's AC/example/check IDs supported by each
     applicable validation row; record the independent expected result and observed result, or
     explain why the case remains unverified. Keep bug reproduction and migration baseline fixture
     provenance visible; a generated replacement is not its own oracle
   - capture each check's tested state when it runs; a state reconstructed after later edits is
     unverified, even if the command's historical result is known

3. Refresh the git diff artifact:
   - Select the intended PR base from the delivery plan or actual PR target; for a stacked PR,
     use the immediate target branch. Do not assume `main`. If the target is ambiguous, resolve
     it before refreshing the snapshot. Record whether the chosen local or remote-tracking ref
     is known to be current; a local ref alone does not prove remote freshness.
   - Run from the repository root, substituting the actual refs and artifact path:

     ```sh
     ai/skills/ticket-post-step-workflow/scripts/refresh_git_diff.sh \
       . <intended-base-ref> <current-branch-ref> \
       <ticket-dir>/<ticket>-git-diff.txt
     ```

   - The helper verifies both refs and their merge base before replacing the snapshot. It records
     the intended base ref, resolved base/head/merge-base SHAs, and the committed PR patch equivalent
     to `git diff <base>...<head>`. If it fails, keep the prior snapshot and report the blocker;
     never describe that prior snapshot as current evidence.
   - Record staged, unstaged, and relevant untracked work separately with `git status --short`,
     `git diff --cached`, `git diff`, and direct inspection. The snapshot excludes those changes;
     record its identity and that limitation in the step summary.

## Constraints

### Validation evidence record

Keep planned validation in the delivery plan separate from results in the step summary. In each
step summary, record validation in a table with these columns. A row may reference a named state
record in the same summary when multiple checks used exactly the same inputs; each row must name
its state explicitly and record any check-specific input or environment difference.

| Check | Status | Source | Scope | Command or scenario | Tested state | Run evidence/result | Freshness/next action |
| --- | --- | --- | --- | --- | --- | --- | --- |

- Use one result status: `passed`, `failed`, `blocked`, `not run`, or `not applicable`. Explain
  why a check is blocked, not run, or not applicable. `Proposed` or `planned` describes a future
  check in the delivery plan, never an executed result.
- Record execution source separately as `local`, `CI`, or `manual`; use `—` when the check was
  not executed. Do not infer a local pass from a CI result or vice versa.
- For each executed check, `Tested state` must resolve to the intended base ref and full SHA,
  checked head ref and full SHA, specification path and SHA-256 digest, and whether the worktree
  was clean or dirty at execution. For a dirty tree, include an inline manifest of relevant
  staged, unstaged, and untracked inputs: path, state, and staged blob ID and/or worktree SHA-256
  digest as applicable. Record deletions explicitly. Include relevant test fixtures, generated
  inputs, configuration, and dependencies. Record excluded local changes and why they cannot affect
  the check. A branch name or committed diff alone does not identify dirty inputs.
- Capture tested-state identity at execution time. If a required ref, digest, or dirty input
  identity was not captured, label final-state applicability `unverified`; do not reconstruct it
  from a later tree or quietly assume the tree was clean. For a check outside a Git or ticket
  boundary, use `not applicable` with a reason for the corresponding identity field and record
  the actual source/specification identity that governed it.
- In `Run evidence/result`, record the UTC execution time, relevant tool and environment identity
  (including versions or CI job when material), a local log/artifact or CI run reference, and the
  observed outcome. A concise, sanitized output excerpt in the summary may serve as the local
  reference when no log is retained. Do not store secrets or full transcripts by default.
- For command-based checks, include the exact invocation, working directory, relevant variables,
  and module/package coverage in the command or scope fields. For CI, identify the workflow/job
  and run or evidence reference when available.
- For manual checks, record the scenario, expected behavior, observed result, and environment or
  prerequisites. Do not claim an observation that was not made.
- Give failed, blocked, and not-run checks a blocker or next action. For `not applicable`, give a
  brief reason. Point to logs/output or summarize the relevant result; preserve useful failure
  evidence in the ticket artifacts.
- Before step or ticket completion, compare each executed row's head, specification digest,
  relevant input manifest, and material tool/environment inputs with the proposed final state.
  A later change to a check's source, tests, fixtures, generated inputs, configuration,
  dependencies, or governing acceptance criteria requires reassessment. Mark the historical
  result `stale` for the final state until rerun or a documented unaffected judgment tied to the
  exact change and check scope; a material change that can affect the outcome needs a rerun. Keep
  its original execution status and identity visible. If impact cannot be established, mark final
  applicability `unverified` and obtain new evidence. Record reruns as new rows or clearly
  versioned results, never overwrite the historical result.
- Historical records that lack these identity fields remain readable as historical observations,
  but have `unverified` final-state applicability. Do not use them alone to support a current-code
  pass. In `Freshness/next action`, use `current`, `stale`, or `unverified` for executed checks and
  state the rerun or unaffected judgment needed; use `—` for checks never executed.
- At ticket completion, map each acceptance criterion to its supporting evidence and list
  unresolved, CI-only, and manual checks. For behavior changes, use a concise trace from each
  important requirement/AC/example ID through its affected boundary and test/manual scenario ID
  to the observed validation row and final-state applicability. Show representative success, failure,
  and compatibility/invariant cases; explain a missing category when inapplicable. Record whether
  a relevant behavior mutation was shown to fail the corresponding check, or why that sensitivity
  was not exercised and what evidence remains. Property or differential checks should be tied to
  the invariant they test. A justified manual scenario names the reason automation was unsuitable,
  its expected result, and its actual observation. Do not treat an aggregate check as covering
  packages or modules its evidence does not show.

- Write ticket artifacts only under the resolved `<ticket-dir>`.
- Do not create extra documentation files beyond the tracker and per-step summary unless the user asks.
- Keep the snapshot in the standard `.txt` artifact with the helper's identity header and patch;
  do not create other diff artifacts unless the user asks.
- Keep the per-step summary concise and factual.
- Before any commit, inspect the complete proposed change in the working tree, including the
  branch snapshot, staged and unstaged changes, untracked files, and generated files. Apply the
  default end-user review and explicit commit authorization requirement, or an applicable explicit
  session-start permission, as defined in `AGENTS.md`. Push requires its own authorization.

## Output Checklist

Before finishing the turn, confirm that all of the following exist or were updated:

- `<ticket-dir>/<ticket>-delivery-steps.md`
- `<ticket-dir>/<ticket>-step-<nn>-<summary>.md`
- `<ticket-dir>/<ticket>-git-diff.txt`
