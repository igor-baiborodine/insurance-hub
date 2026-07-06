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

3. Refresh the git diff artifact:
   - `git diff development <current-branch> > ai/artifacts/<ticket>/<ticket>-git-diff.txt`

## Constraints

- Write ticket artifacts only under `ai/artifacts/<ticket>/`.
- Do not create extra documentation files beyond the tracker and per-step summary unless the user asks.
- Do not run `git diff` in another artifact format unless the user asks.
- Keep the per-step summary concise and factual.

## Output Checklist

Before finishing the turn, confirm that all of the following exist or were updated:

- `ai/artifacts/<ticket>/<ticket>-delivery-steps.md`
- `ai/artifacts/<ticket>/<ticket>-step-<nn>-<summary>.md`
- `ai/artifacts/<ticket>/<ticket>-git-diff.txt`
