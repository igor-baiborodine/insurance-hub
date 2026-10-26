<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Established Product gateway cutover](#established-product-gateway-cutover)
  - [Preparation and target gate](#preparation-and-target-gate)
  - [QA PR1 release and Java baseline](#qa-pr1-release-and-java-baseline)
  - [QA Go, restoration and final acceptance](#qa-go-restoration-and-final-acceptance)
  - [Signal capture, evidence and cleanup](#signal-capture-evidence-and-cleanup)
  - [Local-dev switch and recovery](#local-dev-switch-and-recovery)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Established Product gateway cutover

This is the issue #137 procedure for the established Product route. The
[issue #136 qualification runbook](qualification-runbook.md) records an earlier isolated preview;
its Go Service `:8081` observation is historical. Use this guide with the
[Product deployment runbook](deployment-runbook.md) and
[observability runbook](observability-runbook.md). Run Make commands from the repository root
unless `-C` is shown. Keep sanitized phase evidence under ignored
`ai/artifacts/epic-4.2/issue-137/`.

## Preparation and target gate

PR1 prepares the Go candidate while the established QA gateway still selects Java. Product's
defaults are HTTP `127.0.0.1:8080`, management `127.0.0.1:8081`, and gRPC
`127.0.0.1:9090`; deployed settings bind `0.0.0.0` on those ports. The business Service maps
`80` to named `http`/`8080`, and the internal management Service maps `8081` to named
`management`/`8081`. Startup/liveness use management `/livez`; readiness uses management
`/readyz`. Both gateway overlays mount one Product-only generated `application.yml` via
`MICRONAUT_CONFIG_FILES`, with its hashed ConfigMap name in the Pod template. QA's override
selects `qa-product-api-legacy.qa-svc.svc.cluster.local:80`; local-dev's desired override
selects its Go Service on port `80`. A render or embedded Java YAML alone is not serving proof.

Before publication or target mutation, record the accepted fixture/schema/catalog identity,
source SHA, immutable Product/gateway image IDs, current Pod IDs, desired and Ready/applied Flux
revisions, and UTC clocks. Independently inspect the active context, exact namespaces, Java
fallback, reader and capacity. Stop owned forwards before switching environments. The Product
owners require these target coordinates:

| Environment | `DEPLOY_CONTEXT` | `DEPLOY_SVC_NS` | `DEPLOY_DATA_NS` | Route owner |
| --- | --- | --- | --- | --- |
| QA | `qa-insurance-hub` | `qa-svc` | `qa-data` | Reviewed `main` revisions, automatic Product release, Flux `qa-svc` |
| local-dev | `kind-local-dev-insurance-hub` | `local-dev-all` | `local-dev-all` | Guarded Product targets; `make -C k8s svc-deploy SVC_NAME=agent-portal-gateway ENV_NAME=local-dev` |

Run the owning PR1 gates on the exact candidate. The image/lifecycle tests use disposable
Docker, PostgreSQL and local Kind resources; they do not contact shared QA. The Kind deployment
test needs a ready CNPG operator. Inspect the image test's HTTP/management separation, the Kind
test's Service `80` catalog and management `8081` health, and readiness versus liveness during
dependency loss and shutdown. A skipped or missing required gate blocks release.

```sh
make -C services/product-service check-go-pr
make go-svc-image-check SVC_NAME=product
make go-svc-deploy-check SVC_NAME=product
make go-svc-deploy-test-tooling SVC_NAME=product
make go-svc-deploy-test SVC_NAME=product
make product-qualification-test
make product-baseline-fixtures-check BASELINE_ENV=qa
make product-baseline-fixtures-check BASELINE_ENV=local-dev
```

Review the mixed-Pod transition before PR1 merge. The old QA image must tolerate the new
explicit addresses, the new image must match its source SHA, and Services, probes and ready
EndpointSlices must agree throughout. Keep the QA gateway on Java. If source/image or port
compatibility is unknown, stop before publication.

## QA PR1 release and Java baseline

The merged PR1 Product source triggers **Go Service Release**. Record the merged source SHA,
matching run, scoped release tag, semantic image tag and digest, automated QA overlay commit,
and final Ready Flux source/applied revision. Those are separate identities; wait for the overlay
bot update before measurement. Do not manually apply QA manifests or suspend Flux. Set
`DEPLOY_ENV=qa`, the QA coordinates above, the observed semantic `DEPLOY_IMAGE_REF`, and full
applied `DEPLOY_SOURCE_REVISION`. Inspect the effective Java gateway route and new Product Pods:

```sh
make go-svc-deploy-preflight SVC_NAME=product
DEPLOY_EXPECTED_GATEWAY_PHASE=java make go-svc-deploy-status SVC_NAME=product
make go-svc-deploy-verify SVC_NAME=product
make product-baseline-fixtures-check BASELINE_ENV=qa
BASELINE_PREFLIGHT_CATALOG_OUTPUT=ai/artifacts/epic-4.2/issue-137/catalog-<utc-id>.json \
  make product-baseline-preflight BASELINE_ENV=qa BASELINE_PRODUCT_DB_TRANSPORT=kubectl-exec
```

Use a fresh private output path for each attempt. Start these foreground, loopback-only forwards
in separate terminals and stop each afterward:

```sh
make product-qualification-forward PRODUCT_QUALIFICATION_FORWARD_SERVICE=established PRODUCT_QUALIFICATION_LOCAL_PORT=18081
make product-qualification-forward PRODUCT_QUALIFICATION_FORWARD_SERVICE=prometheus PRODUCT_QUALIFICATION_LOCAL_PORT=19091
```

Obtain the approved QA JWT through normal login. Store only its value in a temporary `0600`
file outside Git, and set `PRODUCT_QUALIFICATION_TOKEN_FILE` to its path; never pass the token
value in argv or record it. Set `PRODUCT_QUALIFICATION_CONTEXT=qa-insurance-hub`,
`PRODUCT_QUALIFICATION_NAMESPACE=qa-svc`, `PRODUCT_QUALIFICATION_PHASE=java`,
`PRODUCT_QUALIFICATION_EXPECTED_REVISION=main@sha1:<applied-main-SHA>`, matching
`PRODUCT_QUALIFICATION_REVISION`, `PRODUCT_QUALIFICATION_IMAGE=<serving-Java-image-ID>`,
`PRODUCT_QUALIFICATION_ESTABLISHED_URL=http://127.0.0.1:18081`, and a new
`PRODUCT_QUALIFICATION_OUTPUT`. The image input is the immutable serving Pod image ID, not a
tag. Run `make product-qualification-established-status`, then
`make product-qualification-established-core`. Core checks the accepted list/CAR/FAI/HSI/TRI
bodies, absent bearer `401`, unknown/lowercase code `404`, exact decimal/presence/nested order,
and actual Java serving attribution. It rejects `200 []` and wrong details. Check accepted
schema/catalog identity again after the phase. Historical preview JSON is not a baseline.

For the Step 7 Java measurement set
`PRODUCT_QUALIFICATION_PROMETHEUS_URL=http://127.0.0.1:19091` and the exact current
`PRODUCT_QUALIFICATION_GATEWAY_POD`, `PRODUCT_QUALIFICATION_PRODUCT_POD` and
`PRODUCT_QUALIFICATION_DATABASE_POD`; select another new output and run
`make product-qualification-established-measure`. The fixed comparison warms for 60 seconds at
2 requests/s and measures 300 seconds at 5 requests/s: 1,500 scheduled correct reads, four
connections maximum, five-second request deadline, per-operation p95 ≤ 500 ms and p99 ≤ 1,000
ms, and gateway/Product CPU and memory ≤ 80% of effective limits. Every required 30-second
resource/route sample, readiness, restart and established-list control must pass. Preserve
failed/partial evidence; a changed candidate or tool needs a fresh Java/Go comparison pair.

## QA Go, restoration and final acceptance

PR2 changes only the mounted QA Product URL to
`http://qa-product-api.qa-svc.svc.cluster.local:80`; PR3 restores the exact Java URL; PR4
selects the same accepted Go candidate again. Prepare PR3's one-line reverse edit before PR2
publication. Do not revert the listener/Service layout, image, selectors, database or reader.
Publish these as three distinct reviewed `main` revisions and observe each Ready Flux revision,
replacement gateway Pods, loaded URL and attributed requests before the next. Use the phase's
`PRODUCT_QUALIFICATION_PHASE`, exact applied revision and serving image ID with
`make product-qualification-established-status` and
`make product-qualification-established-core`. For Go, set
`PRODUCT_QUALIFICATION_COMPARE_TO=<fresh-established-Java-core-JSON>`. PR2 requires core
parity; the full final workload belongs to PR4.

For PR3 record the decision time and remote-`main` publication time separately in UTC. Start
`make product-qualification-established-restore` promptly after publication, supplying exact
Java `PRODUCT_QUALIFICATION_EXPECTED_REVISION`, `PRODUCT_QUALIFICATION_DECIDED_AT`,
`PRODUCT_QUALIFICATION_PUBLISHED_AT`, established forward, token-file path,
`PRODUCT_QUALIFICATION_ATTRIBUTION_FILE` and new output. Query Tempo independently for an
established list read and retain a sanitized source query. The private attribution JSON must
identify `source=Tempo`, `backend=java`, `operation=list`, the exact revision, serving gateway
and Java Pod names, a 32-hex trace ID, a 16-hex gateway client span ID equal to the Java parent
span ID, and `observed_at_utc` after publication. A hand-entered JSON assertion without a
queryable source is insufficient. The verifier requires matching Ready Flux/Pods, Java
attribution and two complete accepted list/four-get rounds at least five seconds apart within
900 seconds of publication. Record publication-to-verification and decision-to-verification
clocks. On failure preserve evidence and use the
[Product deployment recovery procedure](deployment-runbook.md#abort-stop-and-restoration);
restoration is an intermediate rehearsal, not final acceptance.

After PR4's Ready Go revision, repeat status, core, all negative checks and the fixed
measurement. Manually observe Chrome login, catalog/four-details, covers/questions/choices and
dashboard with request attribution outside the measured window. Then run
`make product-qualification-established-observe` with exact Go identities,
`PRODUCT_QUALIFICATION_PROMETHEUS_URL`, current three Pod names, established/token inputs,
new output and private `PRODUCT_QUALIFICATION_SIGNALS_FILE`. It requires 900 complete correct
reads at 1 rps, the same body/resource/route limits and Go signals.

## Signal capture, evidence and cleanup

Use [Grafana Explore](observability-runbook.md#grafana-ui-workflow) over an absolute UTC range
covering the observation and telemetry allowance. Query Prometheus with
`insurance_hub_product_requests_total{service_name="product-service",deployment_environment_name="qa",service_implementation="go"}`;
record a before value within 60 seconds before observation and a larger after value no later
than 45 seconds after it. Query Loki with
`{service_name="product-service",deployment_environment_name="qa",service_implementation="go"} | json`;
select a completion log from the serving Product Pod and copy its trace ID. Query Tempo with
`{ resource.service.name = "product-service" && resource.insurance_hub.service.implementation = "go" && resource.deployment.environment.name = "qa" }`;
open that trace ID and verify a gateway client span whose ID is the Product server's parent.
Record query text/times, selected series/log/trace identifiers and Pod names in sanitized
source evidence. Only then write the private `0600` signal JSON with exact revision, image,
gateway/Product Pods, `observed_at_unix` per signal, metric before/after values and times,
and correlated trace/span IDs. [`observe.py`](../../../../scripts/product_qualification/observe.py)
defines the accepted field names and exact selectors. The schema checker does not query the
backends or prove that an operator-collected assertion is authentic. Keep tokens, raw customer
data and credential-bearing URLs out of the evidence.

Map final AC137-01–09 and S137-01–10 to current source/image/config/fixture, QA and local-dev
serving, workload, telemetry, browser and restoration evidence. Confirm Java fallback,
unchanged schema/catalog and read-only ownership. Stop owned forwards/workers and remove owned
temporary token files. Keep failed attempts and cleanup failures. A failed Go acceptance
requires a reviewed Java URL restoration through Git/Flux and a fresh affected Go acceptance
run before final Go can be claimed.

## Local-dev switch and recovery

Local-dev uses the same Product ports but its own accepted fixture and ordinary smoke checks.
Verify active Kind context/namespaces and target state, build/check the intended Go image,
update the local overlay tag to its actual `git-<12-hex-source>` value, and set the matching
`DEPLOY_IMAGE_REF`. Run the guarded `go-svc-deploy-preflight`, `go-svc-image-load`,
`go-svc-deploy`, `go-svc-deploy-status` and `go-svc-deploy-verify` targets with
`SVC_NAME=product` and exact local-dev `DEPLOY_*` coordinates. Then apply the gateway's
desired Go URL through `make -C k8s svc-deploy SVC_NAME=agent-portal-gateway
ENV_NAME=local-dev` after target preflight. Verify replacement gateway Pods, loaded Go URL and
an attributed request. Smoke-check authenticated list and four accepted details against the
local fixture, absent bearer `401`, unknown code `404`, Chrome login/catalog/one detail,
management probes, and unchanged catalog identity. QA's fixed workload, 900-second observation
and timed restoration do not apply to local-dev.

On failure preserve diagnostics, restore the previous accepted Java URL through the same
gateway overlay and deployment owner, and verify serving Java Pods and accepted reads. Do not
swap Service selectors, delete Java, or mutate the Product table.
