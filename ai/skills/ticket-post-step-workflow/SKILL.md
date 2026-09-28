---
name: ticket-post-step-workflow
description: Use after a concrete ticket delivery step has been completed. This workflow updates the delivery tracker, creates a per-step summary, and refreshes the ticket git diff artifact.
---

# Ticket Post-Step Workflow

Use this skill only after a concrete delivery step has been completed.

## Required Inputs

- Ticket ID, for example `issue-45`
- Current step number
- Current branch name
- Short step summary or title

## Required Post-Step Actions

Perform these actions in order:

1. Update `ai/artifacts/<ticket>/<ticket>-delivery-steps.md`
   - mark the completed step as done
   - add concise findings or decisions for that step
   - record the step artifact filename

2. Create the per-step markdown summary:
   - `ai/artifacts/<ticket>/<ticket>-step-<nn>-<summary>.md`
   - include what changed or was inspected
   - include key decisions
   - include blockers or follow-up for the next step
   - include a `Validation Evidence` section with one row for each applicable check, including
     relevant checks that were not run or are blocked; use the record format below

3. Refresh the git diff artifact:
   - `git diff main <current-branch> > ai/artifacts/<ticket>/<ticket>-git-diff.txt`
   - This snapshot compares committed trees only; staged, unstaged, and untracked work is not
     included. Keep it as the branch-level review artifact and do not represent it as a complete
     working-tree diff.

## Constraints

### Validation evidence record

Keep planned validation in the delivery plan separate from results in the step summary. In each
step summary, record validation in a table with these columns:

| Check | Status | Source | Scope | Command or scenario | Evidence/result | Blocker/next action |
| --- | --- | --- | --- | --- | --- | --- |

- Use one result status: `passed`, `failed`, `blocked`, `not run`, or `not applicable`. Explain
  why a check is blocked, not run, or not applicable. `Proposed` or `planned` describes a future
  check in the delivery plan, never an executed result.
- Record execution source separately as `local`, `CI`, or `manual`; use `—` when the check was
  not executed. Do not infer a local pass from a CI result or vice versa.
- For command-based checks, include the exact invocation, working directory, relevant variables,
  and module/package coverage in the command or scope fields. For CI, identify the workflow/job
  and run or evidence reference when available.
- For manual checks, record the scenario, expected behavior, observed result, and environment or
  prerequisites. Do not claim an observation that was not made.
- Give failed, blocked, and not-run checks a blocker or next action. For `not applicable`, give a
  brief reason. Point to logs/output or summarize the relevant result; preserve useful failure
  evidence in the ticket artifacts.
- At ticket completion, map each acceptance criterion to its supporting evidence and list
  unresolved, CI-only, and manual checks. Do not treat an aggregate check as covering packages or
  modules its evidence does not show.

- Write ticket artifacts only under `ai/artifacts/<ticket>/`.
- Do not create extra documentation files beyond the tracker and per-step summary unless the user asks.
- Do not run `git diff` in another artifact format unless the user asks.
- Keep the per-step summary concise and factual.
- Before any commit, the end user must review the complete proposed change in the working tree,
  including the branch snapshot, staged and unstaged changes, untracked files, and generated files.
  Do not commit until the end user has reviewed and explicitly authorized it; see `AGENTS.md`.

## Output Checklist

Before finishing the turn, confirm that all of the following exist or were updated:

- `ai/artifacts/<ticket>/<ticket>-delivery-steps.md`
- `ai/artifacts/<ticket>/<ticket>-step-<nn>-<summary>.md`
- `ai/artifacts/<ticket>/<ticket>-git-diff.txt`
