<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Repository Rules](#repository-rules)
  - [General](#general)
  - [GitHub Actions](#github-actions)
  - [Markdown Tables Of Contents](#markdown-tables-of-contents)
  - [Git And Artifacts](#git-and-artifacts)
  - [QA Cluster Network Recovery](#qa-cluster-network-recovery)
  - [Validation](#validation)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Repository Rules

## General

- Read relevant local code and docs before changing behavior.
- Prefer existing project patterns over new abstractions.
- Keep changes scoped to the request or ticket.
- Preserve behavior unless the spec explicitly changes it.
- Avoid unrelated formatting, metadata churn, or refactors.
- Update documentation when behavior, setup, workflow, or contracts change.

## GitHub Actions

- Name workflows by scope and purpose in the top-level `name:` field, such as `Go Module
  Validation`, `Go Report Card`, and `Legacy API Release`.
- Use a concrete purpose instead of appending `CI` solely because a workflow runs in CI.
- When changing a workflow display name, update badge labels and documentation that refer to it.
  Keep workflow filenames and job identifiers stable unless they also need to change.

## Markdown Tables Of Contents

- Every Markdown file created or updated by an agent must contain a current `doctoc`-generated
  table of contents.
- After editing, run `./scripts/markdown-toc.sh format <file>...` with the exact task-owned Markdown
  paths, followed by `./scripts/markdown-toc.sh check <file>...`.
- Do not edit the generated table of contents by hand or run the formatter across unrelated files.
- Treat a failed TOC check as incomplete validation and report it if it cannot be resolved.

## Git And Artifacts

- Do not commit files under `ai/artifacts/`.
- Do not overwrite or revert user changes unless explicitly requested.
- For ticket work, resolve `<ticket-dir>` through [AGENTS.md Artifact Rules](../../AGENTS.md#artifact-rules).
  Store the specification, delivery plan, step summaries, and diff snapshots together there,
  whether the ticket is standalone or grouped under an epic.
- Keep committed AI framework files under `ai/` concise and reusable.

## QA Cluster Network Recovery

The maintainer's `qa-insurance-hub` cluster runs through host networking that can lose outbound
forwarding when the host `iptables` `FORWARD` policy is not `ACCEPT`. Consider this host condition
before diagnosing a repository, Flux, DNS, or image defect when several of these symptoms appear
together:

- Flux reports `lookup github.com on 10.43.0.10:53: server misbehaving`.
- CoreDNS reports upstream UDP timeouts to `8.8.8.8:53`.
- workloads report GHCR HTTPS timeouts or `ImagePullBackOff`.
- the Flux source and Kustomization remain on an older revision despite the newer desired state.

Follow [`k8s/base-cluster-how-tos.md`](../../k8s/base-cluster-how-tos.md#prerequisites). A maintainer
with host administration access should inspect the active policy with
`sudo iptables -L FORWARD -v -n`, set it with `sudo iptables -P FORWARD ACCEPT` when required, and
persist it with `sudo netfilter-persistent save`. The policy change takes effect without restarting
Docker or LXD. Do not claim persistence unless the save command or equivalent persistent
configuration was verified.

After recovery, use the explicit `qa-insurance-hub` context and request or await normal Flux
reconciliation. In this repository the source is `flux-system/insurance-hub` and the service
Kustomization is `flux-system/qa-svc`. Verify a fresh result rather than relying on historical
conditions or events: the GitRepository must be ready on the expected current `main` revision, the
Kustomization must be ready on that revision, and affected deployments must use their checked-in
images and become ready. Keep deployment changes under Flux ownership; do not bypass the failure
with an ad hoc workload apply.

If interactive `sudo` is unavailable, record the host action as blocked and give the maintainer the
exact inspection and recovery commands. Treat later QA provisioning or activation as blocked until
fresh source, reconciliation, image-pull, and workload-readiness evidence passes.

## Validation

- Run the narrowest meaningful tests or checks for the changed modules.
- Run the Markdown TOC check for every Markdown file created or updated by the current task.
- If validation is skipped or unavailable, state that explicitly and explain why.
- Do not treat generated plans or summaries as a substitute for running checks.
