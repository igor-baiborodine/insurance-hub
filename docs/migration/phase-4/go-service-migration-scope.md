<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Go-service migration scope and requirements](#go-service-migration-scope-and-requirements)
  - [Delivery principles](#delivery-principles)
  - [Minimum compatibility discovery](#minimum-compatibility-discovery)
  - [Standard scope](#standard-scope)
  - [Suggested MVP sequence](#suggested-mvp-sequence)
  - [Validation during implementation](#validation-during-implementation)
  - [Final implementation testing and rollback](#final-implementation-testing-and-rollback)
  - [Default exclusions and completion criteria](#default-exclusions-and-completion-criteria)
  - [Repository references](#repository-references)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Go-service migration scope and requirements

Use this guide to define the base scope of a Java-to-Go service migration in Insurance Hub.
The goal is a working Go replacement for the service's required behavior, delivered through small,
deployed MVP increments. Reuse the repository's established runtime, build, release, deployment
and monitoring capabilities.

The [Phase 4 implementation review](phase-4-implementation-review.md) provides technical context
and service-specific concerns. This guide defines the delivery approach for new migration scopes:
bounded compatibility discovery, iterative implementation and final integrated qualification.
Historical pilot research and validation programs are examples, not a checklist to repeat for
every service. Existing accepted evidence remains useful; document any scope change explicitly
when applying this approach to work already specified.

## Delivery principles

1. **Use an MVP iterative approach.** Select the smallest useful capability, implement it end to
   end, deploy it and demonstrate it before adding dependent capabilities. A complete service
   emerges from accepted increments; do not wait for the full rewrite to test deployment.
2. **Minimize legacy Java research.** Inspect only what is needed to define the next capability's
   observable contract, data access, permissions and side effects. Reuse existing documentation,
   fixtures and observations. Do not create an exhaustive baseline project by default.
3. **Defer deployment rollback/restore testing to final implementation testing.** Intermediate
   increments retain a documented stop/return path and compatible release information, but do not
   require deployment downgrade, removal/reinstallation or restoration rehearsals. Perform those
   tests against the completed migration candidate before live ownership transfer.
4. **Preserve behavior without expanding the product.** Go implementation quality does not imply
   new APIs, normalized responses, stronger event guarantees or a new recovery platform. Record
   deliberate behavioral changes separately before implementing them.
5. **Deliver safeguards with the capability that needs them.** Configuration, access control,
   bounded lifecycle, safe diagnostics and focused tests accompany the first affected increment.
   MVP means a smaller functional scope, not postponing these essentials.
6. **Reuse and verify.** Adapt published shared runtime APIs and existing owner interfaces.
   Validate the selected service; another service's successful deployment or tests do not prove
   its registration, behavior or operational readiness.

## Minimum compatibility discovery

Keep one short compatibility section in the service specification and a small set of reusable
synthetic fixtures. These are implementation inputs, not a separate research deliverable.

| Question | Minimum useful evidence |
| --- | --- |
| What does the next capability do? | Caller/entry point, representative input, expected output and a few meaningful failure outcomes. Inspect the relevant handler, API model and application path. |
| What is observable at the boundary? | Required fields, defaults, omitted/null/empty behavior, status/error mapping, ordering, dates and decimal rules where they affect consumers. A focused fixture is preferable to an extensive narrative. |
| What state does it use or change? | Actual tables, indexes, objects or events used by that capability; keys, necessary mappings, writer and initialization owners. Identify existing data that must remain usable. |
| What authority does it need? | Existing caller authentication/permissions, internal exposure, required credentials and least-privilege dependency access. |
| What continues running alongside it? | Relevant Java routes, consumers, schedulers, startup initialization and other writers. Identify how the Go increment avoids competing side effects. |

Start with existing source, contracts, tests and accepted fixtures. Run a narrow Java observation
only when an unresolved fact would change the Go implementation or expected result. Capture that
answer once and reuse it. Do not rebuild the entire Java environment merely to reconfirm documented
behavior. Do not derive the expected result from the new Go implementation itself.

Stop discovery once the next increment has testable expected outcomes and its state/access
boundaries are understood. Resolve remaining facts just before the capability that needs them;
an unrelated legacy uncertainty should not block an isolated bootstrap or read increment.
Unknown behavior affecting permissions, data preservation, money, dates or event ownership must
be resolved before dependent code or activation. This requires focused evidence, not a broad audit.

Java/Go performance comparison is **not a standard migration requirement**. Do not build a Java
benchmark harness, statistical comparison framework or repeated dual-stack load program by
default. Use a targeted comparison only when an explicit requirement or a concrete regression
question makes it necessary, and define its small scope and stopping condition in advance.

## Standard scope

Include the following requirements where applicable to the service. Record a short reason for an
inapplicable capability instead of building a placeholder subsystem.

| Area | Base requirement |
| --- | --- |
| Service module and architecture | Own a standalone module under `services/`, with thin entry points, explicit construction, application-owned ports and separate transport/dependency mappings. Start from the maintained scaffold and compatible published Servicekit APIs; do not import another service's private implementation. |
| Contracts and transport | Preserve required caller-visible behavior. Use versioned unary gRPC with generated gRPC-Gateway for migrated HTTP/JSON APIs. Implement legacy representation and error mapping at the gateway boundary, with matching OpenAPI and compatibility tests; do not replace the generated path with independent HTTP business handlers. The generated client must call the actual gRPC listener and middleware. Worker-only, WebSocket or binary-document behavior retains its specifically defined boundary. |
| Business behavior | Implement the selected use cases and essential failure handling. Preserve validation, serialization, dates, decimal precision, ordering and access outcomes that callers rely on. Remove temporary scaffold business endpoints when real capabilities replace them. |
| Persistence and dependencies | Use only the dependencies the service needs. Preserve existing readable data and formats, use bounded operations and cancellation, and keep administrative credentials separate from runtime access. No automatic schema rewrite or forced data reload follows from changing language. |
| Events and scheduled work | Preserve the agreed payload, key, processing, commit and restart semantics. Make worker enablement explicit; prevent competing authoritative writers during coexistence. Do not silently add stronger ordering, exactly-once, replay or repair guarantees. |
| Initialization ownership | Identify Java-owned schema creation, seed loading, indexes and scheduled setup. Provide the minimum necessary replacement before retiring that owner. Reuse existing populated state; support fresh setup when required. Keep administration explicit and outside ordinary request-serving startup. |
| Configuration and lifecycle | Typed validated startup settings, clear redacted errors, meaningful readiness/liveness, bounded I/O, cancellation, partial-start cleanup and graceful shutdown. Disabled workers must not acquire authority or write. |
| Security and privacy | Preserve the agreed authentication/authorization boundary, private backend exposure, managed secrets, least privilege and verified TLS where configured. Exclude credentials and sensitive payloads from logs, labels and diagnostics. |
| Build and release | Register module, consumers, image and deployment through existing catalogs and owning Make/CI interfaces. Keep dependency/tool pins and generated contracts reproducible. Record source revision, immutable image and configuration identity for deployed increments. |
| Deployment and coexistence | Deploy independently through the existing local-dev/QA owners. Use distinct identities/selectors and isolated data or read-only access as needed. A Pod with no HTTP traffic can still consume events or run jobs; control those effects explicitly. |
| Observability | Add safe structured logs, bounded metrics and trace correlation through the configured Alloy paths with each capability. Provide the [standard dashboards and alerts](go-service-dashboards-and-alerts.md), with domain-specific additions, before final qualification and cutover. |
| Validation and operations | Focused automated checks per increment, cumulative final qualification, an executable operating procedure, final rollback/restore evidence, separately controlled ownership transfer and bounded Java retirement. |

Use domain judgment to extend this scope. Search may need exact query/projection semantics;
pricing needs decimal and rounding correctness; documents need rendering and storage completion;
payment needs file/import ownership; realtime services need connection and drain behavior. Add
only requirements that support the service's actual behavior or an explicitly agreed change.

## Suggested MVP sequence

Choose increments around observable outcomes rather than separate infrastructure, security and
telemetry workstreams that defer a usable service until the end. The sequence below is a starting
point; omit stages the service does not need and combine small stages when that reduces overhead.

| Stage | Deliverable | Acceptance at this stage |
| --- | --- | --- |
| 1. Smallest deployable service | Renamed scaffold, service identity, health, configuration, release/deployment registration and basic signals. Prefer a tiny real capability; use a private synthetic endpoint only when needed to prove a new deployment path. | Owning checks pass; the exact image deploys and responds through its intended private interface. Health, safe configuration, reachability and enabled signals work. No broad Java baseline or business dependency is required for a synthetic bootstrap. |
| 2. First business capability | A useful read/query or other bounded end-to-end operation through its real dependency. | Required success and failure behavior passes; the deployed operation works; runtime/security/telemetry protections cover the new path. |
| 3. Remaining essential capabilities | Add writes, consumers, scheduled work or other domain operations in small slices, with isolated authority until handover. | Each capability works in deployment, prior behavior still passes affected regression checks, and new side effects are controlled. |
| 4. Complete monitoring and operational instructions | Service/domain dashboards, actionable alerts and concise commands for operation, observation and recovery. Build these progressively; finish missing coverage here. | Queries and rules work against service data, thresholds have workload rationale, and operators can identify failures in the completed service. |
| 5. Final implementation qualification | The complete migration candidate, including required capabilities, safeguards and monitoring. | Integrated behavior and practical load checks pass; deployment rollback/restore and applicable ownership-transfer rehearsal pass against the selected release and target. |
| 6. Ownership transfer | Switch the actual caller route and any data, consumer or scheduler authority through the owning mechanism. | Observe the agreed health/business/monitoring criteria and preserve current data and progress. Use the qualified return path if abort conditions occur. |
| 7. Legacy retirement | Remove the specific obsolete Java workload/resources after the observation/rollback window. | No required caller or initialization/job owner remains on Java; Go operates with retained data and shared dependencies. |

For repository migrations, validate local-dev and QA in the agreed sequence using existing owners.
When only one environment fits on the development machine, run them sequentially and record the
active target after each switch. Do not require Java and Go environments to run concurrently.

Advance from the accepted increment rather than rebuilding the service tree for the next stage.
Keep a compact record of enabled capabilities, deployed identity, checks and remaining limitations.
Reuse still-applicable evidence; rerun affected checks when code, configuration or the target
changes. Do not require every earlier validation suite after a documentation-only update.

## Validation during implementation

Each increment uses the module's owning Make targets and the repository's applicable checks.
Run focused unit/contract checks, real dependency integration where needed, and a deployed smoke
of the new capability. Exercise meaningful input/dependency failures, access boundaries,
redaction and lifecycle behavior as those paths are introduced. Preserve independently expected
results and include affected existing capabilities in regression checks.

Before accepting the completed implementation, run all applicable owning module/CI gates,
including build, format/lint, unit/integration, race, contract-generation and vulnerability checks
as selected by the repository's validation rules. A small operational qualification workload does
not replace these implementation checks.

Do not defer ordinary process cleanup, cancellation, startup failure or worker-disable tests.
These establish whether the increment runs correctly. **Deployment rollback/restore testing is
different and is deferred to final implementation qualification**, including downgrade/return,
removal and redeployment exercises. Do not add such rehearsals as acceptance gates for every MVP.

During intermediate deployment, retain the accepted image/configuration identity, a concise
stop/return procedure and compatible data boundaries. Prefer additive changes and isolated
effects so a failed experiment cannot damage the authoritative service. If an intermediate
increment would require irreversible shared-state mutation, redesign its isolation or defer that
mutation to the final transition instead of adding an early rollback-testing project.

## Final implementation testing and rollback

Use the completed release candidate, its actual configuration and the intended owning deployment
mechanisms. Final implementation testing occurs before normal traffic or authoritative work is
transferred to Go; it is not deferred until after Java retirement.

Keep the final test set small and outcome-driven:

1. **Integrated behavior:** Exercise representative user/business flows through real endpoints
   and dependencies, including essential failure cases. Check the unchanged gateway/browser or
   other caller when it is part of the affected contract. Reuse accepted fixtures instead of
   repeating broad Java discovery.
2. **Practical Go performance:** Run one bounded representative workload with agreed latency,
   error, throughput and resource limits suitable for intended use. Check response content and
   useful work as well as successful status codes. Record limits and environmental uncertainty;
   do not require proof that Go is faster than Java or claim unmeasured production capacity.
3. **Operational readiness:** Confirm access, health, lifecycle and actual log/metric/trace
   delivery, plus usable dashboards and alert behavior. Reuse applicable increment evidence
   rather than repeating a full platform qualification program.
4. **Deployment rollback and restore:** From the completed candidate, return to an identified
   compatible previous deployment/configuration through the owning mechanism, verify essential
   behavior, then restore the same candidate and verify it again. For a first deployment with no
   Go predecessor, use the specified isolated removal/redeployment and retained Java ownership
   path. Check actual source/image/configuration identity and reconciliation, not just commands
   returning success.
5. **Stateful handover, when applicable:** Rehearse the intended route/writer/consumer/scheduler
   ownership transition and reverse path on a controlled target with representative existing
   state. Verify data and progress are preserved without migration-induced skips or competing
   writers. A Go image downgrade alone does not demonstrate safe ownership transfer.

The release owner records the target, prerequisites, pass/abort conditions, observation period,
restoration procedure and evidence. Use existing deploy/verify interfaces and bounded synthetic
workloads. Execute live-target tests only within their explicit operation authorization.

Here, **restore** means returning the tested deployment and its configuration to the accepted
candidate while preserving compatible state. It does not automatically require disaster recovery,
backup restoration, historical data reconstruction or a generic repair system. If the agreed
migration changes persisted formats or has a concrete data-recovery need, specify that bounded
work explicitly and validate it as part of final qualification before the live transition.

Rerun final tests only when intervening changes invalidate their evidence. Failed or unavailable
required checks remain visible gaps; do not silently turn them into passes or expand the scope
into an open-ended benchmark/research program.

## Default exclusions and completion criteria

Keep the following outside the base migration unless separately justified and agreed:

- Exhaustive Java source analysis, full baseline dossiers, repeated legacy environment rebuilds
  and broad Java-versus-Go performance benchmarking.
- Deployment rollback/restore rehearsals for each intermediate MVP.
- New business features, response normalization, stricter domain validation or fixes to unrelated
  historical data and behavior.
- New replay, reconciliation, backfill, quarantine, outbox or exactly-once machinery merely to
  make the Go service more capable than the existing contract.
- Platform upgrades, gateway/auth replacement, service mesh adoption, broad shared-tool refactors,
  fleet dashboards, production paging programs and speculative autoscaling/capacity work.
- Broad Java source deletion or shared infrastructure retirement beyond the specific service's
  accepted operational removal.

A migration is complete when the agreed essential capabilities run in Go, necessary safeguards
and monitoring work, the final qualification and rollback/restore tests pass, ownership is
transferred and observed, and the scoped Java retirement is accepted. If the requested scope ends
at a deployed pilot, name that narrower completion boundary explicitly. A scaffold, deployed Pod
or successful API response alone does not establish full migration completion.

For each increment, record just the objective, included/excluded behavior, missing prerequisite
facts, observable acceptance cases and owning checks. Keep the final qualification, cutover and
retirement outcomes distinct so implementation progress is not confused with live ownership.

## Repository references

- [Phase 4 implementation review](phase-4-implementation-review.md): technical tradeoffs and
  domain-specific concerns; select those relevant to the capability being migrated.
- [Scaffold copying procedure](../../../templates/go-service/COPYING.md) and
  [scaffold runtime reference](../../../templates/go-service/README.md): service bootstrap and reuse.
- [Module onboarding](go-module-topology.md) and
  [contributor validation guidance](../../../CONTRIBUTING.md): owning inventory and checks.
- [Standard dashboards and alerts](go-service-dashboards-and-alerts.md): common monitoring and
  domain-specific additions.
- [Product operations](product-service/README.md) and
  [Policy Search deployment runbook](policy-search-service/deployment-runbook.md): concrete
  implementation examples to adapt, not mandatory research or rehearsal programs to copy wholesale.
