<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Infrastructure Rules](#infrastructure-rules)
  - [Select the owning guidance](#select-the-owning-guidance)
  - [Resolve service scope](#resolve-service-scope)
  - [Change-to-check index](#change-to-check-index)
  - [Live operations and completion evidence](#live-operations-and-completion-evidence)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Infrastructure Rules

Use this entry point for Kubernetes manifests, Helm values, Flux configuration, cluster bootstrap,
infrastructure administration, observability, and infrastructure credentials or database roles.
It routes work to existing repository checks and runbooks; load only the rows relevant to the
changed boundary.

## Select the owning guidance

- Read [CONTRIBUTING](../../CONTRIBUTING.md) for prerequisites and developer workflow. Inspect the
  affected Make target, prerequisites, and script before execution; a name containing `check`,
  `test`, or `preflight` does not establish its side effects.
- Load [Makefile rules](makefile-rules.md) when changing Make interfaces, and the
  [Python entry point](python-rules.md) and its four linked rules for auxiliary Python changes.
  Load [Go rules](go-rules.md) when Go implementation or tooling is also affected.
- Use [base-cluster procedures](../../k8s/base-cluster-how-tos.md) for Kind, LXD, K3s, host
  networking, and reconstruction; use [cluster-app procedures](../../k8s/cluster-apps-how-tos.md)
  for operators, shared services, Flux, and component verification links.
- Keep versions, environment coordinates, and operational sequences in their owning Makefiles,
  configuration, and runbooks. Do not copy those volatile values into this rule.

## Resolve service scope

Use `<service>` for the registered service key, `<environment>` for a supported environment, and
`<profile>` for a supported database-role profile. Resolve these from the affected configuration
and owning interface; do not derive them blindly from a directory name or inherit a default for
another service. Replace placeholders before running commands.

For Go service deployment, inspect the [service catalog](../../scripts/go/service/catalog.json)
and [dispatch interface](../../scripts/go/service/Makefile). Follow the selected module's README,
Makefile, and linked deployment runbook for supported capabilities, tool bootstrap, target
coordinates, and cleanup. For monitoring, inspect the selected service and environment records
under [observability assets](../../k8s/observability). For database roles, resolve the service/profile
pair through the [role catalog](../../scripts/postgres/service/catalog.json) and its owning runbook.

A generic target name does not establish coverage for every service. Confirm that the selected
capability, assets, probe, and test fixtures exist and exercise the affected service. Missing
registration or coverage is a gap to report; never substitute another service's passing results.
New services add their metadata, checks, and operational guidance in these owning locations.
This rule remains independent of the service inventory.

## Change-to-check index

Commands below run from the repository root. Select the narrowest applicable checks; this is not
a requirement to run every infrastructure suite for every edit. The owning interfaces are the
[root Makefile](../../Makefile), [Kubernetes Makefile](../../k8s/Makefile),
[validation fragment](../../k8s/tests/validation.mk), and
[Python toolchain fragment](../../scripts/python/toolchain.mk).

| Changed boundary | Local or disposable verification route | Prerequisites, limits, and operational evidence |
| --- | --- | --- |
| Kubernetes manifests, Helm values, or Flux desired state | Prefer the owning render/check target. For an uncovered plain Kustomize overlay, use `kubectl kustomize <overlay-dir>` after inspecting its bases and inputs. Review rendered identities, namespaces, selectors, images, RBAC, storage, and exposure against the specification. | Use the owning tool/chart pins; remote bases or charts may need network access. Rendering alone does not validate cluster admission or reconciliation. No repository-wide schema or Flux gate is defined. Follow [QA continuous delivery](../../k8s/cluster-apps-how-tos.md#continuous-delivery-qa) for actual source revision, reconciliation, and workload evidence. |
| Go service deployment overlays or deployment tooling | Where the selected catalog entry supports them, use `make go-svc-deploy-check SVC_NAME=<service>` and, for tooling, `make go-svc-deploy-test-tooling SVC_NAME=<service>`. | Follow the selected module's deployment runbook for pinned tools and dependencies. Rendered/fake-adapter passes do not prove a live deployment. A supported `go-svc-deploy-test` is a separate integration step; inspect the module's recipe for cluster prerequisites, resource creation, and cleanup. |
| Cluster bootstrap or Kubernetes administration tooling | `make python-k8s-admin-check` for managed-Python formatting, lint, and fake-boundary tests. Read [bootstrap targets](../../k8s/bootstrap/Makefile) and the [base-cluster runbook](../../k8s/base-cluster-how-tos.md) for the affected operation. | Requires the managed Python toolchain. These tests do not create Kind/LXD/K3s resources or prove host networking, storage, or recovery. Use the scoped [PVC](../../k8s/tests/bootstrap/qa-verify-dynamic-pvc-provisioning.md) or [test-app](../../k8s/tests/bootstrap/qa-verify-test-app-deployment.md) procedure when that live boundary must be verified. |
| Monitoring/Alloy configuration, dashboards, or alert rules | Select `make prometheus-receiver-config-check`, `make monitoring-assets-check ENV_NAME=<environment> SVC_NAME=<service>`, or `make alloy-config-check`; `make check-observability` covers its configured set. For Python tooling, use `make python-observability-check`. | Confirm the target's environment support and asset selection; aggregate defaults may cover only part of the inventory. Configuration checks need managed Python, Helm repositories/chart pins, and the required pinned local Docker images where used; they may render charts and run disposable validation containers. They do not apply cluster resources. See [observability procedures](../../k8s/cluster-apps-how-tos.md#observability-qa). |
| Observability ingestion or runtime behavior | `make test-monitoring ENV_NAME=<environment> SVC_NAME=<service>` exercises collected disposable Prometheus data. Where the selected service has a supported probe, use `make test-observability-backends OBSERVABILITY_SERVICE_DOMAIN=<service>` and the owning recipe's root/probe overrides if needed. | These integration checks require Docker, pinned images, and the probe's toolchain. Inspect the service fixture, probe, and resource lifecycle before execution; do not let a default select a different service. They do not prove live ingestion, persistence, exposure, or rollout. |
| Service-owned PostgreSQL role/permission tooling | Inspect the owning tests for the selected service/profile. Existing `make postgres-svc-role-test-fast` and `make postgres-svc-role-test` run their declared guard/adapter and disposable PostgreSQL fixtures; these suites are not selected by `SVC_NAME` or `ROLE_PROFILE`. | Confirm fixture coverage for the changed profile; add missing checks in the owner when required by the task. Managed Python and Docker are needed for disposable database tests. Live commands require supported `SVC_NAME=<service>` and `ROLE_PROFILE=<profile>` plus explicit target coordinates from that profile's runbook. A local fixture is not live permission evidence. |

For other infrastructure components, follow the affected section of the cluster-app runbook and
its verification links. If there is no automated check, record that gap, inspect the configuration
and rendered output where supported, and define a bounded manual scenario with expected results.
Do not invent a Make target or substitute installation/deployment for validation. A missing tool,
image, chart, credential, or target environment is a prerequisite gap: record the affected check
as `blocked` or `not run`, its reason, and the next action.

## Live operations and completion evidence

Apply the [agent execution boundaries](agent-security-rules.md) and the owning runbook. Establish
the authorized environment, context, namespaces, resource ownership, side effects, and recovery
path before live mutation. Preserve Flux ownership of QA desired state. Flux bootstrap may commit
and push generated manifests as well as change the cluster; deployment scope alone does not grant
those publication permissions. Handle credentials through the documented protected-file or host
interfaces, and keep values out of prompts, command arguments, and evidence.

For QA DNS, registry pulls, or stale Flux state, use the
[QA network recovery runbook](../../k8s/base-cluster-how-tos.md#qa-network-recovery) before
classifying the symptom as a repository defect. A recovery procedure still needs the applicable
host/cluster operation scope.

Record the selected checks, exact inputs/revisions, target identity when applicable, observed
results, cleanup, and remaining blockers using the
[post-step evidence format](../skills/ticket-post-step-workflow/SKILL.md#validation-evidence-record).
Keep render, fake-adapter, disposable integration, and live observations distinct. Claim deployed
behavior only from fresh evidence for the intended source/image and environment; retain unverified
outcomes when the required environment is unavailable.
