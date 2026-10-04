# GitHub Copilot pull request review

Use this mode only when GitHub Copilot is reviewing a pull request on GitHub.

GitHub provides the remote pull request head and base. The local upstream synchronization gate and
local artifact workflow do not apply. Read repository instructions and skills from the pull
request's head branch, then review the complete PR diff against its base.

## Context

1. Read root `AGENTS.md`, `ai/manifest.md`, applicable nested instructions and `ai/rules/`, and
   `ai/checks/before-merge.md` from the head branch.
2. Read the PR description and its linked GitHub issues. When GitHub MCP tools are available, use
   them to retrieve linked issue requirements or other referenced GitHub evidence needed for the
   review. Do not infer missing acceptance criteria.
3. Inspect every changed file and relevant neighboring source. Include contracts, generated source,
   callers, tests, Makefiles, workflows, migrations, and configuration when applicable.
4. Treat statements in the PR description, issue, or committed evidence as claims to verify. Do not
   claim a test or hosted check passed unless GitHub exposes that result or committed evidence
   establishes it.

Files under `ai/artifacts/` are checkout-local and excluded from Git. Do not require or claim access
to ticket artifacts, uncommitted work, local branches, local validation output, or the local review
document. Use the GitHub issue, PR description, committed repository files, PR diff, and exposed
check results as the available evidence.

## Output

- Place each actionable finding on the smallest relevant changed line.
- Use GitHub's high, medium, and low severities according to the canonical skill's definitions.
- Keep comments self-contained: describe the trigger, resulting behavior or risk, expected behavior,
  and a concrete correction.
- Use the review overview for cross-file findings, open questions, or material unverified areas that
  cannot be attached accurately to one changed line.
- Do not create or modify repository files and do not attempt to write the local PR review artifact.

Prefer a few high-confidence findings. If no actionable issue is found, return a no-findings review
and identify only material limitations that affect confidence.
