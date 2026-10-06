# Before Coding Checklist

Use before implementing non-trivial changes.

- [ ] Relevant repository instructions were loaded.
- [ ] `<ticket-dir>` was resolved using [AGENTS.md Artifact Rules](../../AGENTS.md#artifact-rules),
  including existing epic-grouped folders and ignored artifacts; no duplicate ticket location is created.
- [ ] Before creating or editing ticket artifacts, the path is ignored by local `.git/info/exclude`
  and `git ls-files -- ai/artifacts/` reports no indexed artifact paths.
- [ ] For ticket work, `<ticket-dir>/<ticket>-ticket-content.md` exists and was read as the specification.
- [ ] Ticket readiness was checked.
- [ ] Business objective is clear.
- [ ] Scope and out-of-scope boundaries are clear.
- [ ] Contract, persistence, validation, and security expectations are clear when relevant.
- [ ] Relevant local code was read.
- [ ] A delivery plan exists for multi-step work and references the ticket content for ticket-based work.
