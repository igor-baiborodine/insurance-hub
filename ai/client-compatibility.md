<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Client Compatibility](#client-compatibility)
  - [Target Modes](#target-modes)
  - [Verification Contract](#verification-contract)
  - [Platform References](#platform-references)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Client Compatibility

**Matrix version:** 1 (2026-10-22). **Scope:** repository adapters requested for Claude Code,
Continue IDE, and GitHub Copilot. “Targeted” means an adapter exists; “verified” requires an
observed client run of the common [fixture](checks/client-compatibility-scenarios.md), including
evidence that the canonical file was actually read. This table records no runtime pass yet.

The root [instruction precedence and conflict procedure](../AGENTS.md#instruction-precedence)
is project policy inside each host's higher-level instructions and security controls. Native
discovery order, merged instruction behavior, and available tools can differ. A pointer file
does not grant tool access, artifact access, or permission to publish.

## Target Modes

| Client/mode | Discovery and invocation | Instruction sources and precedence expectation | Tools and local artifacts | Version/date and evidence |
| --- | --- | --- | --- | --- |
| Claude Code, local project session | Root `CLAUDE.md` imports `AGENTS.md`; four `.claude/skills/<stage>/SKILL.md` entries point to `ai/skills/`. Invoke by matching task or explicit `/ticket-…` skill. | Claude Code may load `AGENTS.md` directly on recent versions; the import covers other configurations. Applicable nested guides must be inspected and conflicts escalated under root policy, subject to Claude's host hierarchy. | Tool and ignored `ai/artifacts/` access depend on Claude Code settings and the local checkout. | **Targeted, runtime unverified.** No `claude` executable in this session. Docs checked 2026-10-22; actual version and canonical read trace pending. |
| Continue IDE, Agent | `.continue/rules/CONTINUE.md` routes to root catalog and selected canonical `SKILL.md`; root `CONTINUE.md` is a human pointer. Start Agent mode with the fixture prompt. | Continue loads enabled rules; operator must confirm this rule is active and trace the canonical read. Root conflict procedure is project policy, not an override of Continue's rule order or tool policy. | Agent tools depend on local policy; ignored artifacts require workspace file access. | **Targeted, runtime unverified.** No Continue runtime identified here; version, active-rule trace, and fixture results pending. |
| Continue IDE, Chat or Edit | Same rule path and stage prompt. | Rule loading is documented for both modes; selected canonical file read and conflict outcome require observation. | Do not infer shell or write capability from rule loading. Ticket stages needing unavailable tools must stop or hand off. | **Targeted, runtime unverified.** Version and fixture results pending. |
| GitHub Copilot coding agent | `.github/copilot-instructions.md` routes to root catalog; four `.github/skills/<stage>/SKILL.md` entries route to canonical skills. Invoke with a task naming the stage. | Copilot may combine repository, path, and agent instructions without a general local precedence order. Root conflict procedure remains the desired project behavior within host policy. | Hosted checkout and tool policy determine access. Ignored local ticket artifacts are absent unless explicitly provided in that environment. | **Targeted, runtime unverified.** Hosted version and canonical read trace pending; static pointers checked locally. |
| GitHub Copilot code review | Repository instructions plus existing `.github/skills/code-review/SKILL.md` route to the canonical review skill. Use a PR review request; ticket-stage skills are available when relevant. | Review reads instructions from the PR head revision; verify the selected skill and canonical file in review evidence. Host precedence and permissions still apply. | Published PR checkout does not contain this machine's ignored ticket artifacts; review only available published evidence. | **Targeted, runtime unverified.** Hosted version and review trace pending. |

Other clients, Copilot IDE surfaces, Continue CLI, remote Claude sessions, and modes not listed
above are **unverified**. No adapter is implied for them.

## Verification Contract

Run the same stage-selection and nested-guidance cases in the
[client fixture](checks/client-compatibility-scenarios.md) for each target mode. Record the actual
client version/date, checkout revision, mode and tool policy, prompt, applicable instruction files,
canonical `SKILL.md` read evidence, output/artifact access, and expected-versus-observed outcome.
If the client cannot expose a read trace, label canonical loading **unverified** even when its
answer resembles the intended workflow. Repair a demonstrated discovery gap with a thin pointer;
keep workflow bodies in `ai/skills/`.

## Platform References

- [Claude Code project instructions and `AGENTS.md` behavior](https://code.claude.com/docs/en/memory)
  and [project skill discovery](https://code.claude.com/docs/en/skills).
- [Continue repository rules](https://docs.continue.dev/customize/rules) and
  [rule loading and frontmatter](https://docs.continue.dev/customize/deep-dives/rules).
- [GitHub Copilot custom instructions and precedence](https://docs.github.com/en/copilot/concepts/prompting-and-context/custom-instructions)
  and [project skills](https://docs.github.com/en/copilot/how-tos/copilot-on-github/customize-copilot/customize-cloud-agent/add-skills).
