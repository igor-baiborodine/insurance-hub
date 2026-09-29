# Before Merge Checklist

Use before considering a ticket complete.

- [ ] The ticket-content file reflects the agreed requirements used for implementation and validation.
- [ ] Delivery tracker is updated.
- [ ] Step summaries exist for completed steps.
- [ ] Each step summary has a validation-evidence record for applicable checks, including failed,
  blocked, not-run, and manual/CI-only checks.
- [ ] Validation records separate result status from execution source and include enough command,
  scope, result, and evidence detail to reproduce or assess each check.
- [ ] Every acceptance criterion maps to evidence or is explicitly unresolved; pending CI/manual
  checks and blockers have next actions.
- [ ] `ai/artifacts/` isolation is verified: the ticket-content path is ignored by
  `.git/info/exclude`, and `git ls-files -- ai/artifacts/` plus
  `git diff --cached --name-only -- ai/artifacts/` report no paths.
- [ ] Git diff artifact is refreshed when using ticket workflow.
- [ ] Before any commit, the end user reviewed the complete proposed change, including the branch
  diff, staged and unstaged changes, untracked files, and generated files.
- [ ] Relevant tests, builds, formatting, or lint checks were run.
- [ ] Documentation was updated when behavior or workflow changed.
- [ ] Unrun checks and residual risks are documented.
