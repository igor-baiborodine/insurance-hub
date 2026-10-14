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
- Use stable IDs in the form `issue-<number>-N<nn>`. Preserve IDs from the source requirements;
  assign the next unused number to new entries and never reuse a retired ID.
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
