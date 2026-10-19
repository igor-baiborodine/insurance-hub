<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Git Rules](#git-rules)
  - [Branch Names](#branch-names)
  - [Commit Messages](#commit-messages)
  - [Commit Content and Staged Review](#commit-content-and-staged-review)
  - [Working Tree and History Safety](#working-tree-and-history-safety)
  - [Sensitive and Generated Content](#sensitive-and-generated-content)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Git Rules

Apply these rules when creating or renaming a branch, preparing or reviewing a commit, or managing
the Git working tree or history. They implement the repository's use of the
[Conventional Commits specification](https://www.conventionalcommits.org/en/v1.0.0/).

## Branch Names

Choose one of these types according to the branch's primary purpose:

- `feat`: a new feature
- `fix`: a bug fix
- `docs`: documentation-only changes
- `style`: formatting or other changes that do not affect behavior
- `refactor`: a code change that neither fixes a bug nor adds a feature
- `perf`: a performance improvement
- `test`: additions or corrections to tests
- `chore`: build, tooling, dependency, or other maintenance work

Use one of these formats:

```text
<type>/issue-<number>-<description>
<type>/<description>
```

- Include the issue prefix when the branch corresponds to a ticket. The prefix is optional for
  work without a ticket.
- Write the description with lowercase ASCII letters, digits, and hyphens. Do not use spaces,
  underscores, or uppercase letters.
- Keep the description concise and specific, preferably about five words or fewer.
- Give each branch one purpose. Use the type that best represents its primary outcome; supporting
  commits may use other accurate commit types.

Examples:

- `feat/issue-6-provision-kind-and-microk8s-clusters`
- `fix/issue-14-handle-null-pointer-in-payment-service`
- `docs/issue-22-update-api-specification`
- `feat/add-basic-authentication`
- `refactor/cleanup-database-migrations`
- `chore/update-dependencies`

The [pre-push hook](../../.githooks/pre-push) enforces the allowed types and branch-name character
structure. Keep its regular expression synchronized with this rule when changing the convention.
Configure repository hooks as described in
[Configure Repository Hooks](../../CONTRIBUTING.md#configure-repository-hooks).

## Commit Messages

Use the applicable form:

```text
<type>: [issue-123] <description>
<type>(<scope>): [issue-123] <description>
<type>!: [issue-123] <description>
<type>(<scope>)!: [issue-123] <description>
```

- Use one of the eight branch types listed above as the lowercase commit type.
- Use an optional lowercase scope in parentheses when it adds useful subsystem context, such as
  `ai`, `ci`, or `k8s`.
- Include the lowercase issue reference when the commit relates to a ticket. The square brackets
  are literal, as in `[issue-123]`. Omit the entire reference when there is no ticket.
- Write a concise description in imperative mood, such as `add logging support`.
- Use lowercase prose and omit the final period. Preserve the correct case of names and identifiers
  such as `README`, `Go`, and API symbols.
- For a breaking change, place `!` immediately before the colon, after the optional scope: `type!:`
  or `type(scope)!:`. Explain the affected interface and required migration in the commit body or a
  `BREAKING CHANGE:` footer.

Examples:

- `feat: [issue-12] add new user login endpoint`
- `fix: handle null pointer on empty request`
- `docs(ai): [issue-174] update repository rules`
- `chore: update dependencies to latest versions`
- `refactor(auth): [issue-6] simplify authentication flow`
- `feat(api)!: [issue-42] replace the legacy authentication contract`

The repository has no checked-in `commit-msg` hook, so authors and reviewers must verify commit
messages against this rule.

The bracketed subject reference provides traceability but does not declare that the commit closes
the issue. Use a hosting-platform closing reference only when the delivered change satisfies the
issue's completion criteria.

## Commit Content and Staged Review

- Make each commit one coherent, reviewable change. Keep its implementation, directly associated
  tests, and necessary documentation together.
- Separate unrelated refactoring, broad formatting, and generated-file churn. Do not split a
  contract change from the callers required to keep the affected component working.
- Prefer commits that leave the affected component usable and its applicable checks passing. If an
  intermediate commit intentionally cannot stand alone, explain why and record the intended merge
  strategy in the commit body or delivery documentation.
- Before committing, inspect `git status --short`, `git diff --staged --stat`,
  `git diff --staged`, and `git diff --staged --check`. Review new files, deletions, file modes,
  generated output, and unintended changes as well as the patch content. These checks supplement
  rather than replace the owning module's tests and validation.
- Stage explicit paths or selected hunks when unrelated work is present. Do not include unrelated
  working-tree changes merely because they are available to stage.

## Working Tree and History Safety

- Inspect repository status before editing. Preserve pre-existing staged, unstaged, and untracked
  work. Do not discard, overwrite, stash, unstage, or incorporate unrelated changes without
  explicit authorization.
- Do not use destructive cleanup or recovery commands merely to obtain a clean working tree. Report
  conflicting changes and resolve their ownership before continuing.
- Commit authorization does not authorize a push, merge, branch deletion, release tag, or shared
  history rewrite. Obtain or rely on authorization for each operation as required by
  [`AGENTS.md`](../../AGENTS.md).
- Do not rename an existing branch or rewrite commit history merely to apply this rule
  retroactively. Perform those operations only when the task explicitly requires them.
- Naming compliance does not authorize creating a commit. Follow the review and explicit commit
  authorization requirements in [`AGENTS.md`](../../AGENTS.md).

## Sensitive and Generated Content

- Never commit credentials, private keys, tokens, or unsanitized sensitive data. Keep secrets out
  of commit subjects, bodies, and recorded diffs, including when documenting their removal.
- Keep developer-specific configuration, local environments, database dumps, disposable test
  output, and local AI artifacts untracked unless an owning workflow explicitly requires a safe,
  sanitized form. Files under `ai/artifacts/` must remain untracked.
- Commit generated files only when the owning workflow or repository contract requires them. Review
  generated changes for unexpected content before staging them.
