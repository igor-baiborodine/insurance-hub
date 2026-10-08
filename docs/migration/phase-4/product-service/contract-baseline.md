# Product contract baseline

This document records Java Product wire observations and current consumer needs for issue #131.
The accepted machine-readable environment fixtures are
`legacy/product-service/src/test/resources/product-read-baseline/http/local-dev.json` and
`http/qa.json`. Stored JSONB remains a separate data-baseline input; these files retain the raw HTTP
body returned by each listener.

## Successful HTTP observations

Both environments were captured against the accepted schema identity
`76fac669b254931c0709c236fdd7804b` and data identity
`7ba18949c152e2979df14a28a67cba7f`. Capture checked those identities before and after the ten HTTP
requests. Every direct and gateway request returned `200` with `Content-Type: application/json`.
The gateway requests used a short-lived valid bearer token; only `Bearer <redacted>` is retained.

| Boundary | Requests | Observed body |
| --- | --- | --- |
| Direct Product | `GET /products`; `GET /products/{code}` for `CAR`, `FAI`, `HSI`, and `TRI` | List order was `CAR`, `FAI`, `HSI`, `TRI`; each lookup returned the matching object. |
| Gateway | `GET /api/products`; `GET /api/products/{code}` for all four codes | The successful raw body matched the corresponding direct response byte for byte. |

All ten response-body checksums also matched across local-dev and QA. This is an observation for the
captured revisions and catalog identity. The binding comparison and ordering rules are defined
below; failure-path evidence remains distinct from the accepted pilot behavior.

The representative wire objects always contain `code`, `name`, `image`, `description`, `covers`,
`questions`, `maxNumberOfInsured`, and `icon`. Covers contain `code`, `name`, and `optional`;
`sumInsured` is omitted when its DTO value is null and present as an integer JSON number otherwise.
Questions contain `type`, `code`, `index`, and `text`; choice questions also contain ordered
`choices` with `code` and `label`. The representative rows contain `numeric` and `choice`; the
isolated data-semantics corpus below also establishes the `date` variant.

## Data semantics observations

The accepted `data-edges/cases.json` fixture contains 16 named cases covering all 25 registered
data-semantics variants. The test starts Java normally against disposable PostgreSQL, replaces the
seeded rows only inside that Testcontainer, inserts raw JSONB through JDBC, and captures the real
Product HTTP response. This deliberately bypasses Java write serialization while retaining the
production JSONB decoder, domain mapping, DTO assembly, and HTTP serialization path.

| Input class | Observed Java result |
| --- | --- |
| Decimal values | `12.3400`, `12345678901234567890.123456789`, and `0.00` retain their exact JSON number tokens on the wire. A null `sumInsured` is omitted. |
| Question variants | `choice`, `date`, and `numeric` decode and serialize with the same discriminator. Choice and nested-array order is retained in the observation. |
| Absent and empty product collections | Absent `covers`/`questions` and explicit empty arrays both result in those fields being omitted. Primitive `maxNumberOfInsured` remains present as `0`. |
| Primitive JSON null | Null `maxNumberOfInsured` and question `index` become `0`; null cover `optional` becomes `false`. |
| Choice collection presence | Absent, null, and empty `choices` all map to an empty collection and the field is omitted from the response. |
| Explicit null product collections | Null `covers` or null `questions` reaches domain conversion and produces HTTP `500` with `Internal Server Error: null`. |
| Unknown fields | An unknown product-level property is ignored. Unknown properties inside a cover, question, or choice fail JSONB decoding and produce HTTP `500`. |
| Question discriminator | Unknown, absent, and null `type` values fail JSONB decoding and produce HTTP `500`. The response currently exposes the Hibernate decode exception category. |
| SQL null | The database rejects null `definition` and null `code` with SQLSTATE `23502`; these are distinct from JSON null and absent JSON keys. |
| Product ordering | A list inserted as `EDGE_Z`, `EDGE_A`, `EDGE_M` was returned in that order. Covers, questions, and choices also retained input order. These are repeatable observations, not ordering guarantees, because the repository query has no explicit sort. |

The nested-field and discriminator failures expose internal exception detail in the legacy response.
The fixture preserves that observation without credentials or environment data. The pilot decision
below keeps the status and failure category while replacing internal exception text with a safe
generic response.

## Failure and boundary observations

The accepted `failures/cases.json` fixture contains 30 direct and gateway observations covering all
21 registered failure and boundary variants. Product tests use raw HTTP requests against the real
listener and disposable PostgreSQL. Gateway tests use the real authenticated listener with a
loopback backend that records downstream paths and attempts; fallback invocations are counted from
the fallback component itself. Test-only dependency timeouts bound cleanup and do not change
production configuration.

