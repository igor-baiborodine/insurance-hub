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
captured revisions and catalog identity. It does not establish a deterministic list order, a byte
identity requirement, or gateway retry/fallback behavior. Those rules remain Step 10 decisions;
failure-path evidence is recorded below.

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
The fixture preserves that observable behavior without credentials or environment data. Step 10
makes the explicit compatibility-versus-safety decision.

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
catalog for list and from a genuinely missing product for get. This is observable legacy behavior,
not an approved Go contract. Step 10 must decide whether to preserve those responses and whether
to remove internal Product error detail for safety.

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
