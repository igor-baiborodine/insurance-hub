# Safety Rules

- Never silently change security, authentication, authorization, or data-retention behavior.
- Never silently change API contracts, event payloads, database schemas, or deployment behavior.
- Surface ambiguous or conflicting requirements before implementation.
- Treat customer, payment, policy, and authentication data paths as sensitive.
- Do not introduce new dependencies without a concrete reason and validation path.
- Do not remove tests, validation, or operational checks to make implementation easier.
- Document residual risk when validation cannot fully prove the change.