| Scenario | Direct Product | Gateway |
| --- | --- | --- |
| Empty catalog list | `200` with `[]`. | `200` with `[]`; one downstream request and no fallback. |
| Single catalog | List and existing lookup return `200` with the sole product. | Same observable results; one downstream request and no fallback. |
| Missing code | Empty `Maybe` becomes `404` with the standard `Page Not Found` body. | Backend `404` becomes gateway `404`; one request and no fallback. |
| Lowercase, encoded whitespace, or encoded slash | Each value is preserved in the self link and returns `404`; matching remains case-sensitive. | The raw encoded value is forwarded once and returns gateway `404`; no retry or fallback. |
| Invalid percent encoding | Rejected at the listener with `400` and a malformed-URI message. | Rejected with `400` before any Product call. |
| Trailing slash/empty segment | `/products/` resolves to the list route and returns `200`. | `/api/products/` also resolves to list and forwards `/products` once. |
| Incompatible JSONB | Product returns `500`; the response exposes `org.hibernate.HibernateException` and the JSONB decode category. | Each backend `500` causes three total attempts and one fallback; get then returns gateway `404`, hiding the backend body. |
| Database unavailable | List and get return `500`, with different Hibernate transaction/session details. | A backend `503` causes three attempts and one fallback; list returns `200 []`, while get returns `404`. |
| Backend `500` | Not a separate direct case beyond decode failure. | Three attempts and one fallback; list returns `200 []`, while get returns `404`. |
| Backend connection unavailable | Product dependency loss is represented by the database case above. | Three dropped connections and one fallback; list returns `200 []`, while get returns `404`. |

The annotation value `attempts = "2"` therefore results in three total calls: the initial call and
two retries. Gateway fallback makes dependency failure indistinguishable from a genuinely empty
catalog for list and from a genuinely missing product for get. This remains the accepted behavior
of the existing Java gateway during coexistence. The Go backend does not reproduce fallback; it
returns a retry-eligible server failure and leaves retry/fallback ownership with that gateway.

## Consumer expectations

| Consumer | Fields and behavior used |
| --- | --- |
| Vue catalog list/card | Calls gateway list; uses product `code`, `name`, `image`, `description`, and iterates covers in response order. |
| Vue cover list | Uses cover `code` as the key and renders `name` and `sumInsured`; a falsy amount suppresses the currency label. |
| Vue product detail/offer | Calls gateway get; uses product display fields, iterates questions and choices in response order, renders `numeric` and `choice`, and submits product, cover, question, type, and answer values. |
| Vue dashboard | Calls gateway list; uses product `code`, `name`, and `icon` for cards, filters, and dashboard requests. |
| Angular list/detail | Calls both gateway reads and uses the same product display fields, ordered covers, cover codes, question `code`/`type`/`text`, and choice `code`/`label`; it renders `choice` and `numeric`. |

Neither frontend has a `date` question rendering branch. The dashboard service, Pricing, and Policy
carry product codes but the source search found no additional Product catalog client. Frontend use
of response order makes array order visible, but source inspection and two matching captures do not
by themselves create a deterministic-order promise.

## Binding field mapping and presence rules

The following mapping is the contract input for #132. Concrete protobuf field numbers, package
names, generator pins, and Go types remain owned by that ticket.

| Stored source | Java domain and DTO | Required HTTP JSON | Required gRPC meaning |
| --- | --- | --- | --- |
| `product.code` | `Product.code` → `ProductDto.code` | Opaque, case-sensitive `code` string. Do not trim, case-fold, or decode it beyond normal URI path decoding. | Product code string; `GetProduct` uses the same opaque value. |
| `definition.name`, `image`, `description`, `icon` | Same-named Product and DTO strings | Preserve non-empty strings. Missing, null, and empty stored values all result in omission and are equivalent at the external boundary. | String values; absence/empty need not be distinguished because Java collapses them. The HTTP adapter omits empty values. |
| `definition.covers` | Ordered `List<Cover>` → ordered `List<CoverDto>` | Non-empty `covers` array. Missing or empty becomes omitted; explicit JSON null is a server failure. | Repeated covers in stored order. Empty represents Java's omitted successful result; null is rejected while decoding storage. |
| cover `code`, `name`, `description` | Same-named Cover and DTO strings | Preserve non-empty strings and omit empty/null values. | Same string values and collapsed empty presence as HTTP. |
| cover `optional` | Java primitive `boolean` | Always emit `optional`; missing or null becomes `false`. | Non-optional boolean with default `false`. |
| cover `sumInsured` | `BigDecimal` | Omit null; otherwise emit an unquoted JSON number preserving coefficient and scale, including trailing zeros. Never convert through binary floating point. | Optional exact-decimal value preserving the lexical coefficient and scale. A string or structured decimal is required; protobuf `double` is not compatible. The HTTP adapter renders it as an unquoted JSON number. |
| `definition.questions` | Ordered subtype list → ordered DTO subtype list | Non-empty `questions` array. Missing or empty becomes omitted; explicit JSON null is a server failure. | Repeated questions in stored order with a required variant discriminator. |
| question `type` | `choice`, `date`, or `numeric` domain/DTO subtype | Emit the same `type`. Unknown, missing, or null discriminators are storage decode failures. | Required one-of variant with choice, date, and numeric alternatives. An unset or unknown variant is invalid. |
| question `code`, `text` | Same-named Question and DTO strings | Preserve non-empty strings and omit empty/null values. | Same string values and collapsed empty presence as HTTP. |
| question `index` | Java primitive `int` | Always emit `index`; missing or null becomes `0`. | Non-optional integer with default `0`. |
| choice `choices` | Ordered choice list | Non-empty array in stored order. Missing, null, and empty all become omitted. | Repeated choices in stored order; empty covers all three Java inputs. |
| choice `code`, `label` | Same-named Choice and DTO strings | Preserve non-empty strings and omit empty/null values. | Same string values and collapsed empty presence as HTTP. |
| unknown properties | Product definition ignores only product-level unknowns; nested definitions reject them. | Product-level unknowns do not appear. Unknown cover/question/choice properties cause a safe server failure. | The storage adapter applies the same tolerance boundary before building the response. |

