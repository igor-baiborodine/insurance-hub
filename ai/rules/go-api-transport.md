<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Go API and Transport Contracts](#go-api-and-transport-contracts)
  - [Transport design and ownership](#transport-design-and-ownership)
  - [Contract sources and OpenAPI](#contract-sources-and-openapi)
  - [HTTP behavior and representation mapping](#http-behavior-and-representation-mapping)
  - [Error mapping](#error-mapping)
  - [Request propagation and security](#request-propagation-and-security)
  - [Verification and adoption](#verification-and-adoption)
  - [Repository reference and limits](#repository-reference-and-limits)
  - [Sources](#sources)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Go API and Transport Contracts

Apply when designing, implementing, changing, or reviewing Go HTTP APIs, gRPC services, HTTP
facades, and OpenAPI descriptions. Use [Go architecture](go-architecture.md) for dependency and
composition boundaries, [Go development](go-development.md) for lifecycle and the mandatory
Makefile interface, [Go testing](go-test.md) for test design, and
[Go validation](go-validation.md) for execution and evidence.

## Transport design and ownership

- State the actual request path in the service README and ticket: HTTP and gRPC adapters calling
  shared application use cases, or an HTTP gateway calling a gRPC endpoint. Choose according to
  contract, deployment, and security requirements. Do not introduce a loopback RPC solely to reuse
  business behavior, or mandate a gateway framework for every service.
- Keep business decisions in application/domain code. Adapters own protocol validation, DTOs,
  serialization, and error translation. A gateway owns HTTP-to-RPC translation; it must not access
  another service's database or private packages to bypass its public contract.
- Document which HTTP operations expose which RPCs or use cases. Expose only approved operations;
  generating a gRPC service must not automatically publish every RPC over HTTP. Handle streaming
  only with an explicit HTTP framing, cancellation, backpressure, and error contract.
- Inspect the actual execution path for middleware and interceptors. In-process handler
  registration and shared-use-case calls must not be assumed to execute gRPC server interceptors;
  validation, authorization, limits, and telemetry must cover each reachable boundary.

## Contract sources and OpenAPI

- Use versioned protobuf contracts for internal gRPC. For every HTTP API created or materially
  changed, identify its authoritative HTTP contract and maintain an OpenAPI description matching
  the delivered behavior. During migration, the approved legacy baseline governs parity; do not
  silently replace it with a generator's default mapping.
- Choose and document the contract workflow: protobuf plus explicit HTTP annotations/configuration
  generating gateway/OpenAPI output, or a separately maintained HTTP contract with explicit
  DTO-to-application/RPC mappings. Identify editable sources and generated outputs. Avoid two
  independently editable definitions of the same contract without a consistency check.
- Describe methods, paths, path/query/header parameters, request bodies, media types, response
  bodies/statuses/headers, and operation security in OpenAPI. Include relevant error responses and
  examples; distinguish required fields, nullable values, and omitted fields. Document the external
  path prefix and deployment-specific server configuration without embedding secrets.
- Pin generators, options, and the OpenAPI version supported by the adopted toolchain in checked-in
  configuration. Do not assume a generator emits OpenAPI 3.x; review any conversion step for lost
  semantics. Generate, lint, and check drift through owning Make targets. Never hand-edit generated
  clients, gateway code, or OpenAPI files.
- Preserve protobuf field numbers and presence semantics; reserve removed field names and numbers.
  Review HTTP/JSON compatibility independently of protobuf binary compatibility. Update affected
  callers, mocks, generated artifacts, and compatibility tests together.

## HTTP behavior and representation mapping

- Define HTTP methods, resource paths, status codes, content types, and headers explicitly. Keep
  safe read operations free of business mutations. Specify create/update/delete semantics, partial
  updates, idempotency, and concurrency preconditions when applicable; do not add these features
  to a migration without an agreed contract change.
- Specify path decoding and normalization, case sensitivity, query defaults and duplicate values,
  body size limits, unknown-field handling, malformed requests, and unsupported methods/media
  types. Define pagination limits, stable ordering, filtering, and empty results where applicable.
  Preserve approved legacy behavior rather than introducing new defaults for convenience.
- Record and test these representation decisions at each boundary:

| Concern | Required contract decision |
| --- | --- |
| Presence and collections | Absent versus null versus explicit zero/false; omitted versus empty arrays/maps; response envelope versus a bare collection. |
| Numbers | Decimal precision/scale, large integer safety, and JSON number versus string. Do not round-trip money through floating point. |
| Names and variants | JSON field names, enum values, unknown values, and oneof/discriminator mappings. |
| Time and binary data | Date-only versus timestamp semantics, timezone/precision, and byte encoding. |

- When ProtoJSON is the chosen HTTP representation, use protobuf-aware serialization and explicit
  options. ProtoJSON has its own field naming, integer, enum, and presence behavior; ordinary Go
  struct JSON is not a substitute. When the HTTP contract differs, use deliberate transport DTOs
  and mappings rather than leaking protobuf messages into the business core.
- Validate protocol shape at ingress and business invariants in the core. Keep equivalent business
  behavior consistent across transports while allowing documented differences in their wire
  representations and transport-specific failures.

## Error mapping

- Maintain an operation-relevant mapping from application failures to gRPC statuses and HTTP
  status/body/headers. For an RPC gateway, explicitly map upstream statuses and approved details
  into the HTTP contract; inspect library defaults rather than adopting them implicitly.
- Cover invalid input, missing resources, conflicts, authentication/authorization failures,
  dependency failures, cancellation, deadlines, and unexpected failures as applicable. Specify
  routing, decoding, and middleware errors too; these can occur before a use case or RPC runs.
- Return safe, stable error bodies. Do not expose SQL, stack traces, credentials, raw upstream
  messages, or unapproved status details. Preserve inspectable causes internally and correlate
  failures through telemetry without logging sensitive payloads.
- Preserve accepted legacy mappings even when they differ from a gateway default. Record client
  cancellation distinctly from server/dependency timeout; a disconnected client may receive no
  response. Do not promise a universal HTTP equivalent for every gRPC status.

## Request propagation and security

- Propagate request context and cancellation through adapters, application calls, and downstream
  RPCs. Bound downstream calls by the caller's remaining deadline and service limits; never reset
  a shorter caller deadline to a fresh full timeout. Budget time for response mapping and cleanup.
- Define retry ownership and eligible failures. Retry only operations with established replay
  safety and within the remaining deadline; coordinate gateway and client retry policies. Do not
  automatically replay writes without an explicit deduplication/idempotency contract.
- Define an allowlist for forwarded headers/metadata and returned headers/trailers, including
  credential and identity handling. Propagate tracing through the adopted instrumentation; do not
  blindly trust client-supplied identity, forwarding, or internal metadata headers.
- State authentication and authorization enforcement points for both HTTP and direct gRPC access.
  Direct access must not bypass the specified policy. Document TLS/trust boundaries and any
  deliberately anonymous internal endpoints; do not generalize Product's temporary access policy.
  OpenAPI security declarations document requirements and do not implement them.
- Bound payload sizes and concurrency where required, including the gateway's downstream client.
  Own connection reuse and shutdown in composition. Configure browser CORS/CSRF protections when
  applicable to the chosen exposure and credential model; they do not replace authorization.

## Verification and adoption

- For affected APIs, use owning Make targets to validate protobuf and OpenAPI sources, regenerate
  configured outputs, detect drift (including missing/new artifacts), and check compatibility
  against the documented baseline. Lint and schema checks alone do not prove HTTP behavior.
- Test mappers with independent expected values, including presence, precision, empty results,
  variants, and errors. Exercise real HTTP and gRPC boundaries for status/body/header behavior,
  malformed inputs, middleware/interceptors, access policy, and cancellation/deadline propagation.
- For a gateway that calls gRPC, test the complete HTTP-to-gRPC path, including upstream failures
  and metadata forwarding. Separate adapter tests cannot prove that path. For shared-use-case
  adapters, verify both transports against the same business scenarios and their distinct wire
  expectations. Use approved legacy fixtures for migrations.
- Apply these rules to new and materially changed APIs; keep broad retrofits in separate work.
  Report missing contract artifacts or Make capabilities as gaps, with the affected check and
  follow-up, rather than claiming automated coverage. Follow the development rule's
  [missing-target procedure](go-development.md#scope-and-makefile-interface).

## Repository reference and limits

[Product's runtime contracts](../../services/product-service/README.md#runtime-contracts-and-boundaries) and
[architecture](../../services/product-service/README.md#product-catalog-architecture) illustrate
direct HTTP and gRPC adapters sharing application use cases. Its
[HTTP handler](../../services/product-service/internal/http/handler.go) preserves a legacy HTTP
contract independently of the [protobuf schema](../../services/product-service/api/product/v1/product_service.proto).
Its [generator configuration](../../services/product-service/buf.gen.yaml) currently generates Go
protobuf and gRPC code, not an HTTP gateway or OpenAPI description. This is a reference for explicit
transport separation, not evidence that OpenAPI/gateway generation or validation already exists.

## Sources

- [ProtoJSON specification](https://protobuf.dev/programming-guides/json/): representations, presence, and JSON compatibility.
- [gRPC deadlines](https://grpc.io/docs/guides/deadlines/): bounded calls and deadline propagation.
- [gRPC-Gateway customization](https://grpc-ecosystem.github.io/grpc-gateway/docs/mapping/customizing_your_gateway/): serialization, metadata, and error mapping when that framework is adopted.
- [OpenAPI specification](https://spec.openapis.org/oas/v3.1.1.html): HTTP operation, schema, response, and security descriptions; use the version selected by the service toolchain.
