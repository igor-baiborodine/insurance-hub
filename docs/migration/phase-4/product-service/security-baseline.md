# Product security baseline

This document records the observed Java identity and access model for issue #131. The accepted live
fixtures are `access/local-dev.json` and `access/qa.json`; `access/cases.json` records the controlled
isolated matrix. They contain response bodies and claim names where useful, but no bearer token,
signing material, credential, or environment-specific claim value.

## Current trust and credential ownership

The Auth service authenticates a stored agent login and produces a bearer JWT. Its custom claims
generator adds `avatar`; framework generation supplies `sub`, product-code `roles`, `iss`, `iat`,
`nbf`, `exp`, and, when configured, `jti`. The configured access-token lifetime is 86,400 seconds.
The roles describe an agent's available product codes, but the Product gateway routes do not use
them for authorization.

Auth and the agent portal gateway use the same checked-in symmetric signing-secret configuration.
The effective algorithm is HMAC SHA-256. Neither local-dev nor QA deployment configuration injects
separate JWT trust material, so the application image/configuration owns the issuer and verifier
material in both environments. There is no checked-in rotation, revocation, or separate lifecycle
mechanism. This is a baseline observation, not an endorsement of that credential design.

The gateway marks both `GET /api/products` and `GET /api/products/{productCode}` as authenticated.
Micronaut verifies a signed token, requires a subject, and rejects an expired token. The observed
configuration has no issuer or audience allow-list and no not-before validator. Product itself has
no Micronaut security dependency, security annotation, or request filter.

## Observed access matrix

The isolated suite uses runtime-only HMAC material and a loopback Product listener. The live subset
uses a short-lived Auth token plus derived malformed and invalid-signature inputs. Live captures are
read-only and verify catalog identity before and after the requests.

| Boundary and route | Valid | Absent | Malformed | Invalid signature | Expired | Missing `sub` | Future `nbf` | Different issuer | Different audience | Unrelated/product-mismatched role |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| Gateway list | `200` | `401` | `401` | `401` | `401` | `401` | `200` | `200` | `200` | `200` |
| Gateway get `TRI` | `200` | `401` | `401` | `401` | `401` | `401` | `200` | `200` | `200` | `200` |
| Direct Product list | `200` | `200` | `200` | `200` | `200` | not parsed | `200` | `200` | `200` | `200` |
| Direct Product get `TRI` | `200` | `200` | `200` | `200` | `200` | not parsed | `200` | `200` | `200` | `200` |

Local-dev and QA produced the same results for valid, absent, malformed, and invalid-signature
credentials on list and get routes. Expiry, future not-before, issuer, audience, role, and identity
propagation were tested only in the controlled isolated setup. The negative cases do not touch the
shared database. A `401` response has an empty body and no content type in the observed gateway.

Authentication and authorization are distinct in this model. The gateway authenticates the token,
then grants every authenticated principal the same Product list and get access. There is no
resource, role, or per-product entitlement decision. A token whose only role is unrelated to `TRI`
can read `TRI`; direct Product access performs no authentication at all.

## Identity propagation and transport

The isolated Product listener recorded one downstream request for every accepted gateway case. It
received no `Authorization` header and no user, principal, role, or authentication metadata header.
Rejected gateway cases made no downstream request. The gateway therefore terminates caller
authentication and invokes Product as an anonymous backend client.

The deployed Product and gateway Services are cluster-internal `ClusterIP` services. The gateway
calls Product over plain HTTP using its environment-specific service URL. Product connects to
PostgreSQL with its database credential and environment-configured SSL flag; that database identity
is separate from the caller and is covered by the data-access baseline. No mTLS or workload identity
protects the gateway-to-Product hop in the observed Java design.

## Final Java-compatible mapping for #133

The future Product pilot must keep these access results until an explicit hardening decision changes
them:

- Existing gateway-facing Product HTTP reads continue to require a correctly signed, unexpired JWT
  with a subject. Missing, malformed, bad-signature, and expired credentials remain negative tests.
- Issuer, audience, future `nbf`, and product-code roles are not rejection conditions in the current
  model. #133 must test those accepted cases so it does not accidentally introduce authorization or
  claim checks under a parity claim.
- The gateway/adapter hop does not forward the bearer token or principal metadata to Product. The
  Go HTTP or gRPC backend entry remains an internal anonymous business endpoint with the same list
  and get access, including direct access, unless the maintainer explicitly chooses a new trust
  boundary.
