# GitHub pull request description drafting and update

Use this mode when the target is an existing pull request on GitHub. GitHub-hosted agents cannot
access the checkout-local files under `ai/artifacts/`; do not require or claim access to them.

## Authorization boundary

Determine the requested action from the user's words:

- **Draft**: produce the proposed body without modifying the pull request.
- **Draft and update**, **update**, or equivalent explicit language: update the existing pull
  request body after drafting it. This authorizes only the description update.

Do not infer update authorization from a request to draft, improve, inspect, or review a
description. Do not create, merge, close, label, assign, or otherwise modify the pull request.

## Collect GitHub evidence

1. Resolve the repository and pull request from the request or current GitHub context. If the
   target remains ambiguous, ask for the missing repository or pull request number before any
   update.
2. Read the existing PR title and body, base and head refs, full changed-file diff, commits, linked
   issues, exposed check results, and relevant review discussion. Read repository instructions,
   the shared PR template, applicable rules, and committed source from the PR head.
3. Treat statements in the existing description, linked issues, comments, and commit messages as
   claims to reconcile with the diff and authoritative repository sources. Do not infer missing
   requirements or validation results.
4. Preserve relevant user-authored context from the existing body, including issue links,
   rationale, operational notes, screenshots, and follow-up information. Remove stale template
   placeholders and revise claims that the available evidence contradicts.
5. Use only GitHub-visible evidence. A hosted check may be reported with `Source: CI`. Report local
   validation only when the PR or another accessible, authoritative record contains its concrete
   command, scope, and result; identify it as reported evidence rather than claiming the hosted
   agent ran it. Mark unsupported checks `not run`, `blocked`, or `not applicable` as appropriate.

## Draft or update

Render the complete proposed body using `ai/templates/pr-description-template.md` and the shared
skill standard.

For a draft-only request, return the body and do not call a write operation.

For an explicitly authorized update:

1. Use the available GitHub pull-request update operation to replace the body with the complete
   drafted description. Do not update unrelated fields.
2. Read the pull request again and verify that its body matches the intended description.
3. Report the updated pull request and any material evidence limitations.

If the environment lacks a GitHub write operation, it is configured read-only, authentication is
insufficient, or the update fails, do not claim success or attempt another kind of mutation.
Return the complete draft and state why the pull request was not updated so it can be applied
manually or through the local workflow.
