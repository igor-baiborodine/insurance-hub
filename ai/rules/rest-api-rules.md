# REST API Rules

These rules apply when changing HTTP APIs.

- Treat endpoint paths, methods, status codes, request bodies, response bodies, and validation errors as contract surface.
- Preserve backward compatibility unless the spec explicitly allows a breaking change.
- Keep validation behavior deterministic and testable.
- Return errors in the existing module's established shape.
- Update API documentation, generated clients, or contract modules when the API surface changes.
- Include negative-path tests for validation and authorization-sensitive behavior when applicable.
