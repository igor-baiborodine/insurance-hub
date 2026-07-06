# Insurance Hub Domain Notes

Insurance Hub models insurance platform capabilities across policy, product, pricing, payment, document, dashboard, authentication, gateway, and related services.

Use these notes as lightweight context only. Ticket specs and service code remain the source of truth for detailed behavior.

## Current Shape

- Legacy services are primarily Java/Micronaut modules under `legacy/`.
- API contract modules are separate from service implementation modules where the legacy structure provides them.
- Deployment and operations assets live under `k8s/` and related documentation folders.
- Migration and local development documentation lives under `docs/`.

## Working Guidance

- Preserve service boundaries unless a spec explicitly changes them.
- Treat API contracts, DTOs, validation, persistence behavior, and security behavior as first-class requirements.
- When modernizing or migrating behavior, capture current behavior before proposing a replacement.
- Prefer small, testable increments over broad rewrites.
