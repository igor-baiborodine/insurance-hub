<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Phase 4 nice-to-have requirements ledger](#phase-4-nice-to-have-requirements-ledger)
  - [Maintaining the ledger](#maintaining-the-ledger)
  - [Sub-epic 4.2 Product catalog read pilot in Go](#sub-epic-42-product-catalog-read-pilot-in-go)
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