- No product-specific permission result exists. A role mismatch must not become a fabricated `403`.
  Positive tests include list/get with an unrelated role and direct reads without credentials.
- A new workload identity, mTLS policy, JWT rotation mechanism, or backend role check is separate
  hardening work.

These rules preserve the effective Java security model; they do not claim that anonymous direct
Product access or shared symmetric trust is a sufficient long-term design.

The binding entry and hop mapping is:

| Entry or hop | Identity and enforcement required during coexistence | Transport/trust requirement | #133 proof |
| --- | --- | --- | --- |
| Browser/client → Java gateway `GET /api/products` and `GET /api/products/{code}` | Gateway verifies the existing HMAC signature, expiry, and non-empty `sub`. Missing, malformed, invalid-signature, expired, or missing-subject tokens are `401`. Future `nbf`, issuer, audience, and roles remain non-enforcing. | Existing gateway HTTP exposure and trust-material ownership remain unchanged. | Real-listener positive and negative matrix, including accepted issuer/audience/future-`nbf`/unrelated-role cases. |
| Java gateway → Go compatible HTTP | Anonymous backend call. Do not forward bearer token, principal, role, or invented identity metadata. Go performs no caller or product-entitlement check. | Cluster-internal plain HTTP, matching the observed hop. Existing Java gateway owns authentication, retry, and fallback. | Record the downstream request and prove all identity headers are absent; prove rejected gateway requests make no backend call. |
| Direct Go HTTP list/get | Anonymous, matching direct Java Product. No JWT parser, role check, or `403` result is introduced. | Internal `ClusterIP` exposure owned by #134; no claim of public bypass protection. | List/get succeed without credentials and with malformed, expired, or unrelated credentials because the backend does not parse them. |
| Direct Go unary gRPC list/get | Anonymous internal business entry with the same list/get authorization result as direct HTTP. Transport validation and application errors are not authorization decisions. | Internal gRPC port on the distinct Go Service. No mTLS or workload identity is required for Java parity. | Real gRPC listener tests prove unauthenticated success and absence of role/product checks; #134 proves the port is not externally exposed. |
| Go HTTP adapter → application handler | No network identity transition and no principal synthesis. The adapter passes only the decoded Product request. | In-process boundary. | Adapter tests prove headers/claims do not alter Product results. |
| Go service → PostgreSQL | Separate `go_product_reader` database identity; never the end-user JWT subject and never the Java `product` owner. | PostgreSQL credential supplied through the environment Secret; minimum grants are defined in `data-baseline.md`. | #134 verifies the deployed identity and effective read-only grants; permission fixtures remain issue #131 evidence. |

There is no denied product entitlement case in the Java model. An unrelated or product-mismatched
role is therefore a positive access case after successful gateway authentication, and direct Go
HTTP/gRPC access without a bearer token is also a positive parity case. #133 must keep authentication
failures (`401`) distinct from authorization (`403`, which is not expected), backend application
errors, and database permission failures.

The existing HMAC key lifecycle remains owned by Auth and the Java gateway configuration. The Go
Product service does not consume that signing key under this mapping. Rotation redesign, issuer or
audience enforcement, future-`nbf` enforcement, product-role authorization, mTLS, and workload
identity require separate explicit hardening decisions and cannot be added under a parity claim.

## Reproduction

Run the isolated matrix and fixture checks from the repository root:

```shell
make product-baseline-test BASELINE_SUITE=access
make product-baseline-fixtures-check BASELINE_PART=access
```

Live capture requires Product, gateway, and Product-database forwards plus a temporary file holding
a short-lived gateway token. It records valid, absent, malformed, and invalid-signature cases only:

```shell
make product-baseline-capture \
  BASELINE_ENV=local-dev \
  BASELINE_PART=access \
  BASELINE_PRODUCT_DB_AFTER_PORT=5493 \
  BASELINE_GATEWAY_TOKEN_FILE=/path/to/temporary-token
# Review the ignored run, then:
scripts/product-baseline/accept-access.sh \
  ai/artifacts/epic-4.2/issue-131/access-runs/local-dev-<timestamp>.json
```

Repeat against QA only after stopping local-dev, starting QA, and passing the environment preflight.
After resuming a paused QA VM, synchronize each node clock from its RTC before capture; the access
capture rejects an environment clock more than five minutes from its host. The capture shreds
temporary authorization-header files and never copies the token into a fixture.
