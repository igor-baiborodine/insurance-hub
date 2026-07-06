# Java Rules

These rules are intentionally minimal until service-specific Java conventions are formalized.

- Follow the existing style of the target module.
- Keep API contracts and service implementation boundaries intact.
- Prefer constructor injection and established Micronaut patterns already present in the module.
- Keep DTO validation explicit and close to the contract boundary.
- Add or update tests near the changed production code when behavior changes.
- Do not introduce broad framework upgrades or dependency changes as part of feature work unless the ticket requires them.
