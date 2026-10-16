<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Verify Tempo Traces](#verify-tempo-traces)
  - [Clean Replacement](#clean-replacement)
    - [Confirm The Destructive Boundary](#confirm-the-destructive-boundary)
    - [Remove The Old Data And Install Tempo](#remove-the-old-data-and-install-tempo)
  - [Verify The New Baseline](#verify-the-new-baseline)
    - [Verify Release, Configuration, And Readiness](#verify-release-configuration-and-readiness)
    - [Verify Direct Ingestion And Query](#verify-direct-ingestion-and-query)
    - [Verify Live-Store, Backend Storage, And Restart Recovery](#verify-live-store-backend-storage-and-restart-recovery)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Verify Tempo Traces

This runbook covers the one-time QA clean replacement and ongoing verification of Tempo chart
`3.2.0` with Tempo `3.1.0`. Run commands from `k8s/`. The expected boundary is context
`qa-insurance-hub`, namespace `qa-monitoring`, release and Service `qa-tempo`, PVC
`storage-qa-tempo-0`, MinIO tenant `qa-minio-tempo`, and bucket `tempo-traces`.

## Clean Replacement

The replacement deletes the Tempo 2.x release, PVC, and every object in `tempo-traces`. It must
retain the MinIO tenant, bucket, credentials, policy, and unrelated object-store data.

### Confirm The Destructive Boundary

Run every command below and stop if an identifier differs, more than one matching PVC exists, the
tenant contains an unexpected bucket, or another required consumer depends on historical traces.
Do not print Secret data.

```shell
test "$(kubectl config current-context)" = "qa-insurance-hub"
helm --kube-context qa-insurance-hub status qa-tempo -n qa-monitoring
kubectl --context qa-insurance-hub -n qa-monitoring get statefulset qa-tempo \
  -o custom-columns='NAME:.metadata.name,REPLICAS:.spec.replicas,SERVICE:.spec.serviceName'
kubectl --context qa-insurance-hub -n qa-monitoring get pvc storage-qa-tempo-0 \
  -o custom-columns='NAME:.metadata.name,STATUS:.status.phase,VOLUME:.spec.volumeName,STORAGE:.spec.resources.requests.storage,INSTANCE:.metadata.labels.app\.kubernetes\.io/instance'
kubectl --context qa-insurance-hub -n qa-minio-tempo get tenant qa-minio-tempo
kubectl --context qa-insurance-hub -n qa-monitoring get secret qa-minio-tempo-svc-user-creds
kubectl --context qa-insurance-hub get deployment,statefulset,daemonset,configmap -A -o yaml | \
  rg 'qa-tempo|tempo-traces'
```

In a separate terminal, forward the Tempo MinIO API:

```shell
kubectl --context qa-insurance-hub -n qa-minio-tempo \
  port-forward svc/qa-minio-tempo-hl 19002:9000
```

In the original terminal, create a temporary alias without displaying credentials. Confirm that
`mc ls issue154-tempo` lists only the expected `tempo-traces` bucket.

```shell
export TEMPO_MINIO_ACCESS_KEY="$(kubectl --context qa-insurance-hub -n qa-monitoring \
  get secret qa-minio-tempo-svc-user-creds \
  -o jsonpath='{.data.MINIO_SVC_ACCESS_KEY}' | base64 --decode)"
export TEMPO_MINIO_SECRET_KEY="$(kubectl --context qa-insurance-hub -n qa-monitoring \
  get secret qa-minio-tempo-svc-user-creds \
  -o jsonpath='{.data.MINIO_SVC_SECRET_KEY}' | base64 --decode)"
mc alias set issue154-tempo https://127.0.0.1:19002 \
  "$TEMPO_MINIO_ACCESS_KEY" "$TEMPO_MINIO_SECRET_KEY" --insecure
mc ls issue154-tempo --insecure
mc du issue154-tempo/tempo-traces --insecure
```

### Remove The Old Data And Install Tempo

Proceed only after the boundary above is reviewed and accepted. The `mc rm` command empties the
bucket but does not delete it.

```shell
make tempo-uninstall
helm --kube-context qa-insurance-hub list -n qa-monitoring --filter '^qa-tempo$'
kubectl --context qa-insurance-hub -n qa-monitoring delete pvc storage-qa-tempo-0
mc rm --recursive --force issue154-tempo/tempo-traces --insecure
mc ls issue154-tempo/tempo-traces --insecure
mc ls issue154-tempo --insecure
mc alias remove issue154-tempo
unset TEMPO_MINIO_ACCESS_KEY TEMPO_MINIO_SECRET_KEY
make tempo-install
```

Stop the MinIO port-forward after verifying that the empty `tempo-traces` bucket and tenant
remain. If installation fails, correct the repository-owned values and rerun `make tempo-install`
against the empty/fresh PVC and bucket. Do not restore Tempo 2.x data.

## Verify The New Baseline

### Verify Release, Configuration, And Readiness

```shell
helm --kube-context qa-insurance-hub list -n qa-monitoring --filter '^qa-tempo$'
kubectl --context qa-insurance-hub -n qa-monitoring rollout status \
  statefulset/qa-tempo --timeout=10m
kubectl --context qa-insurance-hub -n qa-monitoring get pod,svc,pvc \
  -l app.kubernetes.io/instance=qa-tempo -o wide
kubectl --context qa-insurance-hub -n qa-monitoring get configmap tempo \
  -o jsonpath='{.data.tempo\.yaml}' | \
  rg 'backend_scheduler|backend_worker|block_retention|live_store|version: vParquet5|overrides'
```

Expected: chart `3.2.0`, Tempo `3.1.0`, one Ready monolithic pod, one fresh 10 GiB PVC, ClusterIP
Services, scoped `overrides.defaults`, `vParquet5`, and `168h` retention under both backend
scheduler and backend worker. The effective configuration must not contain Tempo 2.x `ingester` or
`compactor` blocks, Kafka, or an enabled metrics-generator.

Observe the workload for 10 minutes. Restart counts must settle after first readiness and the pod
must not be `OOMKilled`, evicted, unschedulable, or continuously saturated at its resource limit.

In separate terminals, forward Tempo's HTTP API and OTLP HTTP receiver:

```shell
make tempo-ui
kubectl --context qa-insurance-hub -n qa-monitoring \
  port-forward svc/qa-tempo 4318:4318
```

```shell
curl -sf http://localhost:3200/ready
curl -sf http://localhost:3200/metrics | \
  rg 'tempo_live_store_ready|tempo_live_store_traces_created_total|tempo_distributor_spans_received_total'
```

### Verify Direct Ingestion And Query

Use a unique sanitized trace ID. Require the trace-by-ID query to succeed within two minutes.

```shell
export TEMPO_TRACE_ID="$(openssl rand -hex 16)"
TEMPO_SPAN_ID="$(openssl rand -hex 8)"
TEMPO_TS=$(date +%s%N)
curl -fsS http://localhost:4318/v1/traces \
  -H 'Content-Type: application/json' \
  -d '{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"issue154-tempo-direct"}}]},"scopeSpans":[{"scope":{"name":"issue154"},"spans":[{"traceId":"'"$TEMPO_TRACE_ID"'","spanId":"'"$TEMPO_SPAN_ID"'","name":"issue154-tempo-direct","kind":"SPAN_KIND_INTERNAL","startTimeUnixNano":"'"$TEMPO_TS"'","endTimeUnixNano":"'"$((TEMPO_TS+5000000))"'"}]}]}]}'
TEMPO_FOUND=false
for attempt in $(seq 1 12); do
  if curl -fsS "http://localhost:3200/api/traces/$TEMPO_TRACE_ID" | \
    jq -e '.. | objects | select(.key? == "service.name") | .value.stringValue == "issue154-tempo-direct"'; then
    TEMPO_FOUND=true
    break
  fi
  sleep 10
done
test "$TEMPO_FOUND" = true
```

Run the equivalent fresh OTLP trace through `qa-alloy:4318` and confirm it is queryable in Tempo
within two minutes. Check Alloy's accepted/sent/failure counters and query both probes in Grafana
Explore with the provisioned Tempo datasource.

### Verify Live-Store, Backend Storage, And Restart Recovery

Tempo 3 uses the live-store for recent traces and the backend scheduler/worker for compaction and
retention. Do not use the removed Tempo 2.x ingester/compactor checks.

1. Confirm `tempo_live_store_ready` is healthy and
   `tempo_live_store_traces_created_total` increased for the probe.
2. Wait for the trace to pass the live-store WAL/block flush point. The Tempo `3.1.0` default
   `max_block_duration` is 30 seconds; require new `tempo-traces` object metadata within 10 minutes.
3. Restart the pod, wait for readiness, and query the same trace ID again:

```shell
kubectl --context qa-insurance-hub -n qa-monitoring delete pod qa-tempo-0
kubectl --context qa-insurance-hub -n qa-monitoring wait \
  --for=condition=Ready pod/qa-tempo-0 --timeout=10m
```

Restart `make tempo-ui` in its terminal if the pod restart ended the port-forward, then query the
saved trace:

```shell
curl -fsS "http://localhost:3200/api/traces/$TEMPO_TRACE_ID" | \
  jq -e '.. | objects | select(.key? == "service.name") | .value.stringValue == "issue154-tempo-direct"'
unset TEMPO_TRACE_ID TEMPO_SPAN_ID TEMPO_TS TEMPO_FOUND
```

Record the chart and image versions, probe identifiers and timestamps, query results, live-store
and backend health, readiness/restart counts, PVC identity, bucket metadata, resources, and any
recovery action. Do not record credentials or unrelated trace payloads.