The database `NOT NULL` rules for `product.code` and `product.definition` remain separate from JSON
presence. The Go reader must not rewrite stored JSON, seed defaults, or serialize a decoded model
back into the shared column.

## Scenario parity decisions

The sole repository maintainer owns these decisions. The accepted default is observable Java
compatibility, with two explicit rules: top-level list order is not contractual, and unsafe internal
exception text is not copied into the pilot.

| Fixture/scenario IDs | Java evidence | Required pilot result and comparison rule | Accepted difference and rationale |
| --- | --- | --- | --- |
| `HTTP-DIRECT-LIST-001`, `HTTP-GATEWAY-LIST-001`, all successful get IDs | `200 application/json`; direct and gateway bodies matched for the accepted snapshot. | Preserve status, content type, fields, presence, number lexemes, subtype values, and nested-array order. Compare JSON structurally rather than by object-key or response-byte order. | Volatile transport headers and JSON object-key order may differ. |
| `DATA-EDGES-RICH-001` | Exact scaled/high-precision decimals and all three variants survived the complete Java path. | Exact decimal coefficient and scale, field presence, subtype, and nested order are binding in gRPC meaning and HTTP output. | None. |
| `DATA-EDGES-ABSENT-001`, `DATA-EDGES-EMPTY-001`, `DATA-EDGES-PRIMITIVE-NULL-001`, `DATA-EDGES-CHOICE-PRESENCE-001` | Java collapses the documented missing/null/empty values and emits primitive defaults. | Reproduce the field-level presence table above; comparisons must distinguish omitted fields from present zero/false values. | Protobuf representations may collapse inputs that Java already makes externally indistinguishable. |
| `DATA-EDGES-NULL-COVERS-001`, `DATA-EDGES-NULL-QUESTIONS-001` | Direct Java returns `500` and exposes internal text. | gRPC `Internal`; direct HTTP `500 application/json` with exactly `{"message":"Internal Server Error"}`. | Internal exception text and framework self-link are removed because they leak or couple implementation detail; status and failure class remain compatible. |
| `DATA-EDGES-UNKNOWN-COVER-001`, `DATA-EDGES-UNKNOWN-QUESTION-001`, `DATA-EDGES-UNKNOWN-CHOICE-001`, all `DATA-EDGES-SUBTYPE-*` | Direct Java returns `500` with Hibernate/Jackson detail. | Reject at storage decoding, return gRPC `Internal`, and map direct HTTP to the same generic `500` body. Do not silently drop the nested field or variant. | Internal exception text and framework self-link are removed for the same safety reason. |
| `DATA-EDGES-SQL-NULL-DEFINITION-001`, `DATA-EDGES-SQL-NULL-CODE-001` | PostgreSQL rejects fixture writes with `23502`. | Preserve schema constraints; the read-only application never attempts these writes. | Not a transport case. |
| `DATA-EDGES-PRODUCT-ORDER-001` and representative list IDs | Java repeatedly returned scan/insertion order, but the repository has no `ORDER BY`. | Product-list order is unspecified. Differential comparison matches list elements by unique `code`; duplicate/missing/extra codes still fail. | Top-level product-array order may differ because Java supplies no stable contract. |
| Rich case nested covers/questions/choices | Java retained stored order and consumers iterate it. | Nested array order is binding and compared positionally. | None. |
| Empty/single `FAIL-PRODUCT-*` and `FAIL-GATEWAY-*` | Empty list is `200 []`; one row returns `200`. | Preserve these outcomes. | None. |
| Missing/lowercase/whitespace/encoded-slash lookup cases | Valid decoded values use exact, case-sensitive lookup and return `404` when absent. | No trimming, case conversion, or product-code normalization; gRPC `NotFound`, HTTP `404`. | None. |
| `FAIL-*-LOOKUP-BAD-ENCODING-001` | HTTP listener rejects malformed URI encoding with `400`; gateway makes no backend call. | HTTP adapter rejects before RPC/application invocation with `400`. A direct gRPC request is already decoded and has no equivalent malformed-URI state. | Transport-specific absence of an RPC case. |
| `FAIL-*-LOOKUP-EMPTY-SEGMENT-001` | Trailing slash resolves to list. | HTTP `/products/` remains a list request. Direct gRPC `GetProduct` with empty code returns `InvalidArgument`; it must not be treated as list. | Explicit method identity removes HTTP route ambiguity. |
| Direct decode/database failure IDs | Java returns `500`; decode bodies expose internals. | Decode failures map to gRPC `Internal`; database unavailability maps to `Unavailable`. Both map to safe direct HTTP `500` so the Java gateway still retries. | Safe generic error body; gRPC retains a more useful machine-readable category. |
| Gateway decode/database/backend error and unavailable IDs | Three downstream attempts, then list `200 []` or get `404`. | Preserve this existing Java gateway behavior during coexistence. The Go backend returns failure and does not implement retries or fallback itself. | None in the gateway result; responsibility is explicitly separated. |

