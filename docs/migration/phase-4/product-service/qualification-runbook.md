<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [QA Product qualification runbook](#qa-product-qualification-runbook)
  - [Before activation](#before-activation)
  - [Local forwards and protected credentials](#local-forwards-and-protected-credentials)
  - [Java and Go core checks](#java-and-go-core-checks)
  - [Browser and supporting signals](#browser-and-supporting-signals)
  - [Bounded measurement](#bounded-measurement)
  - [Route sequence, restoration and cleanup](#route-sequence-restoration-and-cleanup)
  - [Tooling checks and limits](#tooling-checks-and-limits)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# QA Product qualification runbook

Issue #136 qualifies the existing Go Product read path in QA through an isolated Vue and Java
gateway preview. The established QA gateway continues to select Java. Execute every command below
from the repository root unless it says `make -C services/product-service`. Retain run JSON and
operator observations under the ignored `ai/artifacts/epic-4.2/issue-136/` directory. The
reviewed Git/Flux revision controls preview activation, Go selection, Java restoration and preview
removal; none of these targets applies Kubernetes resources or publishes Git changes.

## Before activation

Use the accepted four-code QA catalog and current QA access. Run the ticket's QA fixture,
inventory/catalog and Go deployment preflights; inspect Flux, Pod/image/config identities,
resource headroom, reader grants and observability readiness. Verify the current Chrome version
and host OS, and ensure the normal QA Auth token will remain valid for each complete phase. Do not
recapture or accept fixtures, reload the catalog, change credentials, run builds during measurement,
or overlap browser work with a measured phase. Record source and immutable application image
digests, Product database identity, schema/data hashes, relevant resource limits and current
monitoring identities in the local run manifest.

Run `make product-qualification-render` before each preview source revision. It checks the six
rendered objects, distinct selectors, Java/Go URL allowlist and generated gateway ConfigMap.
The Java-default preview is introduced by PR1's QA aggregate reference. After its reviewed merge,
wait for the exact `main@sha1:<40-hex>` revision in the QA GitRepository and `qa-svc`
Kustomization. `make product-qualification-status` requires both Ready at that revision, a
complete one-replica gateway rollout, matching generated configuration and disjoint preview and
established gateway endpoints. Inspect the effective Pod and frontend configuration as well; a
source render alone cannot prove the loaded Vue image uses the configured API proxy.

## Local forwards and protected credentials

Each forward runs in its own foreground terminal and binds only `127.0.0.1`. Stop each owned
terminal with Ctrl-C after the run. Restart the preview forward after a Vue Pod replacement.
These commands contact QA but do not change GitOps resources:

```sh
make product-qualification-forward PRODUCT_QUALIFICATION_FORWARD_SERVICE=preview PRODUCT_QUALIFICATION_LOCAL_PORT=18080
make product-qualification-forward PRODUCT_QUALIFICATION_FORWARD_SERVICE=established PRODUCT_QUALIFICATION_LOCAL_PORT=18081
make product-qualification-forward PRODUCT_QUALIFICATION_FORWARD_SERVICE=grpc PRODUCT_QUALIFICATION_LOCAL_PORT=19090
make product-qualification-forward PRODUCT_QUALIFICATION_FORWARD_SERVICE=prometheus PRODUCT_QUALIFICATION_LOCAL_PORT=19091
```

Use a short-lived JWT issued by the existing QA Auth login for the approved QA agent. Store only
the JWT in a temporary file outside the repository, create it under `umask 077`, verify mode
`0600` and expiry, and pass its path as `PRODUCT_QUALIFICATION_TOKEN_FILE`. Never put the token
value on a command line, in a shell history, run artifact or Git file. Remove the original file
after the run (including on failure); the commands do not own it. The browser signs in normally
through the preview origin. The direct gRPC client is anonymous by the established Product
boundary and requires no JWT. The separate [security reproduction guide](security-baseline.md#reproduction)
describes the accepted live token-file boundary.

## Java and Go core checks

Set these Make variables in the operator shell for one phase. Fill `PRODUCT_QUALIFICATION_REVISION`
with that phase's observed applied Flux revision and `PRODUCT_QUALIFICATION_IMAGE` with its active
Product Pod's immutable image ID. Set `PRODUCT_QUALIFICATION_EXPECTED_REVISION` to the exact
desired `main@sha1:<40-hex>` revision. Use a new output file for every attempt. A failed output
is retained; do not overwrite it or select a favorable rerun. Recheck catalog/schema identities
before and after each phase with the existing baseline preflight.

```sh
export PRODUCT_QUALIFICATION_CONTEXT=qa-insurance-hub
export PRODUCT_QUALIFICATION_NAMESPACE=qa-svc
export PRODUCT_QUALIFICATION_PREVIEW_URL=http://127.0.0.1:18080
export PRODUCT_QUALIFICATION_ESTABLISHED_URL=http://127.0.0.1:18081
export PRODUCT_QUALIFICATION_PROMETHEUS_URL=http://127.0.0.1:19091
export PRODUCT_QUALIFICATION_GRPC_ADDRESS=127.0.0.1:19090
export PRODUCT_QUALIFICATION_TOKEN_FILE=/path/to/protected-qa-token
export PRODUCT_QUALIFICATION_PHASE=java
export PRODUCT_QUALIFICATION_EXPECTED_REVISION='main@sha1:<reviewed-java-sha>'
export PRODUCT_QUALIFICATION_REVISION="$PRODUCT_QUALIFICATION_EXPECTED_REVISION"
export PRODUCT_QUALIFICATION_IMAGE='<effective-java-product-image-id>'
export PRODUCT_QUALIFICATION_OUTPUT='ai/artifacts/epic-4.2/issue-136/java-core-<utc-run-id>.json'
make product-qualification-status
make product-qualification-core
```

The core checker verifies the authenticated preview list and four gets against accepted QA
gateway bodies, unique codes, decimal spelling, optional-field presence and nested order. It
also checks absent and differently cased codes for safe 404 JSON and missing credentials for 401.
It records the raw successful JSON strings and phase identity without the token. The Go phase
uses the same command with `PRODUCT_QUALIFICATION_PHASE=go`, its reviewed Go revision/image,
a new output path, and
`PRODUCT_QUALIFICATION_COMPARE_TO=<Java-core-output-path>` for direct Java/Go parity.
The direct Go gRPC check is run only after Go selection:

```sh
GOTOOLCHAIN=local make -C services/product-service build-qualification-client \
  GO=go QUALIFICATION_CLIENT_OUTPUT=/tmp/issue-136-qualification-client
make product-qualification-grpc PRODUCT_QUALIFICATION_GRPC_CLIENT=/tmp/issue-136-qualification-client
```

The gRPC client uses the accepted direct QA bodies and checks list, four gets, absent/case
`NotFound`, and empty-code `InvalidArgument` over the forwarded QA Go port. It does not use
reflection. Build it with the pinned Go 1.27.3 toolchain and remove the local binary afterward.

## Browser and supporting signals

Open `http://127.0.0.1:18080` in the confirmed Google Chrome version. Sign in normally. In the
Java phase record the preview catalog and product-detail baseline; in the Go phase repeat catalog,
details, covers, questions and choices, and one dashboard product view. Record the visible values,
interaction result, browser version/OS and the loaded network destination or effective Nginx
configuration. Preserve the existing date-question rendering limitation. Do not submit pricing,
offer or policy writes. Perform browser checks before or after, never during, a measured phase.

During successful Go reads, query one representative Product request log, confirm Product request
metrics advance, and inspect one valid gateway-to-Go trace with parent/trace correlation through
the existing Alloy/Prometheus/Loki/Tempo path. Use the [observability runbook](observability-runbook.md)
for the current QA query endpoints and labels. Capture sanitized identifiers and queryable time
windows, not tokens, request bodies or raw definitions. These observations prove supporting happy
paths only.

## Bounded measurement

Run Java first and Go second after each corresponding route, core checks and identity checks
pass. Set `PRODUCT_QUALIFICATION_GATEWAY_POD`, `PRODUCT_QUALIFICATION_PRODUCT_POD` and
`PRODUCT_QUALIFICATION_DATABASE_POD` to the exact current preview gateway, selected Product and
Product PostgreSQL primary Pod names. The observer reads their effective CPU/memory limits and
restarts, queries 60-second CPU and memory windows from local Prometheus every 30 seconds, checks
database readiness and connection count, and checks the established Java gateway catalog every
30 seconds. The database CPU/memory samples are contextual and recorded as unavailable if the
current metrics do not expose them; missing gateway/Product resource samples fail the phase.

```sh
export PRODUCT_QUALIFICATION_GATEWAY_POD='<ready-preview-gateway-pod>'
export PRODUCT_QUALIFICATION_PRODUCT_POD='<ready-active-product-pod>'
export PRODUCT_QUALIFICATION_DATABASE_POD='<ready-product-postgresql-primary-pod>'
export PRODUCT_QUALIFICATION_OUTPUT='ai/artifacts/epic-4.2/issue-136/java-measured-<utc-run-id>.json'
make product-qualification-measure
```

The fixed profile is 60 seconds at 2 requests/s warm-up, then 300 seconds at 5 requests/s: 120
warm-up and exactly 1,500 scheduled measured requests. It rotates list/CAR/FAI/HSI/TRI equally,
uses at most four persistent worker connections, a five-second client deadline, no driver retry,
and checks complete response bodies outside transport timing. Each measured group needs 300
correct HTTP 200 responses. The reported median, p95 and p99 use nearest rank on sorted samples;
each p95 must be at most 500 ms and p99 at most 1,000 ms. The 300-sample p99 is only a pilot
signal. Both preview gateway and active Product 60-second CPU/memory averages must remain at or
below 80% of the effective limit. Failed, unserved, timed-out or incorrect requests, missing
observations, restart/readiness loss, established-route control failure or resource breach abort
the phase and preserve partial JSON evidence. Stop the run with Ctrl-C if unrelated activity,
catalog drift or a rollout invalidates the comparison. A failure blocks Go qualification until
investigated; do not loosen the profile or choose a favorable rerun. This is a deployed
configuration comparison, with no relative speedup gate or capacity/SLO claim.

## Route sequence, restoration and cleanup

PR2 changes only the preview Product URL to the Go Service. After its reviewed merge, wait for
the new Ready Flux revision and complete preview gateway rollout, then repeat status, catalog
identity, core, direct gRPC, Chrome, measured and supporting-signal checks. Prepare the PR3 Java
URL patch and verification procedure before PR2 activation. On a Go failure, preserve failed
evidence and publish the reviewed Java restoration without deleting the preview first.

For PR3, record the rollback decision time separately. After the reviewed Java restoration
revision reaches remote `main`, record its UTC publication time and revision, then run:

```sh
export PRODUCT_QUALIFICATION_EXPECTED_REVISION='main@sha1:<reviewed-restore-sha>'
export PRODUCT_QUALIFICATION_PUBLISHED_AT='<remote-main-publication-utc-iso8601>'
export PRODUCT_QUALIFICATION_OUTPUT='ai/artifacts/epic-4.2/issue-136/java-restoration-<utc-run-id>.json'
make product-qualification-restore
```

The verifier polls normal Flux source/application readiness and the Java-generated ConfigMap,
requires one serving preview gateway Pod and disjoint established endpoints, and performs two
accepted preview list/get rounds five seconds apart plus established Java list controls. It
records remote-publication-to-verification seconds; at most 900 seconds passes. It continues
recovery polling for up to 30 minutes if the acceptance clock expires, marking the bound failed.
Record decision-to-verification time and any use of the owning `make flux-reconcile` command
separately. Do not patch live ConfigMaps, suspend Flux or treat preview deletion as restoration.

PR4 removes only the QA aggregate's preview reference after verified Java restoration. After its
reviewed merge and Ready Flux revision, verify both preview Deployments, Services and generated
ConfigMaps are pruned while established Java routing, Product Pods and catalog remain unchanged.
Stop the owned forwards and securely delete the temporary token file. Preserve sanitized success
and failure records locally; include candidate identities, comparison, restoration timing,
limitations and #137 prerequisites in the handoff. #137 owns any actual route cutover.

## Tooling checks and limits

Before publication, run `make product-qualification-test` and the pinned Python format/lint and
Product Go format/lint/build targets over the changed files. `make product-qualification-render`
is static evidence; fake-adapter tests do not prove QA transport, Chrome behavior, telemetry or
restoration. Each live phase needs its own observed source/image/config/catalog identity. A
material tooling, image, data or resource change invalidates the affected Java/Go comparison pair.
