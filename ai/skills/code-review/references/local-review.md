<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Local code review](#local-code-review)
  - [Required inputs](#required-inputs)
  - [Upstream synchronization gate](#upstream-synchronization-gate)
  - [Collect the proposed change](#collect-the-proposed-change)
  - [Evidence and output](#evidence-and-output)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Local code review

Use this mode from a local checkout when the requested output is the repository's ticket review
artifact.

## Required inputs

- Ticket ID and repository root.
- `<ticket-dir>/<ticket>-ticket-content.md`.
- Existing delivery tracker, completed step summaries, and optional reviewer-supplied artifacts.
- `ai/templates/pr-review-template.md`.

Resolve `<ticket-dir>` using [AGENTS.md Artifact Rules](../../../../AGENTS.md#artifact-rules),
including existing standalone or epic-grouped locations. Use the same directory for all inputs
and outputs below. Before writing any ticket artifact, perform the root artifact preflight.
If required ticket content is missing or empty, follow that guide instead of inventing requirements.

## Upstream synchronization gate

Run this gate before refreshing the diff snapshot, inspecting the implementation, or writing the
review:

```sh
ai/skills/code-review/scripts/check-upstream-sync.sh <repository-root>
```

The launcher uses the repository's managed CPython 3.14.8 runtime by default; `PYTHON` may select
another verified CPython 3.14.8 executable.

The script resolves the current branch's configured upstream, fetches that exact remote branch, and
requires local `HEAD` to equal the fetched commit. If the branch has no remote upstream, the fetch
fails, or the commits differ because the local branch is ahead, behind, or diverged, stop the review.
Report the branch, upstream, local and fetched commit IDs when available, and ahead/behind counts.
Do not refresh the ticket diff snapshot and do not create or overwrite the PR review artifact.

This gate compares committed branch state. Staged, unstaged, and untracked proposed changes remain
part of the later working-tree review and do not by themselves fail synchronization.

## Collect the proposed change

After the synchronization gate passes:

1. Identify the intended PR base from the delivery plan or actual PR target, including the
   immediate parent for a stacked PR. If it is missing or ambiguous, stop and report that blocker;
   do not substitute `main`. Record the selected ref and whether its freshness was verified.
   Refresh the snapshot with
   [the shared helper](../../ticket-post-step-workflow/scripts/refresh_git_diff.sh), passing the
   repository root, intended base ref, current branch ref, and ticket diff path. It records
   base/head/merge-base SHAs and the committed patch from merge base to head. If a ref or merge
   base cannot be resolved, preserve the previous snapshot and stop.
2. Immediately inspect `git status --short`, `git diff --cached`, and `git diff`. Read every
   relevant untracked proposed file. Record whether the tree is clean or list staged, unstaged,
   and untracked paths.
3. Read the complete ticket specification, delivery tracker, completed step summaries, refreshed
   snapshot, and relevant supporting artifacts. An empty snapshot does not prove an empty change;
   reconcile it with branch and working-tree state.
4. Discover applicable nested instructions from the changed paths. Load relevant `ai/rules/`,
   workflow skills, language guidance, and `ai/checks/before-merge.md`.
5. Inspect changed source, tests, contracts, generated output, migrations, configuration, and the
   neighboring code needed to establish actual behavior. For generated code, inspect its source,
   configuration, and recorded generation evidence. Read Makefiles to determine real validation
   coverage.

For contract changes, inspect authoritative schemas, controllers or handlers, gateway mappings,
callers, and neighboring contracts. Check method or RPC names, request and response semantics,
status mapping, required fields, defaults, nullability, versioning, and compatibility.

## Evidence and output

Use the result statuses and execution-source fields defined by
`ai/skills/ticket-post-step-workflow/SKILL.md`. Do not run tests merely to convert missing review
evidence into a pass; report the available recorded evidence and its limits.

Write the review using `ai/templates/pr-review-template.md` and save it only to:

`<ticket-dir>/<ticket>-pr-review.md` for a single-PR ticket, or
`<ticket-dir>/<ticket>-pr<n>-review.md` for PR`n` of a multi-PR ticket. Use the planned PR number
from the delivery tracker and current branch; resolve a mismatch before writing. Preserve the
separate review file for each PR.

Assign finding IDs in order (`F-001`, `F-002`, ...). Under repository-rule compliance, list every
applicable rule area as **Satisfied**, **Finding identified**, **Unverified**, or, with a concrete
reason, **Not applicable**. State the working-tree state and the local/fetched commit proven equal by
the synchronization gate. State the intended PR base, its resolved SHA and freshness status, the
merge-base SHA, and the head SHA. Distinguish the committed patch from staged, unstaged, and untracked
work; never imply that the snapshot alone is the entire proposal.

Before finishing, confirm each finding is reproducible and actionable, findings are severity-ordered,
questions are not phrased as confirmed bugs, and claimed passes have inspected implementation or
recorded execution evidence.
