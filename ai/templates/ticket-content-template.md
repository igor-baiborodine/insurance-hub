<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Context](#context)
- [Description](#description)
- [Acceptance Criteria](#acceptance-criteria)
- [Dev Notes](#dev-notes)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

<!--
Resolve `<ticket-dir>` using AGENTS.md Artifact Rules, then save as
`<ticket-dir>/<ticket>-ticket-content.md` before starting ticket implementation.
The directory may be standalone, grouped under an epic, or the epic's own artifact directory.
Use ticket-specification-workflow when drafting from assessed requirements. Drafting stops at
ticket content; the implementation workflow creates a delivery plan only when requested.
This ticket-content file is the specification used for readiness, planning, implementation, and
validation.
Replace bracketed prompts with ticket-specific content and remove inapplicable bullets.
Preserve supplied requirements and record agreed clarifications; do not invent missing details.
When drafting from <ticket>-requirements.md, map must-have IDs to observable ACs, preserve supplied
AC meaning, and exclude deferred nice-to-haves from mandatory scope. Link the assessment for
provenance while keeping this specification self-contained. For conditional readiness, include
bounded decision tasks and dependency gates in Dev Notes and an AC requiring resolution and a
specification update before dependent implementation. Do not invent decision outcomes.
Keep delivery steps and progress in `<ticket-dir>/<ticket>-delivery-steps.md`; no separate spec is required.
Remove this authoring comment from the completed ticket-content file.
-->

### Context

[Explain the current behavior or problem, why this work is needed, and the business or technical
outcome it supports. Link the relevant migration phase, related tickets, or repository documentation
when applicable. Identify existing behavior that must be preserved.]

### Description

[State the requested change and its intended result. Identify the affected services, API modules,
frontend, deployment assets, or developer tooling.]

The change must cover:

1. **[Required capability or change]**
   - [Describe the required behavior, affected boundary, and concrete inputs/outputs or configuration.]
   - [Specify environment differences, constraints, or failure behavior when relevant.]
2. **[Additional capability or change, if needed]**
   - [Describe the expected behavior and integration with existing components.]

**Out of scope:** [Name related behavior, modules, environments, or future work excluded from this ticket.]

### Acceptance Criteria

- [ ] **[Primary outcome]**: [State an observable result and the input, action, or condition that demonstrates it.]
- [ ] **[Compatibility or failure behavior, if applicable]**: [State what remains compatible and the expected response to invalid input or failure.]
- [ ] **[Validation]**: [Name the relevant automated checks or manual scenario and the expected result in the target environment.]
- [ ] **[Documentation or reproducibility, if applicable]**: [Name the documentation/configuration deliverable and what a developer must be able to reproduce.]

### Dev Notes

- **Repository paths and existing patterns**: [List the relevant files/modules and conventions to follow.]
- **Contracts and behavior, when applicable**: [Specify endpoint methods/paths, request and response schemas, status/error behavior, events, or interfaces; link existing contract definitions where possible.]
- **Persistence and migration, when applicable**: [Describe data/schema changes, consistency or idempotency requirements, compatibility, and rollout/rollback expectations.]
- **Security and configuration, when applicable**: [Specify authentication/authorization, validation, secret references, and environment-specific settings without including credentials.]
- **Dependencies and prerequisites**: [List required tickets, services, tool versions, or infrastructure; distinguish existing dependencies from additions.]
- **Verification commands and evidence**: [Give the narrowest relevant Make/module commands or manual steps, their working directory/environment, and expected output. Distinguish repository checks from checks requiring a live service or editor.]
- **References and decisions**: [Link relevant repository or external documentation and the requirements assessment when present; map requirement IDs to ACs and record agreed decisions or constraints.]
- **Open questions and prerequisite decisions**: [Write “None”, identify blockers, or describe each bounded deferred decision by ID, safe investigation scope, responsible role, expected evidence, acceptance condition, and blocked dependent work. Require a specification update and readiness recheck before dependent implementation; the delivery plan expands these into ordered steps.]
