<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Insurance Hub AI Integration](#insurance-hub-ai-integration)
  - [Local ownership](#local-ownership)
  - [Shared source and client discovery](#shared-source-and-client-discovery)
  - [Consumer validation and artifacts](#consumer-validation-and-artifacts)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Insurance Hub AI Integration

## Local ownership

[AGENTS.md](../AGENTS.md) is the consumer entry point. [profile.json](profile.json) declares
selected components and command capabilities; [consumer-profile.md](consumer-profile.md) owns
Insurance Hub conventions, migration context, tool owners and language selections.

The only local rule files are [infrastructure guidance](rules/infrastructure-rules.md), which
selects this repository's operational runbooks, and [legacy Java conventions](rules/java-rules.md),
which retain existing module style and testing conventions. Local rules may extend shared guidance
or select supported conventions. Resolve apparent conflicts under the root guide before work.

## Shared source and client discovery

Read [the pinned consumer contract](framework/consumer.md), [catalog](framework/catalog.json),
and selected rules/workflows directly in `ai/framework/`. Shared prompts, templates, checklists,
examples and executable helpers also live there; there are no local forwarding copies or example
symlinks. Do not use the dependency's contributor AGENTS.md as consumer policy.

Codex `.agents/skills/` and Claude `.claude/skills/` contain the required native discovery adapters.
The existing Copilot/Continue adapters also point directly to shared sources, with no new runtime
support claim. See [the client matrix](client-compatibility.md) for observed evidence and limits.

## Consumer validation and artifacts

[scripts/ai/framework.mk](../scripts/ai/framework.mk) owns the composed Make checks.
[check_ai_framework.py](checks/check_ai_framework.py) validates consumer references, catalog,
adapters and local ticket structure; canonical source validation belongs to the dependency.
[CONTRIBUTING](../CONTRIBUTING.md#shared-ai-framework) documents preparation and command prerequisites.

Ticket evidence remains under ignored `ai/artifacts/` and follows the root resolver/exclusion policy.
Historical records retain their original paths and tested revisions. Retrieve removed historical
source using `git show <recorded-consumer-revision>:<old-path>`; use canonical paths for current work.
Removing obsolete forwarding files does not rewrite historical findings or make old passes current.
