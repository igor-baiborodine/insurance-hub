<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Local pull request description drafting](#local-pull-request-description-drafting)
  - [Required inputs](#required-inputs)
  - [Collect the proposed change](#collect-the-proposed-change)
  - [Validation notes](#validation-notes)
  - [Output](#output)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Local pull request description drafting

Use this mode from a local checkout when the requested output is the repository's local PR
description artifact.

## Required inputs

- Ticket ID and repository root.
- `<ticket-dir>/<ticket>-ticket-content.md`.
- Existing delivery tracker, completed step summaries, branch diff snapshot, and any explicitly
  supplied investigation or review artifacts.
- `ai/templates/pr-description-template.md`.

Resolve `<ticket-dir>` using [AGENTS.md Artifact Rules](../../../../AGENTS.md#artifact-rules),
including existing standalone or epic-grouped locations. Use the same directory for all inputs
and outputs below. Before writing an artifact, perform the root artifact preflight. If the
ticket-content file is missing or empty, follow that guide rather than inferring requirements from
the ticket ID.

## Collect the proposed change

1. Read the complete ticket specification and shared PR template. Read the delivery tracker and
   completed step summaries to understand delivered scope and recorded validation.
2. Identify the intended PR base from the delivery plan or actual PR target. Read
   `<ticket-dir>/<ticket>-git-diff.txt` and check its base/head SHAs against the selected base
   and current branch. If missing or stale, refresh it with
   [the shared helper](../../ticket-post-step-workflow/scripts/refresh_git_diff.sh) after artifact
   preflight; if the base is ambiguous or cannot be resolved, report the blocker. The snapshot
   covers committed changes from merge base to head only. Inspect `git status --short`,
   `git diff --cached`, `git diff`, and every relevant untracked file so the description represents
   the complete proposed change. State the base and any unverified base freshness.
3. Compare ticket requirements with the changed files and supporting evidence. State an
   investigation cause only when evidence establishes it; otherwise identify it as unconfirmed.
4. Inspect the owning Makefiles and repository workflows before naming validation commands. For Go
   work, follow `ai/rules/go-development.md` and `ai/rules/go-validation.md`; use only documented
   targets for the affected module rather than direct Go tool commands.

## Validation notes

Use the result statuses and execution sources defined by
`ai/skills/ticket-post-step-workflow/SKILL.md`. Name automated tests added or updated in the change.
Give a result only when an artifact or the current session shows its execution and outcome. Label
an appropriate unexecuted command `To run` and preserve `Status: not run; Source: —` for checks a
reviewer still needs to execute. Include module or package scope, working directory, blockers, and
next actions when relevant.

Include manual validation only when applicable:

- For a documented UI flow with a usable environment, state the scenario and expected outcome.
  Otherwise mark UI smoke validation not applicable and give the reason.
- For an API supported by local setup, state the HTTP method and endpoint or the gRPC service and
  RPC, relevant collection/request when discoverable, setup, expected result, and a
  `<PASS/FAIL — not run>` placeholder. If required details cannot be established, retain explicit
  placeholders or mark the check not applicable with a reason. Do not use production credentials
  or execute requests as part of drafting.

## Output

Save the finished description only to:

`<ticket-dir>/<ticket>-pr-description.md` for a single-PR ticket, or
`<ticket-dir>/<ticket>-pr<n>-description.md` for PR`n` of a multi-PR ticket. Use the planned PR
number from the delivery tracker and current branch; resolve a mismatch before writing. Preserve
the separate description file for each PR.

Read an existing output before changing it and preserve relevant user-authored content. Do not
stage, commit, publish, create or modify a pull request, send messages, or execute application smoke
tests as part of local drafting.
