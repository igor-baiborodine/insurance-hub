<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Verify Loki Logs](#verify-loki-logs)
  - [Clean Replacement](#clean-replacement)
    - [Confirm The Destructive Boundary](#confirm-the-destructive-boundary)
    - [Remove The Old Data And Install Loki](#remove-the-old-data-and-install-loki)
  - [Verify The New Baseline](#verify-the-new-baseline)
    - [Verify Release And Readiness](#verify-release-and-readiness)
    - [Verify Direct Ingestion And Query](#verify-direct-ingestion-and-query)
    - [Verify Canary, Alloy, Grafana, And Persistence](#verify-canary-alloy-grafana-and-persistence)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Verify Loki Logs

This runbook covers the one-time QA clean replacement and the ongoing verification of Loki chart
`18.15.1` with Loki `3.7.8`. Run commands from `k8s/`. The expected boundary is context
`qa-insurance-hub`, namespace `qa-monitoring`, release and Service `qa-loki`, PVC
`storage-qa-loki-0`, MinIO tenant `qa-minio-loki`, and bucket `loki-logs`.

## Clean Replacement

The replacement deletes the old Loki release, PVC, and every object in `loki-logs`. It must retain
the MinIO tenant, bucket, credentials, policy, and any unrelated object-store data.

### Confirm The Destructive Boundary

Run every command below and stop if an identifier differs, more than one matching PVC exists, the
tenant contains an unexpected bucket, or another required consumer depends on historical logs.
Do not print Secret data.

```shell
test "$(kubectl config current-context)" = "qa-insurance-hub"
helm --kube-context qa-insurance-hub status qa-loki -n qa-monitoring
kubectl --context qa-insurance-hub -n qa-monitoring get statefulset qa-loki \
  -o custom-columns='NAME:.metadata.name,REPLICAS:.spec.replicas,SERVICE:.spec.serviceName'
kubectl --context qa-insurance-hub -n qa-monitoring get pvc storage-qa-loki-0 \
  -o custom-columns='NAME:.metadata.name,STATUS:.status.phase,VOLUME:.spec.volumeName,STORAGE:.spec.resources.requests.storage,INSTANCE:.metadata.labels.app\.kubernetes\.io/instance'
kubectl --context qa-insurance-hub -n qa-minio-loki get tenant qa-minio-loki
kubectl --context qa-insurance-hub -n qa-monitoring get secret qa-minio-loki-svc-user-creds
kubectl --context qa-insurance-hub get deployment,statefulset,daemonset,configmap -A -o yaml | \
  rg 'qa-loki|loki-logs'
```

In a separate terminal, forward the Loki MinIO API:

```shell
kubectl --context qa-insurance-hub -n qa-minio-loki \
  port-forward svc/qa-minio-loki-hl 19001:9000
```

In the original terminal, create a temporary alias without displaying credentials. Confirm that
`mc ls issue154-loki` lists only the expected `loki-logs` bucket.

```shell
export LOKI_MINIO_ACCESS_KEY="$(kubectl --context qa-insurance-hub -n qa-monitoring \
  get secret qa-minio-loki-svc-user-creds \
  -o jsonpath='{.data.MINIO_SVC_ACCESS_KEY}' | base64 --decode)"
export LOKI_MINIO_SECRET_KEY="$(kubectl --context qa-insurance-hub -n qa-monitoring \
  get secret qa-minio-loki-svc-user-creds \
  -o jsonpath='{.data.MINIO_SVC_SECRET_KEY}' | base64 --decode)"
mc alias set issue154-loki https://127.0.0.1:19001 \
  "$LOKI_MINIO_ACCESS_KEY" "$LOKI_MINIO_SECRET_KEY" --insecure
mc ls issue154-loki --insecure
mc du issue154-loki/loki-logs --insecure
```

### Remove The Old Data And Install Loki

Proceed only after the boundary above is reviewed and accepted. The `mc rm` command empties the
bucket but does not delete it.

```shell
make loki-uninstall
helm --kube-context qa-insurance-hub list -n qa-monitoring --filter '^qa-loki$'
kubectl --context qa-insurance-hub -n qa-monitoring delete pvc storage-qa-loki-0
mc rm --recursive --force issue154-loki/loki-logs --insecure
mc ls issue154-loki/loki-logs --insecure
mc ls issue154-loki --insecure
mc alias remove issue154-loki
unset LOKI_MINIO_ACCESS_KEY LOKI_MINIO_SECRET_KEY
make loki-install
```

Stop the MinIO port-forward after verifying that the empty `loki-logs` bucket and tenant remain.
If installation fails, correct the repository-owned values and rerun `make loki-install` against
the empty/fresh PVC and bucket. Do not restore Loki 3.6 data.

## Verify The New Baseline

### Verify Release And Readiness

```shell
helm --kube-context qa-insurance-hub list -n qa-monitoring --filter '^qa-loki$'
kubectl --context qa-insurance-hub -n qa-monitoring rollout status \
  statefulset/qa-loki --timeout=10m
kubectl --context qa-insurance-hub -n qa-monitoring get pod,svc,pvc \
  -l app.kubernetes.io/instance=qa-loki -o wide
kubectl --context qa-insurance-hub -n qa-monitoring get daemonset qa-loki-canary
```

Expected: chart `18.15.1`, Loki and canary `3.7.8`, one Ready monolithic Loki pod, one fresh 10 GiB
PVC, ClusterIP Services, and Ready canary pods. Observe the workloads for 10 minutes; restart counts
must settle after first readiness and no pod may be `OOMKilled`, evicted, or unschedulable.

Forward the API and check readiness and the effective configuration:

```shell
make loki-ui
curl -sf http://localhost:3100/ready
curl -sf http://localhost:3100/config | \
  rg 'auth_enabled|retention_period|schema|store|object_store'
```

Expected: multi-tenancy disabled, `168h` retention, `v13` TSDB schema, and S3 storage. The rendered
release must have no gateway, cache, embedded MinIO, distributed read/write/backend workloads, or
public Service.

### Verify Direct Ingestion And Query

Keep the Loki port-forward running. Use a unique, sanitized marker and require it to appear within
two minutes.

```shell
export LOKI_PROBE_ID="issue154-loki-$(date -u +%Y%m%dT%H%M%SZ)-$(openssl rand -hex 4)"
LOKI_TS=$(date -u +%s000000000)
curl -fsS -H 'Content-Type: application/json' \
  -X POST http://localhost:3100/loki/api/v1/push \
  --data-raw "{\"streams\":[{\"stream\":{\"app\":\"issue154-direct\",\"environment\":\"qa\"},\"values\":[[\"$LOKI_TS\",\"$LOKI_PROBE_ID\"]]}]}"
LOKI_FOUND=false
for attempt in $(seq 1 12); do
  if curl -fsSG http://localhost:3100/loki/api/v1/query_range \
    --data-urlencode 'query={app="issue154-direct"} |="'"$LOKI_PROBE_ID"'"' \
    --data-urlencode "start=$(date -d '5 minutes ago' +%s)" \
    --data-urlencode "end=$(date +%s)" | jq -e '.data.result | length > 0'; then
    LOKI_FOUND=true
    break
  fi
  sleep 10
done
test "$LOKI_FOUND" = true
```

### Verify Canary, Alloy, Grafana, And Persistence

- Confirm `loki_canary_entries_total` increases and missing, duplicate, and out-of-order counters do
  not grow during the observation window.
- Send a uniquely identified OTLP log to Alloy and query the same marker from Loki within two
  minutes. Confirm Alloy's accepted/sent counters increase without a growing permanent-failure
  counter.
- In Grafana Explore, select the provisioned Loki datasource and query the direct and Alloy probe
  labels.
- Confirm new objects exist in `loki-logs`, then restart Loki and query the saved direct probe:

```shell
kubectl --context qa-insurance-hub -n qa-monitoring delete pod qa-loki-0
kubectl --context qa-insurance-hub -n qa-monitoring wait \
  --for=condition=Ready pod/qa-loki-0 --timeout=10m
```

Restart `make loki-ui` in its terminal if the pod restart ended the port-forward, then query the
saved marker:

```shell
curl -fsSG http://localhost:3100/loki/api/v1/query_range \
  --data-urlencode 'query={app="issue154-direct"} |="'"$LOKI_PROBE_ID"'"' \
  --data-urlencode "start=$(date -d '1 hour ago' +%s)" \
  --data-urlencode "end=$(date +%s)" | jq -e '.data.result | length > 0'
unset LOKI_PROBE_ID LOKI_TS LOKI_FOUND
```

Record the chart and image versions, probe identifier and timestamps, readiness/restart counts,
query result, canary counters, PVC identity, bucket metadata, resources, and any recovery action.
Do not record credentials or unrelated log payloads.
