<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Phase 4 nice-to-have requirements ledger](#phase-4-nice-to-have-requirements-ledger)
  - [Maintaining the ledger](#maintaining-the-ledger)
  - [Sub-epic 4.2 Product catalog read pilot in Go](#sub-epic-42-product-catalog-read-pilot-in-go)
    - [Issue 134 Deploy Product Go alongside Java](#issue-134-deploy-product-go-alongside-java)
      - [issue-134-N01 Go package publication](#issue-134-n01-go-package-publication)
      - [issue-134-N02 Source metadata and release evidence](#issue-134-n02-source-metadata-and-release-evidence)
      - [issue-134-N03 Immutable digest verification and QA pinning](#issue-134-n03-immutable-digest-verification-and-qa-pinning)
      - [issue-134-N04 Historical-revision recovery](#issue-134-n04-historical-revision-recovery)
      - [issue-134-N05 Serialized and idempotent release repair](#issue-134-n05-serialized-and-idempotent-release-repair)
      - [issue-134-N06 Full validation replay during publication](#issue-134-n06-full-validation-replay-during-publication)
      - [issue-134-N07 Hosted disposable deployment validation](#issue-134-n07-hosted-disposable-deployment-validation)
      - [issue-134-N08 Signed supply-chain evidence](#issue-134-n08-signed-supply-chain-evidence)
    - [Issue 135 Product observability through Alloy](#issue-135-product-observability-through-alloy)
      - [issue-135-N01 Persistent local observability stack](#issue-135-n01-persistent-local-observability-stack)
      - [issue-135-N02 External alert notifications](#issue-135-n02-external-alert-notifications)
      - [issue-135-N03 Advanced trace sampling](#issue-135-n03-advanced-trace-sampling)
      - [issue-135-N04 Enhanced navigation between signals](#issue-135-n04-enhanced-navigation-between-signals)
      - [issue-135-N05 Extended runtime and database diagnostics](#issue-135-n05-extended-runtime-and-database-diagnostics)
      - [issue-135-N06 Production SLO and capacity analysis](#issue-135-n06-production-slo-and-capacity-analysis)
    - [Issue 154 Upgrade the QA observability platform](#issue-154-upgrade-the-qa-observability-platform)
      - [issue-154-N01 Rotate Loki MinIO service credentials](#issue-154-n01-rotate-loki-minio-service-credentials)
    - [Issue 156 Simplify CI workflows](#issue-156-simplify-ci-workflows)
      - [issue-156-N01 Automatic added-module selection](#issue-156-n01-automatic-added-module-selection)
      - [issue-156-N02 Automatic removed-module outcome](#issue-156-n02-automatic-removed-module-outcome)
      - [issue-156-N03 Module move and identity correlation](#issue-156-n03-module-move-and-identity-correlation)
    - [Issue 174 Standardize auxiliary tooling on Python](#issue-174-standardize-auxiliary-tooling-on-python)
      - [issue-174-N01 Python static-check CI workflow](#issue-174-n01-python-static-check-ci-workflow)
  - [Sub-epic 4.3 Shared Go runtime and further service migrations](#sub-epic-43-shared-go-runtime-and-further-service-migrations)
    - [Issue 214 Share Go service runtime foundations](#issue-214-share-go-service-runtime-foundations)
      - [issue-214-N01 gRPC deadline and drain helpers](#issue-214-n01-grpc-deadline-and-drain-helpers)
      - [issue-214-N02 Shared disposable test support](#issue-214-n02-shared-disposable-test-support)
      - [issue-214-N03 Shared integration client configuration](#issue-214-n03-shared-integration-client-configuration)
    - [Issue 215 Migrate policy-search along the shared Go runtime](#issue-215-migrate-policy-search-along-the-shared-go-runtime)
      - [issue-215-N01 Search pagination and streaming](#issue-215-n01-search-pagination-and-streaming)
      - [issue-215-N03 Search relevance and user experience](#issue-215-n03-search-relevance-and-user-experience)
      - [issue-215-N04 Per-agent policy-search authorization](#issue-215-n04-per-agent-policy-search-authorization)
      - [issue-215-N05 Dashboard migration using policy-search lessons](#issue-215-n05-dashboard-migration-using-policy-search-lessons)
      - [issue-215-N06 Remaining business-service migrations](#issue-215-n06-remaining-business-service-migrations)
      - [issue-215-N07 Evidence-based persistence evolution](#issue-215-n07-evidence-based-persistence-evolution)
      - [issue-215-N08 Bounded search caching](#issue-215-n08-bounded-search-caching)
      - [issue-215-N09 Phase 5 identity and edge modernization](#issue-215-n09-phase-5-identity-and-edge-modernization)
      - [issue-215-N10 Compatibility-driven platform and dependency upgrades](#issue-215-n10-compatibility-driven-platform-and-dependency-upgrades)
      - [issue-215-N11 Durable policy event publication](#issue-215-n11-durable-policy-event-publication)
      - [issue-215-N12 Additional guarded search administration automation](#issue-215-n12-additional-guarded-search-administration-automation)
      - [issue-215-N13 Java policy-search retirement](#issue-215-n13-java-policy-search-retirement)
  - [Ad hoc repository improvements](#ad-hoc-repository-improvements)
    - [Commit-message policy enforcement](#commit-message-policy-enforcement)
      - [adhoc-commit-msg-N01 Commit-message hook](#adhoc-commit-msg-n01-commit-message-hook)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Phase 4 nice-to-have requirements ledger

This ledger tracks optional improvements identified during Phase 4 ticket scoping, grouped by
their originating sub-epic and ticket. It preserves the requirement, reason for deferral, and
evidence needed if the improvement is selected later.

An entry does not add work to a ticket's accepted mandatory scope or block its completion.
Selecting an entry for delivery requires updating the receiving ticket's scope and acceptance
criteria. Mandatory requirements and unresolved completion blockers belong in their owning
ticket, not in this ledger.

## Maintaining the ledger

- Add a sub-epic section and a ticket subsection when optional requirements are identified.
  Keep entries under their originating ticket even if a different ticket later implements them.
- Use stable IDs in the form `issue-<number>-N<nn>` for ticket-originated entries and
  `adhoc-<topic>-N<nn>` for repository improvements without a ticket. Preserve IDs from the source
  requirements; assign the next unused number within that source or topic to new entries and never
  reuse a retired ID.
- For each entry, record the requirement, deferral rationale, reconsideration trigger, and
  acceptance evidence. Treat triggers as planning guidance, not automatic commitments.
- Maintain status and follow-up links in the ticket's index. When scheduling work, link the
  receiving ticket and record the decision date. When closing an entry, record the date and
  delivery evidence or the reason it was dropped. Retain the entry for traceability.
- If another ticket identifies the same improvement, reference the existing entry and add that
  ticket as a related source instead of creating a duplicate. Do not infer optional requirements
  for tickets whose scope has not yet been reviewed.

| Status | Meaning |
| --- | --- |
| Deferred | Retained for future consideration; no delivery commitment. |
| Scheduled | Included in an identified ticket's accepted delivery scope. |
| Delivered | Acceptance evidence is linked and the selected work is complete. |
| Dropped | Deliberately declined or superseded; the reason or replacement is recorded. |

## Sub-epic 4.2 Product catalog read pilot in Go

Parent: [issue 130](https://github.com/igor-baiborodine/insurance-hub/issues/130),
“Implement and qualify the Product catalog read pilot in Go.”

### Issue 134 Deploy Product Go alongside Java

Origin: the revised issue-134 requirements, optional items N01–N08. They were reclassified on
2026-10-14 when the Product-specific release and deployment workflows were replaced in the
specification by a lightweight generic Go service release model. These entries preserve the
removed release-hardening capabilities without adding them back to issue 134's mandatory scope.

Source ticket: [issue 134](https://github.com/igor-baiborodine/insurance-hub/issues/134).

| Ledger ID | Improvement | Status | Follow-up ticket or disposition |
| --- | --- | --- | --- |
| [issue-134-N01](#issue-134-n01-go-package-publication) | Go package publication | Deferred | Unassigned |
| [issue-134-N02](#issue-134-n02-source-metadata-and-release-evidence) | Source metadata and release evidence | Deferred | Unassigned |
| [issue-134-N03](#issue-134-n03-immutable-digest-verification-and-qa-pinning) | Immutable digest verification and QA pinning | Deferred | Unassigned |
| [issue-134-N04](#issue-134-n04-historical-revision-recovery) | Historical-revision recovery | Deferred | Unassigned |
| [issue-134-N05](#issue-134-n05-serialized-and-idempotent-release-repair) | Serialized and idempotent release repair | Deferred | Unassigned |
| [issue-134-N06](#issue-134-n06-full-validation-replay-during-publication) | Full validation replay during publication | Deferred | Unassigned |
| [issue-134-N07](#issue-134-n07-hosted-disposable-deployment-validation) | Hosted disposable deployment validation | Deferred | Unassigned |
| [issue-134-N08](#issue-134-n08-signed-supply-chain-evidence) | Signed supply-chain evidence | Deferred | Unassigned |

#### issue-134-N01 Go package publication

**Requirement:** Publish a versioned Go module or another reusable Go package artifact as part of
the selected service's release.

**Why deferred:** Product is deployed as a container and no external consumer requires a published
Go package. Package coordinates, compatibility guarantees, access, retention, and ownership would
add release work without supporting the current pilot.

**Reconsider when:** A named consumer must import a repository Go module independently of the
service image, and its compatibility and distribution requirements are agreed.

**Acceptance evidence:** The consumer resolves the versioned package from the agreed registry;
module identity, compatibility, access, retention, and deprecation policy are documented; and
container-only services remain unaffected.

#### issue-134-N02 Source metadata and release evidence

**Requirement:** Preserve the full source commit and detailed release identity in OCI labels and an
uploaded evidence bundle that correlates the source, image tags, QA manifest commit, scoped Git
tag, and GitHub Release.

**Why deferred:** The mandatory scoped source tag, short-SHA image tag, semantic image tag, QA bot
commit, and GitHub Release provide sufficient traceability for the pilot. Building, validating,
uploading, retaining, and later discovering a separate evidence bundle increases runner time and
storage.

**Reconsider when:** Audit, incident response, or promotion workflows repeatedly need a durable
machine-readable release record that cannot be reconstructed reliably from Git, GHCR, and GitHub
Release identities.

**Acceptance evidence:** OCI labels and the evidence bundle agree with the source tag, both image
tags, QA commit, and GitHub Release. The schema, redaction rules, retention, discovery procedure,
and owner are documented and tested.

#### issue-134-N03 Immutable digest verification and QA pinning

**Requirement:** Resolve the registry digest after publication, prove the semantic and short-SHA
tags resolve to the same image, pull and validate that digest using QA-equivalent access, and commit
the digest as QA desired state.

**Why deferred:** Issue 134 accepts an immutable-by-convention semantic version for non-production
QA. Registry pullback, digest inspection, metadata correlation, and digest-oriented manifest
finalization materially increased workflow duration and implementation complexity.

**Reconsider when:** The service enters a promotion stage that requires cryptographic artifact
identity, tags are observed moving or resolving inconsistently, or an environment policy requires
digest-only deployment.

**Acceptance evidence:** Both published tags resolve to one digest; pullback and runtime-image
checks pass using the intended access path; QA records that digest; Flux deploys it; and the running
Pod image ID is correlated with the release record.

#### issue-134-N04 Historical-revision recovery

**Requirement:** Allow an operator to select an earlier full source revision from canonical
`main`, build it from an isolated or detached checkout, and recover its incomplete release without
using current source files as build inputs.

**Why deferred:** The pilot can recover through a reviewed corrective merge and a new release.
Historical-revision recovery introduced detached-worktree, environment, and recursive-Make identity
problems and consumed substantial debugging time without a demonstrated operational need.

**Reconsider when:** Releasing a corrective commit cannot restore a required missing artifact, or
an operational policy requires exact recovery of a previously accepted source revision.

**Acceptance evidence:** The selected revision is proven to belong to canonical `main`; every build
and release input comes from that revision; trusted current automation cannot contaminate it; and
the resulting tag, image, release, and QA identities remain unambiguous.

#### issue-134-N05 Serialized and idempotent release repair

**Requirement:** Serialize releases and make retries detect, validate, and safely converge existing
Git tags, GitHub Releases, image tags, metadata, and QA manifest commits after interruption or
partial success.

**Why deferred:** One-service-at-a-time delivery and corrective releases are sufficient for the
current repository. Transaction-style ordering, collision detection, fake adapters, and automatic
repair were the main sources of complexity in the Product-specific workflow.

**Reconsider when:** Concurrent releases become normal, partial external side effects occur often,
or manual cleanup creates unacceptable risk or operator effort.

**Acceptance evidence:** Controlled concurrent and interrupted executions never move an accepted
identity, overwrite a different image, or create conflicting desired state. Retries converge from
every tested failure boundary, with documented abort and manual recovery procedures.

#### issue-134-N06 Full validation replay during publication

**Requirement:** Re-run the complete Go module, coverage, tooling, release fake-adapter, manifest,
database, security, image, and lifecycle validation suites inside the publishing workflow before
release mutations.

**Why deferred:** Pull-request and canonical-main CI plus owning Make targets already validate the
source. Replaying the full suite during publication substantially increases hosted execution time
and third-party CI cost without supplying a distinct acceptance environment.

**Reconsider when:** A release-specific failure repeatedly escapes existing CI, or publication uses
inputs or an environment materially different from the validated source build.

**Acceptance evidence:** The duplicated gate is tied to a demonstrated failure mode, blocks the
release when reproduced, and has measured runtime and cost. Test ownership, cache behavior, and
the reason ordinary CI cannot own the check are documented.

#### issue-134-N07 Hosted disposable deployment validation

**Requirement:** Run a dedicated hosted workflow that creates disposable Kubernetes and database
resources, builds or loads the service image, and exercises deployment, exposure, reader,
lifecycle, drain, and cleanup behavior for every relevant change.

**Why deferred:** The completed foundation and targeted acceptance work already provide disposable
lifecycle evidence. Recreating Kind, PostgreSQL, images, fixtures, and service tests on routine
changes consumes substantial hosted time and duplicates local or module validation.

**Reconsider when:** Deployment tooling changes frequently, a regression escapes existing render
and targeted checks, or hosted validation becomes cheaper than maintaining the equivalent local
acceptance procedure.

**Acceptance evidence:** The workflow covers a specific otherwise-uncovered risk, uses bounded
runtime and resources, cleans up after success and failure, produces actionable results, and never
mutates shared local-dev or QA infrastructure.

#### issue-134-N08 Signed supply-chain evidence

**Requirement:** Sign service images and release metadata and publish an SBOM, provenance statement,
or other attestation that can be verified before deployment or promotion.

**Why deferred:** No consumer or policy currently requires signed artifacts for the Product pilot.
Signing identities, keyless or key-based trust, verification enforcement, retention, and
revocation would broaden the release and operational scope.

**Reconsider when:** A security policy, external consumer, preproduction promotion gate, or
compliance requirement names the expected evidence and verification boundary.

**Acceptance evidence:** Images and metadata are signed or attested by an agreed identity; SBOM and
provenance content is reproducible; verification is enforced at the agreed boundary; and identity
rotation, revocation, retention, and incident response are documented.

### Issue 135 Product observability through Alloy

Origin: the accepted issue-135 requirements, optional items N01–N06. The requirements were accepted
on 2026-10-13; these six items remain optional and unscheduled. The entries below preserve their
scope and evidence expectations, with planning triggers for later reconsideration.

Source ticket: [issue 135](https://github.com/igor-baiborodine/insurance-hub/issues/135).

| Ledger ID | Improvement | Status | Follow-up ticket or disposition |
| --- | --- | --- | --- |
| [issue-135-N01](#issue-135-n01-persistent-local-observability-stack) | Persistent local observability stack | Deferred | Unassigned |
| [issue-135-N02](#issue-135-n02-external-alert-notifications) | External alert notifications | Deferred | Unassigned |
| [issue-135-N03](#issue-135-n03-advanced-trace-sampling) | Advanced trace sampling | Deferred | Unassigned |
| [issue-135-N04](#issue-135-n04-enhanced-navigation-between-signals) | Enhanced navigation between signals | Deferred | Unassigned |
| [issue-135-N05](#issue-135-n05-extended-runtime-and-database-diagnostics) | Extended runtime and database diagnostics | Deferred | Unassigned |
| [issue-135-N06](#issue-135-n06-production-slo-and-capacity-analysis) | Production SLO and capacity analysis | Deferred | Unassigned |

#### issue-135-N01 Persistent local observability stack

**Requirement:** Provide a one-command persistent local Alloy, Loki, Tempo, Prometheus, and
Grafana stack for telemetry exploration.

**Why deferred:** Issue 135 already requires a collector-independent disabled mode, safe local
logs, and disposable enabled telemetry tests. Those capabilities meet the local-development
requirement with less resource use on the maintainer's laptop.

**Reconsider when:** Repeated local debugging needs interactive backend queries and dashboards,
and measured laptop capacity supports keeping the stack running.

**Acceptance evidence:** The stack starts without QA dependencies, has documented CPU/memory and
storage requirements, and cleanup removes only resources owned by the local stack.

#### issue-135-N02 External alert notifications

**Requirement:** Deliver alert notifications to an agreed email or chat destination, with
escalation routing and operational ownership.

**Why deferred:** Issue 135's mandatory evaluated alert rules and runbook make pilot failure
conditions actionable. Notification destinations, credentials, and on-call responsibilities
require a separate operational decision.

**Reconsider when:** Monitoring must support unattended operation or a named responder needs
notifications outside Grafana or the alerting interface.

**Acceptance evidence:** An agreed test destination receives firing and resolved notifications
without sensitive data. Routing, escalation, and ownership are documented.

#### issue-135-N03 Advanced trace sampling

**Requirement:** Add tail or adaptive sampling with preferential retention of slow or failed
traces.

**Why deferred:** Explicit parent-aware sampling and deterministic test captures meet the pilot's
correlation needs. Advanced sampling adds collector state, resource use, and tuning work that
should be justified by actual trace volume or diagnostic gaps.

**Reconsider when:** Trace volume exceeds the agreed budget, or the existing sampling policy
repeatedly misses slow or failed requests needed for diagnosis.

**Acceptance evidence:** Representative traffic demonstrates a measurable retention benefit,
bounded collector memory, and preserved trace continuity.

#### issue-135-N04 Enhanced navigation between signals

**Requirement:** Add metric exemplars, automated service maps, and richer navigation between
metrics, traces, and logs.

**Why deferred:** Basic trace-ID log queries and investigation links are already mandatory for
issue 135. Richer navigation improves convenience without determining whether an operator can
investigate a failure.

**Reconsider when:** Operators repeatedly need manual correlation steps, or additional migrated
services make the request topology difficult to follow.

**Acceptance evidence:** Links and maps work for HTTP and gRPC paths without introducing
unbounded metric dimensions or indexed log labels.

#### issue-135-N05 Extended runtime and database diagnostics

**Requirement:** Add detailed garbage-collection or database-pool panels, or additional internal
spans, to address demonstrated diagnostic gaps.

**Why deferred:** Mandatory instrumentation already covers transport, database access, and basic
resource diagnosis. More detail should answer a concrete operational question rather than add
instrumentation to every function.

**Reconsider when:** A repeatable performance or reliability problem cannot be explained by the
existing transport, database, pool, and runtime signals.

**Acceptance evidence:** The additional signals answer the identified diagnostic question, use
a bounded field set, and have measured overhead.

#### issue-135-N06 Production SLO and capacity analysis

**Requirement:** Add production service-level-objective (SLO) burn-rate alerts, long-term trends,
and capacity forecasting.

**Why deferred:** The read pilot keeps existing traffic on Java and has no qualified production
workload. Issue 135 owns basic pilot thresholds;
[issue 136](https://github.com/igor-baiborodine/insurance-hub/issues/136) owns integrated
performance qualification, and
[issue 137](https://github.com/igor-baiborodine/insurance-hub/issues/137) owns cutover criteria.
Those dependencies do not assign this optional work to either ticket.

**Reconsider when:** A representative workload, service objectives, observation windows, and
retention needs have been agreed, with sufficient measurements to support longer-term analysis.

**Acceptance evidence:** Record the agreed workload, SLO and evaluation window, retention, and
capacity measurements. Production conclusions must be supported by representative evidence;
the four-product pilot fixture alone is insufficient.

### Issue 154 Upgrade the QA observability platform

Origin: issue-154 Step 3 execution evidence from 2026-10-15. A read-only Kubernetes preflight
command intended to list Secret key names rendered the Loki MinIO service-user Secret's base64
data in the maintainer-visible interactive output. The ticket owner reclassified credential
rotation as optional follow-up after the clean Loki replacement and retained route passed all
runtime security, ingestion, query, persistence, resource, and recovery gates.

Source ticket: [issue 154](https://github.com/igor-baiborodine/insurance-hub/issues/154).

| Ledger ID | Improvement | Status | Follow-up ticket or disposition |
| --- | --- | --- | --- |
| [issue-154-N01](#issue-154-n01-rotate-loki-minio-service-credentials) | Rotate Loki MinIO service credentials | Deferred | Unassigned |

#### issue-154-N01 Rotate Loki MinIO service credentials

**Requirement:** Rotate the QA Loki MinIO service-user credentials with controlled old/new overlap.
Preserve tenant `qa-minio-loki`, bucket `loki-logs`, policy `s3-policy-loki-logs`, and the fixed
Secret name `qa-minio-loki-svc-user-creds` in its source and `qa-monitoring` copy. Restart Loki
after updating both Secret copies, prove ingestion, query, restart persistence, and bucket access,
then revoke the old user and prove its credentials no longer work. Keep credential values out of
commands, process listings, logs, and recorded evidence.

**Why deferred:** The affected credentials remain functional only in the QA observability path,
and issue 154's mandatory platform-upgrade behavior passed with the existing scoped user, policy,
tenant, bucket, and runtime security boundary. Rotation would add a separate credential lifecycle
mutation after the Loki stage had passed and is not required to qualify the pinned observability
baseline.

**Reconsider when:** The QA MinIO credential workflow is hardened, Loki moves beyond this isolated
QA boundary, a security review requires revocation after maintainer-visible exposure, or another
change already needs to rotate or replace the Loki service user.

**Acceptance evidence:** A redacted before/after inventory identifies both fixed-name Secret
copies and the scoped policy without printing values. The replacement user authenticates only to
the allowed bucket; Loki remains Ready and fresh direct and Alloy logs remain queryable across a
restart; the old user is revoked and denied; unrelated buckets, users, policies, tenants, and
workloads are unchanged; and no credential material appears in retained output.

### Issue 156 Simplify CI workflows

Origin: the revised issue-156 requirements. Go module lifecycle handling was reclassified on
2026-10-15. Automatic added-service selection was promoted into issue 156 after the workflow scope
was expanded to existing and upcoming `services/*-service` modules. Decommissioning, directory
movement, identity changes, and rename correlation remain uncommon and deferred.

Source ticket: [issue 156](https://github.com/igor-baiborodine/insurance-hub/issues/156).

| Ledger ID | Improvement | Status | Follow-up ticket or disposition |
| --- | --- | --- | --- |
| [issue-156-N01](#issue-156-n01-automatic-added-module-selection) | Automatic added-module selection | Promoted | Included in issue 156 step 3 |
| [issue-156-N02](#issue-156-n02-automatic-removed-module-outcome) | Automatic removed-module outcome | Deferred | Unassigned |
| [issue-156-N03](#issue-156-n03-module-move-and-identity-correlation) | Module move and identity correlation | Deferred | Unassigned |

#### issue-156-N01 Automatic added-module selection

**Requirement:** Automatically select and validate a newly added immediate
`services/<name>-service` module through the standard Make capabilities while still enforcing the
one-module rule. Inventory ownership and standalone resolution remain mandatory onboarding checks.

**Disposition:** Included in the revised issue-156 scope. Generic qualifying paths and inline
module-root derivation select an immediate `services/<name>-service` module without a workflow edit.
The module must still provide the standard Make capabilities and complete inventory and ownership
onboarding.

**Delivered when:** Step 3's workflow and validators prove a future standard service is triggered,
selected, and assigned generic business-service metadata while preserving the one-module rule.

**Acceptance evidence:** Workflow fixtures prove existing and future service selection, generic
business-service metadata, and mixed existing/new module rejection without service-specific
workflow logic. Existing topology checks continue to reject missing inventory ownership,
standalone-resolution failures, and missing owning validation capabilities.

#### issue-156-N02 Automatic removed-module outcome

**Requirement:** Recognize a complete Go module removal with matching inventory removal as one
logical lifecycle change, report a successful removed-module outcome, and avoid bootstrapping or
validating the deleted directory while rejecting partial removal and stale ownership.

**Why deferred:** Module decommissioning is rare and requires deliberate consumer, topology,
documentation, release, and deployment review outside ordinary PR validation. Those checks remain
with the local decommissioning procedure rather than being embedded in ordinary workflow selection.

**Reconsider when:** Multiple Go modules are scheduled for decommissioning or repeated removals show
that manual coordination adds material effort without improving review quality.

**Acceptance evidence:** Base/head fixtures prove complete removal, partial deletion, stale
inventory, remaining consumers, and mixed-module changes. Only a complete approved removal returns
the removed outcome, invokes no module setup, and preserves required local topology evidence.

#### issue-156-N03 Module move and identity correlation

**Requirement:** Correlate base/head inventory and `go.mod` identity so a pure module-directory move
is treated as one logical module, while a module identity change is treated as removal plus addition.

**Why deferred:** Directory and module-identity migrations are exceptional repository maintenance.
Automatic correlation and rejection rules add substantial policy and test surface to the common PR
path without a current migration need.

**Reconsider when:** A planned repository restructuring or module-path migration needs hosted
validation during the move and its identity, consumer, import, and rollback rules are agreed.

**Acceptance evidence:** Disposable Git histories prove pure directory moves, moves with content
changes, identity-preserving and identity-changing cases, stale inventory, and ambiguous ownership.
Only the agreed lifecycle forms produce a selected outcome; other cases fail before bootstrap with
explicit diagnostics.

### Issue 174 Standardize auxiliary tooling on Python

Origin: issue-174 requirements refinement on 2026-10-19. Hosted enforcement of mechanically
checkable Python rules is retained as a separate follow-up to the ticket's local tooling and
policy work.

Source ticket: [issue 174](https://github.com/igor-baiborodine/insurance-hub/issues/174).

| Ledger ID | Improvement | Status | Follow-up ticket or disposition |
| --- | --- | --- | --- |
| [issue-174-N01](#issue-174-n01-python-static-check-ci-workflow) | Python static-check CI workflow | Deferred | Unassigned |

#### issue-174-N01 Python static-check CI workflow

**Requirement:** Add a dedicated CI workflow that enforces the mechanically checkable Python
rules established by issue 174 through non-mutating static checks, including Ruff formatting
verification and linting. Reuse the canonical configuration, pinned tooling, and owning Make
targets, with a dedicated static-only target where existing targets also execute repository code.
Define triggers for relevant Python source, tooling configuration, dependency pins, Make targets,
and workflow changes. Document checked paths and justified exclusions so new auxiliary Python
files cannot silently escape validation.

The workflow may execute the installed formatter/linter and necessary setup tools, but must not
execute or import repository Python scripts or modules, collect or run pytest tests, or invoke
operational, integration, or acceptance runners. It must not start application infrastructure or
perform deployment or administrative mutations. Formatting checks must not rewrite source, and
linting must not apply fixes. Keep behavioral rules and conventions that static tools cannot
establish, including meaningful Given/When/Then separation, under local validation and review.

**Why deferred:** Issue 174 establishes local Python checks and explicitly excludes a new Python
CI workflow while preserving issue 156's CI boundaries. Hosted static enforcement requires a
separately accepted follow-up scope; this ledger entry does not expand issue 174 or change
existing workflow triggers.

**Reconsider when:** Issue 174's canonical Python rules, pinned tools, and static Make targets
are available, and hosted enforcement is selected for a follow-up ticket.

**Acceptance evidence:** Hosted runs accept compliant files and reject representative formatting
and lint violations with actionable diagnostics using the same configuration and pins as local
checks. Trigger and scope evidence covers relevant source/configuration changes, newly added
Python files, and documented exclusions. Workflow and invoked-target inspection, supported by
run logs and a harmless execution sentinel, proves repository Python code is neither imported
nor executed, including through setup or transitive Make dependencies; no test collection or
infrastructure startup occurs. Checks leave source files unchanged. Document enforced rules,
rules reserved for review or runtime tests, and measured workflow duration; existing Go, legacy,
and release workflow behavior remains unchanged.

## Sub-epic 4.3 Shared Go runtime and further service migrations

Parent: [issue 215](https://github.com/igor-baiborodine/insurance-hub/issues/215).
Issue 214 supplies the shared runtime foundation. The issue-215 requirements assessment selects
policy-search migration, including separately gated cutover and subsequent Java policy-search
retirement. The revised parent specification and numbered child issues now record this scope;
delivery remains separately gated.

### Issue 214 Share Go service runtime foundations

Origin: reuse assessment N01–N03, initially assessed as ad hoc work and assigned to issue 214
on 2026-10-28. The source IDs remain N01–N03; their ledger-qualified IDs are issue-214-N01–N03.
The user requested retention here without promoting these items into mandatory delivery.

Source ticket: [issue 214](https://github.com/igor-baiborodine/insurance-hub/issues/214).
Local assessment: `ai/artifacts/epic-4.3/issue-214/issue-214-requirements.md` (ignored local evidence).

| Ledger ID | Improvement | Status | Follow-up ticket or disposition |
| --- | --- | --- | --- |
| [issue-214-N01](#issue-214-n01-grpc-deadline-and-drain-helpers) | gRPC, deadline and drain helpers | Deferred | Unassigned |
| [issue-214-N02](#issue-214-n02-shared-disposable-test-support) | Shared disposable test support | Deferred | Unassigned |
| [issue-214-N03](#issue-214-n03-shared-integration-client-configuration) | Shared integration/client configuration | Deferred | Unassigned |

#### issue-214-N01 gRPC deadline and drain helpers

**Requirement:** Extract narrowly reusable gRPC setup, request deadline or server-drain mechanics
where multiple consumers demonstrate the same contract. Preserve service-owned registration,
business error mapping, final transport status, cancellation and resource ownership.

**Why deferred:** Transport and lifecycle implementations have materially different responsibilities.
Extracting them now risks changing final-status or cleanup behavior and is not necessary to share
configuration, logging, telemetry providers and health handlers.

**Reconsider when:** Core runtime adoption is stable and another consumer demonstrates the same
bounded transport or lifecycle helper requirement.

**Acceptance evidence:** Real transport tests in all affected consumers preserve final status,
including decoding and response-size failures, caller deadlines and cancellation, and bounded
graceful/forced shutdown. The helper neither imports a business service nor takes implicit
ownership of its resources.

#### issue-214-N02 Shared disposable test support

**Requirement:** Share PostgreSQL or OTLP collector test-support mechanics when repeated fixture
setup has a stable cross-consumer contract, keeping provisioning outside production imports.

**Why deferred:** Existing service and SDK fixtures have different ownership and assumptions.
Current mandatory tests remain required; this entry concerns reuse of their support code only.

**Reconsider when:** Multiple consumers repeat equivalent test setup and a small shared interface
can preserve their independent assertions and fixture provenance.

**Acceptance evidence:** Disposable tests remain isolated, bounded and reproducible, clean up on
success and failure, and use no shared/live data. Dependency inspection proves test provisioning
does not enter production packages or executables; independent consumer behavior checks remain.

#### issue-214-N03 Shared integration client configuration

**Requirement:** Reuse configuration or client-construction mechanics for storage, messaging or
search integrations only where actual Java-to-Go migrations establish matching requirements.

**Why deferred:** Product does not establish future services' persistence, transaction, replay,
security or integration contracts. A speculative abstraction could impose the wrong behavior.

**Reconsider when:** Two migrations provide compatible requirements and independently captured
parity fixtures for the same integration boundary.

**Acceptance evidence:** Each consumer's success, failure, timeout, cancellation, replay and
security cases pass against its own baseline. Shared code preserves service ownership and safe
configuration without introducing shared business models or forcing a persistence-stack change.

### Issue 215 Migrate policy-search along the shared Go runtime

Origin: issue-215 requirements N01/N03–N13 and the user's 2026-10-30 requests to retain
per-agent visibility and turn useful migration exclusions into deferred future work. N04 retains
the previously accepted access-model decision. The user subsequently promoted N13 into issue
215's mandatory retirement scope on 2026-10-30; the other deferred entries do not expand its
mandatory delivery scope.

Source ticket: [issue 215](https://github.com/igor-baiborodine/insurance-hub/issues/215).
Local assessment: `ai/artifacts/epic-4.3/issue-215-requirements.md`, optional requirements and
accepted Q04/Q05 (ignored local evidence).

Related requirement N02 reuses [issue-214-N02](#issue-214-n02-shared-disposable-test-support)
and [issue-214-N03](#issue-214-n03-shared-integration-client-configuration). No duplicate shared
client or test-support entry is created. Other-service and Phase 5 items below are deferred
relative to issue 215; existing roadmap commitments keep their own priority and required gates.

| Ledger ID | Improvement | Status | Follow-up ticket or disposition |
| --- | --- | --- | --- |
| [issue-215-N01](#issue-215-n01-search-pagination-and-streaming) | Search pagination and streaming | Deferred | Unassigned; select through a separately accepted receiving scope |
| [issue-215-N03](#issue-215-n03-search-relevance-and-user-experience) | Search relevance and user experience | Deferred | Unassigned; select through a separately accepted receiving scope |
| [issue-215-N04](#issue-215-n04-per-agent-policy-search-authorization) | Per-agent policy-search authorization | Deferred | Unassigned; select through a separately accepted receiving scope |
| [issue-215-N05](#issue-215-n05-dashboard-migration-using-policy-search-lessons) | Dashboard migration using policy-search lessons | Deferred | Unassigned; select through a separately accepted receiving scope |
| [issue-215-N06](#issue-215-n06-remaining-business-service-migrations) | Remaining business-service migrations | Deferred | Unassigned; select through a separately accepted receiving scope |
| [issue-215-N07](#issue-215-n07-evidence-based-persistence-evolution) | Evidence-based persistence evolution | Deferred | Unassigned; select through a separately accepted receiving scope |
| [issue-215-N08](#issue-215-n08-bounded-search-caching) | Bounded search caching | Deferred | Unassigned; select through a separately accepted receiving scope |
| [issue-215-N09](#issue-215-n09-phase-5-identity-and-edge-modernization) | Phase 5 identity and edge modernization | Deferred | Unassigned; select through a separately accepted receiving scope |
| [issue-215-N10](#issue-215-n10-compatibility-driven-platform-and-dependency-upgrades) | Compatibility-driven platform and dependency upgrades | Deferred | Unassigned; select through a separately accepted receiving scope |
| [issue-215-N11](#issue-215-n11-durable-policy-event-publication) | Durable policy event publication | Deferred | Unassigned; select through a separately accepted receiving scope |
| [issue-215-N12](#issue-215-n12-additional-guarded-search-administration-automation) | Additional guarded search administration automation | Deferred | Unassigned; select through a separately accepted receiving scope |
| [issue-215-N13](#issue-215-n13-java-policy-search-retirement) | Java policy-search retirement | Scheduled | Promoted on 2026-10-30 into [issue 215](https://github.com/igor-baiborodine/insurance-hub/issues/215): mandatory M17, child [#236](https://github.com/igor-baiborodine/insurance-hub/issues/236) (C09); not delivered |

#### issue-215-N01 Search pagination and streaming

**Requirement:** Provide an explicitly versioned way to retrieve more than the current 100 search
hits. Select stable cursor/PIT pagination first when it meets the consumer need; add streaming only
for a demonstrated bulk-consumption use case, with defined ordering, framing, cancellation and
backpressure.

**Why deferred:** Accepted D01 keeps a relevance-first 100-hit limit but introduces explicit
date/number tie-breakers as a migration difference. Retrieval beyond that cap changes result
membership, ordering and client behavior and is independently deliverable.

**Reconsider when:** Users need access to results beyond the cap, or measured payload/workload
evidence justifies a bulk interface.

**Acceptance evidence:** Independent fixtures cover more than 100 matches, page boundaries,
concurrent index updates, cursor expiry and missing/duplicate results. Gateway and browser clients
implement the agreed contract; any stream proves bounded memory, cancellation and slow-reader
handling. Preserve the legacy route until its replacement is explicitly accepted.

#### issue-215-N03 Search relevance and user experience

**Requirement:** Improve relevance, query construction and the policy-search UI through an
explicitly accepted product contract. Candidate improvements include structured filters, safer query
input and clearer ranking; select individual changes based on user needs. Accepted D01 already
requires a visible search-unavailable error in place of the gateway's empty-result fallback, so
that error behavior is mandatory migration scope rather than a deferred option.

**Why deferred:** The remaining relevance, query-construction and UI candidates change observable
behavior beyond the accepted D01 migration contract. Backend error visibility and privacy remain
mandatory during the Java-to-Go transition.

**Reconsider when:** User feedback identifies poor results, confusing query syntax or outages
mistaken for an empty result set.

**Acceptance evidence:** Approved before/after request/result/error fixtures and a representative
search corpus establish expected relevance and query semantics. Mandatory D01 browser/gateway
tests distinguish no matches from failures; any later UI change preserves accessibility and safe
rendering. Verify that query text or policy data does not leak through telemetry.

#### issue-215-N04 Per-agent policy-search authorization

**Requirement:** If policy visibility is to be restricted to an agent's own policies, define
explicit authorization rules and any permitted supervisor or shared-access exceptions. Propagate
trusted caller identity to each applicable search boundary, supply authoritative ownership data
in the search projection, and enforce the accepted visibility rules consistently through HTTP
and gRPC. Define ownership backfill, reassignment and event replay behavior; do not trust a
caller-supplied agent identifier as authorization evidence.

**Why deferred:** The accepted migration preserves authenticated Java gateway access, internal-only
Go endpoints and existing search results without adding role or per-agent filtering. Restricting
results changes business behavior and requires identity and ownership contracts beyond query
parity. Existing authentication, internal exposure controls, privacy and dependency permissions
remain mandatory; this deferral does not weaken those safeguards.

**Reconsider when:** The product/security owner requests own-policy visibility or an applicable
access requirement establishes that restriction. Accept the policy and receiving ticket's scope
before implementation; an applicable mandatory protection must not remain optional merely because
it was originally recorded here.

**Acceptance evidence:** Independent fixtures prove an agent can find permitted policies but
cannot retrieve another agent's policies through either reachable transport, including wildcard
and crafted queries. Tests reject missing or forged identity and spoofed agent headers, verify
any explicitly authorized exceptions, and define safe denial for missing/unknown ownership.
Backfill, ownership changes and replay preserve correct visibility without broadening access;
direct backend calls cannot bypass the accepted authorization policy.

#### issue-215-N05 Dashboard migration using policy-search lessons

**Requirement:** Assess and separately scope migration of dashboard-service to Go using the
validated Elasticsearch, event-processing and runtime lessons from policy-search. Capture
dashboard-specific aggregation, bucket-limit, date/timezone, rounding and attribution behavior
before selecting reusable mechanics.

**Why deferred:** Dashboard is the next recommended service in the Phase 4 review, but its analytics
contract and read model are independent of policy-search. Deferral is relative to issue 215, not a
cancellation or optional reclassification of the wider migration roadmap.

**Reconsider when:** Policy-search qualification provides reusable evidence and the roadmap owner
selects the dashboard migration.

**Acceptance evidence:** A receiving requirements/specification set identifies independent Java
oracles, policy_stats ownership, isolated event consumption and dashboard consumers. Its eventual
delivery proves aggregation/rounding/timezone parity, recovery, access controls and separately gated
cutover; passing policy-search tests alone does not qualify dashboard.

#### issue-215-N06 Remaining business-service migrations

**Requirement:** Scope the remaining Go migrations as independent service initiatives, including
policy creation/details/termination, pricing, payments, documents and other selected business
services. Preserve service-owned business rules and producer/consumer contracts while applying
proven runtime and operational patterns.

**Why deferred:** These services introduce separate write, monetary, expression, document/storage or
realtime responsibilities. They cannot be delivered or qualified as extensions of the policy-search
query migration; existing roadmap commitments retain their own priority.

**Reconsider when:** The roadmap owner selects a service and its Phase 4 adoption prerequisites can
be assessed.

**Acceptance evidence:** Each receiving ticket records source-specific behavior, affected callers,
state/event ownership, mandatory safeguards, measurable acceptance and rollback gates. Compatibility
fixtures cover interactions with migrated policy-search, including policy events; no blanket
business-rule redesign or automatic migration of every service follows from this entry.

#### issue-215-N07 Evidence-based persistence evolution

**Requirement:** Evaluate PostgreSQL ownership changes or an alternative relational search
projection only for a demonstrated operational or product need. Compare the existing Elasticsearch
design with bounded alternatives using representative data and queries before proposing a storage
change.

**Why deferred:** Policy-search currently owns a derived Elasticsearch view, not the authoritative
policy database. Introducing a relational store or transferring PostgreSQL ownership has no
established need in issue 215.

**Reconsider when:** Measured cost, recovery, query limitations or a separately accepted service
migration creates a concrete persistence requirement.

**Acceptance evidence:** A decision record compares query semantics, relevance, latency, freshness,
recovery and operating cost against the existing store. Any selected change has an identified
schema/data owner, migration and consistency strategy, least-privilege access, representative parity
tests and a reversible handoff; an inconclusive comparison does not authorize replacement.

#### issue-215-N08 Bounded search caching

**Requirement:** Add a bounded cache for approved search operations only when it reduces measured
load or latency. Define freshness/TTL, invalidation after registration and termination, capacity
limits, bypass behavior and cache-key isolation appropriate to the accepted access model.

**Why deferred:** Caching creates an additional stale-state and data-isolation boundary without
evidence that the existing bounded query path needs it.

**Reconsider when:** Representative workloads miss accepted latency or capacity objectives and
profiling identifies repeated cacheable queries as a material cause.

**Acceptance evidence:** Before/after measurements demonstrate the benefit. Tests cover termination
visibility, expiration/invalidation, concurrent updates, eviction, cold starts, backend failure and
bypass without treating cached success as proof of fresh indexing. Keys/results respect any future
ownership policy and avoid sensitive telemetry.

#### issue-215-N09 Phase 5 identity and edge modernization

**Requirement:** Assess and separately adopt the Phase 5 identity provider, edge translation and
service-mesh/workload-trust changes where selected by that phase. Give each transition explicit
user/workload identity, route, credential and mixed Java/Go compatibility contracts.

**Why deferred:** Issue 215 retains the Java gateway and accepted search access behavior. Platform
replacement is a separate migration; current authentication, verified trust and internal exposure
controls remain mandatory.

**Reconsider when:** Phase 5 owners approve a concrete identity, gateway or mesh adoption scope and
its interoperability prerequisites are available.

**Acceptance evidence:** Positive/negative identity and direct-backend tests, credential lifecycle
and mixed-peer trust tests, HTTP/gRPC mapping parity and independently rehearsed rollback qualify
each selected transition. Coordinate with issue-215-N04 if per-agent authorization is selected;
workload identity alone does not establish policy ownership.

#### issue-215-N10 Compatibility-driven platform and dependency upgrades

**Requirement:** Plan justified Elasticsearch, Kafka, shared runtime/library and tool upgrades
separately from search migration, with an explicit compatibility matrix and affected-consumer scope.
Select releases for a documented support, security or capability need rather than upgrading
unrelated dependencies opportunistically.

**Why deferred:** Unrelated version churn obscures migration evidence. Selecting compatible, secure
initial pins and satisfying required vulnerability checks are already mandatory.

**Reconsider when:** Support policy, an advisory, incompatibility or an accepted capability
requirement calls for an upgrade.

**Acceptance evidence:** Exact version and dependency identities, relevant advisory assessment and
all affected query/event/runtime/consumer gates pass. Platform data/configuration compatibility and
rollback or supported forward recovery are demonstrated. If an upgrade is required for safe
delivery, promote it to a mandatory prerequisite instead of leaving it deferred.

#### issue-215-N11 Durable policy event publication

**Requirement:** Strengthen policy-service publication with a transactional outbox or another
explicitly justified durable publication mechanism when the desired guarantee exceeds the accepted
producer baseline. Define publication atomicity, relay ownership, event identity/order, retries,
deduplication and reconciliation while retaining Java/Go consumer compatibility.

**Why deferred:** The policy producer belongs to another service. Search consumer retries cannot
repair events that were never published, but a producer redesign is not automatically required when
accepted recovery objectives are already met.

**Reconsider when:** Baseline analysis finds an unacceptable DB-to-broker loss window, or a later
requirement demands stronger publication guarantees.

**Acceptance evidence:** Fault injection covers process crashes before/after DB commit, broker
failure, ambiguous sends, relay restart and duplicate delivery; every committed eligible policy
change eventually reaches the required consumers under the accepted guarantee. The design records
schema/relay ownership and rollout/rollback. If issue 215's accepted recovery objectives require
this, it becomes a separately scoped mandatory dependency before ownership transfer.

#### issue-215-N12 Additional guarded search administration automation

**Requirement:** Add operator conveniences beyond the mandatory migration runbooks: previewable
index maintenance, controlled replay/offset administration, and reusable deployment or
route-transition orchestration where repeated operations justify them. Give each operation explicit
targets, permissions, resource ownership, preconditions and recovery behavior.

**Why deferred:** Baseline index provisioning, complete rebuild/replay, deployment, cutover and
rollback remain covered by M08–M15/C05/C07/C08. This entry covers only additional reusable
automation; it does not defer those capabilities or make destructive actions a feature by
themselves.

**Reconsider when:** Repeated maintenance or release operations expose a concrete automation gap
after the required migration procedures work.

**Acceptance evidence:** Disposable tests prove target guards, a non-mutating preview, least
privilege, interruption/partial-failure recovery and preservation of unrelated indices/groups. Any
deletion or offset change requires a reviewed recovery/retention contract. Live execution still
needs its own scope and authorization; an optional tool or ledger entry grants neither.

#### issue-215-N13 Java policy-search retirement

**Disposition:** Scheduled. On 2026-10-30 the user explicitly included Java policy-search
retirement in issue 215. The parent specification promotes this item to mandatory M17 and child
[#236](https://github.com/igor-baiborodine/insurance-hub/issues/236) (C09), after separately gated
cutover child [#235](https://github.com/igor-baiborodine/insurance-hub/issues/235) (C08). No
retirement has been executed or verified.
This entry retains its original ID as promotion history and is no longer deferred optional work.

**Requirement:** Retire the legacy policy-search workload and obsolete service-specific
configuration only after Go cutover and the accepted rollback window close. Inventory remaining
callers, consumer groups, routes, secrets and retained index/history requirements; distinguish
stopping a workload from destructive resource/data cleanup.

**Why originally deferred:** Java remains a rollback option during qualification and cutover;
accepting C08 alone did not select retirement. The user's subsequent instruction explicitly
includes retirement within issue 215.

**Execution gate:** Go meets the agreed cutover observation/rollback criteria, the rollback
window closes, and the service/release owner verifies the retirement resource inventory and
recovery/retention responsibilities. Scope selection does not authorize live deletion or cleanup.

**Acceptance evidence:** Fresh inventory proves no required Java callers or active legacy consumers
remain. Go-only query/event operation and restart/recovery pass; retention/archive/restore
responsibilities and the final recovery path are accepted. Remove only approved resources,
preserving shared topics, indices, secrets and other services' dependencies.

## Ad hoc repository improvements

### Commit-message policy enforcement

Origin: an ad hoc repository-maintenance review of the commit-message rules and existing
`.githooks` installation mechanism on 2026-10-19. The repository currently relies on authors and
reviewers to validate commit messages manually.

| Ledger ID | Improvement | Status | Follow-up ticket or disposition |
| --- | --- | --- | --- |
| [adhoc-commit-msg-N01](#adhoc-commit-msg-n01-commit-message-hook) | Commit-message hook | Deferred | Unassigned |

#### adhoc-commit-msg-N01 Commit-message hook

**Requirement:** Add a checked-in `.githooks/commit-msg` hook that validates the mechanically
enforceable parts of the repository's commit-message policy: the eight allowed types, optional
lowercase scope, breaking-change marker placement, optional literal `[issue-<number>]` reference,
required spacing, a non-empty subject, and omission of a final period. Require a breaking-change
body or `BREAKING CHANGE:` footer when the subject uses `!`. Define explicit handling for
Git-generated merge and revert messages and for `fixup!` and `squash!` autosquash messages. Add a
focused disposable-repository test harness, an owning Make target, and synchronized guidance in
the Git rules and `CONTRIBUTING.md`. Keep imperative mood, identifier casing, scope quality, commit
coherence, explanation adequacy, and issue-closing semantics under human review.

**Why deferred:** The current written policy and review process remain usable, and the existing
hook installation mechanism requires no immediate migration. Implementing the hook still needs an
agreed exception policy for Git-generated and temporary commits plus tests that prevent valid Git
workflows from being blocked.

**Reconsider when:** Invalid commit messages repeatedly reach review, automated release or audit
tooling begins to depend on parsed commit subjects, or the repository agrees on merge, revert, and
autosquash behavior.

**Acceptance evidence:** Disposable Git-repository tests prove every documented valid subject and
agreed exceptional message is accepted, malformed types, scopes, markers, issue references,
spacing, empty subjects, final periods, and incomplete breaking changes are rejected, and hook
failures leave the commit message available for correction. The documented installation path
activates both existing and new repository hooks without replacing another configured hooks path;
the Make target runs the focused suite; the Git rules and contributor guidance match the enforced
behavior; and the rules reserved for human review remain clearly identified.
