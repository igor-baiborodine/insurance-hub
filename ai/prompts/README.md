# Prompt and Skill Invocations

This directory contains reusable prompt fragments and copyable examples for common repository
tasks. The examples select workflows; the canonical instructions remain in `AGENTS.md` and the
applicable `ai/skills/*/SKILL.md` file.

## Reusable prompt

- [Session bootstrap](session-bootstrap.md): load repository instructions and existing ticket
  context at the start of a session.

## Example requests

Replace each placeholder with the real issue or pull request number.

For local ticket work, [AGENTS.md Artifact Rules](../../AGENTS.md#artifact-rules) resolves the
artifact directory, including tickets grouped under an epic. You may supply the exact path, for
example: `Continue issue-131 using ai/artifacts/epic-4.2/issue-131/issue-131-ticket-content.md`.

| Task and context                                        | Example request                                                                |
|---------------------------------------------------------|--------------------------------------------------------------------------------|
| Start ticket implementation                             | `Please implement issue-<number>.`                                             |
| Continue ticket work in a new local session             | `Read ai/prompts/session-bootstrap.md and continue working on issue-<number>.` |
| Review from a local checkout                            | `Please conduct a local code review for issue-<number>.`                       |
| Review a GitHub pull request                            | `Please conduct a code review for PR #<number>.`                               |
| Draft and update the current GitHub PR                  | `Please draft and update the PR description for issue-<number>.`               |
| Draft and update a PR outside its page or agent context | `Please draft and update the description for PR #<number>.`                    |
| Draft a GitHub PR description without changing the PR   | `Please draft the description for PR #<number>, but do not update it.`         |
| Draft a PR description from a local checkout            | `Please draft a PR description locally for issue-<number>.`                    |

The mode words are intentional:

- `local` selects the checkout workflow and its ignored `ai/artifacts/` output.
- `draft` alone is read-only. `Update` explicitly authorizes changing the existing GitHub PR body
  and no other PR fields.
- An issue number identifies ticket requirements, but it does not always identify a unique pull
  request. Include `PR #<number>` unless the current GitHub context already identifies the target.

## Workflow sources

- [`ticket-implementation-workflow`](../skills/ticket-implementation-workflow/SKILL.md) implements
  a ticket and invokes the post-step workflow after each completed delivery step.
- [`code-review`](../skills/code-review/SKILL.md) handles local and GitHub pull request reviews.
- [`pr-description`](../skills/pr-description/SKILL.md) handles local PR-description artifacts and
  GitHub PR-body drafting or updates.

The [`ticket-post-step-workflow`](../skills/ticket-post-step-workflow/SKILL.md) is normally invoked
by ticket implementation and does not require a separate user prompt.
