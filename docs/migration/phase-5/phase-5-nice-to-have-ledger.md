<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Phase 5 nice-to-have requirements ledger](#phase-5-nice-to-have-requirements-ledger)
  - [Maintaining the ledger](#maintaining-the-ledger)
  - [Policy service](#policy-service)
    - [phase-5-N01 Policy command idempotency](#phase-5-n01-policy-command-idempotency)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Phase 5 nice-to-have requirements ledger

This ledger tracks optional improvements identified for Phase 5. It preserves each requirement,
the reason for deferral, the trigger for reconsidering it, and the evidence required if it is
selected later.

An entry does not add work to an accepted ticket scope or block its completion. Selecting an entry
for delivery requires an owning ticket with agreed scope and acceptance criteria. Mandatory
requirements and unresolved completion blockers belong in that ticket, not in this ledger.

## Maintaining the ledger

- Use stable IDs in the form `phase-5-N<nn>` for phase-level entries. Preserve IDs from source
  requirements, assign the next unused number, and never reuse a retired ID.
- For each entry, record the requirement, deferral rationale, reconsideration trigger, and
  acceptance evidence. Treat triggers as planning guidance, not automatic commitments.
- Maintain status and follow-up links in the index. When scheduling work, link the receiving ticket
  and record the decision date. When closing an entry, record the date and delivery evidence or the
  reason it was dropped. Retain the entry for traceability.
- If another source identifies the same improvement, reference the existing entry and add that
  source rather than creating a duplicate.

| Status | Meaning |
| --- | --- |
| Deferred | Retained for future consideration; no delivery commitment. |
| Scheduled | Included in an identified ticket's accepted delivery scope. |
| Delivered | Acceptance evidence is linked and the selected work is complete. |
| Dropped | Deliberately declined or superseded; the reason or replacement is recorded. |

## Policy service

Origin: issue 442 review follow-up, added after Policy lifecycle PR1 confirmed that the existing
Go API intentionally creates a new result for every successful mutation invocation. Phase 5 will
replace the Java Agent Portal Gateway with Envoy and introduce Linkerd, so command identity and
retry ownership must be agreed before either layer may replay Policy writes.

Related sources: [issue 442](https://github.com/igor-baiborodine/insurance-hub/issues/442),
[Phase 5 architecture](../../system-overview-and-migration-analysis.md#phase-5-modernize-edge-and-authentication-add-service-mesh),
and the Phase 4 termination-specific entries
[`issue-439-N01`](../phase-4/phase-4-nice-to-have-ledger.md#issue-439-n01-repeat-safe-termination-commands)
and
[`issue-439-N02`](../phase-4/phase-4-nice-to-have-ledger.md#issue-439-n02-concurrent-termination-protection).

| Ledger ID | Improvement | Status | Follow-up ticket or disposition |
| --- | --- | --- | --- |
| [phase-5-N01](#phase-5-n01-policy-command-idempotency) | Policy command idempotency | Deferred | Unassigned; select before enabling retries that can replay Policy mutations |

### phase-5-N01 Policy command idempotency

**Requirement:** Define request-level idempotency for the mutable Policy operations `POST /offers`,
`POST /policies`, and `POST /policies/terminate` before Envoy, Linkerd, or another caller may retry
them after an ambiguous outcome. Add an opaque caller-generated command identity to the versioned
Go contract and propagate it unchanged across the trusted edge and mesh boundaries. In the owning
Policy transaction, persist the operation, command identity, a deterministic fingerprint of the
validated request, the command result, and its stable logical outbox intent.

An identical identity and payload must return the original successful result without another
offer, policy, version, cover, or logical event intent. Reusing an identity with different input
must fail with an approved typed conflict and no additional writes. Concurrent copies must
converge on the same result. Different command identities must continue to represent intentional
distinct commands, including two valid issuances from one offer. Termination must incorporate the
approved repeat and concurrency outcomes from `issue-439-N01` and `issue-439-N02` rather than
introducing a second termination contract. `GET /policies/{policyNumber}` remains a read-only,
side-effect-idempotent lookup and does not require a command record. Physical Kafka redelivery of
one stable intent remains governed by the accepted at-least-once publication contract.

**Why deferred:** Issue 442 explicitly excludes public command idempotency and delivers the
private lifecycle without a command identity. The Phase 5 Envoy routes, Linkerd retry policy,
trusted identity propagation, retention period, conflict response, and database ownership for
deduplication have not yet been accepted. Adding only an issuance guard now would leave offer and
termination retries inconsistent and would pre-empt the termination-specific decisions already
retained in the Phase 4 ledger.

**Reconsider when:** Phase 5 defines the Envoy-to-Go contract and Linkerd retry ownership, before
any retry policy can replay a Policy mutation, before the Java Agent Portal Gateway is removed, or
when an ambiguous response or concurrent caller produces a duplicate Policy effect.

**Acceptance evidence:** A focused disposable PostgreSQL/outbox smoke exercises each mutable
operation's first command, a lost-response replay, concurrent identical copies, and command-identity
reuse with different input. It proves one accepted result and one logical effect for identical
copies, an approved conflict with no new writes for mismatched input, and distinct results for
intentional commands with different identities. The termination cases also prove the selected
repeat and concurrent outcomes from the Phase 4 entries. A repeated lookup produces no writes.
Focused HTTP and gRPC checks verify identity propagation and sanitized conflicts, while an isolated
Envoy/Linkerd route proves that retry configuration cannot replay a mutation unless the
idempotency contract is active. Evidence compares aggregate, version, cover, command, and outbox
counts without retaining request payloads, command identities, or personal data.
