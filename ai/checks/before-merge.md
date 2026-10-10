<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Before Merge Checklist](#before-merge-checklist)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Before Merge Checklist

Use before considering a ticket complete.

- [ ] The ticket-content file reflects the agreed requirements used for implementation and validation.
- [ ] Ticket artifacts use the same resolved `<ticket-dir>` from
  [AGENTS.md Artifact Rules](../../AGENTS.md#artifact-rules), with links correct for its nesting depth.
- [ ] Delivery tracker is updated.
- [ ] Step summaries exist for completed steps.
- [ ] Each step summary has a validation-evidence record for applicable checks, including failed,
  blocked, not-run, and manual/CI-only checks.
- [ ] Validation records separate result status from execution source and include enough command,
  scope, result, and evidence detail to reproduce or assess each check.
- [ ] Every acceptance criterion maps to evidence or is explicitly unresolved; pending CI/manual
  checks and blockers have next actions.
- [ ] `ai/artifacts/` isolation is verified: the resolved ticket-content path is ignored by
  `.git/info/exclude`, and `git ls-files -- ai/artifacts/` plus
  `git diff --cached --name-only -- ai/artifacts/` report no paths.
- [ ] Git diff artifact is refreshed when using ticket workflow.
- [ ] Before any commit, the end user reviewed the complete proposed change, including the branch
  diff, staged and unstaged changes, untracked files, and generated files.
- [ ] Relevant tests, builds, formatting, or lint checks were run.
- [ ] Documentation was updated when behavior or workflow changed.
- [ ] Every Markdown file created or updated in the task has a current `doctoc`-generated table of
  contents, verified with `./scripts/markdown-toc.sh check <file>...`.
- [ ] Unrun checks and residual risks are documented.
