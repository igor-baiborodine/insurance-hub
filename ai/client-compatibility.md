<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Client Compatibility](#client-compatibility)
  - [Target Modes](#target-modes)
  - [Verification Contract](#verification-contract)
  - [Platform References](#platform-references)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Client Compatibility

**Matrix version:** 3 (2026-10-23). **Adoption scope:** Codex and Claude Code CLI in local
Insurance Hub checkouts using the pinned shared framework. Static composition and refreshed
runtime scenarios pass on the C-U17 direct-source layout with the C-U18 adapter TOC exemption.
The refreshed runs use consumer `b3e57990edd330ef11825352a4ed61984f77ef47` and framework
`aa36ce457834bb6b2314c674b381c945b2da61b1`. Independent maintainer policy acceptance remains
separate. Continue/Copilot entries retain their historical status and are outside this adoption's
support claim.

Both clients completed the four requested workflow stages and seven negative/review decisions.
Actual canonical reads, commands and before/after manifests are retained in the local Step 12
`clients/codex-refresh`, `clients/claude-refresh` and corresponding negative-run evidence.
Go-focused stages did not load Java rules; the separate Java case selected shared routes and local
style. Previous observations remain historical, superseded by these scoped reruns.

Claude's first negative review refreshed only Git index stat-cache fields despite
`GIT_OPTIONAL_LOCKS=0`. An isolated reproduction identified plain `git diff` as the trigger.
The targeted review rerun also set `diff.autoRefreshIndex=false` through inherited Git configuration;
all captured fixture files, consumer index bytes and exclusions then remained unchanged.
The initial preservation failure is retained. These are observed client behaviors, not proof of
whole-host filesystem/network containment or application-test success.

The root [instruction precedence and conflict procedure](../AGENTS.md#instruction-precedence)
is project policy inside each host's higher-level instructions and security controls. Native
discovery order, merged instruction behavior, and available tools can differ. A pointer file
does not grant tool access, artifact access, or permission to publish.

## Target Modes

| Client/mode | Discovery and invocation | Instruction sources and precedence expectation | Tools and local artifacts | Version/date and evidence |
| --- | --- | --- | --- | --- |
| Codex CLI, local checkout | `AGENTS.md`, `ai/profile.json`, shared consumer entry and six `.agents/skills/` adapters | Root/nested discovery and actual canonical reads required; contributor guide is not consumer policy | Explicit root access; workspace-write or read-only trial mode, no publication | CLI 0.156.1, 2026-10-23: refreshed four stages/post-step and seven negative/review cases passed; canonical reads and protected manifests verified. Model not emitted by the event stream. |
| Claude Code CLI, local checkout | `CLAUDE.md` imports root guide; six `.claude/skills/` adapters | Same pinned consumer entry and shared workflows; local Java/style bindings retained | Explicit root access and scoped tool permissions | CLI 2.1.309 / claude-opus-5-5, 2026-10-23: refreshed four stages/post-step and seven expected decisions observed; no out-of-directory artifact writes observed. Strict review preservation passed on targeted rerun with automatic Git index refresh disabled; initial stat-cache change retained. |
| Continue IDE, Agent | `.continue/rules/CONTINUE.md` routes to root catalog and selected canonical `SKILL.md`; root `CONTINUE.md` is a human pointer. Start Agent mode with the fixture prompt. | Continue loads enabled rules; operator must confirm this rule is active and trace the canonical read. Root conflict procedure is project policy, not an override of Continue's rule order or tool policy. | Agent tools depend on local policy; ignored artifacts require workspace file access. | **Targeted, runtime unverified.** No Continue runtime identified here; version, active-rule trace, and fixture results pending. |
| Continue IDE, Chat or Edit | Same rule path and stage prompt. | Rule loading is documented for both modes; selected canonical file read and conflict outcome require observation. | Do not infer shell or write capability from rule loading. Ticket stages needing unavailable tools must stop or hand off. | **Targeted, runtime unverified.** Version and fixture results pending. |
| GitHub Copilot coding agent | `.github/copilot-instructions.md` routes to root catalog; four `.github/skills/<stage>/SKILL.md` entries route to canonical skills. Invoke with a task naming the stage. | Copilot may combine repository, path, and agent instructions without a general local precedence order. Root conflict procedure remains the desired project behavior within host policy. | Hosted checkout and tool policy determine access. Ignored local ticket artifacts are absent unless explicitly provided in that environment. | **Targeted, runtime unverified.** Hosted version and canonical read trace pending; static pointers checked locally. |
| GitHub Copilot code review | Repository instructions plus existing `.github/skills/code-review/SKILL.md` route to the canonical review skill. Use a PR review request; ticket-stage skills are available when relevant. | Review reads instructions from the PR head revision; verify the selected skill and canonical file in review evidence. Host precedence and permissions still apply. | Published PR checkout does not contain this machine's ignored ticket artifacts; review only available published evidence. | **Targeted, runtime unverified.** Hosted version and review trace pending. |

Other clients, Copilot IDE surfaces, Continue CLI, remote Claude sessions, and modes not listed
above are **unverified**. No adapter is implied for them.

## Verification Contract

Run the same stage-selection and nested-guidance cases in the
[client fixture](framework/checks/client-compatibility-scenarios.md) for each target mode. Record the actual
client version/date, checkout revision, mode and tool policy, prompt, applicable instruction files,
canonical `SKILL.md` read evidence, output/artifact access, and expected-versus-observed outcome.
If the client cannot expose a read trace, label canonical loading **unverified** even when its
answer resembles the intended workflow. Repair a demonstrated discovery gap with a thin pointer;
keep workflow bodies in the unmodified `ai/framework/skills/` dependency.

## Platform References

- [Claude Code project instructions and `AGENTS.md` behavior](https://code.claude.com/docs/en/memory)
  and [project skill discovery](https://code.claude.com/docs/en/skills).
- [Continue repository rules](https://docs.continue.dev/customize/rules) and
  [rule loading and frontmatter](https://docs.continue.dev/customize/deep-dives/rules).
- [GitHub Copilot custom instructions and precedence](https://docs.github.com/en/copilot/concepts/prompting-and-context/custom-instructions)
  and [project skills](https://docs.github.com/en/copilot/how-tos/copilot-on-github/customize-copilot/customize-cloud-agent/add-skills).