Normalization is limited to volatile `Date`, connection/transfer headers, response byte framing,
JSON object-key order, and top-level product-list order. Method, decoded path value, status, content
type, non-volatile selected headers, field presence, exact decimal token, discriminator, scalar value,
nested array order, element identity, and safe error category remain comparison-sensitive.

## Versioned unary gRPC and HTTP boundary

#132 must expose two versioned unary operations with these semantics:

- `ListProducts` takes no filter, sort, or pagination input and returns zero or more products. An
  empty catalog is successful, and product order is unspecified.
- `GetProduct` takes one opaque non-empty product code and returns one product. An absent exact code
  is `NotFound`; an empty RPC code is `InvalidArgument`.
- Storage decoding failures are `Internal`; database unavailability is `Unavailable`. Error messages
  and details must be safe and must not contain SQL, Hibernate/Jackson classes, credentials, raw
  stored definitions, or internal topology.
- The compatible direct HTTP adapter owns `GET /products`, `GET /products/`, and
  `GET /products/{productCode}`. It renders the field/presence rules above, preserves observed
  success bodies and the captured `400`/`404` status, content type, message, and self-link semantics.
  It maps backend server failures to `500 application/json` with exactly
  `{"message":"Internal Server Error"}` and performs no retries or fallback.
- The existing Java gateway remains the authenticated edge and the only retry/fallback owner during
  coexistence. It continues to call a compatible Product HTTP route until a separately tested route
  change. Concrete routing and cutover remain #134/#137 work.

These rules define semantics only. #132 chooses protobuf names/numbers, an exact-decimal encoding,
validation/runtime libraries, generated outputs, and the concrete HTTP adapter implementation.

## Capture and validation

Run the listener and fixture checks with:

```shell
make product-baseline-test BASELINE_SUITE=happy
make product-baseline-fixtures-check BASELINE_PART=http
make product-baseline-test BASELINE_SUITE=data-edges
make product-baseline-fixtures-check BASELINE_PART=data-edges
make product-baseline-test BASELINE_SUITE=failures
make product-baseline-fixtures-check BASELINE_PART=failures
```

Live capture requires temporary Product, gateway, and Product-database forwards plus a file
containing a short-lived gateway token. When a Kubernetes database forward resets after one client
session, provide a second forward for the post-capture identity check:

```shell
make product-baseline-capture \
  BASELINE_ENV=local-dev \
  BASELINE_PART=http \
  BASELINE_PRODUCT_DB_AFTER_PORT=5493 \
  BASELINE_GATEWAY_TOKEN_FILE=/path/to/temporary-token
# Review the new ignored run before acceptance.
scripts/product-baseline/accept-http.sh \
  ai/artifacts/epic-4.2/issue-131/http-runs/local-dev-<timestamp>.json
```

The capture omits volatile `Date` and connection headers, stores content type and length, preserves
the response body as a raw JSON string with a SHA-256 checksum, and never writes the token. Fixture
validation rejects missing scenarios, changed body checksums, mismatched catalog/inventory links,
or bearer values other than the explicit redaction marker.
