<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Product security baseline](#product-security-baseline)
  - [Current trust and credential ownership](#current-trust-and-credential-ownership)
  - [Observed access matrix](#observed-access-matrix)
  - [Identity propagation and transport](#identity-propagation-and-transport)
  - [Final Java-compatible mapping for Issue-133](#final-java-compatible-mapping-for-issue-133)
  - [Isolated application evidence for Issue-133](#isolated-application-evidence-for-issue-133)
  - [Go deployment exposure gate for Issue-134](#go-deployment-exposure-gate-for-issue-134)
  - [Reproduction](#reproduction)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

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

The source locations are the [Auth token configuration](../../../../legacy/auth-service/src/main/resources/application.yml)
and [gateway verifier configuration](../../../../legacy/agent-portal-gateway/src/main/resources/application.yml).
The [local-dev Auth](../../../../k8s/overlays/local-dev/svc/auth/legacy/deployment-patch.yaml),
[QA Auth](../../../../k8s/overlays/qa/svc/auth/legacy/deployment-patch.yaml),
[local-dev gateway](../../../../k8s/overlays/local-dev/svc/agent-portal-gateway/legacy/deployment-patch.yaml),
and [QA gateway](../../../../k8s/overlays/qa/svc/agent-portal-gateway/legacy/deployment-patch.yaml)
workload patches set environment/image and other runtime settings without supplying separate JWT
trust material. Changing the coupled Auth/gateway trust material or token policy requires a
separately reviewed change; this parity ticket does not rotate keys, revoke tokens, or alter users.
The [Go Product configuration](../../../../services/product-service/internal/config/config.go)
consumes a separate PostgreSQL reader URL and has no gateway signing-key setting.
The [local-dev gateway destination](../../../../legacy/agent-portal-gateway/src/main/resources/application-local-dev.yml)
and [QA gateway destination](../../../../legacy/agent-portal-gateway/src/main/resources/application-qa.yml)
still name the Java Product Service. Issue #134 subsequently confirmed their effective routing in
both deployed environments with live gateway responses and direct Java response comparisons.

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

These direct rows describe issue #131's Java Product observations. Issue #133's separate Go
HTTP/gRPC results are recorded below; the earlier live captures do not prove deployed Go exposure.

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

The observed Java Product and gateway Services are cluster-internal `ClusterIP` services. The gateway
calls Product over plain HTTP using its environment-specific service URL. Product connects to
PostgreSQL with its database credential and environment-configured SSL flag; that database identity
is separate from the caller and is covered by the data-access baseline. No mTLS or workload identity
protects the gateway-to-Product hop in the observed Java design.

## Final Java-compatible mapping for Issue-133

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

The binding entry and hop mapping separates source configuration, issue #131's Java observations,
issue #133's isolated application tests, and the deployment checks completed by #134:

| Entry or hop | Access result and identity | Transport and exposure | Configuration owner | Evidence and remaining gate |
| --- | --- | --- | --- | --- |
| Auth → caller | Auth issues an HS256 bearer token with the configured 86,400-second lifetime and product-code roles. | Existing Auth endpoint and token delivery. | Auth image and application configuration; gateway must share its verification material. | #131 source and safe live-token captures; #133 generates separate signing material only for isolated tests. |
| Caller → Java gateway Product list/get | Signed, unexpired token with a non-empty subject succeeds. Missing, malformed, bad-signature, expired, or missing-subject token yields empty `401` with no content type. Future `nbf`, different issuer/audience, and unrelated role still succeed; no Product-role `403`. | Existing `GET /api/products` and `GET /api/products/{code}` routes. | Java gateway security configuration and Product controller; Auth/gateway trust material remains coupled. | #131 local-dev/QA live subset and isolated baseline; #133 real gateway → real Go tests cover ten rejected and ten accepted cases, with zero downstream calls for rejections. |
| Java gateway → Product HTTP | Accepted call reaches the backend once without bearer token or user/principal/role/authentication metadata. Existing retry/fallback behavior remains. | Compatible HTTP `/products` and `/products/{code}`; existing gateway traffic still targets Java. | Java gateway Product client and its environment destination; #137 owns cutover. | #131 Java transport/propagation observation; #133 test observer forwards to real Go and detects identity-marker absence while preserving trace metadata; #134 live local-dev and QA checks confirmed the gateway still selected Java. |
| Direct Go HTTP list/get | Anonymous success with or without supplied credential/identity headers; missing/invalid products keep application errors rather than `401`/`403`. | Internal Go business HTTP listener. | Go Product HTTP adapter, listener configuration, and Service exposure. | #133 real Go listener tests enforce all eighteen direct access case IDs for HTTP, identical catalog results, and unchanged errors; #134 live verification passed in local-dev and QA with internal-only exposure. |
| Direct Go unary gRPC list/get | Anonymous success with or without supplied credential/identity metadata; missing/invalid products keep `NotFound`/`InvalidArgument`, not authentication or permission status. | Internal generated Product v1 gRPC listener. | Go Product gRPC adapter, listener configuration, and Service exposure. | #133 real Go listener tests enforce all eighteen direct access case IDs for gRPC and matching catalog/error results; #134 live verification passed in local-dev and QA with internal-only exposure. |
| Go HTTP adapter → application use cases | No principal is created, filtered, or passed; headers do not change Product results. | In-process dispatch, with no network or gateway gRPC bridge. | Go Product composition and HTTP adapter. | #133 direct HTTP and adapter tests plus source inspection; trace context is separate from caller identity. |
| Go management `/livez` and `/readyz` | Anonymous constant health responses; readiness checks database read access and returns safe `503` during dependency loss or drain. HTTP profiling and gRPC reflection stay unavailable. | Separate internal Go management listener. | Go Product health/configuration and Service/route exposure. | #132/#133 real-listener diagnostics and isolated verifier; #134 rendered and effective exposure checks passed in local-dev and QA. |
| Go service → PostgreSQL | Separate `go_product_reader` runtime login with read-only Product access, never an end-user subject or the Java owner. | Database connection using a separate runtime credential; no startup DDL, seeding, or writes. | Explicitly provisioned #134 role/grants and Secret; Go consumes `PRODUCT_DATABASE_URL`. | #131 candidate role and #132/#133 disposable read-only/startup tests; #134 actual Secret-backed login, effective-grant, safe-denial, and unchanged-catalog checks passed in both environments. |

There is no denied product entitlement case in the Java model. An unrelated or product-mismatched
role is therefore a positive access case after successful gateway authentication, and direct Go
HTTP/gRPC access without a bearer token is also a positive parity case. #133 must keep authentication
failures (`401`) distinct from authorization (`403`, which is not expected), backend application
errors, and database permission failures.

The existing HMAC key lifecycle remains owned by Auth and the Java gateway configuration. The Go
Product service does not consume that signing key under this mapping. Rotation redesign, issuer or
audience enforcement, future-`nbf` enforcement, product-role authorization, mTLS, and workload
identity require separate explicit hardening decisions and cannot be added under a parity claim.

## Isolated application evidence for Issue-133

The [combined gateway test](../../../../legacy/agent-portal-gateway/src/test/java/pl/altkom/asc/lab/micronaut/poc/gateway/GatewayProductSecurityIT.java)
starts a real Java gateway against a real Go HTTP listener backed by disposable PostgreSQL. It
replays all twenty gateway IDs from the [accepted access corpus](../../../../legacy/product-service/src/test/resources/product-read-baseline/access/cases.json): ten rejected cases have the exact empty `401` response and no Go call; ten accepted cases return the QA catalog with one Go call. The observer detects supplied bearer and identity markers before forwarding, finds neither on accepted gateway calls, and keeps normal B3/W3C tracing headers distinct from identity. Failure baselines retain gateway retry/fallback and safe errors; the new tests do not switch its deployed destination.

The [direct Go access suite](../../../../services/product-service/internal/testing/access_integration_test.go)
uses the production composition path, real HTTP/gRPC listeners, and generated Product v1 client.
It enforces all eighteen accepted direct IDs on each transport, with unchanged list/get catalog and
application errors when credential or identity metadata is supplied. Each transport exercises
nine categories on each of list and get: absent, valid, malformed, invalid-signature, expired,
future-`nbf`, different issuer, different audience, and unrelated role. It also checks healthy and
dependency-loss management responses, disabled profiling/reflection, absence of gateway signing
configuration, and safe diagnostic output. These are isolated runtime results with the accepted QA
catalog, not new local-dev or QA cluster observations. The [Product module check](../../../../services/product-service/Makefile)
owns the direct suite; the separate security gate owns the combined gateway path and does not run
the direct suite again. Generated signing, bearer, identity, database-password, and full connection
URL markers were absent from captured Java/Go success and failure output; Product health and client
errors remained generic. This does not establish collector delivery, which belongs to #135.

## Go deployment exposure gate for Issue-134

The separate `product-security-verify` target checks an **already running** Go Product target. It
requires explicit Kubernetes context, namespace, environment, and three reachable endpoints; it
has no default cluster target. It reads the accepted `access/<environment>.json` observations as
response oracles and sends anonymous `GET /products`, `GET /products/TRI`, generated Product v1
gRPC list/get, `/livez`, `/readyz`, and disabled-profiling/reflection probes. HTTP bodies are compared
with the accepted lossless Product comparator; gRPC fields are compared to the same accepted catalog.
Requests have finite deadlines and responses have a size limit. Failures report only the failing
probe category. The target does not load data into PostgreSQL, change roles or Secrets, or deploy.

For the disposable issue #133 harness, set `PRODUCT_SECURITY_TARGET_CONTEXT=isolated` and
`PRODUCT_SECURITY_TARGET_NAMESPACE=isolated` and pass its three published loopback addresses as
`http://host:port`, `host:port`, and `http://host:port` respectively. For a deployed target, first
select and confirm the intended context and namespace, then start explicit local port-forwards for
the Go Service's HTTP, gRPC, and management ports. Run from the repository root, substituting the
actual forwarded ports and one environment at a time:

```shell
PRODUCT_SECURITY_TARGET_CONTEXT=kind-local-dev-insurance-hub \
PRODUCT_SECURITY_TARGET_NAMESPACE=local-dev-all \
PRODUCT_SECURITY_EXPECTED_ENV=local-dev \
PRODUCT_SECURITY_HTTP_ENDPOINT=http://127.0.0.1:<http-port> \
PRODUCT_SECURITY_GRPC_ENDPOINT=127.0.0.1:<grpc-port> \
PRODUCT_SECURITY_HEALTH_ENDPOINT=http://127.0.0.1:<management-port> \
make product-security-verify
```

Use `qa-insurance-hub`, `qa-svc`, and `qa` for QA after stopping local-dev forwards and switching
environments. The target rejects an active context or namespace mismatch before probing. A local
port-forward proves reachability through that forward only; it does not prove cluster-only exposure
or that the gateway selects a particular backend. Acceptance and regression records must retain
the selected resource names, ports, revision, active context, namespace, and probe result for each
environment.

Before each local-dev or QA acceptance or regression run, complete these checks in sequence:

1. Stop the prior environment and its forwards. Select the new Kubernetes context, verify its
   namespace, target cluster/revision, ready Go and Java workloads, and current UTC host and node
   clocks. Correct QA clock drift before token-dependent captures; compare with the accepted
   environment snapshot only after the context and clock preflight passes.
2. Inspect both rendered manifests and effective Deployment, Pod, Service, EndpointSlice, Ingress,
   Gateway, HTTPRoute, GRPCRoute, and any other installed route resources. Require distinct Go and
   Java workload labels/selectors and a ready Go endpoint behind the intended Go Service. Require
   internal `ClusterIP` Services for Go business HTTP/gRPC and management ports. Reject any
   Go-related external IP, NodePort, LoadBalancer, Ingress/Gateway route, host port, or host network
   setting that publishes those listeners outside the intended cluster boundary. Include management
   exposure and namespace-wide or shared routing in the inspection, not just the Product Service.
3. Verify the deployed Go Secret reference and effective database login identify the separate
   restricted reader. Check grants, ownership, membership, `PUBLIC` and default grants, and denied
   write/DDL behavior through the database role proof; preserve the accepted catalog/schema
   before and after. The verifier does not inspect PostgreSQL or prove those grants.
4. Run the anonymous read-only verifier through explicit Go Service forwards and retain its result.
   Verify the existing Java gateway destination and effective routing still select the Java Product
   Service, using a separate live gateway observation. Do not infer routing from the Go probe.
5. Repeat the context, namespace, clock, rendered/effective resource, role, and read checks after
   switching to the second environment. Keep fixture loading and negative dependency tests on
   disposable infrastructure only.

Anonymous internal Product access is the selected parity behavior. `ClusterIP` does not authenticate
or restrict reachable in-cluster clients. This gate makes no claim of gateway-exclusive access,
mTLS, or workload identity. The verifier alone does not prove complete deployment acceptance;
issue #134 combined it with effective exposure, reader, catalog, lifecycle, and Java-routing
evidence in both environments. The final coordinates and rerun procedure are in the
[deployment runbook](deployment-runbook.md#acceptance-evidence-and-handoff).

## Reproduction

For the isolated #133 proof, use Java 14, system Maven, Go 1.27.2, a working Docker daemon,
`jq`, Python 3, `timeout`, and `setsid`. Bootstrap the Product module's pinned tools explicitly;
that step needs Node.js 20 or newer (CI uses 24.13.0) and dependency/Maven package access on a
fresh machine. Leave `PRODUCT_DATABASE_URL` unset. From the repository root:

```shell
make go-product-bootstrap-tools
make product-security-test-tooling
make product-security-check
make -C services/product-service check
```

`product-security-check` validates the accepted access fixtures, runs the Java Product/gateway
access regression, and runs the real gateway → Go → disposable PostgreSQL matrix. The Product
module's full `check` owns the direct Go HTTP/gRPC access suite and exact case-ID assertion, so the
security aggregate does not run that suite twice. The dedicated
[security workflow](../../../../.github/workflows/product-security.yml) runs the controlled failure
checks and security aggregate on relevant PRs and main pushes; hosted results are separate from
local runs. The test script enforces a bounded token-clock drift, Java execution timeout, and
exact case/report selection. It uses generated test signing material, a disposable QA catalog and
restricted reader, and private temporary files; success or failure stops the Java/Go processes,
listeners, and PostgreSQL container. It rejects external database settings and does not rewrite
accepted fixtures, deployed credentials, or gateway routing.

For the earlier #131 Java baseline alone, the narrower commands remain:

```shell
make product-baseline-test BASELINE_SUITE=access
make product-baseline-fixtures-check BASELINE_PART=access
```

Live baseline capture requires Java Product, gateway, and Product-database forwards plus a temporary
file holding a short-lived token issued by the existing Auth flow. It records valid, absent,
malformed, and invalid-signature cases only. Before each environment, confirm the intended
Kubernetes context/namespace and UTC host/node clocks; the capture rejects more than five minutes
of target/host clock difference. Store the temporary token outside the repository with mode `0600`
(`umask 077` before creating it), check its expiry, pass only its path, and use a cleanup trap with
`shred -u` for the original file after the run or on failure. The capture shreds its temporary
authorization-header files; the caller remains responsible
for removing the original token file. Generate negative claim variants only in isolated tests;
do not change deployed keys, token policy, or users for validation.

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
After resuming a paused QA VM, synchronize each node clock from its RTC before capture. The capture
never copies the token into a fixture. Issue #134 delivered the Go deployment and live exposure/role
evidence; #135 owns telemetry delivery; #136 owns differential, consumer, load, failure, and
rollback qualification; #137 alone owns any routing cutover. Anonymous internal Go access and
shared HS256 trust remain explicit interim limitations, not evidence of workload authentication or
revocation.
