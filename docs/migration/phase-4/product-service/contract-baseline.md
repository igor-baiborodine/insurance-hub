# Product contract baseline

This document records the successful Java Product wire observations and current consumer needs for
issue #131. The accepted machine-readable fixtures are
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
failure paths remain Step 7 evidence.

The representative wire objects always contain `code`, `name`, `image`, `description`, `covers`,
`questions`, `maxNumberOfInsured`, and `icon`. Covers contain `code`, `name`, and `optional`;
`sumInsured` is omitted when its DTO value is null and present as an integer JSON number otherwise.
Questions contain `type`, `code`, `index`, and `text`; choice questions also contain ordered
`choices` with `code` and `label`. The observed subtype values are `numeric` and `choice`. Decimal,
presence, unknown subtype, null/empty collection, and ordering edge cases remain Step 6 work.

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

Run the happy-path listener checks with:

```shell
make product-baseline-test BASELINE_SUITE=happy
make product-baseline-fixtures-check BASELINE_PART=http
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
